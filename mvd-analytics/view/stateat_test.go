package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestStateAtCarryForward(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Health: []result.ChangeI16{
			{T: 0, V: 100},
			{T: 5, V: 50},
		},
		RL: []result.Interval{{Start: 1, End: 3}},
	})
	v, err := StateAt(r, StateAtOptions{
		Time:   2.5,
		Fields: []string{FieldHealth, FieldRL},
	})
	if err != nil {
		t.Fatalf("StateAt: %v", err)
	}
	st := v.Players["p1"]
	if st.Health == nil || *st.Health != 100 {
		t.Fatalf("Health at 2.5 = %v, want 100", deref(st.Health))
	}
	if st.RL == nil || *st.RL != true {
		t.Fatalf("RL at 2.5 = %v, want true", st.RL)
	}
}

func TestStateAtBeforeFirstSample(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name:   "p1",
		Health: []result.ChangeI16{{T: 5, V: 100}},
	})
	v, err := StateAt(r, StateAtOptions{
		Time:   2.0,
		Fields: []string{FieldHealth},
	})
	if err != nil {
		t.Fatalf("StateAt: %v", err)
	}
	st := v.Players["p1"]
	if st.Health != nil {
		t.Fatalf("Health pointer not nil before first sample: got %d", *st.Health)
	}
}

func TestStateAtIntervalBoundary(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Quad: []result.Interval{{Start: 1.0, End: 2.0}},
	})
	// At end boundary (half-open): Time=2.0 should NOT be in interval.
	v, _ := StateAt(r, StateAtOptions{Time: 2.0, Fields: []string{FieldQuad}})
	st := v.Players["p1"]
	if st.Quad == nil || *st.Quad != false {
		t.Fatalf("Quad at end boundary = %v, want false", st.Quad)
	}
	// At start boundary (closed): should be true.
	v, _ = StateAt(r, StateAtOptions{Time: 1.0, Fields: []string{FieldQuad}})
	st = v.Players["p1"]
	if st.Quad == nil || *st.Quad != true {
		t.Fatalf("Quad at start boundary = %v, want true", st.Quad)
	}
}

func TestStateAtSpawnDeathRejected(t *testing.T) {
	r := makeStream(t, result.PlayerStream{Name: "p1"})
	_, err := StateAt(r, StateAtOptions{Time: 1, Fields: []string{FieldSpawns}})
	if err == nil {
		t.Fatalf("expected error for FieldSpawns in StateAt")
	}
}

func deref(p *int16) int16 {
	if p == nil {
		return -1
	}
	return *p
}
