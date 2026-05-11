package parser

import (
	"testing"

	"github.com/mvd-analyzer/qwdemo/mvd"
)

// Exercises the StatHealth transition paths that drive DeathEvent and
// SpawnEvent emission. The instant-respawn sequence (100 → -60 → 100)
// must produce exactly one DeathEvent followed by one SpawnEvent,
// interleaved with the StatUpdateEvents — anything less and the
// timeline analyzer's per-bucket D/Sp markers would miss the
// transition.
func TestUpdateStat_HealthTransitionsEmitDeathAndSpawn(t *testing.T) {
	p := NewParser(nil)

	var events []Event
	p.OnEvent(func(e Event) error {
		events = append(events, e)
		return nil
	})

	// Seed as if a prior stat update had set Health = 100.
	p.playerStats[0].Health = 100

	// Gib: 100 → -60 should emit StatUpdateEvent then DeathEvent.
	if err := p.updateStat(0, mvd.StatHealth, -60, 10.03); err != nil {
		t.Fatalf("gib update: %v", err)
	}
	// Instant respawn: -60 → 100 should emit StatUpdateEvent then SpawnEvent.
	if err := p.updateStat(0, mvd.StatHealth, 100, 10.04); err != nil {
		t.Fatalf("respawn update: %v", err)
	}

	if got := len(events); got != 4 {
		t.Fatalf("event count = %d, want 4 (stat, death, stat, spawn)", got)
	}

	if _, ok := events[0].(*StatUpdateEvent); !ok {
		t.Errorf("events[0] = %T, want *StatUpdateEvent", events[0])
	}
	d, ok := events[1].(*DeathEvent)
	if !ok {
		t.Fatalf("events[1] = %T, want *DeathEvent", events[1])
	}
	if d.PlayerNum != 0 || d.Time != 10.03 {
		t.Errorf("DeathEvent = %+v", d)
	}
	if _, ok := events[2].(*StatUpdateEvent); !ok {
		t.Errorf("events[2] = %T, want *StatUpdateEvent", events[2])
	}
	s, ok := events[3].(*SpawnEvent)
	if !ok {
		t.Fatalf("events[3] = %T, want *SpawnEvent", events[3])
	}
	if s.PlayerNum != 0 || s.Time != 10.04 {
		t.Errorf("SpawnEvent = %+v", s)
	}
}

// Edge transitions should not fire when health stays on the same side
// of zero (e.g., a plain damage event dropping 100 → 50) — only the
// StatUpdateEvent should be emitted.
func TestUpdateStat_HealthNoTransitionDoesNotEmitDeathOrSpawn(t *testing.T) {
	p := NewParser(nil)

	var events []Event
	p.OnEvent(func(e Event) error {
		events = append(events, e)
		return nil
	})

	p.playerStats[0].Health = 100

	if err := p.updateStat(0, mvd.StatHealth, 50, 1.0); err != nil {
		t.Fatalf("damage update: %v", err)
	}

	if got := len(events); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
	if _, ok := events[0].(*StatUpdateEvent); !ok {
		t.Errorf("events[0] = %T, want *StatUpdateEvent", events[0])
	}
}

// First-spawn from a fresh player (stats.Health starts at the zero
// value) should fire exactly one SpawnEvent when the first positive
// StatHealth arrives.
func TestUpdateStat_FirstHealthFiresSpawn(t *testing.T) {
	p := NewParser(nil)

	var spawns int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*SpawnEvent); ok {
			spawns++
		}
		return nil
	})

	if err := p.updateStat(0, mvd.StatHealth, 100, 0.5); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := p.updateStat(0, mvd.StatHealth, 100, 0.6); err != nil {
		t.Fatalf("second update: %v", err)
	}

	if spawns != 1 {
		t.Errorf("SpawnEvent count = %d, want 1", spawns)
	}
}
