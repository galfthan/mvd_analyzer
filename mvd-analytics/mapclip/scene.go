package mapclip

import "math"

// PosedHull is a collision hull positioned at an entity origin — one
// mover (lift, door, train) at a moment in time. The pose changes per
// sample, so callers build these transiently from their mover-origin
// timelines rather than holding long-lived scene state.
type PosedHull struct {
	H      *Hull
	Origin [3]float32
}

// sceneAABBSlack widens the posed-AABB fast reject a touch beyond the
// exact player-box inflation so a column exactly on the inflated
// boundary is traced rather than rejected.
const sceneAABBSlack = 1.0

// columnMayHit reports whether a downward trace in column (cx, cy)
// starting at z could possibly hit this posed hull: the column must be
// horizontally within the model bounds plus the player-box inflation,
// and the start must not be below the inflated solid's bottom (solid
// extends playerBoxTop below raw geometry in hull 1; a start under
// that can only miss, and a start inside it reports no floor anyway).
func (m *PosedHull) columnMayHit(cx, cy, z float32) bool {
	h := m.H
	if h == nil || len(h.nodes) == 0 {
		return false
	}
	const pad = playerBoxHalf + sceneAABBSlack
	if cx < h.mins[0]+m.Origin[0]-pad || cx > h.maxs[0]+m.Origin[0]+pad {
		return false
	}
	if cy < h.mins[1]+m.Origin[1]-pad || cy > h.maxs[1]+m.Origin[1]+pad {
		return false
	}
	return z >= h.mins[2]+m.Origin[2]-playerBoxTop
}

// footprintMayHit is columnMayHit widened to the whole 3×3 footprint
// around (x, y): true for every sample one of whose columns columnMayHit
// accepts (plus a unit of slack, so float rounding of x±footprintMargin
// can never make it stricter than the column test).
func (m *PosedHull) footprintMayHit(x, y, z float32) bool {
	h := m.H
	if h == nil || len(h.nodes) == 0 {
		return false
	}
	const pad = playerBoxHalf + sceneAABBSlack + footprintMargin + 1
	if x < h.mins[0]+m.Origin[0]-pad || x > h.maxs[0]+m.Origin[0]+pad {
		return false
	}
	if y < h.mins[1]+m.Origin[1]-pad || y > h.maxs[1]+m.Origin[1]+pad {
		return false
	}
	return z >= h.mins[2]+m.Origin[2]-playerBoxTop
}

// SweptFootprintBox bounds the sample origins whose footprint can reach
// this hull at any pose with origin in [minOrg, maxOrg]: footprintMayHit
// is false outside [lo, hi] for every such pose (hi[2] is unbounded — a
// start any height above a mover may still land on it). Float addition
// rounds monotonically, so the swept box contains every pose's box
// exactly as footprintMayHit computes it. ok is false for a hull no
// sample can reach.
func (h *Hull) SweptFootprintBox(minOrg, maxOrg [3]float32) (lo, hi [3]float32, ok bool) {
	if h == nil || len(h.nodes) == 0 {
		return lo, hi, false
	}
	const pad = playerBoxHalf + sceneAABBSlack + footprintMargin + 1
	for k := 0; k < 2; k++ {
		lo[k] = h.mins[k] + minOrg[k] - pad
		hi[k] = h.maxs[k] + maxOrg[k] + pad
	}
	lo[2] = h.mins[2] + minOrg[2] - playerBoxTop
	hi[2] = float32(math.Inf(1))
	return lo, hi, true
}

// HeightAboveFloorBoxScene is HeightAboveFloorBox over the union of the
// worldspawn hull and posed mover hulls: for every footprint column the
// highest floor across all hulls wins, then the player-feet offset is
// subtracted. This is what stands a player on the dm2 RA lift instead
// of the shaft floor far beneath it — the lift's posed hull contributes
// the higher floor and takes the max.
//
// movers may be nil/empty, making the result identical to
// world.HeightAboveFloorBox. A column that starts inside one hull
// (player overlapping a mover mid-travel) skips that hull only; the
// other hulls still contribute. The bool is false only when no column
// finds a floor in any hull.
func HeightAboveFloorBoxScene(world *Hull, movers []PosedHull, x, y, z float32) (float32, bool) {
	if len(movers) == 0 {
		if world == nil {
			return 0, false
		}
		return world.HeightAboveFloorBox(x, y, z)
	}
	// The movers any of the nine columns can reach, found once per sample
	// rather than per column (a map has dozens of doors, plats and
	// buttons; a sample is near a handful at most). footprintMayHit is a
	// superset of every column's columnMayHit, and the candidates keep
	// the movers' order, so the result is the one the per-column scan
	// gave.
	var candBuf [16]int32
	cands := candBuf[:0]
	for i := range movers {
		if movers[i].footprintMayHit(x, y, z) {
			cands = append(cands, int32(i))
		}
	}
	best := float32(0)
	any := false
	for _, dx := range footprintOffsets {
		for _, dy := range footprintOffsets {
			cx, cy := x+dx, y+dy
			if world != nil {
				if floorZ, ok := world.FloorBelow(cx, cy, z); ok && (!any || floorZ > best) {
					best, any = floorZ, true
				}
			}
			for _, i := range cands {
				m := &movers[i]
				if !m.columnMayHit(cx, cy, z) {
					continue
				}
				if floorZ, ok := m.H.FloorBelowAt(cx, cy, z, m.Origin); ok && (!any || floorZ > best) {
					best, any = floorZ, true
				}
			}
		}
	}
	if !any {
		return 0, false
	}
	return (z - PlayerFeetOffset) - best, true
}
