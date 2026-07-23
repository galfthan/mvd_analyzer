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
// sampling artifact.
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

// TestRegionControlStatsNativeResolution pins the sampling-artifact fix:
// the aggregate Stats (percentages + ByPlayer) must be computed at the
// native 50ms grid regardless of the caller's windowMs, while
// BucketStates stays at the requested (coarse) resolution.
func TestRegionControlStatsNativeResolution(t *testing.T) {
	r := makeRegionResult()

	fine, err := RegionControl(r, regionOpts(50))
	if err != nil {
		t.Fatalf("RegionControl(50): %v", err)
	}
	coarse, err := RegionControl(r, regionOpts(120000))
	if err != nil {
		t.Fatalf("RegionControl(120000): %v", err)
	}

	// Stats are resolution-independent: coarse must equal fine exactly.
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
		t.Fatalf("region mid reported empty=%.1f; native-resolution stats should see the mid-bucket fight", mid.Empty)
	}
	if mid.Contested <= 0 {
		t.Fatalf("region mid Contested=%.1f; expected a non-zero contested share at native resolution", mid.Contested)
	}

	// ByPlayer must reflect the native-grid bucket counts (20 buckets of
	// 50ms across the 1000ms window), identical between the two calls.
	if got := mid.ByPlayer["red1"].Armed; got != 20 {
		t.Fatalf("red1 armed buckets = %d; want 20 (native 50ms grid)", got)
	}
	if got := mid.ByPlayer["blue1"].Armed; got != 20 {
		t.Fatalf("blue1 armed buckets = %d; want 20 (native 50ms grid)", got)
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
