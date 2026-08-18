package result

// BackpackDrop is one RL or LG backpack dropped on player death.
//
// Two provenances, named by Source and never mixed within one demo:
//
//   - BackpackSourceKTX — decoded from KTX's `//ktx drop <ent> <items>
//     <player_ent>` STUFFCMD_DEMOONLY directive (ktx/src/items.c:2762-2766).
//     Authoritative: KTX emits it exactly once per real drop with the weapon
//     and the dropper's slot baked in. Only KTX >= 1.38 emits it, which is
//     49.2% of the archive.
//   - BackpackSourceReconstructed — replayed from DropBackpack's own rules
//     (death instant, the victim's STAT_ACTIVEWEAPON, the death position) on
//     demos whose mod never emitted the hint. Carries no EntNum, because the
//     backpack's edict number is exactly what the wire never said.
//
// Pickup tracking is intentionally NOT included. The wire-derived
// pickup signal (svc_packetentities U_REMOVE on the backpack edict)
// produces phantom pickup/respawn cycles that we cannot reliably
// distinguish from real pickups. A future schema bump can add a
// PickedAt/PickedBy field once the wire-flutter reliability issue
// is diagnosed.
//
// Non-RL/LG drops (SSG/NG/SNG/GL/empty) are not surfaced — KTX only emits
// the hint for heavy weapons, the QW protocol does not transmit backpack
// contents as wire-level entity state, and the reconstruction deliberately
// tracks the hint's own RL/LG scope so the two provenances answer the same
// question.
type BackpackDrop struct {
	Time   int32      `json:"time"` // Match-relative milliseconds (schema v8)
	Player string     `json:"player"`
	Team   string     `json:"team,omitempty"`
	Weapon string     `json:"weapon"` // "rl" or "lg"
	Origin [3]float32 `json:"origin"`
	Loc    string     `json:"loc,omitempty"`
	// EntNum is the server edict number, stable within a match; it joins to
	// WeaponPickup.BackpackEnt. Zero on reconstructed rows — the edict
	// number lives only in the hint, so a reconstructed drop cannot be
	// joined to its pickup. Never treat 0 as an edict.
	EntNum int `json:"entNum"`
	// Source names where this row came from: the BackpackSource* vocabulary
	// below. Always set on rows this pipeline produces.
	Source string `json:"source,omitempty"`
}

// BackpackDrop.Source vocabulary.
const (
	// BackpackSourceKTX: decoded from the `//ktx drop` wire hint.
	BackpackSourceKTX = "ktx"
	// BackpackSourceReconstructed: replayed from DropBackpack's rules on a
	// demo that carried no hint. See analyzer/backpack_recon.go for the
	// stand-down conditions and BACKPACKS.md for the measured accuracy.
	BackpackSourceReconstructed = "reconstructed"
)
