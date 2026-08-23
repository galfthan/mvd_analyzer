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

// TestCoverage pins the three properties that make damage.coverage worth
// publishing:
//
//  1. a healthy recording reads full coverage — every kill the frag log
//     names has reconstructed damage at its instant;
//  2. the figure MOVES with the evidence it claims to measure. Thinning the
//     health/armor change streams (the silent-stat-channel failure this
//     field exists to expose) drops it into the same band the 82 known
//     silent-channel archive demos sit in (< 0.5), while the DENOMINATOR
//     stays put — coverage must never be able to hide a loss by shrinking
//     the thing it divides by;
//  3. the obituary withhold does not reach it, so cmd/qw-recon-oracle's
//     kill-delta coverage and the shipped field are the same number.
//
// Bounds are loose on purpose (measured: 1.00 healthy, 0.28-0.41 at
// keep-1-in-4); ACCURACY.md §per-demo coverage carries the real figures.
func TestCoverage(t *testing.T) {
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
			cov := base.Coverage
			if cov == nil {
				t.Fatal("no coverage on a reconstruction of a demo with a frag log")
			}
			if cov.Kills < 20 {
				t.Fatalf("only %d scoreable kills — too few to say anything", cov.Kills)
			}
			if cov.Covered > cov.Kills {
				t.Fatalf("covered %d > kills %d", cov.Covered, cov.Kills)
			}
			if cov.Ratio < 0.95 {
				t.Errorf("coverage %.4f (%d/%d) on a healthy recording", cov.Ratio, cov.Covered, cov.Kills)
			}

			held, err := damagerecon.ComputeWithOptions(res, damagerecon.Options{WithholdObituaries: true})
			if err != nil {
				t.Fatalf("recon (withheld): %v", err)
			}
			if held.Coverage == nil || *held.Coverage != *cov {
				t.Errorf("withheld coverage %+v != production %+v — the oracle measures a different figure than we ship",
					held.Coverage, cov)
			}

			// Thin the stat channel the reconstruction reads its magnitudes
			// from, keeping every other input intact — the shape of the
			// silent-channel recordings. Destructive, so it goes last.
			for i := range res.Streams.Players {
				p := &res.Streams.Players[i]
				p.Health = keepEveryNth(p.Health, 4)
				p.Armor = keepEveryNth(p.Armor, 4)
			}
			deg, err := damagerecon.Compute(res)
			if err != nil {
				t.Fatalf("recon (degraded): %v", err)
			}
			if deg.Coverage == nil {
				t.Fatal("no coverage on the degraded run")
			}
			if deg.Coverage.Kills != cov.Kills {
				t.Errorf("degraded denominator %d != %d — coverage must divide by the frag log, not by the evidence",
					deg.Coverage.Kills, cov.Kills)
			}
			if deg.Coverage.Ratio > 0.6 {
				t.Errorf("degraded coverage %.4f (%d/%d) — the figure does not track the evidence it reports on",
					deg.Coverage.Ratio, deg.Coverage.Covered, deg.Coverage.Kills)
			}
		})
	}
}

// TestCoverageNoDenominator pins the ABSENCE half of the contract: with no
// scoreable kill in the frag log there is no anchor to measure completeness
// against, so the field is omitted rather than reporting a 0 ratio that would
// read as "the reconstruction saw nothing".
//
// It also carries the load a `len(in.frags) == 0` early return used to
// pretend to: an empty frag log is not a separate case, it is the kills == 0
// case, and both spellings of "no denominator" have to reach the same answer.
func TestCoverageNoDenominator(t *testing.T) {
	build := func(frags []result.FragEntry) *result.Result {
		res := &result.Result{
			Streams: &result.Streams{
				ShotStreamsComputed: true,
				Global:              result.GlobalStream{MatchStart: 0, MatchEnd: 60000},
				Players: []result.PlayerStream{
					{Name: "a", Alive: []result.Interval{{Start: 0, End: 60000}}},
					{Name: "b", Alive: []result.Interval{{Start: 0, End: 60000}}},
				},
			},
		}
		if frags != nil {
			res.Frags = &result.FragResult{Frags: frags}
		}
		return res
	}
	for _, tc := range []struct {
		name  string
		frags []result.FragEntry
	}{
		{"no frag section", nil},
		{"empty frag log", []result.FragEntry{}},
		{"only unscoreable frags", []result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "a", Weapon: "rl", IsSuicide: true},
			{Time: 2000, Killer: "a", Victim: "b", Weapon: "lg", IsTeamKill: true},
			{Time: 3000, Killer: "a", Victim: "b", Weapon: "tele"},
			{Time: 4000, Killer: "world", Victim: "b", Weapon: "drown"},
			{Time: 5000, Killer: "a", Victim: "ghost", Weapon: "rl"}, // victim not on the roster
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := damagerecon.Compute(build(tc.frags))
			if err != nil {
				t.Fatalf("recon: %v", err)
			}
			if out.Coverage != nil {
				t.Errorf("coverage %+v on a demo with no scoreable kill — absence is the only honest answer", out.Coverage)
			}
		})
	}
	// The control: one scoreable kill IS a denominator, so the field appears.
	out, err := damagerecon.Compute(build([]result.FragEntry{
		{Time: 3000, Killer: "a", Victim: "b", Weapon: "rl"},
	}))
	if err != nil {
		t.Fatalf("recon: %v", err)
	}
	if out.Coverage == nil || out.Coverage.Kills != 1 {
		t.Fatalf("coverage %+v — one enemy weapon kill on two rostered players is a denominator of 1", out.Coverage)
	}
}

func keepEveryNth(in []result.ChangeI16, n int) []result.ChangeI16 {
	var out []result.ChangeI16
	for i := range in {
		if i%n == 0 {
			out = append(out, in[i])
		}
	}
	return out
}
