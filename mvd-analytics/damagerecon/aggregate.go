package damagerecon

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// aggregate assembles the reconstructed events into a DamageResult with
// exactly the shapes and bucket semantics of the KTX-derived analyzer
// (analyzer/damage.go Finalize): the per-hit Events log, per-player
// aggregates in both families, the attacker→victim matrix, and the
// positional-kill fold-ins.
func aggregate(in *inputs, events []reconEvent) *result.DamageResult {
	out := &result.DamageResult{
		ByWeapon: make(map[string]int),
		ByPlayer: make(map[string]*result.PlayerDamage),
		Dmg:      "both",
		// The bounded family here is a direct observation (the h/a delta IS
		// KTX dmg_dealt), so the standard arithmetic label applies; the
		// skipped modes were rejected before reconstruction started.
		BoundedMode: "standard",
		Source:      result.DamageSourceReconstructed,
	}
	matrix := make(map[[2]string]*result.DamagePair)

	for _, e := range events {
		if e.kind == "positional" {
			aggregatePositional(in, out, e)
			continue
		}

		vw := ""
		if !e.isEnv && !e.isSelf && !e.isTeam && in.weaponBitsLive {
			if vp := in.players[e.victim]; vp != nil {
				vw = victimWeaponClass(vp, e.t)
			}
		}

		entry := result.DamageEntry{
			Time:      e.t,
			Attacker:  e.attacker,
			Victim:    e.victim,
			Weapon:    e.weapon,
			Damage:    e.raw,
			IsSplash:  e.isSplash,
			IsEnv:     e.isEnv,
			IsSelf:    e.isSelf,
			IsTeam:    e.isTeam,
			VictimWep: vw,
		}
		if e.bounded != e.raw {
			b := e.bounded
			entry.Bounded = &b
		}
		out.Events = append(out.Events, entry)
		out.TotalDamage += e.raw

		vp := getOrCreate(out.ByPlayer, e.victim)
		vp.Taken += e.raw
		vb := vp.BoundedNest()
		vb.Taken += e.bounded
		if e.isEnv {
			vp.TakenEnv += e.raw
			vb.TakenEnv += e.bounded
		}
		if e.isEnv {
			continue // no attacker to credit
		}

		ap := getOrCreate(out.ByPlayer, e.attacker)
		ab := ap.BoundedNest()
		switch {
		case e.isSelf:
			ap.GivenSelf += e.raw
			ap.ByWeaponSelf = result.AddWeaponDamage(ap.ByWeaponSelf, e.weapon, e.raw)
			ab.GivenSelf += e.bounded
			ab.ByWeaponSelf = result.AddWeaponDamage(ab.ByWeaponSelf, e.weapon, e.bounded)
		case e.isTeam:
			ap.GivenTeam += e.raw
			ap.ByWeaponTeam = result.AddWeaponDamage(ap.ByWeaponTeam, e.weapon, e.raw)
			ab.GivenTeam += e.bounded
			ab.ByWeaponTeam = result.AddWeaponDamage(ab.ByWeaponTeam, e.weapon, e.bounded)
		default:
			ap.Given += e.raw
			ap.ByWeapon = result.AddWeaponDamage(ap.ByWeapon, e.weapon, e.raw)
			out.ByWeapon[e.weapon] += e.raw
			addToMatrix(matrix, e.attacker, e.victim, e.weapon, e.raw)
			if vw != "" {
				addVictimWeaponBucket(ap, vw, e.raw)
			}
			ab.Given += e.bounded
			ab.ByWeapon = result.AddWeaponDamage(ab.ByWeapon, e.weapon, e.bounded)
			if vw != "" {
				addVictimWeaponBucket(ab, vw, e.bounded)
			}
		}
	}

	out.Matrix = flattenMatrix(matrix)
	setCoverage(in, out)
	// The measured direct-damage regime, published because a consumer of the
	// rl touch count needs to know whether it could be measured at all: the
	// magnitude prior that makes that count trustworthy only exists where the
	// constant is fixed (direct.go rocketTouched, detectRocketRegime).
	if in.rlLo == in.rlHi {
		out.RocketDirectDamage = int(in.rlLo)
	}
	return out
}

// setCoverage stamps result.DamageCoverage: the share of the frag log's
// weapon kills whose lethal instant the reconstructed Events log accounts
// for. It is the one self-check the reconstruction can run on any demo —
// every named kill is a place where damage provably happened, so a demo
// whose stat channel was barely broadcast reads far below 1 while its
// positions and frag log stay intact.
//
// This is the in-pipeline form of cmd/qw-recon-oracle's kill-delta
// coverage (killsDelta/killsScored), and deliberately scores the same
// denominator: enemy kills by a weapon, both players on the roster.
// Positional kills are excluded because they carry no damage arithmetic and
// aggregate outside Events. The obituary withhold does not reach here —
// coverage is a property of delta EXTRACTION, which keeps its frag anchors
// either way — so a withheld run reports the same figure as production.
func setCoverage(in *inputs, out *result.DamageResult) {
	hit := make(map[fragKey]bool, len(out.Events))
	for i := range out.Events {
		hit[fragKey{out.Events[i].Victim, out.Events[i].Time}] = true
	}
	var kills, covered int
	for i := range in.frags {
		f := &in.frags[i]
		if f.IsSuicide || f.IsTeamKill || isPositionalWeapon(f.Weapon) {
			continue
		}
		if f.Killer == "" || f.Killer == "world" || f.Killer == f.Victim {
			continue
		}
		if in.players[f.Killer] == nil || in.players[f.Victim] == nil {
			continue
		}
		kills++
		if hit[fragKey{f.Victim, f.Time}] {
			covered++
		}
	}
	if kills == 0 {
		// No denominator — an empty frag log or one naming only suicides,
		// team kills and telefrags. Report nothing rather than a bogus 0:
		// coverage would be claiming an assessment it never made.
		return
	}
	out.Coverage = &result.DamageCoverage{
		Kills:   kills,
		Covered: covered,
		Ratio:   float64(covered) / float64(kills),
	}
}

// aggregatePositional folds one telefrag/stomp into the aggregates,
// mirroring analyzer/damage.go's fold: the damage lands in Given/GivenTeam/
// Taken (and the enemy EWep buckets) in both families, but stays out of the
// Events log, the per-weapon maps, Matrix and TotalDamage. A telefrag's
// honest value is the bounded reconstruction in BOTH families (armor +
// remaining health — the delta observes exactly that); a stomp's raw is
// the observed uncapped drop.
func aggregatePositional(in *inputs, out *result.DamageResult, e reconEvent) {
	isTele := e.weapon == "tele"
	b := e.bounded
	raw := e.raw
	if isTele {
		raw = b // wire 9999 is a sentinel in GT; the delta IS the honest value
	}
	kill := result.PositionalKill{
		Time: e.t, Attacker: e.attacker, Victim: e.victim, IsTeam: e.isTeam,
	}
	bv := b
	kill.Bounded = &bv
	if raw != b {
		kill.Damage = raw
	}

	vp := getOrCreate(out.ByPlayer, e.victim)
	vp.Taken += raw
	vp.BoundedNest().Taken += b
	if !e.isEnv {
		ap := getOrCreate(out.ByPlayer, e.attacker)
		switch {
		case e.isSelf:
			ap.GivenSelf += raw
			ap.BoundedNest().GivenSelf += b
		case e.isTeam:
			ap.GivenTeam += raw
			ap.BoundedNest().GivenTeam += b
		default:
			ap.Given += raw
			ap.BoundedNest().Given += b
			vw := ""
			if vps := in.players[e.victim]; vps != nil && in.weaponBitsLive {
				vw = victimWeaponClass(vps, e.t)
			}
			if vw != "" {
				addVictimWeaponBucket(ap, vw, raw)
				addVictimWeaponBucket(ap.BoundedNest(), vw, b)
			}
			kill.VictimWep = vw
		}
	}

	credit := !e.isEnv && !e.isSelf && !e.isTeam
	if isTele {
		out.Telefrags = append(out.Telefrags, kill)
		if credit {
			getOrCreate(out.ByPlayer, e.attacker).Telefrags++
		}
	} else {
		out.Stomps = append(out.Stomps, kill)
		if credit {
			getOrCreate(out.ByPlayer, e.attacker).Stomps++
		}
	}
}

func getOrCreate(m map[string]*result.PlayerDamage, name string) *result.PlayerDamage {
	if p, ok := m[name]; ok {
		return p
	}
	p := &result.PlayerDamage{ByWeapon: make(map[string]int)}
	m[name] = p
	return p
}

// addVictimWeaponBucket mirrors analyzer/damage.go's EWep bucket fold.
func addVictimWeaponBucket(p *result.PlayerDamage, class string, dmg int) {
	switch class {
	case "both":
		p.EnemyVsBoth += dmg
		p.EWep += dmg
	case "rl":
		p.EnemyVsRL += dmg
		p.EWep += dmg
	case "lg":
		p.EnemyVsLG += dmg
		p.EWep += dmg
	case "mid":
		p.EnemyVsMid += dmg
	default:
		p.EnemyVsSG += dmg
	}
}

func addToMatrix(m map[[2]string]*result.DamagePair, attacker, victim, weapon string, dmg int) {
	key := [2]string{attacker, victim}
	p, ok := m[key]
	if !ok {
		p = &result.DamagePair{Attacker: attacker, Victim: victim, ByWeapon: make(map[string]int)}
		m[key] = p
	}
	p.Damage += dmg
	p.ByWeapon[weapon] += dmg
}

func flattenMatrix(m map[[2]string]*result.DamagePair) []result.DamagePair {
	out := make([]result.DamagePair, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Attacker != out[j].Attacker {
			return out[i].Attacker < out[j].Attacker
		}
		return out[i].Victim < out[j].Victim
	})
	return out
}
