package damagerecon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// TestReconVsKTXGroundTruth pins the reconstruction's accuracy against the
// KTX damage stream on three golden-cache demos (one per mode). It skips
// when the cache is absent (populated by the analyzer golden test's first
// run). The asserted bounds are deliberately loose — they hold with or
// without a provisioned BSP dir — and exist to catch regressions, not to
// certify the headline numbers (those live in ACCURACY.md, produced by
// cmd/qw-recon-eval over the full corpus with BSPs).
func TestReconVsKTXGroundTruth(t *testing.T) {
	if os.Getenv("MVDA_BSP_DIR") == "" {
		if _, err := os.Stat(filepath.Join("..", "..", "bsps")); err == nil {
			mapbsp.SetDir(filepath.Join("..", "..", "bsps"))
		}
	}
	cases := []struct {
		file string
		mode string
	}{
		{"212422.mvd.gz", "duel"}, // 1on1 skull
		{"212545.mvd.gz", "2on2"}, // dm4
		{"212260.mvd.gz", "4on4"}, // dm3
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			path := filepath.Join("..", "testdata", "cache", tc.file)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("golden cache demo %s absent — run the analyzer golden test once to populate", tc.file)
			}
			reg := analyzer.NewDefaultRegistry()
			reg.BuildShotStreams = true
			res, err := reg.Analyze(path)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			if res.Damage == nil {
				t.Fatalf("no KTX ground truth in %s", tc.file)
			}
			gt := res.Damage
			rc, err := damagerecon.Compute(res)
			if err != nil {
				t.Fatalf("recon: %v", err)
			}
			if rc.Source != result.DamageSourceReconstructed {
				t.Fatalf("source = %q, want reconstructed", rc.Source)
			}

			// Per-player bounded totals: the goal metric. Bounds are the
			// regression ceiling, not the typical error (median given runs
			// ~1%, taken ~0.1%).
			var sumGivenErr, sumTakenErr float64
			n := 0
			for name, g := range gt.ByPlayer {
				if g.Bounded == nil || g.Bounded.Given < 500 {
					continue
				}
				r := rc.ByPlayer[name]
				if r == nil || r.Bounded == nil {
					t.Fatalf("player %q missing from reconstruction", name)
				}
				givenErr := relErr(r.Bounded.Given, g.Bounded.Given)
				takenErr := relErr(r.Bounded.Taken, g.Bounded.Taken)
				if givenErr > 0.10 {
					t.Errorf("player %q bounded given: recon %d vs GT %d (%.1f%%)",
						name, r.Bounded.Given, g.Bounded.Given, 100*givenErr)
				}
				if takenErr > 0.05 {
					t.Errorf("player %q bounded taken: recon %d vs GT %d (%.1f%%)",
						name, r.Bounded.Taken, g.Bounded.Taken, 100*takenErr)
				}
				sumGivenErr += givenErr
				sumTakenErr += takenErr
				n++
			}
			if n == 0 {
				t.Fatal("no players with enough GT damage to score")
			}
			if mean := sumGivenErr / float64(n); mean > 0.04 {
				t.Errorf("mean bounded given error %.2f%% > 4%%", 100*mean)
			}
			if mean := sumTakenErr / float64(n); mean > 0.015 {
				t.Errorf("mean bounded taken error %.2f%% > 1.5%%", 100*mean)
			}

			// Event-instant coverage: virtually every GT damage instant must
			// have a same-instant reconstructed delta.
			type key struct {
				victim string
				t      int32
			}
			gtInstants := map[key]bool{}
			for i := range gt.Events {
				gtInstants[key{gt.Events[i].Victim, gt.Events[i].Time}] = true
			}
			rcInstants := map[key]bool{}
			for i := range rc.Events {
				rcInstants[key{rc.Events[i].Victim, rc.Events[i].Time}] = true
			}
			covered := 0
			for k := range gtInstants {
				if rcInstants[k] {
					covered++
				}
			}
			if cov := float64(covered) / float64(len(gtInstants)); cov < 0.985 {
				t.Errorf("event-instant coverage %.1f%% < 98.5%% (%d/%d)",
					100*cov, covered, len(gtInstants))
			}
		})
	}
}

func relErr(got, want int) float64 {
	if want == 0 {
		return 0
	}
	d := got - want
	if d < 0 {
		d = -d
	}
	return float64(d) / float64(want)
}
