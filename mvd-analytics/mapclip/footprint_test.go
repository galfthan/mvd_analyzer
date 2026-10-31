package mapclip

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// referenceBox is HeightAboveFloorBox as nine independent FloorBelow
// columns — the definition the set-traced footprint must reproduce.
func referenceBox(h *Hull, x, y, z float32) (float32, bool) {
	best, any := referenceFloor(h, nil, x, y, z)
	if !any {
		return 0, false
	}
	return z - PlayerFeetOffset - best, true
}

// referenceFloor is the highest floor of the nine columns over the world
// and every posed mover the column can reach.
func referenceFloor(world *Hull, movers []PosedHull, x, y, z float32) (float32, bool) {
	var best float32
	any := false
	for _, dx := range footprintOffsets {
		for _, dy := range footprintOffsets {
			cx, cy := x+dx, y+dy
			if world != nil {
				if f, ok := world.FloorBelow(cx, cy, z); ok && (!any || f > best) {
					best, any = f, true
				}
			}
			for i := range movers {
				m := &movers[i]
				if !m.columnMayHit(cx, cy, z) {
					continue
				}
				if f, ok := m.H.FloorBelowAt(cx, cy, z, m.Origin); ok && (!any || f > best) {
					best, any = f, true
				}
			}
		}
	}
	return best, any
}

// TestFootprintSetsMatchPerColumnTraces pins the set-traced footprint
// (footprint.go) to nine independent traces, bit for bit, on every BSP
// provisioned under bsps/, for the world hull and for posed movers.
// Points are on the wire's eighth-unit grid, so many sit exactly on
// axial planes — the boundary cases the set proof has to get right.
// Skips without BSPs.
func TestFootprintSetsMatchPerColumnTraces(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "bsps", "*.bsp"))
	if len(files) == 0 {
		t.Skip("no BSPs under bsps/ (make bsps)")
	}
	rng := rand.New(rand.NewSource(1))
	snap := func(v float32) float32 { return float32(math.Round(float64(v)*8) / 8) }
	n := 4000
	if testing.Short() {
		n = 400
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := bsp.ParseBytes(data)
		if err != nil {
			continue // a format the loader also skips
		}
		world, err := Build(parsed)
		if err != nil {
			continue
		}
		var movers []PosedHull
		for i := 1; i < len(parsed.Models); i++ {
			if h, err := BuildModel(parsed, i); err == nil {
				movers = append(movers, PosedHull{H: h})
			}
		}
		lo, hi := world.mins, world.maxs
		at := func(k int) float32 { return snap(lo[k] + rng.Float32()*(hi[k]-lo[k])) }
		bad := 0
		for i := 0; i < n && bad < 5; i++ {
			x, y, z := at(0), at(1), at(2)
			gh, gok := world.HeightAboveFloorBox(x, y, z)
			wh, wok := referenceBox(world, x, y, z)
			if gok != wok || math.Float32bits(gh) != math.Float32bits(wh) {
				t.Errorf("%s world (%g,%g,%g): set %v/%v, columns %v/%v", filepath.Base(f), x, y, z, gh, gok, wh, wok)
				bad++
			}
			// Movers posed at small random offsets (lifts, doors), the
			// scene query against the per-column scan over all of them.
			for k := range movers {
				movers[k].Origin = [3]float32{snap(rng.Float32()*64 - 32), snap(rng.Float32()*64 - 32), snap(rng.Float32()*256 - 128)}
			}
			gh, gok = HeightAboveFloorBoxScene(world, movers, x, y, z)
			best, any := referenceFloor(world, movers, x, y, z)
			wh, wok = z-PlayerFeetOffset-best, any
			if !any {
				wh = 0
			}
			if gok != wok || math.Float32bits(gh) != math.Float32bits(wh) {
				t.Errorf("%s scene (%g,%g,%g): set %v/%v, columns %v/%v", filepath.Base(f), x, y, z, gh, gok, wh, wok)
				bad++
			}
		}
	}
}
