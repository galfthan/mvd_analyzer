package view

import (
	"testing"

	"github.com/mvd-analyzer/qwanalytics/result"
)

// makeStream builds a tiny synthetic Result with one player and a
// known set of changes for unit tests. Streams.Global covers [0, 10).
func makeStream(t *testing.T, p result.PlayerStream) *result.Result {
	t.Helper()
	return &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{p},
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 10},
		},
	}
}

func TestBucketsBasicHealth(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		Health: []result.ChangeI16{
			{T: 0, V: 100},
			{T: 0.3, V: 60},
			{T: 1.2, V: 30},
			{T: 2.0, V: 100},
		},
	})
	bv, err := Buckets(r, BucketsOptions{
		WindowMs: 1000, // 1 second buckets across [0, 10)
		Fields:   []string{FieldHealth},
	})
	if err != nil {
		t.Fatalf("Buckets returned error: %v", err)
	}
	if bv.WindowMs != 1000 {
		t.Fatalf("WindowMs = %d, want 1000", bv.WindowMs)
	}
	if len(bv.Buckets) != 10 {
		t.Fatalf("len(Buckets) = %d, want 10", len(bv.Buckets))
	}
	// Bucket 0 (0-1s): last health is 60 (carry through bucket end at 1.0).
	got := bv.Buckets[0].Players["p1"][FieldHealth]
	if got != int16(60) {
		t.Fatalf("bucket 0 health = %v, want 60", got)
	}
	// Bucket 1 (1-2s): last entry inside is 30 at t=1.2; carries.
	got = bv.Buckets[1].Players["p1"][FieldHealth]
	if got != int16(30) {
		t.Fatalf("bucket 1 health = %v, want 30", got)
	}
	// Bucket 2 (2-3s): change to 100 at t=2.0.
	got = bv.Buckets[2].Players["p1"][FieldHealth]
	if got != int16(100) {
		t.Fatalf("bucket 2 health = %v, want 100", got)
	}
	// Bucket 3 (3-4s): no change in window; carry-forward to 100.
	got = bv.Buckets[3].Players["p1"][FieldHealth]
	if got != int16(100) {
		t.Fatalf("bucket 3 health = %v, want 100 (carry-forward)", got)
	}
}

func TestBucketsPartialFlag(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name:   "p1",
		Health: []result.ChangeI16{{T: 0, V: 100}},
	})
	// Match length 10s with windowMs=3000ms → 3 full + 1 partial (1s).
	bv, err := Buckets(r, BucketsOptions{
		WindowMs:  3000,
		Fields:    []string{FieldHealth},
		StartTime: 0, EndTime: 10,
	})
	if err != nil {
		t.Fatalf("Buckets error: %v", err)
	}
	if len(bv.Buckets) != 4 {
		t.Fatalf("len = %d, want 4", len(bv.Buckets))
	}
	if bv.Buckets[3].Partial != true {
		t.Fatalf("last bucket Partial = %v, want true", bv.Buckets[3].Partial)
	}
	for i := 0; i < 3; i++ {
		if bv.Buckets[i].Partial {
			t.Fatalf("bucket %d Partial = true, want false", i)
		}
	}
}

func TestBucketsUnknownReducerErrors(t *testing.T) {
	r := makeStream(t, result.PlayerStream{Name: "p1"})
	_, err := Buckets(r, BucketsOptions{
		Fields:   []string{FieldHealth},
		Reducers: map[string]string{FieldHealth: "garbage"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown reducer")
	}
}

func TestBucketsUnknownFieldErrors(t *testing.T) {
	r := makeStream(t, result.PlayerStream{Name: "p1"})
	_, err := Buckets(r, BucketsOptions{
		Fields: []string{"garbage"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestBucketsIntervalHeldAny(t *testing.T) {
	r := makeStream(t, result.PlayerStream{
		Name: "p1",
		RL: []result.Interval{
			{Start: 1.0, End: 2.0},
		},
	})
	bv, err := Buckets(r, BucketsOptions{
		WindowMs: 1000,
		Fields:   []string{FieldRL},
	})
	if err != nil {
		t.Fatalf("Buckets error: %v", err)
	}
	// Bucket 0 (0-1s): RL not held — but since intervalSamples
	// samples the middle of the bucket, the entry at start=1.0
	// (boundary) should not register; bucket 1 (1-2s) should.
	pdata0, has0 := bv.Buckets[0].Players["p1"]
	if has0 {
		if v, ok := pdata0[FieldRL]; ok && v == true {
			t.Fatalf("bucket 0 expected RL=false")
		}
	}
	pdata1 := bv.Buckets[1].Players["p1"]
	if pdata1[FieldRL] != true {
		t.Fatalf("bucket 1 RL = %v, want true", pdata1[FieldRL])
	}
}

func TestBucketsTeamAggregates(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 1},
			Players: []result.PlayerStream{
				{
					Name: "p1", Team: "red",
					Health: []result.ChangeI16{{T: 0, V: 100}},
					Armor:  []result.ChangeI16{{T: 0, V: 50}},
					RL:     []result.Interval{{Start: 0, End: 1}},
				},
				{
					Name: "p2", Team: "red",
					Health: []result.ChangeI16{{T: 0, V: 80}},
					Armor:  []result.ChangeI16{{T: 0, V: 25}},
					LG:     []result.Interval{{Start: 0, End: 1}},
				},
			},
		},
	}
	bv, err := Buckets(r, BucketsOptions{
		WindowMs:    500,
		IncludeTeam: true,
	})
	if err != nil {
		t.Fatalf("Buckets error: %v", err)
	}
	if len(bv.Buckets) != 2 {
		t.Fatalf("len = %d, want 2", len(bv.Buckets))
	}
	td := bv.Buckets[0].Team["red"]
	if td["rl"] != 1 {
		t.Fatalf("team rl = %d, want 1", td["rl"])
	}
	if td["lg"] != 1 {
		t.Fatalf("team lg = %d, want 1", td["lg"])
	}
	if td["w"] != 2 {
		t.Fatalf("team w = %d, want 2", td["w"])
	}
	if td["th"] != 180 {
		t.Fatalf("team th = %d, want 180", td["th"])
	}
}
