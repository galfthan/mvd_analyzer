//go:build !(js && wasm)

package loc

import (
	"math/rand"
	"testing"
)

// benchPoints samples uniformly inside the loc-list's bounding box,
// so queries land in realistic in-game positions.
func benchPoints(locs []Location, n int) [][3]float32 {
	if len(locs) == 0 {
		return nil
	}
	minX, minY, minZ := locs[0].X, locs[0].Y, locs[0].Z
	maxX, maxY, maxZ := minX, minY, minZ
	for _, l := range locs[1:] {
		if l.X < minX {
			minX = l.X
		}
		if l.X > maxX {
			maxX = l.X
		}
		if l.Y < minY {
			minY = l.Y
		}
		if l.Y > maxY {
			maxY = l.Y
		}
		if l.Z < minZ {
			minZ = l.Z
		}
		if l.Z > maxZ {
			maxZ = l.Z
		}
	}
	r := rand.New(rand.NewSource(1))
	out := make([][3]float32, n)
	for i := range out {
		out[i] = [3]float32{
			minX + r.Float32()*(maxX-minX),
			minY + r.Float32()*(maxY-minY),
			minZ + r.Float32()*(maxZ-minZ),
		}
	}
	return out
}

func loadCorpus(b *testing.B, name string) []Location {
	b.Helper()
	f, err := LoadForMap(name)
	if err != nil {
		b.Skipf("corpus missing %s: %v", name, err)
	}
	return f.Locations()
}

func benchLinear(b *testing.B, mapName string) {
	locs := loadCorpus(b, mapName)
	b.Logf("L=%d", len(locs))
	pts := benchPoints(locs, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := pts[i%len(pts)]
		findNearestLinear(locs, p[0], p[1], p[2])
	}
}

func BenchmarkLinear_dm6(b *testing.B)      { benchLinear(b, "dm6") }
func BenchmarkLinear_dm3(b *testing.B)      { benchLinear(b, "dm3") }
func BenchmarkLinear_defer(b *testing.B)    { benchLinear(b, "defer") }
func BenchmarkLinear_outpost3(b *testing.B) { benchLinear(b, "outpost3") }
func BenchmarkLinear_tf2k(b *testing.B)     { benchLinear(b, "tf2k") }
func BenchmarkLinear_2fort5(b *testing.B)   { benchLinear(b, "2fort5") }

func benchPencil(b *testing.B, mapName string) {
	locs := loadCorpus(b, mapName)
	idx := buildPencilIndex(locs, 0)
	b.Logf("L=%d cells=%d", len(locs), len(idx.cells))
	pts := benchPoints(locs, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := pts[i%len(pts)]
		idx.findNearest(locs, p[0], p[1], p[2])
	}
}

func BenchmarkPencil_dm6(b *testing.B)      { benchPencil(b, "dm6") }
func BenchmarkPencil_dm3(b *testing.B)      { benchPencil(b, "dm3") }
func BenchmarkPencil_defer(b *testing.B)    { benchPencil(b, "defer") }
func BenchmarkPencil_outpost3(b *testing.B) { benchPencil(b, "outpost3") }
func BenchmarkPencil_tf2k(b *testing.B)     { benchPencil(b, "tf2k") }
func BenchmarkPencil_2fort5(b *testing.B)   { benchPencil(b, "2fort5") }
