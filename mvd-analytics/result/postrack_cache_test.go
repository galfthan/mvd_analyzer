package result

import (
	"math"
	"reflect"
	"testing"
)

// TestPositionTrackCodecCoversEveryColumn fills every PositionTrack column
// with distinct values (including -0 and NaN bit patterns) and requires
// the codec to bring each back bit-identical — a column added to the
// struct but not to GobEncode/GobDecode fails here.
func TestPositionTrackCodecCoversEveryColumn(t *testing.T) {
	var p PositionTrack
	v := reflect.ValueOf(&p).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Slice {
			t.Fatalf("PositionTrack.%s is not a column; teach this test and the codec about it", v.Type().Field(i).Name)
		}
		s := reflect.MakeSlice(f.Type(), 3, 3)
		for j := 0; j < 3; j++ {
			e := s.Index(j)
			switch e.Kind() {
			case reflect.Float32:
				e.SetFloat([]float64{float64(i) + 0.5, math.Copysign(0, -1), -1e9}[j])
			default:
				e.SetInt(int64(i*3 + j - 4))
			}
		}
		f.Set(s)
	}
	p.X[2] = math.Float32frombits(0x7fc00001) // a NaN payload survives too

	b := appendPositionTrack(nil, &p)
	r := colReader{b: b}
	q := *readPositionTrack(&r)
	if r.bad || len(r.b) != 0 {
		t.Fatal("round trip did not consume the encoding cleanly")
	}
	for i := 0; i < v.NumField(); i++ {
		a, c := v.Field(i), reflect.ValueOf(q).Field(i)
		if a.Len() != c.Len() {
			t.Fatalf("%s: %d values in, %d out", v.Type().Field(i).Name, a.Len(), c.Len())
		}
		for j := 0; j < a.Len(); j++ {
			x, y := a.Index(j), c.Index(j)
			if x.Kind() == reflect.Float32 {
				if math.Float32bits(float32(x.Float())) != math.Float32bits(float32(y.Float())) {
					t.Fatalf("%s[%d]: %v in, %v out", v.Type().Field(i).Name, j, x, y)
				}
			} else if x.Int() != y.Int() {
				t.Fatalf("%s[%d]: %v in, %v out", v.Type().Field(i).Name, j, x, y)
			}
		}
	}

	// Empty columns come back nil, as gob's own slice coding did.
	r = colReader{b: appendPositionTrack(nil, &PositionTrack{T: []int32{}})}
	if e := readPositionTrack(&r); r.bad || e.T != nil {
		t.Fatalf("empty column: bad=%v, T=%v", r.bad, e.T)
	}

	// A truncated or oversized payload latches bad, never panics.
	for _, bad := range [][]byte{nil, b[:1], b[:len(b)-1], {0xff, 0xff, 0xff, 0xff, 0x0f}} {
		r = colReader{b: bad}
		readPositionTrack(&r)
		if !r.bad {
			t.Fatalf("decoded a malformed payload (%d bytes)", len(bad))
		}
	}
}
