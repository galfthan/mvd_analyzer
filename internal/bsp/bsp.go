// Package bsp implements a minimal parser for Quake 1 BSP v29 files.
//
// Only the lumps required for floor-geometry extraction by the sibling
// mapgeom package are decoded: vertices, edges, surfedges, faces, planes,
// and models. Anything else is skipped.
//
// Non-Q1 formats are rejected explicitly:
//   - version 30 (Half-Life) → error
//   - "BSP2" magic (extended BSP2) → error
//   - "IBSP" magic (Quake 2+) → error
package bsp

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// Q1BSPVersion is the only supported BSP file version.
const Q1BSPVersion = 29

// Parse reads a Quake 1 BSP v29 file from the given path and returns the
// decoded lumps we care about.
func Parse(path string) (*BSP, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bsp: read %s: %w", path, err)
	}
	return ParseBytes(data)
}

// ParseBytes decodes a BSP from an in-memory byte slice. Used by tests
// and by callers that already have the bytes.
func ParseBytes(data []byte) (*BSP, error) {
	if len(data) < 4+numLumps*8 {
		return nil, fmt.Errorf("bsp: file too short (%d bytes)", len(data))
	}

	// Magic / version sniffing. Q1 v29 starts with the little-endian
	// int32 29. Reject everything else with a specific error so bad
	// files never silently produce empty output.
	magic := string(data[:4])
	if magic == "BSP2" {
		return nil, fmt.Errorf("bsp: BSP2 format not supported (Q1 v29 only)")
	}
	if magic == "IBSP" {
		return nil, fmt.Errorf("bsp: IBSP format not supported (Q1 v29 only)")
	}

	version := int32(binary.LittleEndian.Uint32(data[0:4]))
	if version != Q1BSPVersion {
		return nil, fmt.Errorf("bsp: unsupported version %d (expected %d)", version, Q1BSPVersion)
	}

	// Read lump directory: 15 entries of (offset, length) int32.
	type dentry struct {
		offset int32
		length int32
	}
	var dirs [numLumps]dentry
	for i := 0; i < numLumps; i++ {
		base := 4 + i*8
		dirs[i].offset = int32(binary.LittleEndian.Uint32(data[base : base+4]))
		dirs[i].length = int32(binary.LittleEndian.Uint32(data[base+4 : base+8]))
	}

	lumpBytes := func(idx int) ([]byte, error) {
		d := dirs[idx]
		if d.offset < 0 || d.length < 0 {
			return nil, fmt.Errorf("bsp: lump %d has negative offset/length", idx)
		}
		end := int64(d.offset) + int64(d.length)
		if end > int64(len(data)) {
			return nil, fmt.Errorf("bsp: lump %d extends past EOF (%d > %d)", idx, end, len(data))
		}
		return data[d.offset:end], nil
	}

	bsp := &BSP{Version: version}

	// PLANES
	planeBytes, err := lumpBytes(lumpPlanes)
	if err != nil {
		return nil, err
	}
	if len(planeBytes)%planeSize != 0 {
		return nil, fmt.Errorf("bsp: planes lump size %d not a multiple of %d", len(planeBytes), planeSize)
	}
	bsp.Planes = make([]Plane, len(planeBytes)/planeSize)
	for i := range bsp.Planes {
		b := planeBytes[i*planeSize:]
		bsp.Planes[i] = Plane{
			Normal: Vec3{
				X: readF32(b[0:4]),
				Y: readF32(b[4:8]),
				Z: readF32(b[8:12]),
			},
			Dist: readF32(b[12:16]),
			Type: int32(binary.LittleEndian.Uint32(b[16:20])),
		}
	}

	// VERTEXES
	vertBytes, err := lumpBytes(lumpVertexes)
	if err != nil {
		return nil, err
	}
	if len(vertBytes)%vertexSize != 0 {
		return nil, fmt.Errorf("bsp: vertexes lump size %d not a multiple of %d", len(vertBytes), vertexSize)
	}
	bsp.Vertices = make([]Vec3, len(vertBytes)/vertexSize)
	for i := range bsp.Vertices {
		b := vertBytes[i*vertexSize:]
		bsp.Vertices[i] = Vec3{
			X: readF32(b[0:4]),
			Y: readF32(b[4:8]),
			Z: readF32(b[8:12]),
		}
	}

	// FACES
	faceBytes, err := lumpBytes(lumpFaces)
	if err != nil {
		return nil, err
	}
	if len(faceBytes)%faceSize != 0 {
		return nil, fmt.Errorf("bsp: faces lump size %d not a multiple of %d", len(faceBytes), faceSize)
	}
	bsp.Faces = make([]Face, len(faceBytes)/faceSize)
	for i := range bsp.Faces {
		b := faceBytes[i*faceSize:]
		f := Face{
			PlaneID:   binary.LittleEndian.Uint16(b[0:2]),
			Side:      binary.LittleEndian.Uint16(b[2:4]),
			FirstEdge: int32(binary.LittleEndian.Uint32(b[4:8])),
			NumEdges:  binary.LittleEndian.Uint16(b[8:10]),
			TexinfoID: binary.LittleEndian.Uint16(b[10:12]),
			LightOfs:  int32(binary.LittleEndian.Uint32(b[16:20])),
		}
		copy(f.Styles[:], b[12:16])
		bsp.Faces[i] = f
	}

	// EDGES
	edgeBytes, err := lumpBytes(lumpEdges)
	if err != nil {
		return nil, err
	}
	if len(edgeBytes)%edgeSize != 0 {
		return nil, fmt.Errorf("bsp: edges lump size %d not a multiple of %d", len(edgeBytes), edgeSize)
	}
	bsp.Edges = make([]Edge, len(edgeBytes)/edgeSize)
	for i := range bsp.Edges {
		b := edgeBytes[i*edgeSize:]
		bsp.Edges[i] = Edge{V: [2]uint16{
			binary.LittleEndian.Uint16(b[0:2]),
			binary.LittleEndian.Uint16(b[2:4]),
		}}
	}

	// SURFEDGES
	seBytes, err := lumpBytes(lumpSurfedges)
	if err != nil {
		return nil, err
	}
	if len(seBytes)%surfedgeSize != 0 {
		return nil, fmt.Errorf("bsp: surfedges lump size %d not a multiple of %d", len(seBytes), surfedgeSize)
	}
	bsp.Surfedges = make([]int32, len(seBytes)/surfedgeSize)
	for i := range bsp.Surfedges {
		bsp.Surfedges[i] = int32(binary.LittleEndian.Uint32(seBytes[i*surfedgeSize:]))
	}

	// MODELS
	modelBytes, err := lumpBytes(lumpModels)
	if err != nil {
		return nil, err
	}
	if len(modelBytes)%modelSize != 0 {
		return nil, fmt.Errorf("bsp: models lump size %d not a multiple of %d", len(modelBytes), modelSize)
	}
	bsp.Models = make([]Model, len(modelBytes)/modelSize)
	for i := range bsp.Models {
		b := modelBytes[i*modelSize:]
		m := Model{
			Mins:     Vec3{X: readF32(b[0:4]), Y: readF32(b[4:8]), Z: readF32(b[8:12])},
			Maxs:     Vec3{X: readF32(b[12:16]), Y: readF32(b[16:20]), Z: readF32(b[20:24])},
			Origin:   Vec3{X: readF32(b[24:28]), Y: readF32(b[28:32]), Z: readF32(b[32:36])},
			VisLeafs: int32(binary.LittleEndian.Uint32(b[52:56])),
			FirstFace: int32(binary.LittleEndian.Uint32(b[56:60])),
			NumFaces:  int32(binary.LittleEndian.Uint32(b[60:64])),
		}
		for j := 0; j < 4; j++ {
			m.HeadNodes[j] = int32(binary.LittleEndian.Uint32(b[36+j*4 : 40+j*4]))
		}
		bsp.Models[i] = m
	}

	return bsp, nil
}

// readF32 decodes a little-endian IEEE-754 float32.
func readF32(b []byte) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}

// Assert io.Reader-friendly path for completeness (currently unused but
// keeps the package honest — ParseBytes is the workhorse).
var _ = io.EOF
