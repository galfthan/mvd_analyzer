// Package corpus holds the special-cases regression net: a table of
// data-quality invariants applied to every demo in
// demo-test-data/mvd/special-cases/.
//
// It exists because the golden corpus is uniformly modern — 2026 KTX
// demos with a demoinfo block, a full damage stream and no mid-match
// roster churn — so the degradation paths that actually break the
// scoreboard (a player who times out, a connection the server refuses, an
// FFA game where nobody has a team, a POV recording) are exercised by no
// pinned test at all. The demos under demo-test-data/ cover exactly those
// cases; this file asserts *invariants* over their output rather than
// pinning bytes, so it stays useful as the pipeline evolves.
//
// The directory is provided per-machine and is not part of the repo, so
// the whole test skips when it is absent (CI and other machines stay
// green). Mirrors the shape of mvd-analytics/diagnostic/.
//
// # Run it with -count=1
//
// `go test` caches the skip, and a directory listing is not one of the
// inputs it tracks: once this package has skipped on a machine, it reports
// `ok (cached)` forever — including after the demos are put in place, so
// the invariants below silently never run again. `make test` therefore
// passes -count=1 for this package. Verified: with a clean test cache, a
// skipping run followed by creating the directory still yields `(cached)`.
//
// # What is actually an oracle here
//
// checkServerinfoScore (the mods' own `score` serverinfo key) and
// checkDemoInfoScoreboard (KTX's end-of-match block) are external. The
// others are self-consistency: they catch a value that disagrees with
// itself, not a value that is uniformly wrong. And checkDemoInfoScoreboard
// is tautological on the frag numbers for duels — see its doc comment — so
// of the seven demos carrying a demoinfo block only the four non-duels are
// independent frag oracles.
package corpus

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// specialCasesDir is the per-machine demo drop the invariants run over.
// Both the plain and the gzipped extension are picked up; the directory
// also holds `*.mvd.ktxstats.json` sidecars, which are not demos.
const specialCasesDir = "../../demo-test-data/mvd/special-cases"

func specialCaseDemos() []string {
	var out []string
	for _, pat := range []string{"*.mvd", "*.mvd.gz"} {
		m, _ := filepath.Glob(filepath.Join(specialCasesDir, pat))
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// demosWithoutStreams are the local demos that produce a scoreboard but no
// player streams at all (result.Streams nil, i.e. every merged stream
// builder came out empty — timeline_streams.go:619-622). Both predate this
// harness; the entries exist so the nil is an *expected* value for these
// two files and a hard failure for any other, rather than something every
// stream check silently returns on.
//
// This is a real known gap: a scoreboard row with no stream means we scored
// a player we have no state record for. It is not analysed here.
var demosWithoutStreams = map[string]string{
	"1on1_]apollyon[_vs_jogi_[dm4].mvd": "2003 duel, match window 10022-610132 detected and 58 frag events " +
		"recorded, but no identity resolves to a non-empty stream",
	"race_us3[dungeonsurf_r02].mvd": "race mode: no match window and no per-player state at all",
}

func TestSpecialCasesInvariants(t *testing.T) {
	demos := specialCaseDemos()
	if len(demos) == 0 {
		t.Skip("no demos in " + specialCasesDir + " — provide the directory to run these invariants")
	}
	// Logged so a run that silently covered nothing is visible in -v output.
	// See the note in this package's doc comment on `go test` caching.
	t.Logf("running invariants over %d demos in %s", len(demos), specialCasesDir)

	for _, demo := range demos {
		t.Run(filepath.Base(demo), func(t *testing.T) {
			reg := analyzer.NewDefaultRegistry()
			res, err := reg.Analyze(demo)
			if err != nil {
				t.Fatalf("analysis failed: %v", err)
			}
			// Every check below returns early on a nil section, so a
			// regression that stops populating one would turn the whole
			// harness green. Assert the sections exist first.
			if res.Match == nil {
				t.Fatal("result.Match is nil — the scoreboard was not produced at all")
			}
			if res.Streams == nil {
				why, known := demosWithoutStreams[filepath.Base(demo)]
				if !known {
					t.Fatal("result.Streams is nil — no player streams were produced at all")
				}
				t.Skipf("no player streams on this demo (pre-existing, pinned here): %s", why)
			}
			if res.Metadata == nil {
				t.Fatal("result.Metadata is nil — serverinfo is unavailable, so one oracle silently drops out")
			}
			checkRosterUnique(t, res)
			checkTeamTotals(t, res)
			checkServerinfoScore(t, res)
			checkDemoInfoScoreboard(t, res)
			checkRosterMatchesStreams(t, res)
			checkIntervalsHaveEvidence(t, res)
			checkIntervalsInMatchWindow(t, res)
		})
	}
}

// checkRosterUnique — one scoreboard row per player. A duplicate means a
// slot's occupancies were not folded back into one identity.
func checkRosterUnique(t *testing.T, res *result.Result) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range res.Match.Players {
		if p.Name == "" {
			t.Errorf("match.players carries an unnamed row: %+v", p)
		}
		if seen[p.Name] {
			t.Errorf("match.players lists %q twice", p.Name)
		}
		seen[p.Name] = true
	}
}

// checkTeamTotals — a team's frag total is the sum of its members'. This
// is self-consistency, not an oracle, but it fails loudly if a roster row
// is dropped from one aggregation and not the other.
func checkTeamTotals(t *testing.T, res *result.Result) {
	t.Helper()
	sum := map[string]int{}
	for _, p := range res.Match.Players {
		if p.Team != "" {
			sum[p.Team] += p.Frags
		}
	}
	for _, tm := range res.Match.Teams {
		if got, want := tm.Frags, sum[tm.Name]; got != want {
			t.Errorf("team %q frags = %d, but its members sum to %d", tm.Name, got, want)
		}
	}
}

// checkServerinfoScore cross-checks the team totals against the server's
// own published scoreboard.
//
// Pre-KTX mods (kmod / qwe) mirror the live team scores into the
// serverinfo `score` key as `[<teamA>]N:[<teamB>]M`, which mvdsv relays as
// svc_serverinfo updates, so the last value in the demo is the final
// score straight from the server. It is a completely independent oracle —
// it would have caught both the departing-player frag loss and the
// refused-connection phantom rows on 4on4_l_vs_la[e1m2] automatically.
//
// Team *names* in the key carry raw colour bytes and do not join reliably,
// so the comparison is on the multiset of numbers.
func checkServerinfoScore(t *testing.T, res *result.Result) {
	t.Helper()
	want, ok := parseServerinfoScore(res.Metadata.ServerInfo["score"])
	if !ok {
		return
	}
	got := make([]int, 0, len(res.Match.Teams))
	for _, tm := range res.Match.Teams {
		got = append(got, tm.Frags)
	}
	sort.Ints(got)
	sort.Ints(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("team frag totals %v do not reconcile with the serverinfo score key %v", got, want)
	}
}

// parseServerinfoScore pulls the frag numbers out of a `score` value like
// `[\x0f\xec\xe1\x0f]230:[ \xfcl\xfc]104`. Returns false when the key is
// absent, empty, or carries fewer than two numbers (a warmup value).
//
// Only a digit run *immediately following* a `]` counts. Harvesting every
// digit run instead would pull digits out of the team names themselves —
// `sf2`, `l33t`, `4on4` are ordinary QW clan tags — and inject phantom
// numbers into the multiset, failing the comparison on a demo where nothing
// is wrong. Team names may contain brackets of their own; a run that
// follows one of those is not all-digits, so it is skipped anyway.
func parseServerinfoScore(v string) ([]int, bool) {
	var out []int
	for i := 0; i+1 < len(v); i++ {
		if v[i] != ']' {
			continue
		}
		j := i + 1
		for j < len(v) && v[j] >= '0' && v[j] <= '9' {
			j++
		}
		if j == i+1 {
			continue
		}
		n, err := strconv.Atoi(v[i+1 : j])
		if err != nil {
			return nil, false
		}
		out = append(out, n)
		i = j - 1
	}
	if len(out) < 2 {
		return nil, false
	}
	return out, true
}

func TestParseServerinfoScore(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int
		ok   bool
	}{
		// The real value off 4on4_l_vs_la[e1m2], colour bytes and all.
		{"l_vs_la", "[\x0f\xec\xe1\x0f]230:[ \xfcl\xfc]104", []int{230, 104}, true},
		{"plain", "[la]230:[l]104", []int{230, 104}, true},
		// Digits inside a team name are ordinary QW clan tags and must not
		// be harvested — this is a false FAILURE, not a missed one.
		{"digits in team name", "[sf2]12:[l33t]7", []int{12, 7}, true},
		{"team name with brackets", "[]a[]230:[b]104", []int{230, 104}, true},
		{"absent", "", nil, false},
		{"warmup single value", "[la]0", nil, false},
		{"no brackets", "230:104", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseServerinfoScore(tc.in)
			if ok != tc.ok || fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("parseServerinfoScore(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// checkDemoInfoScoreboard — where KTX wrote a demoinfo block, its
// end-of-match scoreboard is authoritative: every player it lists must
// appear in our roster with the same frag count, and every roster row must
// join it. A row KTX has no entry for is a phantom; a missing row is a
// participant we dropped; a frag mismatch is an attribution bug.
//
// Caveat on duels: rebuildDuelMatch copies frags straight out of demoinfo,
// so on a 1v1 with a demoinfo block this check compares demoinfo with
// itself and can only fail on the roster membership, not on the numbers.
// Of the demos carrying a demoinfo block, only the non-duel ones are
// independent frag oracles.
func checkDemoInfoScoreboard(t *testing.T, res *result.Result) {
	t.Helper()
	if res.DemoInfo == nil || len(res.DemoInfo.Players) == 0 {
		return
	}
	ours := map[string]int{}
	for _, p := range res.Match.Players {
		ours[p.Name] = p.Frags
	}
	theirs := map[string]int{}
	for _, p := range res.DemoInfo.Players {
		if p.Stats == nil {
			continue
		}
		theirs[p.Name] = p.Stats.Frags
	}
	for name, want := range theirs {
		got, ok := ours[name]
		if !ok {
			t.Errorf("demoinfo lists %q but match.players does not", name)
			continue
		}
		if got != want {
			t.Errorf("%q frags = %d, demoinfo says %d", name, got, want)
		}
	}
	for name := range ours {
		if _, ok := theirs[name]; !ok {
			t.Errorf("match.players lists %q but the demoinfo block has no such player", name)
		}
	}
}

// checkRosterMatchesStreams — the scoreboard and the player streams must
// name the same set of people. The two are built from different evidence
// (svc_updatefrags plus occupancy on one side, entity state on the other),
// so a row with no stream is a player we scored but never saw play, and a
// stream with no row is a player we watched play and then dropped from the
// scoreboard. The second direction is the only oracle at all on the six
// local demos that carry no demoinfo block.
func checkRosterMatchesStreams(t *testing.T, res *result.Result) {
	t.Helper()
	streamed := map[string]bool{}
	for _, ps := range res.Streams.Players {
		streamed[stripSlotSuffix(ps.Name)] = true
	}
	scored := map[string]bool{}
	for _, p := range res.Match.Players {
		scored[p.Name] = true
		if !streamed[p.Name] {
			t.Errorf("match.players lists %q but streams.players has no such player", p.Name)
		}
	}
	for name := range streamed {
		if !scored[name] {
			t.Errorf("streams.players carries %q but match.players has no such row", name)
		}
	}
}

// checkIntervalsHaveEvidence — a possession or powerup interval says the
// player held an item, which is only observable for someone who was in the
// game world. A stream carrying intervals but no spawn, death or position
// sample is therefore reporting the *slot's* state, not the player's: it
// is the shape the refused connections on 4on4_l_vs_la[e1m2] produced when
// per-slot item bits leaked across an occupancy handover.
func checkIntervalsHaveEvidence(t *testing.T, res *result.Result) {
	t.Helper()
	for _, ps := range res.Streams.Players {
		n := 0
		for _, iv := range intervalSets(&ps) {
			n += len(iv.list)
		}
		if n == 0 {
			continue
		}
		played := len(ps.Spawns) > 0 || len(ps.Deaths) > 0 ||
			(ps.Position != nil && len(ps.Position.T) > 0)
		if !played {
			t.Errorf("stream %q carries %d item intervals but no spawn, death or position sample", ps.Name, n)
		}
	}
}

// checkIntervalsInMatchWindow — intervals are half-open, ordered, and
// bounded by the match. An interval running past the match end is the
// classic leak: a slot's open item state flushed at some later boundary
// and attributed to whoever the slot resolved to.
func checkIntervalsInMatchWindow(t *testing.T, res *result.Result) {
	t.Helper()
	lo, hi := res.Streams.Global.MatchStart, res.Streams.Global.MatchEnd
	if hi <= lo {
		return
	}
	for _, ps := range res.Streams.Players {
		for _, set := range intervalSets(&ps) {
			prev := int32(-1 << 31)
			for _, iv := range set.list {
				switch {
				case iv.Start >= iv.End:
					t.Errorf("stream %q %s interval [%d,%d) is empty or inverted", ps.Name, set.name, iv.Start, iv.End)
				case iv.Start < lo || iv.End > hi:
					t.Errorf("stream %q %s interval [%d,%d) escapes the match window [%d,%d]",
						ps.Name, set.name, iv.Start, iv.End, lo, hi)
				case iv.Start < prev:
					t.Errorf("stream %q %s interval [%d,%d) starts before the previous one ended (%d)",
						ps.Name, set.name, iv.Start, iv.End, prev)
				}
				prev = iv.End
			}
		}
	}
}

type namedIntervals struct {
	name string
	list []result.Interval
}

func intervalSets(ps *result.PlayerStream) []namedIntervals {
	return []namedIntervals{
		{"rl", ps.RL}, {"lg", ps.LG}, {"gl", ps.GL}, {"ssg", ps.SSG}, {"sng", ps.SNG},
		{"quad", ps.Quad}, {"pent", ps.Pent}, {"ring", ps.Ring},
	}
}

// stripSlotSuffix undoes the "#<slot>" disambiguation the stream builder
// applies when two identities resolve to the same display name.
func stripSlotSuffix(name string) string {
	i := strings.LastIndexByte(name, '#')
	if i <= 0 {
		return name
	}
	if _, err := strconv.Atoi(name[i+1:]); err != nil {
		return name
	}
	return name[:i]
}
