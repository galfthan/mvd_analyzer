package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// locTable shared by the LocEdgePasses tests: index 0 is the no-loc
// sentinel, 1..3 are named locs.
var edgeLocTable = []string{"", "rl", "ya", "mh"}

func posTrack(samples ...[2]int) *result.PositionTrack {
	pt := &result.PositionTrack{}
	for _, s := range samples {
		pt.T = append(pt.T, int32(s[0]))
		pt.Li = append(pt.Li, int16(s[1]))
		pt.X = append(pt.X, 0)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
	}
	return pt
}

func TestLocEdgePassesRun(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{
					Name: "p1",
					// rl (held a few samples) → ya → mh
					Position: posTrack(
						[2]int{0, 1}, [2]int{50, 1},
						[2]int{100, 2},
						[2]int{200, 3},
					),
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: edgeLocTable},
	}
	v, err := LocEdgePasses(r, LocEdgePassesOptions{})
	if err != nil {
		t.Fatalf("LocEdgePasses: %v", err)
	}
	if len(v.Players) != 1 || v.Players[0].Name != "p1" {
		t.Fatalf("players = %+v", v.Players)
	}
	runs := v.Players[0].Runs
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	run := runs[0]
	if len(run) != 3 {
		t.Fatalf("run len = %d, want 3 (rl→ya→mh)", len(run))
	}
	if run[0].Loc != "rl" || run[0].T != 0 {
		t.Fatalf("run[0] = %+v", run[0])
	}
	if run[1].Loc != "ya" || run[1].T != 0.1 {
		t.Fatalf("run[1] = %+v", run[1])
	}
	if run[2].Loc != "mh" || run[2].T != 0.2 {
		t.Fatalf("run[2] = %+v", run[2])
	}
}

// A death/spawn boundary between two locs must split the run so the
// cross-death transition is never emitted as an edge.
func TestLocEdgePassesBoundarySplits(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Deaths: []int32{150},
					Position: posTrack(
						[2]int{0, 1},   // rl
						[2]int{100, 2}, // ya  (edge rl→ya, pre-death)
						[2]int{200, 3}, // mh  (post-death; must NOT chain ya→mh)
						[2]int{300, 1}, // rl  (edge mh→rl, post-death)
					),
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: edgeLocTable},
	}
	v, _ := LocEdgePasses(r, LocEdgePassesOptions{})
	runs := v.Players[0].Runs
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (split at death)", len(runs))
	}
	if len(runs[0]) != 2 || runs[0][0].Loc != "rl" || runs[0][1].Loc != "ya" {
		t.Fatalf("runs[0] = %+v", runs[0])
	}
	if len(runs[1]) != 2 || runs[1][0].Loc != "mh" || runs[1][1].Loc != "rl" {
		t.Fatalf("runs[1] = %+v", runs[1])
	}
}

// A no-loc (Li==0) gap breaks the run.
func TestLocEdgePassesGapBreaks(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{
					Name: "p1",
					Position: posTrack(
						[2]int{0, 1},   // rl
						[2]int{100, 0}, // no loc — break
						[2]int{200, 2}, // ya (new run, single residence → dropped)
					),
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: edgeLocTable},
	}
	v, _ := LocEdgePasses(r, LocEdgePassesOptions{})
	if len(v.Players) != 0 {
		// Both candidate runs are single-residence, so nothing survives.
		t.Fatalf("players = %+v, want none (no run has >=2 residences)", v.Players)
	}
}

func TestLocEdgePassesPlayerFilter(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{Name: "p1", Position: posTrack([2]int{0, 1}, [2]int{100, 2})},
				{Name: "p2", Position: posTrack([2]int{0, 2}, [2]int{100, 3})},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: edgeLocTable},
	}
	v, _ := LocEdgePasses(r, LocEdgePassesOptions{Players: []string{"p2"}})
	if len(v.Players) != 1 || v.Players[0].Name != "p2" {
		t.Fatalf("players = %+v, want only p2", v.Players)
	}
}
