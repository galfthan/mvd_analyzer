package bsp

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Entity is one parsed entry from the BSP entities lump — one
// `{ "classname" "..." ... }` block in the ASCII entities text. Every
// key/value pair is preserved verbatim in RawKeys; the frequently-used
// Classname, Origin, and Spawnflags fields are promoted for
// convenience.
type Entity struct {
	Classname  string
	Origin     [3]float32
	Spawnflags int
	RawKeys    map[string]string
}

// ReadEntities parses the entities lump out of a BSP file and returns
// the decoded entities in file order. Entity 0 is always worldspawn.
func ReadEntities(path string) ([]Entity, error) {
	raw, err := readEntitiesLump(path)
	if err != nil {
		return nil, err
	}
	return parseEntities(raw)
}

// ReadEntitiesBytes is the in-memory counterpart of ReadEntities —
// parses the entities block from a byte slice that already holds a
// BSP file's bytes. Used by tests.
func ReadEntitiesBytes(data []byte) ([]Entity, error) {
	raw, err := entitiesLumpBytes(data)
	if err != nil {
		return nil, err
	}
	return parseEntities(raw)
}

// ParseEntitiesText is exposed for tests that want to feed a raw
// entities block without wrapping it in a BSP header.
func ParseEntitiesText(text string) ([]Entity, error) {
	return parseEntities([]byte(text))
}

// parseEntities walks the NUL/newline-terminated ASCII block. The
// grammar is simple:
//
//	ents  := { entity }
//	entity := "{" { key value } "}"
//	key, value := quoted-string
//
// Keys and values are always double-quoted; whitespace between tokens
// is ignored. Embedded quotes inside values are uncommon in Quake maps
// and not handled (the format doesn't escape them either).
func parseEntities(data []byte) ([]Entity, error) {
	var out []Entity
	r := &entityReader{src: data}
	for {
		r.skipWS()
		if r.eof() {
			break
		}
		c := r.peek()
		if c != '{' {
			return nil, fmt.Errorf("bsp/entities: expected '{' at offset %d, got %q", r.pos, c)
		}
		r.next()
		ent := Entity{RawKeys: make(map[string]string)}
		for {
			r.skipWS()
			if r.eof() {
				return nil, fmt.Errorf("bsp/entities: unterminated entity at offset %d", r.pos)
			}
			if r.peek() == '}' {
				r.next()
				break
			}
			key, ok := r.readQuoted()
			if !ok {
				return nil, fmt.Errorf("bsp/entities: expected key at offset %d", r.pos)
			}
			r.skipWS()
			val, ok := r.readQuoted()
			if !ok {
				return nil, fmt.Errorf("bsp/entities: expected value for %q at offset %d", key, r.pos)
			}
			ent.RawKeys[key] = val
			switch key {
			case "classname":
				ent.Classname = val
			case "origin":
				if o, ok := parseOrigin(val); ok {
					ent.Origin = o
				}
			case "spawnflags":
				if n, err := strconv.Atoi(val); err == nil {
					ent.Spawnflags = n
				}
			}
		}
		out = append(out, ent)
	}
	return out, nil
}

func parseOrigin(s string) ([3]float32, bool) {
	var out [3]float32
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return out, false
	}
	for i, f := range fields {
		v, err := strconv.ParseFloat(f, 32)
		if err != nil {
			return out, false
		}
		out[i] = float32(v)
	}
	return out, true
}

type entityReader struct {
	src []byte
	pos int
}

func (r *entityReader) eof() bool  { return r.pos >= len(r.src) }
func (r *entityReader) peek() byte { return r.src[r.pos] }
func (r *entityReader) next() byte {
	c := r.src[r.pos]
	r.pos++
	return c
}

func (r *entityReader) skipWS() {
	for !r.eof() {
		c := r.src[r.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == 0 {
			r.pos++
			continue
		}
		// Quake tools sometimes leave // line comments in entities
		// blocks (rare, but e.g. mapper notes). Skip to EOL.
		if c == '/' && r.pos+1 < len(r.src) && r.src[r.pos+1] == '/' {
			for !r.eof() && r.src[r.pos] != '\n' {
				r.pos++
			}
			continue
		}
		return
	}
}

// readEntitiesLump opens a BSP file and returns the raw ASCII bytes
// of the entities lump. Mirrors the lump-directory logic in
// ParseBytes but doesn't require decoding the rest of the file.
func readEntitiesLump(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bsp/entities: read %s: %w", path, err)
	}
	return entitiesLumpBytes(data)
}

func entitiesLumpBytes(data []byte) ([]byte, error) {
	if len(data) < 4+numLumps*8 {
		return nil, fmt.Errorf("bsp/entities: file too short (%d bytes)", len(data))
	}
	// Reject formats we don't support for the geometry side too.
	magic := string(data[:4])
	version := int32(binary.LittleEndian.Uint32(data[0:4]))
	if magic != "BSP2" && magic != "2PSB" && version != Q1BSPVersion {
		if magic == "IBSP" {
			return nil, fmt.Errorf("bsp/entities: IBSP format not supported")
		}
		return nil, fmt.Errorf("bsp/entities: unsupported version %d", version)
	}
	base := 4 + lumpEntities*8
	offset := int32(binary.LittleEndian.Uint32(data[base : base+4]))
	length := int32(binary.LittleEndian.Uint32(data[base+4 : base+8]))
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("bsp/entities: lump has negative offset/length")
	}
	end := int64(offset) + int64(length)
	if end > int64(len(data)) {
		return nil, fmt.Errorf("bsp/entities: lump extends past EOF")
	}
	return data[offset:end], nil
}

func (r *entityReader) readQuoted() (string, bool) {
	if r.eof() || r.peek() != '"' {
		return "", false
	}
	r.next()
	start := r.pos
	for !r.eof() && r.src[r.pos] != '"' {
		r.pos++
	}
	if r.eof() {
		return "", false
	}
	s := string(r.src[start:r.pos])
	r.next() // consume closing quote
	return s, true
}
