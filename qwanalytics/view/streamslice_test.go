package view

import (
	"testing"

	"github.com/mvd-analyzer/qwanalytics/result"
)

func TestStreamSliceCarryForward(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Health: []result.ChangeI16{
			{T: 0, V: 100},
			{T: 5, V: 50},
		},
	})
	v, err := StreamSlice(r, StreamSliceOptions{
		StartTime: 2,
		EndTime:   4,
		Fields:    []string{FieldHealth},
	})
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	if len(v.Players) != 1 {
		t.Fatalf("len players = %d, want 1", len(v.Players))
	}
	h := v.Players[0].Health
	// Window has no native entry; carry-forward synthesises one at StartTime.
	if len(h) != 1 || h[0].T != 2 || h[0].V != 100 {
		t.Fatalf("expected 1 entry at t=2 v=100, got %+v", h)
	}
}

func TestStreamSliceIntervalClamping(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		RL:   []result.Interval{{Start: 1, End: 6}},
	})
	v, err := StreamSlice(r, StreamSliceOptions{
		StartTime: 2,
		EndTime:   4,
		Fields:    []string{FieldRL},
	})
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	rl := v.Players[0].RL
	if len(rl) != 1 {
		t.Fatalf("len rl = %d, want 1", len(rl))
	}
	if rl[0].Start != 2 || rl[0].End != 4 {
		t.Fatalf("clamped interval = %+v, want [2,4)", rl[0])
	}
}

func TestStreamSlicePosition(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Position: &result.PositionTrack{
			T: []float32{0, 1, 2, 3, 4},
			X: []int32{0, 100, 200, 300, 400},
			Y: []int32{0, 0, 0, 0, 0},
			Z: []int32{0, 0, 0, 0, 0},
		},
	})
	v, err := StreamSlice(r, StreamSliceOptions{
		StartTime: 1.5,
		EndTime:   3.5,
		Fields:    []string{FieldPosition},
	})
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	pos := v.Players[0].Position
	if pos == nil {
		t.Fatalf("Position nil")
	}
	// Should include samples at t=2 and t=3.
	if len(pos.T) != 2 {
		t.Fatalf("len pos = %d, want 2", len(pos.T))
	}
	if pos.X[0] != 200 || pos.X[1] != 300 {
		t.Fatalf("positions = %v, want [200, 300]", pos.X)
	}
}
