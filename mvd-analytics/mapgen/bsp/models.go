package bsp

import (
	"fmt"
	"os"
)

// ModelBounds is the axis-aligned bounding box of one BSP submodel. A
// brush entity references a submodel via its `model "*N"` key, where N
// indexes into the slice returned by ReadModelBounds (model 0 is
// worldspawn). The box centre is a reasonable anchor point for a brush
// entity (button, teleport trigger, door); the box itself is the
// trigger volume.
type ModelBounds struct {
	Mins Vec3
	Maxs Vec3
}

// Center returns the midpoint of the bounding box.
func (m ModelBounds) Center() Vec3 {
	return Vec3{
		X: (m.Mins.X + m.Maxs.X) / 2,
		Y: (m.Mins.Y + m.Maxs.Y) / 2,
		Z: (m.Mins.Z + m.Maxs.Z) / 2,
	}
}

// ReadModelBounds reads only the models lump and returns each submodel's
// bounding box, in submodel order. Lightweight (no geometry decode) and
// works for Q1/HL/BSP2 alike, so it can place brush entities even on the
// HL v30 maps the full parser rejects.
func ReadModelBounds(path string) ([]ModelBounds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bsp/models: read %s: %w", path, err)
	}
	return ReadModelBoundsBytes(data)
}

// ReadModelBoundsBytes is the in-memory counterpart of ReadModelBounds.
func ReadModelBoundsBytes(data []byte) ([]ModelBounds, error) {
	raw, err := lumpBytes(data, lumpModels)
	if err != nil {
		return nil, err
	}
	n := len(raw) / modelSize
	out := make([]ModelBounds, n)
	for i := 0; i < n; i++ {
		b := raw[i*modelSize:]
		out[i] = ModelBounds{
			Mins: Vec3{X: readF32(b[0:4]), Y: readF32(b[4:8]), Z: readF32(b[8:12])},
			Maxs: Vec3{X: readF32(b[12:16]), Y: readF32(b[16:20]), Z: readF32(b[20:24])},
		}
	}
	return out, nil
}
