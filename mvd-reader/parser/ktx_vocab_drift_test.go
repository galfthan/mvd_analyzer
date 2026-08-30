package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// The match-start table and the status test are transcriptions of what KTX
// (and, before it, Kombat Teams) broadcasts. When either vendored tree is on
// this machine, read the real writers and check the transcription still
// holds. Skips without the trees.

func vendoredDir(t *testing.T, rel, probe string) string {
	t.Helper()
	for _, up := range []string{"../..", "../../.."} {
		dir := filepath.Join(up, rel)
		if _, err := os.Stat(filepath.Join(dir, probe)); err == nil {
			return dir
		}
	}
	t.Skipf("vendored %s not present on this machine", rel)
	return ""
}

func readVendored(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// matchesStartPattern drives the real print matcher — a fresh parser fed
// the line as a PRINT_HIGH broadcast — rather than re-implementing the
// substring test, so a change to the matcher's gating fails here too.
func matchesStartPattern(t *testing.T, msg string) bool {
	t.Helper()
	p := NewParser(nil)
	mustMatchStartFromPrint(t, p, mvd.PrintHigh, msg+"\n")
	return p.matchStarted
}

// A C source literal: the clock writer escapes its quotes, so the value is
// either \"…\" or a bare word, followed by the literal \n. Every count
// below is EXACT for the vendored tree, so a writer the regexp stops
// recognising fails the test instead of slipping past a threshold.
var (
	reStatusWrite = regexp.MustCompile(`serverinfo status\s+(\\"[^\\]*\\"|\S+?)\\n`)
	reStatusAny   = regexp.MustCompile(`serverinfo status`)
	reBprintBegun = regexp.MustCompile(`G_bprint\s*\([^;]*?"([^"]*begun[^"]*)"`)
)

const ktxStatusWriters = 11 // admin.c 4, match.c 6, world.c 1

func TestKTXMatchStartDrift(t *testing.T) {
	dir := vendoredDir(t, "ktx/src", "match.c")
	match := readVendored(t, dir, "match.c")

	// Every G_bprint in StartMatch whose string says "begun" must be a
	// start pattern. Exactly one today.
	begun := reBprintBegun.FindAllSubmatch(match, -1)
	if len(begun) != 1 {
		t.Errorf("found %d 'begun' broadcasts in match.c, want 1", len(begun))
	}
	for _, m := range begun {
		if !matchesStartPattern(t, string(m[1])) {
			t.Errorf("MatchStartPatterns does not match KTX's %q", m[1])
		}
	}
	for _, want := range []string{`"matchdate: %s\n"`, `"//ktx matchstart\n"`} {
		if !bytes.Contains(match, []byte(want)) {
			t.Errorf("match.c no longer emits %s", want)
		}
	}

	// Every value KTX writes into the status key, from every writer.
	t.Run("status writers", func(t *testing.T) {
		seen, any := 0, 0
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".c") {
				continue
			}
			src := readVendored(t, dir, e.Name())
			any += len(reStatusAny.FindAll(src, -1))
			name := e.Name()
			for _, m := range reStatusWrite.FindAllSubmatch(src, -1) {
				seen++
				v := strings.Trim(string(m[1]), `\"`)
				if strings.Contains(v, "%d") {
					// The running clock: "%d min left".
					v = fmt.Sprintf(strings.ReplaceAll(v, "%d", "%s"), "20")
					if !StatusNamesRunningGame(v) {
						t.Errorf("%s writes status %q: StatusNamesRunningGame = false", name, v)
					}
					continue
				}
				if StatusNamesRunningGame(v) {
					t.Errorf("%s writes status %q: StatusNamesRunningGame = true, want idle", name, v)
				}
			}
		}
		if seen != ktxStatusWriters || any != seen {
			t.Errorf("parsed %d of %d `serverinfo status` writers across ktx/src, want %d of %d", seen, any, ktxStatusWriters, ktxStatusWriters)
		}
	})
}

// Kombat Teams is KTX's ancestor and the mod behind the oldest archive
// demos. Its three broadcasts are the ones KTX inherited; pin them so a
// regression in the tables' oldest reach is visible.
func TestKTeamsMatchStartDrift(t *testing.T) {
	dir := vendoredDir(t, "kteams/v2.21/SRC", "MATCH.QC")
	qc := readVendored(t, dir, "MATCH.QC")

	if !bytes.Contains(qc, []byte(`"The match has begun!\n"`)) {
		t.Error(`kteams v2.21 MATCH.QC no longer broadcasts "The match has begun!"`)
	} else if !matchesStartPattern(t, "The match has begun!") {
		t.Error(`MatchStartPatterns does not match Kombat Teams' start line`)
	}
	for _, v := range []string{"Standby", "Countdown"} {
		if !bytes.Contains(qc, []byte("serverinfo status "+v)) {
			t.Errorf("kteams v2.21 MATCH.QC no longer writes status %s", v)
		}
		if StatusNamesRunningGame(v) {
			t.Errorf("StatusNamesRunningGame(%q) = true on a Kombat Teams idle value", v)
		}
	}
	if !bytes.Contains(qc, []byte(`" min left\"\n"`)) {
		t.Error("kteams v2.21 MATCH.QC no longer writes the ' min left' clock")
	}
}
