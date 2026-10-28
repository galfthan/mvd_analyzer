package mapgeom

import (
	"math"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// buildTreeBSP is a hull-0 tree with two water leaves: a pool (x < 128,
// z < 64) under a visible surface, and a detached teleporter-like cell
// (x >= 192, z < 32) with no surface of its own.
func buildTreeBSP() *bsp.BSP {
	return &bsp.BSP{
		Version: 29,
		Planes: []bsp.Plane{
			{Normal: bsp.Vec3{X: 1}, Dist: 128},
			{Normal: bsp.Vec3{X: 1}, Dist: 192},
			{Normal: bsp.Vec3{Z: 1}, Dist: 64},
			{Normal: bsp.Vec3{Z: 1}, Dist: 32},
		},
		Models: []bsp.Model{{Maxs: bsp.Vec3{X: 256, Y: 64, Z: 128}}},
		Nodes: []bsp.Node{
			{PlaneNum: 0, Children: [2]int32{1, 2}},
			{PlaneNum: 1, Children: [2]int32{3, -2}},
			{PlaneNum: 2, Children: [2]int32{-2, -3}},
			{PlaneNum: 3, Children: [2]int32{-2, -4}},
		},
		Leafs: []bsp.Leaf{{Contents: -2}, {Contents: -1}, {Contents: contentsWater}, {Contents: contentsWater}},
	}
}

func TestLiquidVolumes_ClosedCellsConnectedToSurface(t *testing.T) {
	b := buildTreeBSP()
	surface := [][]bsp.Vec3{{{X: 0, Y: 0, Z: 64}, {X: 64, Y: 0, Z: 64}, {X: 64, Y: 64, Z: 64}, {X: 0, Y: 64, Z: 64}}}
	var degen int
	vols := liquidVolumes(b, surface, &degen)
	tris := vols["water"]
	if len(tris) == 0 || len(tris)%9 != 0 {
		t.Fatalf("water volume: %d floats", len(tris))
	}
	// Only the pool cell: the box-padded slab x∈[-16,128], y∈[-16,80],
	// z∈[-16,64]. The detached cell at x >= 192 has no surface and is dropped.
	var vol float64
	for i := 0; i < len(tris); i += 9 {
		for k := 0; k < 9; k += 3 {
			if tris[i+k] > 128 {
				t.Fatalf("vertex x=%v outside the pool cell", tris[i+k])
			}
		}
		a := [3]float64{float64(tris[i]), float64(tris[i+1]), float64(tris[i+2])}
		bb := [3]float64{float64(tris[i+3]), float64(tris[i+4]), float64(tris[i+5])}
		c := [3]float64{float64(tris[i+6]), float64(tris[i+7]), float64(tris[i+8])}
		x := cross3(bb, c)
		vol += (a[0]*x[0] + a[1]*x[1] + a[2]*x[2]) / 6
	}
	// Divergence theorem: outward counter-clockwise winding gives the
	// enclosed volume with a positive sign; a hole or a flipped face breaks it.
	if want := 144.0 * 96 * 80; math.Abs(vol-want) > 1 {
		t.Errorf("enclosed volume = %v, want %v", vol, want)
	}

	// No visible surface at all: nothing is liquid as far as the viewer is
	// concerned (every cell is a teleporter field).
	if got := liquidVolumes(b, nil, &degen); got != nil {
		t.Errorf("no surfaces: got %d kinds, want nil", len(got))
	}
}

func TestLiquidVolumes_NoTree(t *testing.T) {
	b := buildTreeBSP()
	b.Nodes = nil
	var degen int
	if got := liquidVolumes(b, nil, &degen); got != nil {
		t.Errorf("no tree: got %v, want nil", got)
	}
}
