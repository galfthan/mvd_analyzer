package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestStreamSliceCarryForward(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Health: []result.ChangeI16{
			{T: 0, V: 100},
			{T: 5000, V: 50},
		},
	})
	v, err := StreamSlice(r, StreamSliceOptions{
		StartTime: 2,
		EndTime:   4,
		Fields:    []string{FieldHealth},
	}) // StartTime/EndTime are seconds (public API)
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	if len(v.Players) != 1 {
		t.Fatalf("len players = %d, want 1", len(v.Players))
	}
	h := v.Players[0].Health
	// Window has no native entry; carry-forward synthesises one at
	// StartTime (2000 ms in schema v8).
	if len(h) != 1 || h[0].T != 2000 || h[0].V != 100 {
		t.Fatalf("expected 1 entry at t=2000ms v=100, got %+v", h)
	}
}

func TestStreamSliceIntervalClamping(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		RL:   []result.Interval{{Start: 1000, End: 6000}},
	})
	v, err := StreamSlice(r, StreamSliceOptions{
		StartTime: 2,
		EndTime:   4,
		Fields:    []string{FieldRL},
	}) // StartTime/EndTime are seconds (public API)
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	rl := v.Players[0].RL
	if len(rl) != 1 {
		t.Fatalf("len rl = %d, want 1", len(rl))
	}
	// Clamped to [2000, 4000) ms (schema v8: Interval is int32 ms).
	if rl[0].Start != 2000 || rl[0].End != 4000 {
		t.Fatalf("clamped interval = %+v, want [2000,4000)", rl[0])
	}
}

func TestStreamSlicePosition(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Position: &result.PositionTrack{
			// Schema v8: T is int32 ms. Samples at 0, 1s, 2s, 3s, 4s.
			T: []int32{0, 1000, 2000, 3000, 4000},
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
	// Should include samples at t=2 and t=3 (i.e. 2000 ms, 3000 ms).
	if len(pos.T) != 2 {
		t.Fatalf("len pos = %d, want 2", len(pos.T))
	}
	if pos.X[0] != 200 || pos.X[1] != 300 {
		t.Fatalf("positions = %v, want [200, 300]", pos.X)
	}
	if pos.T[0] != 2000 || pos.T[1] != 3000 {
		t.Fatalf("pos.T = %v, want [2000, 3000]", pos.T)
	}
}

func TestStreamSliceLocResolvesNames(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{{
				Name: "p1",
				Loc:  []result.ChangeI16{{T: 0, V: 1}, {T: 3000, V: 2}, {T: 7000, V: 1}},
			}},
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10000},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "rl", "ya"}},
	}
	v, err := StreamSlice(r, StreamSliceOptions{StartTime: 0, EndTime: 10, Fields: []string{FieldLoc}})
	if err != nil {
		t.Fatalf("StreamSlice: %v", err)
	}
	loc := v.Players[0].Loc
	wantV := []string{"rl", "ya", "rl"}
	wantT := []int32{0, 3000, 7000}
	if len(loc) != len(wantV) {
		t.Fatalf("got %d loc entries, want %d: %+v", len(loc), len(wantV), loc)
	}
	for i := range wantV {
		if loc[i].V != wantV[i] || loc[i].T != wantT[i] {
			t.Fatalf("loc[%d] = {T:%d V:%q}, want {T:%d V:%q}", i, loc[i].T, loc[i].V, wantT[i], wantV[i])
		}
	}
}
