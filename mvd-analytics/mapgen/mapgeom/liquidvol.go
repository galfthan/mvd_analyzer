package mapgeom

import (
	"math"

	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// Hull-0 leaf contents codes for the three liquids (bspfile.h CONTENTS_*).
const (
	contentsWater = -3
	contentsSlime = -4
	contentsLava  = -5
)

// halfSpace is the closed half-space n·p <= d.
type halfSpace struct {
	n [3]float64
	d float64
}

// liquidVolumes returns the closed liquid volumes of worldspawn as triangle
// soups per kind ("water"/"slime"/"lava"), every triangle wound
// counter-clockwise seen from outside its cell. Each liquid leaf of the
// hull-0 tree is a convex cell: the intersection of the half-spaces on the
// path from the root down to it (bounded by the world box). Cells are
// emitted whole, so faces shared by two liquid cells appear twice with
// opposite windings — the viewer measures ray length through the volume as
// Σ back-face depth − Σ front-face depth, in which such pairs cancel.
//
// qbsp gives every '*' brush liquid contents, teleporter fields included,
// so a liquid leaf is only kept when it is connected — through cells whose
// bounding boxes touch — to a cell touching one of the visible liquid
// surfaces in `surfaces` (which already exclude teleporters). Returns nil
// when the BSP's render tree is missing.
func liquidVolumes(b *bsp.BSP, surfaces [][]bsp.Vec3, degen *int) map[string][]float32 {
	if len(b.Models) == 0 || len(b.Nodes) == 0 || len(b.Leafs) == 0 {
		return nil
	}
	m := b.Models[0]
	const pad = 16
	box := []halfSpace{
		{n: [3]float64{1, 0, 0}, d: float64(m.Maxs.X) + pad},
		{n: [3]float64{-1, 0, 0}, d: -float64(m.Mins.X) + pad},
		{n: [3]float64{0, 1, 0}, d: float64(m.Maxs.Y) + pad},
		{n: [3]float64{0, -1, 0}, d: -float64(m.Mins.Y) + pad},
		{n: [3]float64{0, 0, 1}, d: float64(m.Maxs.Z) + pad},
		{n: [3]float64{0, 0, -1}, d: -float64(m.Mins.Z) + pad},
	}
	extent := math.Hypot(math.Hypot(float64(m.Maxs.X-m.Mins.X), float64(m.Maxs.Y-m.Mins.Y)),
		float64(m.Maxs.Z-m.Mins.Z)) + 4*pad

	type cell struct {
		kind  string
		faces [][]bsp.Vec3
		lo    [3]float32
		hi    [3]float32
	}
	var cells []cell
	var walk func(child int32, path []halfSpace, depth int)
	walk = func(child int32, path []halfSpace, depth int) {
		if child < 0 {
			leaf := int(-child - 1)
			if leaf >= len(b.Leafs) {
				return
			}
			kind := ""
			switch b.Leafs[leaf].Contents {
			case contentsWater:
				kind = "water"
			case contentsSlime:
				kind = "slime"
			case contentsLava:
				kind = "lava"
			default:
				return
			}
			faces := convexCell(dedupePlanes(path), extent)
			if len(faces) == 0 {
				return
			}
			lo, hi := ringBounds(faces[0])
			for _, f := range faces[1:] {
				l, h := ringBounds(f)
				for k := 0; k < 3; k++ {
					lo[k] = min(lo[k], l[k])
					hi[k] = max(hi[k], h[k])
				}
			}
			cells = append(cells, cell{kind: kind, faces: faces, lo: lo, hi: hi})
			return
		}
		// A malformed tree could cycle; real trees are a few dozen deep.
		if int(child) >= len(b.Nodes) || depth > 512 {
			return
		}
		nd := b.Nodes[child]
		if int(nd.PlaneNum) < 0 || int(nd.PlaneNum) >= len(b.Planes) {
			return
		}
		pl := b.Planes[nd.PlaneNum]
		n := [3]float64{float64(pl.Normal.X), float64(pl.Normal.Y), float64(pl.Normal.Z)}
		d := float64(pl.Dist)
		// Children[0] is the front side (n·p >= d), Children[1] the back.
		front := append(path[:len(path):len(path)], halfSpace{n: [3]float64{-n[0], -n[1], -n[2]}, d: -d})
		walk(nd.Children[0], front, depth+1)
		back := append(path[:len(path):len(path)], halfSpace{n: n, d: d})
		walk(nd.Children[1], back, depth+1)
	}
	walk(m.HeadNodes[0], box, 0)

	// Flood from the cells a visible surface touches.
	const touch = 1
	overlaps := func(alo, ahi, blo, bhi [3]float32) bool {
		for k := 0; k < 3; k++ {
			if alo[k] > bhi[k]+touch || blo[k] > ahi[k]+touch {
				return false
			}
		}
		return true
	}
	keep := make([]bool, len(cells))
	var queue []int
	for _, s := range surfaces {
		slo, shi := ringBounds(s)
		for i, c := range cells {
			if !keep[i] && overlaps(c.lo, c.hi, slo, shi) {
				keep[i] = true
				queue = append(queue, i)
			}
		}
	}
	for len(queue) > 0 {
		a := cells[queue[0]]
		queue = queue[1:]
		for i, c := range cells {
			if !keep[i] && c.kind == a.kind && overlaps(c.lo, c.hi, a.lo, a.hi) {
				keep[i] = true
				queue = append(queue, i)
			}
		}
	}

	out := make(map[string][]float32)
	for i, c := range cells {
		if !keep[i] {
			continue
		}
		for _, ring := range c.faces {
			out[c.kind] = fanTriangulate(out[c.kind], ring, degen)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ringBounds is the axis-aligned bounding box of a ring.
func ringBounds(r []bsp.Vec3) (lo, hi [3]float32) {
	lo = [3]float32{r[0].X, r[0].Y, r[0].Z}
	hi = lo
	for _, v := range r[1:] {
		p := [3]float32{v.X, v.Y, v.Z}
		for k := 0; k < 3; k++ {
			lo[k] = min(lo[k], p[k])
			hi[k] = max(hi[k], p[k])
		}
	}
	return lo, hi
}

// dedupePlanes drops all but the tightest of any half-spaces sharing a
// normal: a duplicate plane would otherwise yield the same cell face twice.
func dedupePlanes(hs []halfSpace) []halfSpace {
	out := make([]halfSpace, 0, len(hs))
next:
	for _, h := range hs {
		for i := range out {
			o := &out[i]
			if math.Abs(o.n[0]-h.n[0]) < 1e-6 && math.Abs(o.n[1]-h.n[1]) < 1e-6 && math.Abs(o.n[2]-h.n[2]) < 1e-6 {
				if h.d < o.d {
					o.d = h.d
				}
				continue next
			}
		}
		out = append(out, h)
	}
	return out
}

// convexCell returns the face polygons of the convex polyhedron
// ∩ {n·p <= d}: for each plane, a square of half-size `extent` on it,
// clipped by every other half-space. Rings are wound counter-clockwise
// seen from outside (along +n). Planes that only touch the cell in an edge
// or a point, or not at all, yield no face.
func convexCell(hs []halfSpace, extent float64) [][]bsp.Vec3 {
	var faces [][]bsp.Vec3
	for i, h := range hs {
		poly := planeSquare(h, extent)
		for j, o := range hs {
			if j == i {
				continue
			}
			poly = clipPoly(poly, o)
			if len(poly) < 3 {
				break
			}
		}
		if len(poly) < 3 {
			continue
		}
		ring := make([]bsp.Vec3, len(poly))
		for k, p := range poly {
			ring[k] = bsp.Vec3{X: float32(p[0]), Y: float32(p[1]), Z: float32(p[2])}
		}
		faces = append(faces, ring)
	}
	return faces
}

// planeSquare is a square of half-size e centred on the plane's closest
// point to the origin, wound counter-clockwise about +n.
func planeSquare(h halfSpace, e float64) [][3]float64 {
	n := h.n
	// Any vector not parallel to n seeds the in-plane basis.
	a := [3]float64{0, 0, 1}
	if math.Abs(n[2]) > 0.9 {
		a = [3]float64{1, 0, 0}
	}
	u := norm3(cross3(a, n))
	v := cross3(n, u) // u × v = n, so (u, v) runs counter-clockwise about n
	c := [3]float64{n[0] * h.d, n[1] * h.d, n[2] * h.d}
	pt := func(su, sv float64) [3]float64 {
		return [3]float64{
			c[0] + e*(su*u[0]+sv*v[0]),
			c[1] + e*(su*u[1]+sv*v[1]),
			c[2] + e*(su*u[2]+sv*v[2]),
		}
	}
	return [][3]float64{pt(-1, -1), pt(1, -1), pt(1, 1), pt(-1, 1)}
}

// clipPoly keeps the part of convex polygon poly inside h (Sutherland–
// Hodgman against one plane). Winding is preserved.
func clipPoly(poly [][3]float64, h halfSpace) [][3]float64 {
	const eps = 1e-3
	dist := func(p [3]float64) float64 { return h.n[0]*p[0] + h.n[1]*p[1] + h.n[2]*p[2] - h.d }
	out := make([][3]float64, 0, len(poly)+1)
	for i, cur := range poly {
		prev := poly[(i+len(poly)-1)%len(poly)]
		dc, dp := dist(cur), dist(prev)
		cin, pin := dc <= eps, dp <= eps
		if cin != pin {
			t := dp / (dp - dc)
			out = append(out, [3]float64{
				prev[0] + t*(cur[0]-prev[0]),
				prev[1] + t*(cur[1]-prev[1]),
				prev[2] + t*(cur[2]-prev[2]),
			})
		}
		if cin {
			out = append(out, cur)
		}
	}
	return out
}

func cross3(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func norm3(a [3]float64) [3]float64 {
	l := math.Sqrt(a[0]*a[0] + a[1]*a[1] + a[2]*a[2])
	return [3]float64{a[0] / l, a[1] / l, a[2] / l}
}
