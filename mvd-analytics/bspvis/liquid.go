package bspvis

// Liquid queries against the hull-0 render BSP: per-player waterlevel
// classification (mirroring the engine's PM_CategorizePosition) and the
// liquid surface beneath a point. Hull 0 is mandatory here — the clip
// hulls (mapclip) are inflated by the player box, which would distort a
// liquid surface Z; the engine makes the same choice, doing all its
// water checks with point contents on hull 0 (PM_PointContents).
//
// IsLiquid deliberately deviates from the engine predicate in one spot:
// PM_CategorizePosition tests `cont <= CONTENTS_WATER`, which also
// catches CONTENTS_SKY (-6), so a player falling through a sky volume
// reads as "in water" to the physics (drag, no fall damage). Reporting
// a void-faller as swimming — or measuring a jump over a sky pit to the
// "sky surface" — would mislead the consumers this column exists for,
// so sky is excluded and those rare samples read dry.

// Waterlevel probe offsets, from mvdsv/src/pmove.c:650-660 with
// player_mins = {-16,-16,-24}, player_maxs = {16,16,32}:
// feet = mins.z + 1, waist = (mins.z + maxs.z) / 2, eyes = +22.
const (
	waterFeetOffset  = -23
	waterWaistOffset = 4
	waterEyesOffset  = 22

	// liquidTraceMargin extends the downward surface trace a little past
	// the world's lowest point, mirroring mapclip's traceMargin.
	liquidTraceMargin = 64.0
)

// IsLiquid reports whether a leaf contents value is one of the three
// liquids (water/slime/lava). See the package comment on liquid.go for
// why CONTENTS_SKY is excluded despite the engine's physics predicate.
func IsLiquid(contents int32) bool {
	return contents == ContentsWater || contents == ContentsSlime || contents == ContentsLava
}

// liquidIdx is the liquid-leaf AABB index behind the fast reject in
// WaterLevel / LiquidSurfaceBelow. A point inside a liquid leaf is
// necessarily inside that leaf's bounding box, so a probe outside every
// box is provably dry and a column that misses every box in XY (or
// starts below them all) provably has no liquid surface beneath it —
// the reject changes no answers, it only skips BSP walks that would
// return "dry"/"no liquid". Boxes cover every liquid leaf in the file
// (submodel leaves included), which can only make the reject more
// conservative.
type liquidIdx struct {
	boxes      []liquidBox
	umin, umax [3]float32 // union of boxes, valid when len(boxes) > 0

	// disabled: some liquid leaf carries a degenerate (zero or inverted)
	// bounding box — a hand-built or corrupt file — so AABB absence
	// proves nothing and every query must run the real BSP walk.
	disabled bool
}

type liquidBox struct {
	min, max [3]float32
}

// liquidIndex builds the index on first use (guarded for the opt-in
// parallel floor pass, which probes from several goroutines).
func (b *BSP) liquidIndex() *liquidIdx {
	b.liquidOnce.Do(func() {
		idx := &b.liquid
		for i := range b.Leaves {
			l := &b.Leaves[i]
			if !IsLiquid(l.Contents) {
				continue
			}
			bb := liquidBox{
				min: [3]float32{l.Mins.X, l.Mins.Y, l.Mins.Z},
				max: [3]float32{l.Maxs.X, l.Maxs.Y, l.Maxs.Z},
			}
			if bb.min[0] >= bb.max[0] || bb.min[1] >= bb.max[1] || bb.min[2] >= bb.max[2] {
				idx.disabled = true
				idx.boxes = nil
				return
			}
			if len(idx.boxes) == 0 {
				idx.umin, idx.umax = bb.min, bb.max
			} else {
				for a := 0; a < 3; a++ {
					if bb.min[a] < idx.umin[a] {
						idx.umin[a] = bb.min[a]
					}
					if bb.max[a] > idx.umax[a] {
						idx.umax[a] = bb.max[a]
					}
				}
			}
			idx.boxes = append(idx.boxes, bb)
		}
	})
	return &b.liquid
}

// mayContain reports whether (x, y, z) is inside some liquid leaf's
// AABB — false proves the point is not in liquid.
func (idx *liquidIdx) mayContain(x, y, z float32) bool {
	if idx.disabled {
		return true
	}
	if len(idx.boxes) == 0 {
		return false
	}
	if x < idx.umin[0] || x > idx.umax[0] || y < idx.umin[1] || y > idx.umax[1] ||
		z < idx.umin[2] || z > idx.umax[2] {
		return false
	}
	for i := range idx.boxes {
		bb := &idx.boxes[i]
		if x >= bb.min[0] && x <= bb.max[0] && y >= bb.min[1] && y <= bb.max[1] &&
			z >= bb.min[2] && z <= bb.max[2] {
			return true
		}
	}
	return false
}

// columnMayHit reports whether a straight-down ray from (x, y, z) could
// enter some liquid leaf's AABB — false proves no liquid surface lies
// below the point.
func (idx *liquidIdx) columnMayHit(x, y, z float32) bool {
	if idx.disabled {
		return true
	}
	if len(idx.boxes) == 0 {
		return false
	}
	if x < idx.umin[0] || x > idx.umax[0] || y < idx.umin[1] || y > idx.umax[1] ||
		z < idx.umin[2] {
		return false
	}
	for i := range idx.boxes {
		bb := &idx.boxes[i]
		if x >= bb.min[0] && x <= bb.max[0] && y >= bb.min[1] && y <= bb.max[1] &&
			z >= bb.min[2] {
			return true
		}
	}
	return false
}

// WaterLevel classifies how deep a player origin at (x, y, z) sits in
// liquid, mirroring PM_CategorizePosition (mvdsv/src/pmove.c:646-665):
// 0 dry; 1 feet (z-23 in liquid); 2 waist (z+4); 3 eyes (z+22).
// contents is the liquid type at the feet probe (ContentsWater/Slime/
// Lava), meaningful only when level >= 1; ContentsEmpty otherwise.
func (b *BSP) WaterLevel(x, y, z float32) (level int, contents int32) {
	contents = ContentsEmpty
	if !b.liquidIndex().mayContain(x, y, z+waterFeetOffset) {
		return 0, contents
	}
	cont := b.LeafContents(b.PointInLeaf([3]float32{x, y, z + waterFeetOffset}))
	if !IsLiquid(cont) {
		return 0, contents
	}
	contents = cont
	level = 1
	if IsLiquid(b.LeafContents(b.PointInLeaf([3]float32{x, y, z + waterWaistOffset}))) {
		level = 2
		if IsLiquid(b.LeafContents(b.PointInLeaf([3]float32{x, y, z + waterEyesOffset}))) {
			level = 3
		}
	}
	return level, contents
}

// liquid-walk leaf classes, ordered along the ray: the first non-empty
// leaf decides the outcome.
const (
	lwEmpty  = iota // empty (or sky) — keep walking
	lwSolid         // solid blocks before any liquid
	lwLiquid        // entered a liquid leaf
)

// LiquidSurfaceBelow walks the straight-down segment from (x, y, z) to
// the world bottom through the hull-0 render BSP and returns the Z of
// the first crossing from non-liquid into a liquid leaf — the liquid
// surface — plus that leaf's contents. ok is false when a CONTENTS_SOLID
// leaf comes first (a floor blocks before any liquid), no liquid lies
// below, or the start point is already inside solid or liquid (a
// submerged player's surface is above the start, not below — callers
// handle that case via WaterLevel).
//
// Liquid surfaces in Quake maps are horizontal, so unlike the solid
// floor trace there is no footprint sampling — one column is exact.
func (b *BSP) LiquidSurfaceBelow(x, y, z float32) (surfZ float32, contents int32, ok bool) {
	if len(b.Models) == 0 || len(b.Nodes) == 0 {
		return 0, ContentsEmpty, false
	}
	if !b.liquidIndex().columnMayHit(x, y, z) {
		return 0, ContentsEmpty, false
	}
	bottom := b.Models[0].Mins.Z - liquidTraceMargin
	if bottom >= z {
		return 0, ContentsEmpty, false
	}
	lw := liquidWalk{b: b}
	res := lw.walk(b.Models[0].HeadNodes[0], [3]float32{x, y, z}, [3]float32{x, y, bottom})
	if res != lwLiquid || !lw.recorded {
		return 0, ContentsEmpty, false
	}
	return lw.surfZ, lw.contents, true
}

// liquidWalk carries the in-progress surface trace. surfZ is recorded
// at the innermost plane crossing whose near side was all-empty and
// whose far side begins in liquid — the entry point into the liquid
// volume. A ray that STARTS inside liquid reaches the liquid leaf
// without ever crossing such a boundary, leaving recorded false; the
// caller treats that as "no surface below".
type liquidWalk struct {
	b        *BSP
	surfZ    float32
	contents int32
	recorded bool
}

// walk visits the segment p1->p2 under nodeIdx in near-to-far order —
// the same traversal as RayHitsSolid (raytrace.go), widened to a
// three-way outcome and crossing-point bookkeeping.
func (lw *liquidWalk) walk(nodeIdx int32, p1, p2 [3]float32) int {
	if nodeIdx < 0 {
		leafIdx := int(-1 - nodeIdx)
		cont := lw.b.LeafContents(leafIdx)
		switch {
		case cont == ContentsSolid:
			return lwSolid
		case IsLiquid(cont):
			lw.contents = cont
			return lwLiquid
		default:
			return lwEmpty
		}
	}
	if int(nodeIdx) >= len(lw.b.Nodes) {
		return lwSolid
	}
	n := &lw.b.Nodes[nodeIdx]
	// Node.PlaneID is validated in LoadBytes, so the index is safe here.
	pl := &lw.b.Planes[n.PlaneID]
	t1 := pl.Normal.X*p1[0] + pl.Normal.Y*p1[1] + pl.Normal.Z*p1[2] - pl.Dist
	t2 := pl.Normal.X*p2[0] + pl.Normal.Y*p2[1] + pl.Normal.Z*p2[2] - pl.Dist

	if t1 >= 0 && t2 >= 0 {
		return lw.walk(n.Children[0], p1, p2)
	}
	if t1 < 0 && t2 < 0 {
		return lw.walk(n.Children[1], p1, p2)
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
	if res := lw.walk(n.Children[near], p1, mid); res != lwEmpty {
		return res
	}
	res := lw.walk(n.Children[far], mid, p2)
	if res == lwLiquid && !lw.recorded {
		// The near side was all-empty and the far side begins in
		// liquid: this crossing is the surface. Inner recursions record
		// first (and win), since they sit closer to the actual leaf
		// boundary.
		lw.surfZ = mid[2]
		lw.recorded = true
	}
	return res
}
