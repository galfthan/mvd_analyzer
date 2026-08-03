package view

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestStateAtCarryForward(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Health: []result.ChangeI16{
			{T: 0, V: 100},
			{T: 5000, V: 50},
		},
		RL: []result.Interval{{Start: 1000, End: 3000}},
	})
	v, err := StateAt(r, StateAtOptions{
		Time:   2500,
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

// StateAt snaps view / hgt / lq to the nearest position sample, and
// omits hgt/lq when the track lacks those columns (no BSP).
func TestStateAtViewHeightLiquid(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Position: &result.PositionTrack{
			T:   []int32{0, 1000, 2000},
			X:   []float32{0, 100, 200},
			Y:   []float32{0, 0, 0},
			Z:   []float32{0, 0, 0},
			H:   []float32{5, 40, result.NoFloor},
			Lq:  []int8{0, 5, 7},
			VP:  []int16{10, 20, 30},
			VYa: []int16{-10, -20, -30},
			VX:  []float32{100, 200, 300},
			VY:  []float32{-100, -200, -300},
			VZ:  []float32{1, 2, 3},
		},
	})
	v, err := StateAt(r, StateAtOptions{
		Time:   1100, // nearest sample is index 1 (t=1000)
		Fields: []string{FieldView, FieldHeight, FieldLiquid, FieldVelocity},
	})
	if err != nil {
		t.Fatalf("StateAt: %v", err)
	}
	st := v.Players["p1"]
	if st.View == nil || st.View.VP != 20 || st.View.VYa != -20 {
		t.Errorf("View at 1.1 = %+v, want {20,-20}", st.View)
	}
	if st.Hgt == nil || *st.Hgt != 40 {
		t.Errorf("Hgt at 1.1 = %v, want 40", deref32(st.Hgt))
	}
	if st.Lq == nil || *st.Lq != 5 {
		t.Errorf("Lq at 1.1 = %v, want 5", st.Lq)
	}
	if st.Vel == nil || st.Vel.VX != 200 || st.Vel.VY != -200 || st.Vel.VZ != 2 {
		t.Errorf("Vel at 1.1 = %+v, want {200,-200,2}", st.Vel)
	}

	// Bare x/y/z track: view present (always recorded), but hgt/lq absent.
	r2 := makeStream(t, result.PlayerStream{
		Name: "p1",
		Position: &result.PositionTrack{
			T:   []int32{0, 1000},
			X:   []float32{0, 100},
			Y:   []float32{0, 0},
			Z:   []float32{0, 0},
			VP:  []int16{1, 2},
			VYa: []int16{3, 4},
		},
	})
	v2, _ := StateAt(r2, StateAtOptions{Time: 0, Fields: []string{FieldView, FieldHeight, FieldLiquid, FieldVelocity}})
	st2 := v2.Players["p1"]
	if st2.View == nil {
		t.Errorf("view should be present on bare track")
	}
	if st2.Hgt != nil || st2.Lq != nil || st2.Vel != nil {
		t.Errorf("hgt/lq/vel must be absent without their columns: hgt=%v lq=%v vel=%v", st2.Hgt, st2.Lq, st2.Vel)
	}
}

func deref32(p *float32) float32 {
	if p == nil {
		return -1
	}
	return *p
}

func TestStateAtBeforeFirstSample(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name:   "p1",
		Health: []result.ChangeI16{{T: 5000, V: 100}},
	})
	v, err := StateAt(r, StateAtOptions{
		Time:   2000,
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
		Quad: []result.Interval{{Start: 1000, End: 2000}},
	})
	// At end boundary (half-open): Time=2.0 should NOT be in interval.
	v, _ := StateAt(r, StateAtOptions{Time: 2000, Fields: []string{FieldQuad}})
	st := v.Players["p1"]
	if st.Quad == nil || *st.Quad != false {
		t.Fatalf("Quad at end boundary = %v, want false", st.Quad)
	}
	// At start boundary (closed): should be true.
	v, _ = StateAt(r, StateAtOptions{Time: 1000, Fields: []string{FieldQuad}})
	st = v.Players["p1"]
	if st.Quad == nil || *st.Quad != true {
		t.Fatalf("Quad at start boundary = %v, want true", st.Quad)
	}
}

func TestStateAtSpawnDeathRejected(t *testing.T) {
	r := makeStream(t, result.PlayerStream{Name: "p1"})
	_, err := StateAt(r, StateAtOptions{Time: 1000, Fields: []string{FieldSpawns}})
	if err == nil {
		t.Fatalf("expected error for FieldSpawns in StateAt")
	}
}

func TestStateAtLocResolvesName(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{{
				Name: "p1",
				Loc:  []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 2}},
			}},
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10000},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "rl", "ya"}},
	}
	v, err := StateAt(r, StateAtOptions{Time: 2500, Fields: []string{FieldLoc}})
	if err != nil {
		t.Fatalf("StateAt: %v", err)
	}
	if got := v.Players["p1"].Loc; got == nil || *got != "rl" {
		t.Fatalf("Loc at 2.5 = %v, want rl", got)
	}
	v, _ = StateAt(r, StateAtOptions{Time: 6000, Fields: []string{FieldLoc}})
	if got := v.Players["p1"].Loc; got == nil || *got != "ya" {
		t.Fatalf("Loc at 6 = %v, want ya", got)
	}
	// Index mode → raw LocTable index in Li, Loc nil.
	vi, _ := StateAt(r, StateAtOptions{Time: 2500, Fields: []string{FieldLoc}, LocIndex: true})
	st := vi.Players["p1"]
	if st.Li == nil || *st.Li != 1 {
		t.Fatalf("Li at 2.5 = %v, want 1", st.Li)
	}
	if st.Loc != nil {
		t.Fatalf("Loc should be nil in index mode, got %v", *st.Loc)
	}
}

// `alive` is the stored liveness evaluated at the instant, with three
// states: true / false when it was measured, null when it was not. It is
// not field-gated, so the key is always there to carry that null.
func TestStateAtAlive(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name:  "p1",
		Alive: []result.Interval{{Start: 1000, End: 2000}, {Start: 2000, End: 4000}},
	})
	for _, tc := range []struct {
		at   int32
		want bool
	}{
		{500, false},  // before the first life
		{1000, true},  // start boundary is closed
		{2000, true},  // the touching boundary of a same-ms death+respawn
		{4000, false}, // end boundary is open
		{5000, false}, // after the last life
	} {
		v, err := StateAt(r, StateAtOptions{Time: tc.at, Fields: []string{FieldHealth}})
		if err != nil {
			t.Fatalf("StateAt(%d): %v", tc.at, err)
		}
		got := v.Players["p1"].Alive
		if got == nil || *got != tc.want {
			t.Errorf("alive at %d = %v, want %v", tc.at, got, tc.want)
		}
	}

	// Measured, never alive → false, not null.
	rEmpty := makeStream(t, result.PlayerStream{Name: "p1", Alive: []result.Interval{}})
	v, _ := StateAt(rEmpty, StateAtOptions{Time: 1000, Fields: []string{FieldHealth}})
	if got := v.Players["p1"].Alive; got == nil || *got != false {
		t.Errorf("alive on a measured-never-alive player = %v, want false", got)
	}

	// Not measurable → null, and the key survives to the wire.
	rNil := makeStream(t, result.PlayerStream{Name: "p1"})
	v, _ = StateAt(rNil, StateAtOptions{Time: 1000, Fields: []string{FieldHealth}})
	if got := v.Players["p1"].Alive; got != nil {
		t.Errorf("alive on an unmeasurable liveness = %v, want nil", *got)
	}
	b, err := json.Marshal(v.Players["p1"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"alive":null`) {
		t.Errorf("row marshalled to %s, want it to contain \"alive\":null", b)
	}
}

// `alive` is not field-gated — the sibling of
// TestStreamSliceAliveIsNotFieldGated. Every case above happens to request
// FieldHealth, so a gate on any requested field would have survived them all;
// this one asks for a single unrelated, non-positional field and still expects
// liveness, because the key cannot be omitted without null meaning both "not
// requested" and "not measurable".
func TestStateAtAliveIsNotFieldGated(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name:  "p1",
		Alive: []result.Interval{{Start: 0, End: 5000}},
	})
	v, err := StateAt(r, StateAtOptions{Time: 2000, Fields: []string{FieldRL}})
	if err != nil {
		t.Fatalf("StateAt: %v", err)
	}
	st := v.Players["p1"]
	if st.Alive == nil || *st.Alive != true {
		t.Errorf("alive = %v on a fields=[rl] request, want true", st.Alive)
	}
	if st.Health != nil {
		t.Errorf("health = %v on a fields=[rl] request, want nil — the gate under test must stay a real one", *st.Health)
	}
}

// posAgeMs publishes how old the snapped position sample is, so a consumer
// can apply the occupancy walkers' staleness rule (drop at
// |age| >= result.SampleStaleCapMs) to a carry-forward this endpoint
// deliberately leaves unbounded.
func TestStateAtPosAgeMs(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Position: &result.PositionTrack{
			T: []int32{0, 1000},
			X: []float32{0, 100},
			Y: []float32{0, 0},
			Z: []float32{0, 0},
		},
	})
	for _, tc := range []struct {
		name string
		at   int32
		want int32
	}{
		{"on the sample", 1000, 0},
		{"one ms short of the cap", 1000 + result.SampleStaleCapMs - 1, result.SampleStaleCapMs - 1},
		{"at the cap (the walkers already reject here)", 1000 + result.SampleStaleCapMs, result.SampleStaleCapMs},
		{"past the cap", 60000, 59000},
		// Before the first sample the nearest one is a LATER sample, which
		// the sign records rather than hides.
		{"snapped back to a later sample", 0 - 120, -120},
	} {
		v, err := StateAt(r, StateAtOptions{Time: tc.at, Fields: []string{FieldPosition}})
		if err != nil {
			t.Fatalf("StateAt(%s): %v", tc.name, err)
		}
		st := v.Players["p1"]
		if st.Pos == nil {
			t.Fatalf("%s: pos absent — the carry-forward itself must not change", tc.name)
		}
		if st.PosAgeMs == nil || *st.PosAgeMs != tc.want {
			t.Errorf("%s: posAgeMs = %v, want %d", tc.name, st.PosAgeMs, tc.want)
		}
	}

	// No positional field requested (and no track at all) → no age to report.
	v, _ := StateAt(r, StateAtOptions{Time: 1000, Fields: []string{FieldHealth}})
	if got := v.Players["p1"].PosAgeMs; got != nil {
		t.Errorf("posAgeMs = %d without a positional field, want absent", *got)
	}
	rNoTrack := makeStream(t, result.PlayerStream{Name: "p1"})
	v, _ = StateAt(rNoTrack, StateAtOptions{Time: 1000, Fields: []string{FieldPosition}})
	if got := v.Players["p1"].PosAgeMs; got != nil {
		t.Errorf("posAgeMs = %d without a position track, want absent", *got)
	}
}

func deref(p *int16) int16 {
	if p == nil {
		return -1
	}
	return *p
}
