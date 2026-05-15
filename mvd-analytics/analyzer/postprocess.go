package analyzer

import (
	"math"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Default post-processors for the registry. Each one is registered by
// NewDefaultRegistry; callers building a registry from scratch can
// pick which ones they want via RegisterPostProcessor.

// normalizeMatchRelativeTimes shifts every time-stamped field in
// Result so that t=0 is the moment the match started. The original
// match-start offset is preserved in TimelineAnalysis.DemoOffset so
// the frontend can map back to demo-time when needed (e.g. building
// hub viewer URLs).
//
// Warmup entries (those with negative t after the shift) are dropped
// from result.Streams — they would otherwise produce garbage pre-match
// samples that downstream consumers have no use for.
func normalizeMatchRelativeTimes(res *Result, _ *CoreOutputs) {
	matchStart := 0.0
	if res.TimelineAnalysis != nil {
		matchStart = res.TimelineAnalysis.MatchStartTime
	}
	if matchStart <= 0 {
		return
	}
	// matchStartMs is the int32-ms view of matchStart, used to shift
	// the int32-ms schema-v8 streams (PositionTrack.T, Spawns, Deaths).
	// This is the only place we round a float-second matchStart into
	// integer ms — a one-time normalisation rather than a per-sample
	// round-trip, so the precision class of bug doesn't reappear here.
	matchStartMs := int32(math.Round(matchStart * 1000))

	if ta := res.TimelineAnalysis; ta != nil {
		for i := range ta.FragEvents {
			ta.FragEvents[i].Time -= matchStart
		}
		for i := range ta.PowerupEvents {
			ta.PowerupEvents[i].Time -= matchStart
			ta.PowerupEvents[i].EndTime -= matchStart
		}
		for i := range ta.FragStreaks {
			ta.FragStreaks[i].Time -= matchStart
			ta.FragStreaks[i].EndTime -= matchStart
		}
		ta.DemoOffset = matchStart
		ta.MatchStartTime = 0
	}

	// Shift every per-player stream's timestamps and drop warmup
	// entries. The match-window anchors on Streams.Global also rebase.
	if streams := res.Streams; streams != nil {
		streams.Global.MatchStart -= matchStart
		streams.Global.MatchEnd -= matchStart
		if streams.Global.MatchStart < 0 {
			streams.Global.MatchStart = 0
		}
		for pi := range streams.Players {
			p := &streams.Players[pi]
			p.Health = shiftAndFilterChangeI16(p.Health, matchStart)
			p.Armor = shiftAndFilterChangeI16(p.Armor, matchStart)
			p.ArmorType = shiftAndFilterChangeStr(p.ArmorType, matchStart)
			p.Loc = shiftAndFilterChangeI16(p.Loc, matchStart)
			p.Shells = shiftAndFilterChangeI16(p.Shells, matchStart)
			p.Nails = shiftAndFilterChangeI16(p.Nails, matchStart)
			p.Rockets = shiftAndFilterChangeI16(p.Rockets, matchStart)
			p.Cells = shiftAndFilterChangeI16(p.Cells, matchStart)

			p.RL = shiftAndFilterIntervals(p.RL, matchStart)
			p.LG = shiftAndFilterIntervals(p.LG, matchStart)
			p.GL = shiftAndFilterIntervals(p.GL, matchStart)
			p.SSG = shiftAndFilterIntervals(p.SSG, matchStart)
			p.SNG = shiftAndFilterIntervals(p.SNG, matchStart)
			p.Quad = shiftAndFilterIntervals(p.Quad, matchStart)
			p.Pent = shiftAndFilterIntervals(p.Pent, matchStart)
			p.Ring = shiftAndFilterIntervals(p.Ring, matchStart)

			p.Spawns = shiftAndFilterInts(p.Spawns, matchStartMs)
			p.Deaths = shiftAndFilterInts(p.Deaths, matchStartMs)

			if p.Position != nil {
				shiftAndFilterPosition(p.Position, matchStartMs)
			}
		}
	}

	if res.Messages != nil {
		for i := range res.Messages.Events {
			res.Messages.Events[i].Time -= matchStart
		}
	}

	if res.Frags != nil {
		for i := range res.Frags.Frags {
			res.Frags.Frags[i].Time -= matchStart
		}
	}

	if res.Match != nil {
		res.Match.StartTime -= matchStart
		res.Match.EndTime -= matchStart
	}

	if res.Items != nil {
		for i := range res.Items.Items {
			ph := res.Items.Items[i].Phases
			for j := range ph {
				// AvailableFrom=0 is the synthetic "match start" marker
				// for initial phases; leave it alone. All real
				// timestamps get shifted.
				if ph[j].AvailableFrom > 0 {
					ph[j].AvailableFrom -= matchStart
				}
				if ph[j].TakenAt > 0 {
					ph[j].TakenAt -= matchStart
				}
				if ph[j].RespawnAt > 0 {
					ph[j].RespawnAt -= matchStart
				}
			}
		}
	}

	for i := range res.Backpacks {
		res.Backpacks[i].Time -= matchStart
	}

	for i := range res.WeaponPickups {
		res.WeaponPickups[i].Time -= matchStart
		if res.WeaponPickups[i].NextDeathTime > 0 {
			res.WeaponPickups[i].NextDeathTime -= matchStart
		}
		if res.WeaponPickups[i].DropTime > 0 {
			res.WeaponPickups[i].DropTime -= matchStart
		}
	}
}

// shiftAndFilterChangeI16 subtracts matchStart from each entry's T
// and drops entries with negative T. The first surviving entry is
// the carry-forward state at t=0.
func shiftAndFilterChangeI16(stream []result.ChangeI16, matchStart float64) []result.ChangeI16 {
	if len(stream) == 0 {
		return nil
	}
	// Find the latest entry at or before matchStart — it becomes the
	// carry-forward "value at t=0" entry.
	carryIdx := -1
	for i, c := range stream {
		if c.T <= matchStart {
			carryIdx = i
			continue
		}
		break
	}
	out := make([]result.ChangeI16, 0, len(stream))
	if carryIdx >= 0 {
		out = append(out, result.ChangeI16{T: 0, V: stream[carryIdx].V})
	}
	for _, c := range stream {
		if c.T <= matchStart {
			continue
		}
		out = append(out, result.ChangeI16{T: c.T - matchStart, V: c.V})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func shiftAndFilterChangeStr(stream []result.ChangeStr, matchStart float64) []result.ChangeStr {
	if len(stream) == 0 {
		return nil
	}
	carryIdx := -1
	for i, c := range stream {
		if c.T <= matchStart {
			carryIdx = i
			continue
		}
		break
	}
	out := make([]result.ChangeStr, 0, len(stream))
	if carryIdx >= 0 {
		out = append(out, result.ChangeStr{T: 0, V: stream[carryIdx].V})
	}
	for _, c := range stream {
		if c.T <= matchStart {
			continue
		}
		out = append(out, result.ChangeStr{T: c.T - matchStart, V: c.V})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shiftAndFilterIntervals shifts each interval and clamps to t >= 0.
// Intervals entirely before matchStart are dropped; intervals
// straddling are clamped to start at 0.
func shiftAndFilterIntervals(stream []result.Interval, matchStart float64) []result.Interval {
	if len(stream) == 0 {
		return nil
	}
	out := make([]result.Interval, 0, len(stream))
	for _, iv := range stream {
		if iv.End <= matchStart {
			continue
		}
		s := iv.Start - matchStart
		if s < 0 {
			s = 0
		}
		out = append(out, result.Interval{Start: s, End: iv.End - matchStart})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shiftAndFilterInts subtracts matchStartMs from each entry and drops
// entries that fall before the match start. Used for the int32-ms
// schema-v8 streams (Spawns, Deaths).
func shiftAndFilterInts(stream []int32, matchStartMs int32) []int32 {
	if len(stream) == 0 {
		return nil
	}
	out := make([]int32, 0, len(stream))
	for _, t := range stream {
		if t < matchStartMs {
			continue
		}
		out = append(out, t-matchStartMs)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shiftAndFilterPosition trims pre-match position samples and shifts
// the survivors. Mutates pt in place. Must keep all five columns
// (T/X/Y/Z/Li) aligned — BuildLocGraph and ComputeRegionControl
// both guard on `len(pt.Li) == len(pt.T)` and will silently skip the
// player if the lengths drift. All time arithmetic is int32 ms.
func shiftAndFilterPosition(pt *result.PositionTrack, matchStartMs int32) {
	if pt == nil || len(pt.T) == 0 {
		return
	}
	oldLen := len(pt.T)
	keepFrom := 0
	for keepFrom < oldLen && pt.T[keepFrom] < matchStartMs {
		keepFrom++
	}
	if keepFrom > 0 {
		pt.T = pt.T[keepFrom:]
		pt.X = pt.X[keepFrom:]
		pt.Y = pt.Y[keepFrom:]
		pt.Z = pt.Z[keepFrom:]
		if len(pt.Li) == oldLen {
			pt.Li = pt.Li[keepFrom:]
		}
	}
	for i := range pt.T {
		pt.T[i] -= matchStartMs
	}
}

// duelTeamNormalize is the post-processor wrapper around
// normalizeDuelTeams (defined in duel_normalize.go).
func duelTeamNormalize(res *Result, _ *CoreOutputs) {
	normalizeDuelTeams(res)
}

// locGraphPost runs BuildLocGraph on the assembled Result. Has to run
// after the time and duel normalisations so the loc nodes/edges use
// the same time base and team labels as the rest of the result.
func locGraphPost(res *Result, _ *CoreOutputs) {
	res.LocGraph = BuildLocGraph(res)
}
