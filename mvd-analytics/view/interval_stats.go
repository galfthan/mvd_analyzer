package view

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The per-interval stats builder, shared by every interval segmentation:
// fixed-length hot windows (HotWindows) and spawn-to-death lives (Lives).
// Adding a third segmentation should mean writing the segmentation, not the
// statistics.
//
// It deliberately does NOT call view.Frags / view.Damage with From/To. Those
// treat From==0 or To==0 as "no bound", so an interval legitimately beginning
// at t=0 — every player's first life on every demo — would silently receive
// whole-match aggregates. Bounds here are explicit and always applied.
//
// # The attribution window
//
// Every event — incoming and outgoing alike — is attributed by ONE window per
// interval, statsSpan's [attrStart, attrEnd]. The window is not always the
// interval itself:
//
//   - For a segmentation that merely SELECTS stretches of the match (hot
//     windows) the window IS the interval, closed at both ends. That matches
//     the sibling event endpoints (/frags, /damage and /backpacks all include
//     time == to) and matters more than it looks — a kill lands on the same
//     millisecond as its damage, so a half-open end would split a kill from the
//     hit that caused it. Hot windows are not a partition of the match and are
//     free to overlap the same event, so nothing more is needed.
//   - For a segmentation that PARTITIONS the match (lives) the windows must
//     tile [MatchStart, MatchEnd] exactly, so that per-interval stats sum to
//     the player's match totals. There the window runs past the interval — see
//     Lives — and the edges are half-open at exactly one side so that no
//     instant belongs to two intervals and none belongs to none.
//
// startInclusive/endInclusive carry that edge rule. With no attribution window
// the end is closed and startInclusive alone decides the start, which is the
// shape HotWindows passes. The tie-break for a zero-length gap between two
// touching intervals is to give the shared instant to the EARLIER one — the
// life that was ending is the life the player was living when it happened.

// intervalTopLocs caps the loc lists on a stats block. Three is enough to say
// where an interval happened without turning a highlight row into a heat map.
const intervalTopLocs = 3

// MeasuredSources says which of the underlying streams THIS DEMO carries. It
// rides the envelope of every interval response (HotWindowsView, LivesView) and
// is never omitempty.
//
// MEASUREDNESS IS READ FROM HERE AND NEVER FROM A FIELD'S ABSENCE. The numeric
// fields of IntervalStats are always emitted, including a measured zero, so
// `damageGiven: 0` on its own says nothing about whether the demo had a damage
// stream — this block does. That is the repo rule ("never infer measuredness
// from omitempty", RESULT_SCHEMA.md) and the bug class player_stats.go names
// "measured-zero-becomes-absent"; the earlier shape here had it in both
// directions at once.
//
// What each flag gates:
//
//	frags    kills, deaths, teamKills, suicides, byWeapon, mainWeapon, victims,
//	         eventLocs — and, on /lives, killedBy / deathWeapon.
//	damage   damageGiven, damageTaken, damageGivenTeam, damageGivenSelf,
//	         damageByWeapon.
//	shots    shots, hits.
//	locs     locs, eventLocs, and /lives' spawnLoc / deathLoc. True only when
//	         the demo carries BOTH a loc table and at least one player loc
//	         stream, since either alone yields nothing.
//	items    /lives' itemsTaken. Meaningless to /hot-windows, which emits no
//	         item field; the block keeps one shape across both responses rather
//	         than making a consumer learn two.
//	liveness the spawn-to-death segmentation itself — /lives' whole existence.
//	         /lives 422s when it is false, so a caller only ever sees false on
//	         /hot-windows, which does not need it.
//
// False means the source is absent for the whole demo. True does NOT promise
// every row is non-zero: within a measured source, a zero is a measurement and
// an absent MAP KEY (byWeapon, victims, damageByWeapon) means "none of that
// kind" — the same key-level rule the damage schema documents.
type MeasuredSources struct {
	// Frags is NOT `r.Frags != nil`. An empty-but-present frag log on a demo
	// where the scoreboard shows deaths means every obituary went unmatched —
	// the server printed them in a form this pipeline does not parse — and a
	// row reading `kills: 0, teamKills: 0, suicides: 0` beside a measured
	// deaths count is exactly the "looks measured" trap the measuredness work
	// exists to prevent. The verdict is DEMO-GLOBAL and is computed once, by
	// the analyzer, and stored as FragResult.KillsMeasured; player_stats.go
	// reads the same field for the same decision, so the two cannot drift.
	Frags    bool `json:"frags"`
	Damage   bool `json:"damage"`
	Shots    bool `json:"shots"`
	Locs     bool `json:"locs"`
	Items    bool `json:"items"`
	Liveness bool `json:"liveness"`
}

// IntervalStats is the per-interval stats block. It is deliberately
// segmentation-agnostic: hot windows, lives (Lives) and any future interval
// segmentation all fill the same shape from the same builder, so a consumer
// learns it once.
//
// Every numeric field is emitted even at zero — see MeasuredSources for why,
// and for how to tell an unmeasured source from a measured zero. The maps and
// slices keep omitempty: for those, absence means "nothing to list", which the
// envelope marker disambiguates from "not measured".
type IntervalStats struct {
	// DurationMs is the INTERVAL's length, end - start.
	//
	// For a life that is ALIVE time, while every event field below covers the
	// life's wider attribution window — the life plus the dead gap that follows
	// it, so that a posthumous rocket counts for the life that fired it (see
	// Lives). It is the one asymmetry left, and it matters when dividing: a
	// per-second rate computed from these counts and this duration is very
	// slightly high. For a hot window the two coincide exactly.
	DurationMs int32 `json:"durationMs"`

	Kills     int            `json:"kills"`
	Deaths    int            `json:"deaths"`
	TeamKills int            `json:"teamKills"`
	Suicides  int            `json:"suicides"`
	ByWeapon  map[string]int `json:"byWeapon,omitempty"` // kills, by weapon

	// DamageGiven / DamageTaken FOLD telefrag and stomp value in, exactly as
	// /damage does, so a caller can reconcile against that endpoint. The
	// DamageByWeapon breakdown does NOT — a positional kill carries no weapon,
	// and /damage leaves it out of byWeapon for the same reason — so the map
	// sums to at most DamageGiven, not to it.
	DamageGiven     int            `json:"damageGiven"`
	DamageTaken     int            `json:"damageTaken"`
	DamageGivenTeam int            `json:"damageGivenTeam"`
	DamageGivenSelf int            `json:"damageGivenSelf"`
	DamageByWeapon  map[string]int `json:"damageByWeapon,omitempty"` // enemy damage given

	Shots int `json:"shots"`
	Hits  int `json:"hits"`

	// MainWeapon is the weapon with the most kills, ties broken by name so the
	// output is stable (Go map order is randomised). Same rule as the frag
	// streak analyzer's `ewep`. Empty when there were no kills to rank.
	MainWeapon string `json:"mainWeapon,omitempty"`

	// Victims counts kills per victim. Sums to Kills.
	Victims map[string]int `json:"victims,omitempty"`

	// Locs is where the time went (top dwell), EventLocs where the kills
	// landed. They answer different questions and routinely disagree — that
	// disagreement is the interesting part. Both empty when the demo has no
	// loc data (measured.locs false); that never fails the request.
	Locs      []IntervalLoc `json:"locs,omitempty"`
	EventLocs []IntervalLoc `json:"eventLocs,omitempty"`
}

// IntervalLoc is one loc with either a dwell time or an event count.
type IntervalLoc struct {
	Loc   string `json:"loc"`
	Ms    int32  `json:"ms,omitempty"`
	Count int    `json:"count,omitempty"`
}

type statsBuilder struct {
	r   *result.Result
	fam string // damage family: "raw" | "bounded"; "" → raw

	locTable []string
	locOf    map[string][]result.ChangeI16 // player → loc change stream
	blindOf  map[string][]result.Interval  // player → stretches with no position evidence
}

func newStatsBuilder(r *result.Result, fam string) *statsBuilder {
	sb := &statsBuilder{
		r:       r,
		fam:     fam,
		locOf:   map[string][]result.ChangeI16{},
		blindOf: map[string][]result.Interval{},
	}
	if r.TimelineAnalysis != nil {
		sb.locTable = r.TimelineAnalysis.LocTable
	}
	if r.Streams != nil {
		for i := range r.Streams.Players {
			p := &r.Streams.Players[i]
			if len(p.Loc) > 0 {
				sb.locOf[p.Name] = p.Loc
				sb.blindOf[p.Name] = blindWindows(p.Position)
			}
		}
	}
	return sb
}

// measured reports which sources back the stats block on this demo. Every flag
// is the SAME predicate the builder itself branches on a few lines below, so
// the marker cannot claim a source the block then fails to fill (or the
// reverse).
func (sb *statsBuilder) measured() MeasuredSources {
	return MeasuredSources{
		Frags:    fragsMeasured(sb.r),
		Damage:   sb.r.Damage != nil,
		Shots:    sb.r.Shots != nil,
		Locs:     len(sb.locTable) > 0 && len(sb.locOf) > 0,
		Items:    itemsMeasured(sb.r),
		Liveness: livenessMeasured(sb.r),
	}
}

// fragsMeasured is whether this demo's KILL ATTRIBUTION was observable: the
// frag log exists AND the analyzer's demo-global verdict says the log is not
// an empty one on a demo where players demonstrably died.
//
// The verdict itself is analysis policy and belongs in the analyzer, which is
// where it is decided (analyzer.killsMeasurable) and from where it is stored
// on the Result. view reads the stored answer; it does not re-derive it, and
// must not — a second implementation is how the same rule came to be applied
// on /player-stats and not here.
func fragsMeasured(r *result.Result) bool {
	return r.Frags != nil && r.Frags.KillsMeasured
}

// livenessMeasured is whether the demo carries the spawn-to-death segmentation
// at all: at least one player with a non-nil Alive.
//
// PlayerStream.Alive distinguishes three states — nil "not measurable", []
// "measured, never alive", [...] "the lives" — and Lives emits no rows for the
// first two. Without this flag the response `{"lives": []}` said "nobody ever
// lived" in both cases (incident: adversarial review, 2026-08-01). It is the
// gate LivesAvailable applies, so /lives 422s rather than serving that
// ambiguity.
func livenessMeasured(r *result.Result) bool {
	if r == nil || r.Streams == nil {
		return false
	}
	for i := range r.Streams.Players {
		if r.Streams.Players[i].Alive != nil {
			return true
		}
	}
	return false
}

// itemsMeasured is whether the demo carries an item timeline — the one
// predicate behind both the envelope's measured.items and Life.ItemsTaken's
// null-vs-[] distinction, so the two can never contradict each other.
func itemsMeasured(r *result.Result) bool { return r.Items != nil }

// statsSpan is the interval to summarise plus the window its events are
// attributed from.
//
// The zero value of the attribution fields means "the window is [start, end],
// closed at the end" — the non-partitioning case, which is what HotWindows
// passes. A partitioning segmentation sets attributed and supplies the window
// and both edge rules itself.
type statsSpan struct {
	// The interval proper. DurationMs measures THIS and nothing else, so for a
	// life it stays alive time even though the attribution window is wider.
	start, end int32

	attributed         bool
	attrStart, attrEnd int32
	startInclusive     bool // an event exactly at the window start belongs here
	endInclusive       bool // an event exactly at the window end belongs here
}

// window resolves the attribution window and its edge rules.
func (sp statsSpan) window() (lo, hi int32, loIncl, hiIncl bool) {
	if !sp.attributed {
		return sp.start, sp.end, sp.startInclusive, true
	}
	lo, hi = sp.attrStart, sp.attrEnd
	// Defensive clamp. A caller derives the window from neighbouring intervals,
	// and PlayerStream.Alive is a STORED field this package does not produce:
	// out-of-order or overlapping intervals would otherwise yield hi < lo and
	// an event count that depends on which comparison ran first. Clamping — not
	// dropping — keeps a degenerate input to a degenerate window rather than
	// silently discarding the row.
	if hi < lo {
		hi = lo
	}
	return lo, hi, sp.startInclusive, sp.endInclusive
}

// build fills the stats block for one player over the span.
func (sb *statsBuilder) build(player string, sp statsSpan) IntervalStats {
	st := IntervalStats{DurationMs: sp.end - sp.start}

	lo, hi, loIncl, hiIncl := sp.window()
	// One predicate for every event class. An earlier revision had separate
	// incoming and outgoing bounds; the two disagreed on their edges and events
	// fell into both windows or neither (kills double-counted in eventLocs,
	// deaths inside a dead gap attributed to nobody).
	in := func(t int32) bool {
		if t < lo || (t == lo && !loIncl) {
			return false
		}
		if t > hi || (t == hi && !hiIncl) {
			return false
		}
		return true
	}

	if f := sb.r.Frags; f != nil {
		byWeapon := map[string]int{}
		victims := map[string]int{}
		for i := range f.Frags {
			e := &f.Frags[i]
			if !in(e.Time) {
				continue
			}
			if e.Victim == player {
				st.Deaths++
				if e.IsSuicide {
					st.Suicides++
				}
			}
			if e.Killer != player {
				continue
			}
			switch {
			case e.IsTeamKill:
				st.TeamKills++
			case e.IsSuicide:
				// counted on the victim side above
			default:
				st.Kills++
				byWeapon[e.Weapon]++
				victims[e.Victim]++
			}
		}
		if len(byWeapon) > 0 {
			st.ByWeapon = byWeapon
			st.MainWeapon = topCountKey(byWeapon)
		}
		if len(victims) > 0 {
			st.Victims = victims
		}
	}

	if d := sb.r.Damage; d != nil {
		dmgByWeapon := map[string]int{}
		for i := range d.Events {
			e := &d.Events[i]
			if !in(e.Time) {
				continue
			}
			v := damageValue(e, sb.fam)
			if e.Victim == player {
				// Taken counts ALL sources — enemy, team, self, world —
				// matching PlayerDamage.Taken rather than KTX's enemy-only
				// dmg.taken.
				st.DamageTaken += v
			}
			if e.Attacker != player {
				continue
			}
			// Same enemy rule as view.Damage (sections.go): IsEnv does NOT
			// exclude a hit whose attacker is a real player, only a "world"
			// attacker does. Keeping these aligned is what lets a caller add
			// up per-interval damage and get the /damage totals back.
			//
			// ONE EXCEPTION, and it is on the /damage side: an unfiltered
			// SUMMARY response serving the bounded family (the REST default
			// shape, /damage?summary=1&dmg=bounded) SUBSTITUTES KTX's exact
			// end-of-match scoreboard for the per-hit reconstruction and says so
			// with boundedSource:"ktx". Per-interval sums are the
			// reconstruction, so against that response they land a few points
			// off per player — measured across the 42 cached demos, most
			// players on most demos differ (7 of 8 on 203199, 6 of 8 on
			// 211161) by 1 to 23 points. Reconcile against the NON-summary
			// aggregate (or dmg=raw), which is exact: over the same corpus
			// every per-life sum matches it to the point, in both families.
			switch {
			case e.Attacker == "world":
				// world-sourced, nobody's doing
			case e.IsSelf:
				st.DamageGivenSelf += v
			case e.IsTeam:
				st.DamageGivenTeam += v
			default:
				st.DamageGiven += v
				dmgByWeapon[e.Weapon] += v
			}
		}
		if len(dmgByWeapon) > 0 {
			st.DamageByWeapon = dmgByWeapon
		}

		// Telefrags and stomps are NOT in Events — the wire carries a 9999
		// sentinel for the hit, so the analyzer records the kill separately with
		// a RECONSTRUCTED value instead — and an aggregate rebuilt from the hit
		// log alone is short by that value, so a caller summing per-life
		// damageGiven would not get the figure /damage reports for that player.
		// The fold rule lives in sections.go and is applied here through it.
		//
		// collectScoreEvents folds the same values into the SCORE on the same
		// terms (hotwindows.go), which is what makes an unfiltered window's
		// score equal the same-named field below.
		if hasBoundedFamily(d) {
			foldPositional := func(kills []result.PositionalKill) {
				for i := range kills {
					k := &kills[i]
					if !in(k.Time) {
						continue
					}
					raw, bounded := positionalKillValues(*k)
					v := raw
					if sb.fam == "bounded" {
						v = bounded
					}
					if k.Victim == player {
						st.DamageTaken += v
					}
					if k.Attacker != player {
						continue
					}
					switch positionalKillGiven(*k) {
					case foldGivenSelf:
						st.DamageGivenSelf += v
					case foldGivenTeam:
						st.DamageGivenTeam += v
					case foldGivenEnemy:
						// DamageByWeapon is left alone on purpose: /damage's
						// byWeapon excludes the fold too (a positional kill has
						// no weapon), and diverging would break the parity the
						// fold exists to keep.
						st.DamageGiven += v
					}
				}
			}
			foldPositional(d.Telefrags)
			foldPositional(d.Stomps)
		}
	}

	if s := sb.r.Shots; s != nil {
		for i := range s.Shots {
			sh := &s.Shots[i]
			if sh.Player != player || !in(sh.Time) {
				continue
			}
			st.Shots++
			if sh.Hit {
				st.Hits++
			}
		}
	}

	st.Locs = sb.dwellLocs(player, sp.start, sp.end)
	st.EventLocs = sb.killLocs(player, in)
	return st
}

// dwellLocs is where the interval's TIME went: the player's loc residences,
// clipped to [start, end], top few by dwell.
//
// It is clipped to the INTERVAL, not to the attribution window, because it is
// the only quantity here measured in time rather than in events — the same
// reason DurationMs stays alive time.
//
// Absent loc data omits the field and never fails the request — the
// segmentation itself needs only the event log, so a demo with no position
// track still gets everything else.
func (sb *statsBuilder) dwellLocs(player string, start, end int32) []IntervalLoc {
	stream := sb.locOf[player]
	if len(stream) == 0 || len(sb.locTable) == 0 {
		return nil
	}
	blind := sb.blindOf[player]
	ms := map[string]int32{}
	for i := range stream {
		segStart := stream[i].T
		segEnd := end
		if i+1 < len(stream) {
			segEnd = stream[i+1].T
		}
		if segStart < start {
			segStart = start
		}
		if segEnd > end {
			segEnd = end
		}
		if segEnd <= segStart {
			continue
		}
		d := segEnd - segStart - blindMs(blind, segStart, segEnd)
		if d <= 0 {
			continue
		}
		if name := locNameAt(sb.locTable, stream[i].V); name != "" {
			ms[name] += d
		}
	}
	return topLocs(ms, nil)
}

// blindWindows is when the player's position track carries no evidence at all:
// before its first sample, inside every gap longer than
// result.SampleStaleCapMs, and past result.TrackHoldEnd.
//
// The loc CHANGE stream cannot answer this on its own — it only records
// transitions, so the last entry looks identical whether the player stood
// still for a minute or the recording lost sight of them. Without the bound a
// loc stream ending at 9 s inside an interval running to 30 s credits the whole
// remainder to one stale loc; on a POV recording, where only players inside the
// recorder's PVS are written, that is most of the match. Every other occupancy
// walker in this repo bounds unobserved time the same way (view.playerCursor,
// analyzer/locgraph.go) and they must not drift apart.
//
// A player with no position track keeps the unbounded hold: there is no
// evidence to bound against, and refusing to credit anything would blank the
// field for a hand-assembled Result rather than describe it.
func blindWindows(pt *result.PositionTrack) []result.Interval {
	if pt == nil || len(pt.T) == 0 {
		return nil
	}
	out := []result.Interval{{Start: math.MinInt32, End: pt.T[0]}}
	for i := 1; i < len(pt.T); i++ {
		if pt.T[i]-pt.T[i-1] > result.SampleStaleCapMs {
			out = append(out, result.Interval{Start: pt.T[i-1] + result.SampleStaleCapMs, End: pt.T[i]})
		}
	}
	return append(out, result.Interval{Start: result.TrackHoldEnd(pt.T), End: math.MaxInt32})
}

// blindMs is how much of [start, end) fell in an unobserved stretch. The
// windows are sorted and disjoint, so the scan starts at the first one that can
// overlap; on a server-recorded demo there are only the two outer ones.
func blindMs(blind []result.Interval, start, end int32) int32 {
	if len(blind) == 0 {
		return 0
	}
	i := sort.Search(len(blind), func(i int) bool { return blind[i].End > start })
	var total int32
	for ; i < len(blind) && blind[i].Start < end; i++ {
		lo, hi := blind[i].Start, blind[i].End
		if lo < start {
			lo = start
		}
		if hi > end {
			hi = end
		}
		if hi > lo {
			total += hi - lo
		}
	}
	return total
}

// killLocs is where the interval's KILLS happened — the loc the player stood
// in at each kill. Distinct from dwell: the loc you fight from is often not
// the loc you spend time in, and the disagreement is the useful signal.
//
// It takes build's own attribution predicate rather than re-deriving the bounds
// from the span. Re-deriving them is exactly how a boundary kill came to be
// counted once in Kills and twice across two lives' EventLocs.
//
// The counts sum to Kills only up to two documented losses: the list is capped
// at intervalTopLocs, and a kill whose loc did not resolve (no loc change at or
// before it) is not credited to a fabricated loc.
func (sb *statsBuilder) killLocs(player string, in func(int32) bool) []IntervalLoc {
	if sb.r.Frags == nil || len(sb.locTable) == 0 {
		return nil
	}
	stream := sb.locOf[player]
	if len(stream) == 0 {
		return nil
	}
	count := map[string]int{}
	for i := range sb.r.Frags.Frags {
		e := &sb.r.Frags.Frags[i]
		if e.Killer != player || e.IsSuicide || e.IsTeamKill || !in(e.Time) {
			continue
		}
		if name := locNameAt(sb.locTable, locIndexAt(stream, e.Time)); name != "" {
			count[name]++
		}
	}
	return topLocs(nil, count)
}

// locIndexAt is the loc in effect at t: the last change at or before it.
func locIndexAt(stream []result.ChangeI16, t int32) int16 {
	i := sort.Search(len(stream), func(i int) bool { return stream[i].T > t })
	if i == 0 {
		return 0
	}
	return stream[i-1].V
}

// topLocs ranks by value descending, then by loc name so ties are stable, and
// keeps the top few. Exactly one of ms/count is non-nil.
func topLocs(ms map[string]int32, count map[string]int) []IntervalLoc {
	var out []IntervalLoc
	for name, v := range ms {
		out = append(out, IntervalLoc{Loc: name, Ms: v})
	}
	for name, v := range count {
		out = append(out, IntervalLoc{Loc: name, Count: v})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(a, b int) bool {
		va, vb := int(out[a].Ms)+out[a].Count, int(out[b].Ms)+out[b].Count
		if va != vb {
			return va > vb
		}
		return out[a].Loc < out[b].Loc
	})
	if len(out) > intervalTopLocs {
		out = out[:intervalTopLocs]
	}
	return out
}

// topCountKey returns the highest-counted key, ties broken by name. Iterating
// the map directly would pick a different tied key on every run and break the
// golden corpus; same rule as the frag streak analyzer's `ewep`.
func topCountKey(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestN := "", 0
	for _, k := range keys {
		if m[k] > bestN {
			best, bestN = k, m[k]
		}
	}
	return best
}
