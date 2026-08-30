package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
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

// cFuncBody returns the body of a top-level C function whose signature
// line starts with sig, up to the first line that is a lone "}".
func cFuncBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("function %q not found", sig)
	}
	rest := src[i:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("function %q: no closing brace", sig)
	}
	return rest[:j]
}

var (
	reReturnStr = regexp.MustCompile(`return "([^"]+)"`)
	reRedtext   = regexp.MustCompile(`mode = redtext\("([^"]+)"\)`)
	reUmRow     = regexp.MustCompile(`(?m)^\s*\{\s*"([^"]+)"`)
)

func TestKTXVocabularyDrift(t *testing.T) {
	dir := ktxSrc(t)

	t.Run("PrintCountdown Mode literals", func(t *testing.T) {
		body := cFuncBody(t, ktxRead(t, dir, "match.c"), "void PrintCountdown(int seconds)")
		lits := reRedtext.FindAllStringSubmatch(body, -1)
		if len(lits) < 10 {
			t.Fatalf("found %d Mode literals in PrintCountdown, expected the full chain", len(lits))
		}
		for _, m := range lits {
			raw := m[1]
			flat := strings.ReplaceAll(raw, " ", "")
			if canonicalFromCountdown(flat) != "" {
				continue
			}
			// The rows that name no shape must be ones the resolver
			// passes over on purpose: "Unknown" is KTX saying it does not
			// know either, "CA" is the isCA() family on the builds that
			// predate the Wipeout branch (see canonicalFromCountdown), and
			// a ruleset row lands in submodes.
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
		if len(rows) < 15 {
			t.Fatalf("found %d um_list rows", len(rows))
		}
		for _, m := range rows {
			if canonicalFromUmode(m[1]) == "" {
				t.Errorf("um_list names usermode %q: canonicalFromUmode names no shape", m[1])
			}
		}
	})

	t.Run("GetMode and lastscores2str literals", func(t *testing.T) {
		noShape := map[string]bool{"instagib": true, "midair": true, "unknown": true}
		for _, f := range []struct{ file, sig string }{
			{"stats.c", "const char* GetMode(void)"},
			{"commands.c", "char* lastscores2str(lsType_t lst)"},
		} {
			body := cFuncBody(t, ktxRead(t, dir, f.file), f.sig)
			lits := reReturnStr.FindAllStringSubmatch(body, -1)
			if len(lits) < 8 {
				t.Fatalf("%s: found %d return literals", f.sig, len(lits))
			}
			for _, m := range lits {
				if canonicalFromKTXMode(m[1]) == "" && !noShape[m[1]] {
					t.Errorf("%s returns %q: canonicalFromKTXMode names no shape", f.sig, m[1])
				}
			}
		}
	})

	t.Run("match end broadcasts", func(t *testing.T) {
		src := ktxRead(t, dir, "match.c")
		for line, ends := range map[string]bool{
			`G_bprint(2, "The match is over\n")`: true,
			`G_bprint(2, "The point is over\n")`: false, // per-point, see matchEndPatterns
		} {
			if !strings.Contains(src, line) {
				t.Errorf("match.c no longer contains %s — the end table's provenance moved", line)
				continue
			}
			msg := strings.ToLower(line[strings.Index(line, `"`)+1 : strings.LastIndex(line, `\n`)])
			got := false
			for _, p := range matchEndPatterns {
				if strings.Contains(msg, p) {
					got = true
				}
			}
			if got != ends {
				t.Errorf("%q ends the match = %v, want %v", msg, got, ends)
			}
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
