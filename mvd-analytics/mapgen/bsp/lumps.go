package bsp

// Q1 BSP v29 lump indices. The header has 15 dentries immediately after
// the 4-byte version field; each dentry is (offset int32, length int32).
const (
	lumpEntities  = 0
	lumpPlanes    = 1
	lumpMiptex    = 2
	lumpVertexes  = 3
	_             = 4 // VISIBILITY (unused)
	lumpNodes     = 5
	lumpTexinfo   = 6
	lumpFaces     = 7
	_             = 8 // LIGHTING (unused)
	lumpClipnodes = 9
	lumpLeafs     = 10
	_             = 11 // MARKSURFACES (unused)
	lumpEdges     = 12
	lumpSurfedges = 13
	lumpModels    = 14
	numLumps      = 15
)

// On-disk sizes for fixed-width records.
const (
	planeSize    = 20 // normal(12) + dist(4) + type(4)
	vertexSize   = 12 // 3 * float32
	faceSize     = 20 // v29 dface_t: planeId(2)+side(2)+firstEdge(4)+numEdges(2)+texinfoId(2)+styles(4)+lightofs(4)
	faceSize29a  = 28 // BSP2/29a dface29a_t: 5×int32 + styles(4) + lightofs(4)
	edgeSize     = 4  // v29: 2 * uint16
	edgeSize29a  = 8  // BSP2/29a: 2 * uint32
	surfedgeSize = 4  // int32
	modelSize    = 64 // mins(12) + maxs(12) + origin(12) + headnodes(16) + visleafs(4) + firstFace(4) + numFaces(4)

	texinfoSize   = 40 // texinfo_t: vecs[2][4] float32 (32) + miptex int32 (4) + flags int32 (4); miptex at offset 32. Same width on v29 and BSP2.
	miptexNameLen = 16 // miptex_t.name[16] — NUL-padded texture name at the start of each miptex_t

	clipnodeSize    = 8  // v29 dclipnode_t: planenum(4) + 2×int16
	clipnodeSize29a = 12 // BSP2/2PSB dclipnode29a_t: planenum(4) + 2×int32

	nodeSize     = 24 // v29 dnode_t: planenum(4) + 2×int16 children + 6×int16 bbox + firstface/numfaces 2×uint16
	nodeSize2PSB = 32 // 2PSB: planenum(4) + 2×int32 children + 6×int16 bbox + 2×uint32
	nodeSizeBSP2 = 44 // BSP2: planenum(4) + 2×int32 children + 6×float32 bbox + 2×uint32
	leafSize     = 28 // v29 dleaf_t: contents(4) + visofs(4) + 6×int16 bbox + 2×uint16 marksurfaces + ambient(4)
	leafSize2PSB = 32 // 2PSB: contents + visofs + 6×int16 bbox + 2×uint32 + ambient
	leafSizeBSP2 = 44 // BSP2: contents + visofs + 6×float32 bbox + 2×uint32 + ambient
)
