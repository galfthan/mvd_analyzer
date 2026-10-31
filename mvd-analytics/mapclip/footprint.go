package mapclip

// Footprint traces: the nine HeightAboveFloorBox columns traced as
// sets that share one descent.
//
// A downward trace's arithmetic depends on a column's x/y only at
// nodes whose plane is not horizontal: an X/Y-axial plane picks a child
// by the sign of x−dist / y−dist, a general plane by a dot product that
// includes x and y. At a horizontal plane (typ 2) t1/t2 are z−dist for
// every column alike, and the split point (mid) keeps each column's x/y
// exactly (p1[i] + frac·0). So a set of columns that makes the same
// child choice at every non-horizontal node the traced column visits,
// and straddles only horizontal planes, runs the identical instruction
// sequence on identical z values: every member's FloorBelow result is
// bit-identical to the traced column's. traceSet carries that proof and
// aborts the moment it fails; floorBelowSet then splits the set along
// the deciding axis (an X plane splits xs, a Y plane ys, anything else
// falls back to single columns, which are their own proof) and traces
// the parts. The result is the same max HeightAboveFloorBox always took,
// for typically ~2.5 descents per sample instead of nine.

// trAbort is a fourth trace outcome: the set proof failed.
const trAbort = 3

// setMargin guards the sign test on general (non-axial) planes, whose
// per-column dot products the real trace might round differently (FMA
// fusion is allowed on some targets): a member column within this
// distance of such a plane counts as undecided and the set splits.
const setMargin = distEpsilon

// colSet is a sub-grid of the 3×3 footprint: nx×ny columns at xs × ys
// (start-space coordinates, i.e. already translated into a posed hull's
// model space), with xi/yj their footprint indices for the mover
// column filter. The traced column is (xs[0], ys[0]).
type colSet struct {
	xs, ys [3]float32
	xi, yj [3]int8
	nx, ny int
}

func newFootprintSet(x, y, offX, offY float32) colSet {
	cs := colSet{nx: 3, ny: 3}
	for k := 0; k < 3; k++ {
		cs.xs[k] = (x + footprintOffsets[k]) - offX
		cs.ys[k] = (y + footprintOffsets[k]) - offY
		cs.xi[k], cs.yj[k] = int8(k), int8(k)
	}
	return cs
}

func (cs *colSet) span(v *[3]float32, n int) float32 {
	var m float32
	for k := 1; k < n; k++ {
		d := v[k] - v[0]
		if d < 0 {
			d = -d
		}
		if d > m {
			m = d
		}
	}
	return m
}

// setTrace is hullTrace plus the set proof.
type setTrace struct {
	hullTrace
	cs       *colSet
	abortPl  *plane
	straddle bool
}

// sameSide reports whether every member column takes the child the
// traced column takes (front: t >= 0 at both ends) at plane pl over the
// segment z1..z2.
func (st *setTrace) sameSide(pl *plane, z1, z2 float32, front bool) bool {
	cs := st.cs
	switch pl.typ {
	case 0:
		for k := 0; k < cs.nx; k++ {
			if (cs.xs[k]-pl.dist >= 0) != front {
				return false
			}
		}
		return true
	case 1:
		for k := 0; k < cs.ny; k++ {
			if (cs.ys[k]-pl.dist >= 0) != front {
				return false
			}
		}
		return true
	case 2:
		return true
	}
	if cs.nx == 1 && cs.ny == 1 {
		return true
	}
	// A member's t differs from the traced column's by at most
	// |n0|·span_x + |n1|·span_y; clearing that bound settles the set
	// without evaluating each member.
	base := pl.n[0]*cs.xs[0] + pl.n[1]*cs.ys[0] - pl.dist
	a := base + pl.n[2]*z1
	b := base + pl.n[2]*z2
	n0, n1 := pl.n[0], pl.n[1]
	if n0 < 0 {
		n0 = -n0
	}
	if n1 < 0 {
		n1 = -n1
	}
	bound := n0*cs.span(&cs.xs, cs.nx) + n1*cs.span(&cs.ys, cs.ny) + setMargin
	if front {
		if a > bound && b > bound {
			return true
		}
	} else if a < -bound && b < -bound {
		return true
	}
	for i := 0; i < cs.nx; i++ {
		for j := 0; j < cs.ny; j++ {
			base := pl.n[0]*cs.xs[i] + pl.n[1]*cs.ys[j] - pl.dist
			a := base + pl.n[2]*z1
			b := base + pl.n[2]*z2
			if front {
				if a < setMargin || b < setMargin {
					return false
				}
			} else if a > -setMargin || b > -setMargin {
				return false
			}
		}
	}
	return true
}

// recurse is hullTrace.recurse with the set proof at every node.
func (st *setTrace) recurse(num int32, p1f, p2f float32, p1, p2 [3]float32) int {
	for {
		if num < 0 {
			st.leafcount++
			if num == contentsSolid {
				if st.leafcount == 1 {
					st.startsolid = true
				}
				return trSolid
			}
			if num == contentsEmpty {
				st.inopen = true
			} else {
				st.inwater = true
			}
			return trEmpty
		}

		node := st.h.nodes[num]
		pl := &st.h.planes[node.plane]
		var t1, t2 float32
		if pl.typ < 3 {
			t1 = p1[pl.typ] - pl.dist
			t2 = p2[pl.typ] - pl.dist
		} else {
			t1 = pl.n[0]*p1[0] + pl.n[1]*p1[1] + pl.n[2]*p1[2] - pl.dist
			t2 = pl.n[0]*p2[0] + pl.n[1]*p2[1] + pl.n[2]*p2[2] - pl.dist
		}

		if t1 >= 0 && t2 >= 0 {
			if !st.sameSide(pl, p1[2], p2[2], true) {
				st.abortPl = pl
				return trAbort
			}
			num = node.children[0]
			continue
		}
		if t1 < 0 && t2 < 0 {
			if !st.sameSide(pl, p1[2], p2[2], false) {
				st.abortPl = pl
				return trAbort
			}
			num = node.children[1]
			continue
		}
		// Only a horizontal plane splits every member at the same
		// fraction; a single column straddles anything.
		if pl.typ != 2 && (st.cs.nx > 1 || st.cs.ny > 1) {
			st.abortPl, st.straddle = pl, true
			return trAbort
		}

		frac := clampFrac(t1 / (t1 - t2))
		midf := p1f + (p2f-p1f)*frac
		var mid [3]float32
		for i := 0; i < 3; i++ {
			mid[i] = p1[i] + frac*(p2[i]-p1[i])
		}
		nearside := int32(0)
		if t1 < t2 {
			nearside = 1
		}
		check := st.recurse(node.children[nearside], p1f, midf, p1, mid)
		if check == trBlocked || check == trAbort {
			return check
		}
		if check == trSolid && (st.inopen || st.inwater) {
			return check
		}
		oldcheck := check
		check = st.recurse(node.children[1-nearside], midf, p2f, mid, p2)
		if check == trEmpty || check == trBlocked || check == trAbort {
			return check
		}
		if oldcheck != trEmpty {
			return check
		}
		if t1 < t2 {
			frac = clampFrac((t1 + distEpsilon) / (t1 - t2))
		} else {
			frac = clampFrac((t1 - distEpsilon) / (t1 - t2))
		}
		midf = p1f + (p2f-p1f)*frac
		for i := 0; i < 3; i++ {
			mid[i] = p1[i] + frac*(p2[i]-p1[i])
		}
		st.fraction = midf
		st.endpos = mid
		return trBlocked
	}
}

// traceSet is floorTrace for the set's traced column. A non-nil abort
// means the proof failed at that plane and the set must be split.
func (h *Hull) traceSet(cs *colSet, z float32) (floorZ float32, ok, startsolid bool, abort *plane, straddle bool) {
	bottom := h.minZ - traceMargin
	if bottom >= z {
		return 0, false, false, nil, false
	}
	start := [3]float32{cs.xs[0], cs.ys[0], z}
	end := [3]float32{cs.xs[0], cs.ys[0], bottom}
	st := setTrace{hullTrace: hullTrace{h: h, fraction: 1, endpos: end}, cs: cs}
	res := st.recurse(h.root, 0, 1, start, end)
	if res == trAbort {
		return 0, false, false, st.abortPl, st.straddle
	}
	if res == trSolid || st.startsolid {
		return 0, false, true, nil, false
	}
	if st.fraction >= 1 {
		return 0, false, false, nil, false
	}
	return st.endpos[2] - PlayerFeetOffset, true, false, nil, false
}

// floorBelowSet is FloorBelow (start-solid retry included) over every
// column of cs, returning the highest floor. pass, when non-nil, limits
// which footprint columns count (the posed-mover column reject).
func (h *Hull) floorBelowSet(cs *colSet, z float32, pass *[3][3]bool) (best float32, any bool) {
	if h == nil || len(h.nodes) == 0 {
		return 0, false
	}
	fz, ok, ss, ab, strad := h.traceSet(cs, z)
	if ab == nil && !ok && ss {
		fz, ok, _, ab, strad = h.traceSet(cs, z+startSolidNudge)
	}
	if ab == nil {
		if !ok {
			return 0, false
		}
		if pass != nil && !cs.anyPass(pass) {
			return 0, false
		}
		return fz, true
	}

	var parts [9]colSet
	np := 0
	if !strad && (ab.typ == 0 || ab.typ == 1) {
		for side := 0; side < 2; side++ {
			p := *cs
			if ab.typ == 0 {
				p.nx = 0
				for k := 0; k < cs.nx; k++ {
					if (cs.xs[k]-ab.dist >= 0) == (side == 0) {
						p.xs[p.nx], p.xi[p.nx] = cs.xs[k], cs.xi[k]
						p.nx++
					}
				}
				if p.nx == 0 {
					continue
				}
			} else {
				p.ny = 0
				for k := 0; k < cs.ny; k++ {
					if (cs.ys[k]-ab.dist >= 0) == (side == 0) {
						p.ys[p.ny], p.yj[p.ny] = cs.ys[k], cs.yj[k]
						p.ny++
					}
				}
				if p.ny == 0 {
					continue
				}
			}
			parts[np] = p
			np++
		}
	}
	if np < 2 {
		np = 0
		for i := 0; i < cs.nx; i++ {
			for j := 0; j < cs.ny; j++ {
				parts[np] = colSet{
					xs: [3]float32{cs.xs[i]}, ys: [3]float32{cs.ys[j]},
					xi: [3]int8{cs.xi[i]}, yj: [3]int8{cs.yj[j]},
					nx: 1, ny: 1,
				}
				np++
			}
		}
	}
	for k := 0; k < np; k++ {
		if fz, ok := h.floorBelowSet(&parts[k], z, pass); ok && (!any || fz > best) {
			best, any = fz, true
		}
	}
	return best, any
}

func (cs *colSet) anyPass(pass *[3][3]bool) bool {
	for i := 0; i < cs.nx; i++ {
		for j := 0; j < cs.ny; j++ {
			if pass[cs.xi[i]][cs.yj[j]] {
				return true
			}
		}
	}
	return false
}
