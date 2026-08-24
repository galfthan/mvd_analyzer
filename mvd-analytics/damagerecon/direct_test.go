package damagerecon

import "testing"

// A rocket that flew INTO the victim is a touch; one that flew PAST them into
// the wall beside them is not, even though both detonate the same short
// distance from the victim's origin. That pair is the whole reason the test
// is a trajectory and not a radius: the 48-unit endpoint rule called both of
// them direct and over-counted KTX's rl counter by 80% (ACCURACY.md).
func TestDirectImpactSeparatesWallBesideFromTouch(t *testing.T) {
	victim := vec3{0, 0, 0}
	// Fired from 500 units away on the -x axis, straight at the victim's
	// chest: the detonation lands on the hull face, 8 units short of it
	// (the engine's TE_EXPLOSION pull-back).
	if !directImpact("rl", vec3{-500, 0, 20}, vec3{-24, 0, 20}, victim, false) {
		t.Error("a rocket flying into the victim must be a touch")
	}
	// Same shooter, same range, aimed 30 units to the side: the rocket
	// passes the victim and detonates on a wall level with them. The
	// endpoint is 30 units from the hull face — INSIDE the old 48-unit
	// rule — but the line never enters the box.
	if directImpact("rl", vec3{-500, 30, 20}, vec3{40, 30, 20}, victim, false) {
		t.Error("a rocket that flew past the victim into the wall is splash")
	}
	// A rocket coming from behind the victim and stopping short of them is
	// splash too: the trajectory is followed FORWARD only, and 60 units is
	// beyond directFwdReach.
	if directImpact("rl", vec3{500, 0, 20}, vec3{76, 0, 20}, victim, false) {
		t.Error("a detonation a frame and a half short of the hull is splash")
	}
}

// A missile never touches its own owner — both touch handlers return on
// `other == owner` (ktx/src/weapons.c:951, :1315) — so a self hit is radius
// damage however the geometry reads.
func TestDirectImpactSelfIsAlwaysSplash(t *testing.T) {
	v := vec3{0, 0, 0}
	if directImpact("rl", vec3{-500, 0, 20}, vec3{-20, 0, 20}, v, true) {
		t.Error("a rocket cannot touch its own owner")
	}
	if directImpact("gl", vec3{0, 0, 0}, vec3{0, 0, 0}, v, true) {
		t.Error("a grenade cannot touch its own owner")
	}
}

// A grenade is decided by where it detonated, not by a trajectory: it lobs
// and bounces, so its spawn→despawn line is not its path.
func TestDirectImpactGrenadeUsesTheDetonationPoint(t *testing.T) {
	v := vec3{0, 0, 0}
	// On the victim's chest, having arrived from anywhere at all.
	if !directImpact("gl", vec3{300, 300, 300}, vec3{10, 0, 10}, v, false) {
		t.Error("a grenade detonating on the victim is a touch whatever its flight looked like")
	}
	// Resting on the floor well clear of them.
	if directImpact("gl", vec3{300, 300, 300}, vec3{90, 0, -24}, v, false) {
		t.Error("a grenade detonating 74 units clear of the hull touched nobody")
	}
}

// A point-blank rocket whose spawn and detonation are a few units apart has
// no usable direction, so the endpoint decides — at that range it IS the
// touch point.
func TestDirectImpactShortFlightFallsBackToProximity(t *testing.T) {
	v := vec3{0, 0, 0}
	if !directImpact("rl", vec3{-18, 0, 10}, vec3{-10, 0, 10}, v, false) {
		t.Error("a point-blank rocket inside the hull is a touch")
	}
	if directImpact("rl", vec3{200, 0, 10}, vec3{210, 0, 10}, v, false) {
		t.Error("a directionless detonation 194 units away is not")
	}
}

// hullDist measures to the BOX, not to the track origin: a grenade at the
// victim's feet is 0 from the hull and 24 from the origin, and reading the
// latter is what made a foot-level detonation look distant.
func TestHullDistIsToTheBox(t *testing.T) {
	v := vec3{0, 0, 0}
	if d := hullDist(vec3{0, 0, -24}, v); d != 0 {
		t.Errorf("a point on the hull floor = %v, want 0", d)
	}
	if d := hullDist(vec3{0, 0, 0}, v); d != 0 {
		t.Errorf("the origin is inside the hull, got %v", d)
	}
	if d := hullDist(vec3{20, 0, 0}, v); d != 4 {
		t.Errorf("4 units clear of the +x face = %v, want 4", d)
	}
	if d := hullDist(vec3{0, 0, 40}, v); d != 8 {
		t.Errorf("8 units above the hull top = %v, want 8", d)
	}
}

// The magnitude prior: on a fixed-constant server the observed raw decides,
// and a SURVIVED hit that is not the constant cannot have been a lone touch.
// On a killing hit the corpse clamp breaks that guarantee, so the trajectory
// keeps the last word; on a vanilla server the prior says nothing at all.
func TestRocketTouchedMagnitudePrior(t *testing.T) {
	fixed := &inputs{rlLo: 110, rlHi: 110}
	vanilla := &inputs{rlLo: 100, rlHi: 120}
	survived := delta{raw: 110, bounded: 110}
	weaker := delta{raw: 84, bounded: 84}
	killing := delta{raw: 84, bounded: 40, died: true}

	if !fixed.rocketTouched(false, true, survived, 1) {
		t.Error("a survived 110 near the hull is a touch even where the trajectory missed")
	}
	if !fixed.rocketTouched(false, true, delta{raw: 440, bounded: 440}, 4) {
		t.Error("the constant is quad-multiplied")
	}
	if fixed.rocketTouched(true, true, weaker, 1) {
		t.Error("a survived 84 cannot be a lone touch, whatever the trajectory says")
	}
	if !fixed.rocketTouched(true, true, killing, 1) {
		t.Error("a KILLING hit's raw is clamped, so the trajectory decides there")
	}
	if fixed.rocketTouched(true, false, survived, 1) {
		t.Error("a detonation far from the hull is not a touch at any magnitude")
	}
	if !vanilla.rocketTouched(true, true, weaker, 1) {
		t.Error("on a random-damage server the trajectory is the whole answer")
	}
	if vanilla.rocketTouched(false, true, survived, 1) {
		t.Error("...including when the value happens to read 110")
	}
}
