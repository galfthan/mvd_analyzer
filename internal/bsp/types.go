package bsp

// Q1 BSP v29 structures. Only the lumps required for floor-geometry
// extraction are modeled; the rest are skipped intentionally.

// Vec3 is a 3D vector in world units (matches BSP/.loc/player scale).
type Vec3 struct {
	X, Y, Z float32
}

// Plane is the BSP plane lump record (20 bytes on disk).
type Plane struct {
	Normal Vec3
	Dist   float32
	Type   int32
}

// Edge is the BSP edge lump record (4 bytes on disk): two uint16 vertex
// indices into the vertex lump.
type Edge struct {
	V [2]uint16
}

// Face is the BSP face lump record (20 bytes on disk).
//
// Field layout on disk (Q1 v29):
//
//	planeId    uint16
//	side       uint16
//	firstEdge  int32   // offset into SURFEDGES
//	numEdges   uint16
//	texinfoId  uint16
//	styles     [4]byte
//	lightofs   int32
type Face struct {
	PlaneID   uint16
	Side      uint16
	FirstEdge int32
	NumEdges  uint16
	TexinfoID uint16
	Styles    [4]byte
	LightOfs  int32
}

// Model is the BSP model lump record. We only need worldspawn (model 0)
// so we keep the bounding info trimmed but still parse the full 64-byte
// record to advance the cursor correctly.
type Model struct {
	Mins      Vec3
	Maxs      Vec3
	Origin    Vec3
	HeadNodes [4]int32
	VisLeafs  int32
	FirstFace int32
	NumFaces  int32
}

// BSP holds the decoded lumps we care about.
type BSP struct {
	Version   int32
	Planes    []Plane
	Vertices  []Vec3
	Faces     []Face
	Edges     []Edge
	Surfedges []int32
	Models    []Model
}
