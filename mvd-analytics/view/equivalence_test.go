package view

import (
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Plan v3 §8.1 / §8.2 equivalence tests. We avoid the v6-fixture
// approach (no v6 binary in a Phase 1 branch) and instead pin two
// simpler invariants that don't need a reference oracle:
//
//   - Semantic invariant: bucket count = ceil((MatchEnd - MatchStart) / windowSec).
//   - Round-trip identity: synthesise a Result from a BucketsView and
//     re-bucket it; the two BucketsViews should be equal in shape.

func TestBucketsCountInvariant(t *testing.T) {
	cases := []struct {
		name     string
		start    int32 // ms
		end      int32 // ms
		windowMs int
		want     int
	}{
		{"exact division", 0, 10000, 1000, 10},
		{"partial tail", 0, 10000, 3000, 4},
		{"sub-second", 0, 250, 50, 5},
		{"single bucket", 0, 1000, 1000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &result.Result{
				Streams: &result.Streams{
					Global:  result.GlobalStream{MatchStart: tc.start, MatchEnd: tc.end},
					Players: []result.PlayerStream{{Name: "p1"}},
				},
			}
			bv, err := Buckets(r, BucketsOptions{
				WindowMs: tc.windowMs,
				Fields:   []string{FieldHealth},
			})
			if err != nil {
				t.Fatalf("Buckets: %v", err)
			}
			if len(bv.Buckets) != tc.want {
				t.Fatalf("len = %d, want %d", len(bv.Buckets), tc.want)
			}
		})
	}
}

// TestRoundTripBuckets validates the view layer's internal
// consistency: a BucketsView produced by Buckets, then "synthesised"
// back into a Result and re-bucketed, should equal the first view.
//
// This is a pure-view test — there's no analyzer in the loop.
func TestRoundTripBuckets(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 5000},
			Players: []result.PlayerStream{
				{
					Name: "p1",
					Health: []result.ChangeI16{
						{T: 0, V: 100},
						{T: 1200, V: 60},
						{T: 3500, V: 100},
					},
					Armor: []result.ChangeI16{
						{T: 0, V: 50},
					},
				},
			},
		},
	}
	bv1, err := Buckets(r, BucketsOptions{
		WindowMs: 1000,
		Fields:   []string{FieldHealth, FieldArmor},
	})
	if err != nil {
		t.Fatalf("first Buckets: %v", err)
	}

	// Synthesise a Result whose change streams reproduce the bucket
	// values: one change per bucket at bucket start. Run Buckets
	// again at the same window; result should equal bv1.
	r2 := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 5000},
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Health: synthFromBucketView(bv1, FieldHealth, "p1"),
					Armor:  synthFromBucketView(bv1, FieldArmor, "p1"),
				},
			},
		},
	}
	bv2, err := Buckets(r2, BucketsOptions{
		WindowMs: 1000,
		Fields:   []string{FieldHealth, FieldArmor},
	})
	if err != nil {
		t.Fatalf("second Buckets: %v", err)
	}
	if !reflect.DeepEqual(bv1, bv2) {
		t.Fatalf("round-trip mismatch:\n  first: %+v\n  second: %+v", bv1, bv2)
	}
}

// synthFromBucketView extracts the per-bucket reduced value of a
// single field for a single player and emits one ChangeI16 per
// bucket at bucket-start time. Helper for the round-trip test.
func synthFromBucketView(bv *BucketsView, field, player string) []result.ChangeI16 {
	out := make([]result.ChangeI16, 0, len(bv.Buckets))
	for _, b := range bv.Buckets {
		pdata, ok := b.Players[player]
		if !ok {
			continue
		}
		v, ok := pdata[field]
		if !ok {
			continue
		}
		f, ok := numericFromAny(v)
		if !ok {
			continue
		}
		// b.T is float64 seconds (public ViewBucket API); ChangeI16.T
		// is int32 ms in schema v8.
		out = append(out, result.ChangeI16{T: int32(b.T * 1000), V: int16(f)})
	}
	return out
}
