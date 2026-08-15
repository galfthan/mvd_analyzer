package damagerecon

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// vec3 is a small value-type vector; the package works in float64 like the
// rest of the geometry code (aimcore, los).
type vec3 struct{ x, y, z float64 }

func (a vec3) sub(b vec3) vec3       { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) dot(b vec3) float64    { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) length() float64       { return math.Sqrt(a.dot(a)) }
func (a vec3) distTo(b vec3) float64 { return a.sub(b).length() }

// segDist is the distance from point p to segment a-b.
func segDist(p, a, b vec3) float64 {
	ab := b.sub(a)
	ap := p.sub(a)
	ab2 := ab.dot(ab)
	t := 0.0
	if ab2 > 0 {
		t = math.Max(0, math.Min(1, ap.dot(ab)/ab2))
	}
	c := vec3{a.x + t*ab.x, a.y + t*ab.y, a.z + t*ab.z}
	return p.distTo(c)
}

// teleportGuardDist matches the prototype: two adjacent track samples
// farther apart than this are a teleport/respawn discontinuity and must not
// be interpolated across.
const teleportGuardDist = 400.0

// track wraps a PositionTrack with the sampling semantics the
// reconstruction needs: position lerp with a teleport guard, raw
// nearest-sample view angles (matching the prototype's calibration), and a
// velocity delta across an instant (the knockback signal).
type track struct {
	pt *result.PositionTrack
}

func newTrack(pt *result.PositionTrack) *track {
	if pt == nil || len(pt.T) == 0 {
		return nil
	}
	return &track{pt: pt}
}

// idxAtOrBefore returns the largest i with T[i] <= t, or -1.
func (tr *track) idxAtOrBefore(t int32) int {
	return sort.Search(len(tr.pt.T), func(i int) bool { return tr.pt.T[i] > t }) - 1
}

// posAt linearly interpolates the position at t, holding the earlier sample
// across teleport-sized jumps and clamping outside the track.
func (tr *track) posAt(t int32) vec3 {
	pt := tr.pt
	i := tr.idxAtOrBefore(t)
	if i < 0 {
		return vec3{float64(pt.X[0]), float64(pt.Y[0]), float64(pt.Z[0])}
	}
	if i >= len(pt.T)-1 {
		n := len(pt.T) - 1
		return vec3{float64(pt.X[n]), float64(pt.Y[n]), float64(pt.Z[n])}
	}
	a := vec3{float64(pt.X[i]), float64(pt.Y[i]), float64(pt.Z[i])}
	b := vec3{float64(pt.X[i+1]), float64(pt.Y[i+1]), float64(pt.Z[i+1])}
	if a.distTo(b) > teleportGuardDist {
		return a
	}
	span := pt.T[i+1] - pt.T[i]
	if span <= 0 {
		return a
	}
	f := float64(t-pt.T[i]) / float64(span)
	return vec3{a.x + f*(b.x-a.x), a.y + f*(b.y-a.y), a.z + f*(b.z-a.z)}
}

// aimAt returns the view direction (unit forward vector) at the nearest
// sample at-or-before t, decoding the raw angle16 columns. ok is false when
// the track has no view columns.
func (tr *track) aimAt(t int32) (fw vec3, ok bool) {
	pt := tr.pt
	n := len(pt.T)
	if len(pt.VP) != n || len(pt.VYa) != n {
		return vec3{}, false
	}
	i := tr.idxAtOrBefore(t)
	if i < 0 {
		i = 0
	}
	yaw := float64(uint16(pt.VYa[i])) * 360.0 / 65536.0
	pitch := float64(uint16(pt.VP[i])) * 360.0 / 65536.0
	if pitch > 180.0 {
		pitch -= 360.0 // signed: positive = down (QW convention)
	}
	p := pitch * math.Pi / 180.0
	y := yaw * math.Pi / 180.0
	return vec3{math.Cos(p) * math.Cos(y), math.Cos(p) * math.Sin(y), -math.Sin(p)}, true
}

// eyeOffsetZ / targetOffsetZ: the shooter aims from the eye (+22, the same
// constant los.go uses) at roughly the victim's mid-box (+4).
const (
	eyeOffsetZ    = 22.0
	targetOffsetZ = 4.0
)

// aimAngleTo returns the angle in degrees between the view direction at t
// and the eye→target ray. ok is false without view columns.
func (tr *track) aimAngleTo(t int32, target vec3) (float64, bool) {
	fw, ok := tr.aimAt(t)
	if !ok {
		return 0, false
	}
	eye := tr.posAt(t)
	d := vec3{target.x - eye.x, target.y - eye.y, (target.z + targetOffsetZ) - (eye.z + eyeOffsetZ)}
	l := d.length()
	if l < 1 {
		return 0, true
	}
	c := fw.dot(d) / l
	return math.Acos(math.Max(-1, math.Min(1, c))) * 180.0 / math.Pi, true
}

// velDelta is the velocity change across the instant t (sample just before
// t-preMs to sample just after t+postMs) — the knockback signal. ok is
// false without velocity columns or outside the track.
func (tr *track) velDelta(t int32, preMs, postMs int32) (vec3, bool) {
	pt := tr.pt
	n := len(pt.T)
	if len(pt.VX) != n || len(pt.VY) != n || len(pt.VZ) != n {
		return vec3{}, false
	}
	// Prototype parity: i0 = index before the first sample >= t-preMs;
	// i1 = index after the last sample <= t+postMs.
	i0 := sort.Search(n, func(i int) bool { return pt.T[i] >= t-preMs }) - 1
	i1 := sort.Search(n, func(i int) bool { return pt.T[i] > t+postMs })
	if i0 < 0 || i1 >= n {
		return vec3{}, false
	}
	return vec3{
		float64(pt.VX[i1] - pt.VX[i0]),
		float64(pt.VY[i1] - pt.VY[i0]),
		float64(pt.VZ[i1] - pt.VZ[i0]),
	}, true
}

// inIntervals reports whether t lies in any half-open [Start,End) interval.
func inIntervals(ivs []result.Interval, t int32) bool {
	for _, iv := range ivs {
		if iv.Start <= t && t < iv.End {
			return true
		}
	}
	return false
}
