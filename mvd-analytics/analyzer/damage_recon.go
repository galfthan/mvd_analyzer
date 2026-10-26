package analyzer

import (
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// damageSourceKTX re-exposes the result-package constant at package scope:
// DamageAnalyzer.Finalize shadows the `result` qualifier with its parameter
// (same reason analyzer/damage.go has addWeaponDamage).
const damageSourceKTX = result.DamageSourceKTX

// damageReconPost reconstructs the damage section for demos that predate
// the KTX mvdhidden_dmgdone instrumentation (~45% of the archive). When the
// wire carried no damage stream (res.Damage == nil after the damage
// analyzer ran), package damagerecon rebuilds the per-hit log from the
// health/armor change streams + spectator-visible evidence (beams,
// projectile flights, fire sounds, position tracks, the frag log) and the
// section is stamped Source = "reconstructed" so every consumer can tell
// measurement from inference.
//
// A wire-measured damage stream always wins: the post never touches a
// non-nil section. Reconstruction quietly stands down when its inputs are
// missing (no streams, spatial shot streams not built, or a skipped:* server
// mode whose T_Damage rewrites even the KTX-side bounded pass refuses) —
// the section then stays absent exactly as before this node existed.
func damageReconPost(res *Result, co *CoreOutputs) {
	if res.Damage != nil {
		return
	}
	dr, err := damagerecon.Compute(res)
	if err != nil {
		return
	}
	res.Damage = dr
}
