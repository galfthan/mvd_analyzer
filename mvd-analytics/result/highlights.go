package result

// HighlightsResult is the Key-Moments highlight catalogue: every discharge,
// quadbore, telefrag and airgib the match produced, one list per kind, each
// row carrying WHAT EVERYONE INVOLVED HAD at that instant (relation to the
// actor, health + armor, weapons, powerups) so a consumer can rank them any
// way it likes — "biggest discharge", "most stacked telefrag victim",
// "most quad thrown away" — without a second pass over the streams. Built
// once by the `highlights` post-processor from the final frag log, the
// per-player streams and (when present) the damage log; view.ComputeHighlights
// is the pure function behind it. Schema v76.
//
// Absent (nil) when the demo has no match streams or no frag log — nothing
// to snapshot from. Present with empty lists when the match simply had
// none: that is a measured zero, not an unavailable section.
type HighlightsResult struct {
	// Discharges is every LG water discharge with evidence — an obituary
	// naming it (the discharger's own death or a "drains X's batteries" /
	// "accepts X's discharge" kill) or a damage-log radius hit from an LG
	// (the only splash an LG can deal). A discharge that hurt nobody and
	// left no print is not observable and is not here.
	Discharges []HighlightEvent `json:"discharges,omitempty"`
	// Quadbores is every self-kill by the player's own rocket or grenade
	// while holding quad.
	Quadbores []HighlightEvent `json:"quadbores,omitempty"`
	// Telefrags is every death booked under the "tele" weapon — the
	// ordinary telefrag, the recovered team telefrag, the pentagram
	// deflections (the teleporter died) and the spawn telefrag.
	Telefrags []HighlightEvent `json:"telefrags,omitempty"`
	// Airgibs is the airgib list — direct enemy rocket hits on airborne
	// victims (view.ComputeAirgibs) — with the victim's state added.
	// Height-sorted, the detector's order. Until v76 it lived at
	// timelineAnalysis.airgibs.
	Airgibs []HighlightEvent `json:"airgibs,omitempty"`
}

// HighlightEvent is one highlight. The four kinds share the shape so a
// consumer renders one row type: an Actor (who did it), the Victims it
// touched, kill counters, and the damage-log figure when a log exists.
// The kind-specific fields are omitted on the kinds they do not apply to.
type HighlightEvent struct {
	// Kind is "discharge" | "quadbore" | "telefrag" | "airgib".
	Kind string `json:"kind"`
	// Time is match-relative milliseconds: the obituary / damage instant.
	Time int32 `json:"time"`
	// Actor is who did it — the discharger, the player who bored, the
	// teleporter, the rocketeer — with Relation "self". Actor.Killed says
	// whether they died in the event (a discharge need not kill its
	// shooter; a quadbore and a deflect always do).
	Actor HighlightPlayer `json:"actor"`
	// Victims is everyone else the event touched: killed (Killed) or, with
	// a damage log, merely hurt (Damage > 0, Killed false); on a pentagram
	// deflect, the pent holder the actor died on (Survived). Empty when
	// the actor was the only casualty.
	Victims []HighlightPlayer `json:"victims,omitempty"`
	// EnemyKills / TeamKills count the killed Victims by Relation. The
	// actor's own death is Actor.Killed, not a kill.
	EnemyKills int `json:"enemyKills"`
	TeamKills  int `json:"teamKills"`
	// Damage is the damage-log total dealt to the Victims (raw family),
	// 0 when the demo has no damage log or the event dealt none.
	Damage int `json:"damage,omitempty"`
	// Sources names the evidence the row rests on: "frags" (an obituary
	// named it) and/or "damage" (the damage log carried it). A discharge
	// can come from either alone; the other kinds always carry "frags".
	Sources []string `json:"sources"`

	// Cells is the discharger's cell count just before firing — the
	// discharge deals 35 × cells radius damage (ktx/src/weapons.c:1208),
	// so this is the event's magnitude. Nil when the stream has no sample.
	Cells *int16 `json:"cells,omitempty"`
	// Weapon is the quadbore's weapon: "rl" | "gl".
	Weapon string `json:"weapon,omitempty"`
	// QuadHeldMs is how long the quadbore's actor had held the quad when
	// they died — subtract from the mode's quad duration (30 s in KTX)
	// for the time thrown away.
	QuadHeldMs int32 `json:"quadHeldMs,omitempty"`
	// QuadFrags is what the quad run yielded before the bore: the actor's
	// non-suicide kills between the pickup and the death.
	QuadFrags int `json:"quadFrags,omitempty"`
	// TeleKind refines a telefrag: "telefrag" (the actor killed the
	// victim), "deflect" (the actor teleported onto a pentagram holder
	// and died — KTX dtTELE2/dtTELE3) or "spawnicide" (dtTELE4).
	TeleKind string `json:"teleKind,omitempty"`
	// Height / HeightAboveAttacker are the airgib's victim height above
	// the floor and above the shooter (see AirgibEvent).
	Height              float32 `json:"height,omitempty"`
	HeightAboveAttacker float32 `json:"heightAboveAttacker,omitempty"`
}

// HighlightPlayer is one participant with their state at the event's
// instant, read from the per-player streams just BEFORE the event (the
// death frame already carries the corpse values — see the snapshot rule
// in RESULT_SCHEMA.md).
type HighlightPlayer struct {
	Name   string `json:"name"`
	Team   string `json:"team,omitempty"`
	UserID int    `json:"userId,omitempty"`
	// Relation is the player's side relative to the event's actor:
	// "self" (the actor), "team" (same team) or "enemy". In an individual
	// layout (duel, FFA) every other player is "enemy".
	Relation string `json:"relation"`
	// Killed marks a death in this event; Survived marks the pentagram
	// holder a deflected teleporter died on. Damage is the damage-log
	// figure for this player in the event (0 without a log).
	Killed   bool `json:"killed,omitempty"`
	Survived bool `json:"survived,omitempty"`
	Damage   int  `json:"damage,omitempty"`

	// Health / Armor / ArmorType / Stack (= health + armor) are the
	// player's vitals just before the event. Nil when the stream never
	// recorded a value (StateSource ""). ArmorType is "ga" | "ya" | "ra"
	// | "" (none).
	Health    *int16 `json:"health,omitempty"`
	Armor     *int16 `json:"armor,omitempty"`
	ArmorType string `json:"armorType,omitempty"`
	Stack     *int   `json:"stack,omitempty"`
	// Weapons is the tracked inventory the player held ("rl", "lg", "gl",
	// "ssg", "sng" — the STAT_ITEMS bits the streams track); ActiveWeapon
	// is the one wielded ("sg" .. "lg", "axe"), empty when the wire never
	// said. Powerups is the subset of "quad", "pent", "ring" held.
	Weapons      []string `json:"weapons,omitempty"`
	ActiveWeapon string   `json:"activeWeapon,omitempty"`
	Powerups     []string `json:"powerups,omitempty"`
	// Loc is the player's loc name at the instant ("" when unknown).
	Loc string `json:"loc,omitempty"`
	// StateSource says where the vitals came from: "stream" (read from
	// the player's own health/armor streams), "spawn" (the player had just
	// spawned and the spawn stats had not reached the wire — the streams
	// still read the previous life's corpse — so the vitals are the spawn
	// state 100/0), or "" (no stream to read; every vital is nil).
	StateSource string `json:"stateSource,omitempty"`
}
