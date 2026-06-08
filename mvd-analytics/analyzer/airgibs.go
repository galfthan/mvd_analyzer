package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Airgib detection tuning.
const (
	// airgibMinHeightUnits is the floor-relative height (PositionTrack.H,
	// feet above the floor) a victim must be at for a rocket hit to count
	// as an airgib — ~two player models up. The player hull is 56 tall;
	// 96 keeps the list to genuinely-airborne hits, not stair-steps or
	// small hops.
	airgibMinHeightUnits = 96

	// airgibTopN caps the Key-Moments list. The web view re-sorts these
	// client-side, so the cap is on what we surface, not the sort.
	airgibTopN = 20

	// airgibPosMaxGapMs rejects a hit whose nearest victim position
	// sample is further away in time than this — without a position near
	// the hit we can't say how high the victim was.
	airgibPosMaxGapMs = 250

	// Lethality window: a rocket frag (same attacker→victim) this close to
	// the hit marks it lethal. Asymmetric — the obituary lands at or just
	// after the damage.
	airgibLethalBackMs = 200
	airgibLethalFwdMs  = 1000
)

// airgibsPost finds enemy rocket hits landed on airborne victims and
// publishes the top airgibTopN (by height) to TimelineAnalysis.Airgibs
// for the Key Moments view. It is a post-processor so it runs with the
// full Result — the per-hit damage log (result.Damage), the streams'
// floor-height column (PositionTrack.H), the frag log (for lethality),
// and the loc table / name→userid map — all populated and in one
// match-relative time frame (it runs after normalizeMatchRelativeTimes).
//
// No-op when the map has no clip hull (no PositionTrack.H to read), so
// the airgibs list is simply absent rather than wrong on BSP-less runs.
func airgibsPost(res *Result, co *CoreOutputs) {
	if res == nil || res.Damage == nil || res.Streams == nil || res.TimelineAnalysis == nil {
		return
	}

	streamByName := make(map[string]*result.PlayerStream, len(res.Streams.Players))
	anyHeight := false
	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		streamByName[p.Name] = p
		if p.Position != nil && len(p.Position.H) == len(p.Position.T) && len(p.Position.H) > 0 {
			anyHeight = true
		}
	}
	if !anyHeight {
		return // no floor-height data (no BSP for the map)
	}

	locTable := res.TimelineAnalysis.LocTable
	userIDs := res.TimelineAnalysis.PlayerUserIDs
	teamFor := func(name string) string {
		if co != nil && co.Names != nil {
			return co.Names.TeamForName(name)
		}
		return ""
	}

	// Rocket kills, for lethality matching.
	var rlFrags []result.FragEntry
	if res.Frags != nil {
		for _, f := range res.Frags.Frags {
			if f.Weapon == "rl" && !f.IsSuicide {
				rlFrags = append(rlFrags, f)
			}
		}
	}

	var events []result.AirgibEvent
	for _, d := range res.Damage.Events {
		// Enemy rockets only — self / teammate / environmental hits aren't
		// airgibs.
		if d.Weapon != "rl" || d.IsSelf || d.IsTeam || d.IsEnv {
			continue
		}
		vs := streamByName[d.Victim]
		if vs == nil || vs.Position == nil || len(vs.Position.H) != len(vs.Position.T) {
			continue
		}
		idx := nearestSampleIndex(vs.Position.T, d.Time)
		if idx < 0 || absI32(vs.Position.T[idx]-d.Time) > airgibPosMaxGapMs {
			continue
		}
		h := vs.Position.H[idx]
		if h == result.NoFloor || h < airgibMinHeightUnits {
			continue
		}
		loc := ""
		if idx < len(vs.Position.Li) {
			loc = locNameForIndex(locTable, vs.Position.Li[idx])
		}
		events = append(events, result.AirgibEvent{
			Time:           d.Time,
			Attacker:       d.Attacker,
			AttackerTeam:   teamFor(d.Attacker),
			AttackerUserID: userIDs[d.Attacker],
			Victim:         d.Victim,
			VictimTeam:     teamFor(d.Victim),
			VictimUserID:   userIDs[d.Victim],
			Height:         h,
			Loc:            loc,
			Damage:         d.Damage,
			Splash:         d.IsSplash,
			Lethal:         airgibLethal(rlFrags, d),
		})
	}
	if len(events) == 0 {
		return
	}

	// Default order: highest first, ties broken by earliest time for a
	// stable, deterministic list. The web view re-sorts client-side.
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Height != events[j].Height {
			return events[i].Height > events[j].Height
		}
		return events[i].Time < events[j].Time
	})
	if len(events) > airgibTopN {
		events = events[:airgibTopN]
	}
	res.TimelineAnalysis.Airgibs = events
}

// airgibLethal reports whether a rocket frag (same attacker→victim)
// landed within the lethality window of this hit.
func airgibLethal(rlFrags []result.FragEntry, d result.DamageEntry) bool {
	for _, f := range rlFrags {
		if f.Victim != d.Victim || f.Killer != d.Attacker {
			continue
		}
		if f.Time >= d.Time-airgibLethalBackMs && f.Time <= d.Time+airgibLethalFwdMs {
			return true
		}
	}
	return false
}

// nearestSampleIndex returns the index into the time-sorted slice ts
// whose value is closest to t, or -1 when ts is empty.
func nearestSampleIndex(ts []int32, t int32) int {
	if len(ts) == 0 {
		return -1
	}
	i := sort.Search(len(ts), func(k int) bool { return ts[k] >= t })
	if i == 0 {
		return 0
	}
	if i >= len(ts) {
		return len(ts) - 1
	}
	if t-ts[i-1] <= ts[i]-t {
		return i - 1
	}
	return i
}

// locNameForIndex resolves a PositionTrack.Li index into a loc name,
// bounds-checked. Index 0 (and out-of-range) is the "no loc" sentinel.
func locNameForIndex(locTable []string, li int16) string {
	if li <= 0 || int(li) >= len(locTable) {
		return ""
	}
	return locTable[li]
}
