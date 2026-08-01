package analyzer

import "testing"

// scoreboardStatsPost copies the frag-log-corrected kills/deaths onto the
// match scoreboard (joining on the final display name) and counts suicides
// from the IsSuicide frag entries per victim. It must leave a player with no
// data at 0/0/0 and must not panic on missing sections.
func TestScoreboardStatsPost_CopiesCorrectedKillsDeaths(t *testing.T) {
	res := &Result{
		Match: &MatchResult{Players: []PlayerStat{
			{Name: "speedball", Frags: 58},
			{Name: "mj", Frags: 6},
			{Name: "latecomer", Frags: 1}, // no data → stays 0/0/0
		}},
		Frags: &FragResult{
			ByPlayer: map[string]*PlayerFrags{
				"speedball": {Kills: 59, Deaths: 7},
				"mj":        {Kills: 6, Deaths: 59},
			},
			// Two self-deaths for speedball (a fall + an rl), one for mj; the
			// non-suicide entry must not be counted.
			Frags: []FragEntry{
				{Killer: "speedball", Victim: "speedball", Weapon: "fall", IsSuicide: true},
				{Killer: "speedball", Victim: "speedball", Weapon: "rl", IsSuicide: true},
				{Killer: "mj", Victim: "mj", Weapon: "lava", IsSuicide: true},
				{Killer: "speedball", Victim: "mj", Weapon: "rl"}, // real kill, not a suicide
			},
		},
	}

	scoreboardStatsPost(res, nil)

	// {kills, deaths, suicides}
	want := map[string][3]int{
		"speedball": {59, 7, 2},
		"mj":        {6, 59, 1},
		"latecomer": {0, 0, 0},
	}
	for _, p := range res.Match.Players {
		w := want[p.Name]
		if p.Kills != w[0] || p.Deaths != w[1] || p.Suicides != w[2] {
			t.Errorf("%s: got kills=%d deaths=%d suicides=%d, want %d/%d/%d",
				p.Name, p.Kills, p.Deaths, p.Suicides, w[0], w[1], w[2])
		}
	}
}

// The node also PUBLISHES the demo-global kill-attribution verdict on the frag
// artifact, which is the whole point of storing it: view (which cannot import
// this package) and player_stats read one answer instead of each re-deriving
// the rule — and the second reader is how a demo judged unmeasurable by
// /player-stats came to report `measured.frags: true` on /hot-windows.
func TestScoreboardStatsPost_PublishesKillAttributionVerdict(t *testing.T) {
	// An empty frag log beside a scoreboard that records deaths: every
	// obituary went unmatched.
	unmeasured := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a", Deaths: 92}}},
		Frags: &FragResult{ByPlayer: map[string]*PlayerFrags{}},
	}
	scoreboardStatsPost(unmeasured, nil)
	if unmeasured.Frags.KillsMeasured {
		t.Error("killsMeasured is true on a demo whose frag log matched no obituary")
	}

	// The same shape with a log is measured...
	measured := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a", Deaths: 92}}},
		Frags: &FragResult{Frags: []FragEntry{{Killer: "a", Victim: "b", Weapon: "rl"}}},
	}
	scoreboardStatsPost(measured, nil)
	if !measured.Frags.KillsMeasured {
		t.Error("killsMeasured is false on a demo with a matched frag log")
	}

	// ...and so is a demo where nobody died: an empty log contradicts nothing.
	quiet := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a"}}},
		Frags: &FragResult{},
	}
	scoreboardStatsPost(quiet, nil)
	if !quiet.Frags.KillsMeasured {
		t.Error("killsMeasured is false on a demo where nobody died — the zeros there are honest")
	}

	// The verdict is published even without a scoreboard, where the node's
	// other work is skipped entirely.
	noBoard := &Result{Frags: &FragResult{}}
	scoreboardStatsPost(noBoard, nil)
	if !noBoard.Frags.KillsMeasured {
		t.Error("killsMeasured left unset on a demo with no scoreboard; the early return skipped it")
	}
}

func TestScoreboardStatsPost_NilSafe(t *testing.T) {
	// None of these should panic.
	scoreboardStatsPost(&Result{}, nil)
	scoreboardStatsPost(&Result{Match: &MatchResult{}}, nil)
	scoreboardStatsPost(&Result{Frags: &FragResult{}}, nil)
	scoreboardStatsPost(&Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "x", Frags: 1}}},
		Frags: &FragResult{},
	}, nil)
}
