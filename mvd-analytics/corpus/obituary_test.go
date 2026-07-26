package corpus

import (
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
)

// fragLogFloor pins a lower bound on the frag log of the special-cases
// demos whose obituaries the wire actually carries.
//
// It exists for the pre-KTX demo class. Old kmod/qwe progs print an
// obituary as a run of separate bprint fragments ("DARKLORD", "'s
// rocket", "\n"), and every consumer of them — the parser's death-mining
// and the analytics frag log alike — matches whole lines. Before the
// parser assembled fragments into lines (mvd-reader/parser/print.go,
// assemblePrintLine) 4on4_l_vs_la[e1m2] produced a scoreboard with 230
// team frags and an EMPTY frag log, which the playerStats scoreboard then
// served as a confident `kills: 0` on all eight rows.
//
// The floors are deliberately well under the measured values (368 entries
// on l_vs_la against 368 deaths counted from the death stream, 58 on the
// 2003 duel) so this stays an "is the log there at all" assertion rather
// than a golden pin. The demos not listed here are modern KTX recordings
// whose prints were never fragmented; they are covered by the golden
// corpus.
var fragLogFloor = map[string]int{
	"4on4_l_vs_la[e1m2].mvd":            300,
	"1on1_]apollyon[_vs_jogi_[dm4].mvd": 40,
}

// TestSpecialCasesFragLog asserts that a demo whose broadcast log carries
// obituaries produces a frag log, and that every entry names both sides.
// An empty log on such a demo means the print path stopped assembling
// lines — see the package doc for the -count=1 requirement.
func TestSpecialCasesFragLog(t *testing.T) {
	demos := specialCaseDemos()
	if len(demos) == 0 {
		t.Skip("no demos in " + specialCasesDir + " — provide the directory to run these invariants")
	}
	covered := 0
	for _, demo := range demos {
		base := filepath.Base(demo)
		floor, want := fragLogFloor[base]
		if !want {
			continue
		}
		covered++
		t.Run(base, func(t *testing.T) {
			reg := analyzer.NewDefaultRegistry()
			res, err := reg.Analyze(demo)
			if err != nil {
				t.Fatalf("analysis failed: %v", err)
			}
			if res.Frags == nil {
				t.Fatal("result.Frags is nil — no frag log was produced at all")
			}
			if got := len(res.Frags.Frags); got < floor {
				t.Fatalf("frags.frags has %d entries, want at least %d — "+
					"obituary prints are not reaching the frag log", got, floor)
			}
			for i, f := range res.Frags.Frags {
				if f.Victim == "" {
					t.Errorf("frags.frags[%d] = %+v has no victim", i, f)
				}
				if f.Killer == "" {
					t.Errorf("frags.frags[%d] = %+v has no killer", i, f)
				}
			}
		})
	}
	if covered == 0 {
		t.Skipf("none of the %d demos present are in the frag-log table", len(demos))
	}
}
