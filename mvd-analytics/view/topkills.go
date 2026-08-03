package view

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Top kills: the hardest kill BURSTS of the match — for each enemy kill, the
// contiguous run of killing-weapon hits the killer landed on that victim
// leading up to it, summed in the requested damage family.
//
// This is the third segmentation over the same primitive, and the only one that
// is not an interval reduction: top windows segment by fixed-length TIME
// (TopWindows), lives by SPAWN→DEATH (Lives), and this by the kill itself. A
// burst row is a small bespoke backward walk over the damage log rather than a
// stats block, so it fills no IntervalStats — the numbers a highlight row needs
// are the burst's own.
//
// # The burst is the KILLING WEAPON's run, and that is the endpoint's meaning
//
// Only hits with the frag's own weapon join the run. Measured over 1,866 enemy
// kills (8 golden-cache demos), on ~8% of kills that UNDERSTATES what produced
// the kill — a rocket softens, a shotgun finishes, and the row reads
// `weapon: sg, damage: 16` for a kill that took 250 across weapons. That is
// deliberate and is what the endpoint answers: "how hard was this burst with
// this weapon". "What did the kill take across weapons" stays derivable from
// /damage, and nothing in this shape blocks adding a mixed-weapon mode later.
// A different-weapon hit landing inside the run neither joins it nor breaks it.
//
// # CAPTURE generously; the client narrows with maxGapMs
//
// GapMs is a CAPTURE gap, not a display one: truncation is unrecoverable
// downstream while over-merge is filterable, so the default is generous (3000)
// and the row carries what makes narrowing exact. Same-weapon inter-hit cadence
// inside real bursts is p95 2315 ms for rl and 1876 for sg, so a baked 1200 ms
// gap truncated 11% of rl and 23% of sg bursts — worst measured, a 291-damage
// triple-rocket kill reported as 2 (the tail splash of the killing rocket).
//
// MaxGapMs is what makes client-side narrowing EXACT and SpanMs is not:
// keeping exactly the rows with maxGapMs <= g gives every kept row its gap-g
// value verbatim, because dropping hits from a run only widens gaps, so a run
// whose internal gaps are all <= g walks identically at g and at the capture
// gap. A run with maxGapMs > g is dropped, not truncated — the true gap-g
// walk would have reported its shorter suffix — so the narrowed list is the
// gap-g ranking restricted to intact runs; GapMs on the request is the exact
// walk when the remainders matter. A span heuristic cannot express even the
// kept-row rule (21 of the 24 over-merged rl bursts in the corpus pass a
// span <= 2×gap rule). SpanMs is carried because it is the DISPLAY figure
// ("291 dmg in 1.7 s").
//
// # What is not ranked, and what deliberately is
//
// POSITIONAL KILLS PRODUCE NO ROW. A telefrag, stomp or squish carries no
// damage event (result/damage.go keeps them out of the log entirely), so there
// is no run to sum and no honest damage figure to rank; 13 of 1,879 kills in the
// measured corpus. They are absent from the ranking, not from the match — the
// frag log and /damage's Telefrags/Stomps still carry them — and the endpoint
// documentation says so rather than letting a caller discover the hole.
//
// KILLS BY AN ALREADY-DEAD KILLER STAY IN. The walk consults the VICTIM's
// liveness and never the killer's: a rocket in flight when its shooter died
// still kills, and "spawnluck & went-down-swinging" is exactly the highlight
// this endpoint exists to surface. Measured 38/1,866 (2%), overwhelmingly
// posthumous rockets and mutual frags. Nobody should "fix" this.
const (
	// defaultTopKillGapMs is the CAPTURE gap; see the file comment for why it
	// is generous rather than per-weapon tight.
	defaultTopKillGapMs = 3000
	// maxTopKillGapMs bounds the walk. Beyond ~5 s a "burst" is two fights.
	maxTopKillGapMs = 5000

	// defaultTopKillContestedMs is the window returnDamage sums over. The
	// server owns the window; the client owns the threshold that turns the
	// value into "contested" (measured 63/37 contested/passive at 4000, so it
	// discriminates rather than tagging everything).
	defaultTopKillContestedMs = 4000
	maxTopKillContestedMs     = 30000

	defaultTopKillLimit = 20
	topKillMaxLimit     = 200
)

// TopKillsOptions narrows and shapes a TopKills query. Empty fields mean
// "default"; From/To are match-relative int32 ms (0 disables that bound),
// matching FragOptions / DamageOptions / TopWindowsOptions.
type TopKillsOptions struct {
	// GapMs is the capture gap of the backward walk: a hit joins the run while
	// it lands within GapMs of the run's earliest hit so far. <=0 → 3000,
	// clamped to 5000. The resolved value is echoed on the response, so a
	// clamped query still says what it computed.
	GapMs int32
	// ContestedMs is the window ReturnDamage sums the victim's damage back
	// over. <=0 → 4000, clamped to 30000; echoed like GapMs.
	ContestedMs int32
	// Limit caps the returned rows: 0 → 20, <0 → uncapped, above 200 clamped.
	// Those are /top-windows' shipped semantics verbatim (an explicit 0 is a 400
	// at the HTTP layer, which is where an omitted-MCP-integer 0 is told apart
	// from a deliberate one; the clamp here is defence in depth for in-process
	// callers).
	Limit int
	// Players restricts to these KILLERS (case-sensitive), matching the row's
	// `killer` field. A victim-side filter would be a different question and is
	// not offered rather than being silently one or the other.
	Players []string
	// Weapons restricts the KILLING weapon — the burst's own weapon, since the
	// two are the same thing here (case-insensitive, alias-expanded through the
	// shared weaponFilterSet).
	Weapons []string
	// MinDamage drops bursts below it. 0 keeps everything, which is why a plain
	// int suffices where TopWindowsOptions.Min needed a pointer: a burst's
	// damage is non-negative, so "0" and "no filter" select the same rows.
	MinDamage int
	From      int32 // earliest kill time, int32 ms (0 = no bound)
	To        int32 // latest kill time, int32 ms (0 = no bound)
	// Dmg is the damage family: "raw" | "bounded". It applies to the burst
	// damage AND to ReturnDamage, so one response is one family.
	//
	// The VIEW default is raw and the REST default is bounded — mvd-api
	// substitutes "bounded" for an unset dmg exactly as it does on /damage,
	// /lives and /top-windows. An in-process caller (WASM, qw-analyze) that
	// leaves this empty therefore gets a different family than the same query
	// over HTTP.
	Dmg string
}

// TopKillsView is the response: a flat list of bursts, hardest first.
type TopKillsView struct {
	TimeUnit TimeUnit `json:"timeUnit,omitempty"`

	// Dmg and BoundedMode name the family every row's damage and returnDamage
	// was summed in, echoed exactly as /damage, /lives and /top-windows echo
	// them.
	Dmg         string `json:"dmg,omitempty"`
	BoundedMode string `json:"boundedMode,omitempty"`

	// GapMs and ContestedMs are the RESOLVED parameters — defaulted and clamped
	// — not the caller's input, because they are what the numbers below mean.
	GapMs       int32 `json:"gapMs"`
	ContestedMs int32 `json:"contestedMs"`
	Limit       int   `json:"limit"`

	// Measured says which sources this demo carries; see MeasuredSources. Not
	// omitempty — an absent marker would be the very ambiguity it removes.
	Measured MeasuredSources `json:"measured"`

	Kills []TopKill `json:"kills"`
}

// TopKill is one kill burst. Times are match-relative int32 ms.
type TopKill struct {
	Rank   int    `json:"rank"` // 1-based over the returned list
	Killer string `json:"killer"`
	Victim string `json:"victim"`
	Team   string `json:"team,omitempty"` // the KILLER's team
	Time   int32  `json:"time"`           // the kill instant; also the run's last hit
	Weapon string `json:"weapon"`         // killing weapon = the burst's weapon

	// Damage is the run's summed damage in the envelope's family, and the value
	// that ranked this row. Hits is how many damage events the run holds.
	Damage int `json:"damage"`
	Hits   int `json:"hits"`

	// SpanMs is Time - the run's first hit; MaxGapMs the largest gap between
	// consecutive hits in it, 0 when Hits == 1. MaxGapMs is the exact filter for
	// narrowing to a tighter gap client-side — see the file comment.
	SpanMs   int32 `json:"spanMs"`
	MaxGapMs int32 `json:"maxGapMs"`

	// VictimWep is the victim's weapon class (sg|mid|lg|rl|both) at the killing
	// hit, straight from that damage event — the same field /damage reports.
	// Empty when the wire carried none.
	VictimWep string `json:"victimWep,omitempty"`

	// ReturnDamage is what the victim dealt BACK to this killer over the
	// contested window before the kill: any weapon, enemy hits only, same
	// family. It is a value and not a boolean on purpose — the client owns the
	// threshold that calls a kill contested.
	ReturnDamage int `json:"returnDamage"`
}

// TopKillsAvailable reports whether the demo can answer a top-kills query. It
// needs three sources, and the third is the one worth spelling out:
//
//   - the frag log, which anchors every burst;
//   - the damage log, which IS the burst;
//   - measurable liveness, because the backward walk is clipped by the victim's
//     current life start.
//
// The liveness gate is not pedantry. At the 3000 ms capture gap the walk
// crosses the victim's previous death on 74/1,866 measured kills (4%) — the
// capture gap dwarfs a respawn delay — and without the clip those bursts absorb
// the victim's PREVIOUS life (worst measured: 355 reported where the
// current life took 62). Because the list is ranked BY damage, the contaminated rows
// are exactly the rows that float to the top, and a consumer holding only this
// response cannot tell them from real ones. That is the "would actively mislead
// a consumer that cannot itself disambiguate" case, so an unmeasurable-liveness
// demo gets the same 422 /lives gives it rather than a plausible-looking list.
func TopKillsAvailable(r *result.Result) error {
	if r == nil || r.Frags == nil || r.Damage == nil {
		return ErrUnavailable
	}
	if !livenessMeasured(r) {
		return ErrUnavailable
	}
	return nil
}

// TopKills returns the match's hardest kill bursts, ranked by burst damage.
//
// Returns ErrUnavailable when the demo lacks frags, damage or measurable
// liveness (TopKillsAvailable is the same gate), ErrBoundedUnavailable for an
// explicit dmg=bounded on a demo with no bounded family, and ErrInvalidFilter
// for a bad dmg or weapon token — the same contract as TopWindows and Lives.
func TopKills(r *result.Result, opts TopKillsOptions) (*TopKillsView, error) {
	if err := TopKillsAvailable(r); err != nil {
		return nil, err
	}
	fam, err := damageFamily(r, opts.Dmg)
	if err != nil {
		return nil, err
	}
	// The vocabulary is the INTERSECTION of what can anchor a burst, not the
	// full frag vocabulary: a row needs an enemy frag whose weapon also spells
	// a damage event. That excludes positional kills (tele/stomp/squish — no
	// damage events at all), environmental and bookkeeping causes
	// (fall/lava/slime/water/world/suicide/teamkill — the frag rows are
	// excluded or the hits IsEnv), and hook/rail (the damage log spells both
	// "unknown", so they can never anchor). Accepting those tokens would be a
	// valid-param filter that structurally selects nothing — the silent-empty
	// trap the v65 water/drown incident closed; /top-windows narrows per
	// metric on the same grounds (validateTopWindowWeapons).
	if err := validateWeapons(opts.Weapons, topKillWeaponVocab); err != nil {
		return nil, err
	}

	gapMs := clampTopKillMs(opts.GapMs, defaultTopKillGapMs, maxTopKillGapMs)
	contestedMs := clampTopKillMs(opts.ContestedMs, defaultTopKillContestedMs, maxTopKillContestedMs)
	limit := opts.Limit
	if limit == 0 {
		limit = defaultTopKillLimit
	}
	if limit > topKillMaxLimit {
		limit = topKillMaxLimit
	}

	hits := indexHitsByPair(r.Damage)
	lives := victimLives(r)
	pf := newPlayerFilter(opts.Players)
	match := weaponMatcher(opts.Weapons)
	teamOf := defaultNameToTeam(r)

	rows := []TopKill{}
	for i := range r.Frags.Frags {
		f := &r.Frags.Frags[i]
		// ENEMY kills only, on exactly view.TopWindows' terms so the two
		// endpoints agree about what a kill is.
		if f.IsSuicide || f.IsTeamKill || isGenericName(f.Killer) {
			continue
		}
		if !pf.accepts(f.Killer) || !match(f.Weapon) {
			continue
		}
		if opts.From > 0 && f.Time < opts.From {
			continue
		}
		if opts.To > 0 && f.Time > opts.To {
			continue
		}
		b, ok := killBurstFor(hits[damagePair{f.Killer, f.Victim}], f, lives.startAt(f.Victim, f.Time), gapMs, fam)
		if !ok {
			// No killing-weapon hit at the frag instant: a positional kill, the
			// only shape that reaches here. Every one of the 1,866 measured
			// enemy kills has a hit at EXACTLY the frag timestamp (both are
			// stamped from the same MVD frame), which is why the anchor needs no
			// tolerance window and why "a kill with an empty burst" is not a
			// state this endpoint has to describe.
			continue
		}
		if b.damage < opts.MinDamage {
			continue
		}
		rows = append(rows, TopKill{
			Killer:       f.Killer,
			Victim:       f.Victim,
			Team:         teamOf[baseName(f.Killer)],
			Time:         f.Time,
			Weapon:       f.Weapon,
			Damage:       b.damage,
			Hits:         b.hits,
			SpanMs:       b.spanMs,
			MaxGapMs:     b.maxGapMs,
			VictimWep:    b.victimWep,
			ReturnDamage: returnDamage(hits[damagePair{f.Victim, f.Killer}], f.Time, contestedMs, fam),
		})
	}

	sortTopKills(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	for i := range rows {
		rows[i].Rank = i + 1
	}

	return &TopKillsView{
		Dmg:         fam,
		BoundedMode: boundedModeOf(r),
		GapMs:       gapMs,
		ContestedMs: contestedMs,
		Limit:       limit,
		// The measured block comes from the shared builder rather than from a
		// second implementation of the same six predicates — the marker and the
		// sibling responses cannot drift apart if there is only one of it.
		Measured: newStatsBuilder(r, fam).measured(),
		Kills:    rows,
	}, nil
}

// clampTopKillMs resolves a duration parameter: <=0 takes the default, anything
// above max is clamped. Both parameters are echoed on the response after this
// runs, so a clamped query is told what it got. Rejecting an out-of-range value
// is the HTTP layer's call (v59 "rejected, no longer silently clamped"); this
// is the in-process floor, matching TopWindows' treatment of WindowMs/Limit.
func clampTopKillMs(v, def, max int32) int32 {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// topKillWeaponVocab is the killing weapons that can actually anchor a burst:
// frag-log tokens whose kills both survive the enemy-kill filter and carry
// same-spelling damage events. See the validation comment in TopKills for what
// each exclusion rests on; "unknown" stays because obituary-unmatched kills
// with real damage events do occur.
var topKillWeaponVocab = []string{
	"rl", "lg", "gl", "ssg", "sng", "ng", "sg", "axe", "unknown",
}

// damagePair keys the hit index by direction: attacker → victim, never folded,
// because both directions are read for different questions (the burst one way,
// the return damage the other).
type damagePair struct{ attacker, victim string }

// indexHitsByPair groups the damage log by attacker→victim pair, time-ordered.
//
// Only ENEMY weapon hits are indexed — self, team and environmental events can
// never be part of a kill burst on an enemy, and dropping them once here means
// neither the burst walk nor the return-damage sum has to remember to.
// Telefrags and stomps are not in Events at all (result/damage.go), which is
// exactly why a positional kill produces no row.
func indexHitsByPair(d *result.DamageResult) map[damagePair][]*result.DamageEntry {
	idx := map[damagePair][]*result.DamageEntry{}
	for i := range d.Events {
		e := &d.Events[i]
		if e.IsSelf || e.IsTeam || e.IsEnv {
			continue
		}
		k := damagePair{e.Attacker, e.Victim}
		idx[k] = append(idx[k], e)
	}
	// The stored log is time-ordered, so this is a no-op on every real demo.
	// It is here because the walk below binary-searches these slices and the
	// ordering is a property of a STORED field this package does not produce.
	for k := range idx {
		v := idx[k]
		sort.SliceStable(v, func(a, b int) bool { return v[a].Time < v[b].Time })
	}
	return idx
}

// killBurst is one kill's run of killing-weapon hits, already summed.
type killBurst struct {
	damage    int
	hits      int
	spanMs    int32
	maxGapMs  int32
	victimWep string
}

// killBurstFor walks backward from the killing hit and returns the run.
//
// ok is false when no hit with the frag's weapon lands at exactly the frag
// instant — the positional-kill case, see TopKills.
//
// lifeStart clips the walk BELOW: hits before the victim's current life started
// belong to a life the killer already ended, and the capture gap is wide enough
// to reach across a respawn routinely (TopKillsAvailable). The bound is
// inclusive because PlayerStream.Alive intervals are half-open [Start, End) —
// a hit landing exactly on the spawn instant belongs to the new life.
//
// The run is defined over killing-weapon hits ALONE: a hit with another weapon
// inside the span is skipped, neither joining the run nor ending it. Weapon
// tokens are compared case-insensitively against the frag log's spelling; the
// one place the two logs disagree (water/drown) is an environmental cause that
// never produces an enemy kill. NOTE hook/rail: on a mod where those kills
// carry damage events, the damage log spells both "unknown" while the frag
// log names them — such kills cannot anchor and produce no row even
// unfiltered. Corpus-invisible today (no hook/rail server in the cache), but
// the asymmetry is why the tokens are absent from topKillWeaponVocab.
func killBurstFor(hits []*result.DamageEntry, f *result.FragEntry, lifeStart, gapMs int32, fam string) (killBurst, bool) {
	// Everything at or before the kill; the log is time-ordered.
	hi := sort.Search(len(hits), func(i int) bool { return hits[i].Time > f.Time })

	var b killBurst
	var earliest int32
	anchored := false
	for i := hi - 1; i >= 0; i-- {
		e := hits[i]
		if e.Time < lifeStart {
			break
		}
		if !strings.EqualFold(e.Weapon, f.Weapon) {
			continue
		}
		if !anchored {
			if e.Time != f.Time {
				return killBurst{}, false
			}
			anchored = true
			earliest = e.Time
		} else {
			gap := earliest - e.Time
			if gap > gapMs {
				break
			}
			if gap > b.maxGapMs {
				b.maxGapMs = gap
			}
			earliest = e.Time
		}
		b.damage += damageValue(e, fam)
		b.hits++
		// The victim's class as of the KILLING hit, and only it: an earlier hit
		// in the run describes an earlier inventory, and reporting that as the
		// class the victim died holding would be a quiet fabrication. A frame
		// can carry several hits at the kill instant (splash onto a direct);
		// walking backward, the first non-empty of them is the latest.
		if e.Time == f.Time && b.victimWep == "" {
			b.victimWep = e.VictimWep
		}
	}
	if !anchored {
		return killBurst{}, false
	}
	b.spanMs = f.Time - earliest
	return b, true
}

// returnDamage sums what the victim dealt back to the killer over the contested
// window — any weapon, enemy hits only (the index holds nothing else), same
// family as the burst.
//
// The window is [t-contestedMs, t], CLOSED at both ends, matching every other
// time window this package serves (/frags, /damage and the interval stats block
// all include time == to). The closed upper edge is load-bearing rather than
// conventional: a mutual frag lands both hits on the same instant, and that is
// the most contested kill there is.
//
// The killer's own liveness is never consulted here either — a killer who was
// already dead can still have been taking damage right up to the kill.
func returnDamage(hits []*result.DamageEntry, t, contestedMs int32, fam string) int {
	lo := int64(t) - int64(contestedMs)
	sum := 0
	for i := sort.Search(len(hits), func(i int) bool { return hits[i].Time > t }) - 1; i >= 0; i-- {
		if int64(hits[i].Time) < lo {
			break
		}
		sum += damageValue(hits[i], fam)
	}
	return sum
}

// victimLifeIndex is each player's lives, for the one question the burst walk
// asks of them: where did the life that this kill ended begin. Streams whose
// key is a name#slot collision suffix are additionally indexed under the bare
// name as CANDIDATES, resolved per kill by resolveCollision.
type victimLifeIndex struct {
	byName map[string][]result.Interval
	// collided maps a bare display name to the alive sets of every #slot
	// stream that shares it — the frag log names the bare name, so a direct
	// lookup misses and the kill has to pick its stream another way.
	collided map[string][][]result.Interval
}

func victimLives(r *result.Result) victimLifeIndex {
	idx := victimLifeIndex{byName: map[string][]result.Interval{}, collided: map[string][][]result.Interval{}}
	if r.Streams == nil {
		return idx
	}
	for i := range r.Streams.Players {
		p := &r.Streams.Players[i]
		if p.Alive == nil {
			continue
		}
		idx.byName[p.Name] = p.Alive
		if base := baseName(p.Name); base != p.Name {
			idx.collided[base] = append(idx.collided[base], p.Alive)
		}
	}
	return idx
}

// resolveCollision picks, among the colliding streams behind a bare display
// name, the one this kill belongs to: the victim DIED at t, so the right
// stream is the one with an alive interval ending exactly there — the death
// that closed the life is the kill itself (alive[] closes at the death
// instant; the Part 4 reconciliation pinned that correspondence). No match,
// or two streams both ending a life at the same instant (two same-named
// players dying on one frame), resolves to nothing and the burst runs
// unclipped — the documented fallback.
func (idx victimLifeIndex) resolveCollision(victim string, t int32) []result.Interval {
	var found []result.Interval
	for _, alive := range idx.collided[victim] {
		for _, iv := range alive {
			if iv.End == t {
				if found != nil {
					return nil // ambiguous: both candidates die at t
				}
				found = alive
				break
			}
		}
	}
	return found
}

// startAt is the start of the life the kill at t ended, i.e. the floor of the
// backward walk.
//
// The boundary rule is Lives' exactly (lifeSpan): on a same-millisecond death
// and respawn the lives TOUCH ([..,t) and [t,..)) and the shared instant
// belongs to the life that was ending — but a kill at the instant a life
// starts AFTER a real dead gap belongs to the life that just began (a
// spawn-frag), and its own start is the floor. Without that distinction the
// walk floors at the previous life's start and absorbs it — the exact
// contamination TopKillsAvailable's gate exists to keep out of the ranking.
//
// A collided display name (two wire slots, streams keyed name#slot, frag log
// bare) is resolved by the kill's own death instant — resolveCollision — and
// clipped normally when that succeeds. Two edges return 0 — no clip beyond
// the match start — and both are honest:
//
//   - No stream resolves for the victim: the recording never observed the
//     player (nil Alive on an otherwise-measured demo), or a name collision
//     where no candidate's life ends at the kill instant (or two do). The
//     burst then runs UNCLIPPED. Unclipped-not-dropped is deliberate
//     (dropping loses real rows for a display-name accident) and documented
//     in RESULT_SCHEMA's /top-kills section; /lives records the same join
//     edge for its own rows.
//   - The kill precedes the victim's FIRST life. Alive[0].Start is clipped to
//     first observation rather than to MatchStart (clipToPresence), so this is
//     reachable, and there is no earlier life for the walk to leak into.
//
// A kill at a TOUCHING death-and-respawn instant floors at the ENDING life on
// same-frame ordering grounds: the death broadcast and the respawn share the
// frame and the death precedes the spawn within it, so the burst that
// produced the kill — killing hit included, whatever [Start, End) membership
// says about that one hit — happened in the life that died.
func (idx victimLifeIndex) startAt(victim string, t int32) int32 {
	alive := idx.byName[victim]
	if alive == nil {
		alive = idx.resolveCollision(victim, t)
	}
	i := sort.Search(len(alive), func(i int) bool { return alive[i].Start >= t })
	if i == 0 {
		// Nothing starts before t. A life starting exactly AT t is the
		// degenerate spawn-and-die-on-one-instant case; its own start is then
		// the correct floor.
		if len(alive) > 0 && alive[0].Start == t {
			return t
		}
		return 0
	}
	if i < len(alive) && alive[i].Start == t && alive[i-1].End < t {
		// A life starts exactly at t after a REAL dead gap: the kill is a
		// spawn-frag on the new life, not the tail of the old one. Only the
		// touching case (End == t) gives the instant to the ending life.
		return t
	}
	return alive[i-1].Start
}

// sortTopKills is the total ranking: burst damage desc, then the earlier kill,
// then killer and victim by name. Total so that the response is byte-stable —
// one killer can land two kills on the same instant (a quad rocket into two
// opponents), which the first three keys alone do not separate.
func sortTopKills(rows []TopKill) {
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].Damage != rows[b].Damage {
			return rows[a].Damage > rows[b].Damage
		}
		if rows[a].Time != rows[b].Time {
			return rows[a].Time < rows[b].Time
		}
		if rows[a].Killer != rows[b].Killer {
			return rows[a].Killer < rows[b].Killer
		}
		return rows[a].Victim < rows[b].Victim
	})
}
