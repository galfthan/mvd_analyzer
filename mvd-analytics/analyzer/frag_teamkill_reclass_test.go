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

	// One unambiguous enemy kill first, so the killer has a frag line for
	// the promotion to land on (OnEvent creates one only for real kills).
	_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "enemy eats shooter's pineapple\n", TimeMs: 500})
	_ = a.OnEvent(&events.PrintEvent{Level: 1, Message: "mate eats shooter's pineapple\n", TimeMs: 1000})
	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	pf := res.Frags.ByPlayer["shooter"]
	if pf == nil || pf.Kills != 2 {
		t.Fatalf("player line = %+v, want 2 kills after the promotion", pf)
	}
	if pf.ByWeapon["gl"] != 2 {
		t.Errorf("byWeapon[gl] = %d, want 2 — the promotion must restore the weapon too", pf.ByWeapon["gl"])
	}
	if n := res.Frags.ByWeapon["gl"]; n != 2 {
		t.Errorf("global byWeapon[gl] = %d, want 2", n)
	}
}
