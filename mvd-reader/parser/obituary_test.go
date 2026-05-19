package parser

import (
	"encoding/binary"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// FindObituaryVictim must pick the right victim for representative
// kill / suicide / teamkill obit lines.
func TestFindObituaryVictim(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		victim string
	}{
		{"rl kill", "sailorman rides multibear's rocket\n", "sailorman"},
		{"lg kill", "multibear accepts sailorman's shaft\n", "multibear"},
		{"sg kill", "nlk chewed on clox's boomstick\n", "nlk"},
		{"tk telefrag", "nlk was telefragged by his teammate\n", "nlk"},
		{"suicide rl", "sailorman discovers blast radius\n", "sailorman"},
		{"environmental fall", "ocoini cratered\n", "ocoini"},
		{"environmental lava", "Player visits the Volcano God\n", "Player"},
		{"chat looking like obit", "(sailorman): nice rocket\n", ""},
		{"empty", "", ""},

		// Infix-form: Satan's-power-deflect (KTX dtTELE2).
		{"pent deflect", "Satan's power deflects nlk's telefrag\n", "nlk"},

		// KTX dtTELE3 — pent vs pent double-666. Caught by the
		// existing " was telefragged by " marker; the killer suffix
		// "'s Satan's power" is handled by extractKillerName in
		// mvd-analytics/analyzer/obituary.go.
		{"pent vs pent", "nlk was telefragged by lakso's Satan's power\n", "nlk"},

		// KTX dtTELE4 — k_spawnicide random variants.
		{"spawnicide 1", "doberman couldn't resist the shiny spawn point\n", "doberman"},
		{"spawnicide 2", "doberman got too close to the baby factory\n", "doberman"},
		{"spawnicide 3", "doberman was fragged by poor life choices\n", "doberman"},

		// CRMod variants — confirmed by user against CRMod source.
		{"crmod sg", "Player was disembowled by Other's shotgun\n", "Player"},
		{"crmod ssg", "Player eats 2 scoops of Other's lead shot\n", "Player"},
		{"crmod rl shish", "Player is shish-kebabed by Other's rocket\n", "Player"},
		{"crmod blown chunks rl", "Player was blown to chunks by Other's rocket\n", "Player"},
		{"crmod blown chunks gl", "Player was blown to chunks by Other's grenade\n", "Player"},
		{"crmod gl intimate", "Player gets intimate with Other's grenade\n", "Player"},
		{"crmod lg fuzzy", "Player gets a warm fuzzy feeling from Other\n", "Player"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := FindObituaryVictim(tc.msg)
			if v != tc.victim {
				t.Errorf("FindObituaryVictim(%q) = %q, want %q", tc.msg, v, tc.victim)
			}
		})
	}
}

// "X was telefragged by his teammate" must match the teammate variant
// before the plain "X was telefragged by " kill pattern fires —
// otherwise the killer extraction below would treat "his teammate" as
// the killer's name.
func TestFindObituaryVictim_TeammatePrefersTKPattern(t *testing.T) {
	_, pat := FindObituaryVictim("nlk was telefragged by his teammate\n")
	if pat == nil {
		t.Fatalf("expected a matched pattern for teammate telefrag")
	}
	if !pat.TeamKill {
		t.Errorf("expected TeamKill=true, got pattern %+v", pat)
	}
}

// Obit-derived DeathEvent must NOT fire before the parser observes a
// match-start phrase — warmup obits (and the very common case of
// match-start telefrag prints arriving before "The match has begun!"
// in the same wire instant) would otherwise pre-seed the dedup state
// and starve the stat-based detector of its post-start emission.
func TestObituaryDeath_GatedOnMatchStart(t *testing.T) {
	p := NewParser(nil)
	p.players[3] = &mvd.PlayerInfo{Slot: 3, Name: "sailorman"}

	var deaths int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*DeathEvent); ok {
			deaths++
		}
		return nil
	})

	// Pre-match obit — must not fire.
	if err := p.tryEmitObituaryDeath("sailorman rides multibear's rocket\n", 1.0, 1000); err != nil {
		t.Fatalf("pre-match obit: %v", err)
	}
	if deaths != 0 {
		t.Fatalf("pre-match deaths = %d, want 0", deaths)
	}

	// Match-start phrase flips the gate.
	p.updateMatchStartedFromPrint("The match has begun!\n")
	if !p.matchStarted {
		t.Fatalf("matchStarted gate did not flip on start phrase")
	}

	// Same obit, post-start — must fire exactly once.
	if err := p.tryEmitObituaryDeath("sailorman rides multibear's rocket\n", 2.0, 2000); err != nil {
		t.Fatalf("post-match obit: %v", err)
	}
	if deaths != 1 {
		t.Errorf("post-match deaths = %d, want 1", deaths)
	}
}

// Obit-emitted DeathEvent bypasses the parser dedup so the pent-
// deflection corner case (KTX dtTELE2, where DF_DEAD never visibly
// leaves the prior dead interval) is still recorded. A second
// consecutive obit for the same player must fire a second
// DeathEvent — matching KTX's authoritative `logfrag(targ, targ)`
// bookkeeping which increments deathcount per obit.
func TestObituaryDeath_ForceEmitsEvenWhenStateAlreadyDead(t *testing.T) {
	p := NewParser(nil)
	p.players[2] = &mvd.PlayerInfo{Slot: 2, Name: "nlk"}
	p.matchStarted = true
	// Pre-seed: parser already thinks nlk is dead.
	p.playerDeadKnown[2] = true
	p.playerDead[2] = true

	var deaths int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*DeathEvent); ok {
			deaths++
		}
		return nil
	})

	// Two consecutive deflections while nlk's wire state stays dead.
	if err := p.tryEmitObituaryDeath("Satan's power deflects nlk's telefrag\n", 631.4, 631419); err != nil {
		t.Fatalf("first deflect: %v", err)
	}
	if err := p.tryEmitObituaryDeath("Satan's power deflects nlk's telefrag\n", 633.5, 633548); err != nil {
		t.Fatalf("second deflect: %v", err)
	}
	if deaths != 2 {
		t.Errorf("deaths = %d, want 2 (both deflections must fire even though state was already dead)", deaths)
	}
}

// End-to-end through parsePrint: a mid-match obit feeds DeathEvent via
// maybeEmitDeath, and the next svc_playerinfo with DF_DEAD clear fires
// SpawnEvent through the existing transition detector.
func TestParsePrint_ObituaryFiresDeathAndNextPlayerInfoFiresSpawn(t *testing.T) {
	p := NewParser(nil)
	p.players[5] = &mvd.PlayerInfo{Slot: 5, Name: "sailorman"}
	p.matchStarted = true
	// Pre-seed: parser thinks sailorman is alive (default state).
	p.playerDeadKnown[5] = true
	p.playerDead[5] = false
	p.playerSeenInfo[5] = true

	var events []Event
	p.OnEvent(func(e Event) error {
		events = append(events, e)
		return nil
	})

	// svc_print payload: [level byte][message string\0].
	payload := []byte{1}
	msg := "sailorman rides multibear's rocket\n"
	payload = append(payload, []byte(msg)...)
	payload = append(payload, 0)
	r := mvd.NewBufferReader(payload)
	if err := p.parsePrint(r, 5.0, 5000, -1); err != nil {
		t.Fatalf("parsePrint: %v", err)
	}

	// Next svc_playerinfo (DF_DEAD clear) — should fire SpawnEvent.
	pi := []byte{5} // player slot
	pi = binary.LittleEndian.AppendUint16(pi, 0)
	pi = append(pi, 0) // frame
	if err := p.parsePlayerInfo(mvd.NewBufferReader(pi), 5.05, 5050, false); err != nil {
		t.Fatalf("parsePlayerInfo: %v", err)
	}

	var deaths, spawns int
	for _, e := range events {
		switch e.(type) {
		case *DeathEvent:
			deaths++
		case *SpawnEvent:
			spawns++
		}
	}
	if deaths != 1 || spawns != 1 {
		t.Errorf("deaths=%d spawns=%d, want 1/1", deaths, spawns)
	}
}
