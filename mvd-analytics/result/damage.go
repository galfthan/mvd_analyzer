package result

// DamageResult holds per-hit damage and derived aggregates, reconstructed
// from the KTX mvdhidden_dmgdone stream (see mvd-reader MVD_FORMAT.md). All
// damage figures are raw/unbound amounts including overkill, exactly as KTX
// reports them on the wire.
//
// Unbound vs bounded: the wire carries UNBOUND damage (the full hit, capped
// only at 9999 — a telefrag is reported as 9999). KTX's end-of-match
// scoreboard (demoInfo.players[].dmg) instead accumulates damage BOUNDED to
// the victim's remaining health (no overkill). So these figures are
// systematically higher than the scoreboard, most dramatically on killing
// blows and telefrags. That gap is expected, not a defect — see
// DamageReconciliation for the cross-check.
type DamageResult struct {
	TotalDamage int                      `json:"totalDamage"`
	Events      []DamageEntry            `json:"events"`               // per-hit log, time-ordered
	ByWeapon    map[string]int           `json:"byWeapon"`             // attacker weapon -> total enemy damage
	ByPlayer    map[string]*PlayerDamage `json:"byPlayer"`             // keyed by player name
	Matrix      []DamagePair             `json:"matrix"`               // attacker -> victim totals
	Scoreboard  *DamageReconciliation    `json:"scoreboard,omitempty"` // stream vs KTX-scoreboard cross-check
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
}

// PlayerDamage holds per-player damage aggregates.
type PlayerDamage struct {
	Given     int            `json:"given"`     // to enemies (the "useful" number); KTX scoreboard analogue: dmg.given (bounded)
	Taken     int            `json:"taken"`     // from ALL sources (enemy + team + self + env). KTX dmg.taken counts enemy+env only, so Taken runs higher.
	GivenTeam int            `json:"givenTeam"` // to teammates
	GivenSelf int            `json:"givenSelf"` // attacker == victim
	TakenEnv  int            `json:"takenEnv"`  // from world / environment
	ByWeapon  map[string]int `json:"byWeapon"`  // enemy damage given, by attacker weapon

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

// DamageDelta pairs the stream-derived figure with the KTX-scoreboard figure
// for one player. Deltas should be small; large ones flag reconstruction gaps.
type DamageDelta struct {
	StreamGiven int `json:"streamGiven"`
	ScoreGiven  int `json:"scoreGiven"`
	StreamTaken int `json:"streamTaken"`
	ScoreTaken  int `json:"scoreTaken"`
	StreamEWep  int `json:"streamEwep"`
	ScoreEWep   int `json:"scoreEwep"` // KTX dmg.enemy-weapons
}
