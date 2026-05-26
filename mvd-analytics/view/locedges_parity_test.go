package view_test

// Parity check: the per-direction edge multiset reconstructed from
// view.LocEdgePasses must equal the aggregate edges produced by
// analyzer.BuildLocGraph. The Debug tab promises "all single edges in
// locgraph", so the two walks have to agree on which (from, to)
// transitions exist and how many times. Lives in an external test
// package because it imports the analyzer (which imports view) — an
// internal test would create an import cycle (mirrors
// columnar_parity_test.go).

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

func track(samples ...[2]int) *result.PositionTrack {
	pt := &result.PositionTrack{}
	for i, s := range samples {
		pt.T = append(pt.T, int32(s[0]))
		pt.Li = append(pt.Li, int16(s[1]))
		// X/Y values don't affect edge membership (only the teleport
		// label, which BuildLocGraph keys the same edge under), so a
		// monotone ramp is fine.
		pt.X = append(pt.X, int32(i*10))
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
	}
	return pt
}

func TestLocEdgePassesMatchesLocGraph(t *testing.T) {
	locTable := []string{"", "a", "b", "c", "d"}
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Deaths: []int32{250}, // splits c→d so it is never an edge
					Position: track(
						[2]int{0, 1},   // a
						[2]int{50, 1},  // a (dwell)
						[2]int{100, 2}, // b   edge a→b
						[2]int{150, 1}, // a   edge b→a (reverse direction)
						[2]int{200, 3}, // c   edge a→c
						// death at 250 → reset
						[2]int{300, 4}, // d   (no c→d edge across death)
						[2]int{350, 2}, // b   edge d→b
					),
				},
				{
					Name: "p2",
					Position: track(
						[2]int{0, 2},   // b
						[2]int{100, 0}, // no-loc gap → reset
						[2]int{200, 3}, // c   (no b→c edge across gap)
						[2]int{300, 4}, // d   edge c→d
						[2]int{400, 3}, // c   edge d→c
					),
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: locTable},
	}

	// Aggregate edges from the loc-graph builder.
	graph := analyzer.BuildLocGraph(r)
	if graph == nil {
		t.Fatal("BuildLocGraph returned nil")
	}
	want := make(map[string]int)
	for _, e := range graph.Edges {
		want[e.From+"->"+e.To] += e.Total
	}

	// Edge multiset reconstructed from the residence runs.
	v, err := view.LocEdgePasses(r, view.LocEdgePassesOptions{})
	if err != nil {
		t.Fatalf("LocEdgePasses: %v", err)
	}
	got := make(map[string]int)
	for _, p := range v.Players {
		for _, run := range p.Runs {
			for j := 0; j+1 < len(run); j++ {
				got[run[j].Loc+"->"+run[j+1].Loc]++
			}
		}
	}

	if len(want) == 0 {
		t.Fatal("expected some edges, got none from BuildLocGraph")
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("edge %q: passes=%d, locgraph=%d", k, got[k], n)
		}
	}
	for k, n := range got {
		if want[k] != n {
			t.Errorf("edge %q present in passes (%d) but not locgraph (%d)", k, n, want[k])
		}
	}
}
