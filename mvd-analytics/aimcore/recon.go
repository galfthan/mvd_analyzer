package aimcore

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Reconstructed-tier hit recovery.
//
// On a demo whose damage section was reconstructed (damage.source ==
// "reconstructed") the shot linker never saw a wire DamageEvent, so every
// Shot.Hit is false and the measured counters are withheld (see
// AimResult.HitsMeasured). The damage the reconstruction DID recover is still
// a per-hit log with an attacker, a weapon and a time, so the same fire→damage
// join the shots analyzer runs on the wire stream can be re-run against it —
// producing a hit count that is honest about its evidence grade because it
// lands in its own result block (result.WeaponAimRecon).
//
// Two things differ from the wire join and drive every constant below.
//
//  1. TIMING ANCHOR. A wire damage event is multicast by T_Damage in the
//     server frame the hit landed; a reconstructed one is anchored at the
//     VICTIM's health/armor stat instant, i.e. at the MVD frame in which the
//     victim's stat update was written — which at the default sv_demofps 30
//     is a ~34 ms grid (mvdsv/src/sv_send.c:1339-1346) rather than the
//     server's own. Measured (cmd/qw-aim-eval -diag, 13-demo cache): the two
//     land on the SAME instant 97.3% of the time and within 10 ms 97.5%, so
//     the shift is small — but the windows below are deliberately
//     damagerecon's OWN attribution tolerances (attribution.go tolShotMs /
//     tolBeamMs / axeSwingDelayMs) rather than the shots analyzer's tighter
//     26 ms: those are the tolerances the damage log was BUILT with, so a
//     delta further from a fire than that is one the reconstruction did not
//     explain by that fire either, and linking it here would assert a link
//     the log does not. Each stays well inside its weapon's refire, so
//     consecutive fires cannot cross-link.
//
//  2. GRANULARITY. Several wire hits landing on one stat instant merge into
//     ONE reconstructed delta — one event, one attacker, one summed
//     magnitude — so the event count is not the hit count in either
//     direction. The join therefore counts IMPACTS: events of the same
//     attacker+weapon within one demo frame are one impact (a merged delta
//     already is one), and each impact claims at most one fire. Magnitude is
//     never read, which is why the pellet split cannot be recovered (see
//     result.WeaponAimRecon for the full withheld list).
//
// The projectile weapons (rl/gl) anchor differently, in reconFlightHits: not
// on the fire but on the impact of the flight the fire launched, read off
// Shot.FlightEnd. That is the measured counter's own definition rather than an
// approximation of it — a fire whose projectile was never tracked is a miss on
// both sides — and it is what makes rl/gl comparable to a modern demo's
// numbers at all (see reconTierWeapons).
const (
	// reconHitscanWindowMs mirrors damagerecon's tolShotMs: the tolerance
	// with which its attribution pairs a hitscan FIRE SOUND with a damage
	// instant. Well inside the sg/ssg refire (500/700 ms), so consecutive
	// fires cannot cross-link.
	reconHitscanWindowMs = 60

	// reconBeamWindowMs mirrors damagerecon's tolBeamMs: an LG fire is
	// observed as the beam itself (the aim shot source for lg is the beam,
	// not a sound), and beam-to-damage-frame measured 0 on the eval corpus.
	// Kept under the 100 ms LG refire so two ticks of one shaft stay distinct.
	reconBeamWindowMs = 30

	// The axe's traceline runs two 0.1 s animation thinks after its swing
	// sound (ktx/src/player.c player_axe3 → W_FireAxe); same delay+jitter the
	// shots analyzer and damagerecon both use.
	reconAxeDelayMs  = 200
	reconAxeJitterMs = 80

	// reconImpactMergeMs is one MVD frame at the default sv_demofps 30 — the
	// coarsest grid a stat update lands on. Same-weapon damage by one
	// attacker inside that span is one impact: either the wire hit several
	// victims at once (one fire) or the victims' stat updates straddled a
	// frame boundary.
	reconImpactMergeMs = 34

	// Projectile flight-end → reconstructed-damage bounds, used by the
	// FLIGHT join (reconFlightWeapons). They mirror damagerecon's own
	// projectile tolerances (attribution.go tolProjAfterMs / tolProjBeforeMs,
	// measured p5=−81 / p99=+261 of despawn-to-damage-frame lag): the
	// reconstruction only ever explained a delta by a flight whose end sat
	// inside that band, so a delta outside it is one this flight did not
	// cause according to the very log being joined. Asymmetric because the
	// tracked despawn usually PRECEDES the victim's stat instant.
	reconFlightDmgBeforeMs = 81
	reconFlightDmgAfterMs  = 261
)

// reconTierWeapons is the set of weapons the tier SHIPS, in two families.
//
// The SAME-FRAME family (lg/sg/ssg, and the axe at its fixed traceline delay)
// links a fire to damage in the fire's own server frame, and the join was
// validated EXACT against the wire log.
//
// The FLIGHT family (rl/gl, reconFlightWeapons) links a fire to the damage its
// TRACKED PROJECTILE caused, on the measured counter's own definition: a fire
// whose flight was never tracked is a miss, exactly as analyzer/shots.go
// linkProjectiles counts it. That definition needs the fire→flight association
// the shots analyzer used to discard; since v74 it is published as
// Shot.FlightEnd, which is what makes the projectile side shippable at all.
// Before it existed the join could only count reconstructed IMPACTS — a
// different question (a point-blank rocket that never broadcast an entity is
// measured as a miss but is an impact) that read +7.3pp above the measured rl
// convention on the wire log itself.
//
// See damagerecon/ACCURACY.md §"Aim hit recovery" for both families'
// measurements and cmd/qw-aim-eval for the harness. ng/sng stay out: their
// measured counter is 0 on every eval row (nail linking is opt-in and was
// never on), so there is no ground truth to validate a recovery against. A
// weapon outside this set gets no Recon block at all rather than a weak one —
// its fires still publish Shots.
var reconTierWeapons = map[string]bool{
	"lg": true, "sg": true, "ssg": true, "axe": true,
	"rl": true, "gl": true,
}

// reconFlightWeapons is the subset of the join whose fires are matched to
// damage through their tracked projectile flight (Shot.FlightEnd) rather than
// through their own fire time. Nails are NOT here: their flights are only
// bracketed when nail decoding is enabled, so FlightEnd is absent on a default
// parse and a flight join would report an unconditional zero — they keep the
// impact-count join, which the eval path is the only caller of.
var reconFlightWeapons = map[string]bool{"rl": true, "gl": true}

// reconEvalWeapons is every weapon the join CAN be run for — the shipped tier
// plus the nails it withholds. Reachable only through ReconHitsForEval: the
// measurement that decides what ships must not be gated on what shipped
// (before this existed, reproducing the rl/gl numbers in ACCURACY.md needed a
// hand-edit of reconTierWeapons).
var reconEvalWeapons = map[string]bool{
	"lg": true, "sg": true, "ssg": true, "axe": true,
	"rl": true, "gl": true, "ng": true, "sng": true,
}

// Nail fire→impact bounds, used by the EVAL path only (ng/sng, which the
// shipped tier withholds and which have no tracked flight on a default parse).
// The bound is the spike's own lifetime in ktx/src/weapons.c (+6 s, :1471) —
// the widest a fire and its own impact can physically be apart. Deliberately
// the loosest possible bound: the question the eval asks is "what does
// counting impacts give", so the window must not be what limits the answer.
const (
	reconNailLifetimeMs    = 6000
	reconProjJitterMs      = 100 // stat-instant quantization at each end
	reconProjBackwardSlack = 100 // an impact frame just before its own fire sound
)

// reconLinkWindow returns the [lo,hi] offsets from a fire's timestamp within
// which an impact of that weapon may be claimed by it. rl/gl are absent: they
// are joined through their flight (reconFlightWeapons), not their fire time.
func reconLinkWindow(weapon string) (lo, hi int32, ok bool) {
	switch weapon {
	case "lg":
		return -reconBeamWindowMs, reconBeamWindowMs, true
	case "sg", "ssg":
		return -reconHitscanWindowMs, reconHitscanWindowMs, true
	case "axe":
		return reconAxeDelayMs - reconAxeJitterMs, reconAxeDelayMs + reconAxeJitterMs, true
	case "ng", "sng":
		return -reconProjBackwardSlack, reconNailLifetimeMs + reconProjJitterMs, true
	}
	return 0, 0, false
}

// reconDamageByAttacker collects the reconstructed damage rows of res keyed by
// the attacker to credit. What it drops differs from the measured collection in
// Compute because it asks a different question — LINKAGE, not magnitude: a self
// hit is a fire that connected and is kept (the wire join counts those too),
// while an environmental row has no shooter to credit and is not.
//
// The pool is MATCH-WIDE even under a windowed Compute query, which windows the
// fires. A window scopes whose shots are counted, not what evidence they may be
// judged on — and the two are not the same interval for a projectile, whose
// damage trails its fire by the whole flight (a gl fuse is 2.5 s) plus the
// stat-instant lag. Clipping the pool to the fire window would score a grenade
// thrown just before `to` as a miss purely because the window was cut there,
// while the measured tier it is compared against links Shot.Hit match-wide
// before any window exists. Nothing leaks in the other direction: damage from a
// pre-window fire is only reachable if it sits inside an in-window fire's own
// join window, which is exactly the case where crediting it is right — and the
// join claims each fire at most once, so a wider pool can never lift a weapon's
// hits above its (windowed) fire count. Measured over a 60 s/30 s window sweep
// of the 13-demo golden cache: 45 of 402 windows change, 47 hits recovered and
// none lost — the one-sided move the argument predicts.
func reconDamageByAttacker(res *result.Result) map[string][]*dmgRec {
	out := make(map[string][]*dmgRec)
	for _, d := range res.Damage.Events {
		if d.Attacker == "" || d.IsEnv {
			continue
		}
		out[d.Attacker] = append(out[d.Attacker],
			&dmgRec{t: d.Time, weapon: d.Weapon, dmg: d.Damage, team: d.IsTeam, self: d.IsSelf})
	}
	return out
}

// ReconHitsForEval runs the fire→reconstructed-damage join over the WHOLE match
// for every weapon it can be run for (reconEvalWeapons) — including the ng/sng
// the shipped tier withholds — and returns hits per player per weapon. Nil
// unless res carries a reconstructed damage section.
//
// It exists for cmd/qw-aim-eval and nothing else: what a weapon's join costs in
// accuracy is the measurement that DECIDES whether it ships, so it cannot be
// gated on it having shipped. For the weapons in reconTierWeapons it returns
// exactly what Compute publishes (same join, same windows, per-weapon
// independent); for the rest it is a measurement of an alternative the pipeline
// deliberately does not publish — see the reconTierWeapons comment and
// damagerecon/ACCURACY.md §"Aim hit recovery" for why.
func ReconHitsForEval(res *result.Result) map[string]map[string]int {
	if res == nil || res.Shots == nil || res.Damage == nil ||
		res.Damage.Source != result.DamageSourceReconstructed {
		return nil
	}
	byPlayer := make(map[string][]result.Shot)
	for _, s := range res.Shots.Shots {
		byPlayer[s.Player] = append(byPlayer[s.Player], s)
	}
	dmg := reconDamageByAttacker(res)
	out := make(map[string]map[string]int, len(byPlayer))
	for p, shots := range byPlayer {
		if h := reconHitsByWeapon(shots, dmg[p], reconEvalWeapons); len(h) > 0 {
			out[p] = h
		}
	}
	return out
}

// ReconDirectHits counts, per player per weapon, the rl/gl projectiles the
// reconstruction says TOUCHED a player — the convention KTX's own
// `acc.rl.hits` / `acc.gl.hits` counts, because KTX increments that counter
// in the touch handler and nowhere else (ktx/src/weapons.c:990-996, :1327-1333).
// Nil unless res carries a reconstructed damage section.
//
// It is not a join at all, and that is the point. One projectile touches at
// most one player, so a touch IS a direct row: counting the reconstructed
// log's non-splash rl/gl rows per attacker is the whole derivation. The same
// count taken over the WIRE log reproduces the verbatim KTX block on 638 of
// 638 player rows, so this is the wire control's own arithmetic applied to
// the reconstructed log — which is what makes the two eras comparable.
//
// Deliberately NOT routed through the fire→flight join that produces
// WeaponAimRecon.Hits. That join asks whether a FIRE connected and treats a
// rocket whose entity was never broadcast as a miss, matching this
// pipeline's measured `hits` counter. KTX's touch counter has no such
// notion — a point-blank rocket that touches still increments it — so
// joining through the flight throws those away: measured against the
// verbatim block, the flight-joined variant runs 9.5% aggregate error where
// this one runs 0.65% (damagerecon/ACCURACY.md).
//
// Weapons the player FIRED are seeded at zero, so "fired ten rockets and
// touched nobody" is a supported zero rather than an absence. Self rows are
// excluded: a missile never touches its owner (weapons.c:954, :1317), so a
// direct self row would be a contradiction, not a hit.
//
// WINDOWED, unlike the damage pool feeding the fire→damage join. That pool is
// match-wide because it is EVIDENCE a windowed fire may be judged on, and the
// join is what scopes it; this count has no join to scope it — the rows ARE
// the number — so a match-wide count published inside a window-scoped block
// would report the whole match's touches against the window's fires
// (`/aim?from=0&to=60000` could read 100% direct accuracy). The rows are
// therefore filtered on their own damage instant, exactly as the shots are
// filtered on their fire time. The two edges do not line up to the
// millisecond — a rocket fired at `to−0.2 s` lands its touch after `to` and
// is a fire without its row, one fired just before `from` can land inside —
// but that mismatch is bounded by a single flight at each edge and by the
// caller's clamp to the weapon's in-window fire count, where a match-wide
// count is unbounded.
func ReconDirectHits(res *result.Result, q Query) map[string]map[string]int {
	if res == nil || res.Shots == nil || res.Damage == nil ||
		res.Damage.Source != result.DamageSourceReconstructed {
		return nil
	}
	inWindow := func(t int32) bool {
		if q.FromMs != nil && t < *q.FromMs {
			return false
		}
		if q.ToMs != nil && t > *q.ToMs {
			return false
		}
		return true
	}
	out := make(map[string]map[string]int)
	seed := func(player, weapon string) {
		if out[player] == nil {
			out[player] = map[string]int{}
		}
		if _, ok := out[player][weapon]; !ok {
			out[player][weapon] = 0
		}
	}
	for _, s := range res.Shots.Shots {
		if reconFlightWeapons[s.Weapon] && inWindow(s.Time) {
			seed(s.Player, s.Weapon)
		}
	}
	for _, d := range res.Damage.Events {
		if d.Attacker == "" || d.IsEnv || d.IsSelf || d.IsSplash {
			continue
		}
		if !reconFlightWeapons[d.Weapon] || !inWindow(d.Time) {
			continue
		}
		seed(d.Attacker, d.Weapon)
		out[d.Attacker][d.Weapon]++
	}
	return out
}

// reconHitsByWeapon links one player's fires to the reconstructed damage they
// caused and returns the per-weapon count of fires that connected, for the
// weapons in `tier` (reconTierWeapons in the pipeline; reconEvalWeapons under
// ReconHitsForEval). A weapon the player fired is always in the returned map,
// with an honest zero when nothing linked — the CALLER decides whether a block
// is emitted, and it emits one for every fired tier weapon.
//
// Two joins live here, one per weapon family. rl/gl (reconFlightWeapons) go
// through reconFlightHits, which anchors on the fire's tracked flight; every
// other weapon anchors on the fire itself and is matched below.
//
// Impacts are claimed in time order, each taking the EARLIEST unclaimed fire
// whose window covers it; no fire can be claimed twice (the wire join's `used`
// flag, applied from the impact side because a reconstructed impact — unlike a
// wire event — may correspond to more than one wire hit).
//
// Earliest-first is not a preference, it is what makes the count right when
// windows overlap. Both sequences are time-ordered and the window is a fixed
// offset, so each impact's feasible fires are a contiguous run whose bounds
// only move forward — the staircase structure in which "take the earliest
// still-free fire" is an exchange-argument-optimal maximum matching. Taking
// the LATEST instead loses hits: with lg fires at 0/50 ms and impacts at
// 20/55 ms (±30 ms window) the 20 ms impact would claim the FUTURE 50 ms fire
// and the 55 ms impact would then find nothing, counting 1 of 2 (pinned by
// TestReconTierOverlappingWindowsPairUp).
//
// It returns the ANY-PATH count alone. The KTX-convention touch count for
// rl/gl was tried as a second return here — the subset of connecting fires
// whose claimed impact carried a non-splash row — and measured 9.5% aggregate
// error against the verbatim block, because a flight join throws away every
// touch whose projectile the server never broadcast. It lives in
// ReconDirectHits instead, as a row count with no join at all.
func reconHitsByWeapon(shots []result.Shot, dmg []*dmgRec, tier map[string]bool) map[string]int {
	if len(shots) == 0 {
		return nil
	}
	// Fires per weapon, time-ordered, with a claim flag. end carries the
	// fire's tracked-flight impact time (Shot.FlightEnd) for the flight
	// family; nil there means the projectile was never tracked, which the
	// measured counter reads as a miss.
	type fire struct {
		t       int32
		end     *int32
		claimed bool
	}
	fires := make(map[string][]*fire)
	for i := range shots {
		w := shots[i].Weapon
		if !tier[w] {
			continue
		}
		fires[w] = append(fires[w], &fire{t: shots[i].Time, end: shots[i].FlightEnd})
	}
	if len(fires) == 0 {
		return nil
	}
	for w := range fires {
		f := fires[w]
		sort.SliceStable(f, func(i, j int) bool { return f[i].t < f[j].t })
	}

	// Impacts per weapon: same-weapon damage instants merged on the demo-frame
	// grid (see reconImpactMergeMs).
	byWeapon := make(map[string][]*dmgRec)
	for _, d := range dmg {
		if fires[d.weapon] == nil {
			continue
		}
		byWeapon[d.weapon] = append(byWeapon[d.weapon], d)
	}
	out := make(map[string]int, len(fires))
	for w, f := range fires {
		out[w] = 0 // an honest zero: the weapon was fired and linked nothing
		recs := byWeapon[w]
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].t < recs[j].t })
		ts := make([]int32, len(recs))
		for i, d := range recs {
			ts[i] = d.t
		}
		if reconFlightWeapons[w] {
			ends := make([]int32, 0, len(f))
			for _, fi := range f {
				if fi.end != nil {
					ends = append(ends, *fi.end)
				}
			}
			sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
			out[w] = reconFlightHits(ends, recs)
			continue
		}
		if len(ts) == 0 {
			continue
		}
		lo, hi, ok := reconLinkWindow(w)
		if !ok {
			continue
		}
		var impacts []int32
		for _, t := range ts {
			if n := len(impacts); n > 0 && t-impacts[n-1] <= reconImpactMergeMs {
				continue // same impact as the previous instant
			}
			impacts = append(impacts, t)
		}
		// start is the first fire an impact can still reach: impacts are
		// time-ordered, so a fire past its window — or already claimed — is
		// dead for every remaining impact too.
		start := 0
		for _, t := range impacts {
			for start < len(f) && (t-f[start].t > hi || f[start].claimed) {
				start++
			}
			for i := start; i < len(f); i++ {
				if t-f[i].t < lo {
					break // fires are time-ordered; the rest are later still
				}
				if !f[i].claimed {
					f[i].claimed = true
					out[w]++
					break
				}
			}
		}
	}
	return out
}

// reconFlightHits is the PROJECTILE join: it counts how many of one player's
// rl (or gl) fires connected, given the ascending impact times of their tracked
// flights (Shot.FlightEnd, one entry per fire that HAD a flight) and the
// ascending times of that player's same-weapon reconstructed damage.
//
// It reproduces the measured counter's definition rather than approximating it
// (analyzer/shots.go linkProjectiles):
//
//   - a fire whose projectile was never tracked contributes no `end` and is a
//     miss — the same verdict the wire join reaches, because a flight is what
//     pins a fire to an impact there too. This is the whole reason the
//     projectile side can ship: counting reconstructed impacts instead credits
//     those fires and reads ~7pp high on rl (see reconTierWeapons);
//   - a flight claims ONE damage instant — the earliest unclaimed one its
//     window covers, together with that instant's frame-mates — and counts one
//     hit, so a rocket that hurt three players is one hit, the wire join's
//     multi-victim behaviour;
//   - a claimed damage instant is consumed, so two flights ending in the same
//     frame cannot both count the same explosion. The wire join consumes the
//     same way (its `used` flag), including the case where one flight swallows
//     a second flight's frame-mate.
//
// Flights are processed in impact order, each taking the earliest unclaimed
// instant its window covers — the same exchange-argument-optimal matching the
// same-frame join uses, and for the same reason: both sequences are ordered
// and the window is a fixed offset from the flight's end.
func reconFlightHits(ends []int32, damage []*dmgRec) int {
	if len(ends) == 0 || len(damage) == 0 {
		return 0
	}
	used := make([]bool, len(damage))
	hits := 0
	// start is the first damage instant a flight can still reach: ends are
	// ordered, so an instant before this flight's window — or already claimed
	// — is dead for every later flight too.
	start := 0
	for _, end := range ends {
		for start < len(damage) && (damage[start].t < end-reconFlightDmgBeforeMs || used[start]) {
			start++
		}
		claimed := -1
		for i := start; i < len(damage) && damage[i].t <= end+reconFlightDmgAfterMs; i++ {
			if !used[i] {
				claimed = i
				break
			}
		}
		if claimed < 0 {
			continue
		}
		hits++
		// Consume the whole impact instant: one explosion damaging several
		// victims writes one row per victim, all on the same stat frame (see
		// reconImpactMergeMs), and none of them is another flight's evidence.
		//
		// One frame is enough for that in practice. Anchored on the WIRE link
		// (which names an explosion's victims exactly), 5 of the 2436
		// multi-victim rl/gl explosions on the 53-demo dm2/dm3 eval corpus put
		// their victims' reconstructed rows more than one frame apart — 0.2% of
		// multi-victim explosions, 0.03% of all 17581. Only those can strand a
		// row for a later flight to adopt, capping the resulting over-count at
		// ~0.01 pp of rl accuracy, two orders below the tier's measured 0.7 pp.
		// Widening the span would instead merge genuinely distinct explosions
		// (a second rocket landing a frame later is a second hit), so the span
		// stays the one the granularity argument justifies.
		for i := claimed; i < len(damage) && damage[i].t-damage[claimed].t <= reconImpactMergeMs; i++ {
			used[i] = true
		}
	}
	return hits
}
