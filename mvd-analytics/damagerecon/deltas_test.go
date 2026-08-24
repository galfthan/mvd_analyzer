package damagerecon

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func ch(pairs ...int32) []result.ChangeI16 {
	var out []result.ChangeI16
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, result.ChangeI16{T: pairs[i], V: int16(pairs[i+1])})
	}
	return out
}

func TestVictimDeltasBasicDrop(t *testing.T) {
	p := &result.PlayerStream{
		Health: ch(0, 100, 1000, 75),
	}
	d := victimDeltas(p, nil)
	if len(d) != 1 || d[0].t != 1000 || d[0].bounded != 25 || d[0].raw != 25 || d[0].died {
		t.Fatalf("got %+v", d)
	}
}

func TestVictimDeltasMergedArmorHealth(t *testing.T) {
	p := &result.PlayerStream{
		Health: ch(0, 100, 1000, 80),
		Armor:  ch(0, 100, 1000, 70),
	}
	d := victimDeltas(p, nil)
	if len(d) != 1 || d[0].bounded != 50 || d[0].raw != 50 {
		t.Fatalf("got %+v", d)
	}
}

func TestVictimDeltasMegaRotSkipped(t *testing.T) {
	p := &result.PlayerStream{
		Health: ch(0, 100, 500, 200, 1500, 199, 2500, 198),
	}
	if d := victimDeltas(p, nil); len(d) != 0 {
		t.Fatalf("rot ticks must not be damage, got %+v", d)
	}
}

func TestVictimDeltasKillingHitCapsBoundedExtendsRaw(t *testing.T) {
	// 30 health, killed by a hit leaving -70: bounded caps at 30, raw
	// carries the overkill (100 total).
	p := &result.PlayerStream{
		Health: ch(0, 100, 500, 30, 1000, -70),
		Deaths: []int32{1000},
	}
	d := victimDeltas(p, nil)
	if len(d) != 2 {
		t.Fatalf("got %+v", d)
	}
	k := d[1]
	if !k.died || k.bounded != 30 || k.raw != 100 {
		t.Fatalf("kill delta got %+v", k)
	}
}

func TestVictimDeltasCorpseHitRawOnly(t *testing.T) {
	// Corpse at -10 gibbed to -99: raw counts the drop, bounded nothing.
	p := &result.PlayerStream{
		Health: ch(0, 100, 1000, -10, 2000, -99),
		Deaths: []int32{1000},
	}
	d := victimDeltas(p, nil)
	if len(d) != 2 {
		t.Fatalf("got %+v", d)
	}
	g := d[1]
	if g.bounded != 0 || g.raw != 89 || g.died {
		t.Fatalf("corpse delta got %+v", g)
	}
}

func TestVictimDeltasSpawnResetNotDamage(t *testing.T) {
	p := &result.PlayerStream{
		Health: ch(0, 100, 1000, -10, 3000, 100),
		Deaths: []int32{1000},
		Spawns: []int32{3000},
	}
	d := victimDeltas(p, nil)
	if len(d) != 1 || d[0].t != 1000 {
		t.Fatalf("spawn rise must not produce a delta, got %+v", d)
	}
}

func TestVictimDeltasMaskedDeathRespawnSameInstant(t *testing.T) {
	// Full-health telefrag victim respawning the same instant: no health
	// row at all at the kill (100→100 dedups), only death+spawn coincide.
	p := &result.PlayerStream{
		Health: ch(0, 100),
		Armor:  ch(0, 50, 5000, 0),
		Deaths: []int32{5000},
		Spawns: []int32{5000},
	}
	d := victimDeltas(p, nil)
	if len(d) != 1 {
		t.Fatalf("got %+v", d)
	}
	m := d[0]
	if !m.masked || !m.died || m.bounded != 150 || m.raw != 150 {
		t.Fatalf("masked kill got %+v (want armor+health = 150)", m)
	}
}

func TestVictimDeltasCorpseMaskedAnchoredKill(t *testing.T) {
	// Respawn+instant-kill while the tracked state is a corpse: only a
	// frag anchor proves it; charged at spawn capacity.
	p := &result.PlayerStream{
		Health: ch(0, 100, 1000, -99),
		Deaths: []int32{1000, 3000},
	}
	d := victimDeltas(p, map[int32]bool{3000: true})
	if len(d) != 2 {
		t.Fatalf("got %+v", d)
	}
	m := d[1]
	if !m.masked || m.bounded != 100 || m.raw != 100 {
		t.Fatalf("corpse-masked kill got %+v", m)
	}
}

func TestVictimDeltasPentNullifiedRawRecovered(t *testing.T) {
	// Pent holder with RA: armor-only drop of 88 = ceil(0.8*damage); the
	// raw family recovers ~damage = 110.
	p := &result.PlayerStream{
		Health:    ch(0, 100),
		Armor:     ch(0, 200, 1000, 112),
		ArmorType: []result.ChangeStr{{T: 0, V: "ra"}},
	}
	d := victimDeltas(p, nil)
	if len(d) != 1 {
		t.Fatalf("got %+v", d)
	}
	if d[0].bounded != 88 {
		t.Fatalf("bounded must be the armor share, got %+v", d[0])
	}
	if d[0].raw != 110 {
		t.Fatalf("raw must recover save/frac = 110, got %+v", d[0])
	}
}

func TestDamageModelScoreSelfRocketCeiling(t *testing.T) {
	in := &inputs{rlLo: 110, rlHi: 110}
	// 110 observed as SELF splash is impossible (ceiling ~55): must carry a
	// heavy penalty relative to the enemy explanation.
	c := &candidate{weapon: "rl", kind: "rl-sound", dEnd: -1}
	selfPen, selfOK := in.damageModelScore(110, false, c, true, false)
	enemyPen, enemyOK := in.damageModelScore(110, false, c, false, false)
	if !selfOK || !enemyOK {
		t.Fatalf("both should be scoreable")
	}
	if enemyPen != 0 {
		t.Fatalf("enemy direct 110 must be a perfect fit, got %v", enemyPen)
	}
	if selfPen <= 0 {
		t.Fatalf("self 110 must be penalized, got %v", selfPen)
	}
}

func TestDamageModelScoreQuadAfterFalloff(t *testing.T) {
	in := &inputs{rlLo: 110, rlHi: 110}
	// Quad splash at 100u: the engine subtracts the falloff first and
	// multiplies afterwards — T_RadiusDamageApply computes 120 − 0.5·100 = 70
	// (ktx/src/combat.c:1189) and T_Damage applies the ×4 (:537-543), so the
	// hit reads 280. The wrong order (4×120 − 50 = 430) cannot produce a
	// value under 400 anywhere inside the engine's 160-unit reach, which is
	// where 96% of the wire's own quad rl splash rows sit.
	//
	// The base is the T_RadiusDamage argument — 120 for a rocket as for a
	// grenade (ktx/src/weapons.c:1006) — NOT the 110 the touch deals; and
	// isSplash is what selects the falloff branch at all now that the
	// direct/splash verdict is a trajectory question rather than a distance
	// one (direct.go). A rocket 100 units away has plainly touched nobody.
	c := &candidate{weapon: "rl", kind: "proj", dEnd: 100, isSplash: true}
	if pen, ok := in.damageModelScore(280, false, c, false, true); !ok || pen != 0 {
		t.Fatalf("quad splash 280 at 100u must fit exactly, got pen=%v ok=%v", pen, ok)
	}
	if pen, _ := in.damageModelScore(430, false, c, false, true); pen == 0 {
		t.Fatal("the old base-first form's 430 must no longer be a perfect fit")
	}
}

// TestSplashBandPositiveInsideAdmission pins the tie between the admission
// radius and the damage model: splashAdmit is the engine's reach plus the
// same slack the band widens by, so no admitted candidate can reach the
// distance at which the falloff would zero the band's high end (240 units).
// modelBounds therefore carries no non-positive-band rejection — this is the
// invariant that would have to fail first for one to be needed.
func TestSplashBandPositiveInsideAdmission(t *testing.T) {
	in := &inputs{rlLo: 110, rlHi: 110}
	for _, exact := range []bool{false, true} {
		for _, self := range []bool{false, true} {
			for _, quad := range []bool{false, true} {
				for d := 0.0; d <= splashAdmit(exact); d += 0.5 {
					c := &candidate{weapon: "rl", kind: "proj", dEnd: d, isSplash: true, epExact: exact}
					lo, hi, ok := in.modelBounds(c, self, quad)
					if !ok || hi <= 0 || lo <= 0 || lo > hi {
						t.Fatalf("d=%.1f exact=%v self=%v quad=%v: band [%v,%v] ok=%v",
							d, exact, self, quad, lo, hi, ok)
					}
				}
			}
		}
	}
}

// TestSelfHalvingBeforeMultiplication pins the ORDER of the three factors a
// self splash goes through, which is the whole of what lead A corrected.
// T_RadiusDamageApply subtracts the falloff from the base, halves THOSE
// points when the damaged entity is the attacker (ktx/src/combat.c:1189-1193),
// and only then does T_Damage multiply by the quad (:537-543).
//
// At 100 units under quad that is (120 − 50)·0.5·4 = 140. The two orderings
// this package has actually shipped or had proposed produce different
// numbers, so each is pinned as a NON-fit rather than left to a comment:
// halving the base first (60 − 50)·4 = 40, and the "self splash = D −
// 0.25·dist" form damageModelScore's comment used to describe,
// (120 − 25)·4 = 380.
func TestSelfHalvingBeforeMultiplication(t *testing.T) {
	if got := splashModel(100, 4, 0.5); got != 140 {
		t.Fatalf("quad self splash at 100u must be (120-50)*0.5*4 = 140, got %v", got)
	}
	// The quad and the halving commute with each other but NOT with the
	// falloff: dropping the falloff to the far side changes the value.
	if splashModel(100, 4, 0.5) == (120*4-0.5*100)*0.5 {
		t.Fatal("the base-first ordering must not reproduce the engine's value")
	}
	in := &inputs{rlLo: 110, rlHi: 110}
	c := &candidate{weapon: "rl", kind: "proj", dEnd: 100, isSplash: true}
	if pen, ok := in.damageModelScore(140, false, c, true, true); !ok || pen != 0 {
		t.Fatalf("140 must fit the quad self band exactly, got pen=%v ok=%v", pen, ok)
	}
	for _, wrong := range []int{40, 380} {
		if pen, _ := in.damageModelScore(wrong, false, c, true, true); pen == 0 {
			t.Fatalf("%d comes from a different factor ordering and must not fit", wrong)
		}
	}
}

// TestSplashAdmissionBoundary pins the engine reach + slack admission cut on
// both endpoint kinds, through projCandidates rather than through
// splashAdmit alone — the boundary only means something where it is applied.
// 184 for a TE_EXPLOSION-snapped detonation point, 220 for a tracked flight's
// last broadcast position (splashSlack).
func TestSplashAdmissionBoundary(t *testing.T) {
	vpos := vec3{0, 0, 0}
	for _, tc := range []struct {
		exact  bool
		accept float64
		reject float64
	}{
		{true, 183, 185},
		{false, 219, 221},
	} {
		for _, d := range []float64{tc.accept, tc.reject} {
			in := &inputs{projs: []projectile{{
				weapon: "rl", shooter: "a", endT: 1000,
				sp: vec3{d + 500, 0, 0}, ep: vec3{d, 0, 0}, epExact: tc.exact,
			}}}
			got := len(in.projCandidates("b", 1000, vpos))
			want := 1
			if d == tc.reject {
				want = 0
			}
			if got != want {
				t.Errorf("exact=%v d=%.0f: %d candidates, want %d (splashAdmit=%.0f)",
					tc.exact, d, got, want, splashAdmit(tc.exact))
			}
		}
	}
}

// TestDischargeReachGate pins the LG water discharge's own admission bound:
// T_RadiusDamage visits findradius(damage + 40) for the blast exactly as it
// does for a rocket, so a 100-cell discharge reaches 35·100 + 40 and no
// further. The falloff alone does not give that bound — 35·cells − 0.5·dist
// stays positive out to nearly twice it, which is the population this gate
// removes.
func TestDischargeReachGate(t *testing.T) {
	const cells = 100
	if got, want := dischargeReach(cells), 35.0*cells+40.0+splashSlack(false); got != want {
		t.Fatalf("dischargeReach(%d) = %v, want %v", cells, got, want)
	}
	mk := func(d float64) *inputs {
		return &inputs{
			discharges: []discharge{{t: 1000, player: "a", cells: cells}},
			tracks: map[string]*track{"a": {pt: &result.PositionTrack{
				T: []int32{0, 2000}, X: []float32{float32(d), float32(d)},
				Y: []float32{0, 0}, Z: []float32{0, 0},
			}}},
			players: map[string]*result.PlayerStream{},
		}
	}
	inside := dischargeReach(cells) - 1
	if got := len(mk(inside).dischargeCandidates("b", 1000, vec3{})); got != 1 {
		t.Errorf("a discharge %.0fu away is inside the blast: %d candidates, want 1", inside, got)
	}
	outside := dischargeReach(cells) + 1
	if got := len(mk(outside).dischargeCandidates("b", 1000, vec3{})); got != 0 {
		t.Errorf("a discharge %.0fu away is past findradius: %d candidates, want 0", outside, got)
	}
	// ...and the falloff would still have called it damaging: 35*100 - 0.5*d
	// is comfortably positive there, which is why the gate has to be explicit.
	if 35.0*cells-0.5*outside <= 0 {
		t.Fatal("the falloff must still be positive past the reach for this pin to mean anything")
	}
}

// TestKillFloorSplitsOnVerdict pins that both kill top-ups price a DIRECT
// rocket at T_MissileTouch's flat constant and everything else on the radius
// curve. The engine hands the touched entity to T_RadiusDamage as its
// `ignore` (ktx/src/weapons.c:998-1006), so a point-blank quad direct is 440,
// never the 480 the radius curve would top it up to.
func TestKillFloorSplitsOnVerdict(t *testing.T) {
	in := &inputs{rlLo: 110, rlHi: 110}
	if got := in.killModelFloor("rl", false, false, 0, 4); got != 440 {
		t.Errorf("point-blank quad DIRECT rocket floor = %v, want 440", got)
	}
	if got := in.killModelFloor("rl", true, false, 0, 4); got != 480 {
		t.Errorf("point-blank quad SPLASH rocket floor = %v, want 480", got)
	}
	// A grenade has no touch value at all — GrenadeTouch damages only through
	// T_RadiusDamage (:1327-1333) — so its verdict must not move the curve.
	if in.killModelFloor("gl", false, false, 60, 1) != in.killModelFloor("gl", true, false, 60, 1) {
		t.Error("a grenade rides the radius curve whether or not it touched")
	}
	// The self-halving reaches the top-ups too, on the same side of the quad
	// as everywhere else.
	if got := in.killModelFloor("rl", true, true, 100, 4); got != 140 {
		t.Errorf("quad self splash floor at 100u = %v, want 140", got)
	}
	// A floor may only claim the direct range's LOW end: on a pre-1.36 server
	// the constant is a roll, and over-raising is the one thing a raise-only
	// top-up cannot undo.
	vanilla := &inputs{rlLo: 100, rlHi: 120}
	if got := vanilla.killModelFloor("rl", false, false, 0, 1); got != 100 {
		t.Errorf("spread-regime direct floor = %v, want the low end 100", got)
	}
}

// TestGeomPriorIsNotAdmission pins the decoupling: the geometry PRIOR scores
// on its own normalizer, so changing the admission radius re-admits or
// refuses candidates without silently re-weighting the ones that stay. It
// also pins that the prior no longer depends on epExact — scoring an
// un-snapped endpoint BETTER than an exact one at the same distance was an
// artifact of sharing splashAdmit, never a stated intent.
func TestGeomPriorIsNotAdmission(t *testing.T) {
	vpos := vec3{0, 0, 0}
	mk := func(d float64, exact bool) candidate {
		in := &inputs{projs: []projectile{{
			weapon: "rl", shooter: "a", endT: 1000,
			sp: vec3{d + 500, 0, 0}, ep: vec3{d, 0, 0}, epExact: exact,
		}}}
		c := in.projCandidates("b", 1000, vpos)
		if len(c) != 1 {
			t.Fatalf("d=%.0f exact=%v: want one candidate, got %d", d, exact, len(c))
		}
		return c[0]
	}
	if a, b := mk(100, true), mk(100, false); a.geom != b.geom {
		t.Errorf("endpoint kind must not change the geometry prior: %v vs %v", a.geom, b.geom)
	}
	if got, want := mk(100, true).geom, 100.0/geomNorm*0.5; got != want {
		t.Errorf("geom prior = %v, want %v", got, want)
	}
	// Monotone in distance and bounded well under the fixed-geom kinds' own
	// priors (beam 0.3+, discharge 0.1, env 0.12) at the reach — the balance
	// the normalizer is measured against.
	if mk(20, true).geom >= mk(150, true).geom {
		t.Error("the prior must grow with distance")
	}
}

func TestDamageModelScoreEnvExactFitOnly(t *testing.T) {
	in := &inputs{}
	// Environmental tick values are engine-exact: a landing is 5, and any
	// other value must be INFEASIBLE (not merely penalized) so a lone fall
	// candidate can never absorb an unexplained delta.
	c := &candidate{weapon: "fall", kind: "env", dEnd: -1, mLo: 5, mHi: 5}
	if pen, ok := in.damageModelScore(5, false, c, false, false); !ok || pen != 0 {
		t.Fatalf("a flat-5 landing must fit exactly, got pen=%v ok=%v", pen, ok)
	}
	if _, ok := in.damageModelScore(155, true, c, false, false); ok {
		t.Fatalf("a 155 delta must be infeasible as fall damage")
	}
}

func TestVictimWeaponClassBoundaries(t *testing.T) {
	p := &result.PlayerStream{
		RL: []result.Interval{{Start: 1000, End: 5000}},
	}
	// Inventory sampled mid-frame: the interval closing at t still covers a
	// hit AT t (death reset broadcasts after the hit), and one opening at t
	// does not yet (same-frame pickup).
	if got := victimWeaponClass(p, 5000); got != "rl" {
		t.Fatalf("kill at interval end must still be rl, got %q", got)
	}
	if got := victimWeaponClass(p, 1000); got != "sg" {
		t.Fatalf("hit at pickup instant must not be rl yet, got %q", got)
	}
	if got := victimWeaponClass(p, 3000); got != "rl" {
		t.Fatalf("mid-interval must be rl, got %q", got)
	}
}

// TestQuadRegimeGateIsNarrow pins the deathmatch-4 stand-down. KTX makes the
// quad an OCTA there (ktx/src/combat.c:541) and this package models a flat
// ×4, so a dmm4 recording that contains a quad would publish every quad hit
// at about half value under source:"reconstructed" with no marker. It fires
// on that pair alone: quad-less dmm4 (the povdmm4 duels are the population)
// stays analyzable, and it never touches a non-dmm4 demo.
func TestQuadRegimeGateIsNarrow(t *testing.T) {
	mk := func(dm string, quad bool) *result.Result {
		p := result.PlayerStream{Name: "a"}
		if quad {
			p.Quad = []result.Interval{{Start: 1000, End: 3000}}
		}
		return &result.Result{
			Metadata: &result.MetadataResult{ServerInfo: map[string]string{"deathmatch": dm}},
			Streams:  &result.Streams{Players: []result.PlayerStream{p}},
		}
	}
	if got := ReconSkipReason(mk("4", true)); got != "dmm4-quad" {
		t.Errorf("dmm4 with a quad on the wire must stand down, got %q", got)
	}
	if got := ReconSkipReason(mk("4", false)); got != "" {
		t.Errorf("quad-less dmm4 stays analyzable, got %q", got)
	}
	if got := ReconSkipReason(mk("3", true)); got != "" {
		t.Errorf("the ×4 is right outside dmm4, got %q", got)
	}
	if got := ReconSkipReason(mk("", true)); got != "" {
		t.Errorf("no deathmatch key is not dmm4, got %q", got)
	}
	// The gate is damagerecon's alone: SkipModeReason also gates the KTX-side
	// bounded pass, which reads the server's own values and does not care
	// which multiplier produced them.
	if got := SkipModeReason(map[string]string{"deathmatch": "4"}); got != "" {
		t.Errorf("the shared server-mode detection must not learn about dmm4, got %q", got)
	}
}
