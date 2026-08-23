package result

// DamageResult holds per-hit damage and derived aggregates, reconstructed
// from the KTX mvdhidden_dmgdone stream (see mvd-reader MVD_FORMAT.md).
//
// Two damage families exist side by side. The RAW family (Damage, Given,
// Taken, ...) carries the wire value: UNBOUND damage including overkill,
// capped only at 9999, exactly as KTX multicasts it (unbound_dmg_dealt,
// ktx/src/combat.c:795). The BOUNDED family (Events[].Bounded and each
// player's Bounded nest) is reconstructed per hit to KTX's scoreboard
// semantics — armor absorbed + health damage capped to the victim's
// remaining health (dmg_dealt, combat.c:783). The wire does not carry the
// bounded value, but it does reveal it almost exactly: a hit the victim
// SURVIVES has no overkill, so bounded == raw identically; a KILLING hit's
// overkill is the end-of-frame death broadcast (bounded = raw + deathValue,
// the armor share cancelling). The residual slop is small and confined to
// three cases: same-frame multi-hit deaths (one death value cascaded across
// the frame's hits with an approximate save split), the -99 corpse-health
// clamp and respawn-masked deaths (both falling back to an approximate
// shadow-health cap), and the pent/tp save-share estimate. DamageReconciliation
// cross-checks both families against the KTX scoreboard.
//
// Telefrags and stomps are NOT weapon damage: they are positional instant
// kills (teleporting onto a player; landing on their head). The wire reports
// a telefrag as the 9999 cap, which would otherwise dominate the attacker's
// ByWeapon / EWep and the totals; a stomp is a movement kill, not a
// weapon. Both are excluded from the Events log, every per-weapon map
// (ByWeapon, ByWeaponTeam, ByWeaponSelf), Matrix, EWep and
// TotalDamage, and surfaced separately in Telefrags / Stomps (and as the
// opt-in "telefrag" / "stomp" events in view.Events). The kill still appears
// in FragResult / the "frag" event.
//
// Their DAMAGE does fold into Given/GivenTeam/Taken in both families,
// matching KTX's own accumulation (combat.c:1046-1076 maps them to wpNONE —
// in dmg totals, out of per-weapon ones): a telefrag folds its Bounded
// reconstruction (armor + remaining health) into the raw family too, since
// the wire 9999 is a sentinel; a stomp folds its honest wire value (raw) /
// reconstruction (bounded). An ENEMY positional kill also folds into the
// victim-weapon EnemyVs*/EWep buckets — KTX's dmg_eweapon accumulation has
// no deathtype gate either (combat.c:1073), so a kill on an RL/LG holder
// lands in EWep. The per-weapon maps, Matrix and TotalDamage stay excluded (the wpNONE
// parity). On a skipped:* demo the reversal is total: no fold at all, so
// given/taken and the buckets revert to pure v53 exclusion.
type DamageResult struct {
	TotalDamage int                      `json:"totalDamage"`
	Events      []DamageEntry            `json:"events"`               // per-hit log, time-ordered (excludes telefrags + stomps)
	ByWeapon    map[string]int           `json:"byWeapon"`             // attacker weapon -> total enemy damage
	ByPlayer    map[string]*PlayerDamage `json:"byPlayer"`             // keyed by player name
	Matrix      []DamagePair             `json:"matrix"`               // attacker -> victim totals
	Telefrags   []PositionalKill         `json:"telefrags,omitempty"`  // instant-kill telefrags, separate from damage
	Stomps      []PositionalKill         `json:"stomps,omitempty"`     // head-stomp kills, separate from damage
	Scoreboard  *DamageReconciliation    `json:"scoreboard,omitempty"` // stream vs KTX-scoreboard cross-check

	// Dmg echoes which damage family this payload carries: "both" as stored
	// (raw fields + bounded nests), "bounded" when the view materialized the
	// bounded family into the raw field names, absent on a raw view.
	Dmg string `json:"dmg,omitempty"`
	// BoundedMode is "standard" when the bounded family was reconstructed,
	// or "skipped:<mode>" when the server mode rewrites T_Damage's take in
	// ways the wire does not expose — a best-effort reconstruction there
	// would be confidently wrong, so none is attempted and every Bounded
	// field is absent. Skip set: midair (height rule), instagib (flat
	// 5000), dmgfrags (inverted pent+tele accounting), and since v71 the
	// clan-arena family ca / wipeout / ra plus lgc and race (whole damage
	// classes suppressed or rewritten between rounds; see
	// damagerecon.SkipModeReason for the shared detection incl. the
	// composite serverinfo mode string).
	BoundedMode string `json:"boundedMode,omitempty"`

	// Source records where the damage log itself came from:
	// DamageSourceKTX when it was decoded from the wire's KTX
	// mvdhidden_dmgdone stream (raw per-hit values are direct
	// measurements; the bounded family is arithmetic over them), or
	// DamageSourceReconstructed when the demo predates that instrumentation
	// and the log was reconstructed from the health/armor change streams +
	// spectator-visible evidence (package mvd-analytics/damagerecon —
	// magnitudes are near-exact, attribution is best-effort inference).
	// Absent only in pre-v71 stored results. Distinct from BoundedSource,
	// which records a view-time substitution WITHIN a KTX-sourced payload.
	Source string `json:"source,omitempty"`

	// Coverage reports how much of the match a RECONSTRUCTED damage section
	// could actually see (nil on a KTX-sourced section — see DamageCoverage).
	Coverage *DamageCoverage `json:"coverage,omitempty"`

	// BoundedSource records where a SUMMARY response's bounded per-player
	// figures came from: "ktx" when they were substituted with KTX's exact
	// end-of-match scoreboard totals (demoInfo.players[].dmg +
	// weapons[].damage.enemy/.team — authoritative where our per-hit reconstruction
	// is best-effort), or "reconstructed" when no KTX counterpart was
	// available. Set by view.Damage ONLY on an unfiltered summary response
	// that serves the bounded family (dmg=bounded or dmg=both); absent on the
	// full-log, filtered, and raw responses. The stored Result never carries
	// it. NOTE the substitution is deliberately partial: `taken` is left as
	// reconstructed (KTX dmg.taken is enemy-only while our taken counts all
	// sources), and the enemyVs* buckets keep the reconstruction (KTX has no
	// split), so they may no longer sum exactly to the substituted `given`.
	// ByWeaponSelf stays reconstructed too — KTX records no per-weapon self
	// damage — while ByWeapon and ByWeaponTeam both come from KTX's own
	// weapons[].damage sub-block.
	BoundedSource string `json:"boundedSource,omitempty"`
}

// DamageResult.Source vocabulary. DamageSourceKTX also serves as the
// BoundedSource "ktx" token (same meaning: KTX-measured figures).
const (
	DamageSourceKTX           = "ktx"
	DamageSourceReconstructed = "reconstructed"
)

// DamageCoverage answers "how much of this match did the reconstruction
// actually see?" for a Source == "reconstructed" section. It exists because
// a small class of recordings barely broadcasts the health/armor stat
// channel the reconstruction reads: positions and the frag log are intact,
// the damage simply is not observable, and without this field the section
// looks like a quiet match rather than a partial recording.
//
// The figure is the frag log's own cross-check. Every kill the log names is
// a place where damage provably happened; Covered counts the ones whose
// lethal instant the reconstructed Events log accounts for. Kills counts
// only the kills that carry damage arithmetic to account for: an enemy kill
// (not a suicide, team kill, world/nameless killer) by a weapon (telefrags
// and stomps are positional and live outside Events) on two players the
// roster names. Ratio is Covered/Kills in [0,1].
//
// Whole-match by construction: it describes the demo's reconstruction, like
// Source and BoundedMode, and is never rescoped by a player / weapon / time
// filter.
//
// Absent when the section is KTX-sourced (the wire log records every
// T_Damage call, so coverage is 1 by construction — measured exactly 1.000
// on all 65 GT demos) and when the frag log names no scoreable kill (no
// denominator). Every consumer that rides the damage section inherits its
// coverage: the playerStats damage family, derived accuracy hits, and the
// aim recon tier all read this one field rather than restating it.
//
// Measured separation (damagerecon/ACCURACY.md §per-demo coverage): 1 127
// healthy reconstructions across E0-E5 run 1.000 median, 1.000 at the 5th
// percentile, 0.950 worst; the 82 known silent-channel demos run 0.177
// median, 0.488 worst — two populations with a gap between them, not a
// gradient. There is no threshold in the code: the number is published,
// never used to gate or withhold.
type DamageCoverage struct {
	Kills   int     `json:"kills"`   // frag-log kills that carry damage arithmetic
	Covered int     `json:"covered"` // ... whose lethal instant the Events log accounts for
	Ratio   float64 `json:"ratio"`   // Covered/Kills, in [0,1]
}

// PositionalKill is one telefrag (deathtype "tele") or stomp (deathtype
// "stomp") — an instant kill from occupying a player's space rather than
// from a weapon. The wire damage is not a measurement (a telefrag reports
// the 9999 sentinel), so no raw amount is recorded; Bounded carries the
// reconstructed KTX-scoreboard value that the fold-in added to the
// attacker's given/givenTeam and the victim's taken (see DamageResult).
// Time is match-relative milliseconds.
type PositionalKill struct {
	Time     int32  `json:"time"`
	Attacker string `json:"attacker"` // killer ("world" only in the degenerate non-player case)
	Victim   string `json:"victim"`
	IsTeam   bool   `json:"isTeam,omitempty"` // killer and victim on the same team
	// Bounded is the reconstructed kill value folded into the BOUNDED
	// aggregates: telefrag = victim's full armor + remaining health (armor
	// only for dtTELE3 — the pent-vs-pent case, where KTX's invincibility
	// rule zeroes the health share); stomp = the wire value through the
	// normal bounded arithmetic. nil exactly when the bounded
	// reconstruction is skipped (no fold-in happened); 0 is a real value —
	// a teamplay-nullified stomp with no armor. Mirrors DamageEntry.Bounded's
	// pointer convention.
	Bounded *int `json:"bounded,omitempty"`
	// Damage is the RAW-family fold value when it differs from Bounded —
	// only a stomp whose bounded arithmetic capped below the wire value
	// (telefrags fold the same number into both families, so it is omitted
	// there). Absent means "equal to Bounded". Carried so a re-aggregation
	// (view.Damage's filtered recompute) reproduces the stored raw totals
	// exactly.
	Damage int `json:"damage,omitempty"`
	// VictimWep is the victim's weapon class (sg|mid|lg|rl|both) at hit time,
	// recorded on ENEMY kills only so a consumer that re-aggregates the fold
	// (view.Damage's filtered recompute) can reproduce the EnemyVs*/EWep
	// bucket fold — the same class DamageEntry.VictimWep carries. Empty on
	// team/self/world kills and when the bounded reconstruction was skipped
	// (no fold happened).
	VictimWep string `json:"victimWep,omitempty"`
}

// DamageEntry is a single damage event. Time is match-relative
// milliseconds (matches FragEntry.Time).
type DamageEntry struct {
	Time      int32  `json:"time"`
	Attacker  string `json:"attacker"` // "world" for environmental / non-player inflictor
	Victim    string `json:"victim"`
	Weapon    string `json:"weapon"`              // attacker weapon, or environmental category
	Damage    int    `json:"damage"`              // raw/unbound, including overkill
	IsSplash  bool   `json:"isSplash,omitempty"`  // indirect (e.g. rocket splash)
	IsEnv     bool   `json:"isEnv,omitempty"`     // environmental / world-sourced
	IsSelf    bool   `json:"isSelf,omitempty"`    // attacker == victim
	IsTeam    bool   `json:"isTeam,omitempty"`    // same team, not self
	VictimWep string `json:"victimWep,omitempty"` // victim's weapon class at hit: sg|mid|lg|rl|both ("" if env/self/team)

	// Bounded is the KTX-scoreboard-semantics reconstruction of this hit
	// (armor absorbed + health damage capped to remaining health). nil means
	// "equal to Damage" — the common no-overkill case. 0 is a real value: a
	// hit whose health share was fully nullified (pent / teamplay rules)
	// still emits a wire event carrying the pre-nullification amount.
	Bounded *int `json:"bounded,omitempty"`
}

// PlayerDamage holds per-player damage aggregates.
type PlayerDamage struct {
	Given     int            `json:"given"`     // to enemies (the "useful" number); KTX scoreboard analogue: dmg.given (bounded)
	Taken     int            `json:"taken"`     // from ALL sources (enemy + team + self + env). KTX dmg.taken (dmg_t) counts enemy-player damage only (combat.c:1083), so Taken runs higher.
	GivenTeam int            `json:"givenTeam"` // to teammates
	GivenSelf int            `json:"givenSelf"` // attacker == victim
	TakenEnv  int            `json:"takenEnv"`  // from world / environment
	ByWeapon  map[string]int `json:"byWeapon"`  // enemy damage given, by attacker weapon

	// ByWeaponTeam and ByWeaponSelf split GivenTeam and GivenSelf the same
	// way ByWeapon splits Given — same weapon keys, same attacker-weapon
	// perspective, same tele/stomp exclusion (their damage folds into the
	// Given* totals only, see the type comment above).
	//
	// MEASUREDNESS is family-level, never key-level: within a present
	// damage family an absent KEY means "dealt none with that weapon"
	// (a zero is dropped rather than stored), and the whole family only
	// exists when a damage stream was read. The two maps are omitempty
	// because a player who dealt no team/self damage at all has nothing
	// to say, not because the split was unmeasurable.
	ByWeaponTeam map[string]int `json:"byWeaponTeam,omitempty"` // team damage given, by attacker weapon
	ByWeaponSelf map[string]int `json:"byWeaponSelf,omitempty"` // self damage given, by attacker weapon

	// EnemyVs* partition enemy-given damage by the VICTIM's held weapons at
	// the moment of the hit — KTX "ewep" semantics, keyed on the target's
	// inventory (ktx/src/combat.c:1084-1089), NOT the attacker's weapon.
	// Mutually exclusive, priority RL+LG > RL > LG > mid > sg; the five
	// buckets sum to Given.
	EnemyVsSG   int `json:"enemyVsSg"`   // victim holds shotgun-tier only (sg/ng)
	EnemyVsMid  int `json:"enemyVsMid"`  // victim holds ssg/sng/gl, no LG/RL
	EnemyVsLG   int `json:"enemyVsLg"`   // victim holds LG, not RL
	EnemyVsRL   int `json:"enemyVsRl"`   // victim holds RL, not LG
	EnemyVsBoth int `json:"enemyVsBoth"` // victim holds both RL and LG
	EWep        int `json:"ewep"`        // = EnemyVsLG + EnemyVsRL + EnemyVsBoth (KTX dmg_eweapon)

	Telefrags int `json:"telefrags,omitempty"` // in-match, non-team instant-kill telefrags DEALT (a count; team telefrags are not credited, per the team-kill convention)
	Stomps    int `json:"stomps,omitempty"`    // in-match, non-team head-stomp kills DEALT (a count; team stomps are not credited)

	// Bounded mirrors the damage aggregates above under KTX bounded-scoreboard
	// semantics (per-hit reconstruction; see DamageResult). Invariant: the
	// nest never sets Telefrags/Stomps/Bounded (all omitempty), so its JSON
	// shape is exactly the damage-figure fields. Absent when reconstruction
	// was skipped (DamageResult.BoundedMode) or on a raw view.
	Bounded *PlayerDamage `json:"bounded,omitempty"`
}

// BoundedNest lazily creates and returns this player's bounded-family
// aggregate. The nest is itself a PlayerDamage (same field names, same
// helpers) with the invariant that its Telefrags/Stomps/Bounded stay
// zero/nil, so its JSON shape is exactly the damage-figure fields. Both the
// analyzer (building the stored shape) and view.Damage's filtered recompute
// fold bounded values through this one helper.
func (p *PlayerDamage) BoundedNest() *PlayerDamage {
	if p.Bounded == nil {
		p.Bounded = &PlayerDamage{ByWeapon: make(map[string]int)}
	}
	return p.Bounded
}

// AddWeaponDamage folds n into m under the attacker-weapon key w and
// returns the map, allocating it on first use. The map-returning form is
// mandatory: ByWeaponTeam / ByWeaponSelf are omitempty and start nil, and a
// plain func(map, w, n) cannot replace a nil map at the caller. Every
// producer of the three per-weapon damage maps (analyzer aggregation,
// view.Damage's filtered recompute) folds through this one helper so a new
// PlayerDamage constructor can never reintroduce a nil-map panic.
func AddWeaponDamage(m map[string]int, w string, n int) map[string]int {
	if m == nil {
		m = map[string]int{}
	}
	m[w] += n
	return m
}

// DamagePair is one attacker→victim total in the damage matrix.
type DamagePair struct {
	Attacker string         `json:"attacker"`
	Victim   string         `json:"victim"`
	Damage   int            `json:"damage"`
	ByWeapon map[string]int `json:"byWeapon"` // attacker weapon -> damage to this victim
}

// DamageReconciliation cross-checks the stream-derived per-player totals
// against the KTX end-of-match scoreboard (demoInfo.players[].dmg). It is
// diagnostic: divergence is surfaced as data, never used to coerce the
// stream-derived numbers.
type DamageReconciliation struct {
	ByPlayer map[string]*DamageDelta `json:"byPlayer"`
}

// DamageDelta pairs this pipeline's figure with the KTX-scoreboard figure
// for one player. The Stream* fields are UNBOUND (overkill-inclusive, from
// the mvdhidden_dmgdone stream); the Score* fields are BOUNDED (capped to
// victim health, from the KTX scoreboard JSON). Score* <= Stream* by the
// overkill; the gap is expected, not a reconstruction error. The Bounded
// nest compares like-for-like: our bounded reconstruction against the same
// scoreboard, where near-equality IS the correctness signal.
type DamageDelta struct {
	StreamGiven int `json:"streamGiven"` // unbound, this pipeline
	ScoreGiven  int `json:"scoreGiven"`  // bounded, KTX scoreboard (dmg.given)
	StreamTaken int `json:"streamTaken"` // unbound, this pipeline
	ScoreTaken  int `json:"scoreTaken"`  // bounded, KTX scoreboard (dmg.taken)
	StreamEWep  int `json:"streamEwep"`  // unbound, this pipeline
	ScoreEWep   int `json:"scoreEwep"`   // bounded, KTX scoreboard (dmg.enemy-weapons)

	Bounded *DamageDeltaBounded `json:"bounded,omitempty"` // bounded-stream side; absent when reconstruction skipped
}

// DamageDeltaBounded carries the bounded-reconstruction reconciliation
// figures. StreamTaken here is ENEMY-ONLY — KTX dmg_t accumulates only in
// the enemy branch (ktx/src/combat.c:1069) — unlike PlayerDamage.Taken
// (raw or bounded nest), which counts all sources.
type DamageDeltaBounded struct {
	StreamGiven int `json:"streamGiven"` // bounded enemy given, this pipeline
	StreamTaken int `json:"streamTaken"` // bounded enemy-only taken, this pipeline
	StreamEWep  int `json:"streamEwep"`  // bounded ewep, this pipeline
	StreamTeam  int `json:"streamTeam"`  // bounded team given, this pipeline
	ScoreTeam   int `json:"scoreTeam"`   // KTX scoreboard (dmg.team)
}
