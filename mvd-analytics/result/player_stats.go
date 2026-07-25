package result

import (
	"math"
	"strconv"
)

// PlayerStatsResult is the canonical per-player statistics section: one
// row per player (and per team), computed for EVERY demo.
//
// It exists because the two pre-existing sources of "player statistics"
// each answer only half the question. DemoInfoResult is KTX's own
// end-of-match accounting — authoritative for the server-side counters we
// cannot see on the wire (damage, accuracy, pickup tallies), but absent on
// any demo recorded before KTX started embedding it, and wrong in a few
// places we can do better (see below). The stream/frag/item artifacts are
// present on every demo but leave the join to the consumer, which every
// consumer then re-implemented differently.
//
// This section is that join, done once. Each stat FAMILY carries a Src
// naming where its numbers came from ("derived" | "ktx"), mirroring
// DamageResult.BoundedSource. The analyzer produces the fully derived
// form; view.PlayerStats overlays KTX where KTX is the better source (see
// mvd-analytics/RESULT_SCHEMA.md for the family-by-family rules).
//
// What it adds that neither source had: possession time. "Time with RL",
// "time with RA", "time with no armor" are integrals over the native-rate
// possession intervals in Streams (PlayerStream.RL/LG/…, ArmorType,
// Quad/Pent/Ring). KTX tracks weapon hold time internally but never writes
// it to the demoinfo block (ktx/src/stats_json.c json_weap_detail emits
// acc/kills/deaths/pickups/damage only — ps.wpn[].time reaches just the
// end-of-match text tables in ktx/src/statsTables.c:390), so no demo of any
// age carries it. Its armor hold time IS in the block but overcounts: the
// clock opens at pickup and closes only on death or on picking up a
// different armor type (ktx/src/items.c:505-522, ktx/src/client.c:4600),
// never when the armor is chewed to zero by damage. Ours is the exact
// integral, so it will legitimately read LOWER than a KTX end-of-match
// table — that is the correction, not a bug.
//
// Schema v60.
type PlayerStatsResult struct {
	// Players is one row per participant, in Streams.Players order (the
	// canonical player order used across the Result), with any scoreboard
	// player who produced no stream appended. Consumers that want a
	// scoreboard sort it themselves.
	Players []PlayerStatsRow `json:"players"`
	// Teams is the same row shape aggregated per team, omitted on duels
	// and FFA (where a "team" is a single player or meaningless). Team
	// rows carry Name = team name and no Team / Ping / Login / Bot.
	Teams []PlayerStatsRow `json:"teams,omitempty"`
	// Sources is the per-family provenance roll-up, so a caller can see in
	// one glance what this demo's numbers rest on without walking rows.
	Sources PlayerStatsSources `json:"sources"`
}

// Provenance values for the per-family Src fields.
const (
	// SrcDerived: computed by this pipeline from the wire streams and the
	// derived artifacts (frag log, items, weapon pickups, damage
	// reconstruction).
	SrcDerived = "derived"
	// SrcKTX: taken from the KTX demoinfo block, which counts it
	// server-side.
	SrcKTX = "ktx"
)

// PlayerStatsSources records, per family, which source the rows carry.
// Families absent from every row (no KTX block and no derivable
// equivalent) are omitted.
type PlayerStatsSources struct {
	Score    string `json:"score"`
	Damage   string `json:"damage,omitempty"`
	Accuracy string `json:"accuracy,omitempty"`
	Pickups  string `json:"pickups,omitempty"`
	Hold     string `json:"hold"`
}

// PlayerStatsRow is one player's (or team's) canonical statistics.
type PlayerStatsRow struct {
	Name string `json:"name"`
	Team string `json:"team,omitempty"`

	// KTX-only identity fields, straight from the demoinfo block and
	// absent on demos without one. Not derivable from the wire.
	Ping     int          `json:"ping,omitempty"`
	Handicap int          `json:"handicap,omitempty"`
	Login    string       `json:"login,omitempty"`
	Bot      *DemoInfoBot `json:"bot,omitempty"`

	Window PlayerStatsWindow `json:"window"`
	Score  PlayerStatsScore  `json:"score"`
	// Damage is omitted when the demo carries no damage information at
	// all — neither a KTX damage stream to reconstruct from nor a
	// demoinfo block (common on pre-2020 demos).
	Damage *PlayerStatsDamage `json:"damage,omitempty"`
	// Accuracy is KTX-only and therefore omitted on demos without a
	// demoinfo block. There is deliberately no derived fallback: KTX
	// counts pellets server-side, so a wire-inferred approximation under
	// the same key would let a caller compare two different measurements
	// across demos without noticing.
	Accuracy *PlayerStatsAccuracy `json:"accuracy,omitempty"`
	Pickups  *PlayerStatsPickups  `json:"pickups,omitempty"`
	Hold     PlayerStatsHold      `json:"hold"`
}

// PlayerStatsWindow is the set of denominators every rate in the row can
// be read against. All values are match-relative milliseconds on the game
// clock, so pauses are already excluded.
//
// Nothing in this section has an implicit denominator: KTX's hold clocks
// stop at death, making alive time their unstated divisor, and that
// silence is exactly what makes two "RA control" numbers from different
// tools incomparable.
type PlayerStatsWindow struct {
	// MatchMs is the whole match window (Streams.Global MatchEnd -
	// MatchStart). Identical on every row.
	MatchMs int32 `json:"matchMs"`
	// PresentMs is how much of the match this player was in it: from
	// their first activity (or match start) to their last (or match end).
	// It distinguishes a late joiner or an early quitter from a player
	// who was there the whole time and merely dead a lot.
	PresentMs int32 `json:"presentMs"`
	// AliveMs is the time spent alive within PresentMs.
	AliveMs int32 `json:"aliveMs"`
	// DeadMs is PresentMs - AliveMs.
	DeadMs int32 `json:"deadMs"`
}

// PlayerStatsScore is the scoreboard line. Always derived: the KTX
// demoinfo stats over-count pentagram-deflect telefrags (dtTELE2), credit
// world-dealt suicides to the world entity rather than the victim
// (ktx/src/client.c:5132), and reset after a reconnect. MatchResult's
// frag-log-corrected counts are right — see PlayerStat.
type PlayerStatsScore struct {
	Src string `json:"src"`
	// Frags is the canonical QW net score from the svc_updatefrags
	// scoreboard.
	Frags     int `json:"frags"`
	Kills     int `json:"kills"`
	Deaths    int `json:"deaths"`
	Suicides  int `json:"suicides"`
	TeamKills int `json:"teamKills"`
	// Efficiency is kills / (kills + deaths) as a RATIO in [0,1] — not a
	// percentage. 0 when the player neither killed nor died.
	Efficiency Share `json:"efficiency"`
}

// PlayerStatsDamage is the damage line under KTX scoreboard semantics
// (armor absorbed, health damage capped to remaining health) — the
// bounded family, which is what the KTX numbers this merges with mean.
type PlayerStatsDamage struct {
	Src string `json:"src"`
	// Given is damage dealt to enemies.
	Given     int `json:"given"`
	GivenTeam int `json:"givenTeam"`
	GivenSelf int `json:"givenSelf"`
	// EnemyWeapons is enemy damage dealt to victims holding RL and/or LG
	// (KTX dmg_eweapon / "ewep").
	EnemyWeapons int `json:"enemyWeapons"`
	// Taken counts damage from ALL sources — enemy, team, self and
	// environment. It is deliberately NOT the same quantity as KTX's
	// dmg.taken, which is enemy-only (ktx/src/combat.c:1083); that one
	// lands in TakenEnemy so the two are never silently conflated.
	Taken int `json:"taken"`
	// TakenEnemy is KTX's enemy-only damage taken. KTX-only: the
	// reconstruction cannot split taken damage by source, so this is
	// absent (not zero) on demos without a demoinfo block.
	TakenEnemy *int `json:"takenEnemy,omitempty"`
	// TakenToDie is KTX's average damage absorbed per death. KTX-only,
	// same reasoning.
	TakenToDie *int `json:"takenToDie,omitempty"`
}

// PlayerStatsAccuracy is the per-weapon shot accounting, keyed by weapon
// ("sg", "ssg", "ng", "sng", "gl", "rl", "lg"). KTX-only — see
// PlayerStatsRow.Accuracy.
type PlayerStatsAccuracy struct {
	Src string `json:"src"`
	// ByWeapon carries KTX's acc block verbatim. Attacks is a PELLET
	// count for sg/ssg, not a trigger-pull count.
	ByWeapon map[string]DemoInfoAcc `json:"byWeapon"`
}

// PlayerStatsPickups is the per-kind pickup tally, keyed by this repo's
// item-kind vocabulary ("ra", "ya", "ga", "mh", "h15", "h25", "quad",
// "pent", "ring", "rl", "lg", "gl", "ssg", "sng", "ng", ammo kinds) —
// NOT KTX's demoinfo keys. view.PlayerStats maps the KTX keys onto this
// vocabulary when it overlays them (health_100→mh, q/p/r→quad/pent/ring).
type PlayerStatsPickups struct {
	Src    string                       `json:"src"`
	ByKind map[string]PlayerStatsPickup `json:"byKind"`
}

// PlayerStatsPickup is one kind's tally for one player.
type PlayerStatsPickup struct {
	// Took counts acquisitions that actually granted the item — for
	// weapons, pickups where the player did not already hold it (KTX
	// wpn.tooks). TotalTook counts every touch including redundant ones
	// (KTX wpn.ttooks); it is weapons-only and omitted elsewhere.
	Took      int `json:"took,omitempty"`
	TotalTook int `json:"totalTook,omitempty"`
	// Dropped counts backpacks this player left on death carrying this
	// weapon. Weapons only (RL/LG in practice — KTX emits the drop hint
	// for no others).
	Dropped int `json:"dropped,omitempty"`

	// Xfer counts pack transfers CREDITED TO THIS PLAYER as the dropper:
	// a pack they dropped carrying this weapon that a TEAMMATE then took.
	// XferSelf is the same but taken back by the dropper themselves.
	//
	// KTX's xferRL / xferLG (ktx/src/items.c:2586-2615) is the sum of the
	// two: it has no `other != dropper` check, so a player who dies,
	// respawns and re-takes their own pack increments their own counter.
	// Splitting them keeps `Xfer + XferSelf == KTX` checkable while
	// letting a caller ask the question KTX cannot answer.
	//
	// Both are POINTERS: absent means "this demo carries no backpack
	// hints, so transfers are unobservable", which is a different fact
	// from an observed zero. Teamplay-only, exactly like KTX's isTeam()
	// gate — absent on duels and FFA.
	Xfer     *int `json:"xfer,omitempty"`
	XferSelf *int `json:"xferSelf,omitempty"`
}

// PlayerStatsHold is possession time by item, the family that exists only
// here (see PlayerStatsResult). Always derived.
type PlayerStatsHold struct {
	Src string `json:"src"`
	// Weapons is keyed "rl","lg","gl","ssg","sng","ng". The shotgun and
	// axe are deliberately absent: every player holds them for the whole
	// match, so the column would carry no information.
	Weapons map[string]HoldStat `json:"weapons,omitempty"`
	// Armor is keyed "ra","ya","ga" plus "none" — the alive-time
	// complement, i.e. how long the player ran around with no armor at
	// all. KTX cannot produce "none" (its clocks never close on armor
	// reaching zero).
	Armor map[string]HoldStat `json:"armor,omitempty"`
	// Powerups is keyed "quad","pent","ring".
	Powerups map[string]HoldStat `json:"powerups,omitempty"`
}

// HoldStat is one item's possession time for one player.
type HoldStat struct {
	// Ms is total possession time in milliseconds, an exact integral over
	// the native-rate possession intervals clipped to the match window and
	// intersected with the player's alive intervals.
	Ms int32 `json:"ms"`
	// Runs is the number of disjoint possession spells; LongestMs the
	// longest single one. On a team row, Runs sums over members and
	// LongestMs is the longest run by any member.
	Runs      int   `json:"runs"`
	LongestMs int32 `json:"longestMs"`
	// ShareAlive is Ms / Window.AliveMs, ShareMatch is Ms / Window.MatchMs
	// — both ratios in [0,1]. On a team row the denominators are the
	// team's summed alive time and (match window x member count)
	// respectively, i.e. both are fractions of available TEAM time.
	ShareAlive Share `json:"shareAlive"`
	ShareMatch Share `json:"shareMatch"`
}

// shareJSONScale rounds Share values to 4 decimal places when
// SERIALIZING. Shares are ratios in [0,1], so four decimals is 0.01%
// granularity — finer than any consumer displays, while shedding the
// float32 division tail (0.6870000362396240 → 0.687). Values stay at
// native float32 resolution in memory; only the text artifact is rounded.
// Mirrors coordJSONScale (result/coord.go) — same rationale, different
// magnitude.
const shareJSONScale = 10000

// Share is a float32 ratio in [0,1] serialized at 4-decimal precision.
type Share float32

// MarshalJSON renders the rounded text form.
func (s Share) MarshalJSON() ([]byte, error) {
	r := math.Round(float64(s)*shareJSONScale) / shareJSONScale
	return strconv.AppendFloat(nil, r, 'f', -1, 64), nil
}

// NewShare divides num by den as a Share, yielding 0 for a zero or
// negative denominator (a player who was never alive has no share of
// anything — 0 beats NaN in JSON, which cannot encode it at all).
func NewShare(num, den int32) Share {
	if den <= 0 {
		return 0
	}
	return Share(float64(num) / float64(den))
}
