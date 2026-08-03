package aimcore

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Aim attributes a miss to the nearest enemy who is ALIVE at the fire time
// (aimAttribute), reading PlayerStream.Alive through aimAliveAt. That field is
// three-state, and the three states must stay distinct here: nil means
// liveness was not measurable and degrades to "alive" — dropping every shot on
// a demo whose lives could not be derived would be far worse than attributing
// to a player who may have been dead — while an empty non-nil list is the
// measurement "never alive in the window" and gates every shot.
func TestAimLivenessMeasurednessStates(t *testing.T) {
	ts := []int32{0, 500, 1000}
	shooter := &result.PositionTrack{
		T:   ts,
		X:   []float32{0, 0, 0},
		Y:   []float32{0, 0, 0},
		Z:   []float32{0, 0, 0},
		VP:  []int16{0, 0, 0}, // level
		VYa: []int16{0, 0, 0}, // facing +x, straight at the target
	}
	target := &result.PositionTrack{
		T: ts,
		X: []float32{200, 200, 200},
		Y: []float32{0, 0, 0},
		Z: []float32{0, 0, 0},
	}

	// One lg miss at t=500, with no confirmed victim — so the target is chosen
	// by the liveness-gated nearest-crosshair path, not named by the server.
	crosshairSamples := func(alive []result.Interval) int {
		res := &result.Result{
			Shots: &result.ShotsResult{
				Shots: []result.Shot{{Time: 500, Player: "A", Weapon: "lg", Source: "beam"}},
			},
			Streams: &result.Streams{
				Players: []result.PlayerStream{
					{Name: "A", Position: shooter, Alive: []result.Interval{{Start: 0, End: 1000}}},
					{Name: "B", Position: target, Alive: alive},
				},
			},
		}
		ar := Compute(res, Query{})
		if ar == nil {
			return 0
		}
		for _, pa := range ar.Players {
			if pa.Player == "A" && pa.Crosshair != nil {
				return len(pa.Crosshair.T)
			}
		}
		return 0
	}

	if got := crosshairSamples(nil); got != 1 {
		t.Errorf("crosshair samples = %d with an UNMEASURABLE opponent liveness, want 1 — "+
			"unknown must degrade to alive, not silently drop the shot", got)
	}
	if got := crosshairSamples([]result.Interval{{Start: 0, End: 1000}}); got != 1 {
		t.Errorf("crosshair samples = %d against a live opponent, want 1", got)
	}
	if got := crosshairSamples([]result.Interval{}); got != 0 {
		t.Errorf("crosshair samples = %d against an opponent measured as never alive, want 0", got)
	}
	if got := crosshairSamples([]result.Interval{{Start: 0, End: 400}}); got != 0 {
		t.Errorf("crosshair samples = %d against an opponent dead since t=400, want 0 — "+
			"a corpse is not an aim target", got)
	}
}
