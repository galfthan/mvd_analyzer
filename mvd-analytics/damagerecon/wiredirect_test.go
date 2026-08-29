package damagerecon

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// wireGLFixture builds the smallest Result the wire touch classifier reads: a
// shooter and a victim with position tracks, one tracked grenade flight ending
// at ep, and one wire gl damage row for it. The victim stands at the origin.
func wireGLFixture(ep vec3, spawnT, endT int32) *result.Result {
	track := func(x, y, z float32) *result.PositionTrack {
		return &result.PositionTrack{
			T: []int32{0, 10000},
			X: []float32{x, x}, Y: []float32{y, y}, Z: []float32{z, z},
		}
	}
	return &result.Result{
		Streams: &result.Streams{
			ShotStreamsComputed: true,
			Players: []result.PlayerStream{
				{Name: "shooter", Position: track(-400, 0, 0)},
				{Name: "victim", Position: track(0, 0, 0)},
			},
			Projectiles: &result.ProjectileStreams{
				Weapon: []string{"gl"},
				Spawn:  []int32{spawnT},
				End:    []int32{endT},
				Sx:     []float32{-400}, Sy: []float32{0}, Sz: []float32{16},
				Ex: []float32{float32(ep.x)}, Ey: []float32{float32(ep.y)}, Ez: []float32{float32(ep.z)},
			},
		},
		Damage: &result.DamageResult{
			Source: result.DamageSourceKTX,
			Events: []result.DamageEntry{
				{Time: endT, Attacker: "shooter", Victim: "victim", Weapon: "gl", Damage: 80, IsSplash: true},
			},
		},
	}
}

// A grenade detonating ON the victim is a TOUCH, even though the wire row is
// splash-flagged like every other gl row (GrenadeTouch detonates through
// T_RadiusDamage, ktx/src/weapons.c:1327-1340).
func TestWireDirectTouchesGrenadeOnHull(t *testing.T) {
	v := WireDirectTouches(wireGLFixture(vec3{0, 0, 0}, 3000, 3400))
	if len(v) != 1 || !v[0] {
		t.Fatalf("contact grenade must read as a touch, got %v", v)
	}
}

// A grenade detonating well clear of the hull is splash: the wire row is
// identical, so only the geometry separates the two.
func TestWireDirectTouchesGrenadeClearOfHull(t *testing.T) {
	v := WireDirectTouches(wireGLFixture(vec3{-120, 0, 0}, 3000, 3400))
	if len(v) != 1 || v[0] {
		t.Fatalf("grenade 120 units away must read as splash, got %v", v)
	}
}

// A grenade whose observed flight spans the whole 2.5 s fuse died of the fuse
// (weapons.c:1434), so GrenadeTouch never ran — a certain non-touch however
// close the detonation point sits. Needs the TE_EXPLOSION endpoint match
// (epExact), which is what says the bracket ended in a detonation rather than
// in a PVS exit.
func TestWireDirectTouchesSpentFuseIsNoTouch(t *testing.T) {
	res := wireGLFixture(vec3{0, 0, 0}, 500, 500+grenadeFuseObservedMs+100)
	end := res.Streams.Projectiles.End[0]
	res.Streams.PointEffects = &result.PointEffectStreams{
		T: []int32{end}, Type: []int32{int32(events.TeExplosion)}, Count: []int32{0},
		X: []float32{0}, Y: []float32{0}, Z: []float32{0},
	}
	res.Damage.Events[0].Time = end
	if v := WireDirectTouches(res); len(v) != 1 || v[0] {
		t.Fatalf("a grenade that outlived its fuse cannot have touched, got %v", v)
	}
}

// Absence is not a zero. Each missing input is a reason the classification
// could not be made at all, and the caller has to be able to tell that from
// "touched nobody" — aim withholds gl's split on a nil return.
func TestWireDirectTouchesWithheldWithoutInputs(t *testing.T) {
	res := wireGLFixture(vec3{0, 0, 0}, 3000, 3400)
	res.Streams.ShotStreamsComputed = false
	if v := WireDirectTouches(res); v != nil {
		t.Errorf("no spatial shot streams: want nil, got %v", v)
	}

	res = wireGLFixture(vec3{0, 0, 0}, 3000, 3400)
	res.Damage.Source = result.DamageSourceReconstructed
	if v := WireDirectTouches(res); v != nil {
		t.Errorf("reconstructed log classifies its own rows: want nil, got %v", v)
	}

	res = wireGLFixture(vec3{0, 0, 0}, 3000, 3400)
	res.Damage = nil
	if v := WireDirectTouches(res); v != nil {
		t.Errorf("no damage log: want nil, got %v", v)
	}
}

// Rows the classifier never entertains: a self hit (a missile returns
// immediately on `other == owner`, weapons.c:951/:1315) and every non-rl/gl
// weapon.
func TestWireDirectTouchesSkipsSelfAndOtherWeapons(t *testing.T) {
	res := wireGLFixture(vec3{0, 0, 0}, 3000, 3400)
	res.Damage.Events = append(res.Damage.Events,
		result.DamageEntry{Time: 3400, Attacker: "shooter", Victim: "shooter", Weapon: "gl", Damage: 40, IsSplash: true, IsSelf: true},
		result.DamageEntry{Time: 3400, Attacker: "shooter", Victim: "victim", Weapon: "lg", Damage: 30},
	)
	v := WireDirectTouches(res)
	if len(v) != 3 || !v[0] || v[1] || v[2] {
		t.Fatalf("want [true false false], got %v", v)
	}
}
