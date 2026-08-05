package view

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Lives: one row per spawn-to-death run — the natural unit of QuakeWorld
// analysis, and the variable-length segmentation top windows deliberately does
// not try to discover.
//
// It is cheap because both halves already exist: PlayerStream.Alive is the
// segmentation (schema v64) and statsBuilder is the statistics, shared with
// TopWindows. Adding a segmentation should mean writing the segmentation.
//
// Relationship to timelineAnalysis.fragStreaks: that is the top-10 projection
// of this, ranked by frags and carrying only a count and an `ewep`. It is left
// in place — removing it would be a genuine break — but a consumer wanting
// every life, or any stat beyond frags, should read this.
//
// # Lives PARTITION the match
//
// A player's lives tile [MatchStart, MatchEnd] exactly — and on a demo with no
// match window at all (Global.MatchEnd == 0, the demo-timebase path) they tile
// [MatchStart, ∞), the last life's window closing at the int32 ceiling because
// there is no match end to close it at. Life n is attributed every event from
// its own start (MatchStart for the first) up to the start of life n+1 (that
// closing edge for the last), incoming and outgoing alike, so:
//
//	sum over a player's lives of kills, deaths, suicides, teamKills,
//	damageGiven, damageTaken, damageGivenTeam, damageGivenSelf, shots and
//	hits == that player's match totals.
//
// Three things make the partition necessary rather than decorative, and all
// three were found on real demos rather than reasoned about:
//
//   - POSTHUMOUS OUTGOING EVENTS. A rocket in flight when its shooter dies
//     still kills; measured on 211805, five landed 76-197 ms after the killer's
//     own death. You cannot fire while dead, so they were earned in the life
//     that just ended.
//   - INCOMING EVENTS INSIDE A DEAD GAP. KTX records deaths on an already-dead
//     player (the dtTELE2 pent deflection, parser/stats.go). Measured on
//     212260, nlk has two `tele` suicides inside one dead gap. They belong to
//     the life that was ending; dropping them made per-life deaths sum to 49
//     against a match total of 52.
//   - THE MATCH EDGES. Alive[0].Start is clipped to when the player was first
//     OBSERVED, not to MatchStart (analyzer/timeline_finalize.go clipToPresence),
//     and the last life can end before MatchEnd. Measured on 212260, doberman's
//     first life starts at 553 while a telefrag at t=0 costs him a teamkill;
//     on 212483 sailorman's last life ends 64 ms before MatchEnd with a damage
//     event on the final millisecond.
//
// The boundary rule: the instant separating two lives belongs to the LATER one
// when there is a real dead gap (the kill landed after the player had already
// respawned), and to the EARLIER one when the gap is zero — a same-millisecond
// death and respawn, where the player was living the earlier life when it
// happened. The final edge is closed at MatchEnd; there is nothing beyond it
// to collide with.
//
// The one asymmetry left is DURATION. DurationMs is alive time (End - Start)
// while the counts cover the wider attribution window, so a rate derived by
// dividing one by the other is not exact. That is deliberate — a life's
// duration is how long the player lived, not how long their rockets were in
// the air — but it has to be known before dividing, which is why every row
// also carries the window itself as AttrStart / AttrEnd.

// LivesOptions narrows a Lives query. From/To are match-relative int32 ms
// (0 disables that bound), matching every sibling view.
//
// FILTERED RESPONSES DO NOT RECONCILE. The partition property above holds for
// an unfiltered query and only for one, in two distinct ways:
//
//   - MinMs drops a short life together with its attribution window, so the
//     events inside that window are absent from the response entirely: lives
//     [0,10000], [12000,12100], [14000,30000] with a kill at 12500 and
//     MinMs=1000 report 0 kills for a log that holds 1.
//   - From/To select lives that OVERLAP the window, and each selected life
//     still carries its whole attribution window — which extends past To, and
//     for the first life back to MatchStart. Counts from outside the requested
//     window are therefore included.
//
// Both are the honest behaviour for "show me these lives" — a life's stats are
// the life's stats regardless of why it was selected — but a caller summing a
// filtered response must not expect the match totals.
type LivesOptions struct {
	Players []string // restrict to these players (case-sensitive)
	From    int32    // only lives overlapping [From, To]
	To      int32
	// Dmg is the damage family: "raw" | "bounded".
	//
	// The VIEW default is raw and the REST default is bounded — mvd-api's
	// handleLives substitutes "bounded" for an unset dmg, as handleDamage
	// does, falling back to raw on a demo with no bounded family. An
	// in-process caller (WASM, qw-analyze) that leaves this empty therefore
	// gets a different family than the same query over HTTP.
	Dmg   string
	MinMs int32 // drop lives shorter than this; 0 → keep all
}

// LivesView is the response: every life, in time order per player, players in
// name order.
type LivesView struct {
	TimeUnit TimeUnit `json:"timeUnit,omitempty"`

	// Dmg and BoundedMode name the damage family every row's damageGiven /
	// damageTaken / damageByWeapon was computed in, echoed on every response
	// exactly as /damage echoes them — and as /top-windows now does, so the two
	// interval endpoints answer the same question the same way. Absent only on
	// a demo with no damage stream, where measured.damage is false anyway.
	Dmg         string `json:"dmg,omitempty"`
	BoundedMode string `json:"boundedMode,omitempty"`

	// Measured says which sources this demo carries; see MeasuredSources. Not
	// omitempty — an absent marker would be the very ambiguity it removes. It
	// is what tells `damageGiven: 0` on a demo with no damage stream (which
	// Lives deliberately tolerates) from a life that dealt none.
	Measured MeasuredSources `json:"measured"`

	Lives []Life `json:"lives"`
}

// Life end reasons. Every life has exactly one; an absent killedBy used to
// conflate all three.
const (
	// LifeEndDeath: the life ended at a recorded death. killedBy/deathWeapon
	// are the obituary for it, and are absent when the death was seen only by
	// the DF_DEAD / STAT_HEALTH detectors and no obituary named it. That state
	// is reachable (the two detectors exist precisely because each sees deaths
	// the other misses) but is NOT currently observed: across the 42 cached
	// demos, 0 of the 11364 death-ended lives — of 11558 lives in total — lack
	// an obituary. Both figures are one measurement over one corpus; an earlier
	// "42 of 3501" here mixed a narrower cache into a wider one.
	LifeEndDeath = "death"
	// LifeEndMatchEnd: the player was alive when the match ended — or, on a
	// demo with no match window (Global.MatchEnd == 0, the demo-timebase path),
	// when the last of the recorded play ended. See livesEnd.
	LifeEndMatchEnd = "matchEnd"
	// LifeEndLeftGame: the life was cut short by the player's position track
	// ending (they quit or the recording lost them) rather than by a death —
	// Alive is clipped to observed presence, see clipToPresence.
	LifeEndLeftGame = "leftGame"
)

// Life is one spawn-to-death run with the shared per-interval stats block.
//
// Start/End and DurationMs are the ALIVE interval. The stats block counts
// events over the wider attribution window [AttrStart, AttrEnd], which is why
// Deaths is not capped at 1: a life carries both the death that ended it and
// ANY death recorded during the dead gap that followed (the KTX deflection
// case). Measured across the 11558 lives of the 42 cached demos: 12 rows report
// 2 and one reports 3. Deaths is 0 for a life that ended any other way — see
// EndReason.
type Life struct {
	Player string `json:"player"`
	Team   string `json:"team,omitempty"`
	Index  int    `json:"index"` // 0-based, per player, in time order
	Start  int32  `json:"start"`
	End    int32  `json:"end"`

	// AttrStart / AttrEnd are the ATTRIBUTION WINDOW: the span every event
	// field below was counted over, which is the life PLUS the dead gap that
	// follows it (and, for the first and last lives, out to the match edges).
	// DurationMs is Start..End — ALIVE time — so a rate divided by it reads
	// high, and without these a consumer could not see the span the numbers
	// actually cover. Emitted always: AttrStart is legitimately 0 on every
	// player's first life, so omitempty would delete the commonest value.
	//
	// The windows of one player's lives tile the match end to end, half-open at
	// exactly one side apiece — see the boundary rule on Lives — so no instant
	// belongs to two of them and none to none. On a demo with no match window
	// the last life's AttrEnd is the int32 ceiling: there is no match end to
	// bound it and everything after the last life belongs to it.
	AttrStart int32 `json:"attrStart"`
	AttrEnd   int32 `json:"attrEnd"`

	// EndReason is how the life ended: LifeEndDeath, LifeEndMatchEnd or
	// LifeEndLeftGame. Always present — it is decidable from the same markers
	// the segmentation itself came from.
	EndReason string `json:"endReason"`

	// SpawnLoc and DeathLoc are where the life began and ended. Omitted when
	// the demo carries no loc data.
	SpawnLoc string `json:"spawnLoc,omitempty"`
	DeathLoc string `json:"deathLoc,omitempty"`

	// KilledBy / DeathWeapon describe the death that ENDED this life, absent
	// when it did not end in one (EndReason says which) and when the death
	// carried no obituary.
	KilledBy    string `json:"killedBy,omitempty"`
	DeathWeapon string `json:"deathWeapon,omitempty"`

	// ItemsTaken is every item this player picked up in the life's attribution
	// window, in time order.
	//
	// NOT omitempty, deliberately: null means the demo carries no item
	// timeline at all, [] means it does and this life took nothing. An absent
	// field would conflate the two. The envelope's measured.items says the same
	// thing once for the whole response and is built from the same predicate;
	// this is the per-row form of it, kept because a caller iterating rows
	// should not have to hold the envelope to read one.
	ItemsTaken []LifeItem `json:"itemsTaken"`

	// WeaponsHeld is which of rl/lg/gl/ssg/sng the player held at any point
	// while ALIVE in this life, in that fixed order. Possession is clipped to
	// the alive interval rather than to the attribution window because KTX does
	// not clear the weapon bits on death (ktx/src/player.c) — a corpse still
	// reads as armed, and the dead gap would hand every life the weapons its
	// owner died holding.
	//
	// omitempty is safe here where it is not on ItemsTaken: possession comes
	// from STAT_ITEMS, which is core QW protocol and is always decoded wherever
	// the streams this endpoint segments by exist. Absent means "held none of
	// them", not "unknown".
	WeaponsHeld []string `json:"weaponsHeld,omitempty"`

	IntervalStats
}

// LifeItem is one item pickup credited to a life.
type LifeItem struct {
	Item string `json:"item"` // instance name, e.g. "ra_1" — /items' `name`
	Kind string `json:"kind"` // "ra", "mh", "quad", "rl", ...
	Time int32  `json:"time"`
}

// Lives returns every player's lives.
//
// Returns ErrUnavailable when the demo carries no streams or no measurable
// liveness (LivesAvailable is the same gate), and ErrBoundedUnavailable for an
// explicit dmg=bounded on a demo with no bounded family — the same contract as
// TopWindows.
func Lives(r *result.Result, opts LivesOptions) (*LivesView, error) {
	if err := LivesAvailable(r); err != nil {
		return nil, err
	}
	fam, err := damageFamily(r, opts.Dmg)
	if err != nil && !isNoDamage(r, err) {
		return nil, err
	}

	pf := newPlayerFilter(opts.Players)
	teamOf := defaultNameToTeam(r)
	sb := newStatsBuilder(r, fam)
	takes := itemTakes(r)
	obits := obituaries(r)
	matchStart := r.Streams.Global.MatchStart
	endOfPlay, lastAttrEnd := livesEnd(r)

	// Player order is by name, not stream order, so the output does not depend
	// on slot assignment.
	idx := make([]int, 0, len(r.Streams.Players))
	for i := range r.Streams.Players {
		if pf.accepts(r.Streams.Players[i].Name) {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool {
		return r.Streams.Players[idx[a]].Name < r.Streams.Players[idx[b]].Name
	})

	out := &LivesView{
		Dmg:         fam,
		BoundedMode: boundedModeOf(r),
		Measured:    sb.measured(),
		Lives:       []Life{},
	}
	// An INVERTED window selects nothing, matching /frags and /damage — whose
	// per-event `time >= from && time <= to` is empty for from > to — and
	// matching TopWindows, where lo > hi yields no candidate start. The
	// overlap test below would instead have kept every life that straddles
	// both bounds, so the two interval endpoints disagreed on the same query.
	// Rejecting the range is the HTTP layer's call to make, not this one's.
	if opts.From > 0 && opts.To > 0 && opts.From > opts.To {
		return out, nil
	}
	for _, i := range idx {
		p := &r.Streams.Players[i]
		// A nil Alive means liveness was not measurable for THIS player, which
		// is a different thing from an empty Alive ("measured, never alive").
		// Both range zero times here; the demo-global distinction rides
		// measured.liveness, and a demo where NO player has liveness never
		// reaches this loop (LivesAvailable).
		for n, iv := range p.Alive {
			sp := lifeSpan(p, n, matchStart, lastAttrEnd)
			if opts.MinMs > 0 && iv.End-iv.Start < opts.MinMs {
				continue
			}
			if opts.From > 0 && iv.End < opts.From {
				continue
			}
			if opts.To > 0 && iv.Start > opts.To {
				continue
			}
			l := Life{
				Player:    p.Name,
				Team:      teamOf[baseName(p.Name)],
				Index:     n,
				Start:     iv.Start,
				End:       iv.End,
				AttrStart: sp.attrStart,
				AttrEnd:   sp.attrEnd,
			}
			l.IntervalStats = sb.build(p.Name, sp)
			l.SpawnLoc = sb.locNameFor(p.Name, iv.Start)
			l.DeathLoc = sb.locNameFor(p.Name, iv.End)
			var named bool
			l.KilledBy, l.DeathWeapon, named = obits.at(p.Name, iv.End)
			l.EndReason = endReason(p, iv.End, endOfPlay, named)
			l.ItemsTaken = takes.during(p.Name, sp)
			l.WeaponsHeld = weaponsHeld(p, iv)
			out.Lives = append(out.Lives, l)
		}
	}
	return out, nil
}

// livesEnd resolves the two ends the segmentation needs, which are the same
// number on a normal demo and different when there is no match window at all.
//
//   - endOfPlay is where "the player was still alive at the end" is decided
//     (endReason). Normally Global.MatchEnd.
//   - lastAttrEnd is how far the LAST life's attribution window reaches.
//     Normally Global.MatchEnd too.
//
// MatchEnd == 0 is a real, reachable state, not a defensive guess: on the
// demo-timebase path (no match start detected, so nothing was rebased)
// analyzer.deriveAliveIntervals falls back to each player's own last observed
// timestamp and publishes Alive from it, so lives exist while the match window
// does not. Such a demo reaches this package through POST /v1/demos.
//
// There, lastAttrEnd becomes the int32 CEILING rather than the life's own end:
// the partition is what makes per-life sums equal match totals, and stopping
// at the last life's end orphans every event after it. Measured on the
// synthetic case (one life [0,20000], MatchEnd 0, events out to 30000) the
// unbounded version lost 2 of 8 kills, 20 of 80 damage and 2 of 8 shots.
// endOfPlay instead becomes the LAST life end across all players — the end of
// the recorded play — so a player still alive there reads matchEnd rather than
// leftGame, while one whose own track stopped earlier still reads leftGame.
func livesEnd(r *result.Result) (endOfPlay, lastAttrEnd int32) {
	if me := r.Streams.Global.MatchEnd; me > 0 {
		return me, me
	}
	for i := range r.Streams.Players {
		alive := r.Streams.Players[i].Alive
		if n := len(alive); n > 0 && alive[n-1].End > endOfPlay {
			endOfPlay = alive[n-1].End
		}
	}
	return endOfPlay, math.MaxInt32
}

// lifeSpan is the attribution window for life n — the partition rule described
// on Lives, expressed once.
//
// The window boundaries are successive life STARTS, never ends, which is what
// makes them a partition even if the stored Alive intervals were to overlap:
// the dead gap after a life belongs to that life, so the next window opens
// where the next life does. statsSpan.window clamps the degenerate remainder
// (starts out of order).
//
// lastAttrEnd is where the FINAL life's window closes: the match end, or the
// int32 ceiling on a demo that has none (livesEnd).
func lifeSpan(p *result.PlayerStream, n int, matchStart, lastAttrEnd int32) statsSpan {
	iv := p.Alive[n]
	sp := statsSpan{start: iv.Start, end: iv.End, attributed: true}

	sp.attrStart = iv.Start
	sp.startInclusive = true
	if n == 0 {
		// Alive[0].Start is clipped to first observation, not to MatchStart,
		// so everything before it would otherwise be attributable to no life.
		if matchStart < sp.attrStart {
			sp.attrStart = matchStart
		}
	} else if p.Alive[n-1].End == iv.Start {
		// Touching lives: the shared instant went to the earlier one.
		sp.startInclusive = false
	}

	if n+1 < len(p.Alive) {
		sp.attrEnd = p.Alive[n+1].Start
		// Closed only when the gap is zero, mirroring the start rule above so
		// that exactly one of the two lives claims the shared instant.
		sp.endInclusive = sp.attrEnd == iv.End
	} else {
		sp.attrEnd = iv.End
		if lastAttrEnd > sp.attrEnd {
			sp.attrEnd = lastAttrEnd
		}
		// The last edge is closed: there is no later life to collide with, and
		// half-opening it would drop every event landing exactly on MatchEnd.
		sp.endInclusive = true
	}
	return sp
}

// endReason decides how a life ended, from the markers the segmentation itself
// was derived from (analyzer.aliveIntervals): an interval ends at a death, at
// the end of play, or at the end of the player's observed presence.
//
// An obituary naming the player at the end instant is taken as a death even if
// no death MARKER sits there — the frag log is the stronger evidence and the
// two detectors exist precisely because each sees deaths the other misses.
//
// endOfPlay is Global.MatchEnd, or — on a demo with no match window — the last
// life end across every player (livesEnd). Without that fallback the whole
// `matchEnd > 0` test failed on such a demo and EVERY surviving player was
// reported as having left the game.
func endReason(p *result.PlayerStream, end, endOfPlay int32, obituary bool) string {
	if obituary {
		return LifeEndDeath
	}
	i := sort.Search(len(p.Deaths), func(i int) bool { return p.Deaths[i] >= end })
	if i < len(p.Deaths) && p.Deaths[i] == end {
		return LifeEndDeath
	}
	if endOfPlay > 0 && end >= endOfPlay {
		return LifeEndMatchEnd
	}
	return LifeEndLeftGame
}

// isNoDamage reports whether err is just "this demo has no damage stream",
// which must not fail a lives query — lives are segmented by spawn/death, and
// the damage half of the stats block is simply absent.
func isNoDamage(r *result.Result, err error) bool {
	return r.Damage == nil && err == ErrUnavailable
}

// obituaryIndex is the frag log keyed by victim, so finding the death that
// closed a life is a binary search rather than a scan. The scan was measured at
// 30 ms for a 500-life 4on4 — not fatal, but it is the whole cost of the
// endpoint and it is O(lives × frags).
type obituaryIndex map[string][]result.FragEntry

func obituaries(r *result.Result) obituaryIndex {
	idx := obituaryIndex{}
	if r.Frags == nil {
		return idx
	}
	for _, e := range r.Frags.Frags {
		idx[e.Victim] = append(idx[e.Victim], e)
	}
	for victim := range idx {
		v := idx[victim]
		sort.SliceStable(v, func(a, b int) bool { return v[a].Time < v[b].Time })
	}
	return idx
}

// at finds the obituary for the death that closed a life. Lives end at the
// death instant, so the match is exact rather than a nearest-in-window search.
// ok is false when no obituary named this player at t — either the life did not
// end in a death, or it ended in one the DF_DEAD / STAT_HEALTH detectors saw
// and the frag log did not.
func (idx obituaryIndex) at(player string, t int32) (killer, weapon string, ok bool) {
	v := idx[player]
	i := sort.Search(len(v), func(i int) bool { return v[i].Time >= t })
	if i < len(v) && v[i].Time == t {
		return v[i].Killer, v[i].Weapon, true
	}
	return "", "", false
}

// lifeWeapons is the weapon set reported by WeaponsHeld, in report order.
var lifeWeapons = []struct {
	name string
	of   func(*result.PlayerStream) []result.Interval
}{
	{"rl", func(p *result.PlayerStream) []result.Interval { return p.RL }},
	{"lg", func(p *result.PlayerStream) []result.Interval { return p.LG }},
	{"gl", func(p *result.PlayerStream) []result.Interval { return p.GL }},
	{"ssg", func(p *result.PlayerStream) []result.Interval { return p.SSG }},
	{"sng", func(p *result.PlayerStream) []result.Interval { return p.SNG }},
}

// weaponsHeld lists the slot weapons whose possession overlapped the life.
// Possession intervals are half-open [Start, End), so a weapon acquired exactly
// at the death instant belongs to the next life, not this one.
func weaponsHeld(p *result.PlayerStream, iv result.Interval) []string {
	var out []string
	for _, w := range lifeWeapons {
		for _, h := range w.of(p) {
			if h.Start < iv.End && h.End > iv.Start {
				out = append(out, w.name)
				break
			}
		}
	}
	return out
}

// itemTakeIndex is every item pickup, per player, in time order — built once
// per query so a 500-life response does not rescan the item timelines 500
// times.
type itemTakeIndex struct {
	measured bool
	byPlayer map[string][]LifeItem
}

// itemTakes indexes r.Items by picker.
//
// Pickups are keyed by the picker's resolved identity (ItemPhase.TakenBy,
// analyzer/items.go resolveAttributions), which is the same demoinfo-resolved
// name the streams carry — except for the "#slot" suffix the analyzer adds when
// two slots collide on one name, where the item timeline keeps the bare name.
// Such a life reports no items rather than another player's; guessing which of
// the colliding slots took it would be worse than saying nothing.
func itemTakes(r *result.Result) itemTakeIndex {
	// The same predicate the envelope's measured.items reads, so the null-vs-[]
	// distinction below and the marker always agree.
	if !itemsMeasured(r) {
		return itemTakeIndex{}
	}
	idx := itemTakeIndex{measured: true, byPlayer: map[string][]LifeItem{}}
	for _, it := range r.Items.Items {
		for _, ph := range it.Phases {
			if ph.TakenBy == "" || ph.TakenAt == 0 {
				continue
			}
			idx.byPlayer[ph.TakenBy] = append(idx.byPlayer[ph.TakenBy],
				LifeItem{Item: it.Name, Kind: it.Kind, Time: ph.TakenAt})
		}
	}
	for name := range idx.byPlayer {
		takes := idx.byPlayer[name]
		sort.Slice(takes, func(a, b int) bool {
			if takes[a].Time != takes[b].Time {
				return takes[a].Time < takes[b].Time
			}
			return takes[a].Item < takes[b].Item
		})
	}
	return idx
}

// during returns the player's pickups inside a span's attribution window, so
// that per-life pickups partition the player's match pickups exactly as the
// other counts do. Returns nil — not [] — when there is no item timeline.
func (idx itemTakeIndex) during(player string, sp statsSpan) []LifeItem {
	if !idx.measured {
		return nil
	}
	lo, hi, loIncl, hiIncl := sp.window()
	out := []LifeItem{}
	for _, t := range idx.byPlayer[player] {
		if t.Time < lo || (t.Time == lo && !loIncl) {
			continue
		}
		if t.Time > hi || (t.Time == hi && !hiIncl) {
			break
		}
		out = append(out, t)
	}
	return out
}

// locNameFor is the loc the player stood in at t, "" when the demo carries no
// loc data.
func (sb *statsBuilder) locNameFor(player string, t int32) string {
	stream := sb.locOf[player]
	if len(stream) == 0 || len(sb.locTable) == 0 {
		return ""
	}
	return locNameAt(sb.locTable, locIndexAt(stream, t))
}
