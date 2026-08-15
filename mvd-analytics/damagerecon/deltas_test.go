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
	selfPen, selfOK := in.damageModelScore(110, false, "rl", "rl-sound", -1, true, false)
	enemyPen, enemyOK := in.damageModelScore(110, false, "rl", "rl-sound", -1, false, false)
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

func TestDamageModelScoreQuadBeforeFalloff(t *testing.T) {
	in := &inputs{rlLo: 110, rlHi: 110}
	// Quad splash at 200u: engine computes 440 - 100 = 340 (base×4 first).
	// The wrong order 4×(110-100) = 40 would reject 340.
	if pen, ok := in.damageModelScore(340, false, "rl", "proj", 200, false, true); !ok || pen != 0 {
		t.Fatalf("quad splash 340 at 200u must fit exactly, got pen=%v ok=%v", pen, ok)
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
