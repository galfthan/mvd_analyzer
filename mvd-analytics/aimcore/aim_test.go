package aimcore

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Aim attributes a miss to the nearest enemy who is ALIVE at the fire time
// (aimAttribute), reading PlayerStream.Alive through aimAliveAt. That field is
// three-state, and the three states must stay distinct here: nil means
// liveness was not measurable and degrades to "alive" — dropping every shot on
// a demo whose lives could not be derived would be far worse than attributing
// to a player who may have been dead — while an empty non-nil list is the
// measurement "never alive in the window" and gates every shot.
func TestAimLivenessMeasurednessStates(t *testing.T) {
	ts := []int32{0, 500, 1000}
	shooter := &result.PositionTrack{
		T:   ts,
		X:   []float32{0, 0, 0},
		Y:   []float32{0, 0, 0},
		Z:   []float32{0, 0, 0},
		VP:  []int16{0, 0, 0}, // level
		VYa: []int16{0, 0, 0}, // facing +x, straight at the target
	}
	target := &result.PositionTrack{
		T: ts,
		X: []float32{200, 200, 200},
		Y: []float32{0, 0, 0},
		Z: []float32{0, 0, 0},
	}

	// One lg miss at t=500, with no confirmed victim — so the target is chosen
	// by the liveness-gated nearest-crosshair path, not named by the server.
	crosshairSamples := func(alive []result.Interval) int {
		res := &result.Result{
			Shots: &result.ShotsResult{
				Shots: []result.Shot{{Time: 500, Player: "A", Weapon: "lg", Source: "beam"}},
			},
			Streams: &result.Streams{
				Players: []result.PlayerStream{
					{Name: "A", Position: shooter, Alive: []result.Interval{{Start: 0, End: 1000}}},
					{Name: "B", Position: target, Alive: alive},
				},
			},
		}
		ar := Compute(res, Query{})
		if ar == nil {
			return 0
		}
		for _, pa := range ar.Players {
			if pa.Player == "A" && pa.Crosshair != nil {
				return len(pa.Crosshair.T)
			}
		}
		return 0
	}

	if got := crosshairSamples(nil); got != 1 {
		t.Errorf("crosshair samples = %d with an UNMEASURABLE opponent liveness, want 1 — "+
			"unknown must degrade to alive, not silently drop the shot", got)
	}
	if got := crosshairSamples([]result.Interval{{Start: 0, End: 1000}}); got != 1 {
		t.Errorf("crosshair samples = %d against a live opponent, want 1", got)
	}
	if got := crosshairSamples([]result.Interval{}); got != 0 {
		t.Errorf("crosshair samples = %d against an opponent measured as never alive, want 0", got)
	}
	if got := crosshairSamples([]result.Interval{{Start: 0, End: 400}}); got != 0 {
		t.Errorf("crosshair samples = %d against an opponent dead since t=400, want 0 — "+
			"a corpse is not an aim target", got)
	}
}

// A quad pellet writes 16 to the wire damage log, not 4 (T_Damage multiplies
// the attacker's damage while super_damage_finished > time,
// ktx/src/combat.c:540-546). The pellet estimator must divide by what the
// SHOOTER'S quad at fire time says, or a quad fire with two pellets in reads
// as eight and saturates the 6-pellet clamp — which was the entire measured
// shotgun residual against the KTX block.
func TestPelletHitsDivideByTheShooterQuad(t *testing.T) {
	ts := []int32{0, 500, 1500, 2500}
	track := func(x float32) *result.PositionTrack {
		return &result.PositionTrack{
			T:   ts,
			X:   []float32{x, x, x, x},
			Y:   []float32{0, 0, 0, 0},
			Z:   []float32{0, 0, 0, 0},
			VP:  []int16{0, 0, 0, 0},
			VYa: []int16{0, 0, 0, 0},
		}
	}
	// Two identical sg fires that each landed 32 damage: one at t=500 while A
	// holds the quad, one at t=1500 after it ran out.
	res := &result.Result{
		Shots: &result.ShotsResult{Shots: []result.Shot{
			{Time: 500, Player: "A", Weapon: "sg", Hit: true, Victims: []string{"B"}},
			{Time: 1500, Player: "A", Weapon: "sg", Hit: true, Victims: []string{"B"}},
		}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 500, Attacker: "A", Victim: "B", Weapon: "sg", Damage: 32},
			{Time: 1500, Attacker: "A", Victim: "B", Weapon: "sg", Damage: 32},
		}},
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "A", Position: track(0), Alive: []result.Interval{{Start: 0, End: 2500}},
				Quad: []result.Interval{{Start: 0, End: 1000}}},
			{Name: "B", Position: track(200), Alive: []result.Interval{{Start: 0, End: 2500}}},
		}},
	}
	ar := Compute(res, Query{})
	if ar == nil {
		t.Fatal("no aim computed")
	}
	var sg *result.WeaponAim
	for i := range ar.Players {
		if ar.Players[i].Player != "A" {
			continue
		}
		for j := range ar.Players[i].Weapons {
			if ar.Players[i].Weapons[j].Weapon == "sg" {
				sg = &ar.Players[i].Weapons[j]
			}
		}
	}
	if sg == nil {
		t.Fatal("no sg row for A")
	}
	// 32/16 = 2 pellets under the quad, 32/4 = 8 clamped to 6 without it.
	if sg.PelletHits != 8 {
		t.Errorf("pelletHits = %d, want 8 (2 quad pellets + 6 clamped normal ones)", sg.PelletHits)
	}
	if sg.Full != 1 || sg.Partial != 1 {
		t.Errorf("full/partial = %d/%d, want 1/1 — only the un-quadded fire filled its 6",
			sg.Full, sg.Partial)
	}
}

// aimHeldAt's absent-stream default is the OPPOSITE of aimAliveAt's: a player
// with no quad interval never held one, and reading that as "held" would
// divide every pellet fire on a quad-less demo by 16.
func TestAimHeldAtAbsentStreamIsNotHeld(t *testing.T) {
	if aimHeldAt(nil, 500) {
		t.Error("aimHeldAt(nil) = true, want false — no interval is no possession")
	}
	iv := []result.Interval{{Start: 100, End: 200}, {Start: 400, End: 600}}
	for _, c := range []struct {
		t    int32
		want bool
	}{{99, false}, {100, true}, {199, true}, {200, false}, {500, true}, {600, false}} {
		if got := aimHeldAt(iv, c.t); got != c.want {
			t.Errorf("aimHeldAt(t=%d) = %v, want %v", c.t, got, c.want)
		}
	}
}

// glDirectFixture: one grenade fire that connected, its tracked flight ending
// on the victim's hull, and the wire damage row it produced — which the server
// flags splash like every other gl row, because GrenadeTouch does all its
// damage through T_RadiusDamage (ktx/src/combat.c:1207). Direct therefore
// cannot come off that flag and comes off the touch classifier instead.
func glDirectFixture() *result.Result {
	ts := []int32{0, 3400, 5000}
	track := func(x float32) *result.PositionTrack {
		return &result.PositionTrack{
			T:   ts,
			X:   []float32{x, x, x},
			Y:   []float32{0, 0, 0},
			Z:   []float32{0, 0, 0},
			VP:  []int16{0, 0, 0},
			VYa: []int16{0, 0, 0},
		}
	}
	end := int32(3400)
	return &result.Result{
		Shots: &result.ShotsResult{Shots: []result.Shot{
			{Time: 3000, Player: "A", Weapon: "gl", Hit: true, Victims: []string{"B"}, FlightEnd: &end},
		}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 3400, Attacker: "A", Victim: "B", Weapon: "gl", Damage: 80, IsSplash: true},
		}},
		Streams: &result.Streams{
			ShotStreamsComputed: true,
			Players: []result.PlayerStream{
				{Name: "A", Position: track(-400), Alive: []result.Interval{{Start: 0, End: 5000}}},
				{Name: "B", Position: track(0), Alive: []result.Interval{{Start: 0, End: 5000}}},
			},
			Projectiles: &result.ProjectileStreams{
				Weapon: []string{"gl"},
				Spawn:  []int32{3000},
				End:    []int32{3400},
				Sx:     []float32{-400}, Sy: []float32{0}, Sz: []float32{16},
				Ex: []float32{0}, Ey: []float32{0}, Ez: []float32{0},
			},
		},
	}
}

func glRow(t *testing.T, ar *result.AimResult) *result.WeaponAim {
	t.Helper()
	if ar == nil {
		t.Fatal("no aim computed")
	}
	for i := range ar.Players {
		if ar.Players[i].Player != "A" {
			continue
		}
		for j := range ar.Players[i].Weapons {
			if ar.Players[i].Weapons[j].Weapon == "gl" {
				return &ar.Players[i].Weapons[j]
			}
		}
	}
	t.Fatal("no gl row for A")
	return nil
}

// gl's Direct is the TOUCH count from the flight-geometry classifier, not the
// wire's splash flag — which says "splash" on every gl row ever written and
// would report a grenade landing on somebody's head as a splash-only hit.
func TestGLDirectComesFromTheTouchClassifier(t *testing.T) {
	gl := glRow(t, Compute(glDirectFixture(), Query{}))
	if gl.Direct != 1 || gl.Splash != 0 {
		t.Errorf("gl direct/splash = %d/%d, want 1/0 — the grenade detonated on the victim's hull",
			gl.Direct, gl.Splash)
	}
}

// Without the spatial shot streams there is no flight to read the geometry
// off, and the split is WITHHELD rather than filled from the splash flag: a
// Direct of 0 there would be the flag answering rl's question in gl's row.
// Missed does not ride the split and is kept.
func TestGLDirectWithheldWithoutShotStreams(t *testing.T) {
	res := glDirectFixture()
	res.Streams.ShotStreamsComputed = false
	res.Streams.Projectiles = nil
	gl := glRow(t, Compute(res, Query{}))
	if gl.Direct != 0 || gl.Splash != 0 {
		t.Errorf("gl direct/splash = %d/%d, want 0/0 (withheld)", gl.Direct, gl.Splash)
	}
	if gl.Missed != gl.Shots-gl.Hits {
		t.Errorf("gl missed = %d, want %d — missed does not ride the direct/splash split",
			gl.Missed, gl.Shots-gl.Hits)
	}
}

// The touch count is windowed on the DAMAGE instant, exactly as the fires are
// windowed on their fire time (the same rule ReconDirectHits follows) — so a
// window that excludes the impact excludes the touch.
func TestGLDirectIsWindowed(t *testing.T) {
	to := int32(3200)
	gl := glRow(t, Compute(glDirectFixture(), Query{ToMs: &to}))
	if gl.Direct != 0 {
		t.Errorf("gl direct = %d in a window ending before the impact, want 0", gl.Direct)
	}
}
