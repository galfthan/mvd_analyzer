package locvis

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/loc"
)

// attributeV6 implements the V6 "Euclidean primary + PVS-veto" algorithm.
// Ported verbatim from experiments/locattr/variants/v6_euclidean_pvs_veto.
//
// Algorithm:
//  1. Sort every loc ascending by squared Euclidean distance.
//  2. Resolve the player's leaf; if SOLID, wiggle ±1 z, ±0.5 xy. If
//     still SOLID after the wiggle, give up on the veto and return V1's
//     nearest (a PVS test from inside solid has no meaningful row).
//  3. Walk candidates from nearest. Veto any whose stored leaf is < 0
//     (loc lives in solid — corpus artifact) or whose leaf is not in
//     the player's PVS row. First survivor wins.
//  4. If every candidate is vetoed, return V1's nearest.
func (f *Finder) attributeV6(x, y, z float32) string {
	locs := f.base.Locations()
	if len(locs) == 0 {
		return ""
	}
	sorted := sortedByDistance(locs, x, y, z)
	if len(sorted) == 0 {
		return ""
	}
	fallback := locs[sorted[0].idx].Name

	queryLeaf, ok := resolveQueryLeaf(f.bsp, x, y, z)
	if !ok {
		return fallback
	}
	pvs := f.bsp.LeafPVS(queryLeaf)
	for _, c := range sorted {
		leaf := f.locLeaves[c.idx]
		if leaf < 0 {
			continue
		}
		if f.bsp.PVSContains(pvs, leaf) {
			return locs[c.idx].Name
		}
	}
	return fallback
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
// position resolves to a SOLID leaf — usually it means the position
// sample is sitting exactly on a floor or wall face, and a tiny nudge
// reveals the actual empty leaf the player occupies. Z first (the most
// common case is the player origin on a floor face), then XY.
var queryWiggle = [...][3]float32{
	{0, 0, 1}, {0, 0, -1},
	{0.5, 0, 0}, {-0.5, 0, 0},
	{0, 0.5, 0}, {0, -0.5, 0},
}

// candidate pairs a loc index with its squared distance to the query
// position.
type candidate struct {
	idx    int
	distSq float32
}

// sortedByDistance returns every loc sorted ascending by squared
// distance from (x,y,z). Both V6 and V6a need the full ordering for
// the V1 fallback path (cand[0] is V1's answer).
func sortedByDistance(locs []loc.Location, x, y, z float32) []candidate {
	if len(locs) == 0 {
		return nil
	}
	scored := make([]candidate, len(locs))
	for i := range locs {
		dx := x - locs[i].X
		dy := y - locs[i].Y
		dz := z - locs[i].Z
		scored[i] = candidate{idx: i, distSq: dx*dx + dy*dy + dz*dz}
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].distSq < scored[j].distSq
	})
	return scored
}
