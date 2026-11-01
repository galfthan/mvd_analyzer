package result

import (
	"bytes"
	"encoding/binary"
	"flag"
	"math"
	"os"
	"reflect"
	"testing"
)

var updatePackFixture = flag.Bool("update-pack-fixture", false, "rewrite mvd-web/test/fixtures/position-pack.bin")

// packFixturePath is the pack the frontend's decoder test reads
// (mvd-web/test/postrack.test.js), so the Go writer and the JS reader are
// pinned to the same bytes. The JS test spells out the same values.
const packFixturePath = "../../mvd-web/test/fixtures/position-pack.bin"

// fixtureStreams covers what the layout has to get right: a name whose
// byte length is not a multiple of 4 (and not ASCII), every column, odd
// counts of the 1- and 2-byte columns (padding), negative values, -0 and
// NoFloor, a track with only the four always-present columns, and a
// player with no track.
func fixtureStreams() *Streams {
	return &Streams{Players: []PlayerStream{
		{Name: "bär!", Position: &PositionTrack{
			T:   []int32{0, 13, -7},
			X:   []float32{1.125, -2.5, float32(math.Copysign(0, -1))},
			Y:   []float32{100, 200.25, -300},
			Z:   []float32{24.03125, 0, -1e9},
			Li:  []int16{3, -1, 7},
			H:   []float32{0, NoFloor, 31.999},
			Lq:  []int8{0, 9, -1},
			VP:  []int16{-32768, 0, 32767},
			VYa: []int16{16384, -16384, 1},
			VX:  []float32{320, -58.333332, 0.5},
			VY:  []float32{1, 2, 3},
			VZ:  []float32{-270, 0, 270},
		}},
		{Name: "xy", Position: &PositionTrack{
			T: []int32{5},
			X: []float32{1},
			Y: []float32{2},
			Z: []float32{3},
		}},
		{Name: "spec"},
	}}
}

func TestPositionPackFixture(t *testing.T) {
	got := EncodePositionPack(fixtureStreams())
	if *updatePackFixture {
		if err := os.WriteFile(packFixturePath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(packFixturePath)
	if err != nil {
		t.Fatalf("%v (regenerate: go test ./mvd-analytics/result -run TestPositionPackFixture -args -update-pack-fixture)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodePositionPack no longer writes %s; if the layout change is intended, bump PositionPackVersion, regenerate with -update-pack-fixture and update the JS decoder", packFixturePath)
	}
}

func TestPositionPackSizeIsExact(t *testing.T) {
	for _, s := range []*Streams{nil, {}, fixtureStreams()} {
		b := EncodePositionPack(s)
		if len(b) != positionPackSize(s) || cap(b) != len(b) {
			t.Fatalf("size %d, cap %d, predicted %d", len(b), cap(b), positionPackSize(s))
		}
		if len(b)%4 != 0 {
			t.Fatalf("pack length %d not 4-aligned", len(b))
		}
	}
}

// TestPositionPackCoversEveryColumn gives PositionTrack's i-th column
// (declaration order, which is the pack's column order) length i+1 and
// walks the pack's length words — a column the pack does not carry, or
// carries out of order, fails here.
func TestPositionPackCoversEveryColumn(t *testing.T) {
	var p PositionTrack
	v := reflect.ValueOf(&p).Elem()
	var sizes []int
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		f.Set(reflect.MakeSlice(f.Type(), i+1, i+1))
		sizes = append(sizes, int(f.Type().Elem().Size()))
	}
	b := EncodePositionPack(&Streams{Players: []PlayerStream{{Position: &p}}})
	off := 12 + 4 + 4 // header, empty name, has-track
	for i, size := range sizes {
		if n := int(binary.LittleEndian.Uint32(b[off:])); n != i+1 {
			t.Fatalf("column %d (%s) has length %d, want %d", i, v.Type().Field(i).Name, n, i+1)
		}
		off += 4 + (i+1)*size + pad4((i+1)*size)
	}
	if off != len(b) {
		t.Fatalf("pack has %d bytes past PositionTrack's %d columns", len(b)-off, len(sizes))
	}
}

func TestWithoutPositionsLeavesTheOriginal(t *testing.T) {
	s := fixtureStreams()
	c := s.WithoutPositions()
	if s.Players[0].Position == nil || c.Players[0].Position != nil || c.Players[0].Name != "bär!" {
		t.Fatal("WithoutPositions must strip the copy's tracks only")
	}
	if (*Streams)(nil).WithoutPositions() != nil {
		t.Fatal("nil Streams stays nil")
	}
}
