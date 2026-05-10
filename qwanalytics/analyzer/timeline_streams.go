package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwdemo/events"
)

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
func (b *streamBuilder) recordHealth(t float64, v int16) {
	if n := len(b.health); n > 0 && b.health[n-1].v == v {
		return
	}
	b.health = append(b.health, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordArmor(t float64, v int16) {
	if n := len(b.armor); n > 0 && b.armor[n-1].v == v {
		return
	}
	b.armor = append(b.armor, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordArmorType(t float64, v string) {
	if n := len(b.armorType); n > 0 && b.armorType[n-1].v == v {
		return
	}
	b.armorType = append(b.armorType, changeStr{t: t, v: v})
}

func (b *streamBuilder) recordLoc(t float64, v int16) {
	if n := len(b.loc); n > 0 && b.loc[n-1].v == v {
		return
	}
	b.loc = append(b.loc, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordShells(t float64, v int16) {
	if n := len(b.shells); n > 0 && b.shells[n-1].v == v {
		return
	}
	b.shells = append(b.shells, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordNails(t float64, v int16) {
	if n := len(b.nails); n > 0 && b.nails[n-1].v == v {
		return
	}
	b.nails = append(b.nails, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordRockets(t float64, v int16) {
	if n := len(b.rockets); n > 0 && b.rockets[n-1].v == v {
		return
	}
	b.rockets = append(b.rockets, changeI16{t: t, v: v})
}

func (b *streamBuilder) recordCells(t float64, v int16) {
	if n := len(b.cells); n > 0 && b.cells[n-1].v == v {
		return
	}
	b.cells = append(b.cells, changeI16{t: t, v: v})
}

// recordPosition appends every native sample (no dedup; D11
// asymmetry).
func (b *streamBuilder) recordPosition(t float64, x, y, z float32) {
	b.posT = append(b.posT, float32(t))
	b.posX = append(b.posX, int32(x))
	b.posY = append(b.posY, int32(y))
	b.posZ = append(b.posZ, int32(z))
}

func (b *streamBuilder) recordSpawn(t float64) {
	b.spawns = append(b.spawns, t)
}

func (b *streamBuilder) recordDeath(t float64) {
	b.deaths = append(b.deaths, t)
}

// updateInterval drives an interval stream based on a boolean flip.
// On false→true, opens an anchor at t. On true→false, closes the
// previous anchor as [anchor, t) and appends to the closed list.
// Same-state events are no-ops (dedup invariant for booleans).
func (s *intervalState) updateInterval(t float64, held bool) {
	if held == s.held {
		return
	}
	if held {
		s.anchor = t
		s.held = true
		return
	}
	// true → false: close the open interval.
	if s.held {
		s.closed = append(s.closed, intervalRecord{start: s.anchor, end: t})
	}
	s.held = false
}

// closeAtMatchEnd flushes any still-open interval at match end so the
// caller doesn't get half-built records. After this no further
// updateInterval calls should arrive.
func (s *intervalState) closeAtMatchEnd(t float64) {
	if s.held {
		s.closed = append(s.closed, intervalRecord{start: s.anchor, end: t})
		s.held = false
	}
}

// recordItemFlags is a one-shot helper called from the analyzer's
// stat-update path. It folds the parsed boolean state for every
// inventory field into the corresponding interval streams.
func (b *streamBuilder) recordItemFlags(t float64, w weaponLoadout, p powerupLoadout) {
	b.rl.updateInterval(t, w.rl)
	b.lg.updateInterval(t, w.lg)
	b.gl.updateInterval(t, w.gl)
	b.ssg.updateInterval(t, w.ssg)
	b.sng.updateInterval(t, w.sng)
	b.quad.updateInterval(t, p.quad)
	b.pent.updateInterval(t, p.pent)
	b.ring.updateInterval(t, p.ring)
}

// finalize closes any open intervals at matchEnd and converts internal
// records to the public result types.
func (b *streamBuilder) finalize(matchEnd float64) {
	b.rl.closeAtMatchEnd(matchEnd)
	b.lg.closeAtMatchEnd(matchEnd)
	b.gl.closeAtMatchEnd(matchEnd)
	b.ssg.closeAtMatchEnd(matchEnd)
	b.sng.closeAtMatchEnd(matchEnd)
	b.quad.closeAtMatchEnd(matchEnd)
	b.pent.closeAtMatchEnd(matchEnd)
	b.ring.closeAtMatchEnd(matchEnd)
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
		ps.Position = &result.PositionTrack{
			T: append([]float32(nil), b.posT...),
			X: append([]int32(nil), b.posX...),
			Y: append([]int32(nil), b.posY...),
			Z: append([]int32(nil), b.posZ...),
		}
	}
	if len(b.spawns) > 0 {
		ps.Spawns = append([]float64(nil), b.spawns...)
	}
	if len(b.deaths) > 0 {
		ps.Deaths = append([]float64(nil), b.deaths...)
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
		Global: result.GlobalStream{MatchStart: matchStart, MatchEnd: matchEnd},
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
		state.streams.finalize(matchEnd)
		uniqName := disambiguatePlayerName(name, slot, nameCounts)
		ps := state.streams.toPlayerStream(uniqName, slotToTeam[slot])
		streams.Players = append(streams.Players, ps)
	}
	if len(streams.Players) == 0 {
		return nil
	}
	return streams
}

// emitLocStreams walks the parse-time buckets and appends every
// (time, loc) transition into the matching player's stream builder.
// Called from Finalize after the blip filter has smoothed
// pData.location — so result.Streams.Players[].Loc reflects the same
// authoritative loc track every v6 consumer saw.
//
// Also emits a synthetic spawn timestamp at each player's first
// match-time bucket, matching v6's firstMatchBucketSeen flag. The
// loc-graph cursor reset depends on this marker; without it, the
// player's first observed loc transition gets counted as a real edge.
func (a *TimelineAnalyzer) emitLocStreams(slotToName map[int]string, locIndex map[string]int) {
	firstBucketSeen := make(map[int]bool, len(a.playerState))
	for _, bucket := range a.buckets {
		for slot, pData := range bucket.playerData {
			if pData == nil {
				continue
			}
			name := slotToName[slot]
			if name == "" {
				continue
			}
			state := a.playerState[slot]
			if state == nil {
				continue
			}
			idx, ok := locIndex[pData.location]
			if !ok {
				idx = 0
			}
			state.streams.recordLoc(bucket.startTime, int16(idx))

			if !firstBucketSeen[slot] {
				firstBucketSeen[slot] = true
				// If the parser didn't already emit a SpawnEvent at or
				// before this bucket, synthesise one so loc-graph cursor
				// reset works the same way it did in v6.
				existing := state.streams.spawns
				if len(existing) == 0 || existing[0] > bucket.startTime {
					prepended := append([]float64{bucket.startTime}, existing...)
					state.streams.spawns = prepended
				}
			}
		}
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
