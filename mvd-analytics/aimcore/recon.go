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
)

// reconTierWeapons is the set of weapons the tier SHIPS: those whose damage
// lands in the fire's own server frame (the axe at its fixed traceline delay),
// where the join was validated EXACT against the wire log — see
// damagerecon/ACCURACY.md §"Aim hit recovery" for the measurement and
// cmd/qw-aim-eval for the harness.
//
// rl/gl/ng/sng are deliberately absent. Their impact is a detonation an
// unbounded flight after the fire, and what pins which projectile caused which
// impact is the entity-flight bracket the shots analyzer builds from
// ProjectileSpawn/Despawn and discards — not reachable from a finished Result.
// Counting impacts instead answers a different question than the measured
// counter does (which counts fires whose flight LINKED, so a point-blank
// rocket that never broadcast its entity is measured as a miss): on the wire
// log itself the two differ by +6.8pp on rl, so shipping it would put a
// reconstructed rl accuracy 7 points above the measured convention. A weapon
// outside this set gets no Recon block at all rather than a weak one — its
// fires still publish Shots.
var reconTierWeapons = map[string]bool{
	"lg": true, "sg": true, "ssg": true, "axe": true,
}

// reconEvalWeapons is every weapon the join CAN be run for — the shipped tier
// plus the projectiles it withholds. Reachable only through ReconHitsForEval:
// the measurement that decides what ships must not be gated on what shipped
// (before this existed, reproducing the rl/gl numbers in ACCURACY.md needed a
// hand-edit of reconTierWeapons).
var reconEvalWeapons = map[string]bool{
	"lg": true, "sg": true, "ssg": true, "axe": true,
	"rl": true, "gl": true, "ng": true, "sng": true,
}

// Projectile fire→impact bounds, used by the EVAL path only (reconEvalWeapons).
// Each is the projectile's own lifetime in ktx/src/weapons.c — rocket
// nextthink+10 (:1076), grenade fuse +2.5 (:1430), spike +6 (:1471) — i.e. the
// widest a fire and its own impact can physically be apart. Deliberately the
// loosest possible bound: the question the control asks is "what does counting
// impacts give", so the window must not be what limits the answer.
const (
	reconRocketLifetimeMs  = 10000
	reconGrenadeFuseMs     = 2500
	reconNailLifetimeMs    = 6000
	reconProjJitterMs      = 100 // stat-instant quantization at each end
	reconProjBackwardSlack = 100 // an impact frame just before its own fire sound
)

// reconLinkWindow returns the [lo,hi] offsets from a fire's timestamp within
// which an impact of that weapon may be claimed by it.
func reconLinkWindow(weapon string) (lo, hi int32, ok bool) {
	switch weapon {
	case "lg":
		return -reconBeamWindowMs, reconBeamWindowMs, true
	case "sg", "ssg":
		return -reconHitscanWindowMs, reconHitscanWindowMs, true
	case "axe":
		return reconAxeDelayMs - reconAxeJitterMs, reconAxeDelayMs + reconAxeJitterMs, true
	case "rl":
		return -reconProjBackwardSlack, reconRocketLifetimeMs + reconProjJitterMs, true
	case "gl":
		return -reconProjBackwardSlack, reconGrenadeFuseMs + reconProjJitterMs, true
	case "ng", "sng":
		return -reconProjBackwardSlack, reconNailLifetimeMs + reconProjJitterMs, true
	}
	return 0, 0, false
}

// reconDamageByAttacker collects the reconstructed damage rows of res keyed by
// the attacker to credit, scoped by inWindow. What it drops differs from the
// measured collection in Compute because it asks a different question —
// LINKAGE, not magnitude: a self hit is a fire that connected and is kept (the
// wire join counts those too), while an environmental row has no shooter to
// credit and is not.
func reconDamageByAttacker(res *result.Result, inWindow func(int32) bool) map[string][]*dmgRec {
	out := make(map[string][]*dmgRec)
	for _, d := range res.Damage.Events {
		if d.Attacker == "" || d.IsEnv || !inWindow(d.Time) {
			continue
		}
		out[d.Attacker] = append(out[d.Attacker],
			&dmgRec{t: d.Time, weapon: d.Weapon, dmg: d.Damage, splash: d.IsSplash, team: d.IsTeam})
	}
	return out
}

// ReconHitsForEval runs the fire→reconstructed-damage join over the WHOLE match
// for every weapon it can be run for (reconEvalWeapons) — including the
// rl/gl/ng/sng the shipped tier withholds — and returns hits per player per
// weapon. Nil unless res carries a reconstructed damage section.
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
	dmg := reconDamageByAttacker(res, func(int32) bool { return true })
	out := make(map[string]map[string]int, len(byPlayer))
	for p, shots := range byPlayer {
		if h := reconHitsByWeapon(shots, dmg[p], reconEvalWeapons); len(h) > 0 {
			out[p] = h
		}
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
func reconHitsByWeapon(shots []result.Shot, dmg []*dmgRec, tier map[string]bool) map[string]int {
	if len(shots) == 0 {
		return nil
	}
	// Fires per weapon, time-ordered, with a claim flag.
	type fire struct {
		t       int32
		claimed bool
	}
	fires := make(map[string][]*fire)
	for i := range shots {
		w := shots[i].Weapon
		if !tier[w] {
			continue
		}
		fires[w] = append(fires[w], &fire{t: shots[i].Time})
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
	byWeapon := make(map[string][]int32)
	for _, d := range dmg {
		if fires[d.weapon] == nil {
			continue
		}
		byWeapon[d.weapon] = append(byWeapon[d.weapon], d.t)
	}
	out := make(map[string]int, len(fires))
	for w, f := range fires {
		out[w] = 0 // an honest zero: the weapon was fired and linked nothing
		ts := byWeapon[w]
		if len(ts) == 0 {
			continue
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
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
