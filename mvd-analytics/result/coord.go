package result

import (
	"math"
	"strconv"
)

// coordJSONDecimals is the decimal precision used when SERIALIZING
// positions, velocities, and floor heights to JSON. The values are kept
// at native float32 resolution in memory — only the text artifact is
// rounded. Three decimals is lossless for the wire's eighth-unit
// coordinates (.125 / .25 / .375 …) yet sheds the false-precision tail a
// float32 division leaves on derived velocity (e.g. -58.333332 →
// -58.333) and on the floor-height trace.
const coordJSONDecimals = 3

// appendCoordJSON appends v as a JSON number rounded to coordJSONDecimals
// places, dropping trailing zeros (192 → "192", -58.333332 → "-58.333").
// Rounding happens in float64 then formats the shortest round-tripping
// decimal of the rounded value.
func appendCoordJSON(b []byte, v float32) []byte {
	r := math.Round(float64(v)*1e3) / 1e3
	return strconv.AppendFloat(b, r, 'f', -1, 64)
}

// Coord is a float32 carried at native resolution in Go but serialized to
// JSON at coordJSONDecimals precision (see appendCoordJSON). Use it for a
// single position / velocity / height value.
type Coord float32

// MarshalJSON renders the rounded text form.
func (c Coord) MarshalJSON() ([]byte, error) {
	return appendCoordJSON(nil, float32(c)), nil
}

// Coords is a []float32 carried at native resolution but serialized to
// JSON at coordJSONDecimals precision, in a single pass (cheaper than a
// per-element Coord for the dense native-rate columns). Conversion
// to/from []float32 is free (identical underlying type), so analyzers
// compute in plain float32 and convert once when filling the result.
type Coords []float32

// MarshalJSON renders the array with each element rounded.
func (cs Coords) MarshalJSON() ([]byte, error) {
	if cs == nil {
		return []byte("null"), nil
	}
	// ~7 chars/value + commas + brackets is a good upper-bound hint.
	b := make([]byte, 0, len(cs)*8+2)
	b = append(b, '[')
	for i, v := range cs {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendCoordJSON(b, v)
	}
	return append(b, ']'), nil
}
