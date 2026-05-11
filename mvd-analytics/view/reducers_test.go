package view

import (
	"math"
	"testing"
)

func TestLastReducer(t *testing.T) {
	r := LastReducer{}
	if got := r.Apply([]Sample{{T: 1, V: 10}, {T: 2, V: 20}, {T: 3, V: 30}}); got != 30 {
		t.Fatalf("Last over [10,20,30] = %v, want 30", got)
	}
	if got := r.Apply(nil); got != nil {
		t.Fatalf("Last over empty = %v, want nil", got)
	}
}

func TestFirstReducer(t *testing.T) {
	r := FirstReducer{}
	if got := r.Apply([]Sample{{T: 1, V: 10}, {T: 2, V: 20}}); got != 10 {
		t.Fatalf("First over [10,20] = %v, want 10", got)
	}
	if got := r.Apply(nil); got != nil {
		t.Fatalf("First over empty = %v, want nil", got)
	}
}

func TestMeanReducer(t *testing.T) {
	r := MeanReducer{}
	got := r.Apply([]Sample{{V: 10}, {V: 20}, {V: 30}})
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("Mean returned non-float64: %T", got)
	}
	if math.Abs(f-20) > 1e-9 {
		t.Fatalf("Mean over [10,20,30] = %v, want 20", f)
	}
	if r.Apply(nil) != nil {
		t.Fatalf("Mean over empty != nil")
	}
	// Non-numeric input → nil.
	if r.Apply([]Sample{{V: "ya"}}) != nil {
		t.Fatalf("Mean over strings != nil")
	}
}

func TestMinMaxReducer(t *testing.T) {
	min := MinReducer{}.Apply([]Sample{{V: 30}, {V: 10}, {V: 20}})
	max := MaxReducer{}.Apply([]Sample{{V: 30}, {V: 10}, {V: 20}})
	if min != 10.0 {
		t.Fatalf("Min = %v want 10", min)
	}
	if max != 30.0 {
		t.Fatalf("Max = %v want 30", max)
	}
}

func TestDominantReducer(t *testing.T) {
	r := DominantReducer{}
	got := r.Apply([]Sample{{V: "a"}, {V: "b"}, {V: "a"}, {V: "c"}})
	if got != "a" {
		t.Fatalf("Dominant = %v, want a", got)
	}
	// All-tied: the latest index wins.
	got = r.Apply([]Sample{{V: "a"}, {V: "b"}, {V: "c"}})
	if got != "c" {
		t.Fatalf("Tied dominant = %v, want c (latest)", got)
	}
	if r.Apply(nil) != nil {
		t.Fatalf("Dominant over empty != nil")
	}
}

func TestHeldAnyReducer(t *testing.T) {
	r := HeldAnyReducer{}
	if r.Apply([]Sample{{V: false}, {V: false}, {V: true}}) != true {
		t.Fatalf("HeldAny([F,F,T]) != true")
	}
	if r.Apply([]Sample{{V: false}, {V: false}}) != false {
		t.Fatalf("HeldAny([F,F]) != false")
	}
	if r.Apply(nil) != false {
		t.Fatalf("HeldAny(nil) != false")
	}
}

func TestMajorityReducer(t *testing.T) {
	r := MajorityReducer{}
	if r.Apply([]Sample{{V: true}, {V: true}, {V: false}}) != true {
		t.Fatalf("Majority([T,T,F]) != true (2/3)")
	}
	if r.Apply([]Sample{{V: true}, {V: false}, {V: false}}) != false {
		t.Fatalf("Majority([T,F,F]) != false (1/3)")
	}
	// Exactly 50 % counts as majority.
	if r.Apply([]Sample{{V: true}, {V: false}}) != true {
		t.Fatalf("Majority([T,F]) at 1/2 != true")
	}
}

func TestAnyReducer(t *testing.T) {
	r := AnyReducer{}
	if r.Apply([]Sample{{T: 1, V: nil}}) != true {
		t.Fatalf("Any single sample != true")
	}
	if r.Apply(nil) != false {
		t.Fatalf("Any empty != false")
	}
}

func TestLookupReducer(t *testing.T) {
	if _, err := LookupReducer("last"); err != nil {
		t.Fatalf("LookupReducer(last) errored: %v", err)
	}
	if _, err := LookupReducer("garbage"); err == nil {
		t.Fatalf("LookupReducer(garbage) accepted unknown name")
	}
}
