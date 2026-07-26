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
// 4on4) and which JSON decodes 40x slower. Everything else is 1.75 MB of
// JSON: 11 ms to write, 48 ms to read, against gob's 158 ms for the whole
// Result.
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
// binary predating this codec) to DecodeCache. democache also keys the
// tier-2 path on resultCacheFormat, so this is belt-and-braces: a stale
// file is a miss, never a silent half-decode.
var cacheMagic = [4]byte{'M', 'V', 'D', 'C'}

// streamsBox wraps the one gob-encoded section so a nil Streams (a
// degraded parse) is encodable — gob rejects a nil pointer passed
// directly to Encode, but omits a nil struct field happily.
type streamsBox struct{ S *Streams }

// EncodeCache serializes a Result for the tier-2 cache.
//
// Layout: magic[4] | jsonLen uint32 (little-endian) | JSON | gob(Streams).
// The gob streams straight into the same buffer rather than through an
// intermediate []byte, so the 30 MB of stream data is not copied twice.
func EncodeCache(r *Result) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("encode cache: nil result")
	}

	// Shallow copy so the caller's Result is never mutated — this runs on
	// the live Result the LRU is still handing to concurrent requests.
	rest := *r
	rest.Streams = nil
	restJSON, err := json.Marshal(&rest)
	if err != nil {
		return nil, fmt.Errorf("encode cache json: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(cacheMagic[:])
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(restJSON)))
	buf.Write(lenBuf[:])
	buf.Write(restJSON)
	// Boxed: gob refuses a nil pointer at top level, and Streams is nil on
	// a degraded parse. Inside a struct gob simply omits the field, which
	// decodes back to nil — the right answer for a section that genuinely
	// was not produced.
	if err := gob.NewEncoder(&buf).Encode(streamsBox{S: r.Streams}); err != nil {
		return nil, fmt.Errorf("encode cache streams: %w", err)
	}
	return buf.Bytes(), nil
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
	jsonLen := int(binary.LittleEndian.Uint32(rest[:4]))
	rest = rest[4:]
	if jsonLen > len(rest) {
		return nil, fmt.Errorf("decode cache: json length %d exceeds payload %d", jsonLen, len(rest))
	}
	jsonBytes, gobBytes := rest[:jsonLen], rest[jsonLen:]

	var r Result
	if err := json.Unmarshal(jsonBytes, &r); err != nil {
		return nil, fmt.Errorf("decode cache json: %w", err)
	}
	// Streams is nil in the JSON half by construction; gob owns it.
	var box streamsBox
	if err := gob.NewDecoder(bytes.NewReader(gobBytes)).Decode(&box); err != nil {
		return nil, fmt.Errorf("decode cache streams: %w", err)
	}
	r.Streams = box.S
	return &r, nil
}
