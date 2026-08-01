package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// A loc residence is DWELL — presence — so /loc-trails is alive-gated exactly
// like loc-graph node time. Without this the corpse travels that loc-graph
// excludes would still surface here as dwell, and the two endpoints would
// answer the same question differently on the same demo.
func TestLocTrailsExcludeDeadDwell(t *testing.T) {
	// In "mid" throughout, but dead from 10s to 20s.
	pt := &result.PositionTrack{}
	for ms := int32(0); ms <= 30000; ms += 13 {
		pt.T = append(pt.T, ms)
		pt.X = append(pt.X, 0)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
		pt.Li = append(pt.Li, 1)
	}
	p := result.PlayerStream{
		Name:     "p",
		Position: pt,
		Loc:      []result.ChangeI16{{T: 0, V: 1}},
		Alive:    []result.Interval{{Start: 0, End: 10000}, {Start: 20000, End: 30000}},
	}
	res := &result.Result{
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 30000},
			Players: []result.PlayerStream{p},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "mid"}},
	}

	got, err := LocTrails(res, LocTrailsOptions{})
	if err != nil {
		t.Fatalf("LocTrails: %v", err)
	}
	if len(got.Players) != 1 {
		t.Fatalf("got %d players, want 1", len(got.Players))
	}
	seq := got.Players[0].Sequence
	if len(seq) != 2 {
		t.Fatalf("got %d residences %v, want 2 (split by the dead period)", len(seq), seq)
	}
	var dwell int32
	for _, e := range seq {
		dwell += e.End - e.Start
		if e.Start < 10000 && e.End > 10000 {
			t.Errorf("residence %v straddles the death at 10000 ms", e)
		}
	}
	// 20 s of life, not the 30 s the samples cover.
	if dwell > 21000 {
		t.Errorf("total dwell %d ms; the 10 s dead period was counted", dwell)
	}
}

// A nil Alive means liveness was not measurable, and the honest response to
// "unknown" is to degrade rather than to drop — the same rule the occupancy
// walkers apply. Blanking a demo's whole trail would be worse than the bug the
// gate exists to fix.
func TestLocTrailsUngatedWhenLivenessUnmeasured(t *testing.T) {
	pt := &result.PositionTrack{
		T: []int32{0, 13, 26}, X: []float32{0, 0, 0}, Y: []float32{0, 0, 0},
		Z: []float32{0, 0, 0}, Li: []int16{1, 1, 1},
	}
	res := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 1000},
			Players: []result.PlayerStream{{
				Name: "p", Position: pt,
				Loc:   []result.ChangeI16{{T: 0, V: 1}},
				Alive: nil,
			}},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "mid"}},
	}
	got, err := LocTrails(res, LocTrailsOptions{})
	if err != nil {
		t.Fatalf("LocTrails: %v", err)
	}
	if len(got.Players) != 1 || len(got.Players[0].Sequence) == 0 {
		t.Fatal("nil Alive blanked the trail; it should degrade to ungated")
	}
}
