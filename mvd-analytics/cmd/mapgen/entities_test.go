package main

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapents"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
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

func TestBrushPlacement(t *testing.T) {
	models := []bsp.ModelBounds{
		{Mins: bsp.Vec3{X: 0, Y: 0, Z: 0}, Maxs: bsp.Vec3{X: 0, Y: 0, Z: 0}},     // *0 worldspawn
		{Mins: bsp.Vec3{X: 10, Y: 20, Z: 0}, Maxs: bsp.Vec3{X: 30, Y: 40, Z: 8}}, // *1
	}
	// Valid submodel ref → centre + bounds.
	e := bsp.Entity{Classname: "trigger_teleport", RawKeys: map[string]string{"model": "*1"}}
	c, b, ok := brushPlacement(e, models)
	if !ok {
		t.Fatal("expected placement for *1")
	}
	if c != [3]float32{20, 30, 4} {
		t.Errorf("centre = %v, want [20 30 4]", c)
	}
	if b == nil || b.Min != [3]float32{10, 20, 0} || b.Max != [3]float32{30, 40, 8} {
		t.Errorf("bounds = %+v, want min[10 20 0] max[30 40 8]", b)
	}
	// Missing / non-submodel / out-of-range model → not placed.
	for _, bad := range []map[string]string{{}, {"model": "maps/x.bsp"}, {"model": "*9"}} {
		if _, _, ok := brushPlacement(bsp.Entity{RawKeys: bad}, models); ok {
			t.Errorf("expected no placement for %v", bad)
		}
	}
}

func names(ents []mapents.MapEntity) []string {
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.Name
	}
	return out
}
