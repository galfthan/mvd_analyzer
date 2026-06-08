package mapclip

import (
	"fmt"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// Build distils a parsed BSP into the worldspawn player clip hull (hull
// 1). It walks the CLIPNODES tree from model 0's HeadNodes[1], keeping
// only the nodes reachable from that root — which drops hull 2 and every
// brush-model submodel's clipnodes, leaving exactly the static world
// collision the floor trace needs (Approach A) — then renumbers the kept
// nodes and the planes they reference into dense local arrays.
//
// A worldspawn whose hull-1 root is a contents leaf (no collision tree)
// yields a Hull with no nodes; FloorBelow then always reports "no
// floor", which is the correct degenerate answer.
func Build(b *bsp.BSP) (*Hull, error) {
	if b == nil || len(b.Models) == 0 {
		return nil, fmt.Errorf("mapclip: bsp has no models")
	}
	world := b.Models[0]
	root := world.HeadNodes[1]
	h := &Hull{minZ: world.Mins.Z}

	if root < 0 {
		// No clip tree — the whole world is a single contents leaf.
		h.root = root
		return h, nil
	}

	// DFS from root, collecting reachable node indices. Guard against a
	// malformed tree pointing past the clipnode array.
	reachable := make(map[int32]bool)
	var order []int32
	stack := []int32{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n < 0 || reachable[n] {
			continue
		}
		if int(n) >= len(b.ClipNodes) {
			return nil, fmt.Errorf("mapclip: clipnode index %d out of range (%d nodes)", n, len(b.ClipNodes))
		}
		reachable[n] = true
		order = append(order, n)
		cn := b.ClipNodes[n]
		stack = append(stack, cn.Children[0], cn.Children[1])
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	nodeRemap := make(map[int32]int32, len(order))
	for i, old := range order {
		nodeRemap[old] = int32(i)
	}

	// Collect + renumber the planes those nodes reference.
	planeRemap := make(map[int32]int32)
	var planeOrder []int32
	for _, old := range order {
		pn := b.ClipNodes[old].PlaneNum
		if pn < 0 || int(pn) >= len(b.Planes) {
			return nil, fmt.Errorf("mapclip: plane index %d out of range (%d planes)", pn, len(b.Planes))
		}
		if _, ok := planeRemap[pn]; !ok {
			planeRemap[pn] = 0 // placeholder; assigned after sort
			planeOrder = append(planeOrder, pn)
		}
	}
	sort.Slice(planeOrder, func(i, j int) bool { return planeOrder[i] < planeOrder[j] })
	for i, old := range planeOrder {
		planeRemap[old] = int32(i)
	}

	h.planes = make([]plane, len(planeOrder))
	for i, old := range planeOrder {
		p := b.Planes[old]
		h.planes[i] = plane{
			n:    [3]float32{p.Normal.X, p.Normal.Y, p.Normal.Z},
			dist: p.Dist,
			typ:  p.Type,
		}
	}

	remapChild := func(c int32) int32 {
		if c < 0 {
			return c // contents leaf
		}
		return nodeRemap[c]
	}
	h.nodes = make([]clipNode, len(order))
	for i, old := range order {
		cn := b.ClipNodes[old]
		h.nodes[i] = clipNode{
			plane:    planeRemap[cn.PlaneNum],
			children: [2]int32{remapChild(cn.Children[0]), remapChild(cn.Children[1])},
		}
	}
	h.root = nodeRemap[root]
	return h, nil
}
