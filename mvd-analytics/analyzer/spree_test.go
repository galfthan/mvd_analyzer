package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func spreeRes(frags []result.FragEntry, players []result.PlayerStream) *Result {
	return &Result{
		Frags:   &result.FragResult{KillsMeasured: true, Frags: frags},
		Streams: &result.Streams{Players: players},
	}
}

// The state machine's core: a run of kills is latched by the killer's own
// death, and a run still alive at match end is latched too — otherwise the
// winning streak, the one anybody would want to read, is the one that gets
// lost (KTX latches there for the same reason, ktx/src/stats.c:1637).
func TestDeriveSpreesLatchesOnDeathAndAtMatchEnd(t *testing.T) {
	res := spreeRes(
		[]result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 2000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 3000, Killer: "a", Victim: "b", Weapon: "rl"},
			// a dies at 4000 — the run of 3 is latched here.
			{Time: 5000, Killer: "a", Victim: "b", Weapon: "lg"},
			{Time: 6000, Killer: "a", Victim: "b", Weapon: "lg"},
		},
		[]result.PlayerStream{
			{Name: "a", Deaths: []int32{4000}},
			{Name: "b"},
		},
	)
	got := deriveSprees(res)
	if got["a"].max != 3 {
		t.Errorf("a max spree = %d, want 3 (the pre-death run beats the live one)", got["a"].max)
	}

	// Flip the run lengths: now the LIVE run at match end is the longest, and
	// only the end-of-match latch can see it.
	res = spreeRes(
		[]result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 5000, Killer: "a", Victim: "b", Weapon: "lg"},
			{Time: 6000, Killer: "a", Victim: "b", Weapon: "lg"},
			{Time: 7000, Killer: "a", Victim: "b", Weapon: "lg"},
		},
		[]result.PlayerStream{
			{Name: "a", Deaths: []int32{4000}},
			{Name: "b"},
		},
	)
	if got := deriveSprees(res); got["a"].max != 3 {
		t.Errorf("a max spree = %d, want 3 — the live run at match end must latch", got["a"].max)
	}
}

// Team kills and suicides never increment: the streak is a re-cut of exactly
// the kills PlayerStatsScore.Kills counts, so a figure that counted more
// would be a second, disagreeing tally rather than a view of the first. This
// is also the one place the derivation deliberately parts from KTX, which
// bumps a player's own streak on their suicide wherever teamplay is off
// (ktx/src/client.c:4865).
func TestDeriveSpreesIgnoresTeamKillsAndSuicides(t *testing.T) {
	res := spreeRes(
		[]result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 1500, Killer: "a", Victim: "mate", Weapon: "rl", IsTeamKill: true},
			{Time: 2000, Killer: "a", Victim: "a", Weapon: "rl", IsSuicide: true},
		},
		[]result.PlayerStream{{Name: "a"}, {Name: "b"}},
	)
	got := deriveSprees(res)
	if got["a"].max != 1 {
		t.Errorf("a max spree = %d, want 1 — only the enemy kill counts", got["a"].max)
	}
	// The suicide is still a DEATH, so it ends the run: the frag log's victim
	// side is the only death record a player with no stream markers has.
	if got["a"].maxQuad != 0 {
		t.Errorf("a quad spree = %d, want 0", got["a"].maxQuad)
	}
}

// The quad streak counts only kills made while the quad is held, and resets
// on a fresh quad as well as on death — KTX latches spree_max_q inside the
// pickup handler (ktx/src/items.c:2180-2181), so two consecutive quad runs
// are never summed into one streak.
func TestDeriveSpreesQuadRunsDoNotMerge(t *testing.T) {
	res := spreeRes(
		[]result.FragEntry{
			{Time: 1100, Killer: "a", Victim: "b", Weapon: "rl"}, // quad run 1
			{Time: 1200, Killer: "a", Victim: "b", Weapon: "rl"}, // quad run 1
			{Time: 5000, Killer: "a", Victim: "b", Weapon: "rl"}, // no quad
			{Time: 9100, Killer: "a", Victim: "b", Weapon: "rl"}, // quad run 2
		},
		[]result.PlayerStream{
			{Name: "a", Quad: []result.Interval{{Start: 1000, End: 2000}, {Start: 9000, End: 10000}}},
			{Name: "b"},
		},
	)
	got := deriveSprees(res)
	if got["a"].max != 4 {
		t.Errorf("a max spree = %d, want 4 — no death, so one run", got["a"].max)
	}
	if got["a"].maxQuad != 2 {
		t.Errorf("a quad spree = %d, want 2 — the second quad opens a new run", got["a"].maxQuad)
	}
}

// Same-millisecond ordering. A kill lands before the killer's own death (the
// rocket that was already in flight counts, which is ClientObituary's order
// too), and a quad pickup lands before a kill at that instant so the kill
// belongs to the new quad run rather than closing the old one.
func TestDeriveSpreesSameInstantOrdering(t *testing.T) {
	res := spreeRes(
		[]result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 2000, Killer: "a", Victim: "b", Weapon: "rl"},
		},
		[]result.PlayerStream{
			{Name: "a", Deaths: []int32{2000}, Quad: []result.Interval{{Start: 2000, End: 3000}}},
			{Name: "b"},
		},
	)
	got := deriveSprees(res)
	if got["a"].max != 2 {
		t.Errorf("a max spree = %d, want 2 — a kill at the death instant counts", got["a"].max)
	}
	if got["a"].maxQuad != 1 {
		t.Errorf("a quad spree = %d, want 1 — the quad taken that instant covers the kill", got["a"].maxQuad)
	}
}

// A scoreboard-only player — one the frag log names but who produced no
// stream — still gets their streak reset by their own deaths, because the
// log's victim side is unioned with the protocol death markers rather than
// chosen against them.
func TestDeriveSpreesResetsWithoutAStream(t *testing.T) {
	res := &Result{Frags: &result.FragResult{KillsMeasured: true, Frags: []result.FragEntry{
		{Time: 1000, Killer: "ghost", Victim: "b", Weapon: "rl"},
		{Time: 2000, Killer: "b", Victim: "ghost", Weapon: "rl"},
		{Time: 3000, Killer: "ghost", Victim: "b", Weapon: "rl"},
	}}}
	got := deriveSprees(res)
	if got["ghost"].max != 1 {
		t.Errorf("ghost max spree = %d, want 1 — the frag log's victim side ends the run", got["ghost"].max)
	}
}

// No frag log, no streaks: the same evidence PlayerStatsScore.Kills rests on.
func TestDeriveSpreesNilWithoutFragLog(t *testing.T) {
	if got := deriveSprees(&Result{}); got != nil {
		t.Errorf("sprees without a frag log = %v, want nil", got)
	}
}

// The published fields ride the kill side's gate exactly: present with Kills,
// absent with it, and an observed 0 for a player who never killed.
func TestDeriveScorePublishesSprees(t *testing.T) {
	res := spreeRes(
		[]result.FragEntry{
			{Time: 1000, Killer: "a", Victim: "b", Weapon: "rl"},
			{Time: 2000, Killer: "a", Victim: "b", Weapon: "rl"},
		},
		[]result.PlayerStream{{Name: "a"}, {Name: "b"}},
	)
	sprees := deriveSprees(res)
	s := deriveScore(res, "a", nil, sprees)
	if s.MaxSpree == nil || *s.MaxSpree != 2 {
		t.Errorf("a maxSpree = %v, want 2", s.MaxSpree)
	}
	if s.MaxQuadSpree == nil || *s.MaxQuadSpree != 0 {
		t.Errorf("a maxQuadSpree = %v, want an observed 0", s.MaxQuadSpree)
	}
	if b := deriveScore(res, "b", nil, sprees); b.MaxSpree == nil || *b.MaxSpree != 0 {
		t.Errorf("b maxSpree = %v, want an observed 0 for a player who only died", b.MaxSpree)
	}

	// Unmeasurable kill attribution takes the streaks with it — 0 beside a
	// full scoreboard would read as a measurement.
	res.Frags.KillsMeasured = false
	if s := deriveScore(res, "a", nil, sprees); s.MaxSpree != nil || s.MaxQuadSpree != nil {
		t.Errorf("sprees = %v/%v with unmeasured kills, want ABSENT", s.MaxSpree, s.MaxQuadSpree)
	}
}

// A team row carries the best streak any member ran, never the sum — summing
// them would invent a run nobody made. Same max-over-members rule
// HoldStat.LongestMs follows.
func TestAggregateTeamRowsSpreeIsMaxNotSum(t *testing.T) {
	n := func(v int) *int { return &v }
	rows := []result.PlayerStatsRow{
		{Name: "a", Team: "red", Score: result.PlayerStatsScore{MaxSpree: n(4), MaxQuadSpree: n(2)}},
		{Name: "b", Team: "red", Score: result.PlayerStatsScore{MaxSpree: n(7), MaxQuadSpree: n(1)}},
	}
	teams := aggregateTeamRows(rows, 1000)
	if len(teams) != 1 {
		t.Fatalf("teams = %d, want 1", len(teams))
	}
	if got := teams[0].Score.MaxSpree; got == nil || *got != 7 {
		t.Errorf("team maxSpree = %v, want 7", got)
	}
	if got := teams[0].Score.MaxQuadSpree; got == nil || *got != 2 {
		t.Errorf("team maxQuadSpree = %v, want 2", got)
	}
	// The members' own rows must not have been mutated into the team's.
	if *rows[0].Score.MaxSpree != 4 {
		t.Errorf("member maxSpree = %d, want 4 — the aggregate must not write through", *rows[0].Score.MaxSpree)
	}
}
