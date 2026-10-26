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

// TestWithholdObituaries pins the contract cmd/qw-recon-oracle rests on:
// Options.WithholdObituaries must cut the frag log out of ATTRIBUTION only.
// Delta extraction keeps its anchors, so both runs must observe the same
// instants at the same bounded magnitudes — otherwise the oracle would be
// scoring a different reconstruction than the one that ships.
//
// The second half is the oracle itself in miniature: with the obituary
// withheld, the evidence-only verdict at a kill instant must still name the
// killer the obituary names most of the time. The floor is deliberately
// loose (the measured figure is ~97%); it exists to catch a regression that
// severs attribution, not to certify the number in ACCURACY.md.
func TestWithholdObituaries(t *testing.T) {
	if os.Getenv("MVDA_BSP_DIR") == "" {
		if _, err := os.Stat(filepath.Join("..", "..", "bsps")); err == nil {
			mapbsp.SetDir(filepath.Join("..", "..", "bsps"))
		}
	}
	for _, file := range []string{"212422.mvd.gz", "212260.mvd.gz"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join("..", "testdata", "cache", file)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("golden cache demo %s absent — run the analyzer golden test once to populate", file)
			}
			reg := analyzer.NewDefaultRegistry()
			reg.BuildShotStreams = true
			res, err := reg.Analyze(path)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			base, err := damagerecon.Compute(res)
			if err != nil {
				t.Fatalf("recon: %v", err)
			}
			held, err := damagerecon.ComputeWithOptions(res, damagerecon.Options{WithholdObituaries: true})
			if err != nil {
				t.Fatalf("recon (withheld): %v", err)
			}

			// A telefrag/stomp is the one class the withheld run legitimately
			// routes elsewhere (Events instead of Telefrags), so it is excluded
			// from the instant comparison.
			positional := map[instKey]bool{}
			killer := map[instKey]string{}
			if res.Frags != nil {
				for i := range res.Frags.Frags {
					f := &res.Frags.Frags[i]
					k := instKey{f.Victim, f.Time}
					if f.Weapon == "tele" || f.Weapon == "stomp" {
						positional[k] = true
						continue
					}
					if !f.IsSuicide && !f.IsTeamKill && f.Killer != "" &&
						f.Killer != "world" && f.Killer != f.Victim {
						killer[k] = f.Killer
					}
				}
			}
			a, b := boundedByInstant(base), boundedByInstant(held)
			for k, v := range a {
				if positional[k] {
					continue
				}
				w, ok := b[k]
				if !ok {
					t.Fatalf("instant %v missing from the withheld run — withholding leaked into delta extraction", k)
				}
				if w != v {
					t.Fatalf("instant %v bounded %d (production) vs %d (withheld)", k, v, w)
				}
			}
			for k := range b {
				if !positional[k] {
					if _, ok := a[k]; !ok {
						t.Fatalf("instant %v exists only in the withheld run", k)
					}
				}
			}

			top := topAttackerByInstant(held)
			scored, correct := 0, 0
			for k, want := range killer {
				got, ok := top[k]
				if !ok {
					continue
				}
				scored++
				if got == want {
					correct++
				}
			}
			if scored < 20 {
				t.Fatalf("only %d scoreable kills in %s", scored, file)
			}
			if acc := float64(correct) / float64(scored); acc < 0.90 {
				t.Errorf("obituary-withheld attacker accuracy %.1f%% (%d/%d) < 90%%",
					100*acc, correct, scored)
			}
		})
	}
}

type instKey struct {
	victim string
	t      int32
}

func boundedByInstant(d *result.DamageResult) map[instKey]int {
	out := map[instKey]int{}
	for i := range d.Events {
		e := &d.Events[i]
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		out[instKey{e.Victim, e.Time}] += b
	}
	return out
}

// topAttackerByInstant names the attacker credited with the largest bounded
// share of an instant — the verdict the oracle scores.
func topAttackerByInstant(d *result.DamageResult) map[instKey]string {
	best := map[instKey]int{}
	out := map[instKey]string{}
	for i := range d.Events {
		e := &d.Events[i]
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		k := instKey{e.Victim, e.Time}
		if _, seen := out[k]; !seen || b > best[k] {
			best[k], out[k] = b, e.Attacker
		}
	}
	return out
}
