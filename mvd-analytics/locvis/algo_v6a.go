package locvis

import "github.com/mvd-analyzer/mvd-analytics/bspvis"

// attributeV6a implements the V6a "Euclidean primary + raycast-veto"
// algorithm. Same control flow as V6 but the veto is a per-candidate
// raycast through the BSP (line-of-sight) rather than a PVS bit-test.
// Stricter — catches thin-pillar / vertical-wall blockings the PVS
// over-approximation lets through — but more expensive (~O(BSP depth)
// per ray vs O(1) bit-test for V6).
//
// Ported verbatim from experiments/locattr/variants/v6a_euclidean_raycast_veto.
//
// Algorithm:
//  1. Sort every loc ascending by squared Euclidean distance.
//  2. Resolve the player's leaf; if SOLID, wiggle ±1 z, ±0.5 xy. If
//     still SOLID after the wiggle, return V1's nearest (a raycast
//     from inside solid would always hit).
//  3. Walk candidates from nearest. Veto any whose stored leaf is < 0
//     (loc in solid — corpus artifact), or whose segment player→loc
//     crosses a SOLID leaf. First survivor wins.
//  4. If every candidate is vetoed, return V1's nearest.
func (f *Finder) attributeV6a(x, y, z float32) string {
	locs := f.base.Locations()
	if len(locs) == 0 {
		return ""
	}
	sorted := sortedByDistance(locs, x, y, z)
	if len(sorted) == 0 {
		return ""
	}
	fallback := locs[sorted[0].idx].Name

	from, ok := resolveQueryPoint(f.bsp, x, y, z)
	if !ok {
		return fallback
	}
	for _, c := range sorted {
		if f.locLeaves[c.idx] < 0 {
			continue
		}
		to := [3]float32{locs[c.idx].X, locs[c.idx].Y, locs[c.idx].Z}
		if !f.bsp.RayHitsSolid(from, to) {
			return locs[c.idx].Name
		}
	}
	return fallback
}

// resolveQueryPoint returns a non-SOLID origin for the raycast, wiggling
// slightly when the player is jittered into a wall. ok=false means the
// wiggle didn't escape solid, so the caller should skip the veto.
func resolveQueryPoint(bsp *bspvis.BSP, x, y, z float32) ([3]float32, bool) {
	p := [3]float32{x, y, z}
	if !bsp.PointSolid(p) {
		return p, true
	}
	for _, off := range queryWiggle {
		q := [3]float32{x + off[0], y + off[1], z + off[2]}
		if !bsp.PointSolid(q) {
			return q, true
		}
	}
	return p, false
}
