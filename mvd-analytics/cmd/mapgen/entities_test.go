package main

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapents"
)

func TestAssignNames(t *testing.T) {
	ents := []mapents.MapEntity{
		// Two RLs at the same loc → suffixed within (type, kind, loc).
		{Type: mapents.TypeItem, Kind: "rl", Loc: "low", X: 0},
		{Type: mapents.TypeItem, Kind: "rl", Loc: "low", X: 10},
		// Different kind, same loc → bare loc name (kind disambiguates).
		{Type: mapents.TypeItem, Kind: "ra", Loc: "low"},
		// Different type, same loc → bare loc name (type disambiguates).
		{Type: mapents.TypeTeleportDst, Loc: "low"},
		// No loc → kind fallback.
		{Type: mapents.TypeItem, Kind: "lg"},
		// No loc, no kind → type fallback, two spawns → suffixed.
		{Type: mapents.TypeSpawn, X: 1},
		{Type: mapents.TypeSpawn, X: 2},
	}
	assignNames(ents)

	got := make(map[string]int)
	for _, e := range ents {
		got[e.Name]++
	}
	want := []string{"low-1", "low-2", "low", "low", "lg", "spawn-1", "spawn-2"}
	for _, w := range want {
		if got[w] == 0 {
			t.Errorf("expected an entity named %q; names=%v", w, names(ents))
		}
	}
	// "low" should appear exactly twice (the ra item + the teleportDst).
	if got["low"] != 2 {
		t.Errorf("name %q count = %d, want 2; names=%v", "low", got["low"], names(ents))
	}
}

func names(ents []mapents.MapEntity) []string {
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.Name
	}
	return out
}
