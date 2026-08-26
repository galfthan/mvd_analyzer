package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// R2: Finalize re-classifies teamkills against the demoinfo team table
// (OnEvent's isTeamKill compared obituary display names against userinfo
// that may have carried auth names) and adjusts the killer's Kills. It
// never adjusted ByWeapon, so sum(byWeapon) drifted above kills — and
// playerStats publishes both, asserting they are on the same footing.
func TestFragFinalizeTeamkillReclassificationMovesByWeapon(t *testing.T) {
	// mate and nemesis are both "red" per demoinfo; the obituary names
	// them, so OnEvent scored the frag as a normal kill and Finalize must
	// take it back.
	build := func(sameTeam bool) *Result {
		a := NewFragAnalyzer()
		ctx := &Context{}
		ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "shooter", Team: "red"}
		ctx.Players[2] = &events.PlayerInfo{Slot: 2, Name: "mate", Team: "blue"}
		_ = a.Init(ctx)
		a.timing.Started = true

		victimTeam := "blue"
		if sameTeam {
			victimTeam = "red"
		}
		di := &result.DemoInfoResult{Players: []result.DemoInfoPlayer{
			{Name: "shooter", Team: "red"},
			{Name: "mate", Team: victimTeam},
		}}
		a.UseCoreOutputs(&CoreOutputs{DemoInfo: di, Names: NewNameTable(di)})

		_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "mate eats shooter's pineapple\n", TimeMs: 1000})
		var res Result
		if err := a.Finalize(&res); err != nil {
			t.Fatal(err)
		}
		return &res
	}

	t.Run("kill reclassified as teamkill", func(t *testing.T) {
		res := build(true)
		pf := res.Frags.ByPlayer["shooter"]
		if pf == nil {
			t.Fatal("no frag line for shooter")
		}
		if pf.Kills != 0 {
			t.Errorf("kills = %d, want 0 — the frag is a teamkill", pf.Kills)
		}
		if n, ok := pf.ByWeapon["gl"]; ok {
			t.Errorf("byWeapon[gl] = %d, want the key GONE — kills and byWeapon are on the same footing", n)
		}
		if n := res.Frags.ByWeapon["gl"]; n != 0 {
			t.Errorf("global byWeapon[gl] = %d, want 0", n)
		}
	})

	t.Run("genuine enemy kill is untouched", func(t *testing.T) {
		res := build(false)
		pf := res.Frags.ByPlayer["shooter"]
		if pf == nil || pf.Kills != 1 || pf.ByWeapon["gl"] != 1 {
			t.Fatalf("player line = %+v, want 1 kill / 1 rl", pf)
		}
		if n := res.Frags.ByWeapon["gl"]; n != 1 {
			t.Errorf("global byWeapon[gl] = %d, want 1", n)
		}
	})
}

// The reverse direction: a frag OnEvent scored as a teamkill and Finalize
// promotes back to a kill must gain a byWeapon entry, not just a kill.
func TestFragFinalizeTeamkillDemotionRestoresByWeapon(t *testing.T) {
	a := NewFragAnalyzer()
	ctx := &Context{}
	// Wire-side userinfo says both are red, so OnEvent calls it a teamkill.
	ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "shooter", Team: "red"}
	ctx.Players[2] = &events.PlayerInfo{Slot: 2, Name: "mate", Team: "red"}
	_ = a.Init(ctx)
	a.timing.Started = true

	ctx.Players[3] = &events.PlayerInfo{Slot: 3, Name: "enemy", Team: "blue"}
	// ...but the demoinfo block puts shooter and mate on different teams.
	di := &result.DemoInfoResult{Players: []result.DemoInfoPlayer{
		{Name: "shooter", Team: "red"},
		{Name: "mate", Team: "blue"},
		{Name: "enemy", Team: "blue"},
	}}
	a.UseCoreOutputs(&CoreOutputs{DemoInfo: di, Names: NewNameTable(di)})

	// The killer's ONLY frag was misread as a teamkill at OnEvent, so no
	// frag line exists when the promotion runs — Finalize must create it
	// rather than silently dropping the adjustment (external-review
	// finding: the old ok-guard left the global byWeapon incremented with
	// no per-player counterpart).
	_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "mate eats shooter's pineapple\n", TimeMs: 1000})
	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	pf := res.Frags.ByPlayer["shooter"]
	if pf == nil || pf.Kills != 1 {
		t.Fatalf("player line = %+v, want a line with 1 kill created by the promotion", pf)
	}
	if pf.ByWeapon["gl"] != 1 {
		t.Errorf("byWeapon[gl] = %d, want 1 — the promotion must restore the weapon too", pf.ByWeapon["gl"])
	}
	if n := res.Frags.ByWeapon["gl"]; n != 1 {
		t.Errorf("global byWeapon[gl] = %d, want 1", n)
	}
}

// A kill demoted to a teamkill must move ALL THREE counters — kills,
// byWeapon and teamKills — or the identity frags == kills − suicides −
// teamKills breaks by one on the reclass path (external-review finding:
// only the first two moved).
func TestFragFinalizeTeamkillDemotionMovesTeamKills(t *testing.T) {
	a := NewFragAnalyzer()
	ctx := &Context{}
	// Wire-side userinfo puts them on different teams → OnEvent scores a
	// kill...
	ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "shooter", Team: "red"}
	ctx.Players[2] = &events.PlayerInfo{Slot: 2, Name: "mate", Team: "blue"}
	_ = a.Init(ctx)
	a.timing.Started = true

	// ...but the demoinfo block says they were teammates all along.
	di := &result.DemoInfoResult{Players: []result.DemoInfoPlayer{
		{Name: "shooter", Team: "red"},
		{Name: "mate", Team: "red"},
	}}
	a.UseCoreOutputs(&CoreOutputs{DemoInfo: di, Names: NewNameTable(di)})

	_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "mate eats shooter's pineapple\n", TimeMs: 1000})
	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	pf := res.Frags.ByPlayer["shooter"]
	if pf == nil {
		t.Fatal("no frag line for shooter")
	}
	if pf.Kills != 0 || pf.TeamKills != 1 {
		t.Errorf("kills=%d teamKills=%d, want 0/1 — the demotion must move both", pf.Kills, pf.TeamKills)
	}
	if len(pf.ByWeapon) != 0 {
		t.Errorf("byWeapon = %v, want empty after the demotion", pf.ByWeapon)
	}
	if n := res.Frags.ByWeapon["gl"]; n != 0 {
		t.Errorf("global byWeapon[gl] = %d, want 0", n)
	}
}

// In an individual mode nothing is a team kill. The userinfo `team` tag in
// FFA is clan decoration — on ffa-demos/ffa_1[dm2]260116-2057.mvd three
// players wear `red` and kill each other — and KTX's teamkill obituaries are
// unreachable outside team / CTF / coop anyway (client.c:5342-5343). Before
// v75 the tag comparison alone flagged 34 of that demo's 211 kills.
func TestFragFinalizeIndividualModeHasNoTeamkills(t *testing.T) {
	build := func(individual bool) *Result {
		a := NewFragAnalyzer()
		ctx := &Context{}
		ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "toast", Team: "red"}
		ctx.Players[2] = &events.PlayerInfo{Slot: 2, Name: "Amr", Team: "red"}
		_ = a.Init(ctx)
		a.timing.Started = true

		co := &CoreOutputs{}
		if individual {
			gm := resolveGameMode(nil, nil, nil, map[string]string{"mode": "ffa"}, nil, nil)
			co.GameMode = &gm
		} else {
			gm := resolveGameMode(nil, nil, nil, map[string]string{"mode": "4on4", "teamplay": "2"}, nil, nil)
			co.GameMode = &gm
		}
		a.UseCoreOutputs(co)

		_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "Amr eats toast's pineapple\n", TimeMs: 1000})
		var res Result
		if err := a.Finalize(&res); err != nil {
			t.Fatal(err)
		}
		return &res
	}

	ffa := build(true)
	if len(ffa.Frags.Frags) != 1 || ffa.Frags.Frags[0].IsTeamKill {
		t.Errorf("FFA frag = %+v, want a plain kill", ffa.Frags.Frags)
	}
	if pf := ffa.Frags.ByPlayer["toast"]; pf == nil || pf.Kills != 1 || pf.TeamKills != 0 {
		t.Errorf("FFA toast = %+v, want kills 1 / teamkills 0", pf)
	}

	// The same wire on a team game keeps the tag comparison.
	team := build(false)
	if len(team.Frags.Frags) != 1 || !team.Frags.Frags[0].IsTeamKill {
		t.Errorf("4on4 frag = %+v, want a team kill", team.Frags.Frags)
	}
}
