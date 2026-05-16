package analyzer

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// msTime converts a float64-seconds event timestamp into the canonical
// int32 milliseconds used throughout schema v8. Event Time is the
// derived view of the decoder's int32-ms accumulator
// (msg.Time = float64(d.timeMs)*0.001); math.Round inverts that exactly
// for any integer-ms value representable in float64 (up to ~285 years
// — comfortably more than any conceivable match).
//
// Events that carry an explicit TimeMs field (PlayerPositionEvent,
// SpawnEvent, DeathEvent) bypass this helper and use TimeMs directly;
// the other event types haven't been plumbed yet, so producers convert
// here at the analyzer write site. The round-trip is mathematically
// lossless when math.Round is used.
func msTime(t float64) int32 {
	return int32(math.Round(t * 1000))
}

// Streams emission for the timeline analyzer.
//
// Every OnEvent dispatch updates the running cursor (timelinePlayerState
// fields) AND the historical record (the streamBuilder substruct).
// The cursor is the analyser's "value right now"; the builder is the
// append-only ledger that becomes result.PlayerStream at finalize.
//
// Append rules (D11 in PLAN-v3): change streams dedup against last
// value; position never dedups; intervals open on false→true and close
// on true→false (or at match end).

// recordHealth dedups against the last seen value before appending.
// All record* functions take integer milliseconds (schema v8).
func (b *streamBuilder) recordHealth(tMs int32, v int16) {
	if n := len(b.health); n > 0 && b.health[n-1].v == v {
		return
	}
	b.health = append(b.health, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordArmor(tMs int32, v int16) {
	if n := len(b.armor); n > 0 && b.armor[n-1].v == v {
		return
	}
	b.armor = append(b.armor, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordArmorType(tMs int32, v string) {
	if n := len(b.armorType); n > 0 && b.armorType[n-1].v == v {
		return
	}
	b.armorType = append(b.armorType, changeStr{t: tMs, v: v})
}

func (b *streamBuilder) recordLoc(tMs int32, v int16) {
	if n := len(b.loc); n > 0 && b.loc[n-1].v == v {
		return
	}
	b.loc = append(b.loc, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordShells(tMs int32, v int16) {
	if n := len(b.shells); n > 0 && b.shells[n-1].v == v {
		return
	}
	b.shells = append(b.shells, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordNails(tMs int32, v int16) {
	if n := len(b.nails); n > 0 && b.nails[n-1].v == v {
		return
	}
	b.nails = append(b.nails, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordRockets(tMs int32, v int16) {
	if n := len(b.rockets); n > 0 && b.rockets[n-1].v == v {
		return
	}
	b.rockets = append(b.rockets, changeI16{t: tMs, v: v})
}

func (b *streamBuilder) recordCells(tMs int32, v int16) {
	if n := len(b.cells); n > 0 && b.cells[n-1].v == v {
		return
	}
	b.cells = append(b.cells, changeI16{t: tMs, v: v})
}

// recordPosition appends every native sample (no dedup; D11
// asymmetry). Time is integer milliseconds — the canonical wire-native
// unit; we never narrow it back to float to avoid drift across the
// boundary comparisons in locgraph / blip filter.
func (b *streamBuilder) recordPosition(tMs int32, x, y, z float32) {
	b.posT = append(b.posT, tMs)
	b.posX = append(b.posX, int32(x))
	b.posY = append(b.posY, int32(y))
	b.posZ = append(b.posZ, int32(z))
}

func (b *streamBuilder) recordSpawn(tMs int32) {
	b.spawns = append(b.spawns, tMs)
}

func (b *streamBuilder) recordDeath(tMs int32) {
	b.deaths = append(b.deaths, tMs)
}

// updateInterval drives an interval stream based on a boolean flip.
// On false→true, opens an anchor at tMs. On true→false, closes the
// previous anchor as [anchor, tMs) and appends to the closed list.
// Same-state events are no-ops (dedup invariant for booleans). All
// times are integer milliseconds (schema v8).
func (s *intervalState) updateInterval(tMs int32, held bool) {
	if held == s.held {
		return
	}
	if held {
		s.anchor = tMs
		s.held = true
		return
	}
	// true → false: close the open interval.
	if s.held {
		s.closed = append(s.closed, intervalRecord{start: s.anchor, end: tMs})
	}
	s.held = false
}

// closeAtMatchEnd flushes any still-open interval at match end so the
// caller doesn't get half-built records. After this no further
// updateInterval calls should arrive.
func (s *intervalState) closeAtMatchEnd(tMs int32) {
	if s.held {
		s.closed = append(s.closed, intervalRecord{start: s.anchor, end: tMs})
		s.held = false
	}
}

// recordItemFlags is a one-shot helper called from the analyzer's
// stat-update path. It folds the parsed boolean state for every
// inventory field into the corresponding interval streams.
func (b *streamBuilder) recordItemFlags(tMs int32, w weaponLoadout, p powerupLoadout) {
	b.rl.updateInterval(tMs, w.rl)
	b.lg.updateInterval(tMs, w.lg)
	b.gl.updateInterval(tMs, w.gl)
	b.ssg.updateInterval(tMs, w.ssg)
	b.sng.updateInterval(tMs, w.sng)
	b.quad.updateInterval(tMs, p.quad)
	b.pent.updateInterval(tMs, p.pent)
	b.ring.updateInterval(tMs, p.ring)
}

// finalize closes any open intervals at matchEnd and converts internal
// records to the public result types.
func (b *streamBuilder) finalize(matchEndMs int32) {
	b.rl.closeAtMatchEnd(matchEndMs)
	b.lg.closeAtMatchEnd(matchEndMs)
	b.gl.closeAtMatchEnd(matchEndMs)
	b.ssg.closeAtMatchEnd(matchEndMs)
	b.sng.closeAtMatchEnd(matchEndMs)
	b.quad.closeAtMatchEnd(matchEndMs)
	b.pent.closeAtMatchEnd(matchEndMs)
	b.ring.closeAtMatchEnd(matchEndMs)
}

// toPlayerStream converts the builder into result.PlayerStream,
// suitable for appending to result.Streams.Players.
func (b *streamBuilder) toPlayerStream(name, team string) result.PlayerStream {
	ps := result.PlayerStream{Name: name, Team: team}
	if len(b.health) > 0 {
		ps.Health = make([]result.ChangeI16, len(b.health))
		for i, c := range b.health {
			ps.Health[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.armor) > 0 {
		ps.Armor = make([]result.ChangeI16, len(b.armor))
		for i, c := range b.armor {
			ps.Armor[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.armorType) > 0 {
		ps.ArmorType = make([]result.ChangeStr, len(b.armorType))
		for i, c := range b.armorType {
			ps.ArmorType[i] = result.ChangeStr{T: c.t, V: c.v}
		}
	}
	if len(b.loc) > 0 {
		ps.Loc = make([]result.ChangeI16, len(b.loc))
		for i, c := range b.loc {
			ps.Loc[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	ps.RL = intervalsToResult(b.rl.closed)
	ps.LG = intervalsToResult(b.lg.closed)
	ps.GL = intervalsToResult(b.gl.closed)
	ps.SSG = intervalsToResult(b.ssg.closed)
	ps.SNG = intervalsToResult(b.sng.closed)
	ps.Quad = intervalsToResult(b.quad.closed)
	ps.Pent = intervalsToResult(b.pent.closed)
	ps.Ring = intervalsToResult(b.ring.closed)
	if len(b.shells) > 0 {
		ps.Shells = make([]result.ChangeI16, len(b.shells))
		for i, c := range b.shells {
			ps.Shells[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.nails) > 0 {
		ps.Nails = make([]result.ChangeI16, len(b.nails))
		for i, c := range b.nails {
			ps.Nails[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.rockets) > 0 {
		ps.Rockets = make([]result.ChangeI16, len(b.rockets))
		for i, c := range b.rockets {
			ps.Rockets[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.cells) > 0 {
		ps.Cells = make([]result.ChangeI16, len(b.cells))
		for i, c := range b.cells {
			ps.Cells[i] = result.ChangeI16{T: c.t, V: c.v}
		}
	}
	if len(b.posT) > 0 {
		pos := &result.PositionTrack{
			T: append([]int32(nil), b.posT...),
			X: append([]int32(nil), b.posX...),
			Y: append([]int32(nil), b.posY...),
			Z: append([]int32(nil), b.posZ...),
		}
		if len(b.posLi) == len(b.posT) {
			pos.Li = append([]int16(nil), b.posLi...)
		}
		ps.Position = pos
	}
	if len(b.spawns) > 0 {
		ps.Spawns = append([]int32(nil), b.spawns...)
	}
	if len(b.deaths) > 0 {
		ps.Deaths = append([]int32(nil), b.deaths...)
	}
	return ps
}

func intervalsToResult(in []intervalRecord) []result.Interval {
	if len(in) == 0 {
		return nil
	}
	out := make([]result.Interval, len(in))
	for i, r := range in {
		out[i] = result.Interval{Start: r.start, End: r.end}
	}
	return out
}

// disambiguatePlayerName resolves D12 (collision suffix). Given a slot
// and a name that may collide with another slot's resolved name in the
// same match, return the slot-suffixed form so each slot's stream is
// uniquely keyed.
func disambiguatePlayerName(name string, slot int, allNames map[string]int) string {
	if allNames[name] > 1 {
		return name + "#" + intToStr(slot)
	}
	return name
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if negative {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// buildStreamsResult assembles result.Streams from each player's
// streamBuilder. Walks slots in order so iteration is deterministic.
// matchStart / matchEnd anchor GlobalStream.
//
// Disambiguation: if two slots resolve to the same canonical name,
// the second carries a "#slot" suffix per D12.
func (a *TimelineAnalyzer) buildStreamsResult(slotToName map[int]string, slotToTeam map[int]string, matchStart, matchEnd float64) *result.Streams {
	if len(a.playerState) == 0 {
		return nil
	}
	// Convert seconds → int32 ms once at this entry; the result schema
	// stores time in integer ms (schema v8).
	matchStartMs := msTime(matchStart)
	matchEndMs := msTime(matchEnd)
	// Count name occurrences for collision detection.
	nameCounts := make(map[string]int)
	for slot := range a.playerState {
		if name := slotToName[slot]; name != "" {
			nameCounts[name]++
		}
	}

	// Sort slots so iteration order is deterministic across runs.
	slots := make([]int, 0, len(a.playerState))
	for slot := range a.playerState {
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	streams := &result.Streams{
		Global: result.GlobalStream{MatchStart: matchStartMs, MatchEnd: matchEndMs},
	}
	for _, slot := range slots {
		state := a.playerState[slot]
		if state == nil {
			continue
		}
		name := slotToName[slot]
		if name == "" {
			continue
		}
		state.streams.finalize(matchEndMs)
		uniqName := disambiguatePlayerName(name, slot, nameCounts)
		ps := state.streams.toPlayerStream(uniqName, slotToTeam[slot])
		streams.Players = append(streams.Players, ps)
	}
	if len(streams.Players) == 0 {
		return nil
	}
	return streams
}

// resolveLocsAndFilterBlips populates each player's PositionTrack.Li
// column from the loc finder, runs the blip filter on it (collapsing
// short-residence wall-bleed onto adjacent stable runs), and emits
// the resulting sparse Loc change stream into PlayerStream.Loc.
//
// Replaces the v6 path of populating per-bucket pData.location and
// running applyBlipFilter on `a.buckets`. The new approach operates
// directly on the native-rate position samples so the parse-time
// bucket data structure is no longer needed.
//
// Returns the loc-name → index map for any callers that need to
// resolve external loc references (e.g. the regions builder).
func (a *TimelineAnalyzer) resolveLocsAndFilterBlips() (locTable []string, locIndex map[string]int) {
	locTable = []string{""}
	locIndex = map[string]int{"": 0}
	if a.locFinder == nil {
		return locTable, locIndex
	}
	thresholdMs := int32(a.blipThresholdMs)

	// First pass: resolve every native sample's nearest loc, populate
	// PositionTrack.Li. Build the loc-name → index map incrementally
	// so finalize doesn't need a separate "collect names then assign
	// indices" pass; the index for a name is stable from first use.
	indexFor := func(name string) int16 {
		if name == "" {
			return 0
		}
		idx, ok := locIndex[name]
		if !ok {
			idx = len(locTable)
			locTable = append(locTable, name)
			locIndex[name] = idx
		}
		return int16(idx)
	}

	// Sort slots so iteration is deterministic — locTable indices are
	// assigned in order of first appearance, and a Go map iteration
	// order would shuffle them across runs.
	slots := make([]int, 0, len(a.playerState))
	for slot := range a.playerState {
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	for _, slot := range slots {
		state := a.playerState[slot]
		b := &state.streams
		if len(b.posT) == 0 {
			continue
		}
		if cap(b.posLi) < len(b.posT) {
			b.posLi = make([]int16, len(b.posT))
		} else {
			b.posLi = b.posLi[:len(b.posT)]
		}
		for i := range b.posT {
			x, y, z := float32(b.posX[i]), float32(b.posY[i]), float32(b.posZ[i])
			if x == 0 && y == 0 && z == 0 {
				b.posLi[i] = 0
				continue
			}
			b.posLi[i] = indexFor(a.locFinder.FindNearest(x, y, z))
		}
	}

	// Second pass: run the blip filter on each player's Li column,
	// using each player's spawn / death timestamps to split runs.
	if thresholdMs > 0 {
		for _, slot := range slots {
			state := a.playerState[slot]
			b := &state.streams
			if len(b.posT) == 0 {
				continue
			}
			boundaries := mergeBoundaries(b.spawns, b.deaths)
			filterPositionLiBlips(b, boundaries, thresholdMs)
		}
	}

	// Third pass: emit the sparse PlayerStream.Loc change stream from
	// the (now-smoothed) Li column. Both pt.T and the Loc change
	// stream are int32 ms in schema v8 — no conversion needed.
	for _, slot := range slots {
		state := a.playerState[slot]
		b := &state.streams
		for i := range b.posT {
			state.streams.recordLoc(b.posT[i], b.posLi[i])
		}
	}
	return locTable, locIndex
}

// mergeBoundaries returns a sorted list of timestamps where the blip
// filter must split runs: every spawn and every death. Spawns and
// deaths can interleave; merge sorts both into one ascending slice.
// Values are integer milliseconds — comparisons against b.posT are
// exact, no eps slack needed.
func mergeBoundaries(spawns, deaths []int32) []int32 {
	if len(spawns) == 0 && len(deaths) == 0 {
		return nil
	}
	out := make([]int32, 0, len(spawns)+len(deaths))
	i, j := 0, 0
	for i < len(spawns) && j < len(deaths) {
		if spawns[i] <= deaths[j] {
			out = append(out, spawns[i])
			i++
		} else {
			out = append(out, deaths[j])
			j++
		}
	}
	out = append(out, spawns[i:]...)
	out = append(out, deaths[j:]...)
	return out
}

// filterPositionLiBlips smooths short-residence Li runs in b.posLi.
// Mirrors v6's applyBlipFilter / filterBlipsInRun logic but operates
// on per-position-sample Li values rather than per-50ms buckets.
//
// Splits the sample stream into segments at boundary timestamps
// (spawn / death) and at Li=0 gaps (no resolved loc). Within each
// segment, groups consecutive same-Li samples; segments whose
// duration is below thresholdMs become "blips" that get reassigned
// to the surrounding stable Li values. Mutates b.posLi in place.
//
// All time arithmetic is integer milliseconds — boundaries and
// b.posT both use int32 ms so comparisons are exact (this is the
// site of the gib-respawn precision bug schema v8 fixed).
func filterPositionLiBlips(b *streamBuilder, boundaries []int32, thresholdMs int32) {
	if b == nil || len(b.posT) == 0 || len(b.posLi) != len(b.posT) {
		return
	}
	// Walk samples, break runs at boundary crossings or Li=0.
	runStart := 0
	bIdx := 0
	for runStart < len(b.posT) {
		// Skip leading Li=0 samples (no loc resolved).
		for runStart < len(b.posT) && b.posLi[runStart] == 0 {
			runStart++
		}
		if runStart >= len(b.posT) {
			return
		}
		runEnd := runStart + 1
		for runEnd < len(b.posT) && b.posLi[runEnd] != 0 {
			t := b.posT[runEnd]
			for bIdx < len(boundaries) && boundaries[bIdx] <= t {
				if boundaries[bIdx] > b.posT[runStart] {
					goto runComplete
				}
				bIdx++
			}
			runEnd++
		}
	runComplete:
		filterBlipsInPositionRun(b, runStart, runEnd, thresholdMs)
		runStart = runEnd
	}
}

// filterBlipsInPositionRun applies the blip-collapse rules to one
// contiguous Li run [runStart, runEnd) of b.posLi. Implementation
// follows v6's filterBlipsInRun (leading/trailing blips inherit
// nearest stable; blips between two stables split ceil/floor; blips
// between same-loc stables collapse).
func filterBlipsInPositionRun(b *streamBuilder, runStart, runEnd int, thresholdMs int32) {
	if runEnd-runStart < 2 {
		return
	}
	type segment struct {
		li         int16
		start, end int
		duration   int32 // ms
	}
	var segs []segment
	for i := runStart; i < runEnd; {
		li := b.posLi[i]
		j := i + 1
		for j < runEnd && b.posLi[j] == li {
			j++
		}
		var dur int32
		if j < runEnd {
			dur = b.posT[j] - b.posT[i]
		} else if runEnd < len(b.posT) {
			dur = b.posT[runEnd] - b.posT[i]
		} else if j-1 > i {
			dur = b.posT[j-1] - b.posT[i]
		}
		segs = append(segs, segment{li: li, start: i, end: j, duration: dur})
		i = j
	}
	if len(segs) == 0 {
		return
	}
	stable := make([]bool, len(segs))
	firstStable, lastStable := -1, -1
	for i, s := range segs {
		if s.duration >= thresholdMs {
			stable[i] = true
			if firstStable < 0 {
				firstStable = i
			}
			lastStable = i
		}
	}
	if firstStable < 0 {
		return
	}
	for i := 0; i < firstStable; i++ {
		setLiInRange(b.posLi, segs[i].start, segs[i].end, segs[firstStable].li)
	}
	prev := firstStable
	for next := firstStable + 1; next <= lastStable; next++ {
		if !stable[next] {
			continue
		}
		if prev+1 < next {
			aLi := segs[prev].li
			dLi := segs[next].li
			firstBlipSeg := prev + 1
			if aLi == dLi {
				for k := firstBlipSeg; k < next; k++ {
					setLiInRange(b.posLi, segs[k].start, segs[k].end, aLi)
				}
			} else {
				total := 0
				for k := firstBlipSeg; k < next; k++ {
					total += segs[k].end - segs[k].start
				}
				aCount := (total + 1) / 2
				assigned := 0
				for k := firstBlipSeg; k < next; k++ {
					for s := segs[k].start; s < segs[k].end; s++ {
						if assigned < aCount {
							b.posLi[s] = aLi
						} else {
							b.posLi[s] = dLi
						}
						assigned++
					}
				}
			}
		}
		prev = next
	}
	for i := lastStable + 1; i < len(segs); i++ {
		setLiInRange(b.posLi, segs[i].start, segs[i].end, segs[lastStable].li)
	}
}

func setLiInRange(li []int16, lo, hi int, v int16) {
	for i := lo; i < hi; i++ {
		li[i] = v
	}
}

// itemBitsToLoadouts decodes the raw item bitfield into the
// (weapons, powerups, armorType) tuple. Used by the stream emission
// path on every StatItems update.
func itemBitsToLoadouts(items int) (weaponLoadout, powerupLoadout, string) {
	w := weaponLoadout{
		rl:  items&events.ITRocketLauncher != 0,
		lg:  items&events.ITLightning != 0,
		ssg: items&events.ITSuperShotgun != 0,
		sng: items&events.ITSuperNailgun != 0,
		gl:  items&events.ITGrenadeLauncher != 0,
	}
	p := powerupLoadout{
		quad: items&events.ITQuad != 0,
		pent: items&events.ITInvulnerability != 0,
		ring: items&events.ITInvisibility != 0,
	}
	armorType := ""
	switch {
	case items&events.ITArmor3 != 0:
		armorType = "ra"
	case items&events.ITArmor2 != 0:
		armorType = "ya"
	case items&events.ITArmor1 != 0:
		armorType = "ga"
	}
	return w, p, armorType
}
