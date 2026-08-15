// Package damagerecon reconstructs the per-hit damage log for demos that
// predate the KTX mvdhidden_dmgdone instrumentation (~45% of the archive:
// res.Damage == nil because the wire never carried a damage stream).
//
// The reconstruction reads only spectator-visible evidence that the
// analyzer pipeline already extracts into the Result:
//
//   - the per-player health/armor CHANGE streams (result.PlayerStream.Health/
//     Armor) — change-driven at server frame rate, so every hit lands as a
//     same-instant h/a drop and the observed delta IS the KTX bounded value
//     (armor absorbed + health share capped at remaining health);
//   - LG beam segments and rocket/grenade entity flights
//     (result.Streams.Beams / .Projectiles);
//   - weapon fire sounds (result.ShotsResult — only the damage-free
//     Time/Player/Weapon columns; Hit/Victims are damage-derived and are
//     deliberately never read);
//   - position/view/velocity tracks, spawn/death instants, held-weapon and
//     quad intervals (result.PlayerStream);
//   - the frag log (result.FragResult), anchoring every kill.
//
// Magnitudes come from the deltas (deltas.go); attribution scores candidate
// explanations by geometry + QW damage-model consistency (attribution.go);
// aggregation reproduces the exact DamageResult shape the KTX-derived
// analyzer emits (aggregate.go), stamped Source = "reconstructed".
//
// Feasibility, validation method, and the residual error classes are
// documented in the 2026-08-11 study this package ports
// (.reports/qw-damage-recon-2026-08-11/REPORT.md); the Go port's own
// accuracy numbers live in ACCURACY.md next to this file.
package damagerecon

import (
	"errors"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Errors reported by Compute. All mean "no reconstruction was produced";
// none is a pipeline failure.
var (
	// ErrNoStreams: the Result carries no per-player streams (aborted or
	// pre-match-only demo) — there is nothing to reconstruct from.
	ErrNoStreams = errors.New("damagerecon: no player streams")
	// ErrNoSpatialStreams: the beam/projectile spatial streams were never
	// built (Registry.BuildShotStreams off). Reconstruction without them
	// would silently degrade attribution, so it refuses instead.
	ErrNoSpatialStreams = errors.New("damagerecon: spatial shot streams not built (enable BuildShotStreams / -include projectiles,beams)")
	// ErrSkippedMode: the server mode rewrites T_Damage in ways the
	// damage model cannot follow (midair / instagib / dmgfrags) — the same
	// modes the KTX-side bounded reconstruction refuses.
	ErrSkippedMode = errors.New("damagerecon: server mode not reconstructable")
)

// Compute reconstructs a DamageResult from the assembled Result's state
// streams. It never reads res.Damage (the caller decides whether a wire
// damage stream takes precedence) nor any damage-derived field.
func Compute(res *result.Result) (*result.DamageResult, error) {
	if res == nil || res.Streams == nil || len(res.Streams.Players) == 0 {
		return nil, ErrNoStreams
	}
	if !res.Streams.ShotStreamsComputed {
		return nil, ErrNoSpatialStreams
	}
	if mode := skipModeReason(res); mode != "" {
		return nil, ErrSkippedMode
	}

	in := buildInputs(res)
	in.bsp = loadBSPGate(res)
	events := attribute(in)
	return aggregate(in, events), nil
}

// skipModeReason mirrors DamageAnalyzer.boundedSkipReason on the assembled
// Result: a serverinfo cvar naming a mode whose T_Damage rewrites are not
// observable per hit. On such demos the study's ground truth itself refuses
// a bounded figure, so no reconstruction is attempted at all.
func skipModeReason(res *result.Result) string {
	if res.Metadata == nil || res.Metadata.ServerInfo == nil {
		return ""
	}
	si := res.Metadata.ServerInfo
	for _, m := range [...]struct{ cvar, mode string }{
		{"k_midair", "midair"},
		{"k_instagib", "instagib"},
		{"k_dmgfrags", "dmgfrags"},
	} {
		if v := si[m.cvar]; v != "" && v != "0" {
			return m.mode
		}
	}
	return ""
}
