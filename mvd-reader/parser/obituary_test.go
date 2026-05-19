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
