package view

import (
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// makeRegionResult builds a synthetic Result where two players (one per
// team) pass through the "mid" region only during a short window that
// sits BETWEEN coarse bucket-start samples. LocTable index 1 = "mid"
// (the region loc), index 2 = "spawn" (alive but outside the region).
//
// Both players are in "mid" carrying RL at t=60000..61000 ms; outside
// that window they sit in "spawn". A coarse windowMs whose bucket-starts
// (0 and 120000) both miss the [60000,61000) window never point-samples
// anyone in the region — the pre-fix bug that made the aggregate a
// sampling artifact. The exact time-weighted stats walk instead sees the
// [60000,61000) presence via the sample times themselves, so a 1000 ms
// armed presence is attributed regardless of windowMs.
func makeRegionResult() *result.Result {
	mkPlayer := func(name, team string) result.PlayerStream {
		return result.PlayerStream{
			Name: name,
			Team: team,
			Position: &result.PositionTrack{
				T:  []int32{0, 60000, 61000},
				Li: []int16{2, 1, 2},
			},
			RL: []result.Interval{{Start: 59000, End: 62000}},
		}
	}
	return &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				mkPlayer("red1", "red"),
				mkPlayer("blue1", "blue"),
			},
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 200000},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "mid", "spawn"},
		},
	}
}

func regionOpts(windowMs int) RegionControlOptions {
	return RegionControlOptions{
		WindowMs: windowMs,
		Regions:  []result.ControlRegion{{Name: "mid", Locs: []string{"mid"}}},
		TeamA:    "red",
		TeamB:    "blue",
		TeamOf: func(name string) string {
			switch name {
			case "red1":
				return "red"
			case "blue1":
				return "blue"
			}
			return ""
		},
	}
}

// TestRegionControlStatsExactTimeWeighted pins the exact-walk behaviour:
// the aggregate Stats (percentages + ByPlayer) are the exact
// time-weighted integral over the native sample times, independent of the
// caller's windowMs, while BucketStates stays at the requested (coarse)
// resolution.
func TestRegionControlStatsExactTimeWeighted(t *testing.T) {
	r := makeRegionResult()

	fine, err := RegionControl(r, regionOpts(50))
	if err != nil {
		t.Fatalf("RegionControl(50): %v", err)
	}
	coarse, err := RegionControl(r, regionOpts(120000))
	if err != nil {
		t.Fatalf("RegionControl(120000): %v", err)
	}

	// Stats are resolution-independent: both are exact, so coarse must
	// equal fine exactly.
	if !reflect.DeepEqual(coarse.Stats, fine.Stats) {
		t.Fatalf("coarse Stats != fine Stats:\ncoarse=%+v\nfine=  %+v", coarse.Stats, fine.Stats)
	}

	// And they must be non-empty for the region the fight happened in —
	// the coarse point-sample alone would have reported empty:100.
	mid, ok := coarse.Stats["mid"]
	if !ok {
		t.Fatalf("no stats for region mid")
	}
	if mid.Empty >= 100 {
		t.Fatalf("region mid reported empty=%.1f; exact stats should see the mid-window fight", mid.Empty)
	}
	if mid.Contested <= 0 {
		t.Fatalf("region mid Contested=%.1f; expected a non-zero contested share", mid.Contested)
	}

	// ByPlayer is now integer MILLISECONDS of presence (time-weighted),
	// not bucket counts: each player is armed in "mid" for the exact
	// [60000,61000) = 1000 ms, identical between the two calls.
	if got := mid.ByPlayer["red1"].Armed; got != 1000 {
		t.Fatalf("red1 armed ms = %d; want 1000 (exact time-weighted)", got)
	}
	if got := mid.ByPlayer["blue1"].Armed; got != 1000 {
		t.Fatalf("blue1 armed ms = %d; want 1000 (exact time-weighted)", got)
	}

	// BucketStates stays at the requested display resolution: the coarse
	// call yields 2 buckets over [0,200000), the fine call 4000.
	if n := len(coarse.BucketStates["mid"]); n != 2 {
		t.Fatalf("coarse BucketStates len = %d; want 2 (windowMs=120000)", n)
	}
	if n := len(fine.BucketStates["mid"]); n != 4000 {
		t.Fatalf("fine BucketStates len = %d; want 4000 (windowMs=50)", n)
	}
}

// TestRegionControlStatsSubGridPrecision pins precision no 50ms grid
// could reach: a single player sits in "mid" armed for exactly
// [60013,60037) — 24 ms, both endpoints off the 50ms grid — and the
// exact walk must attribute exactly 24 ms of armed presence. A grid at
// any multiple of 50ms would either miss this window entirely or snap it
// to a 50ms quantum; only the event-driven integral gets 24.
func TestRegionControlStatsSubGridPrecision(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{
					Name: "red1",
					Team: "red",
					Position: &result.PositionTrack{
						// 13ms-spaced samples straddling a 24ms in-region window.
						T:  []int32{0, 60013, 60037, 60050},
						Li: []int16{2, 1, 2, 2},
					},
					RL: []result.Interval{{Start: 59000, End: 62000}},
				},
				{
					Name: "blue1",
					Team: "blue",
					Position: &result.PositionTrack{
						T:  []int32{0},
						Li: []int16{2},
					},
				},
			},
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 200000},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "mid", "spawn"},
		},
	}

	rc, err := RegionControl(r, regionOpts(50))
	if err != nil {
		t.Fatalf("RegionControl(50): %v", err)
	}
	mid, ok := rc.Stats["mid"]
	if !ok {
		t.Fatalf("no stats for region mid")
	}
	if got := mid.ByPlayer["red1"].Armed; got != 24 {
		t.Fatalf("red1 armed ms = %d; want exactly 24 (sub-50ms window [60013,60037))", got)
	}
	if _, present := mid.ByPlayer["blue1"]; present {
		t.Fatalf("blue1 never entered mid; should have no ByPlayer entry")
	}
}

// TestRegionControlStatsCoarseSubWindow: a sub-window narrower than windowMs
// rounds the display grid to zero buckets (from=60000&to=62000 at
// windowMs=5000: (2000+2500)/5000 == 0). Stats are windowMs-independent, so
// they must still be computed and equal the fine-grid stats over the same
// sub-window; only bucketStates is absent.
func TestRegionControlStatsCoarseSubWindow(t *testing.T) {
	r := makeRegionResult()
	sub := func(windowMs int) RegionControlOptions {
		o := regionOpts(windowMs)
		o.StartTime = 60000
		o.EndTime = 62000
		return o
	}

	fine, err := RegionControl(r, sub(50))
	if err != nil {
		t.Fatalf("RegionControl(50): %v", err)
	}
	coarse, err := RegionControl(r, sub(5000))
	if err != nil {
		t.Fatalf("RegionControl(5000): %v", err)
	}

	if coarse.Stats == nil {
		t.Fatalf("coarse Stats nil; must be computed independent of the display grid")
	}
	if !reflect.DeepEqual(coarse.Stats, fine.Stats) {
		t.Fatalf("coarse Stats != fine Stats:\ncoarse=%+v\nfine=  %+v", coarse.Stats, fine.Stats)
	}
	if len(coarse.BucketStates) != 0 {
		t.Fatalf("coarse BucketStates = %v; want absent (grid rounds to 0 buckets)", coarse.BucketStates)
	}
	// Sanity: the fine grid over the same window does have buckets.
	if len(fine.BucketStates["mid"]) == 0 {
		t.Fatalf("fine BucketStates empty; windowMs=50 over [60000,62000) should have buckets")
	}
}

// TestRegionControlNoMatchingPlayers: when no roster-mapped player has
// positions in the window (v58's empty:100 case), stats are still present —
// every region at Empty:100 with no ByPlayer — and bucketStates is all '_'
// where the grid has buckets. The pre-fix `len(players)==0` early return
// dropped the stats entirely.
func TestRegionControlNoMatchingPlayers(t *testing.T) {
	r := makeRegionResult()
	opts := regionOpts(50)
	opts.TeamOf = func(string) string { return "" } // nobody maps to a team

	rc, err := RegionControl(r, opts)
	if err != nil {
		t.Fatalf("RegionControl: %v", err)
	}
	mid, ok := rc.Stats["mid"]
	if !ok {
		t.Fatalf("no stats for region mid; empty roster must still emit stats")
	}
	if mid.Empty != 100 {
		t.Fatalf("region mid Empty = %.1f; want 100 (empty roster)", mid.Empty)
	}
	if mid.ByPlayer != nil {
		t.Fatalf("region mid ByPlayer = %v; want nil (no player presence)", mid.ByPlayer)
	}
	bs, ok := rc.BucketStates["mid"]
	if !ok || len(bs) == 0 {
		t.Fatalf("bucketStates for mid missing/empty; windowMs=50 over [0,200000) has buckets")
	}
	for i := 0; i < len(bs); i++ {
		if bs[i] != RegionStateEmpty {
			t.Fatalf("bucketStates[%d] = %q; want '_' (empty roster)", i, bs[i])
		}
	}
}
