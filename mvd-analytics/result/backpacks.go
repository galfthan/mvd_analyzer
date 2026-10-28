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
//     `//ktx bp`. Since v77 the join is also stamped on the row itself
//     (analyzer/backpack_pickups.go): Fate picked with Picker / PickerTeam /
//     PickupTime and the pickup's own PickerHadWeapon / PickerKills /
//     PickerNextDeathTime, copied from that weaponPickups row, so a consumer
//     reads one list. KTX's third directive `//ktx expire <ent>`
//     (ktx/src/g_spawn.c:196-210) announces a pack removed UNTAKEN — Fate
//     expired. A row with neither is Fate unobserved: the absence of a
//     `//ktx bp` is not evidence that nobody took it, since a demo can carry
//     the drop hint and no pickup hints at all.
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
	// BackpackFate* vocabulary below (schema v72). On a `reconstructed` row
	// it is the linkage's reading of the pack-entity track; empty there
	// means the pack's fate was never asked about (no player streams),
	// which is a different statement from BackpackFateUnobserved ("asked,
	// and the wire did not answer"). On a `ktx` row (v77) it is picked when
	// a `//ktx bp` pickup joined, expired from the `//ktx expire` hint, and
	// unobserved otherwise. Source carries the provenance of Fate as well
	// as of the row.
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
	// PickerRelation is the picker's side relative to the dropper (v77):
	// "self" when the dropper respawned and took their own pack back, "team"
	// when both carry the same team, else "enemy" — in an individual layout
	// (duel, FFA) every other picker is an enemy. Set whenever Picker is.
	PickerRelation string `json:"pickerRelation,omitempty"`
	// PickerHadWeapon / PickerKills / PickerNextDeathTime are the matched
	// weaponPickups row's HadBefore / Kills / NextDeathTime (v77): whether
	// the picker already held the weapon, the kills credited to this pickup
	// (zero when they already held it — see WeaponPickup), and the picker's
	// next death in match ms (0 if they never died again). `ktx` rows only —
	// the entity track a reconstructed row's pickup comes from carries none
	// of the three, so they are absent there rather than guessed.
	PickerHadWeapon     *bool `json:"pickerHadWeapon,omitempty"`
	PickerKills         *int  `json:"pickerKills,omitempty"`
	PickerNextDeathTime int32 `json:"pickerNextDeathTime,omitempty"`
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

// BackpackDrop.Fate vocabulary. Reconstructed rows can carry any of the
// three; a `ktx` row carries only `expired` (from `//ktx expire`) — its
// other outcomes live in the weapon-pickups join, so an absent Fate on a
// `ktx` row means "unknown here", not "nobody took it".
const (
	// BackpackFatePicked: the pack entity left the wire with at least one
	// live player's bounding box overlapping it — the touch that ran
	// BackpackTouch (ktx/src/items.c:2367). Picker names that player when
	// exactly one was on it, or when the weapon-bit gain separated several.
	BackpackFatePicked = "picked"
	// BackpackFateExpired: SUB_Remove took the pack at KTX's 120 s removal
	// timeout (items.c:2871-2872), untaken. On a `ktx` row KTX said so in
	// `//ktx expire <ent>`; on a `reconstructed` row the pack entity left
	// the wire at that age with nobody on it.
	BackpackFateExpired = "expired"
	// BackpackFateUnobserved: the honest residual. Either no backpack-model
	// entity bound to the drop, or the entity was still on the wire when the
	// recording ended, or it left the wire early with nobody on it — none of
	// which is evidence that nobody took the pack.
	BackpackFateUnobserved = "unobserved"
)
