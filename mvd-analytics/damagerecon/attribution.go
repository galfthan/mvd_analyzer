package damagerecon

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Attribution tolerances and radii, all calibrated against modern
// ground-truth demos in the 2026-08-11 study (values are the study's
// measured percentiles with slack; see REPORT.md).
const (
	tolBeamMs = 30 // beam flash to damage frame (measured: 0)
	// The projectile-end→damage delay is asymmetric (measured p5=-81,
	// p99=261: the tracked despawn usually PRECEDES the damage frame, by up
	// to a couple hundred ms on low-fps recordings). The searches below use
	// [t-tolProjBeforeMs, t+tolProjAfterMs] on endT accordingly.
	tolProjBeforeMs = 261
	tolProjAfterMs  = 81

	tolShotMs           = 60     // hitscan sound to damage frame
	axeSwingDelayMs     = 200    // ax1.wav swing to W_FireAxe traceline (2×0.1s thinks)
	axeSwingJitterMs    = 80     // demo-frame quantization on both timestamps
	rBeamSeg            = 90.0   // units victim to beam segment (p99 measured 60, max 79)
	rBeamSrc            = 160.0  // units beam start to attacker eye
	nailSpeed           = 1000.0 // nails AND rockets fly 1000 ups
	rocketSpeed         = 1000.0
	tolFlightMs         = 180.0 // flight-time consistency for the trackless-rocket fallback
	tolBlastMs          = 40    // TE_EXPLOSION multicast to damage frame (same server function)
	rGrenadeContact     = 350.0 // trackless grenade: muzzle to contact detonation (one frame of lob)
	tolGrenadeContactMs = 350   // trackless grenade: launch to contact detonation
	rAxe                = 110.0
	rHitscan            = 3000.0
	rLGRange            = 700.0 // id1 LG traceline reaches 600 units; interp slack
	tolLGAmmoMs         = 250   // cells stat update to damage frame (old low-fps recordings lag)
	sgAimGateDeg        = 50.0  // real sg hits are within ~25° (p95); hard gate at 2×
	rlSoundAimGateDeg   = 60.0

	// splashReach is the engine's own splash reach: T_RadiusDamage visits
	// exactly trap_findradius(inflictor->origin, damage + 40) — 160 units
	// for both projectiles' 120 base (ktx/src/combat.c:1252; weapons.c:1006
	// and :1300) — and findradius measures the same quantity
	// T_RadiusDamageApply then prices, the explosion origin to the victim's
	// BBOX CENTRE (mvdsv pr_cmds.c:1233, pr2_cmds.c:886). A player 161 units
	// away takes nothing at all, and the quad does not extend that: the
	// multiplier is applied downstream in T_Damage (combat.c:537-543), not
	// to findradius' argument. See splashSlack for the distance our
	// measurement of it can be off by, and splashAdmit for the two together.
	splashReach = 160.0

	// geomNorm is what the projectile candidates' geometry PRIOR divides
	// their explosion-to-victim distance by: a pure SCORING weight, saying
	// how fast confidence should fall off with distance when several
	// admitted candidates compete for the same delta.
	//
	// It is deliberately NOT splashAdmit. The prior read `dEnd/splashAdmit(..)`
	// until this constant existed, which made two unrelated things one number:
	// narrowing admission from 380 to 184/220 (plan-damage-recon.md §8 lead B)
	// silently DOUBLED the distance slope of every projectile candidate and
	// re-weighted them against the fixed-geom kinds (env 0.12, beam 0.3+,
	// discharge 0.1) that did not move — so an admission change nobody
	// described as a scoring change moved attribution. Worse, the divisor
	// varied with epExact, which scored an un-snapped endpoint BETTER than an
	// exact one at the same distance for no reason anyone had stated.
	//
	// The value is measured, not inherited: swept on the 30-demo dm3 half and
	// confirmed on the held-out dm2 half — see ACCURACY.md §"The geometry
	// prior's own normalizer".
	geomNorm = 260.0

	// regimeMinSamples: how many near-direct rocket observations a demo must
	// carry before detectRocketRegime's clustering test says anything at all.
	// Below it the demo is UNESTABLISHED (the question was never put); at or
	// above it a failure to cluster is evidence in its own right.
	regimeMinSamples = 6
)

// candidate is one possible explanation for a delta.
type candidate struct {
	geom     float64 // geometry-quality penalty (lower is better)
	attacker string
	weapon   string
	kind     string  // "beam" | "proj" | "hitscan" | "nail" | "rl-sound" | "discharge" | "env"
	dEnd     float64 // explosion-to-victim distance; <0 = unknown
	ep       vec3    // explosion point (kind == "proj" only)
	hasEP    bool
	epExact  bool // ep is a TE_EXPLOSION detonation point, not an interpolated track end
	isSplash bool
	// hullNear: the detonation point is close enough to the victim's hull
	// for a touch to be entertained at all (direct.go directHullNear).
	hullNear bool
	// mLo/mHi: precomputed damage-model bounds for the kinds whose range
	// depends on per-candidate state the generic model cannot see
	// ("discharge": 35*cells; "env": the engine's lava/slime/drown/fall
	// tick values at the victim's measured liquid state).
	mLo, mHi float64
}

// reconEvent is one attributed damage observation — the reconstruction's
// analogue of a wire DamageEvent, already name-resolved.
type reconEvent struct {
	t        int32
	attacker string // "world" for environmental / unattributed
	victim   string
	weapon   string // attacker weapon, "unknown" when unattributed
	raw      int
	bounded  int
	died     bool
	isSelf   bool
	isTeam   bool
	isEnv    bool
	isSplash bool
	dEnd     float64 // winning candidate's explosion-to-victim distance; <0 unknown
	// mLo/mHi carry the winning candidate's PRECOMPUTED damage-model band
	// for the two kinds whose range is per-candidate state the generic
	// model cannot rederive ("discharge": 35·cells at the measured
	// distance; "env": the engine tick at the measured liquid state).
	// attributeDelta's misfit probe rebuilds a candidate from the event to
	// ask "does the winner explain the value?", and without these
	// modelBounds answers (0, 0) — "no magnitude opinion" — which reads as
	// a perfect fit and silently skips the pair split.
	mLo, mHi float64
	// kind records the evidence class that won the attribution — either a
	// scored candidate family (beam | proj | hitscan | nail | rl-sound |
	// discharge | env) or an anchored/unmodelled one (frag-anchor |
	// positional | masked-kill | teamkill-anchor | env-anchor | env-fallback
	// | pent-synth | none). Only the scored families carry a damage-model
	// band, which is what attributeDelta's pair-split trigger turns on.
	kind string
}

// attribute explains every extracted delta: frag anchors first, then
// evidence-scored candidates, else environmental/unknown.
//
// Two passes: the first with the vanilla rocket direct-damage range
// (100+random*20). If the demo's near-direct enemy rocket hits cluster on
// exactly 110 — the fixed constant modern KTX servers use — the second
// pass rescores with the exact-110 model, which discriminates close
// self/enemy rocket ambiguity far more sharply.
func attribute(in *inputs) []reconEvent {
	calibrateBloods(in)
	events := attributePass(in)
	lo, hi, regime := detectRocketRegime(in, events)
	in.rlRegime = regime
	if regime == result.RocketRegimeFixed {
		in.rlLo, in.rlHi = lo, hi
		events = attributePass(in)
	}
	return events
}

func attributePass(in *inputs) []reconEvent {
	var events []reconEvent
	for _, victim := range in.order {
		p := in.players[victim]
		vtrack := in.tracks[victim]
		anchors := make(map[int32]bool)
		for k := range in.fragAnyAt {
			if k.victim == victim {
				anchors[k.t] = true
			}
		}
		deltas := victimDeltas(p, anchors)
		for _, d := range deltas {
			events = append(events, in.attributeDelta(victim, vtrack, d)...)
		}
		events = append(events, in.pentSyntheticEvents(victim, p, vtrack, deltas)...)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].t < events[j].t })
	return events
}

// pentSyntheticEvents recovers the RAW damage a pentagram holder absorbed
// invisibly: pent zeroes the health share, so a pent holder with no armor
// takes a hit with NO h/a change at all — while KTX's raw accumulation
// still counts the full wire value (a pent rocket-jump logs ~50 raw self,
// an enemy direct 110). The pent interval (StatItems), the tracked
// explosions, and the shooter's quad state reconstruct the value from the
// damage model instead. Explosions at instants that already produced a
// delta (armor-absorbing pent hits) are skipped — those carry the hit
// already.
func (in *inputs) pentSyntheticEvents(victim string, p *result.PlayerStream, vtrack *track, deltas []delta) []reconEvent {
	if len(p.Pent) == 0 || vtrack == nil {
		return nil
	}
	seen := make(map[int32]bool, len(deltas))
	for _, d := range deltas {
		seen[d.t] = true
	}
	// The exclusion window must cover projCandidates' attribution window
	// exactly: an endpoint at endT can explain deltas in
	// [endT-tolProjAfterMs, endT+tolProjBeforeMs] — any delta in that span
	// already carries the hit, and a narrower window here would ALSO
	// synthesize a pent event from the same explosion, double-counting it.
	deltaNear := func(t int32) bool {
		for dt := int32(-tolProjAfterMs); dt <= tolProjBeforeMs; dt++ {
			if seen[t+dt] {
				return true
			}
		}
		return false
	}
	var out []reconEvent
	for _, pr := range in.projs {
		if pr.shooter == "" || !inIntervals(p.Pent, pr.endT) || !inIntervals(p.Alive, pr.endT) {
			continue
		}
		vpos := vtrack.posAt(pr.endT)
		dEnd := pr.ep.distTo(vpos)
		if dEnd > splashAdmit(pr.epExact) || deltaNear(pr.endT) {
			continue
		}
		if !in.bsp.splashReaches(pr.ep, vpos) {
			continue
		}
		q := in.quadFactor(pr.shooter, pr.endT)
		selfF := 1.0
		if pr.shooter == victim {
			selfF = 0.5
		}
		direct := directImpact(pr.weapon, pr.sp, pr.ep, vpos, pr.shooter == victim) &&
			!grenadeFuseExpired(&pr)
		// A direct ROCKET is the flat touch value with no falloff and no
		// splash on top — T_MissileTouch deals the constant and then hands
		// the same victim to T_RadiusDamage as the `ignore` entity
		// (ktx/src/weapons.c:998-1006) — so pricing it off the 120 radius
		// curve synthesizes ~120 (480 quadded) where the engine dealt 110
		// (440). A grenade has no touch value at all: GrenadeTouch does ALL
		// its damage through GrenadeExplode → T_RadiusDamage (:1327-1333),
		// so it rides the radius curve whether or not it touched. A direct
		// self row cannot exist (directImpact short-circuits on isSelf), so
		// the halving below never applies to the direct branch.
		raw := int(splashModel(dEnd, q, selfF))
		if direct && pr.weapon == "rl" {
			dirLo, dirHi := in.directBase()
			raw = int(0.5 * (dirLo + dirHi) * q)
		}
		if raw <= 0 {
			continue
		}
		e := in.mkEventSplash(delta{t: pr.endT, raw: raw, bounded: 0},
			pr.shooter, victim, pr.weapon, "pent-synth", !direct)
		e.dEnd = dEnd
		out = append(out, e)
	}
	// Trackless self rockets — the pent rocket jump: a point-blank rocket
	// explodes before its entity is ever broadcast, so the projectile pass
	// above cannot see it. A rocket never touches its own owner, so this is
	// pure radius damage; the explosion is at the shooter's feet and the
	// nominal distance below folds into ~0.5·(120 − 0.5·30) ≈ 52 raw per
	// jump (GT pent-jump observations run 32–54).
	const pentJumpNominalDist = 30.0
	for _, s := range in.shots {
		if s.weapon != "rl" || s.player != victim {
			continue
		}
		if !inIntervals(p.Pent, s.t) || !inIntervals(p.Alive, s.t) {
			continue
		}
		if in.shotConsumed(victim, s.t, 100) || deltaNear(s.t) {
			continue
		}
		q := in.quadFactor(victim, s.t)
		raw := int(splashModel(pentJumpNominalDist, q, 0.5))
		if raw <= 0 {
			continue
		}
		e := in.mkEventSplash(delta{t: s.t, raw: raw, bounded: 0},
			victim, victim, "rl", "pent-synth", true)
		out = append(out, e)
	}
	return out
}

// detectRocketRegime inspects the first-pass enemy rocket hits whose
// tracked explosion landed near-direct on a surviving, unquadded victim:
// on a fixed-constant server every such observation reads exactly 110
// (measured in the study: 88.8%% of modern GT directs). Vanilla servers
// (100+random*20) spread across the range instead.
//
// The verdict is THREE-valued, because "no fixed constant" covers two
// states that are not the same claim and that a consumer reads differently
// (result.RocketDirectRegime):
//
//   - RocketRegimeFixed — the observations clustered on 110, so the
//     magnitude prior (direct.go rocketTouched) is in force;
//   - RocketRegimeSpread — there were ENOUGH observations to test the
//     hypothesis and they did not cluster, which is what a pre-1.36
//     `100 + g_random()*20` server looks like. Evidence against the fixed
//     constant, not proof of the roll: a noisy first pass on a modern
//     server can also land here, which is why the token names what was
//     seen rather than what the server was;
//   - RocketRegimeUnestablished — fewer than regimeMinSamples near-direct
//     observations, i.e. the question was never put. That is the
//     low-rocket case, and the one whose touch counts stay accurate
//     anyway (ACCURACY.md).
func detectRocketRegime(in *inputs, events []reconEvent) (lo, hi float64, regime string) {
	// High-value (>=95) enemy rocket observations on surviving, unquadded
	// victims are (near-)directs. Fixed-110 servers put a spike at exactly
	// 110 and NOTHING above it; vanilla direct damage (100+random*20)
	// spreads through 111..120.
	n, at110, above := 0, 0, 0
	for i := range events {
		e := &events[i]
		if e.kind != "proj" || e.weapon != "rl" || e.isSelf || e.isTeam || e.isEnv || e.died {
			continue
		}
		if in.hasQuad(e.attacker, e.t) {
			continue
		}
		if e.bounded < 95 || e.bounded > 125 {
			continue
		}
		n++
		switch {
		case e.bounded == 110:
			at110++
		case e.bounded > 110:
			above++
		}
	}
	if n < regimeMinSamples {
		return 0, 0, result.RocketRegimeUnestablished
	}
	if float64(at110) >= 0.4*float64(n) && float64(above) <= math.Max(1, 0.1*float64(n)) {
		return 110, 110, result.RocketRegimeFixed
	}
	return 0, 0, result.RocketRegimeSpread
}

// attributeDelta explains one delta, usually as one event; a same-frame
// multi-attacker merge that no single candidate can explain but a PAIR sums
// to is split into two (the "mixed instant": e.g. a rocket and a shotgun
// blast landing in the same server frame produce one h/a delta).
func (in *inputs) attributeDelta(victim string, vtrack *track, d delta) []reconEvent {
	e := in.attributeOne(victim, vtrack, d)
	single := []reconEvent{e}
	if d.died || d.masked || vtrack == nil {
		return single
	}
	// TRIGGERING is not the same as PARTNERING. This switch names the kinds
	// whose winning single explanation may be challenged; trySplitPair's
	// candidate list is wider, because a family can be the second author of
	// a merge without ever being the first. The two sets differ on purpose:
	//
	//   - "env" only ever partners. An env candidate is scored on engine-
	//     exact tick values and damageModelScore refuses it outright when the
	//     value is outside its band (the `kind == "env"` branch returns
	//     feasible=false, not a penalty), so an env win is by construction an
	//     exact-value match and the misfit test below scores it 0. Listing it
	//     here would be dead code, not a behaviour change.
	//   - "discharge" DOES trigger. It is the exact merge the pair path
	//     exists for — a pool fight where one player discharges while
	//     another's rocket lands on the same victim in the same server frame
	//     — and the discharge is what WINS that merge's single pass: its
	//     ±25% value band absorbs a merged value the rocket's narrow splash
	//     band cannot (TestAttributeDeltaSplitsDischargeMerge scores 1.63
	//     against the rocket's 8.8 on a 200-point merge). Leaving it out
	//     made the pair path unreachable from production for precisely the
	//     deltas it was added to explain.
	//   - every remaining kind is an ANCHORED or unmodelled attribution
	//     (frag / telefrag / teamkill / env anchors, the env fallback,
	//     "none"): none of them won by a scored candidate, so modelBounds
	//     has no opinion on their value — (0, 0) — and the misfit test
	//     below would score them 0 as well.
	switch e.kind {
	case "beam", "proj", "hitscan", "nail", "rl-sound", "discharge":
	default:
		return single
	}
	// Only bother when the winning single explanation misfits the value.
	// mLo/mHi have to travel with the event: a "discharge" candidate's band
	// is per-candidate state (35·cells at the measured distance), and
	// rebuilding the probe without it makes modelBounds answer (0, 0) — "no
	// magnitude opinion" — which reads as a perfect fit and skips the split.
	quad := in.hasQuad(e.attacker, d.t)
	pen, ok := in.damageModelScore(d.bounded, false, &candidate{weapon: e.weapon, kind: e.kind, dEnd: e.dEnd, mLo: e.mLo, mHi: e.mHi}, e.isSelf, quad)
	if !ok || pen < 0.5 {
		return single
	}
	if pair, ok := in.trySplitPair(victim, vtrack, d); ok {
		return pair
	}
	return single
}

// trySplitPair searches the candidate set for two DIFFERENT attackers whose
// model ranges sum to the observed delta, and splits the value between them
// proportionally to the range midpoints (each share clamped into its own
// range). Survived deltas only — a killing delta's bounded cap breaks the
// sum identity.
//
// The family list wants to be the same set attributeOne scores: a family
// missing here is a delta the single path can explain badly but the pair path
// cannot explain at all. Still absent is explosionCandidates (trackless
// TE_EXPLOSION rockets) — a separate, much larger population that has not been
// measured. The LG discharge is a member: it is radius damage like a rocket's
// (T_RadiusDamage(self, self, 35*cells, world, dtLG_DIS),
// ktx/src/weapons.c:1208, :1225), so a water fight where one player discharges
// while another lands a rocket on the same victim in the same frame merges
// into one delta with two authors. Note that being in this list is only half
// of it: the caller's trigger switch decides which kinds may CHALLENGE a
// single explanation, and it is a narrower set — see attributeDelta.
//
// What refuses a wrong discharge partner is its VALUE band plus its distance:
// the ±25%+10 window dischargeCandidates computes around 35·cells − 0.5·d is
// ~90 points wide at E = 200, so the geometry prior has to be priced by range
// — flat, a discharge anywhere in the pool is the cheapest candidate there is.
//
// The family is measured INERT, and structurally so. Over a 2 400-demo archive
// sweep the split entertains 68 discharge candidates over 61 discharge-
// triggered entries and pairs none of them: on every one the observed delta
// sits BELOW the discharge's own band, and a pair only ever ADDS damage. That
// is the survived-delta ceiling — a victim who lives absorbs at most 299
// points — against a band floor of 0.75·(35·cells − 0.5·d) − 10. See the
// trySplitPair bullet in plan-archive-features.md for the full table.
//
// The one pair the engine forbids is beam+discharge from the SAME attacker:
// W_FireLightning's underwater branch returns before WS_Mark(wpLG), before the
// TE_LIGHTNING2 multicast and before LightningDamage (weapons.c:1174-1229), so
// a discharging fire deals no beam damage at all, and the wipe to zero cells
// makes every later call take the `ammo_cells < 1` early return (:1163). The
// different-attackers guard below already excludes it; no extra branch is
// needed, and TestDischargeNeverPairsWithOwnBeam pins that.
func (in *inputs) trySplitPair(victim string, vtrack *track, d delta) ([]reconEvent, bool) {
	vpos := vtrack.posAt(d.t)
	var cands []candidate
	cands = append(cands, in.beamCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.lgAmmoCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.projCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.hitscanCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.nailCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.rlSoundCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.dischargeCandidates(victim, d.t, vpos)...)
	cands = append(cands, in.envCandidates(victim, d.t, vpos, vtrack)...)

	type bounded struct {
		c      candidate
		lo, hi float64
	}
	var bs []bounded
	for _, c := range cands {
		quad := in.hasQuad(c.attacker, d.t)
		lo, hi, ok := in.modelBounds(&c, c.attacker == victim, quad)
		if !ok || (lo == 0 && hi == 0) {
			continue
		}
		bs = append(bs, bounded{c: c, lo: lo, hi: hi})
	}
	obs := float64(d.bounded)
	bestScore := math.Inf(1)
	var bi, bj *bounded
	for i := range bs {
		for j := i + 1; j < len(bs); j++ {
			a, b := &bs[i], &bs[j]
			if a.c.attacker == b.c.attacker {
				continue
			}
			if obs < a.lo+b.lo-1 || obs > a.hi+b.hi+1 {
				continue
			}
			score := a.c.geom + b.c.geom + 0.3 // pair prior: prefer single explanations
			if a.c.attacker == victim || b.c.attacker == victim {
				score += in.selfPen
			}
			if score < bestScore {
				bestScore, bi, bj = score, a, b
			}
		}
	}
	if bi == nil {
		return nil, false
	}
	midI := (bi.lo + bi.hi) / 2
	midJ := (bj.lo + bj.hi) / 2
	share := obs / 2
	if midI+midJ > 0 {
		share = obs * midI / (midI + midJ)
	}
	vi := math.Max(bi.lo, math.Min(bi.hi, share))
	vj := obs - vi
	if vj < bj.lo || vj > bj.hi {
		vj = math.Max(bj.lo, math.Min(bj.hi, vj))
		vi = obs - vj
	}
	// A share rounding to zero would emit a phantom zero-damage hit row
	// charged to a named attacker — treat the pair as unsplittable instead.
	if vi < 1 || obs-vi < 1 {
		return nil, false
	}
	// Bounded splits by the model shares; raw splits PROPORTIONALLY to
	// them — assigning "raw minus my bounded share" to the second
	// candidate would silently hand it all of the raw-vs-bounded excess
	// (an armor-nullified merge has raw > bounded), biasing raw attacker
	// totals by candidate order.
	bndI := int(vi + 0.5)
	rawI := bndI
	if d.bounded > 0 {
		rawI = int(float64(d.raw)*vi/obs + 0.5)
	}
	di := delta{t: d.t, raw: rawI, bounded: bndI}
	dj := delta{t: d.t, raw: d.raw - di.raw, bounded: d.bounded - di.bounded}
	e1 := in.mkEventSplash(di, bi.c.attacker, victim, bi.c.weapon, bi.c.kind, bi.c.isSplash)
	e1.dEnd = bi.c.dEnd
	in.stampRocketVerdict(&e1, &bi.c, di)
	e2 := in.mkEventSplash(dj, bj.c.attacker, victim, bj.c.weapon, bj.c.kind, bj.c.isSplash)
	e2.dEnd = bj.c.dEnd
	in.stampRocketVerdict(&e2, &bj.c, dj)
	return []reconEvent{e1, e2}, true
}

// telefragAnchor types a delta whose obituary is a telefrag the frag log
// left killer-less ("X was telefragged" parses as a suicide). The attacker
// is inferable: the player whose track TELEPORTS onto the victim's spot at
// that instant, or "world" when none is identifiable.
func (in *inputs) telefragAnchor(victim string, d delta) (reconEvent, bool) {
	f := in.anyFragAt(victim, d.t)
	if f == nil || f.Weapon != "tele" {
		return reconEvent{}, false
	}
	att := in.teleportArrivalAt(victim, d.t)
	if att == "" {
		att = "world"
	}
	return in.mkEvent(d, att, victim, "tele", "positional"), true
}

func (in *inputs) attributeOne(victim string, vtrack *track, d delta) reconEvent {
	// Frag anchor: a non-suicide non-teamkill frag at the exact instant
	// names killer + weapon authoritatively.
	if f := in.killerFragAt(victim, d.t); f != nil && f.Killer != "world" {
		if isPositionalWeapon(f.Weapon) {
			return in.mkEvent(d, f.Killer, victim, f.Weapon, "positional")
		}
		kind := "frag-anchor"
		if d.masked {
			kind = "masked-kill"
		}
		e := in.mkEvent(d, f.Killer, victim, f.Weapon, kind)
		in.topUpKillRaw(&e, vtrack)
		return e
	}
	if d.masked {
		// Masked death with no killer-naming frag — still typed (and often
		// attributed) by the killer-less telefrag obituary.
		if e, ok := in.telefragAnchor(victim, d); ok {
			return e
		}
		return in.mkEvent(d, "world", victim, "unknown", "masked-kill")
	}

	if d.died {
		// A visible killing delta whose obituary is a killer-less telefrag.
		if e, ok := in.telefragAnchor(victim, d); ok {
			return e
		}
		// Environmental deaths carry typed suicide obituaries ("burst into
		// flames" → lava): the category is authoritative there, no model
		// needed.
		if f := in.anyFragAt(victim, d.t); f != nil && f.IsSuicide {
			if w := envObituaryWeapon(f.Weapon); w != "" {
				return in.mkEvent(d, "world", victim, w, "env-anchor")
			}
		}
	}

	var cands []candidate
	if vtrack != nil {
		vpos := vtrack.posAt(d.t)
		cands = append(cands, in.beamCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.lgAmmoCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.projCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.explosionCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.hitscanCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.nailCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.rlSoundCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.dischargeCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.envCandidates(victim, d.t, vpos, vtrack)...)
	}

	best, ok := in.scoreCandidates(victim, vtrack, d, cands)
	if !ok {
		// KTX teamkill obituaries are weapon-less jokes ("checks his
		// glasses"), so a killing delta with a named teamkiller and no
		// evidence candidate still has a truthful attacker.
		if d.died {
			if f := in.anyFragAt(victim, d.t); f != nil && f.IsTeamKill && f.Killer != "" && f.Killer != victim {
				return in.mkEvent(d, f.Killer, victim, "unknown", "teamkill-anchor")
			}
		}
		// No weapon candidate fit — but an environmental candidate whose
		// VALUE fits still classifies the tick. The value check matters:
		// classifying by state alone stamped "fall" (a flat 5) onto
		// arbitrary unexplained kill deltas.
		if vtrack != nil {
			for _, c := range in.envCandidates(victim, d.t, vtrack.posAt(d.t), vtrack) {
				if pen, feasible := in.damageModelScore(d.bounded, d.died, &c, false, false); feasible && pen == 0 {
					return in.mkEvent(d, "world", victim, c.weapon, "env-fallback")
				}
			}
		}
		return in.mkEvent(d, "world", victim, "unknown", "none")
	}
	e := in.mkEventSplash(d, best.attacker, victim, best.weapon, best.kind, best.isSplash)
	e.dEnd = best.dEnd
	e.mLo, e.mHi = best.mLo, best.mHi
	in.stampRocketVerdict(&e, &best, d)
	if d.died && best.kind == "discharge" {
		// The clamp hides most of a discharge's raw value (35*cells reaches
		// four digits); the expected value rides in dEnd.
		if m := int(best.dEnd); m > e.raw {
			e.raw = m
		}
	}
	if d.died && best.kind == "proj" {
		// The -99 corpse clamp hides overkill deeper than 99 HP on a
		// killing hit; the damage model still knows the floor the wire
		// value cannot have been under (quad rockets are the big case:
		// 440-ish raw vs ~200 observable). Only ever raises raw, and it is
		// the engine's own formula for the verdict stampRocketVerdict just
		// stamped, at the measured distance — see killModelFloor, and
		// splashModel for why the pair of fudge constants it used to carry
		// went away with the quad-ordering fix.
		modelMin := in.killModelFloor(best.weapon, e.isSplash, e.isSelf,
			best.dEnd, in.quadFactor(best.attacker, d.t))
		if m := int(modelMin); m > e.raw {
			e.raw = m
		}
	}
	return e
}

// topUpKillRaw raises a killing hit's RAW value to the damage model's
// floor when the wire's -99 corpse clamp (or a masked respawn) hid deeper
// overkill — quad rockets are the big case: ~440 dealt, at most ~199+armor
// observable. Geometry comes from the killing explosion (nearest tracked
// projectile end) for rl/gl, and from the same-frame beam count for lg.
// Only ever raises; a survived or unmodeled kill keeps the observation.
//
// It is also where an OBITUARY-anchored rl/gl kill gets its direct/splash
// verdict. Such an event is named by the frag log, not by a scored
// candidate, so nothing else ever looks at its geometry — and leaving the
// zero value in place published every rocket kill as a direct touch, which
// is what a "did the projectile hit them" counter must not assume. A kill
// with no tracked explosion to judge stays splash: not seeing a touch is
// not seeing one.
//
// The default is stamped BEFORE any early return, so no path can leave the
// zero value standing as a verdict. The trackless-victim return below is the
// one that could: a victim whose stream carries no position samples has no
// geometry for anything to judge, which is the strongest form of "no touch
// was seen" and never a reason to assert one. (Rare — 4 of 4 227 players
// over 2 433 archive demos, and 0 of 325 622 reconstructed rl/gl rows landed
// on such a victim — but rarity is not a contract.)
func (in *inputs) topUpKillRaw(e *reconEvent, vtrack *track) {
	if !e.died {
		return
	}
	if e.weapon == "rl" || e.weapon == "gl" {
		e.isSplash = true
	}
	if vtrack == nil {
		return
	}
	q := in.quadFactor(e.attacker, e.t)
	model := 0.0
	switch e.weapon {
	case "rl", "gl":
		vpos := vtrack.posAt(e.t)
		bestD := -1.0
		bestDirect, bestNear := false, false
		lo := sort.Search(len(in.projs), func(i int) bool { return in.projs[i].endT >= e.t-tolProjBeforeMs })
		for i := lo; i < len(in.projs) && in.projs[i].endT <= e.t+tolProjAfterMs; i++ {
			pr := in.projs[i]
			if pr.weapon != e.weapon {
				continue
			}
			// The geometry must belong to the CREDITED attacker: without
			// this, quad player A's kill can be topped up from player B's
			// same-frame rocket. An unresolved shooter ("") stays eligible —
			// it may well be the attacker's own flight.
			if pr.shooter != "" && pr.shooter != e.attacker {
				continue
			}
			if d := pr.ep.distTo(vpos); d <= splashAdmit(pr.epExact) && (bestD < 0 || d < bestD) {
				bestD = d
				bestDirect = directImpact(pr.weapon, pr.sp, pr.ep, vpos, pr.shooter == e.victim) &&
					!grenadeFuseExpired(&pr)
				bestNear = hullDist(pr.ep, vpos) <= directHullNear
			}
		}
		if bestD < 0 {
			// Trackless kill (point-blank, no entity broadcast): the
			// unclaimed TE_EXPLOSION at the instant is the exact geometry,
			// and the attacker's own muzzle is the only source point the
			// trajectory test can use (ktx/src/weapons.c:1086-1088).
			var muzzle vec3
			hasMuzzle := false
			if atr := in.tracks[e.attacker]; atr != nil {
				mp := atr.posAt(e.t)
				muzzle, hasMuzzle = vec3{mp.x, mp.y, mp.z + muzzleOffsetZ}, true
			}
			elo := sort.Search(len(in.explosions), func(i int) bool { return in.explosions[i].t >= e.t-tolBlastMs })
			for i := elo; i < len(in.explosions) && in.explosions[i].t <= e.t+tolBlastMs; i++ {
				ex := &in.explosions[i]
				if ex.claimed {
					continue
				}
				if d := ex.p.distTo(vpos); d <= splashAdmit(true) && (bestD < 0 || d < bestD) {
					bestD = d
					bestDirect = hasMuzzle && directImpact(e.weapon, muzzle, ex.p, vpos, e.attacker == e.victim)
					bestNear = hullDist(ex.p, vpos) <= directHullNear
				}
			}
		}
		if bestD < 0 {
			return // no geometry at all: keep the observation
		}
		e.isSplash = !bestDirect
		if e.weapon == "rl" && !e.isSelf {
			// Same magnitude prior the scored path applies, on the value as
			// OBSERVED — the top-up below only raises it.
			e.isSplash = !in.rocketTouched(bestDirect, bestNear,
				delta{t: e.t, raw: e.raw, bounded: e.bounded, died: e.died}, q)
		}
		model = in.killModelFloor(e.weapon, e.isSplash, e.isSelf, bestD, q)
	case "lg":
		// A discharge kill (35*cells radius blast) hides almost all of its
		// raw value behind the clamp; the discharger's cells count knows it.
		for _, dc := range in.discharges {
			if dc.player != e.attacker || abs32(dc.t-e.t) > 100 {
				continue
			}
			tr := in.tracks[dc.player]
			if tr == nil {
				continue
			}
			d := tr.posAt(dc.t).distTo(vtrack.posAt(e.t))
			if d > dischargeReach(dc.cells) {
				// Out of the blast's reach — this kill was the attacker's
				// ordinary shaft, not their discharge, and must fall through
				// to the beam count below rather than be priced as a blast.
				continue
			}
			// Same radius-damage form as the projectiles: 35·cells is what
			// W_FireLightning hands T_RadiusDamage (ktx/src/weapons.c:1208,
			// :1225), the falloff comes off it there, and the quad is applied
			// afterwards in T_Damage.
			expected := (35.0*float64(dc.cells) - 0.5*d) * q
			if e.isSelf {
				expected *= 0.5
			}
			if m := int(expected); m > e.raw {
				e.raw = m
			}
			return
		}
		if q == 1 {
			return // a single non-quad cell (30) never out-runs the clamp
		}
		// Each same-frame TE_LIGHTNING2 is one cell at 30q — counting only
		// beams that START at the credited attacker (same rBeamSrc gate as
		// beamCandidates), so a second shafter's bolt cannot inflate this
		// attacker's top-up.
		cells := 0
		atr := in.tracks[e.attacker]
		lo := sort.Search(len(in.beams), func(i int) bool { return in.beams[i].t >= e.t-tolBeamMs })
		for i := lo; i < len(in.beams) && in.beams[i].t <= e.t+tolBeamMs; i++ {
			if atr != nil && atr.posAt(in.beams[i].t).distTo(in.beams[i].s) > rBeamSrc {
				continue
			}
			cells++
		}
		if cells == 0 {
			cells = 1
		}
		model = 30.0 * q * float64(cells)
	default:
		return
	}
	if m := int(model); m > e.raw {
		e.raw = m
	}
}

// scoreCandidates applies the damage-model score, the enemy-attribution
// prior and the knockback signal, returning the lowest-penalty candidate.
func (in *inputs) scoreCandidates(victim string, vtrack *track, d delta, cands []candidate) (candidate, bool) {
	var dv vec3
	dvn := 0.0
	hasDv := false
	if vtrack != nil {
		if v, ok := vtrack.velDelta(d.t, 40, 60); ok {
			dv, hasDv = v, true
			dvn = v.length()
		}
	}
	var vpos vec3
	if vtrack != nil {
		vpos = vtrack.posAt(d.t)
	}

	best := candidate{}
	bestScore := math.Inf(1)
	found := false
	for _, c := range cands {
		quad := in.hasQuad(c.attacker, d.t)
		pen, feasible := in.damageModelScore(d.bounded, d.died, &c, c.attacker == victim, quad)
		if !feasible {
			continue
		}
		total := c.geom + 1.5*pen
		if c.attacker == victim {
			total += in.selfPen
		}
		if c.kind == "proj" && hasDv && dvn > 50.0 && c.hasEP {
			// Knockback: the victim's velocity change should point away
			// from the explosion.
			k := vec3{vpos.x - c.ep.x, vpos.y - c.ep.y, (vpos.z + targetOffsetZ) - c.ep.z}
			kl := k.length()
			if kl > 1 {
				cosk := dv.dot(k) / (dvn * kl)
				total += (1.0 - cosk) * 0.3
			}
		}
		if total < bestScore {
			best, bestScore = c, total
			found = true
		}
	}
	return best, found
}

// damageModelScore scores a candidate by how well the observed bounded
// delta matches the QW damage model — bounded == raw for every surviving
// hit, so magnitude is a sharp discriminator. Returns a penalty >= 0
// (lower is better); feasible == false marks a physically impossible
// candidate.
//
// QW radius damage: received = D − 0.5·dist(center, explosion), and the
// attacker's own splash halves THAT — 0.5·(D − 0.5·dist), not D − 0.25·dist,
// because T_RadiusDamageApply halves the post-falloff points
// (ktx/src/combat.c:1189-1193). Rocket D = 100..120, grenade 120.
// LG = 30/cell, sg pellet 4×6, ssg ×14, ng spike 9, sng 18, axe 20.
// Quad multiplies ×4.
func (in *inputs) damageModelScore(obs int, died bool, c *candidate, isSelf, quad bool) (float64, bool) {
	lo, hi, feasible := in.modelBounds(c, isSelf, quad)
	if !feasible {
		return 0, false
	}
	if lo == 0 && hi == 0 {
		return 0, true // unmodeled kind: no magnitude opinion
	}
	if died {
		// Killing hit: bounded is capped below raw; low observations are fine.
		lo = 1.0
	}
	o := float64(obs)
	var pen float64
	switch {
	case o < lo-0.5:
		pen = lo - o
	case o > hi+0.5:
		pen = o - hi
	default:
		return 0, true
	}
	if c.kind == "env" {
		// Environmental tick values are engine-exact (10·wl lava, flat-5
		// fall, ...): a value outside the band is not a poor fit, it is a
		// different cause. Without this a lone landing candidate "won"
		// arbitrary unexplained kill deltas as fall damage.
		return 0, false
	}
	return pen / math.Max(10.0, 0.25*hi), true
}

// modelBounds returns the QW damage model's feasible [lo, hi] for one
// candidate explanation ((0,0) for an unmodeled kind — no opinion).
// Factored out of damageModelScore so the same-frame pair-split can test
// whether TWO candidates' ranges sum to the observation.
func (in *inputs) modelBounds(c *candidate, isSelf, quad bool) (float64, float64, bool) {
	weapon, kind, dEnd := c.weapon, c.kind, c.dEnd
	q := 1.0
	if quad {
		q = 4.0
	}
	var lo, hi float64
	switch kind {
	case "beam":
		// 30/cell, up to 2 cells per server frame; lo stretched down to the
		// armor-only share (ra 24 / ya 18 / ga 9) seen when a server mode
		// nullifies the health share (povdmm4-style).
		lo, hi = 9.0*q, 60.0*q
	case "proj", "rl-sound":
		dLo, dHi := splashBase()
		// Self splash is HALVED by the engine (T_RadiusDamage,
		// ktx/src/combat.c:1183-1194: points = damage - 0.5*dist, then
		// *0.5 when head == attacker), and a direct self-hit is impossible
		// (the missile never touches its owner; the radius blast skips the
		// direct victim). This caps a self rocket at ~55 + slop — a sharp
		// self-vs-enemy discriminator in close-quarters rocket fights.
		selfF := 1.0
		if isSelf {
			selfF = 0.5
		}
		// The quad multiplies the falloff's RESULT, not the base: the
		// engine subtracts 0.5·dist in T_RadiusDamageApply (combat.c:1189)
		// and only then calls T_Damage, where the ×4 is applied
		// (combat.c:537-543). A quad splash is therefore 4·(120 − 0.5·d),
		// range (0, 480], and not 4·120 − 0.5·d, which cannot go below 400
		// inside the engine's 160-unit reach — where 96% of the wire's own
		// quad rl splash rows live (plan-damage-recon.md §8 lead A). Every
		// multiplier that composes with a radius hit sits on the same side:
		// quad/octa, the ctf strength rune and the handicap are all applied
		// inside T_Damage, while the self-halving and the shambler halving
		// are inside T_RadiusDamageApply, before it. Hence q outside the
		// falloff, selfF (either side — both are plain factors) with it.
		slack := splashSlack(c.epExact)
		switch {
		case kind == "rl-sound" || dEnd < 0:
			// Unknown explosion point: full damage possible for an enemy,
			// splash-only ceiling for self. Splash values are allowed, but a
			// tiny delta is far likelier another cause. (The engine's own
			// floor is higher — a splash at the 160-unit reach still deals
			// 40·q — but a distance-less candidate also has to cover the
			// merged and partly-masked instants a distance-carrying one is
			// scored out of, and tightening to it measured neutral.)
			lo, hi = 25.0*q*selfF, dHi*q*selfF
		default:
			// Radius damage — including a grenade that DID touch the victim:
			// GrenadeTouch deals nothing itself and detonates through
			// GrenadeExplode → T_RadiusDamage with ignore = world
			// (ktx/src/weapons.c:1300, :1335), so a touched grenade victim is
			// still on the falloff curve.
			//
			// hi stays positive for every admissible candidate: dEnd is
			// capped at splashAdmit = splashReach + this same slack, which is
			// 220 at the widest, so the subtracted term never reaches the 240
			// that would zero it. The tie is pinned in
			// TestSplashBandPositiveInsideAdmission.
			lo = (dLo - 0.5*(dEnd+slack)) * q * selfF
			hi = (dHi - 0.5*math.Max(0, dEnd-slack)) * q * selfF
			lo = math.Max(1.0, lo)
		}
	case "hitscan":
		if c.mLo > 0 {
			// Blood-count-pinned volley (calibrated TE_BLOOD, single
			// shooter): the expected value is exact, ±one pellet of slop
			// for count/delta rounding at the edges.
			lo, hi = c.mLo-4.0, c.mHi+4.0
			break
		}
		switch weapon {
		case "sg":
			lo, hi = 4.0*q, 24.0*q
		case "ssg":
			lo, hi = 4.0*q, 56.0*q
		default: // axe
			lo, hi = 20.0*q, 20.0*q
		}
	case "discharge", "env":
		// Bounds precomputed per candidate (35*cells for a discharge; the
		// engine tick values at the measured liquid state / a flat 5 for a
		// landing). Quad/self never apply to these.
		lo, hi = c.mLo, c.mHi
	case "nail":
		per := 9.0
		if weapon == "sng" {
			per = 18.0
		}
		lo, hi = per*q, per*q*3 // up to a few spikes per frame
	default:
		return 0, 0, true
	}
	return lo, hi, true
}

// beamCandidates: LG beams whose segment passes near the victim at the
// same frame; the attacker is the nearest player to the beam start.
// TE_LIGHTNINGBLOOD is the per-cell hit confirmation (LightningDamage
// writes one on the victim at the trace endpoint): on demos that carry
// them, blood at the instant strengthens the LG explanation and its
// absence penalizes it — a beam that merely PASSES near the victim (the
// segment gate cannot tell a miss from a hit) stops absorbing same-frame
// shotgun damage.
func (in *inputs) beamCandidates(victim string, t int32, vpos vec3) []candidate {
	lgPen := 0.0
	if len(in.lgBloods) > 0 {
		if in.lgBloodNear(t, vpos) {
			lgPen = -0.15
		} else {
			lgPen = 0.5
		}
	}
	var out []candidate
	lo := sort.Search(len(in.beams), func(i int) bool { return in.beams[i].t >= t-tolBeamMs })
	for i := lo; i < len(in.beams) && in.beams[i].t <= t+tolBeamMs; i++ {
		b := in.beams[i]
		sd := segDist(vpos, b.s, b.e)
		if sd > rBeamSeg {
			continue
		}
		best, bestD := "", rBeamSrc
		for _, name := range in.order {
			if name == victim {
				continue
			}
			tr := in.tracks[name]
			if tr == nil {
				continue
			}
			if dd := tr.posAt(b.t).distTo(b.s); dd < bestD {
				best, bestD = name, dd
			}
		}
		if best != "" {
			out = append(out, candidate{
				geom: math.Max(0, sd/rBeamSeg*0.3+lgPen), attacker: best, weapon: "lg",
				kind: "beam", dEnd: -1,
			})
		}
	}
	return out
}

// lgAmmoCandidates: LG attacks recovered from the shooter's cells drain on
// beam-sparse recordings (old servers drop most TE_LIGHTNING2 multicasts
// from the demo — observed MVDSV 0.33 demos carry beams for under 20% of
// cells spent, leaving whole shaft fights beam-invisible). A cells
// decrement at the instant is the fire; range (id1 LG traces 600 units),
// line of sight and the aim cone gate the target. Only generated on
// demos flagged lgBeamSparse — where beams are healthy they are the
// sharper signal and these duplicates would only add noise.
func (in *inputs) lgAmmoCandidates(victim string, t int32, vpos vec3) []candidate {
	if !in.lgBeamSparse {
		return nil
	}
	// Only where the beam record is silent FOR THIS VICTIM: a recorded
	// beam passing near them at the instant means the sharper beam
	// evidence is live here. A beam elsewhere on the map is someone
	// else's shaft and must not suppress recovery of an unrecorded one.
	blo := sort.Search(len(in.beams), func(i int) bool { return in.beams[i].t >= t-tolBeamMs })
	for i := blo; i < len(in.beams) && in.beams[i].t <= t+tolBeamMs; i++ {
		if segDist(vpos, in.beams[i].s, in.beams[i].e) <= rBeamSeg {
			return nil
		}
	}
	var out []candidate
	lo := sort.Search(len(in.lgAmmoFires), func(i int) bool { return in.lgAmmoFires[i].t >= t-tolLGAmmoMs })
	for i := lo; i < len(in.lgAmmoFires) && in.lgAmmoFires[i].t <= t+tolLGAmmoMs; i++ {
		s := in.lgAmmoFires[i]
		if s.player == victim {
			continue
		}
		tr := in.tracks[s.player]
		if tr == nil {
			continue
		}
		spos := tr.posAt(t)
		dd := spos.distTo(vpos)
		if dd > rLGRange {
			continue
		}
		apen := 0.0
		if ang, ok := tr.aimAngleTo(t, vpos); ok {
			if ang > sgAimGateDeg {
				continue
			}
			apen = math.Min(ang/10.0, 3.0) * 0.2
		}
		if !in.bsp.reachesBody(eyeOf(spos), vpos) {
			continue
		}
		out = append(out, candidate{
			geom: 0.3 + dd/rLGRange*0.15 + apen, attacker: s.player, weapon: "lg",
			kind: "beam", dEnd: -1,
		})
	}
	return out
}

// projCandidates: tracked rocket/grenade flights ending near the victim at
// the same frame. The direct/splash verdict comes from the flight's
// TRAJECTORY (direct.go), not from endpoint proximity: a rocket detonating
// on the wall beside the victim is endpoint-near and never touched them.
func (in *inputs) projCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	lo := sort.Search(len(in.projs), func(i int) bool { return in.projs[i].endT >= t-tolProjBeforeMs })
	for i := lo; i < len(in.projs) && in.projs[i].endT <= t+tolProjAfterMs; i++ {
		pr := in.projs[i]
		if pr.shooter == "" {
			continue
		}
		dEnd := pr.ep.distTo(vpos)
		if dEnd > splashAdmit(pr.epExact) {
			continue
		}
		direct := directImpact(pr.weapon, pr.sp, pr.ep, vpos, pr.shooter == victim) &&
			!grenadeFuseExpired(&pr)
		// CanDamage gate: splash reaches only what the explosion traces to.
		// A touch needs no such trace — the projectile was inside the hull.
		if !direct && !in.bsp.splashReaches(pr.ep, vpos) {
			continue
		}
		out = append(out, candidate{
			geom: dEnd / geomNorm * 0.5, attacker: pr.shooter, weapon: pr.weapon,
			kind: "proj", dEnd: dEnd, ep: pr.ep, hasEP: true, epExact: pr.epExact,
			isSplash: !direct, hullNear: hullDist(pr.ep, vpos) <= directHullNear,
		})
	}
	return out
}

// explosionCandidates: TE_EXPLOSION detonations at the damage instant that
// no tracked projectile claimed — the point-blank rockets (and short-lob
// grenades) that exploded before their entity was ever broadcast. The
// explosion names the exact place; the shooter is recovered from the rl/gl
// fires whose flight time from muzzle to THAT POINT is consistent, aimed
// there (rockets fly straight; grenades lob, so no aim gate). Where the
// old rl-sound fallback guessed geometry from flight time to the victim,
// this measures it — the candidate carries an exact dEnd, so the
// self-splash value ceiling and the quad model discriminate sharply.
func (in *inputs) explosionCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	elo := sort.Search(len(in.explosions), func(i int) bool { return in.explosions[i].t >= t-tolBlastMs })
	for i := elo; i < len(in.explosions) && in.explosions[i].t <= t+tolBlastMs; i++ {
		ex := &in.explosions[i]
		if ex.claimed {
			continue // a tracked flight owns this detonation (projCandidates)
		}
		dEnd := ex.p.distTo(vpos)
		if dEnd > splashAdmit(true) {
			continue
		}
		slo := sort.Search(len(in.shots), func(k int) bool { return in.shots[k].t >= ex.t-2000 })
		for k := slo; k < len(in.shots) && in.shots[k].t <= ex.t+40; k++ {
			s := in.shots[k]
			if s.weapon != "rl" && s.weapon != "gl" {
				continue
			}
			if in.shotConsumed(s.player, s.t, 100) {
				continue // this fire's projectile HAS an entity track
			}
			tr := in.tracks[s.player]
			if tr == nil {
				continue
			}
			spos := tr.posAt(s.t)
			// The line the projectile flew: a trackless rocket has no entity
			// track, so its only known source is the shooter's muzzle
			// (origin + '0 0 16', ktx/src/weapons.c:1086-1088). A grenade
			// ignores the line entirely (direct.go).
			muzzle := vec3{spos.x, spos.y, spos.z + muzzleOffsetZ}
			direct := directImpact(s.weapon, muzzle, ex.p, vpos, s.player == victim)
			if !direct && !in.bsp.splashReaches(ex.p, vpos) {
				continue
			}
			apen := 0.0
			ferr := 0.0
			if s.weapon == "rl" {
				// Rockets fly 1000 ups in a straight line at the crosshair:
				// flight-time consistency plus an aim gate toward the
				// detonation point.
				flight := spos.distTo(ex.p) / rocketSpeed * 1000.0
				ferr = math.Abs(float64(ex.t-s.t) - flight)
				if ferr > tolFlightMs {
					continue
				}
				if ang, ok := tr.aimAngleTo(s.t, ex.p); ok {
					if ang > rlSoundAimGateDeg {
						continue
					}
					apen = math.Min(ang/30.0, 2.0) * 0.15
				}
			} else {
				// Grenades lob (~600 ups launch, gravity, bouncing, 2.5s
				// fuse) — rocket flight physics would reject or misassign
				// them. A TRACKLESS grenade means the entity was never
				// broadcast, i.e. it detonated on contact within a frame of
				// launch: gate on short range and a tight launch-to-blast
				// delay instead of a flight model.
				if spos.distTo(ex.p) > rGrenadeContact || ex.t-s.t > tolGrenadeContactMs {
					continue
				}
			}
			out = append(out, candidate{
				geom:     0.1 + dEnd/geomNorm*0.4 + ferr/tolFlightMs*0.15 + apen,
				attacker: s.player, weapon: s.weapon, kind: "proj",
				dEnd: dEnd, ep: ex.p, hasEP: true, epExact: true,
				isSplash: !direct, hullNear: hullDist(ex.p, vpos) <= directHullNear,
			})
		}
	}
	return out
}

// hitscanCandidates: sg/ssg fire sounds at the same frame gated by aim
// cone, plus axe swings 200ms BEFORE the damage gated by melee range: the
// axe fire sound (weapons/ax1.wav, W_Attack) precedes the damage traceline
// by exactly two 0.1s animation thinks (W_FireAxe at player_axe3 and its
// b/c/d siblings, ktx/src/player.c), so the swing is searched around
// t−200ms and the range is measured at the trace time, not the swing.
func (in *inputs) hitscanCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	axeLo := sort.Search(len(in.shots), func(i int) bool { return in.shots[i].t >= t-axeSwingDelayMs-axeSwingJitterMs })
	for i := axeLo; i < len(in.shots) && in.shots[i].t <= t-axeSwingDelayMs+axeSwingJitterMs; i++ {
		s := in.shots[i]
		if s.weapon != "axe" || s.player == victim {
			continue
		}
		tr := in.tracks[s.player]
		if tr == nil {
			continue
		}
		dd := tr.posAt(t).distTo(vpos)
		if dd > rAxe {
			continue
		}
		out = append(out, candidate{
			geom: 0.1 + dd/rAxe*0.2, attacker: s.player, weapon: s.weapon,
			kind: "hitscan", dEnd: dd,
		})
	}
	// TE_BLOOD shaping (demos that carry it): blood on the victim at the
	// instant confirms a hitscan hit — strengthen the shotgun explanations
	// and, when the demo's count packaging calibrated AND exactly one
	// shotgunner fired, pin the expected magnitude to 4·(summed counts).
	// No blood on a blood-carrying demo means the pellets did not strike
	// this victim — penalize (soft: coverage is ~99.9%, not 100%).
	bloodPen, nBlood, sumBlood := 0.0, 0, 0
	if len(in.bloods) > 0 {
		nBlood, sumBlood = in.bloodsNear(t, vpos)
		if nBlood > 0 {
			bloodPen = -0.15
		} else {
			bloodPen = 0.4
		}
	}
	nSG, _ := in.hitscanFiresAt(t)
	singleSG := nSG == 1
	lo := sort.Search(len(in.shots), func(i int) bool { return in.shots[i].t >= t-tolShotMs })
	for i := lo; i < len(in.shots) && in.shots[i].t <= t+tolShotMs; i++ {
		s := in.shots[i]
		if s.player == victim {
			continue
		}
		tr := in.tracks[s.player]
		if tr == nil {
			continue
		}
		switch s.weapon {
		case "sg", "ssg":
			spos := tr.posAt(s.t)
			dd := spos.distTo(vpos)
			if dd > rHitscan {
				continue
			}
			// Aim-cone gate: real sg hits are within ~25° (p95, flicks +
			// sample staleness included). The penalty slope is what
			// separates simultaneous shooters — the true one is usually
			// within a few degrees, the others tens.
			apen := 0.0
			if ang, ok := tr.aimAngleTo(s.t, vpos); ok {
				// Blood on the victim proves SOMEONE's pellets struck them
				// this instant: with only one shotgunner firing, the aim
				// cone stops being a hard gate (its false negatives — snap
				// flicks, stale view samples — were the sg→env leak) and
				// rides on as a capped penalty instead.
				if ang > sgAimGateDeg && !(nBlood > 0 && singleSG) {
					continue
				}
				apen = math.Min(ang/10.0, 3.0) * 0.3
			}
			// Hitscan lands the same frame as the fire sound; drift inside
			// the tolerance window still separates competing shooters.
			tpen := math.Abs(float64(s.t-t)) / tolShotMs * 0.1
			// Pellets cannot cross solid geometry.
			if !in.bsp.reachesBody(eyeOf(spos), vpos) {
				continue
			}
			c := candidate{
				geom: math.Max(0, 0.15+apen+tpen+bloodPen), attacker: s.player, weapon: s.weapon,
				kind: "hitscan", dEnd: dd,
			}
			if in.bloodTrust && nBlood == 1 && sumBlood > 0 {
				// Calibrated count, one blood message = one connecting
				// volley: whoever fired it dealt exactly 4·sum·(their
				// quad). Pinned per candidate — with differing quad states
				// this rules the mismatched shooter out; with equal states
				// it pins the magnitude either way.
				q := in.quadFactor(s.player, t)
				c.mLo, c.mHi = 4.0*float64(sumBlood)*q, 4.0*float64(sumBlood)*q
			}
			out = append(out, c)
		}
	}
	return out
}

// nailCandidates: ng/sng fires whose nail flight time (1000 ups) is
// consistent with the delay to the damage frame.
func (in *inputs) nailCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	lo := sort.Search(len(in.nailShots), func(i int) bool { return in.nailShots[i].t >= t-3000 })
	for i := lo; i < len(in.nailShots) && in.nailShots[i].t <= t; i++ {
		s := in.nailShots[i]
		if s.player == victim {
			continue
		}
		tr := in.tracks[s.player]
		if tr == nil {
			continue
		}
		dt := float64(t - s.t)
		spos := tr.posAt(s.t)
		flight := spos.distTo(vpos) / nailSpeed * 1000.0
		if math.Abs(dt-flight) <= 150 {
			// A nail flies a straight line; the shooter->victim segment
			// must be clear of solid geometry.
			if !in.bsp.reachesBody(eyeOf(spos), vpos) {
				continue
			}
			out = append(out, candidate{
				geom: 0.4 + math.Abs(dt-flight)/150*0.2, attacker: s.player,
				weapon: s.weapon, kind: "nail", dEnd: -1,
			})
		}
	}
	return out
}

// rlSoundCandidates: point-blank rockets explode before the entity is ever
// broadcast, so no projectile track exists — fall back to the fire sound +
// flight-time consistency + aim gate, restricted to shots with no tracked
// projectile.
func (in *inputs) rlSoundCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	lo := sort.Search(len(in.shots), func(i int) bool { return in.shots[i].t >= t-450 })
	for i := lo; i < len(in.shots) && in.shots[i].t <= t+40; i++ {
		s := in.shots[i]
		if s.weapon != "rl" {
			continue
		}
		tr := in.tracks[s.player]
		if tr == nil {
			continue
		}
		if in.shotConsumed(s.player, s.t, 100) {
			continue
		}
		dt := float64(t - s.t)
		if dt < -40 {
			continue
		}
		spos := tr.posAt(s.t)
		flight := spos.distTo(vpos) / rocketSpeed * 1000.0
		err := math.Abs(dt - flight)
		if err > tolFlightMs {
			continue
		}
		// A point-blank rocket flies (briefly) straight from the shooter:
		// the path must be clear.
		if !in.bsp.reachesBody(eyeOf(spos), vpos) {
			continue
		}
		apen := 0.0
		if s.player != victim {
			// Enemy point-blank rocket: the shooter was aiming at the victim.
			if ang, ok := tr.aimAngleTo(s.t, vpos); ok {
				if ang > rlSoundAimGateDeg {
					continue
				}
				apen = math.Min(ang/30.0, 2.0) * 0.15
			}
		}
		out = append(out, candidate{
			geom: 0.35 + err/tolFlightMs*0.25 + apen, attacker: s.player,
			weapon: "rl", kind: "rl-sound", dEnd: -1, isSplash: true,
		})
	}
	return out
}

// Environmental damage models (ktx/src/client.c WaterMove + the landing
// path): lava = 10*waterlevel every 0.2s, slime = 4*waterlevel every 1s,
// drowning = escalating 4,6,8,10,12,14 every 1s once fully submerged with
// air out, a landing = flat 5 at vz < -650. The liquid state comes from
// the BSP at the victim's interpolated position (one level of slack for
// interpolation error); the landing from the velocity track.
const fallLandingVz = -650.0

func (in *inputs) envCandidates(victim string, t int32, vpos vec3, vtrack *track) []candidate {
	var out []candidate
	if level, contents := in.bsp.waterLevelAt(vpos); level > 0 {
		switch contents {
		case bspvis.ContentsLava:
			lo := 10.0 * float64(max(1, level-1))
			hi := 10.0 * float64(min(3, level+1))
			out = append(out, candidate{geom: 0.12, attacker: "world",
				weapon: "lava", kind: "env", dEnd: -1, mLo: lo, mHi: hi})
		case bspvis.ContentsSlime:
			lo := 4.0 * float64(max(1, level-1))
			hi := 4.0 * float64(min(3, level+1))
			out = append(out, candidate{geom: 0.12, attacker: "world",
				weapon: "slime", kind: "env", dEnd: -1, mLo: lo, mHi: hi})
		default: // water: only drowning hurts, and only fully submerged
			// ... for 12+ seconds (air_finished): probe the last 12s of
			// track — anything less submerged and this delta cannot be a
			// drown tick, no matter how well the value fits (shotgun
			// damage overlaps the 4-14 drown range, and without this gate
			// sg hits on swimming victims got misread as drowning).
			if level >= 3 && in.submergedFor(vtrack, t, 11500) {
				out = append(out, candidate{geom: 0.12, attacker: "world",
					weapon: "drown", kind: "env", dEnd: -1, mLo: 4, mHi: 14})
			}
		}
	}
	if vtrack != nil {
		// The velocity columns are derived from ~13-40ms position samples,
		// so the pre-impact peak is routinely under-measured — probe a
		// window and accept a slightly softer threshold than the engine's
		// -650. Safe to loosen: the value gate (a landing is exactly 5) is
		// what keeps this candidate honest.
		if vz, ok := vtrack.minVzIn(t-300, t); ok && vz < fallLandingVz+75 {
			out = append(out, candidate{geom: 0.1, attacker: "world",
				weapon: "fall", kind: "env", dEnd: -1, mLo: 5, mHi: 5})
		}
	}
	return out
}

// submergedFor reports whether the victim's track shows full submersion
// (waterlevel 3) at every probe over the trailing durMs window.
func (in *inputs) submergedFor(vtrack *track, t int32, durMs int32) bool {
	if vtrack == nil {
		return false
	}
	const step = 1500
	for dt := int32(0); ; dt += step {
		if dt > durMs {
			dt = durMs // final probe lands exactly at the window edge
		}
		level, _ := in.bsp.waterLevelAt(vtrack.posAt(t - dt))
		if level < 3 {
			return false
		}
		if dt == durMs {
			return true
		}
	}
}

// envObituaryWeapon maps a suicide obituary's weapon token to the damage
// vocabulary's environmental category, or "" when the obituary does not
// name an environmental death ("water" is the obituary token for
// drowning; the damage log spells it "drown").
func envObituaryWeapon(w string) string {
	switch w {
	case "lava", "slime", "fall", "trigger":
		return w
	case "water", "drown":
		return "drown"
	}
	return ""
}

// dischargeCandidates: LG water discharges hitting the victim — a radius
// blast of 35*cells from the discharger's position. Expected damage is
// computed here (the model needs the per-discharge cells count, which the
// generic scorer cannot see) and carried via dEnd < 0 + a tight geom score;
// the model score treats "discharge" like a known-value hit.
func (in *inputs) dischargeCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
	for _, dc := range in.discharges {
		if abs32(dc.t-t) > 100 {
			continue
		}
		tr := in.tracks[dc.player]
		if tr == nil {
			continue
		}
		spos := tr.posAt(dc.t)
		d := spos.distTo(vpos)
		if d > dischargeReach(dc.cells) {
			continue
		}
		// Radius damage, engine order: the falloff comes off 35·cells inside
		// T_RadiusDamageApply and the quad multiplies the result in T_Damage
		// (ktx/src/weapons.c:1208, :1225; combat.c:1189, :537-543).
		expected := (35.0*float64(dc.cells) - 0.5*d) * in.quadFactor(dc.player, dc.t)
		if dc.player == victim {
			expected *= 0.5
		}
		if expected <= 0 {
			continue
		}
		if !in.bsp.reachesBody(eyeOf(spos), vpos) {
			continue
		}
		// Geometry prior, priced like a projectile's (d/geomNorm·0.5). The
		// flat 0.1 this used to charge made a discharge the cheapest
		// candidate on the board at ANY distance inside its reach — and that
		// reach is 35·cells + 40: 390 units at the 10-cell detection floor,
		// 740 at 20 cells, 3 540 at a 100-cell wipe. So a discharge
		// that merely happened somewhere in the pool could undercut a
		// candidate that actually has the victim in front of it, guarded only
		// by the ±25%+10 value band, which is ~90 points wide at E = 200 and
		// spans a whole quad rocket's range. Distance-priced, the close pool
		// fight the family exists for is unchanged (still 0.1 at ~52 units)
		// while a coincidental long-range one pays for its range.
		out = append(out, candidate{
			geom: d / geomNorm * 0.5, attacker: dc.player, weapon: "lg",
			kind: "discharge", dEnd: expected, isSplash: true,
			mLo: expected*0.75 - 10, mHi: expected*1.1 + 10,
		})
	}
	return out
}

// dischargeReach is the admission radius for an LG water discharge, the
// third radius-damage source in the model. Its base is 35·cells
// (ktx/src/weapons.c:1208, :1225) and T_RadiusDamage visits the same
// findradius(damage + 40) set as a rocket's, so the blast reaches
// 35·cells + 40 and no further — a bound the falloff alone does not give,
// since 35·cells − 0.5·dist stays positive out to twice that.
//
// Both endpoints here are interpolated position-track samples (the engine
// measures from the discharger's own origin to the victim's bbox centre), so
// the slack is the un-snapped one. The population it removes is real but
// currently inert: over the 60-demo dm2/dm3 ground truth, 111 discharges
// generate 275 candidates, 11 outside the engine's reach and 9 outside it
// even with the slack — and cutting them moves no scored row, because the
// per-candidate value band happened to refuse all nine anyway. It is kept as
// the engine bound rather than credited with an accuracy gain: that band is a
// ±25% window around a long-range blast's small expected value, which a small
// delta can sit inside, so the coincidence is not a rule.
func dischargeReach(cells int) float64 {
	return 35.0*float64(cells) + 40.0 + splashSlack(false)
}

// splashBase is the base damage a radius weapon hands T_RadiusDamage before
// the falloff: 120 for BOTH the rocket and the grenade
// (ktx/src/weapons.c:1006 and :1300, and id1 before them).
//
// It is emphatically not the rocket's direct value. T_MissileTouch deals its
// own constant to the entity it touched and then passes that entity as
// T_RadiusDamage's `ignore` (weapons.c:998-1006), so the two numbers belong
// to disjoint victims and only coincide by accident. Modelling rocket splash
// at the direct constant — as this package did until the direct/splash
// verdict became a trajectory question — understates every rocket splash by
// 10: measured on the dm2/dm3 ground truth, `value + 0.5·dist` over 2 530
// wire-flagged splash rows has median 122.4, not 112.
func splashBase() (lo, hi float64) { return 120.0, 120.0 }

// splashModel is what T_RadiusDamage deals at distance d: the falloff comes
// off the base first (points = damage − 0.5·dist, ktx/src/combat.c:1189),
// the self-halving is applied to those points inside the same function
// (:1190-1193), and only then does T_Damage multiply by the quad
// (:537-543). Kept as one function because the RAW kill top-ups need the
// value rather than a band.
//
// The top-ups used to price themselves off a base of 110 with 60 units of
// distance slack instead of this, and the wide, low pair was measured
// necessary at the time: they only ever RAISE a value, so they must
// under-estimate, and the quad multiplier standing on the wrong side of the
// falloff (4·120 − 0.5·d rather than 4·(120 − 0.5·d)) over-raised every quad
// splash kill by hundreds. With the order corrected the fudge is not merely
// unnecessary but harmful — swept over the 30-demo dm3 half and confirmed on
// the held-out dm2 half, raw given mean per-player error falls monotonically
// as the pair approaches the engine's own numbers: dm3 2.87% → 1.16% and dm2
// 3.44% → 1.38% going from (110, 60) to (120, 0). So there is no pair left
// to calibrate; the model is the engine's.
//
// Two honesty notes on that sweep. (120, 0) is the swept grid's CORNER, and
// NEGATIVE slack was deliberately never swept: the corner is the engine's own
// formula, and a pair beyond it would be a compensator fitted to this corpus'
// residual error rather than a model of what the server did. The stopping
// rule is engine truth, not the minimum of the curve. And the curve the sweep
// minimized is the per-player MEAN; on dm3 the raw-given MEDIAN prefers a
// base of 118 by a hair, which is the size of the residual the model does not
// claim to explain.
func splashModel(dist, quad, selfF float64) float64 {
	lo, _ := splashBase()
	return (lo - 0.5*dist) * quad * selfF
}

// killModelFloor is the value a killing hit's RAW cannot have been under,
// given the direct/splash verdict already stamped on the event. Both kill
// top-ups price themselves through it so they can never disagree.
//
// The verdict decides the CURVE, not just a label. A direct rocket is
// T_MissileTouch's flat constant with no falloff and no radius damage on top:
// the touched entity is handed to T_RadiusDamage as its `ignore`
// (ktx/src/weapons.c:998-1006), so the two numbers belong to disjoint
// victims. Pricing a direct off the 120 radius curve tops a point-blank quad
// direct up to 480 where the engine dealt 440. Only the rocket has a touch
// value — GrenadeTouch damages exclusively through GrenadeExplode →
// T_RadiusDamage (:1327-1333) — so a direct grenade rides the radius curve
// like any other.
//
// A floor may only assume the direct range's LOW end: a pre-1.36 server rolls
// 100 + random()*20 (weapons.c:986), and detectRocketRegime narrows the pair
// to 110..110 only on evidence. (pentSyntheticEvents takes the midpoint of
// the same range instead because it SYNTHESIZES a value rather than raising
// one — an under-estimate there is a lost hit, not a safe floor.)
//
// selfF carries T_RadiusDamageApply's `if (head == attacker) points *= 0.5`
// (ktx/src/combat.c:1190-1193), which lands on the post-falloff points and
// therefore inside splashModel, before T_Damage's quad. It has no direct
// branch because a rocket never touches its own owner (directImpact
// short-circuits on isSelf).
func (in *inputs) killModelFloor(weapon string, isSplash, isSelf bool, dist, quad float64) float64 {
	if weapon == "rl" && !isSplash {
		lo, _ := in.directBase()
		return lo * quad
	}
	selfF := 1.0
	if isSelf {
		selfF = 0.5
	}
	return splashModel(dist, quad, selfF)
}

// splashSlack is how far our explosion→victim distance can sit from the one
// the engine measured, for the two kinds of endpoint the tracking produces.
// It is the single statement of that error: the damage-model band widens by
// it and splashAdmit extends the engine's reach by it.
//
// An explosion-snapped endpoint (epExact) is the true detonation point, so
// three offsets remain: the victim's BBOX CENTRE, which the engine measures
// to and we do not (up to 4 units, the fixed hull's (mins+maxs)*0.5 = '0 0
// 4'); the 8-unit pull-back TE_EXPLOSION is written with, applied AFTER
// T_RadiusDamage has run at the true origin (ktx/src/weapons.c:1008-1010);
// and the victim position, interpolated between samples on the ~13-40 ms
// broadcast grid. Measured against the engine distance each wire value
// implies (unbound_dmg_dealt = ceil(q·(120 − 0.5·d)), so d = 2·(120 − v/q))
// over 14 140 wire-flagged enemy rl splash rows of the dm2/dm3 ground truth,
// the error runs median 4.8 — the two systematic terms — p95 18.1 and p99
// 27.4; on the 8-map golden cache, 5.0 / 22.2 / 35.2.
//
// A tracked flight that was NOT snapped ends at the last broadcast entity
// position instead, up to one server frame short of the detonation — 34
// units at the rocket's 1000 ups — which is what the wider figure covers.
// That case is unmeasurable on either ground-truth corpus (every GT splash
// row there carries a snapped endpoint), so it keeps the slack the damage
// model already carried for it rather than being invented afresh.
func splashSlack(epExact bool) float64 {
	if epExact {
		return 24.0
	}
	return 60.0
}

// splashAdmit is the greatest measured distance at which the engine's own
// reach can still have covered the victim — the candidate-admission radius,
// and the reason a candidate 300 units from the explosion is no longer
// entertained at all (it was, out to 380, until plan-damage-recon.md §8 lead
// B). Measured on the dm2/dm3 rl splash rows: the 184 an exact endpoint
// admits keeps 99.76% of them where the old 380 kept 99.78%. The 220 an
// un-snapped one admits is inert — holding it at 380 instead reproduces
// both the obituary-oracle sweep and the 188-demo derived-summary
// protocol byte for byte, because ~99% of demos carry TE_EXPLOSION and
// essentially every deciding candidate has an exact detonation point.
func splashAdmit(epExact bool) float64 { return splashReach + splashSlack(epExact) }

// directBase is what T_MissileTouch deals to the player the rocket touched:
// a flat 110 since KTX 1.36 (ktx/src/weapons.c:986; commit c7263e8f,
// 2008-09-29, replaced the id1 `100 + g_random()*20` — v1.35 and earlier
// still roll it). Which regime a demo's server ran is detected from the
// demo itself rather than from a version string (detectRocketRegime), so the
// range starts at the vanilla 100..120 and narrows to 110..110 on evidence.
func (in *inputs) directBase() (lo, hi float64) { return in.rlLo, in.rlHi }

// hasQuad reports whether the player held the quad at t; quadFactor is the
// same signal as the engine's ×4 damage multiplier. An unknown name (world,
// a departed player) is never quadded.
func (in *inputs) hasQuad(name string, t int32) bool {
	ap := in.players[name]
	return ap != nil && inIntervals(ap.Quad, t)
}

// (The ×4 is the deathmatch-1/3 multiplier. KTX makes the quad an OCTA in
// deathmatch 4 — `damage *= (deathmatch != 4 ? 4 : ... 8)`, combat.c:541 —
// and dmm4 is not one of the skipped modes, so a dmm4 recording's quad hits
// are modelled at half their true value. Unmeasured: no ground-truth corpus
// here contains one. plan-damage-recon.md §8 lead C.)
func (in *inputs) quadFactor(name string, t int32) float64 {
	if in.hasQuad(name, t) {
		return 4.0
	}
	return 1.0
}

// eyeOf: the shooter-side trace endpoint used by the BSP gates (los.go's
// eye constant, +22).
func eyeOf(p vec3) vec3 { return vec3{p.x, p.y, p.z + eyeOffsetZ} }

func (in *inputs) mkEvent(d delta, attacker, victim, weapon, kind string) reconEvent {
	return in.mkEventSplash(d, attacker, victim, weapon, kind, false)
}

func (in *inputs) mkEventSplash(d delta, attacker, victim, weapon, kind string, isSplash bool) reconEvent {
	// (dEnd defaults to 0 on anchored/positional events; only candidate
	// wins overwrite it — regime detection filters on kind first.)
	isSelf := attacker == victim
	isEnv := attacker == "world"
	aTeam, aOK := in.teams[attacker]
	vTeam := in.teams[victim]
	isTeam := !isSelf && !isEnv && aOK && aTeam != "" && aTeam == vTeam && !in.duel
	e := reconEvent{
		t: d.t, attacker: attacker, victim: victim, weapon: weapon,
		raw: d.raw, bounded: d.bounded, died: d.died,
		isSelf: isSelf, isTeam: isTeam, isEnv: isEnv, isSplash: isSplash,
		kind: kind,
	}
	return e
}

// victimWeaponClass classifies the victim's held weapons at t into the
// EWep buckets (KTX ewep semantics, target inventory; NG counts as
// shotgun-tier, matching analyzer/damage.go victimWeaponClass).
//
// Boundary rule: KTX samples the victim's inventory MID-frame (inside
// T_Damage) while the StatItems broadcasts that open/close our intervals
// land at END of frame — so at an exact-instant boundary the pre-instant
// inventory is authoritative: an interval closing at t (death reset) still
// covers a hit at t, and one opening at t (same-frame pickup) does not yet.
// Hence exclusive start, inclusive end (inWeaponIntervals), unlike the
// half-open inIntervals the time-window checks use.
func victimWeaponClass(p *result.PlayerStream, t int32) string {
	hasRL := inWeaponIntervals(p.RL, t)
	hasLG := inWeaponIntervals(p.LG, t)
	switch {
	case hasRL && hasLG:
		return "both"
	case hasRL:
		return "rl"
	case hasLG:
		return "lg"
	case inWeaponIntervals(p.GL, t) || inWeaponIntervals(p.SSG, t) || inWeaponIntervals(p.SNG, t):
		return "mid"
	default:
		return "sg"
	}
}

// inWeaponIntervals: exclusive-start / inclusive-end containment (see
// victimWeaponClass for why weapon inventory boundaries differ from the
// half-open convention).
func inWeaponIntervals(ivs []result.Interval, t int32) bool {
	for _, iv := range ivs {
		if iv.Start < t && t <= iv.End {
			return true
		}
	}
	return false
}
