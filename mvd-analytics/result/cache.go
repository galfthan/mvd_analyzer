package result

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"fmt"
)

// Binary cache encoding for a parsed Result — mvd-api's tier-2 on-disk
// store. Not a wire format: nothing outside the cache reads these bytes,
// and the layout may change whenever democache bumps resultCacheFormat.
//
// # Why this is not just gob
//
// encoding/gob FLATTENS POINTERS AND OMITS ZERO VALUES. A struct field
// holding a pointer to a zero SCALAR is not transmitted, and on decode
// the field is left nil. Two losses, both real in this schema:
//
//	Taken:   &0     -> nil   (pointer to a zero scalar)
//	X:       -0.0   -> 0.0   (negative zero compares equal to zero)
//
// (A pointer to an all-zero STRUCT does survive — gob transmits the
// pointee once it is reached. Measured, not assumed.)
//
// The first is not a rounding artifact, it is a change of meaning.
// This schema uses pointers precisely to separate "measured, and the
// answer was zero" from "not measurable" — see RESULT_SCHEMA.md, "a value
// that cannot be MEASURED stays absent rather than becoming a zero". A
// plain gob cache silently rewrites the first into the second, so the same
// demo answers differently depending on whether the request hit a cold
// parse or a warm cache: `damage.taken: 0` (they took no damage) became
// `damage.taken` absent (we could not tell), and
// `accuracy.byWeapon[].hits: 0` (fired, never hit) became "there was no
// damage stream to link fires against".
//
// # Why not a gob-safe scalar type instead
//
// A custom optional-int type would make the FIRST case safe by
// construction, but it does nothing for negative zero, and it would mean
// rewriting every read site of every optional field across the analyzer,
// the view layer and the API. Fixing the ENCODING covers both losses at
// once and touches no caller.
//
// # The layout
//
// JSON is the default, because JSON distinguishes 0 from absent and is
// the representation this schema is already contracted on (the golden
// corpus and the OpenAPI spec both pin it). gob is kept for exactly one
// section, Streams, which is 97% of the bytes (50.5 MB of 52.3 MB on a
// 4on4) and which JSON decodes 40x slower — and within it, the position
// columns that are nearly all of those bytes skip gob too, written raw
// after it (postrack_cache.go; ~8x cheaper both ways).
//
// The cost is ADDITIVE, not a replacement: the gob decode of Streams is
// unchanged, and the ~1.75 MB of JSON for everything else adds ~11 ms to
// a write and ~48 ms to a read on top of it. (The old bare gob read the
// whole Result in 158 ms; this codec does not make that number go away,
// it appends to it.) Both the time and the size premium are
// demo-dependent — measured 458 KB -> 788 KB on a 2on2 dm6 and 1.19 MB
// -> 2.16 MB with a 16.7 ms -> 52.5 ms decode on the 4on4
// defer_reconnect, so treat mvd-api/README.md's headline "+2.6% on disk"
// as one 4on4's figure rather than a ceiling.
//
// The failure mode is what makes this the right split. Adding an optional
// field anywhere outside Streams is automatically safe — the default is
// correct, and forgetting something costs milliseconds, not truth. Only
// Streams carries the constraint, and TestStreamsHasNoOptionalScalars
// enforces it on one section rather than asking a reader to audit twenty.
// (It checks pointer-to-scalar only, which is the whole exposure given
// the behaviour above; negative zero in Streams is theoretical, since
// wire coordinates decode as +0.)
//
// One deliberate imprecision: Share (result.Share) rounds to 4 decimals
// in MarshalJSON, so shares come back rounded. The invariant guaranteed
// here is that the SERVED BYTES are unchanged, not that the in-memory
// float32 is bit-identical — and since serving rounds through the same
// function, round(round(x)) == round(x) makes the two indistinguishable
// to every consumer. TestCacheRoundTripPreservesServedBytes is the proof.

// cacheMagic guards against feeding a bare gob (a cache written by a
// binary predating this codec), or an earlier layout of it, to
// DecodeCache. democache also keys the tier-2 path on resultCacheFormat,
// so this is belt-and-braces: a stale file is a miss, never a silent
// half-decode.
var cacheMagic = [4]byte{'M', 'V', 'D', '2'}

// streamsBox wraps the one gob-encoded section so a nil Streams (a
// degraded parse) is encodable — gob rejects a nil pointer passed
// directly to Encode, but omits a nil struct field happily.
type streamsBox struct{ S *Streams }

// EncodeCache serializes a Result for the tier-2 cache.
//
// Layout: magic[4] | jsonLen uint32 | JSON | gobLen uint32 | gob(Streams
// without positions) | per player: hasPos byte, then the position columns
// (postrack_cache.go). Lengths are little-endian. The buffer is sized once
// up front, so the ~30 MB of columns are written in place, never regrown.
func EncodeCache(r *Result) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("encode cache: nil result")
	}

	// Shallow copies, never writes: the caller's Result is left exactly as
	// it was (democache encodes before publishing it to the LRU, but a
	// cache codec has no business mutating what it serializes).
	rest := *r
	rest.Streams = nil
	restJSON, err := json.Marshal(&rest)
	if err != nil {
		return nil, fmt.Errorf("encode cache json: %w", err)
	}

	// Streams minus the position tracks, which follow raw.
	var streams *Streams
	posSize := 0
	if r.Streams != nil {
		s := *r.Streams
		s.Players = make([]PlayerStream, len(r.Streams.Players))
		for i, p := range r.Streams.Players {
			if p.Position != nil {
				posSize += positionTrackSize(p.Position)
			}
			p.Position = nil
			s.Players[i] = p
		}
		streams = &s
	}
	var gobBuf bytes.Buffer
	// Boxed: gob refuses a nil pointer at top level, and Streams is nil on
	// a degraded parse. Inside a struct gob simply omits the field, which
	// decodes back to nil — the right answer for a section that genuinely
	// was not produced.
	if err := gob.NewEncoder(&gobBuf).Encode(streamsBox{S: streams}); err != nil {
		return nil, fmt.Errorf("encode cache streams: %w", err)
	}

	out := make([]byte, 0, len(cacheMagic)+8+len(restJSON)+gobBuf.Len()+posSize+len(streams.playersOrNil()))
	out = append(out, cacheMagic[:]...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(restJSON)))
	out = append(out, restJSON...)
	out = binary.LittleEndian.AppendUint32(out, uint32(gobBuf.Len()))
	out = append(out, gobBuf.Bytes()...)
	if r.Streams != nil {
		for i := range r.Streams.Players {
			p := r.Streams.Players[i].Position
			if p == nil {
				out = append(out, 0)
				continue
			}
			out = append(out, 1)
			out = appendPositionTrack(out, p)
		}
	}
	return out, nil
}

func (s *Streams) playersOrNil() []PlayerStream {
	if s == nil {
		return nil
	}
	return s.Players
}

// DecodeCache is EncodeCache's inverse.
func DecodeCache(data []byte) (*Result, error) {
	if len(data) < len(cacheMagic)+4 {
		return nil, fmt.Errorf("decode cache: short payload (%d bytes)", len(data))
	}
	if !bytes.Equal(data[:len(cacheMagic)], cacheMagic[:]) {
		return nil, fmt.Errorf("decode cache: bad magic (stale or foreign cache file)")
	}
	rest := data[len(cacheMagic):]
	jsonBytes, rest, err := cacheSection(rest, "json")
	if err != nil {
		return nil, err
	}
	gobBytes, posBytes, err := cacheSection(rest, "streams")
	if err != nil {
		return nil, err
	}

	var r Result
	if err := json.Unmarshal(jsonBytes, &r); err != nil {
		return nil, fmt.Errorf("decode cache json: %w", err)
	}
	// Streams is nil in the JSON half by construction; gob owns it.
	var box streamsBox
	if err := gob.NewDecoder(bytes.NewReader(gobBytes)).Decode(&box); err != nil {
		return nil, fmt.Errorf("decode cache streams: %w", err)
	}
	cr := colReader{b: posBytes}
	for i := range box.S.playersOrNil() {
		if len(cr.b) == 0 {
			return nil, fmt.Errorf("decode cache positions: truncated at player %d", i)
		}
		has := cr.b[0]
		cr.b = cr.b[1:]
		if has != 0 {
			box.S.Players[i].Position = readPositionTrack(&cr)
		}
	}
	if cr.bad || len(cr.b) != 0 {
		return nil, fmt.Errorf("decode cache positions: malformed")
	}
	r.Streams = box.S
	return &r, nil
}

// cacheSection splits a uint32-length-prefixed section off data.
func cacheSection(data []byte, name string) (section, rest []byte, err error) {
	if len(data) < 4 {
		return nil, nil, fmt.Errorf("decode cache: short payload before %s", name)
	}
	n := binary.LittleEndian.Uint32(data)
	data = data[4:]
	if uint64(n) > uint64(len(data)) {
		return nil, nil, fmt.Errorf("decode cache: %s length %d exceeds payload %d", name, n, len(data))
	}
	return data[:n], data[n:], nil
}
