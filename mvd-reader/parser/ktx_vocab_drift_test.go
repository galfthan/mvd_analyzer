package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

func matchesStartPattern(msg string) bool {
	lower := strings.ToLower(msg)
	for _, p := range MatchStartPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// A C source literal: the clock writer escapes its quotes, so the value is
// either \"…\" or a bare word, followed by the literal \n.
var reStatusWrite = regexp.MustCompile(`serverinfo status (\\"[^\\]*\\"|\S+?)\\n`)

func TestKTXMatchStartDrift(t *testing.T) {
	dir := vendoredDir(t, "ktx/src", "match.c")
	match := readVendored(t, dir, "match.c")

	if !bytes.Contains(match, []byte(`redtext("The match has begun!")`)) {
		t.Error(`match.c no longer broadcasts "The match has begun!" — MatchStartPatterns' KTX provenance moved`)
	} else if !matchesStartPattern("The match has begun!") {
		t.Error(`MatchStartPatterns does not match KTX's "The match has begun!"`)
	}
	for _, want := range []string{`"matchdate: %s\n"`, `"//ktx matchstart\n"`} {
		if !bytes.Contains(match, []byte(want)) {
			t.Errorf("match.c no longer emits %s", want)
		}
	}

	// Every value KTX writes into the status key, from every writer.
	t.Run("status writers", func(t *testing.T) {
		seen := 0
		for _, name := range []string{"match.c", "admin.c", "world.c"} {
			for _, m := range reStatusWrite.FindAllSubmatch(readVendored(t, dir, name), -1) {
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
		if seen < 11 {
			t.Errorf("found %d status writers, expected KTX's 11", seen)
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
	} else if !matchesStartPattern("The match has begun!") {
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
