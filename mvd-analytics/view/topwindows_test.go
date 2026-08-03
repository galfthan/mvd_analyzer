package view

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// hwResult builds a two-player Result with an explicit frag log. Times are
// match-relative ms.
func hwResult(frags []result.FragEntry, matchEnd int32) *result.Result {
	return &result.Result{
		// KillsMeasured mirrors what the analyzer publishes for a demo with a
		// non-empty obituary log; measured.frags reads the stored verdict and
		// never re-derives it (see MeasuredSources.Frags).
		Frags: &result.FragResult{Frags: frags, KillsMeasured: true},
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: matchEnd},
		},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "A", Team: "red"}, {Name: "B", Team: "blue"},
		}},
	}
}

func kill(t int32, killer, victim, weapon string) result.FragEntry {
	return result.FragEntry{Time: t, Killer: killer, Victim: victim, Weapon: weapon}
}

// dmg is one hit in the damage log.
func dmg(t int32, attacker, victim, weapon string, v int) result.DamageEntry {
	return result.DamageEntry{Time: t, Attacker: attacker, Victim: victim, Weapon: weapon, Damage: v}
}

// mustHW runs a query that must succeed. Every error path this endpoint has is
// pinned in one place (TestTopWindowsRejects); at every other call site an
// error is a fixture bug, and spelling that out per call was a third of this
// file.
func mustHW(t *testing.T, r *result.Result, opts TopWindowsOptions) *TopWindowsView {
	t.Helper()
	got, err := TopWindows(r, opts)
	if err != nil {
		t.Fatalf("TopWindows(%+v): %v", opts, err)
	}
	return got
}

// firstWindow is the top-ranked row of a query that must produce at least one.
func firstWindow(t *testing.T, v *TopWindowsView) TopWindow {
	t.Helper()
	if len(v.Windows) == 0 {
		t.Fatal("no window")
	}
	return v.Windows[0]
}

// topWindow is mustHW + firstWindow, the shape most tests here want.
func topWindow(t *testing.T, r *result.Result, opts TopWindowsOptions) TopWindow {
	t.Helper()
	return firstWindow(t, mustHW(t, r, opts))
}

// The whole contract in one test: the returned window is the best stretch of
// the requested length, and it is found even though it does not begin on any
// round-number boundary.
func TestTopWindowsFindsTheBestStretch(t *testing.T) {
	// Four spread-out kills, then five inside 4 seconds starting at 60123.
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(20000, "A", "B", "rl"),
		kill(40000, "A", "B", "rl"), kill(50000, "A", "B", "rl"),
		kill(60123, "A", "B", "lg"), kill(61000, "A", "B", "lg"),
		kill(62000, "A", "B", "lg"), kill(63000, "A", "B", "lg"),
		kill(64123, "A", "B", "lg"),
	}
	got := mustHW(t, hwResult(frags, 120000), TopWindowsOptions{WindowMs: 5000, Limit: 1})
	if len(got.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(got.Windows))
	}
	w := got.Windows[0]
	if w.Score != 5 {
		t.Errorf("score = %d, want 5", w.Score)
	}
	// Anchored on a real event time, not a grid multiple.
	if w.Start != 60123 {
		t.Errorf("start = %d, want 60123 (the first kill of the run, not a grid boundary)", w.Start)
	}
	if w.End != 65123 {
		t.Errorf("end = %d, want start+windowMs", w.End)
	}
	if w.Player != "A" || w.Team != "red" {
		t.Errorf("player/team = %q/%q, want A/red", w.Player, w.Team)
	}
	if w.Rank != 1 {
		t.Errorf("rank = %d, want 1", w.Rank)
	}
}

// Returned windows must not be shifted copies of one run. Three separated runs
// give three windows to compare; one run alone would leave the loop below with
// nothing to do, which is how this test used to pass vacuously.
func TestTopWindowsAreNonOverlapping(t *testing.T) {
	var frags []result.FragEntry
	for _, base := range []int32{1000, 30000, 60000} {
		for i := int32(0); i < 10; i++ {
			frags = append(frags, kill(base+i*300, "A", "B", "lg"))
		}
	}
	got := mustHW(t, hwResult(frags, 120000), TopWindowsOptions{WindowMs: 3000, Limit: 5})
	if len(got.Windows) != 3 {
		t.Fatalf("got %d windows, want 3 (one per run) — nothing to check for overlap", len(got.Windows))
	}
	for i := 1; i < len(got.Windows); i++ {
		for j := 0; j < i; j++ {
			a, b := got.Windows[i], got.Windows[j]
			// Closed spans, so TOUCHING is overlapping: [0,10] and [10,20]
			// would both claim an event at 10.
			if a.Player == b.Player && a.Start <= b.End && a.End >= b.Start {
				t.Errorf("windows %d and %d overlap: [%d,%d] and [%d,%d]",
					i, j, a.Start, a.End, b.Start, b.End)
			}
		}
	}
}

// The two caps compose, and the ORDER matters: perPlayer is applied before
// limit, so perPlayer=1&limit=3 means "the top 3, one per player" rather than
// "the top player's best 1".
func TestTopWindowsCaps(t *testing.T) {
	var frags []result.FragEntry
	// A dominates; B and C each get one good run.
	for i := int32(0); i < 6; i++ {
		frags = append(frags, kill(1000+i*10000, "A", "X", "rl"), kill(1100+i*10000, "A", "X", "rl"))
	}
	frags = append(frags, kill(5000, "B", "X", "lg"), kill(5500, "B", "X", "lg"))
	frags = append(frags, kill(15000, "C", "X", "lg"), kill(15500, "C", "X", "lg"))
	res := hwResult(frags, 120000)

	all := mustHW(t, res, TopWindowsOptions{WindowMs: 2000, Limit: -1})
	byPlayer := map[string]int{}
	for _, w := range all.Windows {
		byPlayer[w.Player]++
	}
	if byPlayer["A"] < 3 {
		t.Fatalf("fixture too weak: A has only %d windows", byPlayer["A"])
	}

	capped := mustHW(t, res, TopWindowsOptions{WindowMs: 2000, PerPlayer: 1, Limit: 3})
	if len(capped.Windows) != 3 {
		t.Fatalf("got %d windows, want 3", len(capped.Windows))
	}
	seen := map[string]int{}
	for _, w := range capped.Windows {
		seen[w.Player]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("perPlayer=1 but %s appears %d times — the caps were applied in the wrong order", p, n)
		}
	}
}

// Suicides and teamkills are not hot moments, so they never score — but they
// are part of the narrative, so they do appear in the stats block.
func TestTopWindowsSuicidesAndTeamkillsDoNotScore(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"),
		{Time: 1200, Killer: "A", Victim: "T", Weapon: "teamkill", IsTeamKill: true},
		{Time: 1400, Killer: "A", Victim: "A", Weapon: "suicide", IsSuicide: true},
	}
	got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{WindowMs: 5000})
	if len(got.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(got.Windows))
	}
	w := got.Windows[0]
	if w.Score != 1 {
		t.Errorf("score = %d, want 1 (only the real kill scores)", w.Score)
	}
	if w.Kills != 1 {
		t.Errorf("kills = %d, want 1", w.Kills)
	}
	if w.TeamKills != 1 {
		t.Errorf("teamKills = %d, want 1 — excluded from the score, kept in the stats", w.TeamKills)
	}
	if w.Suicides != 1 {
		t.Errorf("suicides = %d, want 1", w.Suicides)
	}
}

// weapons= scopes the SCORING events only; the stats block still describes
// everything that happened in the window. Score and the same-named stat
// therefore differ, which is why scoredBy exists.
func TestTopWindowsWeaponFilterScopesScoringOnly(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "lg"), kill(1500, "A", "B", "rl"),
		kill(2000, "A", "B", "lg"),
	}
	got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{
		WindowMs: 5000, Weapons: []string{"lg"},
	})
	w := firstWindow(t, got)
	if w.Score != 2 {
		t.Errorf("score = %d, want 2 (lg kills only)", w.Score)
	}
	if w.Kills != 3 {
		t.Errorf("kills = %d, want 3 (the stats block is unfiltered)", w.Kills)
	}
	if w.ByWeapon["rl"] != 1 {
		t.Errorf("byWeapon lost the rl kill: %v", w.ByWeapon)
	}
	// scoredBy lives on the ENVELOPE, once: it is invariant across the rows by
	// construction, and it is the only place the metric is echoed.
	if got.ScoredBy.Metric != MetricFrags || len(got.ScoredBy.Weapons) != 1 || got.ScoredBy.Weapons[0] != "lg" {
		t.Errorf("scoredBy = %+v, want metric=frags weapons=[lg]", got.ScoredBy)
	}
}

// netFrags is the metric that distinguishes a run from a trade.
func TestTopWindowsNetFrags(t *testing.T) {
	// A gets 4 kills but dies 3 times in the same stretch.
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(1200, "B", "A", "rl"),
		kill(1400, "A", "B", "rl"), kill(1600, "B", "A", "rl"),
		kill(1800, "A", "B", "rl"), kill(2000, "B", "A", "rl"),
		kill(2200, "A", "B", "rl"),
	}
	res := hwResult(frags, 60000)

	byFrags := topWindow(t, res, TopWindowsOptions{Metric: MetricFrags, WindowMs: 5000, Players: []string{"A"}})
	byNet := topWindow(t, res, TopWindowsOptions{Metric: MetricNetFrags, WindowMs: 5000, Players: []string{"A"}})
	if byFrags.Score != 4 {
		t.Errorf("frags score = %d, want 4", byFrags.Score)
	}
	if byNet.Score != 1 {
		t.Errorf("netFrags score = %d, want 1 (4 kills - 3 deaths)", byNet.Score)
	}
}

// A net metric can go negative; those windows are not "hot" and are dropped.
func TestTopWindowsDropsNonPositiveScores(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "B", "A", "rl"), kill(1500, "B", "A", "rl"),
	}
	got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{
		Metric: MetricNetFrags, WindowMs: 5000, Players: []string{"A"},
	})
	if len(got.Windows) != 0 {
		t.Errorf("got %+v, want no windows: A only died", got.Windows)
	}
}

// Every way a query can be refused, in one place — the closed vocabularies and
// the two family errors, on the metrics that reach each of them.
func TestTopWindowsRejects(t *testing.T) {
	withFrags := func() *result.Result {
		return hwResult([]result.FragEntry{kill(1000, "A", "B", "rl")}, 60000)
	}
	withShots := func() *result.Result {
		r := withFrags()
		r.Shots = &result.ShotsResult{}
		return r
	}
	// A skipped:* demo carries a damage section but no bounded reconstruction.
	skipped := func() *result.Result {
		r := withFrags()
		r.Damage = &result.DamageResult{BoundedMode: "skipped:midair"}
		return r
	}
	noStreams := func() *result.Result { return &result.Result{Streams: &result.Streams{}} }

	for _, tc := range []struct {
		name string
		res  *result.Result
		opts TopWindowsOptions
		want error // nil = the query must be ACCEPTED
	}{
		{"unknown metric", withFrags(), TopWindowsOptions{Metric: "efficiency"}, ErrInvalidFilter},
		{"unknown weapon", withFrags(), TopWindowsOptions{Weapons: []string{"banana"}}, ErrInvalidFilter},
		// lava is a real frag cause but cannot be FIRED, so it is valid on a
		// frag metric and invalid on a shot metric.
		{"lava on a frag metric", withFrags(), TopWindowsOptions{Metric: MetricDeaths, Weapons: []string{"lava"}}, nil},
		{"lava on a shot metric", withShots(), TopWindowsOptions{Metric: MetricShots, Weapons: []string{"lava"}}, ErrInvalidFilter},
		{"no frag log", noStreams(), TopWindowsOptions{}, ErrUnavailable},
		// An explicit dmg=bounded on a demo with no bounded family is the
		// caller's error under EVERY metric, not only the damage ones — a
		// silent fallback to raw is what let the family mismatch pinned by
		// TestTopWindowsDamageFamilyAppliesUnderEveryMetric hide.
		{"dmg=bounded on a skipped demo", skipped(), TopWindowsOptions{Metric: MetricDamageGiven, Dmg: "bounded"}, ErrBoundedUnavailable},
		{"dmg=bounded on a skipped demo, metric=frags", skipped(), TopWindowsOptions{Metric: MetricFrags, Dmg: "bounded"}, ErrBoundedUnavailable},
		{"dmg=both", skipped(), TopWindowsOptions{Metric: MetricDamageGiven, Dmg: "both"}, ErrInvalidFilter},
		{"dmg=banana, metric=frags", skipped(), TopWindowsOptions{Metric: MetricFrags, Dmg: "banana"}, ErrInvalidFilter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := TopWindows(tc.res, tc.opts)
			switch {
			case tc.want == nil && err != nil:
				t.Errorf("err = %v, want the query accepted", err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// The golden corpus pins output byte-for-byte, so identical input must give
// identical bytes however Go decides to order its maps this run.
func TestTopWindowsDeterministic(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "lg"), kill(1100, "A", "C", "rl"),
		kill(1200, "A", "B", "lg"), kill(1300, "A", "C", "rl"),
		// A second player with an identical-scoring run, to exercise the
		// player tie-break.
		kill(5000, "Z", "B", "lg"), kill(5100, "Z", "C", "rl"),
		kill(5200, "Z", "B", "lg"), kill(5300, "Z", "C", "rl"),
	}
	res := hwResult(frags, 60000)
	var first []byte
	for i := 0; i < 50; i++ {
		got := mustHW(t, res, TopWindowsOptions{WindowMs: 2000, Limit: -1})
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Marshalling `{"windows":[]}` 50 times is stable for reasons that
			// have nothing to do with this code, so pin content too: both
			// players go lg=2, rl=2 inside their window, which is exactly the
			// topCountKey tie the sorted-key rule exists for.
			if len(got.Windows) != 2 {
				t.Fatalf("got %d windows, want 2 — the tie fixture no longer ties", len(got.Windows))
			}
			for _, w := range got.Windows {
				if w.MainWeapon != "lg" {
					t.Fatalf("%s mainWeapon = %q, want lg (2 lg / 2 rl, ties break by name)", w.Player, w.MainWeapon)
				}
			}
			first = b
			continue
		}
		if string(b) != string(first) {
			t.Fatalf("run %d differs from run 0:\n %s\n %s", i, first, b)
		}
	}
}

// An interval beginning at t=0 is the case that makes reusing view.Frags with
// From/To wrong — 0 reads as "no bound" there, so the window would silently be
// handed whole-match aggregates.
func TestIntervalStatsBoundedAtZero(t *testing.T) {
	frags := []result.FragEntry{
		kill(0, "A", "B", "rl"), kill(500, "A", "B", "rl"),
		kill(50000, "A", "B", "lg"), // far outside the window
	}
	w := topWindow(t, hwResult(frags, 120000), TopWindowsOptions{WindowMs: 1000, Limit: 1})
	if w.Start != 0 {
		t.Fatalf("start = %d, want 0", w.Start)
	}
	if w.Kills != 2 {
		t.Errorf("kills = %d, want 2 — the window starting at t=0 got whole-match aggregates", w.Kills)
	}
}

// Signed metrics broke the original event-anchored search, in two separate
// ways. Anchoring windows only at event times is optimal for non-negative
// values; with negatives, sliding right until the left edge meets an event can
// pull a NEGATIVE onto the right edge. Candidate starts therefore include the
// domain's own left edge and t-windowMs — and t+1.
//
// The t+1 anchor is incident F2 (adversarial differential review, 2026-08-01):
// the candidate starts missed the breakpoint where an event LEAVES the window.
// Event k counts over the closed start range [t_k-W, t_k], so it leaves at
// t_k+1; anchoring only at t_k never visits the constant piece that begins just
// after a negative event drops out.
//
// All three cases are brute-force verified optima that an anchor-poor version
// got wrong — the first two returned NO window at all, the third a suboptimal
// one.
func TestTopWindowsFindsSignedOptimum(t *testing.T) {
	t.Run("the optimum starts at the domain edge", func(t *testing.T) {
		// A kills at t=1 (+1) and dies at t=11 (-1). windowMs=10.
		// [0,10] scores +1; the only event-anchored candidate [1,11] scores 0.
		frags := []result.FragEntry{
			kill(1, "A", "B", "rl"),
			{Time: 11, Killer: "B", Victim: "A", Weapon: "rl"},
		}
		got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{
			Metric: MetricNetFrags, WindowMs: 10, Players: []string{"A"},
		})
		if len(got.Windows) == 0 {
			t.Fatal("no window found; the positive-scoring window [0,10] was missed")
		}
		if got.Windows[0].Score != 1 {
			t.Errorf("score = %d, want 1", got.Windows[0].Score)
		}
	})

	t.Run("negative leaves at t+1", func(t *testing.T) {
		// A: -7 at t=1, +2 at t=5, -3 at t=7; windowMs=4. The only positive
		// window is [2,6] — 2 is the instant the -7 stops counting.
		res := hwResult(nil, 60000)
		res.Damage = &result.DamageResult{Events: []result.DamageEntry{
			dmg(1, "B", "A", "rl", 7), dmg(5, "A", "B", "rl", 2), dmg(7, "B", "A", "rl", 3),
		}}
		got := mustHW(t, res, TopWindowsOptions{
			Metric: MetricNetDamage, WindowMs: 4, Players: []string{"A"},
		})
		if len(got.Windows) == 0 {
			t.Fatal("no window: the optimum [2,6] starts one ms after the negative left")
		}
		w := got.Windows[0]
		if w.Score != 2 || w.Start != 2 || w.End != 6 {
			t.Errorf("got [%d,%d] score %d, want [2,6] score 2", w.Start, w.End, w.Score)
		}
	})

	t.Run("with from/to bounds", func(t *testing.T) {
		// A: -4 at t=4, +5 at t=7; windowMs=8, starts bounded to [1,5]. The
		// best start is 5 (score 5); anchoring at events alone finds 4 (score 1).
		res := hwResult(nil, 60000)
		res.Damage = &result.DamageResult{Events: []result.DamageEntry{
			dmg(4, "B", "A", "rl", 4), dmg(7, "A", "B", "rl", 5),
		}}
		w := topWindow(t, res, TopWindowsOptions{
			Metric: MetricNetDamage, WindowMs: 8, From: 1, To: 5, Players: []string{"A"},
		})
		if w.Score != 5 || w.Start != 5 {
			t.Errorf("got start %d score %d, want start 5 score 5", w.Start, w.Score)
		}
	})
}

// A brute-force differential: for random signed event sets, the reported best
// score must equal an O(n·T) reference over EVERY integer start in the domain.
// Enumerating only the anchor families the implementation itself uses would
// prove that the code agrees with its own theory, not that the theory is right.
func TestTopWindowsMatchesBruteForce(t *testing.T) {
	const windowMs = 1000
	const horizon = 20000
	seedFrags := func(seed int) []result.FragEntry {
		var f []result.FragEntry
		x := seed*7919 + 13
		for i := 0; i < 40; i++ {
			x = (x*1103515245 + 12345) & 0x7fffffff
			at := int32(x % horizon)
			if x%3 == 0 {
				f = append(f, result.FragEntry{Time: at, Killer: "B", Victim: "A", Weapon: "rl"})
			} else {
				f = append(f, kill(at, "A", "B", "rl"))
			}
		}
		return f
	}
	for seed := 0; seed < 40; seed++ {
		frags := seedFrags(seed)
		got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{
			Metric: MetricNetFrags, WindowMs: windowMs, Players: []string{"A"}, Limit: 1,
		})
		// Reference: score every integer start the match window admits.
		best := 0
		for st := int32(0); st <= horizon; st++ {
			sum := 0
			for _, f := range frags {
				if f.Time < st || f.Time > st+windowMs {
					continue
				}
				if f.Killer == "A" {
					sum++
				}
				if f.Victim == "A" {
					sum--
				}
			}
			if sum > best {
				best = sum
			}
		}
		var reported int
		if len(got.Windows) > 0 {
			reported = got.Windows[0].Score
		}
		if reported != best {
			t.Fatalf("seed %d: reported best score %d, brute force says %d", seed, reported, best)
		}
	}
}

// Spans are closed at both ends for scoring AND for the stats block, so two
// windows sharing an endpoint would both claim an event at that instant. The
// overlap test therefore treats touching as overlapping.
func TestTopWindowsTouchingWindowsDoNotShareAnEvent(t *testing.T) {
	frags := []result.FragEntry{
		kill(0, "A", "B", "rl"), kill(10, "A", "B", "rl"), kill(20, "A", "B", "rl"),
	}
	got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{WindowMs: 10, Limit: -1})
	total := 0
	for _, w := range got.Windows {
		total += w.Score
	}
	if total > 3 {
		t.Errorf("windows %+v claim %d kills between them, but only 3 exist — a shared endpoint was double counted",
			got.Windows, total)
	}
}

// The window is exactly windowMs long and its score covers all of it. Clipping
// the scoring at `to` while reporting an unclipped span would make score
// disagree with the stats block computed over that same span.
func TestTopWindowsScoreMatchesStatsAtTheToBound(t *testing.T) {
	frags := []result.FragEntry{kill(90, "A", "B", "rl"), kill(110, "A", "B", "rl")}
	w := topWindow(t, hwResult(frags, 60000), TopWindowsOptions{
		WindowMs: 30, To: 100, Limit: 1,
	})
	if w.Score != w.Kills {
		t.Errorf("score = %d but stats say kills = %d over the same span [%d,%d]",
			w.Score, w.Kills, w.Start, w.End)
	}
}

// A non-world environmental hit still credits its player attacker in
// view.Damage, so it must here too — otherwise metric=damageGiven disagrees
// with the /damage endpoint on the same demo.
func TestTopWindowsDamageMatchesDamageView(t *testing.T) {
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{Events: []result.DamageEntry{
		{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 40, IsEnv: true},
	}}
	got := mustHW(t, res, TopWindowsOptions{Metric: MetricDamageGiven, WindowMs: 5000})
	if len(got.Windows) == 0 || got.Windows[0].Score != 40 {
		t.Errorf("got %+v, want a window scoring 40 — an IsEnv hit with a player attacker is enemy damage in /damage", got.Windows)
	}
}

// A drowning death is "water" in the frag log and "drown" in the damage log —
// the same event under two spellings. Both are accepted on both sides, so a
// caller does not have to know which log backs the metric they picked. The
// alias has to be in the VOCABULARY, not just the matcher: validation runs
// first, so a matcher-only alias is inert and the request still 400s.
func TestTopWindowsWaterDrownAliasAccepted(t *testing.T) {
	res := hwResult([]result.FragEntry{kill(1000, "A", "B", "rl")}, 60000)
	res.Damage = &result.DamageResult{Events: []result.DamageEntry{
		{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 10},
	}}
	for _, tc := range []struct{ metric, weapon string }{
		{MetricDeaths, "water"},
		{MetricDeaths, "drown"},
		{MetricDamageTaken, "drown"},
		{MetricDamageTaken, "water"},
	} {
		if _, err := TopWindows(res, TopWindowsOptions{
			Metric: tc.metric, Weapons: []string{tc.weapon}, WindowMs: 5000,
		}); errors.Is(err, ErrInvalidFilter) {
			t.Errorf("metric=%s weapons=%s rejected: %v", tc.metric, tc.weapon, err)
		}
	}
	// The sibling endpoints gain the same acceptance, which was the point —
	// and an accepted token must MATCH, or the vocabulary has merely turned a
	// 400 that named the valid set into a silent empty 200, which is the exact
	// failure the closed vocabulary exists to prevent.
	//
	// Incident (adversarial review, 2026-08-01): the alias was added to the
	// vocabularies (validation) while the expansion lived only in the
	// top-window scoring matcher. Measured on 212260, /frags?weapons=drown went
	// from a 400 to `200 {"totalFrags":0}` and /damage?weapons=water to
	// `200 {"totalDamage":0}`, for the two tokens a caller is most likely to
	// guess wrong.
	aliased := hwResult([]result.FragEntry{
		{Time: 1000, Killer: "B", Victim: "A", Weapon: "water", IsSuicide: true},
	}, 60000)
	aliased.Damage = &result.DamageResult{Events: []result.DamageEntry{
		{Time: 1000, Attacker: "world", Victim: "A", Weapon: "drown", Damage: 6, IsEnv: true},
	}}
	for _, tok := range []string{"water", "drown"} {
		fv, err := Frags(aliased, FragOptions{Weapons: []string{tok}})
		if err != nil {
			t.Fatalf("/frags?weapons=%s: %v", tok, err)
		}
		if fv.TotalFrags != 1 {
			t.Errorf("/frags?weapons=%s matched %d entries, want the one `water` drowning — "+
				"an accepted token that matches nothing is the silent-empty result the vocabulary forbids",
				tok, fv.TotalFrags)
		}
		dv, err := Damage(aliased, DamageOptions{Weapons: []string{tok}})
		if err != nil {
			t.Fatalf("/damage?weapons=%s: %v", tok, err)
		}
		if dv.TotalDamage != 6 {
			t.Errorf("/damage?weapons=%s summed %d, want the one `drown` hit (6)", tok, dv.TotalDamage)
		}
		// And the scoring path agrees with them, from the same set builder.
		w := topWindow(t, aliased, TopWindowsOptions{
			Metric: MetricDeaths, Weapons: []string{tok}, WindowMs: 5000, Players: []string{"A"},
		})
		if w.Score != 1 {
			t.Errorf("metric=deaths weapons=%s: score = %d, want one window scoring 1", tok, w.Score)
		}
	}
}

// A bare call — the shape every MCP agent will make first — must be a 30 s,
// top-10, frags query, and the envelope must say so. Nothing else pins the
// defaults: every other test passes an explicit WindowMs.
//
// TimeUnit is deliberately not asserted: the view leaves it zero and the HTTP
// layer stamps view.UnitMs on the way out, like every sibling envelope.
func TestTopWindowsDefaultsAndEnvelope(t *testing.T) {
	// 15 players, one kill each, spaced far enough apart that every player
	// contributes exactly one window — so the row count measures `limit`.
	var frags []result.FragEntry
	for i := 0; i < 15; i++ {
		frags = append(frags, kill(int32(1000+i*100000), string(rune('a'+i)), "X", "rl"))
	}
	got := mustHW(t, hwResult(frags, 2000000), TopWindowsOptions{})
	if got.ScoredBy.Metric != MetricFrags {
		t.Errorf("scoredBy.metric = %q, want %q", got.ScoredBy.Metric, MetricFrags)
	}
	if got.WindowMs != 30000 {
		t.Errorf("windowMs = %d, want 30000", got.WindowMs)
	}
	if got.Limit != 10 {
		t.Errorf("limit = %d, want 10", got.Limit)
	}
	if got.PerPlayer != 0 {
		t.Errorf("perPlayer = %d, want 0 (a bare call has no diversity cap)", got.PerPlayer)
	}
	if len(got.Windows) != 10 {
		t.Fatalf("got %d windows, want 10 — the default limit did not bite", len(got.Windows))
	}
	for i, w := range got.Windows {
		if w.Rank != i+1 {
			t.Errorf("windows[%d].rank = %d, want %d", i, w.Rank, i+1)
		}
		if w.End-w.Start != 30000 {
			t.Errorf("windows[%d] spans %d ms, want the default 30000", i, w.End-w.Start)
		}
		if w.DurationMs != 30000 {
			t.Errorf("windows[%d].durationMs = %d, want 30000", i, w.DurationMs)
		}
	}
}

// limit is capped, and the cap is echoed — a caller asking for 1000 must be
// able to see from the response that it got 200.
func TestTopWindowsLimitCap(t *testing.T) {
	var frags []result.FragEntry
	for i := 0; i < 250; i++ {
		frags = append(frags, result.FragEntry{
			Time: int32(1000 + i*100000), Killer: "p" + string(rune('A'+i%26)) + string(rune('a'+i/26)),
			Victim: "X", Weapon: "rl",
		})
	}
	got := mustHW(t, hwResult(frags, 30000000), TopWindowsOptions{WindowMs: 5000, Limit: 1000})
	if len(got.Windows) != topWindowMaxLimit {
		t.Errorf("got %d windows, want the %d cap", len(got.Windows), topWindowMaxLimit)
	}
	if got.Limit != topWindowMaxLimit {
		t.Errorf("echoed limit = %d, want the effective %d", got.Limit, topWindowMaxLimit)
	}
}

// The metric name is matched case-insensitively but ECHOED canonically, so a
// caller can round-trip the value. Lower-casing the input and comparing it
// against the vocabulary directly would reject every camelCase name.
func TestTopWindowsMetricIsCaseInsensitive(t *testing.T) {
	res := hwResult([]result.FragEntry{
		kill(1000, "A", "B", "rl"), {Time: 1200, Killer: "B", Victim: "A", Weapon: "rl"},
	}, 60000)
	for _, in := range []string{"netFrags", "netfrags", "NETFRAGS", "NetFrags", "  netFrags  "} {
		got := mustHW(t, res, TopWindowsOptions{Metric: in, WindowMs: 5000})
		if got.ScoredBy.Metric != MetricNetFrags {
			t.Errorf("metric=%q echoed as %q, want %q", in, got.ScoredBy.Metric, MetricNetFrags)
		}
	}
}

// min is the score floor, and 0 is a MEANINGFUL request — "keep the windows
// that broke even" is coherent for a net metric and unreachable if a plain int
// carried it.
func TestTopWindowsMin(t *testing.T) {
	// A trades evenly: 2 kills, 2 deaths, so every window scores at most 0.
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), {Time: 1100, Killer: "B", Victim: "A", Weapon: "rl"},
		kill(1200, "A", "B", "rl"), {Time: 1300, Killer: "B", Victim: "A", Weapon: "rl"},
	}
	res := hwResult(frags, 60000)
	opts := func(min *int) TopWindowsOptions {
		return TopWindowsOptions{Metric: MetricNetFrags, WindowMs: 5000, Players: []string{"A"}, Min: min}
	}
	zero, three := 0, 3
	if got := mustHW(t, res, opts(nil)); len(got.Windows) != 0 {
		t.Errorf("default min: got %+v, want nothing — an even trade is not hot", got.Windows)
	}
	if got := mustHW(t, res, opts(&zero)); len(got.Windows) == 0 || got.Windows[0].Score != 0 {
		t.Errorf("min=0: got %+v, want a window scoring 0", got.Windows)
	}
	// And a floor above the best score empties the list again.
	if got := mustHW(t, res, opts(&three)); len(got.Windows) != 0 {
		t.Errorf("min=3: got %+v, want nothing", got.Windows)
	}
}

// From/To bound where a window may START, so the same demo yields the early run
// under To and the late one under From.
func TestTopWindowsFromTo(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(1500, "A", "B", "rl"),
		kill(50000, "A", "B", "lg"), kill(50500, "A", "B", "lg"),
	}
	res := hwResult(frags, 120000)
	early := mustHW(t, res, TopWindowsOptions{WindowMs: 5000, To: 2000, Limit: -1})
	if len(early.Windows) != 1 || early.Windows[0].Start != 1000 {
		t.Fatalf("to=2000: got %+v, want just the run starting at 1000", early.Windows)
	}
	late := mustHW(t, res, TopWindowsOptions{WindowMs: 5000, From: 40000, Limit: -1})
	if len(late.Windows) != 1 || late.Windows[0].Start != 50000 {
		t.Fatalf("from=40000: got %+v, want just the run starting at 50000", late.Windows)
	}
}

// The three damage metrics read the same log from three sides. damageTaken
// counts ALL sources (matching PlayerDamage.Taken), so the self hit lands in
// it; it is never "given".
func TestTopWindowsDamageMetrics(t *testing.T) {
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{Events: []result.DamageEntry{
		{Time: 1000, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 100},
		{Time: 1200, Attacker: "B", Victim: "A", Weapon: "rl", Damage: 30},
		{Time: 1400, Attacker: "A", Victim: "A", Weapon: "rl", Damage: 20, IsSelf: true},
	}}
	// The metric picks the window, and the stats block then describes THAT
	// window from every side — which is why damageTaken, whose first scoring
	// event is the t=1200 hit, reports no damage given: the t=1000 hit is
	// before its window starts.
	for _, tc := range []struct {
		metric                  string
		want, start             int
		given, taken, givenSelf int
	}{
		{MetricDamageGiven, 100, 1000, 100, 50, 20},
		{MetricDamageTaken, 50, 1200, 0, 50, 20}, // 30 from B + 20 self
		{MetricNetDamage, 50, 1000, 100, 50, 20}, // 100 given - 50 taken
	} {
		got := mustHW(t, res, TopWindowsOptions{
			Metric: tc.metric, WindowMs: 5000, Players: []string{"A"},
		})
		w := firstWindow(t, got)
		if w.Score != tc.want {
			t.Errorf("%s: score = %d, want %d", tc.metric, w.Score, tc.want)
		}
		if int(w.Start) != tc.start {
			t.Errorf("%s: start = %d, want %d", tc.metric, w.Start, tc.start)
		}
		if w.DamageGiven != tc.given || w.DamageTaken != tc.taken || w.DamageGivenSelf != tc.givenSelf {
			t.Errorf("%s: stats given/taken/self = %d/%d/%d, want %d/%d/%d",
				tc.metric, w.DamageGiven, w.DamageTaken, w.DamageGivenSelf,
				tc.given, tc.taken, tc.givenSelf)
		}
		if got.ScoredBy.Dmg != "raw" {
			t.Errorf("%s: scoredBy.dmg = %q, want raw (the view default; the REST layer defaults to bounded)",
				tc.metric, got.ScoredBy.Dmg)
		}
	}
}

// dmg=bounded scores the KTX-scoreboard reconstruction instead of the raw hit,
// and a nil Bounded means "equal to Damage" — the common no-overkill case, NOT
// zero. Getting that backwards would silently halve every bounded window.
func TestTopWindowsBoundedDamageFamily(t *testing.T) {
	bounded := 45
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{
		BoundedMode: "standard",
		Events: []result.DamageEntry{
			{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 200, Bounded: &bounded},
			{Time: 1200, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 40}, // nil Bounded
		},
	}
	for _, tc := range []struct {
		dmg  string
		want int
	}{
		{"raw", 240},
		{"bounded", 85}, // 45 (capped overkill) + 40 (nil Bounded = Damage)
	} {
		got := mustHW(t, res, TopWindowsOptions{
			Metric: MetricDamageGiven, WindowMs: 5000, Dmg: tc.dmg, Players: []string{"A"},
		})
		w := firstWindow(t, got)
		if w.Score != tc.want {
			t.Errorf("dmg=%s: score = %d, want %d", tc.dmg, w.Score, tc.want)
		}
		if w.DamageGiven != tc.want {
			t.Errorf("dmg=%s: stats damageGiven = %d, want %d — the stats block must read the same family",
				tc.dmg, w.DamageGiven, tc.want)
		}
		if got.ScoredBy.Dmg != tc.dmg {
			t.Errorf("dmg=%s: scoredBy.dmg = %q", tc.dmg, got.ScoredBy.Dmg)
		}
	}
}

// A positional kill (telefrag / stomp) SCORES for the damage metrics and folds
// into the stats block alike, so that absent a weapons= filter a window's score
// equals the same-named field of its own row.
//
// It reverses an earlier rule — folded but never scored — that rested on the
// premise that a telefrag's value is the 9999 wire sentinel. It is not: the
// analyzer reconstructs it (victim armor + remaining health), and across the 42
// cached demos the 82 positional kills run 0..298 with a median of 100. Under
// the old rule metric=damageGiven did not score damageGiven — a telefrag-only
// stretch produced no candidate at all while /damage reported damage for it.
func TestTopWindowsPositionalKillsScoreAndFoldAlike(t *testing.T) {
	hit, tele := 100, 300
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{
		BoundedMode: "standard",
		Events: []result.DamageEntry{
			{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: hit, Bounded: &hit},
		},
		Telefrags: []result.PositionalKill{
			{Time: 2000, Attacker: "A", Victim: "B", Bounded: &tele},
		},
	}
	w := topWindow(t, res, TopWindowsOptions{Metric: MetricDamageGiven, WindowMs: 5000, Players: []string{"A"}})
	if w.Score != hit+tele {
		t.Errorf("score = %d, want %d — the telefrag value scores as well as folding", w.Score, hit+tele)
	}
	if w.DamageGiven != w.Score {
		t.Errorf("damageGiven = %d but score = %d; with no weapons filter the two are the same number",
			w.DamageGiven, w.Score)
	}
	// damageByWeapon excludes it, exactly as /damage's byWeapon does: a
	// positional kill has no weapon to file it under.
	if w.DamageByWeapon["rl"] != hit {
		t.Errorf("damageByWeapon = %v, want only the rl hit", w.DamageByWeapon)
	}
	// And the whole point: it reconciles against /damage over the same span.
	dv, err := Damage(res, DamageOptions{From: w.Start, To: w.End})
	if err != nil {
		t.Fatal(err)
	}
	if pd := dv.ByPlayer["A"]; pd == nil || pd.Given != w.DamageGiven {
		t.Errorf("/damage given = %+v, stats damageGiven = %d", pd, w.DamageGiven)
	}
	if pd := dv.ByPlayer["B"]; pd == nil || pd.Taken != hit+tele {
		t.Errorf("/damage taken for the victim = %+v, want %d", pd, hit+tele)
	}

	// A telefrag-only stretch is now a candidate at all, which is what "the
	// metric does not score itself" cost: [1990,6990] holds no hit.
	late := topWindow(t, res, TopWindowsOptions{
		Metric: MetricDamageGiven, WindowMs: 5000, From: 1990, Players: []string{"A"},
	})
	if late.Score != tele {
		t.Errorf("windows after the last hit scored %d, want %d", late.Score, tele)
	}

	// The pseudo-tokens select them, exactly as DamageOptions.Weapons does —
	// a positional kill carries no wire weapon, so "tele"/"stomp" are how a
	// caller names one.
	// to=500 pins the window to [0,5000], which holds both the hit and the
	// telefrag, so the two numbers are comparable: the filter scopes the SCORE
	// while the stats block still describes everything in the span.
	only := topWindow(t, res, TopWindowsOptions{
		Metric: MetricDamageGiven, WindowMs: 5000, To: 500, Weapons: []string{"tele"}, Players: []string{"A"},
	})
	if only.Score != tele {
		t.Fatalf("weapons=tele: score = %d, want %d (the telefrag alone)", only.Score, tele)
	}
	if only.DamageGiven != hit+tele {
		t.Errorf("weapons=tele: damageGiven = %d, want %d — the filter scopes the SCORE, not the stats",
			only.DamageGiven, hit+tele)
	}
	rlOnly := topWindow(t, res, TopWindowsOptions{
		Metric: MetricDamageGiven, WindowMs: 5000, Weapons: []string{"rl"}, Players: []string{"A"},
	})
	if rlOnly.Score != hit {
		t.Errorf("weapons=rl: score = %d, want %d (the hit alone)", rlOnly.Score, hit)
	}
}

// The scoring side and the stats side must classify a positional kill the same
// way, or score and stat part company on the very rows the fold exists for: a
// TEAM telefrag is givenTeam and never damageGiven, a self-telefrag is
// givenSelf, and both are `taken` for the victim whoever dealt them.
func TestTopWindowsPositionalKillClassification(t *testing.T) {
	v := 200
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{
		BoundedMode: "standard",
		Telefrags: []result.PositionalKill{
			{Time: 1000, Attacker: "A", Victim: "T", IsTeam: true, Bounded: &v},
		},
	}
	given := mustHW(t, res, TopWindowsOptions{Metric: MetricDamageGiven, WindowMs: 5000, Players: []string{"A"}})
	if len(given.Windows) != 0 {
		t.Errorf("damageGiven scored %+v on a TEAM telefrag; team damage is not damageGiven", given.Windows)
	}
	w := topWindow(t, res, TopWindowsOptions{Metric: MetricDamageTaken, WindowMs: 5000, Players: []string{"T"}})
	if w.Score != v {
		t.Fatalf("damageTaken score = %d, want %d", w.Score, v)
	}
	if w.DamageTaken != w.Score {
		t.Errorf("damageTaken stat = %d, score = %d", w.DamageTaken, w.Score)
	}
}

// The damage family is a property of the RESPONSE, not of the metric: the
// stats block reports damage whatever selected the window, so dmg must be
// honoured — and ECHOED — under metric=frags exactly as under damageGiven.
//
// Incident (adversarial review, 2026-08-01): the family was resolved only for
// the damage metrics, so the REST layer's dmg=bounded default was silently
// dropped for every other one. On 212260 the top frag window reported
// damageGiven 1676 against 795 from /damage over the same span, and nothing in
// the response said which family produced either number. The rejection half —
// an explicit dmg=bounded on a demo with no bounded family must fail under
// EVERY metric, not fall back to raw — is pinned in TestTopWindowsRejects.
func TestTopWindowsDamageFamilyAppliesUnderEveryMetric(t *testing.T) {
	bounded := 40
	res := hwResult([]result.FragEntry{kill(1000, "A", "B", "rl")}, 60000)
	res.Damage = &result.DamageResult{
		BoundedMode: "standard",
		Events: []result.DamageEntry{
			{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 100, Bounded: &bounded},
		},
	}
	for _, tc := range []struct {
		metric, dmg string
		wantGiven   int
	}{
		{MetricFrags, "", 100},
		{MetricFrags, "raw", 100},
		{MetricFrags, "bounded", 40},
		{MetricShots, "bounded", 40},
		{MetricDamageGiven, "bounded", 40},
	} {
		res.Shots = &result.ShotsResult{Shots: []result.Shot{{Time: 1000, Player: "A", Weapon: "rl", Hit: true}}}
		got := mustHW(t, res, TopWindowsOptions{
			Metric: tc.metric, Dmg: tc.dmg, WindowMs: 5000, Players: []string{"A"},
		})
		w := firstWindow(t, got)
		if w.DamageGiven != tc.wantGiven {
			t.Errorf("metric=%s dmg=%q: stats damageGiven = %d, want %d — the requested family was dropped",
				tc.metric, tc.dmg, w.DamageGiven, tc.wantGiven)
		}
		// And the envelope says which family produced it, on every metric.
		wantFam := "raw"
		if tc.dmg == "bounded" {
			wantFam = "bounded"
		}
		if got.Dmg != wantFam {
			t.Errorf("metric=%s dmg=%q: envelope dmg = %q, want %q", tc.metric, tc.dmg, got.Dmg, wantFam)
		}
		if got.BoundedMode != "standard" {
			t.Errorf("metric=%s: boundedMode = %q, want standard (echoed as /damage echoes it)",
				tc.metric, got.BoundedMode)
		}
		if isDamageMetric(tc.metric) && got.ScoredBy.Dmg != got.Dmg {
			t.Errorf("scoredBy.dmg = %q but the stats block used %q", got.ScoredBy.Dmg, got.Dmg)
		}
	}

	// A demo with no damage stream at all still answers a frag query: there is
	// no family to resolve and no damage to report, and measured.damage says so.
	noDmg := hwResult([]result.FragEntry{kill(1000, "A", "B", "rl")}, 60000)
	got := mustHW(t, noDmg, TopWindowsOptions{Metric: MetricFrags, Dmg: "bounded"})
	if got.Dmg != "" || got.BoundedMode != "" || got.Measured.Damage {
		t.Errorf("no damage stream: dmg=%q boundedMode=%q measured.damage=%v; want all empty/false",
			got.Dmg, got.BoundedMode, got.Measured.Damage)
	}
}

// A skipped:* demo carries no bounded reconstruction, and the analyzer folds
// nothing into its stored totals — so neither may the stats block NOR the
// score, or they would invent damage /damage does not report.
//
// The telefrag deliberately carries a nonzero raw value even though a real
// skipped:* demo leaves both value fields unset (analyzer/damage.go only
// populates them under `boundedSkip == ""`). A valueless fixture contributes
// zero through the `v <= 0` skip whether or not the hasBoundedFamily gates are
// there, so it pins nothing: this test passed unchanged with the scoring-side
// gate in collectScoreEvents deleted. The gate, not the missing number, has to
// be what stops the fold.
func TestTopWindowsNoFoldWithoutABoundedFamily(t *testing.T) {
	hit, tele := 100, 300
	res := hwResult(nil, 60000)
	res.Damage = &result.DamageResult{
		BoundedMode: "skipped:midair",
		Events: []result.DamageEntry{
			{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: hit},
		},
		// No Bounded: the reconstruction never ran. Damage is the raw-family
		// fold value, and it must be ignored all the same.
		Telefrags: []result.PositionalKill{{Time: 2000, Attacker: "A", Victim: "B", Damage: tele}},
	}
	w := topWindow(t, res, TopWindowsOptions{Metric: MetricDamageGiven, WindowMs: 5000, Players: []string{"A"}})
	if w.Score != hit {
		t.Errorf("score = %d, want %d — a skipped:* demo scores no positional kill", w.Score, hit)
	}
	if w.DamageGiven != hit {
		t.Errorf("damageGiven = %d, want %d — a skipped:* demo folds nothing", w.DamageGiven, hit)
	}
}

// shots counts fires, hits counts connects — activity vs reward, over the same
// stream and the same window.
func TestTopWindowsShotsAndHits(t *testing.T) {
	res := hwResult(nil, 60000)
	res.Shots = &result.ShotsResult{Shots: []result.Shot{
		{Time: 1000, Player: "A", Weapon: "lg", Hit: true},
		{Time: 1100, Player: "A", Weapon: "lg"},
		{Time: 1200, Player: "A", Weapon: "lg", Hit: true},
		{Time: 1300, Player: "A", Weapon: "rl", Hit: true},
		{Time: 1400, Player: "A", Weapon: "rl"},
		{Time: 40000, Player: "A", Weapon: "lg", Hit: true}, // far outside
	}}
	for _, tc := range []struct {
		metric  string
		weapons []string
		want    int
	}{
		{MetricShots, nil, 5},
		{MetricHits, nil, 3},
		{MetricShots, []string{"lg"}, 3},
		{MetricHits, []string{"lg"}, 2},
	} {
		w := topWindow(t, res, TopWindowsOptions{
			Metric: tc.metric, Weapons: tc.weapons, WindowMs: 5000, Players: []string{"A"},
		})
		if w.Score != tc.want {
			t.Errorf("%s weapons=%v: score = %d, want %d", tc.metric, tc.weapons, w.Score, tc.want)
		}
		// The stats block is never weapon-filtered, so it reports the whole
		// window whatever the score counted.
		if w.Shots != 5 || w.Hits != 3 {
			t.Errorf("%s weapons=%v: stats shots/hits = %d/%d, want 5/3 (unfiltered)",
				tc.metric, tc.weapons, w.Shots, w.Hits)
		}
	}
}

// locs is where the TIME went, eventLocs where the kills landed. They answer
// different questions and routinely disagree — this fixture makes them
// disagree on purpose, because a builder that fed one from the other would
// still pass a test where they match.
func TestTopWindowsLocsAndEventLocs(t *testing.T) {
	res := hwResult([]result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(3500, "A", "B", "lg"),
	}, 60000)
	res.TimelineAnalysis = &result.TimelineAnalysisResult{LocTable: []string{"", "rl", "ya"}}
	res.Streams.Players = []result.PlayerStream{{
		Name: "A",
		Loc:  []result.ChangeI16{{T: 0, V: 1}, {T: 3000, V: 2}},
	}}
	w := topWindow(t, res, TopWindowsOptions{WindowMs: 5000, Limit: 1})
	if w.Start != 1000 || w.End != 6000 {
		t.Fatalf("window = [%d,%d], want [1000,6000]", w.Start, w.End)
	}
	// Dwell: 1000-3000 in rl, 3000-6000 in ya, clipped to the window.
	want := []IntervalLoc{{Loc: "ya", Ms: 3000}, {Loc: "rl", Ms: 2000}}
	if len(w.Locs) != len(want) {
		t.Fatalf("locs = %+v, want %+v", w.Locs, want)
	}
	for i := range want {
		if w.Locs[i] != want[i] {
			t.Errorf("locs[%d] = %+v, want %+v", i, w.Locs[i], want[i])
		}
	}
	// Kills: one from rl, one from ya — the reverse ranking of the dwell.
	wantEv := []IntervalLoc{{Loc: "rl", Count: 1}, {Loc: "ya", Count: 1}}
	if len(w.EventLocs) != len(wantEv) {
		t.Fatalf("eventLocs = %+v, want %+v", w.EventLocs, wantEv)
	}
	for i := range wantEv {
		if w.EventLocs[i] != wantEv[i] {
			t.Errorf("eventLocs[%d] = %+v, want %+v", i, w.EventLocs[i], wantEv[i])
		}
	}
}

// Regression, incident F3 (adversarial differential review, 2026-08-01): the
// window end was computed in int64 and then narrowed into an int32 field.
// windowMs reaches the view unclamped — the HTTP layer accepts any 0..MaxInt32
// — so a MaxInt32 window wrapped to end = -2147482649, i.e. End < Start, a
// negative durationMs and an overlap predicate comparing garbage.
func TestTopWindowsClampsTheWindowEnd(t *testing.T) {
	got := mustHW(t, hwResult([]result.FragEntry{kill(1000, "A", "B", "rl")}, 60000),
		TopWindowsOptions{WindowMs: math.MaxInt32, Limit: -1})
	if len(got.Windows) == 0 {
		t.Fatal("no window")
	}
	// EVERY row, and limit=-1 so every row is here. Asserting on windows[0]
	// alone proved nothing: the candidate anchored at the domain edge starts at
	// 0, so its end stays inside int32 either way, and it sorts ABOVE the
	// wrapped one — this test passed unchanged with the clamp deleted.
	for _, w := range got.Windows {
		if w.End < w.Start {
			t.Errorf("window = [%d,%d]: end wrapped past int32", w.Start, w.End)
		}
		if w.End != math.MaxInt32 {
			t.Errorf("window starting at %d: end = %d, want the int32 ceiling %d",
				w.Start, w.End, int32(math.MaxInt32))
		}
		if w.DurationMs != math.MaxInt32-w.Start {
			t.Errorf("window starting at %d: durationMs = %d, want %d",
				w.Start, w.DurationMs, math.MaxInt32-w.Start)
		}
	}
}

// Everything landing on one millisecond leaves collectScoreEvents as ONE tick
// carrying the sum — a kill and a death at the same instant are a net 0 there,
// not two ticks that a window could start between. Asserted on the event
// stream rather than on a window because the fold is currently invisible in
// the output: candidate starts are deduped and the two-pointer sum is
// order-independent within a millisecond, so topWindowsFor returns identical
// candidates for a folded and an unfolded stream (checked over 200k random
// duplicate-heavy cases). The fold is the precondition that keeps it so.
func TestCollectScoreEventsFoldsOneTickPerMillisecond(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(1000, "C", "A", "rl"),
		kill(1200, "A", "B", "rl"),
	}
	ev, err := collectScoreEvents(hwResult(frags, 60000), MetricNetFrags, nil, "raw")
	if err != nil {
		t.Fatal(err)
	}
	want := []scoreEvent{{t: 1000, v: 0}, {t: 1200, v: 1}}
	if !reflect.DeepEqual(ev["A"], want) {
		t.Errorf("A = %v, want %v", ev["A"], want)
	}
	// The fold allocates: compacting into the head of the input would rewrite
	// a slice the caller still owns, leaving stale duplicates in its tail.
	in := []scoreEvent{{t: 1, v: 1}, {t: 1, v: 2}, {t: 2, v: 5}}
	keep := append([]scoreEvent(nil), in...)
	if coalesce(in); !reflect.DeepEqual(in, keep) {
		t.Errorf("input = %v, want %v — coalesce compacted in place", in, keep)
	}
}

// The ranking is total and the ORDER of its keys is part of the contract:
// score, then the earlier window, then the player name. Five windows all
// scoring 1 — four of them at the same instant — pin both halves, since a
// name-before-start ranking would hoist AAA's later window to rank 1.
func TestTopWindowsTieBreakStartThenPlayer(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "D", "X", "rl"), kill(1000, "B", "X", "rl"),
		kill(1000, "C", "X", "rl"), kill(1000, "A", "X", "rl"),
		kill(5000, "AAA", "X", "rl"),
	}
	got := mustHW(t, hwResult(frags, 60000), TopWindowsOptions{WindowMs: 2000, Limit: -1})
	var order []string
	for _, w := range got.Windows {
		if w.Score != 1 {
			t.Fatalf("%s scored %d, want 1 — the fixture no longer ties", w.Player, w.Score)
		}
		order = append(order, w.Player)
	}
	if want := []string{"A", "B", "C", "D", "AAA"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}
