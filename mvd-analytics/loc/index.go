package loc

import (
	"math"
	"sync"
)

// pencilIndex is a 2D uniform-grid accelerator: each cell is a
// pencil — a `cellSize × cellSize` XY square extending the full Z
// range. Inspired by the linked-cell lists used in MD simulations,
// adapted to QW's loc geometry where playable space is mostly
// XY-extended with stacked vertical layers per location.
//
// Build is O(L), query is O(neighbors in 3×3 XY shell), which beats
// the O(L) linear scan once L grows past a few hundred. At
// competitive-map sizes (L ≤ ~316) the linear scan still wins on
// constant factors; the lazy build means we only pay setup when the
// caller actually queries.
type pencilIndex struct {
	cellSize float32
	cells    map[[2]int][]int // (xCell, yCell) → indices into Finder.locations
}

// defaultCellSize for pencil cells, in world units. Empirically
// 256 gives the best L>500 wins on the corpus: competitive maps
// land ~30–50 cells (1–8 locs each), big custom maps ~100–300 cells
// (3–10 locs each), keeping the 3×3 shell candidate count low at
// every L.
const defaultCellSize = 256

func buildPencilIndex(locs []Location, cellSize float32) *pencilIndex {
	if cellSize <= 0 {
		cellSize = defaultCellSize
	}
	idx := &pencilIndex{cellSize: cellSize, cells: make(map[[2]int][]int, len(locs))}
	for i, l := range locs {
		k := idx.keyOf(l.X, l.Y)
		idx.cells[k] = append(idx.cells[k], i)
	}
	return idx
}

func (i *pencilIndex) keyOf(x, y float32) [2]int {
	return [2]int{
		int(math.Floor(float64(x / i.cellSize))),
		int(math.Floor(float64(y / i.cellSize))),
	}
}

// findNearest returns the index of the nearest loc and its squared
// distance, or (-1, 0) if the index is empty.
func (i *pencilIndex) findNearest(locs []Location, x, y, z float32) (int, float32) {
	if len(i.cells) == 0 {
		return -1, 0
	}
	qc := i.keyOf(x, y)

	bestIdx := -1
	bestDistSq := float32(math.MaxFloat32)

	// Expanding XY-shell search. At radius r, scan cells whose
	// chebyshev distance from qc is exactly r. Terminate once the
	// best squared distance is no greater than (r * cellSize)² —
	// past that radius no cell can hold a closer point.
	//
	// First iteration (r=0) covers the home cell; nearly all queries
	// on dense maps terminate after r=1 (3×3 scan).
	for r := 0; r <= 16; r++ {
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				if r > 0 && abs(dx) < r && abs(dy) < r {
					continue // only the surface of the r-shell
				}
				key := [2]int{qc[0] + dx, qc[1] + dy}
				for _, li := range i.cells[key] {
					l := locs[li]
					ddx := x - l.X
					ddy := y - l.Y
					ddz := z - l.Z
					d := ddx*ddx + ddy*ddy + ddz*ddz
					if d < bestDistSq {
						bestDistSq = d
						bestIdx = li
					}
				}
			}
		}
		if bestIdx >= 0 {
			cutoff := float32(r) * i.cellSize
			if bestDistSq <= cutoff*cutoff {
				return bestIdx, bestDistSq
			}
		}
	}
	return bestIdx, bestDistSq
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// indexOnce builds the pencilIndex on the first FindNearest call,
// so callers that load a Finder and never query (e.g. cmd/mapgen
// dumping the .loc corpus) don't pay setup cost.
type indexOnce struct {
	once sync.Once
	idx  *pencilIndex
}
