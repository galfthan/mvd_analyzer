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
//     demos whose mod never emitted the hint.
//
// The PICKUP side is carried differently by the two provenances, because the
// evidence is different:
//
//   - A `ktx` row's pickup is a row of weaponPickups with Source
//     "backpack", joined on (EntNum, Time) — KTX names the picker outright in
//     `//ktx bp`. Fate / Picker / PickerTeam / PickupTime are empty there:
//     nothing would be gained by restating the join, and a second answer
//     could disagree with it.
//   - A `reconstructed` row's pickup is read off the pack ENTITY, and lands
//     in Fate / Picker / PickerTeam / PickupTime on this row. It is
//     deliberately NOT written into weaponPickups: that section documents
//     itself as authoritative KTX hints, and its rows feed kill credit and
//     pack-transfer stats that stay wire-measured. See
//     analyzer/backpack_linkage.go and analyzer/BACKPACKS.md.
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
	// EntNum is the server edict number, stable within a match. On a `ktx`
	// row it comes from the hint and joins to WeaponPickup.BackpackEnt. On a
	// `reconstructed` row it is the backpack-model entity the linkage bound
	// to the drop (schema v72), and joins to nothing — the pickup side of a
	// reconstructed row is Fate/Picker below. Zero means no edict was
	// identified; never treat 0 as an edict.
	EntNum int `json:"entNum"`
	// Source names where this row came from: the BackpackSource* vocabulary
	// below. Always set on rows this pipeline produces.
	Source string `json:"source,omitempty"`
	// Fate is what the wire showed happening to the pack, from the
	// BackpackFate* vocabulary below. Set only on `reconstructed` rows
	// (schema v72), and only when the reconstruction ran — empty means the
	// pack's fate was never asked about, which is a different statement from
	// BackpackFateUnobserved ("asked, and the wire did not answer").
	Fate string `json:"fate,omitempty"`
	// Picker names who took the pack. Set only when Fate is
	// BackpackFatePicked AND the evidence named exactly one player; a pickup
	// with two players on the pack and nothing separating them stays
	// BackpackFatePicked with no Picker rather than guessing. PickerTeam is
	// that picker's team and is omitted when they have none, so an FFA or
	// duel pickup carries a Picker and no PickerTeam — absence means
	// "teamless", not "no picker".
	Picker     string `json:"picker,omitempty"`
	PickerTeam string `json:"pickerTeam,omitempty"`
	// PickupTime is the match-relative millisecond the pack left the wire.
	// Set with Fate == BackpackFatePicked (with or without a Picker).
	PickupTime int32 `json:"pickupTime,omitempty"`
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

// BackpackDrop.Fate vocabulary (reconstructed rows only).
const (
	// BackpackFatePicked: the pack entity left the wire with at least one
	// live player's bounding box overlapping it — the touch that ran
	// BackpackTouch (ktx/src/items.c:2367). Picker names that player when
	// exactly one was on it, or when the weapon-bit gain separated several.
	BackpackFatePicked = "picked"
	// BackpackFateExpired: the pack entity left the wire at KTX's 120 s
	// removal timeout (items.c:2871-2872) with nobody on it — SUB_Remove,
	// not a pickup.
	BackpackFateExpired = "expired"
	// BackpackFateUnobserved: the honest residual. Either no backpack-model
	// entity bound to the drop, or the entity was still on the wire when the
	// recording ended, or it left the wire early with nobody on it — none of
	// which is evidence that nobody took the pack.
	BackpackFateUnobserved = "unobserved"
)
