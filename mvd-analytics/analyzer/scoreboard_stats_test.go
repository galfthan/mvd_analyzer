package analyzer

import (
	"io"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
	"github.com/mvd-analyzer/mvd-reader/mvd"
)

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
// /player-stats came to report `measured.frags: true` on /top-windows.
// The inputs are shaped the way the PIPELINE presents them, which is the
// whole point of this test: the match analyser emits scoreboard rows with
// Name/Team/Frags and NOTHING else (match.go:518-521), so Deaths is 0 on every
// row until this node's own fold fills it in. A verdict that read the
// scoreboard before that fold saw all-zero deaths and came out "measured" for
// every demo the pipeline has ever produced — while the hand-built fixture
// this test used to carry (Deaths: 92 preset on the row) passed happily. The
// death evidence that exists at this point is the protocol tally in ByPlayer.
func TestScoreboardStatsPost_PublishesKillAttributionVerdict(t *testing.T) {
	// An empty frag log on a demo where the protocol recorded deaths: every
	// obituary went unmatched.
	unmeasured := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a", Team: "red", Frags: 0}}},
		Frags: &FragResult{ByPlayer: map[string]*PlayerFrags{"a": {Deaths: 92}}},
	}
	scoreboardStatsPost(unmeasured, nil)
	if unmeasured.Frags.KillsMeasured {
		t.Error("killsMeasured is true on a demo whose frag log matched no obituary")
	}
	// ...and the fold still ran: the verdict must not cost the scoreboard its
	// deaths, which are measured and stay.
	if got := unmeasured.Match.Players[0].Deaths; got != 92 {
		t.Errorf("scoreboard deaths = %d, want the protocol's 92", got)
	}

	// The same shape with a log is measured...
	measured := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a", Team: "red"}}},
		Frags: &FragResult{
			Frags:    []FragEntry{{Killer: "a", Victim: "b", Weapon: "rl"}},
			ByPlayer: map[string]*PlayerFrags{"a": {Kills: 1, Deaths: 92}},
		},
	}
	scoreboardStatsPost(measured, nil)
	if !measured.Frags.KillsMeasured {
		t.Error("killsMeasured is false on a demo with a matched frag log")
	}

	// ...and so is a demo where nobody died: an empty log contradicts nothing.
	quiet := &Result{
		Match: &MatchResult{Players: []PlayerStat{{Name: "a"}}},
		Frags: &FragResult{ByPlayer: map[string]*PlayerFrags{"a": {}}},
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

// eventSource replays a fixed event list through the Source interface, so a
// test can drive the whole registry off a demo-shaped stream instead of a
// hand-assembled Result. Assertions on the pipeline's OUTPUT are the only ones
// that can catch a node reading an input that is not yet final.
type eventSource struct {
	evs []events.Event
	i   int
}

func (s *eventSource) Next() (events.Event, error) {
	if s.i >= len(s.evs) {
		return nil, io.EOF
	}
	e := s.evs[s.i]
	s.i++
	return e, nil
}

func (s *eventSource) Close() error { return nil }

// A demo whose obituaries were all unparseable — the class of demo
// killsMeasured exists for — must come out UNMEASURED end-to-end.
//
// The stream below is what such a demo looks like on the wire: a real match,
// two players, protocol deaths (svc_updatestat health <= 0 / DF_DEAD, which is
// where FragResult.ByPlayer[].Deaths comes from) and not one obituary print
// the parser recognises. The frag log is therefore empty while the deaths are
// solid, so `kills: 0` is not a measurement and every surface must say so.
//
// The node-level test above cannot make this assertion: it hands the node an
// input the pipeline never produces (a scoreboard whose Deaths column is
// already filled). Reading the real thing is how the verdict came to be
// vacuously true for every demo the pipeline has ever produced.
func TestPipelineUnmatchedObituariesAreUnmeasured(t *testing.T) {
	const (
		start = int32(10_000)
		end   = int32(70_000)
	)
	evs := []events.Event{
		&events.ServerDataEvent{TimeMs: 0, Data: &mvd.ServerData{
			ProtocolVersion: 28, GameDir: "qw",
			LevelName: "The Abandoned Base", MapFile: "maps/dm2.bsp",
		}},
		&events.UserInfoEvent{TimeMs: 0, Player: &mvd.PlayerInfo{
			Slot: 0, UserID: 11, Name: "alpha", Team: "red"}},
		&events.UserInfoEvent{TimeMs: 0, Player: &mvd.PlayerInfo{
			Slot: 1, UserID: 22, Name: "bravo", Team: "blue"}},
		&events.MatchStartEvent{TimeMs: start, Source: events.MatchStartSourcePrint},
	}
	// Both players move, score and die for a minute. Every death is a protocol
	// death event with no accompanying obituary line.
	for ms := start; ms <= end; ms += 1000 {
		for slot := 0; slot < 2; slot++ {
			evs = append(evs, &events.PlayerPositionEvent{
				PlayerNum: slot, TimeMs: ms,
				Origin: [3]float32{float32(100 * slot), float32(ms / 100), 24},
			})
		}
	}
	frags := []int{0, 0}
	for i, d := range []struct {
		slot int
		ms   int32
	}{
		{1, 20_000}, {0, 25_000}, {1, 33_000},
		{0, 41_000}, {1, 52_000}, {1, 63_000},
	} {
		killer := 1 - d.slot
		frags[killer]++
		evs = append(evs,
			&events.DeathEvent{PlayerNum: d.slot, TimeMs: d.ms},
			&events.FragUpdateEvent{PlayerNum: killer, Frags: frags[killer], TimeMs: d.ms},
			&events.SpawnEvent{PlayerNum: d.slot, TimeMs: d.ms + 100 + int32(i)},
		)
	}
	evs = append(evs,
		&events.PrintEvent{TimeMs: end, Level: 2, TargetPlayerNum: -1,
			Message: "The match is over\n"},
		&events.IntermissionEvent{TimeMs: end})

	res, err := NewDefaultRegistry().AnalyzeSource(&eventSource{evs: evs}, "unmatched-obituaries.mvd")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// The premise: an empty frag log beside real deaths. If either half stops
	// holding, the assertion below proves nothing.
	if res.Frags == nil || len(res.Frags.Frags) != 0 {
		t.Fatalf("premise broken: frag log is not empty (%+v)", res.Frags)
	}
	deaths := 0
	for _, pf := range res.Frags.ByPlayer {
		deaths += pf.Deaths
	}
	if deaths != 6 {
		t.Fatalf("premise broken: protocol deaths = %d, want 6", deaths)
	}
	if res.Match == nil || len(res.Match.Players) != 2 {
		t.Fatalf("premise broken: scoreboard = %+v, want two players", res.Match)
	}

	if res.Frags.KillsMeasured {
		t.Error("killsMeasured is true on a pipeline-produced demo whose obituaries " +
			"all went unmatched — every consumer of the stored verdict (/player-stats, " +
			"/frags, /top-windows, /lives) now publishes 0 kills as a measurement")
	}
	// The downstream consequence the verdict exists to prevent: a row reading
	// 0 kills beside a measured death count.
	if res.PlayerStats == nil || len(res.PlayerStats.Players) == 0 {
		t.Fatal("no player stats")
	}
	for _, p := range res.PlayerStats.Players {
		if p.Score.Kills != nil {
			t.Errorf("%s: kills served as %d over an empty frag log", p.Name, *p.Score.Kills)
		}
		if p.Score.Deaths == 0 {
			t.Errorf("%s: deaths dropped — they are measured and must survive", p.Name)
		}
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
