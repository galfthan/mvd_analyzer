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
// Schema v63.
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
	// SrcDerivedUnbounded: computed by this pipeline, but from the RAW
	// wire damage rather than the bounded reconstruction, because the
	// server mode made a bounded reconstruction impossible.
	//
	// analyzer/damage.go builds the bounded family only when boundedSkip
	// is empty (damage.go:308,542-546); on a k_midair / k_instagib /
	// k_dmgfrags demo it is skipped entirely, because those modes rewrite
	// T_Damage's take in ways the wire does not expose and a best-effort
	// reconstruction would be confidently wrong. The damage family then
	// silently became raw wire damage INCLUDING overkill while still
	// reading "derived" — measured on 4on4_oeks_vs_tsq[dm2], raw exceeds
	// bounded by 38-44%, and on k_instagib the wire value is a flat
	// 5000/hit. This value is what makes that degradation legible: a
	// caller branches on one field instead of correlating `src` with
	// damage.boundedMode.
	//
	// Applies to the damage family only, and inherits into the Sources
	// roll-up like any other per-row value.
	SrcDerivedUnbounded = "derived:unbounded"
	// SrcReconstructed: the damage family only, on demos whose wire never
	// carried the KTX damage stream — the numbers come from the
	// damage-recon node's reconstruction (DamageResult.Source ==
	// DamageSourceReconstructed: health/armor deltas + spectator-visible
	// evidence, see mvd-analytics/damagerecon). Magnitudes are near-exact
	// but attribution is inference: treat given/givenTeam/givenSelf as
	// ~1% / indicative estimates (damagerecon/ACCURACY.md has the
	// per-field error tables). Distinct from SrcDerived, which is computed
	// from the WIRE damage stream and is measurement-grade; the same
	// legibility rule as SrcDerivedUnbounded — the number must never
	// silently change grade while reading "derived".
	SrcReconstructed = "reconstructed"
	// SrcMixed appears in the Sources roll-up and on TEAM rows whose
	// members disagreed about where the family came from (the shared-or-
	// mixed aggregation rule — see AggregateAccuracy and the view's team
	// reaggregation). A PLAYER row never carries it: per-player families
	// come from exactly one source.
	//
	// It is a CANARY, not a data condition. Measured across every local
	// demo carrying a KTX block, the playerStats name set and the demoinfo
	// name set are identical and every listed player carries both a dmg
	// blob and an acc entry — so a demo either has the block for everyone
	// or for nobody. The one way a mix ever arose was a roster row KTX had
	// never heard of (a refused connection surfaced as a player), which is
	// the phantom-roster defect. If this value is ever served, that defect
	// is back.
	SrcMixed = "mixed"
)

// PlayerStatsSources records, per family, which source the rows carry.
// Families absent from every row (no KTX block and no derivable
// equivalent) are omitted.
//
// COMPUTED FROM THE ROWS BEING SERVED, after any filtering: all rows KTX
// -> "ktx", none -> "derived", disagreement -> "mixed". It used to be set
// from an "any row matched KTX" flag on the unfiltered set, which both
// over-reported (one KTX-matched row badged the whole family) and
// described rows a filter had since removed.
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

	// Identity and Sessions are the reconnect-unification key and the wire
	// occupancies behind this row, copied verbatim from the player's stream
	// (see result.PlayerStream for the full contract — the key is DEMO-LOCAL
	// and must not be persisted or compared across demos). Two rows sharing
	// an identity are one human under two names; the sessions say which slot
	// and userid they were at any instant.
	//
	// Absent on TEAM rows (an aggregate is not a connection) and on a
	// scoreboard-only player row — one the KTX block lists but that produced
	// no stream, so there is no occupancy to attribute.
	Identity string          `json:"identity,omitempty"`
	Sessions []PlayerSession `json:"sessions,omitempty"`

	// KTX-only identity fields, straight from the demoinfo block and
	// absent on demos without one. Not derivable from the wire.
	//
	// Ping is a POINTER for the measured-zero rule: KTX writes the key
	// unconditionally, so a 0 there would be a reading rather than a gap.
	// (In practice unreachable — ping is measured over frame round-trips,
	// so its floor is one frame, and KTX bots report synthetic nonzero
	// pings. The pointer is for consistency, not for a live case.)
	Ping     *int         `json:"ping,omitempty"`
	Handicap int          `json:"handicap,omitempty"`
	Login    string       `json:"login,omitempty"`
	Bot      *DemoInfoBot `json:"bot,omitempty"`

	// ControlMs is KTX's "control time" — how long the player held map
	// control by its own reckoning (ktx/src/stats_json.c writes it as
	// float seconds; converted to int32 ms here for the pure-ms model).
	// KTX-only: there is no wire-side equivalent. Our own control
	// measure is the region-control view, which is not the same thing.
	ControlMs *int32 `json:"controlMs,omitempty"`
	// Speed is KTX's per-player speed summary in Quake units/second.
	// KTX-only today; the position streams could support a derived
	// version, which is left for a follow-up.
	Speed *PlayerStatsSpeed `json:"speed,omitempty"`

	// Members is the number of players on a TEAM row that were actually
	// in the match (PresentMs > 0) — exactly the count its ShareMatch
	// denominator (match window x members) rests on, published so a
	// consumer can recompute or re-scale it. A scoreboard-only row
	// (connected, never streamed) is summed into the team's totals but
	// does NOT count here, because it contributed no time anyone could
	// have played.
	//
	// A POINTER, set on team rows and only there: an `omitempty` int
	// dropped the key exactly when it mattered most — a team whose only
	// member never streamed serializes with no `members` while every
	// ShareMatch on it rests on matchMs x 0, so the one row whose
	// denominator a consumer must check was the one that hid it.
	Members *int `json:"members,omitempty"`

	Window PlayerStatsWindow `json:"window"`
	Score  PlayerStatsScore  `json:"score"`
	// Damage is omitted when the demo carries no damage information at
	// all — neither a KTX damage stream to reconstruct from nor a
	// demoinfo block (common on pre-2020 demos). A player who neither
	// dealt nor took a point of damage on a demo that DOES carry the
	// stream gets a zeroed family, not an absent one: that is an observed
	// zero, and it is a different fact from "unmeasurable".
	Damage *PlayerStatsDamage `json:"damage,omitempty"`
	// Accuracy is KTX's block where the demo carries one, else a
	// reconstruction from the decoded fire stream — the two are different
	// measurements and Src says which you have. Omitted only when the
	// demo decoded no weapon fires for this player at all. See
	// PlayerStatsAccuracy for what each source counts.
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
// (ktx/src/client.c:4951), and reset after a reconnect. MatchResult's
// frag-log-corrected counts are right — see PlayerStat.
//
// The two SIDES of this struct rest on different evidence, and only one
// of them is always available. Frags is the svc_updatefrags net score,
// straight off the wire; Deaths is counted from the protocol death
// events. Both are measured on every demo. Everything else — kills,
// suicides, teamKills, byWeapon and the efficiency computed from them —
// is attributed from the OBITUARY-derived frag log, which some servers
// never give us in a matchable form. Those fields are therefore
// POINTERS: absent means kill attribution was not measurable on this
// demo, which is a different fact from a player who killed nobody.
type PlayerStatsScore struct {
	Src string `json:"src"`
	// Frags is the canonical QW net score from the svc_updatefrags
	// scoreboard.
	Frags int `json:"frags"`
	// Kills, Suicides and TeamKills come from the frag log. Absent
	// together, whenever it carries no entries at all on a demo where
	// players demonstrably died — serving 0 kills beside 121 deaths is
	// byte-indistinguishable from a genuinely awful team.
	Kills     *int `json:"kills,omitempty"`
	Deaths    int  `json:"deaths"`
	Suicides  *int `json:"suicides,omitempty"`
	TeamKills *int `json:"teamKills,omitempty"`
	// Efficiency is kills / (kills + deaths) as a RATIO in [0,1] — not a
	// percentage. 0 when the player neither killed nor died; absent
	// exactly when Kills is, since it is computed from it.
	Efficiency *Share `json:"efficiency,omitempty"`
	// ByWeapon is enemy kills split by the weapon that dealt them, keyed
	// like the rest of this section ("rl", "lg", "sg", ...). From the
	// corrected frag log, so it is on the same footing as Kills above and
	// never overlaid — KTX's per-weapon kills inherit the same reconnect
	// and telefrag over-counting the top-level stats do. Weapons the
	// player never killed with are omitted, not zero-filled.
	ByWeapon map[string]int `json:"byWeapon,omitempty"`
	// ByEnemyWeapon is the OTHER axis: the same enemy kills split by what
	// the VICTIM was holding when they died, keyed by the victim-weapon
	// vocabulary (VictimWeapon* below), NOT by the weapon names ByWeapon
	// uses. It is the weapon-denial figure — "how many armed enemies did
	// I take out" — and the kill-side counterpart of
	// PlayerStatsDamage.ByEnemyWeapon.
	//
	// A PARTITION of Kills: the buckets are mutually exclusive and sum to
	// Kills exactly, which is the whole reason for the "both" bucket. Do
	// not read `rl` alone as "enemy RLs killed" — that is `rl + both`.
	// (KTX's own per-weapon ekills counter is INCLUSIVE instead, bumping
	// every bucket the victim's inventory carried, and is not what this
	// is: see the derivation note below.)
	//
	// Derived on every demo carrying streams, never overlaid from KTX —
	// the same standing as ByWeapon, and for the same reason. KTX's
	// ekills is defective for this purpose beyond the inclusive keying:
	// it force-zeroes the axe and sg buckets, and zeroes EVERY bucket on
	// deathmatch >= 4 and on k_instagib (ktx/src/stats_json.c:377-380),
	// so its zeros are not readings. The verbatim KTX numbers remain
	// available on the demoinfo section for anyone who wants them.
	//
	// Absent exactly when Kills is — the kill side is measured or omitted
	// as one family. Within a present map, zero buckets are omitted like
	// every other by-weapon map here.
	ByEnemyWeapon map[string]int `json:"byEnemyWeapon,omitempty"`
	// ByWeaponVsEnemyWeapon is the JOINT distribution the two maps above
	// are the marginals of: killer weapon -> victim-weapon bucket -> kills.
	// The outer key is ByWeapon's vocabulary (the obituary weapon codes,
	// including tele / stomp / unknown), the inner key ByEnemyWeapon's
	// (VictimWeapon*), and it answers what neither marginal can — "how many
	// of my LG kills were against enemies carrying an RL", the question a
	// team asks when it wants to know whether a weapon is winning fights or
	// just finishing off the disarmed.
	//
	// Summing it recovers both marginals EXACTLY, which is the invariant
	// this field rests on and the corpus check asserts:
	//
	//	sum over inner keys  -> ByWeapon
	//	sum over outer keys  -> ByEnemyWeapon
	//
	// so a consumer never has to decide which of the three to trust. Same
	// measuredness as its marginals (absent exactly when Kills is) and the
	// same zero-dropping: an outer key exists only if the player killed
	// with that weapon, an inner key only if that pairing happened.
	//
	// It is the widest map in this section — up to (weapons used) x 6 — so
	// it is the one to skip when a consumer only needs a scoreboard. The
	// marginals cost nothing and answer most questions.
	ByWeaponVsEnemyWeapon map[string]map[string]int `json:"byWeaponVsEnemyWeapon,omitempty"`
}

// The victim-weapon vocabulary: the buckets that PlayerStatsScore
// .ByEnemyWeapon and PlayerStatsDamage.ByEnemyWeapon are keyed by, and the
// same classification the DamageResult EnemyVs* fields use
// (analyzer.victimWeaponClass, mirroring ktx/src/combat.c:1084-1089).
//
// MUTUALLY EXCLUSIVE and applied in priority order RL+LG > RL > LG > mid >
// sg, so every kill and every point of enemy damage lands in exactly one.
// "Enemy RLs" is therefore VictimWeaponRL + VictimWeaponBoth, and likewise
// for LG — the two overlap in Both by construction.
//
// The vocabulary is deliberately COARSER than the killer-weapon one: it
// answers "how well armed was the target", where ssg/sng/gl are one tier
// and the axe/sg/ng floor every player always carries is another. A
// finer split would imply a precision the victim's inventory bitfield
// does not carry — it says what they held, not what they would have used.
const (
	// VictimWeaponSG: the victim held nothing above the shotgun tier
	// (axe / sg / ng only) — the loadout every player respawns with, so
	// this bucket is "killed them while they had nothing".
	VictimWeaponSG = "sg"
	// VictimWeaponMid: the victim held ssg, sng and/or gl, but neither RL
	// nor LG.
	VictimWeaponMid = "mid"
	// VictimWeaponLG: the victim held LG and not RL.
	VictimWeaponLG = "lg"
	// VictimWeaponRL: the victim held RL and not LG.
	VictimWeaponRL = "rl"
	// VictimWeaponBoth: the victim held BOTH RL and LG — the fully armed
	// enemy, and the reason the buckets partition rather than overlap.
	VictimWeaponBoth = "both"
	// VictimWeaponUnknown: the kill is real but the victim's loadout is
	// not knowable — they produced no possession stream at all (a
	// scoreboard-only player, or a slot the stream builder never saw).
	// Kept as its own bucket rather than folded into SG, which would
	// assert the victim was unarmed, and rather than dropped, which would
	// break the partition. Appears on the KILLS side only: the damage
	// buckets classify from the victim's inventory carried on each hit,
	// which is never missing when the hit itself was read.
	VictimWeaponUnknown = "unknown"
)

// PlayerStatsDamage is the damage line under KTX scoreboard semantics
// (armor absorbed, health damage capped to remaining health) — the
// bounded family, which is what the KTX numbers this merges with mean.
//
// EXCEPT where the server mode made the bounded reconstruction
// impossible, in which case these are RAW wire numbers including overkill
// and Src says so: see SrcDerivedUnbounded. This is the one family whose
// Src is three-valued.
type PlayerStatsDamage struct {
	// Src is "ktx", "derived", or "derived:unbounded" — the last meaning
	// the numbers are raw wire damage because no bounded reconstruction
	// exists for this demo (DamageResult.BoundedMode is skipped:*).
	Src string `json:"src"`
	// Given is damage dealt to enemies.
	Given     int `json:"given"`
	GivenTeam int `json:"givenTeam"`
	GivenSelf int `json:"givenSelf"`
	// EnemyWeapons is enemy damage dealt to victims holding RL and/or LG
	// (KTX dmg_eweapon / "ewep").
	EnemyWeapons int `json:"enemyWeapons"`
	// ByEnemyWeapon splits Given by what the VICTIM was holding at the
	// moment of each hit, keyed by the victim-weapon vocabulary
	// (VictimWeapon* — sg / mid / lg / rl / both). It is the full
	// breakdown EnemyWeapons above summarises: EnemyWeapons is exactly
	// lg + rl + both, and the two remaining buckets are the damage that
	// went into targets who were NOT holding a big weapon, which no
	// published figure carried before.
	//
	// The buckets are MUTUALLY EXCLUSIVE, and the damage counterpart of
	// PlayerStatsScore.ByEnemyWeapon, keyed identically so the two axes
	// read alike. "Damage into enemy RLs" is `rl + both`, not `rl`.
	//
	// They partition the enemy damage THIS PIPELINE RECONSTRUCTED, which
	// is not always the same number as Given. On a derived row the two
	// agree exactly. On a KTX-OVERLAID row they do not: Given becomes
	// KTX's own dmg.given counter while this split stays the
	// reconstruction's, since KTX has no per-tier equivalent to merge in.
	// Measured over the cached corpus (82 KTX rows), 66 carried a
	// residual, the largest 16 damage, 208 in total against 659,577 given
	// — 0.03%. So do not compute a share-of-given from these buckets and
	// expect it to reach exactly 100% on a `src: "ktx"` row.
	//
	// The KILL side has no such gap: Score is never overlaid, so
	// PlayerStatsScore.ByEnemyWeapon sums to Kills exactly.
	//
	// DERIVED ONLY, and finer than the server's own accounting: KTX
	// tracks a single dmg_eweapon scalar that lumps RL and LG together
	// (ktx/src/combat.c:1075) and has no per-tier split at all, so this
	// map rides through the KTX overlay untouched rather than being
	// merged with it — the same treatment ByWeaponSelf gets, and for the
	// same reason.
	//
	// MEASUREDNESS follows the damage STREAM, not the family: it needs
	// the victim's inventory on each hit, so it is present exactly when
	// Taken is (see ByWeaponSelf above) and absent on a demo carrying a
	// KTX block but no damage stream. Telefrags and stomps are excluded
	// like everywhere else in this family — positional kills, not weapon
	// damage — which is one way this map differs from the kill-side
	// sibling, where they ARE classified and counted.
	ByEnemyWeapon map[string]int `json:"byEnemyWeapon,omitempty"`
	// ByWeapon is enemy damage GIVEN split by the attacker's weapon, keyed
	// like the rest of this section. Follows Src with the family: KTX's
	// weapons[w].damage.enemy when the block carries it, else the bounded
	// reconstruction's PlayerDamage.ByWeapon. Weapons the player dealt no
	// damage with are omitted, not zero-filled.
	//
	// NOTE this splits ENEMY given only. ByWeaponTeam / ByWeaponSelf
	// below split the other two given directions; there is still no
	// by-weapon split of TAKEN damage on either side — KTX does not
	// record one and the victim's per-hit log would answer a different
	// question (who shot me with what), so that absence is a real gap,
	// not an oversight.
	ByWeapon map[string]int `json:"byWeapon,omitempty"`
	// ByWeaponTeam and ByWeaponSelf split GivenTeam and GivenSelf by the
	// attacker's weapon, keyed exactly like ByWeapon. Telefrags and stomps
	// are excluded from all three (positional kills, not weapon damage).
	//
	// MEASUREDNESS is family-level and has two rules, which a consumer
	// must apply instead of reading anything into omitempty:
	//   - ByWeapon and ByWeaponTeam are measured whenever this damage
	//     family is present. Src "ktx" means the KTX block existed, and
	//     KTX counts per-weapon enemy AND team damage for every weapon
	//     entry (ktx/src/stats_json.c:208-212 writes the pair in one
	//     sub-block, omitted only when both are zero). Src "derived*"
	//     means a damage stream was read.
	//   - ByWeaponSelf is measured ONLY when a damage stream exists — KTX
	//     has no per-weapon self counter — which is exactly what a
	//     non-nil Taken says. On a KTX-block-without-stream demo Taken is
	//     absent and so is this map, whatever the player did.
	// Within a measured family an absent KEY means the player dealt none
	// with that weapon. The derived copy drops zeros; KTX keeps an
	// explicit measured 0 where the weapon's damage sub-block exists, and
	// omits the sub-block when both its counters are zero — so key-level
	// absence is zero-or-never either way, and never distinguishable.
	ByWeaponTeam map[string]int `json:"byWeaponTeam,omitempty"`
	ByWeaponSelf map[string]int `json:"byWeaponSelf,omitempty"`
	// TeamWeapons is the same measure for TEAMMATES holding RL/LG (KTX
	// dmg_tweapon, ktx/src/combat.c:1063) — the friendly-fire mirror of
	// EnemyWeapons. KTX-only: our reconstruction does not bucket team
	// damage by the victim's inventory.
	TeamWeapons *int `json:"teamWeapons,omitempty"`
	// Taken counts damage from ALL sources — enemy, team, self and
	// environment. It is deliberately NOT the same quantity as KTX's
	// dmg.taken, which is enemy-only (ktx/src/combat.c:1069); that one
	// lands in TakenEnemy so the two are never silently conflated.
	//
	// A POINTER because only our per-hit reconstruction measures it: on a
	// demo that carries a KTX block but no damage stream, KTX supplies
	// every other field here and this one is genuinely unmeasured. A zero
	// would read as "took no damage at all".
	Taken *int `json:"taken,omitempty"`
	// TakenEnemy is enemy-only damage taken — KTX's dmg_t. NOT KTX-only:
	// KTX's value when the block carries it, otherwise reconstructed from
	// the per-hit log by summing the hits flagged neither team, self nor
	// environment (analyzer.deriveTakenEnemy). A POINTER because that
	// reconstruction needs a damage stream: absent, not zero, on a demo
	// carrying no damage information at all.
	TakenEnemy *int `json:"takenEnemy,omitempty"`
	// TakenToDie is the average damage absorbed per death,
	// TakenEnemy / deaths, on the same footing as TakenEnemy — derived
	// wherever that is. Additionally absent when the player never died;
	// KTX's 99999 no-deaths sentinel (ktx/src/stats_json.c:357) is never
	// served as a number.
	TakenToDie *int `json:"takenToDie,omitempty"`
}

// PlayerStatsAccuracy is the per-weapon shot accounting, keyed by weapon
// ("axe", "sg", "ssg", "ng", "sng", "gl", "rl", "lg" — KTX records axe
// swings too, ktx/src/weapons.c:85).
//
// Src decides what the numbers MEAN, and the two sources are not the same
// measurement:
//
//   - "ktx": KTX's own server-side counters, verbatim. Attacks is a PELLET
//     count for sg/ssg (`attacks += bullets`, ktx/src/weapons.c:812; a fire
//     count for every other weapon) and Hits counts pellets that connected
//     (ktx/src/weapons.c:387). Real / Virtual are a SEPARATE rl/gl-only
//     counter, NOT a split of Hits — see below.
//   - "derived": reconstructed from the decoded fire stream. Attacks is
//     always a TRIGGER-PULL count, and Hits counts fires that produced at
//     least one linked damage event — so shotgun accuracy in particular
//     reads on a different scale. Real/Virtual have no equivalent and are
//     absent.
//
// So compare accuracies across demos only after checking Src. The derived
// form is offered because a demo with no KTX block should degrade to a
// rougher number rather than to a missing field — but it is only as good
// as the shot attribution underneath it (see the /shots section's own
// caveats), which on some older demos mislabels a player's weapon.
type PlayerStatsAccuracy struct {
	Src      string                    `json:"src"`
	ByWeapon map[string]PlayerStatsAcc `json:"byWeapon"`
}

// PlayerStatsAcc is one weapon's shot accounting for one player.
type PlayerStatsAcc struct {
	// Attacks is pellets (KTX, sg/ssg) or trigger pulls (derived, and KTX
	// for every other weapon) — see PlayerStatsAccuracy.Src.
	Attacks int `json:"attacks"`
	// Hits is ABSENT rather than zero when there is nothing to count it
	// against: a derived block on a demo with no damage stream can count
	// fires but can link none of them, and a zero there would read as "shot
	// and never hit" instead of "not measurable".
	Hits *int `json:"hits,omitempty"`
	// Real and Virtual are KTX's rhits / vhits (ktx/src/combat.c:1085,1100),
	// present on rl and gl only. They count VICTIMS DAMAGED BY A BLAST, not
	// rockets that hit — one rocket splashing three players adds three — so
	// they routinely EXCEED Hits, which for rl/gl is the direct-impact count
	// (the rocket entity touching a player, ktx/src/weapons.c:994 for rl, :1329 for gl). They are
	// not a direct/splash split of Hits, and the ratio Real/Attacks is not an
	// accuracy.
	//
	// Real counts victims who actually lost health or armor. Virtual counts
	// victims who WOULD have, measured before godmode / pentagram / teamplay
	// damage-avoidance zeroed the damage out (`virtual_take` is latched at
	// ktx/src/combat.c:719, ahead of those checks), so Virtual >= Real and
	// the gap is damage that was prevented rather than missed.
	Real    *int `json:"real,omitempty"`
	Virtual *int `json:"virtual,omitempty"`
}

// AggregateAccuracy sums member accuracy blocks into a team row's.
//
// It lives here rather than in either caller because BOTH need it and
// must agree: the analyzer aggregates the derived rows into the stored
// artifact, and view.PlayerStats re-aggregates after the KTX overlay
// (accuracy is overlaid per player at read time, so an analyzer-only
// aggregate would be stale on every KTX demo).
//
// Two rules the old frontend implementation got wrong:
//
//   - Hits is *int where absent means "not measurable" — a derived block
//     on a demo with no damage stream counts fires but can link none of
//     them. Summing a member who has it with one who does not understates
//     the team hit-rate under a number that looks measured, so the team
//     value stays ABSENT unless EVERY contributing member carries it.
//   - Src is stamped from the members and must agree. A disagreement is
//     the phantom-roster defect (see SrcMixed), not a data condition;
//     SrcMixed is recorded rather than silently picking one.
//
// Real / Virtual are deliberately NOT aggregated, for the same reason
// TakenToDie is not: KTX omits the pair entirely unless it recorded one
// (ktx/src/stats_json.c:146), so an all-or-nothing rule would drop them
// whenever a single member never fired rl/gl, and a partial sum would
// silently under-count. They stay a per-player reading.
//
// Returns nil when no member carries the family.
func AggregateAccuracy(members []*PlayerStatsAccuracy) *PlayerStatsAccuracy {
	byWeapon := map[string]PlayerStatsAcc{}
	// hitsSeen counts members contributing to a weapon; hitsHave counts
	// those whose Hits was measurable. Equal at the end == everybody had it.
	hitsSeen, hitsHave := map[string]int{}, map[string]int{}
	src := ""
	any := false
	for _, m := range members {
		if m == nil {
			continue
		}
		any = true
		switch {
		case src == "":
			src = m.Src
		case src != m.Src:
			src = SrcMixed
		}
		for w, e := range m.ByWeapon {
			agg := byWeapon[w]
			agg.Attacks += e.Attacks
			hitsSeen[w]++
			if e.Hits != nil {
				hitsHave[w]++
				n := 0
				if agg.Hits != nil {
					n = *agg.Hits
				}
				n += *e.Hits
				agg.Hits = &n
			}
			byWeapon[w] = agg
		}
	}
	if !any {
		return nil
	}
	for w, agg := range byWeapon {
		if hitsHave[w] != hitsSeen[w] {
			agg.Hits = nil
			byWeapon[w] = agg
		}
	}
	return &PlayerStatsAccuracy{Src: src, ByWeapon: byWeapon}
}

// PlayerStatsPickups is the per-kind pickup tally, keyed by this repo's
// item-kind vocabulary ("ra", "ya", "ga", "mh", "h15", "h25", "quad",
// "pent", "ring", "rl", "lg", "gl", "ssg", "sng", "ng", ammo kinds) —
// NOT KTX's demoinfo keys. A KTX overlay can additionally introduce
// "sg" and "axe", which KTX counts and this pipeline does not derive. view.PlayerStats maps the KTX keys onto this
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
	// Weapons is keyed "rl","lg","gl","ssg","sng". The shotgun and axe
	// are deliberately absent: every player holds them for the whole
	// match, so the column would carry no information. The NAILGUN is
	// absent for a different reason — PlayerStream tracks no NG
	// possession interval (result/streams.go records rl/lg/gl/ssg/sng
	// only), so there is nothing to integrate. Adding it means adding
	// that stream first.
	Weapons map[string]HoldStat `json:"weapons,omitempty"`
	// Armor is keyed "ra","ya","ga" plus "none" — the alive-time
	// complement, i.e. how long the player ran around with no armor at
	// all. KTX cannot produce "none" (its clocks never close on armor
	// reaching zero).
	Armor map[string]HoldStat `json:"armor,omitempty"`
	// Powerups is keyed "quad","pent","ring".
	Powerups map[string]HoldStat `json:"powerups,omitempty"`
}

// PlayerStatsSpeed is KTX's speed summary, Quake units per second.
type PlayerStatsSpeed struct {
	Max float32 `json:"max"`
	Avg float32 `json:"avg"`
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
