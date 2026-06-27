package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
	"github.com/mvd-analyzer/mvd-reader/mvd"
)

func newTestShotsAnalyzer() (*ShotsAnalyzer, *Context) {
	a := NewShotsAnalyzer()
	ctx := &Context{}
	_ = a.Init(ctx)
	a.timing.Started = true // skip match-boundary detection
	a.core = &CoreOutputs{Slots: map[int]SlotInfo{
		3: {Name: "shooter", Team: "red"},
		0: {Name: "victimA", Team: "blue"},
		1: {Name: "victimB", Team: "blue"},
	}}
	return a, ctx
}

// weaponSound builds a CHAN_WEAPON SoundEvent for entity ent (slot ent-1).
func weaponSound(ent int, name string, tMs int32) *events.SoundEvent {
	return &events.SoundEvent{
		Ent: ent, Channel: chanWeapon, Name: name,
		Time: float64(tMs) / 1000, TimeMs: tMs,
	}
}

// TestShots_SoundMappingAndProjectileDisambiguation verifies each fire wav
// maps to the right weapon — in particular that the historically-mismatched
// Quake filenames resolve correctly (sgun1=rl, rocket1i=ng) — and that
// non-fire sounds and non-weapon channels are ignored.
func TestShots_SoundMappingAndProjectileDisambiguation(t *testing.T) {
	a, _ := newTestShotsAnalyzer()
	// ent 4 == slot 3 == "shooter".
	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 100))    // rl
	_ = a.OnEvent(weaponSound(4, "weapons/rocket1i.wav", 200)) // ng
	_ = a.OnEvent(weaponSound(4, "weapons/grenade.wav", 300))  // gl
	_ = a.OnEvent(weaponSound(4, "weapons/guncock.wav", 400))  // sg
	_ = a.OnEvent(weaponSound(4, "weapons/shotgn2.wav", 500))  // ssg
	_ = a.OnEvent(weaponSound(4, "weapons/spike2.wav", 600))   // sng
	// Non-fire sounds and non-weapon channel: ignored.
	_ = a.OnEvent(weaponSound(4, "weapons/bounce.wav", 700)) // grenade bounce
	_ = a.OnEvent(weaponSound(4, "weapons/lhit.wav", 800))   // LG hit
	_ = a.OnEvent(&events.SoundEvent{Ent: 4, Channel: 2, Name: "weapons/sgun1.wav", TimeMs: 900})

	r := &Result{}
	_ = a.Finalize(r)
	if r.Shots == nil {
		t.Fatal("no ShotsResult")
	}
	got := map[string]string{} // weapon -> player, in time order check below
	var weapons []string
	for _, s := range r.Shots.Shots {
		got[s.Weapon] = s.Player
		weapons = append(weapons, s.Weapon)
		if s.Source != "sound" {
			t.Errorf("shot %+v: source = %q, want sound", s, s.Source)
		}
		if s.Player != "shooter" {
			t.Errorf("shot %+v: player = %q, want shooter", s, s.Player)
		}
	}
	want := []string{"rl", "ng", "gl", "sg", "ssg", "sng"}
	if len(weapons) != len(want) {
		t.Fatalf("weapons = %v, want %v", weapons, want)
	}
	for i, w := range want {
		if weapons[i] != w {
			t.Errorf("shot %d weapon = %q, want %q", i, weapons[i], w)
		}
	}
}

// TestShots_HitscanLinking links an SG fire to the two same-frame damage
// events it produced, while a projectile (RL) fire stays unlinked even with
// same-frame RL damage present.
func TestShots_HitscanLinking(t *testing.T) {
	a, _ := newTestShotsAnalyzer()

	// SG fire by slot 3 at 1000ms; two pellets-worth of damage to two victims.
	_ = a.OnEvent(weaponSound(4, "weapons/guncock.wav", 1000))
	_ = a.OnEvent(&events.DamageEvent{Attacker: 3, Victim: 0, DeathType: mvd.DtSG, Damage: 20, Time: 1.0})
	_ = a.OnEvent(&events.DamageEvent{Attacker: 3, Victim: 1, DeathType: mvd.DtSG, Damage: 12, Time: 1.012})

	// RL fire by slot 3 at 2000ms with same-frame RL damage — must NOT link
	// (projectile, handled by entity tracking, not same-frame).
	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 2000))
	_ = a.OnEvent(&events.DamageEvent{Attacker: 3, Victim: 0, DeathType: mvd.DtRL, Damage: 80, Time: 2.0})

	r := &Result{}
	_ = a.Finalize(r)

	var sg, rl *Shot
	for i := range r.Shots.Shots {
		switch r.Shots.Shots[i].Weapon {
		case "sg":
			sg = &r.Shots.Shots[i]
		case "rl":
			rl = &r.Shots.Shots[i]
		}
	}
	if sg == nil || !sg.Hit || len(sg.Victims) != 2 {
		t.Fatalf("sg shot = %+v, want Hit with 2 victims", sg)
	}
	if sg.Victims[0] != "victimA" || sg.Victims[1] != "victimB" {
		t.Errorf("sg victims = %v, want [victimA victimB]", sg.Victims)
	}
	if rl == nil || rl.Hit {
		t.Errorf("rl shot = %+v, want unlinked (projectile)", rl)
	}

	// Aggregate accuracy: 1 connecting SG shot of 1 SG shot = 1.0.
	var sgAcc float64
	var found bool
	for _, p := range r.Shots.ByPlayer {
		if p.Player != "shooter" {
			continue
		}
		for _, w := range p.ByWeapon {
			if w.Weapon == "sg" {
				sgAcc = w.Accuracy
				found = true
			}
		}
	}
	if !found || sgAcc != 1.0 {
		t.Errorf("sg accuracy = %v (found=%v), want 1.0", sgAcc, found)
	}
}

// projSpawn / projDespawn build the entity-tracking events for one flight.
func projSpawn(ent int, kind string, origin [3]float32, tMs int32) *events.ProjectileSpawnEvent {
	return &events.ProjectileSpawnEvent{EntNum: ent, Kind: kind, Origin: origin, Time: float64(tMs) / 1000, TimeMs: tMs}
}
func projDespawn(ent int, kind string, tMs int32) *events.ProjectileDespawnEvent {
	return &events.ProjectileDespawnEvent{EntNum: ent, Kind: kind, Time: float64(tMs) / 1000, TimeMs: tMs}
}
func rlDamage(attacker, victim int, tMs int32) *events.DamageEvent {
	return &events.DamageEvent{Attacker: attacker, Victim: victim, DeathType: mvd.DtRL, Damage: 80, Time: float64(tMs) / 1000}
}

// TestShots_ProjectileBracketDisambiguation is the core Phase-2 case: one
// player fires two rockets; the SECOND rocket impacts BEFORE the first
// (it hit a near wall while the first flew on). A naive "next RL damage
// after the shot" link would cross the wires. The entity [spawn,despawn]
// bracket pins each shot to the impact its own rocket caused.
func TestShots_ProjectileBracketDisambiguation(t *testing.T) {
	a, _ := newTestShotsAnalyzer()

	// Two RL fires by slot 3.
	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 1000)) // shot A
	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 1300)) // shot B

	// Rocket A (ent 50) flies long: spawn 1000, despawn 1500.
	// Rocket B (ent 51) hits a near wall: spawn 1300, despawn 1400.
	_ = a.OnEvent(projSpawn(50, "rl", [3]float32{0, 0, 0}, 1000))
	_ = a.OnEvent(projSpawn(51, "rl", [3]float32{0, 0, 0}, 1300))
	_ = a.OnEvent(projDespawn(51, "rl", 1400)) // B impacts first
	_ = a.OnEvent(rlDamage(3, 0, 1400))        // B hits victimA
	_ = a.OnEvent(projDespawn(50, "rl", 1500)) // A impacts later
	_ = a.OnEvent(rlDamage(3, 1, 1500))        // A hits victimB

	r := &Result{}
	_ = a.Finalize(r)

	byTime := map[int32]*Shot{}
	for i := range r.Shots.Shots {
		byTime[r.Shots.Shots[i].Time] = &r.Shots.Shots[i]
	}
	a0, b0 := byTime[1000], byTime[1300]
	if a0 == nil || b0 == nil {
		t.Fatalf("missing shots: %+v", r.Shots.Shots)
	}
	// Shot A (fired first, impacted last at 1500) hit victimB.
	if !a0.Hit || len(a0.Victims) != 1 || a0.Victims[0] != "victimB" {
		t.Errorf("shot@1000 = %+v, want hit victimB", a0)
	}
	// Shot B (fired second, impacted first at 1400) hit victimA.
	if !b0.Hit || len(b0.Victims) != 1 || b0.Victims[0] != "victimA" {
		t.Errorf("shot@1300 = %+v, want hit victimA", b0)
	}
}

// TestShots_ProjectileMiss leaves a rocket whose flight ends with no damage
// unlinked (a miss), and reports rl accuracy from the connecting fraction.
func TestShots_ProjectileMiss(t *testing.T) {
	a, _ := newTestShotsAnalyzer()
	a.hadDmg = true // a damage stream exists (so accuracy is reported)

	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 1000)) // hits
	_ = a.OnEvent(projSpawn(50, "rl", [3]float32{0, 0, 0}, 1000))
	_ = a.OnEvent(projDespawn(50, "rl", 1500))
	_ = a.OnEvent(rlDamage(3, 0, 1500))

	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 3000)) // misses
	_ = a.OnEvent(projSpawn(51, "rl", [3]float32{0, 0, 0}, 3000))
	_ = a.OnEvent(projDespawn(51, "rl", 3600)) // no damage -> miss

	r := &Result{}
	_ = a.Finalize(r)

	hits := 0
	for _, s := range r.Shots.Shots {
		if s.Hit {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("rl hits = %d, want 1 (one of two rockets connected)", hits)
	}
	for _, p := range r.Shots.ByPlayer {
		for _, w := range p.ByWeapon {
			if w.Weapon == "rl" && w.Accuracy != 0.5 {
				t.Errorf("rl accuracy = %v, want 0.5", w.Accuracy)
			}
		}
	}
}

// TestShots_LGAmmoDelta counts LG fire from cell decrements while rejecting
// the spawn baseline, ammo pickups, and the death/discharge dump.
func TestShots_LGAmmoDelta(t *testing.T) {
	a, _ := newTestShotsAnalyzer()
	cell := func(slot, v int, tMs int32) *events.StatUpdateEvent {
		return &events.StatUpdateEvent{PlayerNum: slot, StatIndex: events.StatCells, Value: v, Time: float64(tMs) / 1000}
	}

	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 3})
	_ = a.OnEvent(cell(3, 50, 100)) // baseline after spawn — no shots
	_ = a.OnEvent(cell(3, 48, 200)) // -2 cells -> 2 LG shots
	_ = a.OnEvent(cell(3, 47, 300)) // -1 cell  -> 1 LG shot
	_ = a.OnEvent(cell(3, 80, 400)) // pickup (+33) -> no shots
	_ = a.OnEvent(cell(3, 79, 500)) // -1 cell  -> 1 LG shot
	_ = a.OnEvent(cell(3, 0, 600))  // -79 drop (death/discharge) -> no shots

	r := &Result{}
	_ = a.Finalize(r)

	lg := 0
	for _, s := range r.Shots.Shots {
		if s.Weapon != "lg" {
			t.Fatalf("unexpected non-lg shot %+v", s)
		}
		if s.Source != "ammo" {
			t.Errorf("lg shot source = %q, want ammo", s.Source)
		}
		lg++
	}
	if lg != 4 {
		t.Errorf("lg shots = %d, want 4 (2+1+1)", lg)
	}
}

// TestShots_WarmupGating keeps warmup fires in the stream but excludes them
// from the match-time ByPlayer aggregate.
func TestShots_WarmupGating(t *testing.T) {
	a, _ := newTestShotsAnalyzer()
	a.timing.Started = false // start in warmup

	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 100)) // warmup rl
	_ = a.OnEvent(&events.PrintEvent{Message: "fight!", Time: 0.2})
	_ = a.OnEvent(weaponSound(4, "weapons/sgun1.wav", 300)) // match rl

	r := &Result{}
	_ = a.Finalize(r)

	if len(r.Shots.Shots) != 2 {
		t.Fatalf("stream shots = %d, want 2 (warmup + match)", len(r.Shots.Shots))
	}
	total := 0
	for _, p := range r.Shots.ByPlayer {
		total += p.Total
	}
	if total != 1 {
		t.Errorf("aggregate total = %d, want 1 (match-time only)", total)
	}
}

// TestShots_Reconciliation converts detected discrete shots into KTX's
// attack unit (pellets for SG) for the cross-check.
func TestShots_Reconciliation(t *testing.T) {
	a, _ := newTestShotsAnalyzer()
	a.core.DemoInfo = &DemoInfoResult{Players: []DemoInfoPlayer{
		{Name: "shooter", Team: "red", Weapons: map[string]*DemoInfoWeapon{
			"sg": {Acc: &DemoInfoAcc{Attacks: 18, Hits: 6}},
		}},
	}}

	// 3 SG trigger pulls -> KTX attacks = 3 * 6 pellets = 18.
	for i := 0; i < 3; i++ {
		_ = a.OnEvent(weaponSound(4, "weapons/guncock.wav", int32(1000+i*600)))
	}

	r := &Result{}
	_ = a.Finalize(r)
	rec := r.Shots.Reconciliation
	if rec == nil {
		t.Fatal("no reconciliation")
	}
	rows := rec.ByPlayer["shooter"]
	var sg *ShotsDelta
	for i := range rows {
		if rows[i].Weapon == "sg" {
			sg = &rows[i]
		}
	}
	if sg == nil {
		t.Fatal("no sg reconciliation row")
	}
	if sg.StreamShots != 3 || sg.StreamAttacks != 18 || sg.KtxAttacks != 18 {
		t.Errorf("sg delta = %+v, want streamShots=3 streamAttacks=18 ktxAttacks=18", *sg)
	}
}
