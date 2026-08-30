package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// The mode vocabularies in gamemode.go are transcriptions of KTX source:
// PrintCountdown's Mode literals, um_list's names, GetMode's and
// lastscores2str's return strings. Transcriptions drift — the countdown
// table was once copied from the wrong KTX symbol and matched nothing for
// a year — so when the vendored tree is on this machine, read the real
// producers and check every string they can write is one the tables name
// (or one they knowingly pass over). Skips without the tree; the offline
// pin is TestCanonicalTables.

// ktxSrc returns the vendored ktx/src directory, or skips the test.
func ktxSrc(t *testing.T) string {
	t.Helper()
	for _, dir := range []string{"../../ktx/src", "../../../ktx/src"} {
		if _, err := os.Stat(filepath.Join(dir, "match.c")); err == nil {
			return dir
		}
	}
	t.Skip("vendored ktx/src not present on this machine")
	return ""
}

func ktxRead(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// cFuncBody returns the body of a top-level C function DEFINITION whose
// signature line is sig — the anchor is the signature followed by the
// opening brace on its own line, so a forward declaration of the same
// signature cannot redirect it — up to the first lone "}" line.
func cFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig+"\n{")
	if i < 0 {
		t.Fatalf("function definition %q not found", sig)
	}
	rest := src[i:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("function %q: no closing brace", sig)
	}
	return rest[:j]
}

// Whitespace-tolerant, and every count below is EXACT for the vendored
// tree: a KTX change that adds, removes or re-spells a producer fails the
// test rather than slipping past a "≥ N" threshold, and so does a rewrite
// the regexp no longer recognises. Update the count and the table together.
var (
	reReturnStr = regexp.MustCompile(`return\s*"([^"]+)"`)
	reRedtext   = regexp.MustCompile(`mode\s*=\s*redtext\s*\(\s*"([^"]+)"\s*\)`)
	reUmRow     = regexp.MustCompile(`(?m)^\s*\{\s*"([^"]+)"`)
	reBprintStr = regexp.MustCompile(`G_bprint\s*\([^;]*?"([^"]*)"`)
)

const (
	ktxCountdownModeLiterals = 14 // match.c PrintCountdown
	ktxUmListRows            = 17 // commands.c um_list
	ktxGetModeReturns        = 11 // stats.c GetMode
	ktxLastscoresReturns     = 10 // commands.c lastscores2str
)

func TestKTXVocabularyDrift(t *testing.T) {
	dir := ktxSrc(t)

	t.Run("PrintCountdown Mode literals", func(t *testing.T) {
		body := cFuncBody(t, ktxRead(t, dir, "match.c"), "void PrintCountdown(int seconds)")
		lits := reRedtext.FindAllStringSubmatch(body, -1)
		if len(lits) != ktxCountdownModeLiterals {
			t.Fatalf("found %d Mode literals in PrintCountdown, want %d — KTX changed or the regexp missed one", len(lits), ktxCountdownModeLiterals)
		}
		for _, m := range lits {
			raw := m[1]
			flat := strings.ReplaceAll(raw, " ", "")
			if canonicalFromCountdown(flat) != "" {
				continue
			}
			// The rows that name no shape must be ones the resolver
			// passes over on purpose: "Unknown" is KTX saying it does not
			// know either, "CA" is the isCA() family on at least one
			// current server build (see canonicalFromCountdown), and a
			// ruleset row lands in submodes.
			if raw == "Unknown" || raw == "CA" {
				continue
			}
			if subs, _ := submodeSet(nil, &MatchSettings{Mode: flat}); len(subs) == 0 {
				t.Errorf("PrintCountdown writes Mode %q: canonicalFromCountdown names no shape and submodeSet no ruleset", raw)
			}
		}
	})

	t.Run("um_list names", func(t *testing.T) {
		src := ktxRead(t, dir, "commands.c")
		i := strings.Index(src, "usermode um_list[] =")
		if i < 0 {
			t.Fatal("um_list not found")
		}
		table := src[i:]
		table = table[:strings.Index(table, "};")]
		rows := reUmRow.FindAllStringSubmatch(table, -1)
		if len(rows) != ktxUmListRows {
			t.Fatalf("found %d um_list rows, want %d", len(rows), ktxUmListRows)
		}
		for _, m := range rows {
			if canonicalFromUmode(m[1]) == "" {
				t.Errorf("um_list names usermode %q: canonicalFromUmode names no shape", m[1])
			}
		}
	})

	t.Run("GetMode and lastscores2str literals", func(t *testing.T) {
		noShape := map[string]bool{"instagib": true, "midair": true, "unknown": true}
		for _, f := range []struct {
			file, sig string
			want      int
		}{
			{"stats.c", "const char* GetMode(void)", ktxGetModeReturns},
			{"commands.c", "char* lastscores2str(lsType_t lst)", ktxLastscoresReturns},
		} {
			body := cFuncBody(t, ktxRead(t, dir, f.file), f.sig)
			lits := reReturnStr.FindAllStringSubmatch(body, -1)
			if len(lits) != f.want {
				t.Fatalf("%s: found %d return literals, want %d", f.sig, len(lits), f.want)
			}
			for _, m := range lits {
				if canonicalFromKTXMode(m[1]) == "" && !noShape[m[1]] {
					t.Errorf("%s returns %q: canonicalFromKTXMode names no shape", f.sig, m[1])
				}
			}
		}
	})

	t.Run("match end broadcasts", func(t *testing.T) {
		// Every G_bprint string in EndMatch that says something is over,
		// fed to the real detector. Exactly two today: the match line ends
		// the match, the per-point hoony line does not (matchEndPatterns).
		body := cFuncBody(t, ktxRead(t, dir, "match.c"), "void EndMatch(float skip_log)")
		want := map[string]bool{"The match is over": true, "The point is over": false}
		seen := 0
		for _, m := range reBprintStr.FindAllStringSubmatch(body, -1) {
			msg := strings.TrimSuffix(m[1], `\n`)
			if !strings.Contains(strings.ToLower(msg), "over") {
				continue
			}
			seen++
			ends, known := want[msg]
			if !known {
				t.Errorf("EndMatch broadcasts %q, which the end table has no verdict on", msg)
				continue
			}
			var d MatchTimingDetector
			d.OnMatchStart(&events.MatchStartEvent{TimeMs: 1000, Source: "matchdate"})
			d.OnPrint(&events.PrintEvent{Level: events.PrintHigh, Message: msg + "\n", TimeMs: 2000})
			if d.Ended != ends {
				t.Errorf("%q ends the match = %v, want %v", msg, d.Ended, ends)
			}
		}
		if seen != len(want) {
			t.Errorf("found %d 'over' broadcasts in EndMatch, want %d", seen, len(want))
		}
	})

	t.Run("finalscores round modes", func(t *testing.T) {
		// canonicalIsRounds cites the lastscores round-count writer; keep
		// the two in step.
		body := cFuncBody(t, ktxRead(t, dir, "commands.c"), "void lastscore_add(void)")
		for _, ls := range []struct{ tag, mode string }{
			{"lsCA", result.GameModeCA}, {"lsWO", result.GameModeWipeout},
			{"lsRA", result.GameModeRA}, {"lsHM", result.GameModeHoony},
		} {
			if !strings.Contains(body, ls.tag) {
				t.Errorf("lastscore_add no longer mentions %s", ls.tag)
			}
			if !canonicalIsRounds(ls.mode) {
				t.Errorf("canonicalIsRounds(%q) = false", ls.mode)
			}
		}
	})
}
