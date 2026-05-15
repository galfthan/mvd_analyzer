package result

// BackpackDrop is one RL or LG backpack dropped on player death,
// captured from KTX's `//ktx drop` STUFFCMD_DEMOONLY directive
// (ktx/src/items.c:2740). The hint is the authoritative source —
// it fires exactly once per real drop, with the weapon and the
// dropper's slot baked in.
//
// Pickup tracking is intentionally NOT included. The wire-derived
// pickup signal (svc_packetentities U_REMOVE on the backpack edict)
// produces phantom pickup/respawn cycles that we cannot reliably
// distinguish from real pickups. A future schema bump can add a
// PickedAt/PickedBy field once the wire-flutter reliability issue
// is diagnosed.
//
// Non-RL/LG drops (SSG/NG/SNG/GL/empty) are not surfaced — KTX
// only emits the hint for heavy weapons, and the QW protocol does
// not transmit backpack contents as wire-level entity state.
type BackpackDrop struct {
	Time   float64    `json:"time"`
	Player string     `json:"player"`
	Team   string     `json:"team,omitempty"`
	Weapon string     `json:"weapon"` // "rl" or "lg"
	Origin [3]float32 `json:"origin"`
	Loc    string     `json:"loc,omitempty"`
	EntNum int        `json:"entNum"` // server edict number; stable within a match
}
