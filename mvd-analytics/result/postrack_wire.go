package result

import (
	"encoding/binary"
	"math"
)

// The position pack is the binary wire form of every player's native-rate
// position track — the browser's alternative to the `pos` JSON. The tracks
// are ~92% of a full Result's JSON (45 of 49 MB on a 4on4), and in the
// WASM build marshalling them is ~7 s of a ~20 s analysis, before the page
// spends more parsing the text back. Raw columns cost a copy each way.
//
// Two producers write the same bytes: the WASM entry point (handed to the
// page as a transferable buffer next to the JSON, which then omits `pos`)
// and mvd-api's GET /v1/demos/{id}/streams/positions. One decoder reads
// both — mvd-web/src/lib/postrack.js, which views the columns in place as
// typed arrays rather than copying them.
//
// Layout, all integers little-endian uint32 unless noted, every column
// starting on a 4-byte boundary so a typed array can view it in place:
//
//	magic "MVDP" | version (1) | player count
//	per player, in Streams.Players order:
//	  name length | name UTF-8 bytes | pad to 4
//	  has-track (0: no track, nothing follows for this player; 1: a track)
//	  12 columns, each: length | elements | pad to 4
//	    t int32, x y z float32, li int16, h float32, lq int8,
//	    vp vya int16, vx vy vz float32
//
// Values are the in-memory ones, bit for bit: the JSON's 3-decimal
// rounding (coord.go) is a text-size measure that a binary form doesn't
// need. The column order is the tier-2 codec's (postrack_cache.go); a new
// column is appended here too (checklist site 9, result/coord.go) and
// bumps PositionPackVersion, which the decoder checks.

// PositionPackVersion is the layout version written after the magic.
const PositionPackVersion = 1

var positionPackMagic = [4]byte{'M', 'V', 'D', 'P'}

// PositionPackContentType is the media type mvd-api serves the pack as.
const PositionPackContentType = "application/vnd.mvd-analyzer.positions"

// WithoutPositions returns a shallow copy of s whose players carry no
// position track — what goes beside a pack (or the tier-2 raw columns) so
// the tracks are not encoded twice. s itself is not modified.
func (s *Streams) WithoutPositions() *Streams {
	if s == nil {
		return nil
	}
	c := *s
	c.Players = make([]PlayerStream, len(s.Players))
	for i, p := range s.Players {
		p.Position = nil
		c.Players[i] = p
	}
	return &c
}

func pad4(n int) int { return (4 - n%4) % 4 }

// positionPackSize is the exact encoded size, so the buffer is allocated
// once and never regrown.
func positionPackSize(s *Streams) int {
	n := 12
	for i := range s.playersOrNil() {
		p := &s.Players[i]
		n += 4 + len(p.Name) + pad4(len(p.Name)) + 4
		if t := p.Position; t != nil {
			n += 12 * 4
			n += len(t.T)*4 + (len(t.X)+len(t.Y)+len(t.Z)+len(t.H)+len(t.VX)+len(t.VY)+len(t.VZ))*4
			for _, k := range []int{len(t.Li), len(t.VP), len(t.VYa)} {
				n += k*2 + pad4(k*2)
			}
			n += len(t.Lq) + pad4(len(t.Lq))
		}
	}
	return n
}

// EncodePositionPack writes the position pack for s (nil: no players).
func EncodePositionPack(s *Streams) []byte {
	b := make([]byte, 0, positionPackSize(s))
	b = append(b, positionPackMagic[:]...)
	b = binary.LittleEndian.AppendUint32(b, PositionPackVersion)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s.playersOrNil())))
	for i := range s.playersOrNil() {
		p := &s.Players[i]
		b = binary.LittleEndian.AppendUint32(b, uint32(len(p.Name)))
		b = append(b, p.Name...)
		b = appendPad4(b)
		t := p.Position
		if t == nil {
			b = binary.LittleEndian.AppendUint32(b, 0)
			continue
		}
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = packI32s(b, t.T)
		b = packF32s(b, t.X)
		b = packF32s(b, t.Y)
		b = packF32s(b, t.Z)
		b = packI16s(b, t.Li)
		b = packF32s(b, t.H)
		b = packI8s(b, t.Lq)
		b = packI16s(b, t.VP)
		b = packI16s(b, t.VYa)
		b = packF32s(b, t.VX)
		b = packF32s(b, t.VY)
		b = packF32s(b, t.VZ)
	}
	return b
}

func appendPad4(b []byte) []byte {
	for range pad4(len(b)) {
		b = append(b, 0)
	}
	return b
}

func packI32s(b []byte, s []int32) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint32(b, uint32(v))
	}
	return b
}

func packF32s(b []byte, s []float32) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func packI16s(b []byte, s []int16) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return appendPad4(b)
}

func packI8s(b []byte, s []int8) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	for _, v := range s {
		b = append(b, byte(v))
	}
	return appendPad4(b)
}
