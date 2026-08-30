package damagerecon

import "github.com/mvd-analyzer/mvd-analytics/result"

// Direct-touch classification on the WIRE damage log.
//
// direct.go answers "did this rocket or grenade TOUCH the victim?" from the
// projectile's broadcast trajectory, the damage magnitude and the grenade
// fuse — evidence that is era-independent — and the reconstruction runs it
// because a pre-instrumentation demo has no other way to reach KTX's counter.
// A modern demo needs it for gl only: rl's touch reaches the wire as an
// unflagged damage row and gl's does not (result.WeaponAim states why).
//
// This file feeds the WIRE rows to that same classifier. Nothing about
// direct.go changes: a wire row is an (attacker, victim, weapon, time, exact
// damage) tuple, which is what a reconstructed delta's attribution produces,
// so the only work here is finding the projectile candidate the row belongs
// to — the same projCandidates / explosionCandidates search attribution runs,
// narrowed to the attacker and weapon the WIRE already names rather than
// scored against every other explanation.
//
// The wire row is the STRONGER input of the two eras. Its damage value is the
// server's own, not a health/armor delta reconstruction, so the magnitude
// prior (rocketTouched) reads an exact number; its attacker and weapon are
// measured rather than inferred, so no candidate can win the row for the
// wrong shooter. Measured verdicts are in ACCURACY.md §"The wire-linked
// accuracy family vs the verbatim block".

// WireDirectTouches classifies every gl row of a WIRE damage log as a direct
// TOUCH or not, returning one verdict per res.Damage.Events entry (the slices
// are index-parallel); rl rows stay false here — the wire's own splash flag
// is rl's verdict, and the classifier's rl reading belongs to
// WireDirectTouchesForEval. Non-projectile, environmental and self rows are
// always false: a missile never touches its own owner, both touch handlers
// returning immediately on `other == owner` (ktx/src/weapons.c:954, :1317).
//
// Returns nil — never an all-false slice — when the classification could not
// run at all, so a consumer can tell "no touches" from "not measured". Three
// things are required and each is a real absence:
//
//   - a KTX-sourced damage section (a reconstructed one classifies its own
//     rows at attribution time; see stampRocketVerdict);
//   - the spatial shot streams (Registry.BuildShotStreams), which carry the
//     projectile flights and TE_EXPLOSION points the geometry is read from.
//     mvd-api and the WASM build always request them
//     (democache.go, cmd/wasm/main.go); a bare qw-analyze parse does not, and
//     there the gl touch count is withheld rather than guessed;
//   - per-player position streams to place the victim's hull at the damage
//     instant.
//
// Only gl is classified: rl keeps the wire's own splash flag, which is exact.
// The rl verdict — what the classifier would have scored had it replaced the
// flag — is the measurement that decided that, and it is an EVAL question,
// not a production one: WireDirectTouchesForEval below answers it for
// cmd/qw-demoinfo-eval's `acc.rl.classifier/wire` column.
//
// COST: this runs over the whole damage log, and a windowed /aim recomputes
// it per request (view.Aim → aimcore.Compute), BSP load included, for a
// verdict that does not depend on the window. Caching it would mean carrying
// the slice on the Result, which is a schema change; the per-request cost is
// the geometry search over the gl rows of one demo.
func WireDirectTouches(res *result.Result) []bool {
	return wireDirectTouches(res, false)
}

// WireDirectTouchesForEval classifies rl rows as well as gl ones. Nothing in
// the pipeline reads the rl verdict — production takes rl's touch count off
// the wire's own splash flag — so this exists only so the choice stays
// measured. Same rule as ReconHitsForEval: what a derivation costs is what
// decides whether it ships, so the measurement cannot be gated on it having
// shipped.
func WireDirectTouchesForEval(res *result.Result) []bool {
	return wireDirectTouches(res, true)
}

func wireDirectTouches(res *result.Result, withRL bool) []bool {
	if res == nil || res.Streams == nil || len(res.Streams.Players) == 0 {
		return nil
	}
	if !res.Streams.ShotStreamsComputed {
		return nil
	}
	if res.Damage == nil || res.Damage.Source != result.DamageSourceKTX {
		return nil
	}
	in := buildInputs(res)
	in.bsp = loadBSPGate(res)
	if withRL {
		// Narrow the rocket direct-damage band only where the demo's own hits
		// ESTABLISHED the fixed constant, exactly as attribute() does: outside
		// that verdict the band stays buildInputs' vanilla 100..120, which is
		// what tells rocketTouched the magnitude prior says nothing and the
		// trajectory has the whole answer. Assigning the detector's (0, 0)
		// there would instead be a prior demanding a damage of zero, refusing
		// every survived touch.
		lo, hi, regime := detectWireRocketRegime(in, res.Damage.Events)
		in.rlRegime = regime
		if regime == result.RocketRegimeFixed {
			in.rlLo, in.rlHi = lo, hi
		}
	}

	out := make([]bool, len(res.Damage.Events))
	for i := range res.Damage.Events {
		ev := &res.Damage.Events[i]
		if ev.Weapon != "gl" && !(withRL && ev.Weapon == "rl") {
			continue
		}
		if ev.Attacker == "" || ev.IsEnv || ev.IsSelf {
			continue
		}
		tr := in.tracks[ev.Victim]
		if tr == nil {
			continue
		}
		out[i] = in.wireTouched(ev, tr.posAt(ev.Time))
	}
	return out
}

// wireTouched runs the direct-touch classifier over one wire damage row.
//
// The candidate search is the reconstruction's own (wireProjCandidate); what
// it returns already carries direct.go's geometric verdict, because
// projCandidates and explosionCandidates call directImpact/grenadeFuseExpired
// when they build a candidate. A row with no candidate at all is NOT a touch:
// the projectile that caused it was never broadcast and never detonated
// within reach of the victim as far as this demo records, so there is no
// geometry saying it passed through the hull.
//
// rl additionally folds the MAGNITUDE prior (rocketTouched) exactly as
// stampRocketVerdict does on the reconstructed side, and reads the wire's own
// damage value rather than a delta reconstruction of it. `died` — which is
// what lets the prior fall back to the trajectory where the −99 corpse clamp
// hides overkill — is the row carrying a BOUNDED value different from its raw
// one, i.e. the analyzer's own reconstruction of an overkill or nullified
// hit (result.DamageEntry.Bounded). On a skipped-mode demo no row carries one
// (DamageResult.BoundedMode), so a killing rocket there is judged on its raw
// value alone; those modes rewrite T_Damage and are outside every population
// this family is measured on.
func (in *inputs) wireTouched(ev *result.DamageEntry, vpos vec3) bool {
	c := in.wireProjCandidate(ev, vpos)
	if c == nil {
		return false
	}
	if ev.Weapon != "rl" {
		return !c.isSplash
	}
	d := delta{t: ev.Time, raw: ev.Damage, bounded: ev.Damage, died: ev.Bounded != nil}
	if ev.Bounded != nil {
		d.bounded = *ev.Bounded
	}
	return in.rocketTouched(!c.isSplash, c.hullNear, d, in.quadFactor(ev.Attacker, ev.Time))
}

// wireProjCandidate picks the projectile explanation of one wire row: the
// best-scoring candidate of the attacker and weapon the WIRE names, over both
// projectile families the reconstruction knows — tracked flights
// (projCandidates) and the trackless point-blank detonations recovered from
// TE_EXPLOSION (explosionCandidates).
//
// Filtering by the wire's attacker and weapon is what makes this a lookup
// rather than an attribution: the reconstruction has to decide WHO dealt a
// health drop and with what, and every wrong answer there is a wrong
// direct/splash verdict too. Here both are measured, so the only remaining
// question is which of that shooter's projectiles the row belongs to — and
// `geom` (explosion-to-victim distance, plus the flight-consistency terms on
// the trackless path) is the reconstruction's own answer to it. Nil when the
// row has no projectile evidence at all.
func (in *inputs) wireProjCandidate(ev *result.DamageEntry, vpos vec3) *candidate {
	cands := in.projCandidates(ev.Victim, ev.Time, vpos)
	cands = append(cands, in.explosionCandidates(ev.Victim, ev.Time, vpos)...)
	var best *candidate
	for i := range cands {
		c := &cands[i]
		if c.attacker != ev.Attacker || c.weapon != ev.Weapon {
			continue
		}
		if best == nil || c.geom < best.geom {
			best = c
		}
	}
	return best
}

// detectWireRocketRegime is detectRocketRegime's clustering test run over the
// WIRE rows: which of the three verdicts this demo's own near-direct rocket
// hits reach about the server's direct-damage constant (110 since ktx commit
// c7263e8f, 2008-09-29; `100 + g_random()*20` before it).
//
// It deliberately does NOT read the wire's splash flag, even though the flag
// is right there and would name the direct rows exactly. The flag is the
// ground truth the rl classifier is MEASURED against, and a prior calibrated
// on it would be measuring itself. The predicate is therefore the
// reconstruction's own — enemy rl damage on a surviving, unquadded victim in
// the 95..125 band — with the wire's exact value where the reconstruction
// reads a delta.
func detectWireRocketRegime(in *inputs, evs []result.DamageEntry) (lo, hi float64, regime string) {
	n, at110, above := 0, 0, 0
	for i := range evs {
		e := &evs[i]
		if e.Weapon != "rl" || e.IsSelf || e.IsTeam || e.IsEnv || e.Bounded != nil {
			continue
		}
		if in.hasQuad(e.Attacker, e.Time) {
			continue
		}
		if e.Damage < 95 || e.Damage > 125 {
			continue
		}
		n++
		switch {
		case e.Damage == 110:
			at110++
		case e.Damage > 110:
			above++
		}
	}
	return regimeVerdict(n, at110, above)
}
