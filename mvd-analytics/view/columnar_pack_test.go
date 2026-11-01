package view

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

var updateBucketPackFixture = flag.Bool("update-bucket-pack-fixture", false, "rewrite mvd-web/test/fixtures/bucket-pack.{bin,json}")

// The frontend's decoder test (mvd-web/test/bucketpack.test.js) decodes
// the .bin and compares it with the .json — the same view's JSON — so the
// Go writer, the JS reader and the JSON contract are pinned together.
const bucketPackFixture = "../../mvd-web/test/fixtures/bucket-pack"

// packFixtureView is a built view with every wire type: i16 (h, li, …),
// f32 (x/y/z), f64 (a mean reducer), u8 (weapons, spawns, alive), s16
// (armorType, with repeats), team i32 counters and abt; a late field
// (validFrom), a partial last bucket, a non-ASCII player name, and players
// whose spans differ in length, so columns land at uneven offsets.
func packFixtureView(t *testing.T) *ColumnarBuckets {
	t.Helper()
	r := gapFixture()
	r.Streams.Players[0].Name = "bär!"
	for i := range r.Streams.Players[0].Position.X {
		r.Streams.Players[0].Position.X[i] += 0.3 // a float32 the JSON rounds
	}
	r.Streams.Players[0].Armor = append(r.Streams.Players[0].Armor, result.ChangeI16{T: 6100, V: 51}) // a fractional mean
	r.Streams.Players[0].ArmorType = []result.ChangeStr{{T: 2000, V: "ya"}, {T: 6000, V: "ra"}}
	r.Streams.Players[2].ArmorType = []result.ChangeStr{{T: 0, V: "ra"}}
	cb, err := BucketsColumnar(r, BucketsOptions{
		WindowMs:    700,
		Fields:      AllStandardFields,
		Reducers:    map[string]string{FieldArmor: "mean"},
		IncludeTeam: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cb
}

func TestColumnarPackFixture(t *testing.T) {
	cb := packFixtureView(t)
	got, err := EncodeColumnarPack(cb)
	if err != nil {
		t.Fatal(err)
	}
	if *updateBucketPackFixture {
		js, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bucketPackFixture+".bin", got, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bucketPackFixture+".json", js, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(bucketPackFixture + ".bin")
	if err != nil {
		t.Fatalf("%v (regenerate: go test ./mvd-analytics/view -run TestColumnarPackFixture -args -update-bucket-pack-fixture)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeColumnarPack no longer writes %s.bin; if the change is intended, regenerate with -update-bucket-pack-fixture (bump ColumnarPackVersion and update the JS decoder for a layout change)", bucketPackFixture)
	}
}

// TestColumnarPackLayout reads the header back and checks every column
// is 8-aligned, in bounds, sized for its type, and that the fixture covers
// every wire type the decoder knows.
func TestColumnarPackLayout(t *testing.T) {
	b, err := EncodeColumnarPack(packFixtureView(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "MVDB" || binary.LittleEndian.Uint32(b[4:]) != ColumnarPackVersion {
		t.Fatal("bad magic / version")
	}
	hl := int(binary.LittleEndian.Uint32(b[8:]))
	var h packHeader
	if err := json.Unmarshal(b[12:12+hl], &h); err != nil {
		t.Fatal(err)
	}
	body := b[12+hl+(8-(12+hl)%8)%8:]
	if (len(b)-len(body))%8 != 0 {
		t.Fatal("body not 8-aligned")
	}
	size := map[string]int{"i16": 2, "i32": 4, "f32": 4, "f64": 8, "u8": 1, "s16": 2}
	seen := map[string]bool{}
	check := func(what string, c packCol) {
		seen[c.T] = true
		if c.O%8 != 0 || c.O+c.N*size[c.T] > len(body) || size[c.T] == 0 {
			t.Fatalf("%s: column %+v misplaced in a %d-byte body", what, c, len(body))
		}
	}
	for name, p := range h.Players {
		check(name+".alive", p.Alive)
		if p.Alive.N != p.N {
			t.Fatalf("%s: alive has %d entries, n=%d", name, p.Alive.N, p.N)
		}
		for k, c := range p.Cols {
			check(name+"."+k, c)
		}
	}
	for name, tm := range h.Teams {
		for k, c := range tm.Cols {
			check(name+"."+k, c)
		}
		for k, c := range tm.ABT {
			check(name+".abt."+k, c)
		}
	}
	for typ := range size {
		if !seen[typ] {
			t.Errorf("fixture has no %s column", typ)
		}
	}
}

func TestColumnarPackRejectsUnknownColumn(t *testing.T) {
	cb := &ColumnarBuckets{Players: map[string]*ColumnarPlayer{"p": {N: 1, Alive: []bool{true}, Cols: map[string]any{"h": []uint8{1}}}}}
	if _, err := EncodeColumnarPack(cb); err == nil {
		t.Fatal("a column type without a wire type must fail, not be dropped")
	}
}
