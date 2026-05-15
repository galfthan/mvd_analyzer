package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestEventsDefaultExcludesHealth(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10},
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Health: []result.ChangeI16{{T: 1, V: 100}, {T: 2, V: 50}},
					Spawns: []float64{0.5},
				},
			},
		},
	}
	v, err := Events(r, EventsFilter{})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for _, e := range v.Events {
		if e.Type == "health" {
			t.Fatalf("default Events should not include health, got %+v", e)
		}
	}
	// Spawn IS in the default set.
	gotSpawn := false
	for _, e := range v.Events {
		if e.Type == "spawn" {
			gotSpawn = true
		}
	}
	if !gotSpawn {
		t.Fatalf("expected spawn event in default Events output")
	}
}

func TestEventsHealthOptIn(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10},
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Health: []result.ChangeI16{{T: 1, V: 100}, {T: 2, V: 50}},
				},
			},
		},
	}
	v, err := Events(r, EventsFilter{Types: []string{"health"}})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(v.Events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(v.Events))
	}
	if v.Events[0].Type != "health" {
		t.Fatalf("Type = %s, want health", v.Events[0].Type)
	}
}

func TestEventsTimeOrdered(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10},
			Players: []result.PlayerStream{
				{
					Name:   "p1",
					Spawns: []float64{1.0, 5.0},
					Deaths: []float64{3.0, 7.0},
				},
			},
		},
	}
	v, _ := Events(r, EventsFilter{})
	last := -1.0
	for _, e := range v.Events {
		if e.T < last {
			t.Fatalf("events out of order: %v", v.Events)
		}
		last = e.T
	}
}
