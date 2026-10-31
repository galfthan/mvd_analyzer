package view

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The bucket pack is the binary wire form of a ColumnarBuckets — the
// browser's alternative to its JSON. The WASM build's deferred 50 ms view
// of a 4on4 is ~12 MB of JSON whose marshal is most of what that call
// costs in WASM, and the page parses it back as boxed numbers. The pack
// carries the columns raw, so the page views them in place as typed arrays
// (mvd-web/src/lib/bucketpack.js, the one decoder).
//
// A ColumnarBuckets has no fixed column set — which columns a player has,
// and their element types, follow the requested fields and reducers — so
// the pack is self-describing: a small JSON header with the view's shape
// and, in place of each column, a descriptor of where its elements sit in
// the binary body.
//
//	magic "MVDB" | version uint32 | header length uint32 | header JSON
//	pad to 8 | body
//
// The header is the ColumnarBuckets JSON (timeUnit, windowMs, start, count,
// partialLastMs, locTable; per player first, n, validFrom; per team the
// counter keys and abt) with every column — including each player's
// "alive" mask — replaced by {"t": type, "o": byte offset into the body,
// "n": element count}. Each column starts on an 8-byte boundary of the
// body, which itself starts on one, so even a float64 column can be
// viewed in place. Integers are little-endian. Types:
//
//	i16 int16, i32 int32, f32 float32, f64 float64,
//	u8  bool as 0/1 (the JSON's 0/1 too),
//	s16 uint16 index into the header's "strings" table (armorType)
//
// Team counters are []int in Go and i32 on the wire. Float columns carry the
// in-memory values bit for bit; the JSON's 3-decimal rounding is a
// text-size measure.

// ColumnarPackVersion is the layout version written after the magic.
const ColumnarPackVersion = 1

// ColumnarPackContentType is the media type mvd-api serves the pack as
// (an Accept-negotiated form of GET /v1/demos/{id}/buckets).
const ColumnarPackContentType = "application/vnd.mvd-analyzer.buckets"

var columnarPackMagic = [4]byte{'M', 'V', 'D', 'B'}

type packCol struct {
	T string `json:"t"`
	O int    `json:"o"`
	N int    `json:"n"`
}

type packPlayer struct {
	First     int                `json:"first"`
	N         int                `json:"n"`
	Alive     packCol            `json:"alive"`
	ValidFrom map[string]int     `json:"validFrom,omitempty"`
	Cols      map[string]packCol `json:"cols"`
}

type packTeam struct {
	Cols map[string]packCol `json:"cols"`
	ABT  map[string]packCol `json:"abt,omitempty"`
}

type packHeader struct {
	TimeUnit      TimeUnit               `json:"timeUnit,omitempty"`
	WindowMs      int                    `json:"windowMs"`
	Start         int32                  `json:"start"`
	Count         int                    `json:"count"`
	PartialLastMs int32                  `json:"partialLastMs,omitempty"`
	LocTable      []string               `json:"locTable,omitempty"`
	Strings       []string               `json:"strings,omitempty"`
	Players       map[string]*packPlayer `json:"players,omitempty"`
	Teams         map[string]*packTeam   `json:"teams,omitempty"`
}

// packWriter appends columns to the body, each on an 8-byte boundary.
type packWriter struct {
	body    []byte
	strings []string
	strIdx  map[string]int
}

func (w *packWriter) col(t string, n, elem int) (packCol, []byte) {
	for len(w.body)%8 != 0 {
		w.body = append(w.body, 0)
	}
	c := packCol{T: t, O: len(w.body), N: n}
	w.body = append(w.body, make([]byte, n*elem)...)
	return c, w.body[c.O:]
}

func (w *packWriter) str(s string) uint16 {
	i, ok := w.strIdx[s]
	if !ok {
		i = len(w.strings)
		w.strings = append(w.strings, s)
		w.strIdx[s] = i
	}
	return uint16(i)
}

func (w *packWriter) column(v any) (packCol, error) {
	le := binary.LittleEndian
	switch s := v.(type) {
	case []int16:
		c, b := w.col("i16", len(s), 2)
		for i, x := range s {
			le.PutUint16(b[2*i:], uint16(x))
		}
		return c, nil
	case []int:
		c, b := w.col("i32", len(s), 4)
		for i, x := range s {
			le.PutUint32(b[4*i:], uint32(int32(x)))
		}
		return c, nil
	case result.Coords:
		c, b := w.col("f32", len(s), 4)
		for i, x := range s {
			le.PutUint32(b[4*i:], math.Float32bits(x))
		}
		return c, nil
	case []float64:
		c, b := w.col("f64", len(s), 8)
		for i, x := range s {
			le.PutUint64(b[8*i:], math.Float64bits(x))
		}
		return c, nil
	case []bool:
		c, b := w.col("u8", len(s), 1)
		for i, x := range s {
			if x {
				b[i] = 1
			}
		}
		return c, nil
	case []string:
		c, b := w.col("s16", len(s), 2)
		for i, x := range s {
			le.PutUint16(b[2*i:], w.str(x))
		}
		return c, nil
	}
	return packCol{}, fmt.Errorf("bucket pack: no wire type for %T", v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EncodeColumnarPack writes cb as a bucket pack. Players, teams and
// columns are laid out in name order, so equal views give equal bytes.
func EncodeColumnarPack(cb *ColumnarBuckets) ([]byte, error) {
	w := &packWriter{strIdx: map[string]int{}}
	h := packHeader{TimeUnit: cb.TimeUnit, WindowMs: cb.WindowMs, Start: cb.Start, Count: cb.Count, PartialLastMs: cb.PartialLastMs, LocTable: cb.LocTable}
	if len(cb.Players) > 0 {
		h.Players = make(map[string]*packPlayer, len(cb.Players))
	}
	for _, name := range sortedKeys(cb.Players) {
		cp := cb.Players[name]
		pp := &packPlayer{First: cp.First, N: cp.N, ValidFrom: cp.ValidFrom, Cols: make(map[string]packCol, len(cp.Cols))}
		var err error
		if pp.Alive, err = w.column(cp.Alive); err != nil {
			return nil, err
		}
		for _, k := range sortedKeys(cp.Cols) {
			if pp.Cols[k], err = w.column(cp.Cols[k]); err != nil {
				return nil, err
			}
		}
		h.Players[name] = pp
	}
	if len(cb.Teams) > 0 {
		h.Teams = make(map[string]*packTeam, len(cb.Teams))
	}
	for _, name := range sortedKeys(cb.Teams) {
		ct := cb.Teams[name]
		pt := &packTeam{Cols: make(map[string]packCol, len(ct.Cols))}
		var err error
		for _, k := range sortedKeys(ct.Cols) {
			if pt.Cols[k], err = w.column(ct.Cols[k]); err != nil {
				return nil, err
			}
		}
		if len(ct.ABT) > 0 {
			pt.ABT = make(map[string]packCol, len(ct.ABT))
			for _, k := range sortedKeys(ct.ABT) {
				if pt.ABT[k], err = w.column(ct.ABT[k]); err != nil {
					return nil, err
				}
			}
		}
		h.Teams[name] = pt
	}
	h.Strings = w.strings
	hj, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	pre := 12 + len(hj)
	pad := (8 - pre%8) % 8
	out := make([]byte, 0, pre+pad+len(w.body))
	out = append(out, columnarPackMagic[:]...)
	out = binary.LittleEndian.AppendUint32(out, ColumnarPackVersion)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(hj)))
	out = append(out, hj...)
	out = append(out, make([]byte, pad)...)
	return append(out, w.body...), nil
}
