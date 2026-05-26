package view

import (
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// LocEdgePassesOptions narrows a LocEdgePasses query. Players, when
// non-empty, restricts the output to those canonical names; empty
// accepts everyone.
type LocEdgePassesOptions struct {
	Players []string
}

// LocEdgePassesView is the per-player breakdown of loc residence runs.
// A "run" is a maximal continuous sequence of loc residences with no
// death/spawn reset and no no-loc gap between them; consecutive
// residences within a run are the individual loc-graph edges. The
// frontend debug tab groups runs into 1-, 2-, or 3-edge passes.
type LocEdgePassesView struct {
	Players []PlayerLocRuns `json:"players"`
}

// PlayerLocRuns is one player's loc residence runs.
type PlayerLocRuns struct {
	Name string           `json:"name"`
	Runs [][]LocResidence `json:"runs"`
}

// LocResidence is one continuous stay in a single loc. T is the
// match-relative time (seconds) the player entered the loc — i.e. the
// transition time of the edge that led into it. Adjacent residences in
// a run always differ in Loc.
type LocResidence struct {
	Loc string  `json:"loc"`
	T   float64 `json:"t"`
}

// LocEdgePasses reconstructs the individual loc-graph edge traversals
// from each player's native-rate PositionTrack. It walks the track with
// the SAME reset semantics as analyzer.BuildLocGraph — the cursor resets
// at every spawn / death boundary and at no-loc (Li==0) samples — so the
// set of (from, to) transitions it emits matches the aggregate loc-graph
// edges exactly (Full-time metric; combat-posture conditioning is not
// applied). Teleport classification is omitted because it only labels
// edges, never adds or drops them.
//
// Output is per-player runs of residences rather than pre-flattened
// edges so the consumer can re-group into N-edge passes without another
// query.
func LocEdgePasses(r *result.Result, opts LocEdgePassesOptions) (*LocEdgePassesView, error) {
	out := &LocEdgePassesView{}
	if r == nil || r.Streams == nil || r.TimelineAnalysis == nil {
		return out, nil
	}
	locTable := r.TimelineAnalysis.LocTable
	if len(locTable) == 0 {
		return out, nil
	}
	resolveLoc := func(li int16) string {
		if li > 0 && int(li) < len(locTable) {
			return locTable[li]
		}
		return ""
	}
	pf := newPlayerFilter(opts.Players)

	for _, p := range r.Streams.Players {
		if !pf.accepts(p.Name) {
			continue
		}
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}
		boundaries := mergeSortedBoundaries(p.Spawns, p.Deaths)
		bIdx := 0

		var (
			runs    [][]LocResidence
			run     []LocResidence
			curLoc  string
			haveRun bool
		)
		// endRun closes the in-progress run. A run needs at least two
		// residences to contain an edge; shorter runs are discarded.
		endRun := func() {
			if len(run) >= 2 {
				runs = append(runs, run)
			}
			run = nil
			curLoc = ""
			haveRun = false
		}

		for i := range pt.T {
			t := pt.T[i] // int32 ms
			// Reset across every spawn / death boundary we've passed, so a
			// death-then-respawn never chains into a spurious edge.
			for bIdx < len(boundaries) && boundaries[bIdx] <= t {
				endRun()
				bIdx++
			}

			li := pt.Li[i]
			if li == 0 {
				endRun()
				continue
			}
			locName := resolveLoc(li)
			if locName == "" {
				endRun()
				continue
			}

			if !haveRun {
				curLoc = locName
				run = []LocResidence{{Loc: locName, T: float64(t) * 0.001}}
				haveRun = true
				continue
			}
			if locName != curLoc {
				run = append(run, LocResidence{Loc: locName, T: float64(t) * 0.001})
				curLoc = locName
			}
		}
		endRun()

		if len(runs) > 0 {
			out.Players = append(out.Players, PlayerLocRuns{Name: p.Name, Runs: runs})
		}
	}
	return out, nil
}

// mergeSortedBoundaries merges two individually-ascending int32 slices
// into one ascending slice. Mirrors analyzer.mergeBoundaries (unexported
// there); spawns and deaths each arrive in time order.
func mergeSortedBoundaries(spawns, deaths []int32) []int32 {
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
