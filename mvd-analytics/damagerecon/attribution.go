package damagerecon

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Attribution tolerances and radii, all calibrated against modern
// ground-truth demos in the 2026-08-11 study (values are the study's
// measured percentiles with slack; see REPORT.md).
const (
	tolBeamMs         = 30     // beam flash to damage frame (measured: 0)
	tolProjMs         = 130    // projectile end to damage frame (p5=-81, p99=261 → asymmetric window below)
	tolShotMs         = 60     // hitscan sound to damage frame
	rBeamSeg          = 90.0   // units victim to beam segment (p99 measured 60, max 79)
	rBeamSrc          = 160.0  // units beam start to attacker eye
	rSplash           = 380.0  // units projectile end to victim (p95=199)
	rDirect           = 48.0   // below this, treat as a possible direct hit
	nailSpeed         = 1000.0 // nails AND rockets fly 1000 ups
	rocketSpeed       = 1000.0
	tolFlightMs       = 180.0 // flight-time consistency for the trackless-rocket fallback
	rAxe              = 110.0
	rHitscan          = 3000.0
	sgAimGateDeg      = 50.0 // real sg hits are within ~25° (p95); hard gate at 2×
	rlSoundAimGateDeg = 60.0
)

// candidate is one possible explanation for a delta.
type candidate struct {
	geom     float64 // geometry-quality penalty (lower is better)
	attacker string
	weapon   string
	kind     string  // "beam" | "proj" | "hitscan" | "nail" | "rl-sound"
	dEnd     float64 // explosion-to-victim distance; <0 = unknown
	ep       vec3    // explosion point (kind == "proj" only)
	hasEP    bool
	isSplash bool
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
	// kind records the evidence class that won the attribution:
	// frag-anchor | positional | beam | proj | hitscan | nail | rl-sound |
	// masked-kill | none.
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
	events := attributePass(in)
	if lo, hi, fixed := detectRocketRegime(in, events); fixed {
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
			events = append(events, in.attributeOne(victim, vtrack, d))
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
	deltaNear := func(t int32) bool {
		for dt := int32(-60); dt <= 60; dt++ {
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
		if dEnd > rSplash || deltaNear(pr.endT) {
			continue
		}
		if !in.bsp.splashReaches(pr.ep, vpos) {
			continue
		}
		dLo := 120.0
		if pr.weapon == "rl" {
			dLo = in.rlLo
		}
		q := 1.0
		if ap := in.players[pr.shooter]; ap != nil && inIntervals(ap.Quad, pr.endT) {
			q = 4.0
		}
		selfF := 1.0
		if pr.shooter == victim {
			selfF = 0.5
		}
		raw := int((dLo*q - 0.5*dEnd) * selfF)
		if raw <= 0 {
			continue
		}
		e := in.mkEventSplash(delta{t: pr.endT, raw: raw, bounded: 0},
			pr.shooter, victim, pr.weapon, "pent-synth", dEnd >= rDirect)
		e.dEnd = dEnd
		out = append(out, e)
	}
	// Trackless self rockets — the pent rocket jump: a point-blank rocket
	// explodes before its entity is ever broadcast, so the projectile pass
	// above cannot see it. The explosion is at the shooter's feet; the
	// nominal explosion distance below folds into ~0.5·(D − 0.5·30) ≈ 48
	// raw per jump (GT pent-jump observations run 32–54).
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
		q := 1.0
		if inIntervals(p.Quad, s.t) {
			q = 4.0
		}
		raw := int((in.rlLo*q - 0.5*pentJumpNominalDist) * 0.5)
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
func detectRocketRegime(in *inputs, events []reconEvent) (lo, hi float64, fixed bool) {
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
		if ap := in.players[e.attacker]; ap != nil && inIntervals(ap.Quad, e.t) {
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
	if n >= 6 && float64(at110) >= 0.4*float64(n) && float64(above) <= math.Max(1, 0.1*float64(n)) {
		return 110, 110, true
	}
	return 0, 0, false
}

func (in *inputs) attributeOne(victim string, vtrack *track, d delta) reconEvent {
	// Frag anchor: a non-suicide non-teamkill frag at the exact instant
	// names killer + weapon authoritatively.
	if f := in.fragAt[fragKey{victim, d.t}]; f != nil && f.Killer != "world" {
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
		// Masked death with no killer-naming frag. A "was telefragged"
		// obituary parses as a killer-less suicide, but the attacker is
		// still inferable: the player whose track TELEPORTS onto the
		// victim's spot at that instant.
		if f := in.fragAnyAt[fragKey{victim, d.t}]; f != nil && f.Weapon == "tele" {
			if att := in.teleportArrivalAt(victim, d.t); att != "" {
				return in.mkEvent(d, att, victim, "tele", "positional")
			}
			return in.mkEvent(d, "world", victim, "tele", "positional")
		}
		return in.mkEvent(d, "world", victim, "unknown", "masked-kill")
	}

	if d.died {
		// A visible killing delta whose obituary is a killer-less telefrag
		// ("was telefragged" parses as suicide): type it positional and
		// infer the attacker from the teleport arrival.
		if f := in.fragAnyAt[fragKey{victim, d.t}]; f != nil && f.Weapon == "tele" {
			if att := in.teleportArrivalAt(victim, d.t); att != "" {
				return in.mkEvent(d, att, victim, "tele", "positional")
			}
			return in.mkEvent(d, "world", victim, "tele", "positional")
		}
	}

	var cands []candidate
	if vtrack != nil {
		vpos := vtrack.posAt(d.t)
		cands = append(cands, in.beamCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.projCandidates(d.t, vpos)...)
		cands = append(cands, in.hitscanCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.nailCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.rlSoundCandidates(victim, d.t, vpos)...)
		cands = append(cands, in.dischargeCandidates(victim, d.t, vpos)...)
	}

	best, ok := in.scoreCandidates(victim, vtrack, d, cands)
	if !ok {
		// KTX teamkill obituaries are weapon-less jokes ("checks his
		// glasses"), so a killing delta with a named teamkiller and no
		// evidence candidate still has a truthful attacker.
		if d.died {
			if f := in.fragAnyAt[fragKey{victim, d.t}]; f != nil && f.IsTeamKill && f.Killer != "" && f.Killer != victim {
				return in.mkEvent(d, f.Killer, victim, "unknown", "teamkill-anchor")
			}
		}
		return in.mkEvent(d, "world", victim, "unknown", "none")
	}
	e := in.mkEventSplash(d, best.attacker, victim, best.weapon, best.kind, best.isSplash)
	e.dEnd = best.dEnd
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
		// 440-ish raw vs ~200 observable). Only ever raises raw.
		dLo := 120.0
		if best.weapon == "rl" {
			dLo = in.rlLo
		}
		q := 1.0
		if ap := in.players[best.attacker]; ap != nil && inIntervals(ap.Quad, d.t) {
			q = 4.0
		}
		selfF := 1.0
		if e.isSelf {
			selfF = 0.5
		}
		modelMin := (dLo*q - 0.5*(best.dEnd+60.0)) * selfF
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
func (in *inputs) topUpKillRaw(e *reconEvent, vtrack *track) {
	if !e.died || vtrack == nil {
		return
	}
	q := 1.0
	if ap := in.players[e.attacker]; ap != nil && inIntervals(ap.Quad, e.t) {
		q = 4.0
	}
	model := 0.0
	switch e.weapon {
	case "rl", "gl":
		dLo := 120.0
		if e.weapon == "rl" {
			dLo = in.rlLo
		}
		vpos := vtrack.posAt(e.t)
		bestD := -1.0
		lo := sort.Search(len(in.projs), func(i int) bool { return in.projs[i].endT >= e.t-tolProjMs })
		for i := lo; i < len(in.projs) && in.projs[i].endT <= e.t+tolProjMs; i++ {
			pr := in.projs[i]
			if pr.weapon != e.weapon {
				continue
			}
			if d := pr.ep.distTo(vpos); d <= rSplash && (bestD < 0 || d < bestD) {
				bestD = d
			}
		}
		if bestD < 0 {
			return // trackless kill: no geometry, keep the observation
		}
		model = dLo*q - 0.5*(bestD+60.0)
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
			base := 35.0 * float64(dc.cells) * q
			expected := base - 0.5*d
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
		// Each same-frame TE_LIGHTNING2 is one cell at 30q.
		cells := 0
		lo := sort.Search(len(in.beams), func(i int) bool { return in.beams[i].t >= e.t-tolBeamMs })
		for i := lo; i < len(in.beams) && in.beams[i].t <= e.t+tolBeamMs; i++ {
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
		quad := false
		if ap := in.players[c.attacker]; ap != nil {
			quad = inIntervals(ap.Quad, d.t)
		}
		pen, feasible := in.damageModelScore(d.bounded, d.died, c.weapon, c.kind, c.dEnd, c.attacker == victim, quad)
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
// QW radius damage: received = D − 0.5·dist(center, explosion); the
// attacker's own splash = D − 0.25·dist. Rocket D = 100..120, grenade 120.
// LG = 30/cell, sg pellet 4×6, ssg ×14, ng spike 9, sng 18, axe 20.
// Quad multiplies ×4.
func (in *inputs) damageModelScore(obs int, died bool, weapon, kind string, dEnd float64, isSelf, quad bool) (float64, bool) {
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
		dLo, dHi := 120.0, 120.0
		if weapon == "rl" {
			dLo, dHi = in.rlLo, in.rlHi
		}
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
		// Engine order: quad multiplies the BASE damage, THEN the radius
		// falloff subtracts 0.5*dist (T_RadiusDamage) — so a quad splash is
		// 4D - 0.5d, not 4(D - 0.5d). Self splash halves the post-falloff
		// points.
		if kind == "rl-sound" || dEnd < 0 || dEnd < rDirect {
			// Direct hit (or unknown explosion point): full damage possible
			// for an enemy; splash-only ceiling for self.
			lo, hi = dLo*q*selfF, dHi*q*selfF
			if kind == "rl-sound" {
				// Unknown geometry: splash values allowed, but a tiny delta
				// is far likelier another cause.
				lo = 25.0 * q * selfF
			} else if isSelf && dEnd >= 0 {
				// Tracked explosion right at the shooter: pure close splash.
				lo = math.Max(1.0, (dLo*q-0.5*(dEnd+60.0))*selfF)
			}
		} else {
			lo = (dLo*q - 0.5*(dEnd+60.0)) * selfF // slack for interpolation error
			hi = (dHi*q - 0.5*math.Max(0, dEnd-60.0)) * selfF
			if hi <= 0 {
				return 0, false
			}
			lo = math.Max(1.0, lo)
		}
	case "hitscan":
		switch weapon {
		case "sg":
			lo, hi = 4.0*q, 24.0*q
		case "ssg":
			lo, hi = 4.0*q, 56.0*q
		default: // axe
			lo, hi = 20.0*q, 20.0*q
		}
	case "discharge":
		// dEnd carries the expected value (35*cells radius model, computed
		// per candidate); quad/self already folded in there.
		lo, hi = dEnd*0.75-10, dEnd*1.1+10
	case "nail":
		per := 9.0
		if weapon == "sng" {
			per = 18.0
		}
		lo, hi = per*q, per*q*3 // up to a few spikes per frame
	default:
		return 0, true
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
	return pen / math.Max(10.0, 0.25*hi), true
}

// beamCandidates: LG beams whose segment passes near the victim at the
// same frame; the attacker is the nearest player to the beam start.
func (in *inputs) beamCandidates(victim string, t int32, vpos vec3) []candidate {
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
				geom: sd / rBeamSeg * 0.3, attacker: best, weapon: "lg",
				kind: "beam", dEnd: -1,
			})
		}
	}
	return out
}

// projCandidates: tracked rocket/grenade flights ending near the victim at
// the same frame.
func (in *inputs) projCandidates(t int32, vpos vec3) []candidate {
	var out []candidate
	lo := sort.Search(len(in.projs), func(i int) bool { return in.projs[i].endT >= t-tolProjMs })
	for i := lo; i < len(in.projs) && in.projs[i].endT <= t+tolProjMs; i++ {
		pr := in.projs[i]
		if pr.shooter == "" {
			continue
		}
		dEnd := pr.ep.distTo(vpos)
		if dEnd > rSplash {
			continue
		}
		// CanDamage gate: splash reaches only what the explosion traces to.
		if dEnd >= rDirect && !in.bsp.splashReaches(pr.ep, vpos) {
			continue
		}
		out = append(out, candidate{
			geom: dEnd / rSplash * 0.5, attacker: pr.shooter, weapon: pr.weapon,
			kind: "proj", dEnd: dEnd, ep: pr.ep, hasEP: true,
			isSplash: dEnd >= rDirect,
		})
	}
	return out
}

// hitscanCandidates: sg/ssg/axe fire sounds at the same frame, gated by
// range (axe) or aim cone (shotguns).
func (in *inputs) hitscanCandidates(victim string, t int32, vpos vec3) []candidate {
	var out []candidate
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
		case "axe":
			dd := tr.posAt(s.t).distTo(vpos)
			if dd > rAxe {
				continue
			}
			out = append(out, candidate{
				geom: 0.1 + dd/rAxe*0.2, attacker: s.player, weapon: s.weapon,
				kind: "hitscan", dEnd: dd,
			})
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
				if ang > sgAimGateDeg {
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
			out = append(out, candidate{
				geom: 0.15 + apen + tpen, attacker: s.player, weapon: s.weapon,
				kind: "hitscan", dEnd: dd,
			})
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
		base := 35.0 * float64(dc.cells)
		if ap := in.players[dc.player]; ap != nil && inIntervals(ap.Quad, dc.t) {
			base *= 4
		}
		expected := base - 0.5*d
		if dc.player == victim {
			expected *= 0.5
		}
		if expected <= 0 {
			continue
		}
		if !in.bsp.reachesBody(eyeOf(spos), vpos) {
			continue
		}
		out = append(out, candidate{
			geom: 0.1, attacker: dc.player, weapon: "lg",
			kind: "discharge", dEnd: expected,
		})
	}
	return out
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
