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
