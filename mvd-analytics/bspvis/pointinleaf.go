package bspvis

// Q1 leaf contents constants (mvdsv/src/bspfile.h:144-149). Anything <
// CONTENTS_EMPTY is "occupied" in some sense; only CONTENTS_SOLID
// actually blocks visibility / movement.
const (
	ContentsEmpty = -1
	ContentsSolid = -2
	ContentsWater = -3
	ContentsSlime = -4
	ContentsLava  = -5
	ContentsSky   = -6
)

// LeafCount returns the number of leaves in the visibility BSP, including
// the universal CONTENTS_SOLID sink at index 0.
func (b *BSP) LeafCount() int {
	return len(b.Leaves)
}

// LeafContents returns the contents value of the given leaf, or
// CONTENTS_SOLID for indices outside the valid range — the same fallback
// the engine uses when an invalid leaf is queried.
func (b *BSP) LeafContents(leafIdx int) int32 {
	if leafIdx < 0 || leafIdx >= len(b.Leaves) {
		return ContentsSolid
	}
	return b.Leaves[leafIdx].Contents
}

// PointInLeaf returns the index of the leaf containing the given point,
// descending the worldspawn visibility BSP from Models[0].HeadNodes[0].
//
// Algorithm follows ezquake-source/src/r_model.c:Mod_PointInLeaf
// (lines 72-94) and mvdsv/src/cmodel.c:CM_PointInLeaf (lines 397-418):
// at each node compute the signed plane distance, recurse the front
// child (>0) or back child (<=0) — ties go to the back child. Stop when
// a child is negative; the leaf index is -1 - child.
//
// If the model has no head node (degenerate map), returns 0 (the
// CONTENTS_SOLID sink at leaf 0).
func (b *BSP) PointInLeaf(p [3]float32) int {
	if len(b.Models) == 0 || len(b.Nodes) == 0 {
		return 0
	}
	cur := b.Models[0].HeadNodes[0]
	for cur >= 0 {
		if int(cur) >= len(b.Nodes) {
			return 0
		}
		n := &b.Nodes[cur]
		pl := &b.Planes[n.PlaneID]
		d := pl.Normal.X*p[0] + pl.Normal.Y*p[1] + pl.Normal.Z*p[2] - pl.Dist
		if d > 0 {
			cur = n.Children[0]
		} else {
			cur = n.Children[1]
		}
	}
	leafIdx := int(-1 - cur)
	if leafIdx < 0 || leafIdx >= len(b.Leaves) {
		return 0
	}
	return leafIdx
}

// PointSolid is a convenience: returns true iff the point falls inside a
// CONTENTS_SOLID leaf. Liquid contents (water/slime/lava) are not solid.
func (b *BSP) PointSolid(p [3]float32) bool {
	return b.LeafContents(b.PointInLeaf(p)) == ContentsSolid
}
