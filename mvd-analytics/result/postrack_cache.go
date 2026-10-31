package result

import (
	"encoding/binary"
	"math"
)

// The position columns travel outside gob in the tier-2 cache
// (EncodeCache): raw, little-endian, each prefixed by its length. They are
// ~97% of the Streams bytes (26.6 of 27.6 MB on a 4on4), and through gob —
// per-element varint coding, then the encoded bytes copied through nested
// encoder buffers — they cost a third of a second per cold parse to write
// and a quarter on every warm hit to read. Raw columns are bit-exact and an
// order of magnitude cheaper.
//
// A length-0 column decodes as nil, which is what gob did with it, so the
// served JSON is unchanged (TestCacheRoundTripPreservesServedBytes).
//
// The column order is the layout: adding a column means appending it to
// both lists below (checklist site 8, result/coord.go) and bumping
// democache's resultCacheFormat. TestPositionTrackCodecCoversEveryColumn
// fails on a column the codec does not carry.

// positionTrackSize is the encoded size of p, for sizing the buffer once.
func positionTrackSize(p *PositionTrack) int {
	return len(p.T)*4 + (len(p.X)+len(p.Y)+len(p.Z)+len(p.H)+len(p.VX)+len(p.VY)+len(p.VZ))*4 +
		(len(p.Li)+len(p.VP)+len(p.VYa))*2 + len(p.Lq) + 12*binary.MaxVarintLen64
}

func appendPositionTrack(b []byte, p *PositionTrack) []byte {
	b = appendI32s(b, p.T)
	b = appendF32s(b, p.X)
	b = appendF32s(b, p.Y)
	b = appendF32s(b, p.Z)
	b = appendI16s(b, p.Li)
	b = appendF32s(b, p.H)
	b = appendI8s(b, p.Lq)
	b = appendI16s(b, p.VP)
	b = appendI16s(b, p.VYa)
	b = appendF32s(b, p.VX)
	b = appendF32s(b, p.VY)
	b = appendF32s(b, p.VZ)
	return b
}

func readPositionTrack(r *colReader) *PositionTrack {
	var q PositionTrack
	q.T = readCol(r, 4, func(b []byte) int32 { return int32(binary.LittleEndian.Uint32(b)) })
	q.X = readCol(r, 4, readF32)
	q.Y = readCol(r, 4, readF32)
	q.Z = readCol(r, 4, readF32)
	q.Li = readCol(r, 2, readI16)
	q.H = readCol(r, 4, readF32)
	q.Lq = readCol(r, 1, func(b []byte) int8 { return int8(b[0]) })
	q.VP = readCol(r, 2, readI16)
	q.VYa = readCol(r, 2, readI16)
	q.VX = readCol(r, 4, readF32)
	q.VY = readCol(r, 4, readF32)
	q.VZ = readCol(r, 4, readF32)
	return &q
}

func appendI32s(b []byte, s []int32) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint32(b, uint32(v))
	}
	return b
}

func appendF32s(b []byte, s []float32) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func appendI16s(b []byte, s []int16) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	for _, v := range s {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}

func appendI8s(b []byte, s []int8) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	for _, v := range s {
		b = append(b, byte(v))
	}
	return b
}

func readF32(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }
func readI16(b []byte) int16   { return int16(binary.LittleEndian.Uint16(b)) }

// colReader walks the length-prefixed columns; bad latches on the first
// short or malformed column so a corrupt cache file is an error, never a
// panic or a giant allocation.
type colReader struct {
	b   []byte
	bad bool
}

func readCol[T any](r *colReader, size int, get func([]byte) T) []T {
	if r.bad {
		return nil
	}
	n, k := binary.Uvarint(r.b)
	if k <= 0 || n > uint64(len(r.b)-k)/uint64(size) {
		r.bad = true
		return nil
	}
	r.b = r.b[k:]
	if n == 0 {
		return nil
	}
	out := make([]T, n)
	for i := range out {
		out[i] = get(r.b[i*size:])
	}
	r.b = r.b[int(n)*size:]
	return out
}
