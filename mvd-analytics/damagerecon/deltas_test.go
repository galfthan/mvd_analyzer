package damagerecon

import (
	"math"
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

// staticTrack: a player parked at p for the whole match.
func staticTrack(p vec3) *track {
	return &track{pt: &result.PositionTrack{
		T: []int32{0, 5000},
		X: []float32{float32(p.x), float32(p.x)},
		Y: []float32{float32(p.y), float32(p.y)},
		Z: []float32{float32(p.z), float32(p.z)},
	}}
}

// waterFightInputs: the dm4/e1m2 pool geometry the discharge pair exists for.
// "v" is the victim at the origin, "a" discharges 10 cells 400 units away
// (35·10 − 0.5·400 = 150 expected, inside the 450-unit findradius), and "b"
// lands a rocket that detonates 100 units off the victim (a 58..82 splash).
// Neither range alone covers a 200-point merge; together they do.
func waterFightInputs() *inputs {
	return &inputs{
		order:      []string{"v", "a", "b"},
		players:    map[string]*result.PlayerStream{},
		discharges: []discharge{{t: 1000, player: "a", cells: 10}},
		projs: []projectile{{
			weapon: "rl", shooter: "b", endT: 1000,
			sp: vec3{0, 600, 0}, ep: vec3{0, 100, 0}, epExact: true,
		}},
		tracks: map[string]*track{
			"v": staticTrack(vec3{}),
			"a": staticTrack(vec3{400, 0, 0}),
			"b": staticTrack(vec3{0, 600, 0}),
		},
	}
}

// TestSplitPairAdmitsDischarge pins the LG water discharge as a member of the
// same-frame pair split. A discharge is radius damage exactly as a rocket is
// (T_RadiusDamage(self, self, 35*cells, world, dtLG_DIS),
// ktx/src/weapons.c:1208, :1225), so a pool fight where one player discharges
// while another's rocket lands on the same victim in the same server frame
// merges into a single h/a delta with two authors — and before the family was
// in trySplitPair's list that delta could only ever be charged to one of them.
func TestSplitPairAdmitsDischarge(t *testing.T) {
	in := waterFightInputs()
	evs, ok := in.trySplitPair("v", in.tracks["v"], delta{t: 1000, raw: 200, bounded: 200})
	if !ok {
		t.Fatal("discharge + enemy rocket did not split a 200-point merge")
	}
	if len(evs) != 2 {
		t.Fatalf("split returned %d events, want 2", len(evs))
	}
	byAttacker := map[string]reconEvent{}
	for _, e := range evs {
		byAttacker[e.attacker] = e
	}
	dis, okA := byAttacker["a"]
	rock, okB := byAttacker["b"]
	if !okA || !okB {
		t.Fatalf("split authors = %v, want one share each for a and b", byAttacker)
	}
	if dis.kind != "discharge" || dis.weapon != "lg" || !dis.isSplash {
		t.Errorf("a's share = %+v, want a splash lg discharge", dis)
	}
	if rock.kind != "proj" || rock.weapon != "rl" {
		t.Errorf("b's share = %+v, want a proj rl", rock)
	}
	if dis.bounded+rock.bounded != 200 {
		t.Errorf("shares %d + %d do not sum to the observed 200", dis.bounded, rock.bounded)
	}
	// Each share has to land inside its own model range, which is the whole
	// point of splitting rather than halving: the rocket's splash band at
	// 100 units is 58..82, the discharge's is 0.75..1.1 around 150.
	if rock.bounded < 58 || rock.bounded > 82 {
		t.Errorf("rocket share %d outside the 58..82 splash band at 100u", rock.bounded)
	}
	if dis.bounded < 102 || dis.bounded > 175 {
		t.Errorf("discharge share %d outside the band around the 150 expected", dis.bounded)
	}
}

// TestDischargeNeverPairsWithOwnBeam pins an ENGINE invariant, not a code
// branch: a discharging LG fire deals no beam damage in the same frame, so a
// discharge and a beam from the SAME attacker can never both explain one
// delta. W_FireLightning's underwater branch zeroes ammo_cells and returns
// before WS_Mark(wpLG), before the TE_LIGHTNING2 multicast and before
// LightningDamage (ktx/src/weapons.c:1174-1229); every later call then takes
// the `ammo_cells < 1` early return (:1163).
//
// trySplitPair's different-attackers guard already enforces it, and the test
// is built so that guard is load-bearing: the discharger's own beam passes
// straight through the victim (geom 0), so the forbidden a+a pair scores
// BETTER than the legal a+b one and would win without it.
func TestDischargeNeverPairsWithOwnBeam(t *testing.T) {
	in := waterFightInputs()
	in.beams = []beam{{t: 1000, s: vec3{400, 0, 0}, e: vec3{-100, 0, 0}}}
	evs, ok := in.trySplitPair("v", in.tracks["v"], delta{t: 1000, raw: 200, bounded: 200})
	if !ok {
		t.Fatal("the legal discharge + enemy rocket pair must still split")
	}
	if evs[0].attacker == evs[1].attacker {
		t.Fatalf("split charged both shares to %q: a discharge and a beam cannot co-occur", evs[0].attacker)
	}
	for _, e := range evs {
		if e.attacker == "a" && e.kind == "beam" {
			t.Fatalf("the discharger's own beam was paired with their discharge: %+v", e)
		}
	}
	// The beam candidate really is present and really is the cheaper partner —
	// without it this test would pass for the wrong reason.
	cands := in.beamCandidates("v", 1000, vec3{})
	if len(cands) != 1 || cands[0].attacker != "a" || cands[0].geom != 0 {
		t.Fatalf("beam candidates = %+v, want one geom-0 candidate from a", cands)
	}
}

// TestAttributeDeltaSplitsDischargeMerge drives the water fight through the
// FULL delta path rather than calling trySplitPair directly, which is the only
// way to see the two gates between a discharge and the split:
//
//   - attributeDelta's trigger switch. A discharge is the cheaper geometric
//     fit, so it wins the single pass on a merge (here total 1.63 against the
//     rocket's 8.8) — and while "discharge" was missing from that switch the
//     whole 200-point delta was charged to the discharger and the pair code
//     was unreachable from production;
//   - the misfit probe, which rebuilds a candidate from the event. A
//     discharge's model band is per-candidate state, so it has to travel on
//     the event (reconEvent.mLo/mHi); without it modelBounds answers "no
//     opinion" and the probe reads a perfect fit.
func TestAttributeDeltaSplitsDischargeMerge(t *testing.T) {
	in := waterFightInputs()
	evs := in.attributeDelta("v", in.tracks["v"], delta{t: 1000, raw: 200, bounded: 200})
	if len(evs) != 2 {
		t.Fatalf("attributeDelta returned %d events, want the 2-author split; got %+v", len(evs), evs)
	}
	byAttacker := map[string]reconEvent{}
	for _, e := range evs {
		byAttacker[e.attacker] = e
	}
	if _, ok := byAttacker["a"]; !ok {
		t.Errorf("no share charged to the discharger: %+v", evs)
	}
	if _, ok := byAttacker["b"]; !ok {
		t.Errorf("no share charged to the rocketeer: %+v", evs)
	}
	// The single pass really does hand the delta to the discharge — without
	// that this test would pass through the ordinary "proj" trigger and pin
	// nothing about the discharge gate.
	one := in.attributeOne("v", in.tracks["v"], delta{t: 1000, raw: 200, bounded: 200})
	if one.kind != "discharge" || one.attacker != "a" {
		t.Fatalf("single-pass winner = %s/%s, want the discharge from a", one.attacker, one.kind)
	}
	if one.mLo <= 0 || one.mHi <= one.mLo {
		t.Fatalf("winner carries no model band (mLo=%v mHi=%v): the misfit probe cannot see the misfit", one.mLo, one.mHi)
	}
}

// TestDischargeGeomPricedByRange pins the discharge geometry prior as a
// DISTANCE, not a constant. A 20-cell blast reaches ~740 units, so a flat prior
// made a discharge that merely happened somewhere in the pool the cheapest
// candidate on the board — cheaper than a rocket detonating on the victim's
// head — and its only guard was the ±25% value band, which is ~90 points wide
// at this magnitude and admits the quad rocket's range wholesale.
func TestDischargeGeomPricedByRange(t *testing.T) {
	in := &inputs{
		order:   []string{"v", "a", "b"},
		players: map[string]*result.PlayerStream{"b": {Quad: []result.Interval{{Start: 0, End: 5000}}}},
		// 20 cells at 700 units: 35·20 − 0.5·700 = 350 expected, band 252..395.
		discharges: []discharge{{t: 1000, player: "a", cells: 20}},
		// Quad rocket 100 units off the victim: 4·(120 − 0.5·d), band 232..328.
		projs: []projectile{{
			weapon: "rl", shooter: "b", endT: 1000,
			sp: vec3{0, 600, 0}, ep: vec3{0, 100, 0}, epExact: true,
		}},
		tracks: map[string]*track{
			"v": staticTrack(vec3{}),
			"a": staticTrack(vec3{700, 0, 0}),
			"b": staticTrack(vec3{0, 600, 0}),
		},
	}
	d := delta{t: 1000, raw: 300, bounded: 300}
	var cands []candidate
	cands = append(cands, in.projCandidates("v", d.t, vec3{})...)
	cands = append(cands, in.dischargeCandidates("v", d.t, vec3{})...)
	var dis, proj *candidate
	for i := range cands {
		switch cands[i].kind {
		case "discharge":
			dis = &cands[i]
		case "proj":
			proj = &cands[i]
		}
	}
	if dis == nil || proj == nil {
		t.Fatalf("want both a discharge and a proj candidate, got %+v", cands)
	}
	// Both explain the value perfectly — the tie is decided by geometry alone,
	// which is exactly the situation a flat prior gets wrong.
	for _, c := range []*candidate{dis, proj} {
		if pen, ok := in.damageModelScore(d.bounded, false, c, false, in.hasQuad(c.attacker, d.t)); !ok || pen != 0 {
			t.Fatalf("%s candidate does not fit 300 (pen=%v ok=%v); the test no longer isolates geometry", c.kind, pen, ok)
		}
	}
	if dis.geom <= proj.geom {
		t.Fatalf("discharge geom %v at 700u is not dearer than a rocket's %v at 100u", dis.geom, proj.geom)
	}
	best, ok := in.scoreCandidates("v", in.tracks["v"], d, cands)
	if !ok || best.kind != "proj" {
		t.Fatalf("winner = %+v, want the rocket that actually reached the victim", best)
	}
	// ... and the near-range pool fight the family exists for is untouched:
	// at ~52 units the prior is still the 0.1 it used to charge everywhere.
	in.tracks["a"] = staticTrack(vec3{52, 0, 0})
	near := in.dischargeCandidates("v", d.t, vec3{})
	if len(near) != 1 {
		t.Fatalf("near discharge candidates = %+v, want 1", near)
	}
	if math.Abs(near[0].geom-0.1) > 0.005 {
		t.Errorf("close-range discharge geom = %v, want ~0.1", near[0].geom)
	}
}

// tracklessGrenadeInputs: a merge whose second author has NO tracked flight
// at all. "g" lobs a grenade that detonates on contact 100 units from the
// victim within a frame of launch — too early for the entity to have been
// broadcast, so the only wire evidence is the fire sound plus the
// TE_EXPLOSION — while "s" lands an ssg blast in the same server frame.
//
// The grenade is the sharp case for the family: rlSoundCandidates covers
// "rl" only (a grenade lobs, so no flight model applies), so before
// explosionCandidates joined trySplitPair's list this instant had exactly
// ONE candidate author and could not be split however badly the single
// explanation misfit the value.
func tracklessGrenadeInputs() *inputs {
	return &inputs{
		order:      []string{"v", "g", "s"},
		players:    map[string]*result.PlayerStream{},
		explosions: []pointFx{{t: 1100, p: vec3{0, 100, 0}}},
		shots: []firedShot{
			{t: 1000, player: "g", weapon: "gl"},
			{t: 1100, player: "s", weapon: "ssg"},
		},
		tracks: map[string]*track{
			"v": staticTrack(vec3{}),
			"g": staticTrack(vec3{0, 300, 0}),
			"s": staticTrack(vec3{0, -800, 0}),
		},
	}
}

// TestAttributeDeltaSplitsTracklessExplosionMerge drives the trackless
// grenade + shotgun merge through the FULL delta path, not trySplitPair
// directly: a family in the candidate list is only half of it, and the
// discharge round shipped a pair that production could never reach because
// its test called the split function itself.
//
// The 130-point delta has two authors and neither can explain it alone: the
// blast's radius band at 100 units is 58..82 and the ssg's is 4..56, so the
// value only exists inside their 62..138 sum.
func TestAttributeDeltaSplitsTracklessExplosionMerge(t *testing.T) {
	in := tracklessGrenadeInputs()
	d := delta{t: 1100, raw: 130, bounded: 130}
	evs := in.attributeDelta("v", in.tracks["v"], d)
	if len(evs) != 2 {
		t.Fatalf("attributeDelta returned %d events, want the 2-author split; got %+v", len(evs), evs)
	}
	byAttacker := map[string]reconEvent{}
	for _, e := range evs {
		byAttacker[e.attacker] = e
	}
	gr, okG := byAttacker["g"]
	sg, okS := byAttacker["s"]
	if !okG || !okS {
		t.Fatalf("split authors = %v, want one share each for g and s", byAttacker)
	}
	if gr.kind != "proj" || gr.weapon != "gl" {
		t.Errorf("g's share = %+v, want a gl proj", gr)
	}
	if gr.dEnd != 100 {
		t.Errorf("g's share carries dEnd %v, want the measured 100 units to the detonation point", gr.dEnd)
	}
	if gr.bounded+sg.bounded != 130 {
		t.Errorf("shares %d + %d do not sum to the observed 130", gr.bounded, sg.bounded)
	}
	if gr.bounded < 58 || gr.bounded > 82 {
		t.Errorf("grenade share %d outside its 58..82 radius band at 100u", gr.bounded)
	}
	if sg.bounded < 4 || sg.bounded > 56 {
		t.Errorf("ssg share %d outside its 4..56 band", sg.bounded)
	}
	// The trigger really is reached through the ordinary "proj" arm: the
	// detonation wins the single pass and misfits the merged value.
	one := in.attributeOne("v", in.tracks["v"], d)
	if one.attacker != "g" || one.kind != "proj" {
		t.Fatalf("single-pass winner = %s/%s, want the trackless grenade", one.attacker, one.kind)
	}
}

// TestSplitPairPrefersTheMeasuredDetonation pins WHICH rocket candidate the
// split takes when both are on the board. A point-blank rocket has two
// possible explanations: rlSoundCandidates, which knows only that a fire
// sound is flight-time-consistent with the victim's position and therefore
// spans the whole 25..120 radius range, and explosionCandidates, which knows
// where the rocket actually went off. Only the second can price the share.
//
// Here the detonation is 100 units from the victim (a 58..82 splash) and the
// merge is 138 with an ssg (4..56). The exact candidate is both the cheaper
// geometry (0.25 against the fire sound's 0.49) and the only one that
// constrains the value; the guess would hand the rocketeer 98 of the 138.
//
// 138 is not free: the fixture works only inside a 135..138 window, and both
// edges are the point of the test.
//
//   - The single-pass winner here is the rl-sound guess, not the detonation —
//     at a merged value its 25..120 band misfits least. So the SPLIT is
//     entertained at all only once the probe's misfit clears 0.5 against
//     THAT band: (obs − 120) / max(10, 0.25·120) ≥ 0.5, i.e. obs ≥ 135.
//     Below it the delta returns one rl-sound event and nothing is measured.
//   - 138 is exactly 82 + 56, the two bands' tops. At 139 trySplitPair's ±1
//     feasibility slack still admits the pair but the share clamp has to push
//     the rocket to 83 — one point outside its own band, which the assertion
//     below rejects — and at 140 the measured pair is infeasible altogether,
//     so the split falls back to the rl-sound guess: the exact regression
//     this test exists to catch, arriving for the wrong reason.
//
// A failure here therefore means one of two things: the split stopped
// preferring the measured detonation (the regression), or a band/probe
// constant moved and the fixture's window no longer contains 138 — check the
// four numbers above before touching the assertions.
func TestSplitPairPrefersTheMeasuredDetonation(t *testing.T) {
	in := &inputs{
		order:      []string{"v", "a", "s"},
		players:    map[string]*result.PlayerStream{},
		explosions: []pointFx{{t: 1500, p: vec3{0, 100, 0}}},
		shots: []firedShot{
			{t: 1200, player: "a", weapon: "rl"},
			{t: 1500, player: "s", weapon: "ssg"},
		},
		tracks: map[string]*track{
			"v": staticTrack(vec3{}),
			"a": staticTrack(vec3{0, 400, 0}),
			"s": staticTrack(vec3{0, -800, 0}),
		},
	}
	d := delta{t: 1500, raw: 138, bounded: 138}
	// Both explanations for a really are present — without that this test
	// would pass for the wrong reason.
	if got := len(in.rlSoundCandidates("v", d.t, vec3{})); got != 1 {
		t.Fatalf("rl-sound candidates = %d, want the 1 this test is about", got)
	}
	evs := in.attributeDelta("v", in.tracks["v"], d)
	if len(evs) != 2 {
		t.Fatalf("attributeDelta returned %d events, want a 2-author split; got %+v", len(evs), evs)
	}
	byAttacker := map[string]reconEvent{}
	for _, e := range evs {
		byAttacker[e.attacker] = e
	}
	rock, ok := byAttacker["a"]
	if !ok {
		t.Fatalf("split authors = %v, want a share for the rocketeer", byAttacker)
	}
	if rock.kind != "proj" || rock.dEnd != 100 {
		t.Fatalf("a's share = %s (dEnd %v), want the measured detonation, not the rl-sound guess", rock.kind, rock.dEnd)
	}
	if rock.bounded < 58 || rock.bounded > 82 {
		t.Errorf("rocket share %d outside the 58..82 band the detonation point implies", rock.bounded)
	}
}

// directConstantInputs: a TRACKED enemy rocket whose detonation point measures
// 140 units from the victim's interpolated position — far enough that the
// radius band it implies (38..62) cannot explain a 110-point delta — plus a
// shotgun blast that could complete the pair. On a fixed-110 server that delta
// is not a merge at all: it is T_MissileTouch's flat constant, one whole hit.
//
// The flight is tracked rather than trackless on purpose: a fire SOUND would
// also raise an rlSoundCandidates explanation whose 25..120 band swallows a
// 110 without misfitting, and the delta would never reach the split at all.
func directConstantInputs() *inputs {
	return &inputs{
		order:    []string{"v", "a", "s"},
		players:  map[string]*result.PlayerStream{},
		rlRegime: result.RocketRegimeFixed,
		rlLo:     110,
		rlHi:     110,
		projs: []projectile{{
			weapon: "rl", shooter: "a", spawnT: 1700, endT: 2000,
			sp: vec3{0, 440, 0}, ep: vec3{0, 140, 0}, epExact: true,
		}},
		shots: []firedShot{{t: 2000, player: "s", weapon: "ssg"}},
		tracks: map[string]*track{
			"v": staticTrack(vec3{}),
			"a": staticTrack(vec3{0, 440, 0}),
			"s": staticTrack(vec3{0, -800, 0}),
		},
	}
}

// TestAttributeDeltaKeepsTheDirectConstantWhole pins the direct-constant
// exemption: a rocket delta of exactly the demo's measured direct constant is
// never challenged by the pair split.
//
// The misfit is real and the pair is feasible — that is the point. The
// winner's radius band at 140 units is 38..62 and the probe's (which rebuilds
// with the wider un-snapped slack) is 20..80, so a 110 misfits it by 30, and
// 110 sits inside the 42..119 the rocket and the ssg sum to. Every gate the
// split needs is open; only the constant closes it.
//
// The control at 109 is what makes this a test of the constant rather than of
// the geometry: one point off, the same fixture splits.
func TestAttributeDeltaKeepsTheDirectConstantWhole(t *testing.T) {
	in := directConstantInputs()
	d := delta{t: 2000, raw: 110, bounded: 110}
	one := in.attributeOne("v", in.tracks["v"], d)
	if one.attacker != "a" || one.weapon != "rl" {
		t.Fatalf("single-pass winner = %s/%s, want the rocketeer this test is about", one.attacker, one.weapon)
	}
	if evs := in.attributeDelta("v", in.tracks["v"], d); len(evs) != 1 {
		t.Errorf("a %d-point delta at the fixed direct constant returned %d events, want the single whole hit; got %+v",
			d.bounded, len(evs), evs)
	}
	off := delta{t: 2000, raw: 109, bounded: 109}
	if evs := in.attributeDelta("v", in.tracks["v"], off); len(evs) != 2 {
		t.Fatalf("one point off the constant returned %d events, want the split — the exemption has to be about the CONSTANT, not about this geometry; got %+v",
			len(evs), evs)
	}
	// A demo whose regime was never established keeps the old behaviour: the
	// constant is only known where the evidence established it.
	spread := directConstantInputs()
	spread.rlRegime, spread.rlLo, spread.rlHi = result.RocketRegimeSpread, 100, 120
	if evs := spread.attributeDelta("v", spread.tracks["v"], d); len(evs) != 2 {
		t.Errorf("on a demo with no fixed-110 regime the same delta returned %d events, want the split", len(evs))
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

// TestTeamTelefragRoutesPositional pins the team-telefrag channel that the
// 2026-08-26 classification pass found under "PHANTOM → team": KTX prints a
// teammate telefrag as "<victim> was telefragged by his teammate"
// (ktx/src/client.c:5355), which is a TEAMKILL obituary, so killerFragAt —
// which skips teamkills — never sees it and the kill used to fall through to
// the teamkill anchor as ordinary team weapon damage. The victim's health
// broadcast runs to KTX's -99 corpse clamp, so that booked the telefragger for
// the whole corpse drop instead of the victim's capacity: 199 raw where the
// wire's own telefrag row says 100.
func TestTeamTelefragRoutesPositional(t *testing.T) {
	mk := func(weapon string) *inputs {
		in := &inputs{
			players:   map[string]*result.PlayerStream{"v": {Name: "v"}, "a": {Name: "a"}},
			tracks:    map[string]*track{},
			teams:     map[string]string{"v": "red", "a": "red"},
			order:     []string{"a", "v"},
			fragAt:    map[fragKey]*result.FragEntry{},
			fragAnyAt: map[fragKey]*result.FragEntry{},
			rlLo:      100, rlHi: 120,
		}
		f := &result.FragEntry{Time: 1000, Killer: "a", Victim: "v", Weapon: weapon, IsTeamKill: true}
		in.fragAnyAt[fragKey{"v", 1000}] = f
		return in
	}
	// The victim died at 100 health; the corpse row reads -99, so the raw
	// observation carries 99 points of clamp the engine never dealt.
	d := delta{t: 1000, raw: 199, bounded: 100, died: true}

	e := mk("tele").attributeOne("v", nil, d)
	if e.kind != "positional" || e.weapon != "tele" {
		t.Fatalf("team telefrag routed as %s/%s, want positional/tele", e.kind, e.weapon)
	}
	if e.attacker != "a" {
		t.Errorf("attacker = %q, want the killer the obituary's recovery named", e.attacker)
	}
	if !e.isTeam {
		t.Errorf("a telefrag between teammates must stay in the team family")
	}
	// aggregate is what applies the telefrag's raw == bounded rule; check it
	// end to end, since that is where the 99 points were being charged.
	in := mk("tele")
	out := aggregate(in, []reconEvent{e})
	if len(out.Events) != 0 {
		t.Errorf("a positional kill must not appear in the Events log: %+v", out.Events)
	}
	if len(out.Telefrags) != 1 {
		t.Fatalf("Telefrags = %+v, want the one kill", out.Telefrags)
	}
	if got := out.ByPlayer["a"].GivenTeam; got != 100 {
		t.Errorf("raw givenTeam = %d, want the victim's capacity 100 (not the -99 corpse drop)", got)
	}
	if got := out.ByPlayer["a"].BoundedNest().GivenTeam; got != 100 {
		t.Errorf("bounded givenTeam = %d, want 100", got)
	}

	// The killer-named teamkill phrasings ("checks his glasses") carry no
	// deathtype, so they must NOT be routed positionally.
	if e := mk("teamkill").attributeOne("v", nil, d); e.kind == "positional" {
		t.Errorf("a cause-less teamkill obituary must not become a positional kill: %+v", e)
	}
}
