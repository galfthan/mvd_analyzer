package bspvis

// RayHitsSolid reports whether the segment a->c crosses any
// CONTENTS_SOLID leaf in the visibility BSP. Both endpoints are world-
// unit coordinates (same scale as the loc-attribution pipeline).
//
// This is a boolean specialisation of the standard Quake
// SV_RecursiveHullCheck (mvdsv/src/cmodel.c:RecursiveHullTrace, lines
// 222-339): we descend the visibility tree, split the segment at plane
// crossings, and report "hit" the moment any traversed leaf has
// CONTENTS_SOLID. The full engine version tracks the impact point,
// surface normal, and a DIST_EPSILON nudge; for line-of-sight we don't
// need any of that.
//
// Liquid leaves (water/slime/lava) do not block — only CONTENTS_SOLID.
// A segment that starts inside solid returns true (start-solid is a
// "hit" by definition here).
func (b *BSP) RayHitsSolid(a, c [3]float32) bool {
	if len(b.Models) == 0 || len(b.Nodes) == 0 {
		return true
	}
	return b.segHitsSolid(b.Models[0].HeadNodes[0], a, c)
}

// segHitsSolid is the recursive trace. nodeIdx >= 0 selects an interior
// node; nodeIdx < 0 selects leaf -1 - nodeIdx.
//
// Sign convention here differs from PointInLeaf: hull trace uses
// `>= 0 -> front` (not `> 0`), matching cmodel.c:RecursiveHullTrace and
// SV_RecursiveHullCheck in WinQuake/world.c. The `=` matters when an
// endpoint lies exactly on a splitting plane — the engine biases such
// rays to the front child.
func (b *BSP) segHitsSolid(nodeIdx int32, p1, p2 [3]float32) bool {
	if nodeIdx < 0 {
		leafIdx := int(-1 - nodeIdx)
		if leafIdx < 0 || leafIdx >= len(b.Leaves) {
			return true
		}
		return b.Leaves[leafIdx].Contents == ContentsSolid
	}
	if int(nodeIdx) >= len(b.Nodes) {
		return true
	}
	n := &b.Nodes[nodeIdx]
	if int(n.PlaneID) >= len(b.Planes) {
		return true
	}
	pl := &b.Planes[n.PlaneID]
	t1 := pl.Normal.X*p1[0] + pl.Normal.Y*p1[1] + pl.Normal.Z*p1[2] - pl.Dist
	t2 := pl.Normal.X*p2[0] + pl.Normal.Y*p2[1] + pl.Normal.Z*p2[2] - pl.Dist

	if t1 >= 0 && t2 >= 0 {
		return b.segHitsSolid(n.Children[0], p1, p2)
	}
	if t1 < 0 && t2 < 0 {
		return b.segHitsSolid(n.Children[1], p1, p2)
	}

	denom := t1 - t2
	frac := float32(0.5)
	if denom != 0 {
		frac = t1 / denom
	}
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	mid := [3]float32{
		p1[0] + frac*(p2[0]-p1[0]),
		p1[1] + frac*(p2[1]-p1[1]),
		p1[2] + frac*(p2[2]-p1[2]),
	}

	near, far := int32(0), int32(1)
	if t1 < 0 {
		near, far = 1, 0
	}
	if b.segHitsSolid(n.Children[near], p1, mid) {
		return true
	}
	return b.segHitsSolid(n.Children[far], mid, p2)
}
