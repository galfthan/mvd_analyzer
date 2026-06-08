package mapclip

import (
	"math"
	"os"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// floorHull builds a one-plane clip hull: a horizontal clip plane at
// Z=24 with EMPTY above and SOLID below. Because the player origin rests
// playerFeetOffset (24) above the floor it stands on, a clip plane at 24
// represents a real floor surface at Z=0 — FloorBelow should return ~0.
func floorHull(t *testing.T) *bsp.BSP {
	t.Helper()
	return &bsp.BSP{
		Planes: []bsp.Plane{{Normal: bsp.Vec3{X: 0, Y: 0, Z: 1}, Dist: 24, Type: 2}},
		Models: []bsp.Model{{Mins: bsp.Vec3{Z: -100}, HeadNodes: [4]int32{0, 0, 0, 0}}},
		// child[0] (front, Z>=plane) → EMPTY, child[1] (back) → SOLID.
		ClipNodes: []bsp.ClipNode{{PlaneNum: 0, Children: [2]int32{contentsEmpty, contentsSolid}}},
	}
}

func TestFloorBelow_SinglePlane(t *testing.T) {
	h, err := Build(floorHull(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Well above the floor → floor surface ≈ 0 (clip plane 24 − feet 24).
	z, ok := h.FloorBelow(0, 0, 200)
	if !ok {
		t.Fatalf("FloorBelow(0,0,200) found no floor")
	}
	if math.Abs(float64(z)) > 0.5 {
		t.Errorf("floor Z = %v, want ≈ 0", z)
	}

	// A grounded player sits playerFeetOffset above the floor.
	if z2, ok := h.FloorBelow(0, 0, float32(playerFeetOffset)); !ok || math.Abs(float64(z2)) > 0.5 {
		t.Errorf("grounded FloorBelow = (%v,%v), want (≈0,true)", z2, ok)
	}

	// A point below the clip plane starts inside solid → not attributable.
	if _, ok := h.FloorBelow(0, 0, -50); ok {
		t.Errorf("FloorBelow below solid should report no floor")
	}

	// HeightAboveFloor reads ~0 grounded and the airborne delta otherwise.
	if hg, ok := h.HeightAboveFloor(0, 0, float32(playerFeetOffset)); !ok || math.Abs(float64(hg)) > 0.5 {
		t.Errorf("grounded height = (%v,%v), want (≈0,true)", hg, ok)
	}
	if ha, ok := h.HeightAboveFloor(0, 0, float32(playerFeetOffset)+100); !ok || math.Abs(float64(ha-100)) > 0.5 {
		t.Errorf("airborne height = (%v,%v), want (≈100,true)", ha, ok)
	}
}

func TestFloorBelow_NoFloorOverVoid(t *testing.T) {
	// Both sides empty → nothing solid below → no floor anywhere.
	b := &bsp.BSP{
		Planes:    []bsp.Plane{{Normal: bsp.Vec3{X: 0, Y: 0, Z: 1}, Dist: 24, Type: 2}},
		Models:    []bsp.Model{{Mins: bsp.Vec3{Z: -100}, HeadNodes: [4]int32{0, 0, 0, 0}}},
		ClipNodes: []bsp.ClipNode{{PlaneNum: 0, Children: [2]int32{contentsEmpty, contentsEmpty}}},
	}
	h, err := Build(b)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := h.FloorBelow(0, 0, 200); ok {
		t.Errorf("expected no floor over void")
	}
}

// TestLoadForMap_FromBSP checks the runtime path: point the shared
// mapbsp loader at the vendored maps/ tree and build dm2's hull straight
// from its .bsp. Skips when maps/ is absent (CI has no BSPs — floor then
// degrades to absent, same as locvis visibility).
func TestLoadForMap_FromBSP(t *testing.T) {
	const dir = "../../maps"
	if _, err := os.Stat(dir + "/dm2.bsp"); err != nil {
		t.Skip("no vendored maps/dm2.bsp; skipping BSP-load test")
	}
	mapbsp.SetDir(dir)
	defer mapbsp.SetDir("")

	h, err := LoadForMap("dm2")
	if err != nil {
		t.Fatalf("LoadForMap(dm2): %v", err)
	}
	if len(h.nodes) == 0 || len(h.planes) == 0 {
		t.Fatalf("dm2 hull empty: %d nodes, %d planes", len(h.nodes), len(h.planes))
	}
	if _, err := LoadForMap("definitely-not-a-real-map-xyz"); err == nil {
		t.Errorf("expected error for missing map")
	}
}

// TestFloorBelow_RealBSP is a developer smoke test against a vendored
// BSP. It traces straight down from each player-spawn origin: a spawn
// point's origin is the standing player origin, so the floor under it
// must sit playerFeetOffset (24) below — within a couple of units after
// integer rounding and slope. Skips when the per-machine maps/ tree is
// absent (CI has no BSPs).
func TestFloorBelow_RealBSP(t *testing.T) {
	const path = "../../maps/dm2.bsp"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no vendored maps/dm2.bsp; skipping real-BSP smoke test")
	}
	parsed, err := bsp.Parse(path)
	if err != nil {
		t.Fatalf("parse dm2.bsp: %v", err)
	}
	h, err := Build(parsed)
	if err != nil {
		t.Fatalf("Build dm2: %v", err)
	}
	ents, err := bsp.ReadEntities(path)
	if err != nil {
		t.Fatalf("ReadEntities dm2: %v", err)
	}
	tested, found := 0, 0
	for _, e := range ents {
		if e.Classname != "info_player_deathmatch" {
			continue
		}
		// Trace from a little above the spawn origin so we never start
		// exactly on a clip boundary.
		fz, ok := h.FloorBelow(e.Origin[0], e.Origin[1], e.Origin[2]+16)
		tested++
		if !ok {
			// Legitimate: some dm2 spawns rest on the RA/quad lift, a
			// moving brush model pruned from the worldspawn hull
			// (Approach A). No static floor below is the correct answer.
			continue
		}
		found++
		gap := float64(e.Origin[2] - fz)
		if gap < 16 || gap > 32 {
			t.Errorf("spawn at %v: origin-floor gap %.1f, want ≈24", e.Origin, gap)
		}
	}
	if tested == 0 {
		t.Fatal("no spawn points found in dm2.bsp")
	}
	// The vast majority of spawns sit on static world floor; only a few
	// lift spawns legitimately miss. Guard against a wholesale failure
	// (e.g. a broken trace returning no floor everywhere).
	if found*2 < tested {
		t.Errorf("only %d/%d spawns found a static floor — trace likely broken", found, tested)
	}
	t.Logf("dm2 spawns: %d/%d on static floor (rest on lifts)", found, tested)
}
