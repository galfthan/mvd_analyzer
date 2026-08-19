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

// reconTierWeapons is the set of weapons the tier covers: those whose damage
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

// reconLinkWindow returns the [lo,hi] offsets from a fire's timestamp within
// which an impact of that weapon may be claimed by it. Only the reconTierWeapons
// have one, by construction.
func reconLinkWindow(weapon string) (lo, hi int32, ok bool) {
	switch weapon {
	case "lg":
		return -reconBeamWindowMs, reconBeamWindowMs, true
	case "sg", "ssg":
		return -reconHitscanWindowMs, reconHitscanWindowMs, true
	case "axe":
		return reconAxeDelayMs - reconAxeJitterMs, reconAxeDelayMs + reconAxeJitterMs, true
	}
	return 0, 0, false
}

// reconHitsByWeapon links one player's fires to the reconstructed damage they
// caused and returns the per-weapon count of fires that connected.
//
// Impacts are claimed in time order, each taking the LATEST unclaimed fire
// whose window covers it, so a burst of fires and a burst of impacts pair up
// nearest-first and no fire is counted twice (the wire join's `used` flag,
// applied from the impact side because a reconstructed impact — unlike a wire
// event — may correspond to more than one wire hit).
func reconHitsByWeapon(shots []result.Shot, dmg []*dmgRec) map[string]int {
	if len(shots) == 0 || len(dmg) == 0 {
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
		if !reconTierWeapons[w] {
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
		// time-ordered, so a fire older than the widest window is dead for
		// every remaining impact too.
		start := 0
		for _, t := range impacts {
			for start < len(f) && t-f[start].t > hi {
				start++
			}
			best := -1
			for i := start; i < len(f); i++ {
				dt := t - f[i].t
				if dt < lo {
					break // fires are time-ordered; the rest are later still
				}
				if !f[i].claimed {
					best = i
				}
			}
			if best >= 0 {
				f[best].claimed = true
				out[w]++
			}
		}
	}
	return out
}
