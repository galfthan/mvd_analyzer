package damagerecon

import "math"

// Direct-impact classification: did this rocket or grenade TOUCH the victim,
// or only splash them?
//
// The question is not decoration. KTX increments its own rl/gl `hits`
// counter in the touch handler and nowhere else (ktx/src/weapons.c:990-996
// T_MissileTouch, :1327-1333 GrenadeTouch), so it is the number a modern
// demo's demoinfo block reports — and answering it from spectator evidence
// is what lets a pre-instrumentation demo be compared with one at all. An
// instrumented demo says it on the wire (dmg_is_splash is raised only inside
// T_RadiusDamage, ktx/src/combat.c:1207-1227) and the older half says
// nothing, which is what this file reconstructs.
//
// Three engine facts make it answerable:
//
//   - both projectiles are ZERO-SIZE point entities (setsize 0 0 0 0 0 0,
//     ktx/src/weapons.c:1083 and :1437) and the player hull is a fixed
//     32x32x56 box (VEC_HULL_MIN/MAX, ktx/src/client.c:34-37 — QuakeWorld
//     has no crouch, the constants are file-scope and never reassigned), so
//     a touch is exactly a point entering that box, with no projectile
//     radius to model;
//   - a rocket flies a straight line at 1000 ups from the muzzle
//     (weapons.c:1063, :1086-1088), so the flight's spawn and despawn
//     origins determine the WHOLE trajectory — including the stretch past
//     the last broadcast position, which is where the touch happened;
//   - a rocket that touched somebody dealt them a flat constant and nothing
//     else, because T_MissileTouch hands the same victim to T_RadiusDamage
//     as the `ignore` entity (weapons.c:998-1006). On a server whose
//     constant is fixed (KTX >= 1.36) the observed magnitude alone almost
//     decides the question — see rocketTouched.
//
// The failure mode all this replaces is one endpoint proximity cannot see: a
// rocket detonating on the wall BESIDE a victim is within 48 units of them
// and never passed through them. Measured against KTX's own counter, the
// 48-unit test over-counted rl by 80% (ACCURACY.md).
//
// Two things deliberately NOT built, both refuted by measurement rather than
// by argument:
//
//   - EXCLUSIVITY (one explosion touches at most one player, and the touched
//     player takes no splash from it) is a true engine invariant, and it is
//     not worth enforcing: over 3 525 rl/gl explosion groups on the dm2/dm3
//     ground truth the wire violated it 0 times and this classifier violates
//     it 2 times (0.06%). A cross-victim constraint pass would be a
//     mechanism carrying two rows.
//   - the GRENADE FUSE (GrenadeExplode is scheduled 2.5 s after the throw,
//     weapons.c:1434, and only a player touch detonates one early —
//     GrenadeTouch bounces off everything whose takedamage is not
//     DAMAGE_AIM, :1335 — so a flight ending early ended on a player).
//     Sound physics, but it moved the derived gl counter from 0.36%
//     aggregate error to 3.57% against the verbatim KTX block, buying 1.4pp
//     of exact rows on 149. The tracked flight brackets ENTITY VISIBILITY
//     rather than the fuse, so the signal is noisier than the physics
//     suggests.
const (
	// The player hull. hullHalf is the horizontal half-extent; the vertical
	// extents are asymmetric about the origin.
	hullHalf = 16.0
	hullMinZ = -24.0
	hullMaxZ = 32.0

	// hullSlack widens the hull for wire quantization on both sides of the
	// test — the detonation point and the position track are both 1/8-unit
	// coordinates, and the victim position is interpolated between samples.
	// Deliberately SMALL: swept 0-20 against the verbatim KTX block, the
	// derived rl counter runs 0.65% aggregate error at 4 and 5.5% at 12,
	// while per-explosion precision falls from 95.7% to 92.7% by 8. A fat
	// hull admits exactly the wall-beside rockets this test exists to reject.
	hullSlack = 4.0

	// directFwdReach: how far PAST the observed detonation point the
	// trajectory is followed before giving up on a touch. Two engine
	// offsets: the explosion origin is pulled 8 units back along the
	// velocity before TE_EXPLOSION is written (ktx/src/weapons.c:1008-1010),
	// and an endpoint that is only the last broadcast entity position sits
	// up to one server frame — 34 units at 1000 ups — short of the touch.
	//
	// Nothing is followed BACKWARD. A touch detonates the rocket where it
	// touched, so the hull is always ahead of the reported point and never
	// behind it; extending backward only admitted rockets that flew PAST the
	// victim, and cost 8pp of exact rows against the KTX block.
	directFwdReach = 44.0

	// directMinFlight: below this the two flight endpoints do not determine
	// a direction — wire rounding dominates — and the trajectory test is
	// withheld in favour of plain hull proximity.
	directMinFlight = 24.0

	// grenadeTouchSlack: how far a grenade's detonation point may sit from
	// the hull and still count as a touch. Looser than hullSlack because a
	// grenade has no straight trajectory to check it against, and because a
	// grenade that touches detonates AT the contact point rather than 8
	// units back (GrenadeExplode writes self->s.v.origin unchanged,
	// ktx/src/weapons.c:1302-1306). Swept against the KTX block: 0.36%
	// aggregate error at 24, 2.5% at 20, 3.9% at 32.
	grenadeTouchSlack = 24.0

	// muzzleOffsetZ: a rocket leaves the shooter at origin + v_forward*8 +
	// '0 0 16' (ktx/src/weapons.c:1086-1088). The tracked flights carry
	// their own spawn origin; this is for the TRACKLESS ones, whose only
	// known source point is the shooter's track position.
	muzzleOffsetZ = 16.0

	// directHullNear bounds how far the detonation point may sit from the
	// hull for a touch to be entertained AT ALL. It is a sanity gate on the
	// magnitude prior below, not a verdict of its own: measured on the
	// dm2/dm3 ground truth, 96.6% of wire-flagged direct rocket rows sit
	// within 32 units of the victim's hull and 97.8% within 48.
	directHullNear = 40.0
)

// axisGap is the distance from v to the interval [lo, hi], zero inside it.
func axisGap(v, lo, hi float64) float64 {
	switch {
	case v < lo:
		return lo - v
	case v > hi:
		return v - hi
	}
	return 0
}

// hullDist is the distance from p to the player hull standing at vpos, zero
// when p is inside it. Unlike a distance to the track origin it does not
// depend on where in the box that origin sits: a grenade resting on the
// floor at a player's feet is 0 units from the hull and 24 from the origin.
func hullDist(p, vpos vec3) float64 {
	dx := axisGap(p.x, vpos.x-hullHalf, vpos.x+hullHalf)
	dy := axisGap(p.y, vpos.y-hullHalf, vpos.y+hullHalf)
	dz := axisGap(p.z, vpos.z+hullMinZ, vpos.z+hullMaxZ)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// slabClip is one axis of the slab test: it narrows [*t0, *t1] to the span
// of the ray a + t*d that lies inside [lo, hi], and reports whether anything
// survives.
func slabClip(a, d, lo, hi float64, t0, t1 *float64) bool {
	if math.Abs(d) < 1e-9 {
		return a >= lo && a <= hi
	}
	n, f := (lo-a)/d, (hi-a)/d
	if n > f {
		n, f = f, n
	}
	if n > *t0 {
		*t0 = n
	}
	if f < *t1 {
		*t1 = f
	}
	return *t0 <= *t1
}

// segHitsHull reports whether the segment a→b crosses the player hull at
// vpos grown by slack on every axis.
func segHitsHull(a, b, vpos vec3, slack float64) bool {
	d := b.sub(a)
	t0, t1 := 0.0, 1.0
	return slabClip(a.x, d.x, vpos.x-hullHalf-slack, vpos.x+hullHalf+slack, &t0, &t1) &&
		slabClip(a.y, d.y, vpos.y-hullHalf-slack, vpos.y+hullHalf+slack, &t0, &t1) &&
		slabClip(a.z, d.z, vpos.z+hullMinZ-slack, vpos.z+hullMaxZ+slack, &t0, &t1)
}

// directImpact decides, from geometry alone, whether the explosion at ep
// touched the victim standing at vpos. `from` is the point the projectile
// flew FROM — the tracked flight's spawn origin, or the shooter's muzzle for
// a trackless one.
//
// isSelf short-circuits to splash: a missile never touches its own owner —
// both touch handlers return immediately on `other == owner`
// (ktx/src/weapons.c:951, :1315) — so a self rocket or grenade is radius
// damage by construction and the question does not arise.
//
// A grenade is decided by the detonation point alone. It detonates early
// only by touching a player, so where it detonated IS where it touched;
// running the rocket's trajectory test on it would be wrong twice over,
// since a grenade lobs and bounces and spawn→despawn is not its path.
//
// A rocket is decided by its trajectory: the straight line through the two
// flight endpoints, followed forward past the detonation, must cross the
// hull. That is the engine's own touch test — the server traces the point
// entity against the player box — reconstructed from the two positions the
// wire broadcast.
func directImpact(weapon string, from, ep, vpos vec3, isSelf bool) bool {
	if isSelf {
		return false
	}
	if weapon != "rl" {
		return hullDist(ep, vpos) <= grenadeTouchSlack
	}
	d := ep.sub(from)
	l := d.length()
	if l < directMinFlight {
		// No usable direction: a point-blank rocket whose spawn and
		// detonation are a few units apart. Fall back to the endpoint,
		// which at that range is the touch point anyway.
		return hullDist(ep, vpos) <= grenadeTouchSlack
	}
	f := directFwdReach / l
	b := vec3{ep.x + d.x*f, ep.y + d.y*f, ep.z + d.z*f}
	return segHitsHull(ep, b, vpos, hullSlack)
}

// rocketTouched folds the MAGNITUDE prior over a rocket's trajectory
// verdict. It is the second of the two mutually-disambiguating signals, and
// on a modern server it is the sharper one — dropping it costs the derived
// counter a factor of twenty in aggregate error (0.65% -> 15.4%).
//
// A direct rocket deals its flat constant and nothing else (the victim is
// T_RadiusDamage's `ignore`, ktx/src/weapons.c:998-1006) while splash is
// 120 − 0.5·dist. So on a server whose direct value is the fixed 110
// (KTX >= 1.36) the two curves cross at exactly one distance, and an
// observation reading 110·quad is a touch: measured over 3 275 wire rl rows
// on the dm2/dm3 ground truth, 623 of 623 direct rows read exactly 110 or
// 440 and exactly ONE splash row did.
//
// The observation it reads is the delta's RAW value, which for a survived
// hit is the wire value itself — the extraction sums the health and armor
// drops, and the engine's split is exact for an integer damage
// (`save = ceil(armortype*damage)`, `take = ceil(damage - save)`,
// ktx/src/combat.c:634-655). That is why a survived hit whose raw is not the
// constant is refused outright: it cannot have been a lone touch. A KILLING
// hit carries no such guarantee — the −99 corpse clamp hides overkill past
// it — so there the trajectory keeps the last word.
//
// On a pre-1.36 server (`100 + g_random()*20`, replaced by ktx commit
// c7263e8f, 2008-09-29) the direct value overlaps the close-range splash
// curve and the prior says nothing; the trajectory is then the whole answer.
// Which regime a demo's server ran is measured from the demo rather than
// read off a version string — see detectRocketRegime.
func (in *inputs) rocketTouched(geomDirect, hullNear bool, d delta, q float64) bool {
	if !hullNear {
		return false
	}
	dirLo, dirHi := in.directBase()
	if dirLo != dirHi {
		return geomDirect
	}
	if d.raw == int(dirLo*q+0.5) {
		return true
	}
	if !d.died && !d.masked {
		return false
	}
	return geomDirect
}

// stampRocketVerdict re-decides one attributed rocket event's direct/splash
// flag, folding the magnitude prior over the trajectory verdict the winning
// candidate carries. Rockets only: a grenade deals nothing on touch, so its
// magnitude says nothing about whether it touched, and a self hit is splash
// by construction.
func (in *inputs) stampRocketVerdict(e *reconEvent, c *candidate, d delta) {
	if c.kind != "proj" || c.weapon != "rl" || e.isSelf {
		return
	}
	e.isSplash = !in.rocketTouched(!c.isSplash, c.hullNear, d, in.quadFactor(c.attacker, d.t))
}
