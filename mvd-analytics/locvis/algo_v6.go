package locvis

import (
	"math"

	"github.com/mvd-analyzer/mvd-analytics/bspvis"
)

// attributeV6 implements the V6 "nearest PVS-visible loc" algorithm.
// Per-query cost:
//
//  1. resolveQueryLeaf — PointInLeaf for the player position, with a
//     wiggle if it lands in CONTENTS_SOLID. O(BSP depth) ≈ 100–200 ns.
//  2. f.leafVisLocs[queryLeaf] — slice index, O(1).
//  3. Linear scan over the precomputed visible-locs list, tracking
//     Euclidean-nearest. O(M), M = locs visible from the player's leaf.
//
// If the wiggle can't escape solid, OR the player's leaf has no visible
// locs, fall back to f.base.FindNearest (V1 — pencil-indexed on large
// maps, linear scan otherwise).
//
// The PVS bit-test work moved out of the hot path into LoadForMap, so
// FindNearest never touches f.bsp at runtime beyond resolveQueryLeaf's
// PointInLeaf + LeafContents probes.
func (f *Finder) attributeV6(x, y, z float32) string {
	queryLeaf, ok := resolveQueryLeaf(f.bsp, x, y, z)
	if !ok {
		return f.base.FindNearest(x, y, z)
	}
	cands := f.leafVisLocs[queryLeaf]
	if len(cands) == 0 {
		return f.base.FindNearest(x, y, z)
	}
	locs := f.base.Locations()
	bestIdx := int32(-1)
	bestDistSq := float32(math.MaxFloat32)
	for _, i := range cands {
		dx := x - locs[i].X
		dy := y - locs[i].Y
		dz := z - locs[i].Z
		d := dx*dx + dy*dy + dz*dz
		if d < bestDistSq {
			bestDistSq = d
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return f.base.FindNearest(x, y, z)
	}
	return locs[bestIdx].Name
}

// resolveQueryLeaf returns the leaf for (x,y,z), wiggling slightly if
// the player is jittered into a SOLID leaf. ok=false means even the
// wiggled positions stayed in solid.
func resolveQueryLeaf(bsp *bspvis.BSP, x, y, z float32) (int, bool) {
	leaf := bsp.PointInLeaf([3]float32{x, y, z})
	if bsp.LeafContents(leaf) != bspvis.ContentsSolid {
		return leaf, true
	}
	for _, off := range queryWiggle {
		l := bsp.PointInLeaf([3]float32{x + off[0], y + off[1], z + off[2]})
		if bsp.LeafContents(l) != bspvis.ContentsSolid {
			return l, true
		}
	}
	return 0, false
}

// queryWiggle is the set of small offsets we try when the player's
// position resolves to a SOLID leaf — usually the position sample is
// sitting exactly on a floor / wall face and a tiny nudge reveals the
// actual empty leaf the player occupies. Z first (most common: player
// origin on a floor face), then XY.
var queryWiggle = [...][3]float32{
	{0, 0, 1}, {0, 0, -1},
	{0.5, 0, 0}, {-0.5, 0, 0},
	{0, 0.5, 0}, {0, -0.5, 0},
}
