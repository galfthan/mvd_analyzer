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

// specialCasesGlob is the per-machine demo drop the invariants run over.
const specialCasesGlob = "../../demo-test-data/mvd/special-cases/*.mvd"

func TestSpecialCasesInvariants(t *testing.T) {
	demos, _ := filepath.Glob(specialCasesGlob)
	if len(demos) == 0 {
		t.Skip("no demos in demo-test-data/mvd/special-cases/ — provide the directory to run these invariants")
	}
	sort.Strings(demos)

	for _, demo := range demos {
		t.Run(filepath.Base(demo), func(t *testing.T) {
			reg := analyzer.NewDefaultRegistry()
			res, err := reg.Analyze(demo)
			if err != nil {
				t.Fatalf("analysis failed: %v", err)
			}
			checkRosterUnique(t, res)
			checkTeamTotals(t, res)
			checkServerinfoScore(t, res)
			checkDemoInfoScoreboard(t, res)
			checkRosterHasStream(t, res)
			checkIntervalsHaveEvidence(t, res)
			checkIntervalsInMatchWindow(t, res)
		})
	}
}

// checkRosterUnique — one scoreboard row per player. A duplicate means a
// slot's occupancies were not folded back into one identity.
func checkRosterUnique(t *testing.T, res *result.Result) {
	t.Helper()
	if res.Match == nil {
		return
	}
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
	if res.Match == nil {
		return
	}
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
	if res.Match == nil || res.Metadata == nil {
		return
	}
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
func parseServerinfoScore(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	var out []int
	for i := 0; i < len(v); {
		if v[i] < '0' || v[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(v) && v[j] >= '0' && v[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(v[i:j])
		if err != nil {
			return nil, false
		}
		out = append(out, n)
		i = j
	}
	if len(out) < 2 {
		return nil, false
	}
	return out, true
}

// checkDemoInfoScoreboard — where KTX wrote a demoinfo block, its
// end-of-match scoreboard is authoritative: every player it lists must
// appear in our roster with the same frag count, and every roster row must
// join it. A row KTX has no entry for is a phantom; a missing row is a
// participant we dropped; a frag mismatch is an attribution bug.
func checkDemoInfoScoreboard(t *testing.T, res *result.Result) {
	t.Helper()
	if res.Match == nil || res.DemoInfo == nil || len(res.DemoInfo.Players) == 0 {
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

// checkRosterHasStream — a scoreboard row must have a matching player
// stream. The two are built from different evidence (svc_updatefrags plus
// occupancy on one side, entity state on the other), so a row with no
// stream is a player we scored but never saw play.
func checkRosterHasStream(t *testing.T, res *result.Result) {
	t.Helper()
	if res.Match == nil || res.Streams == nil {
		return
	}
	have := map[string]bool{}
	for _, ps := range res.Streams.Players {
		have[stripSlotSuffix(ps.Name)] = true
	}
	for _, p := range res.Match.Players {
		if !have[p.Name] {
			t.Errorf("match.players lists %q but streams.players has no such player", p.Name)
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
	if res.Streams == nil {
		return
	}
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
	if res.Streams == nil {
		return
	}
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
