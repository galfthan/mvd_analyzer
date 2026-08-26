package result

// DemoInfoResult contains parsed KTX embedded JSON stats (authoritative).
type DemoInfoResult struct {
	Version   int    `json:"version,omitempty"`
	Date      string `json:"date,omitempty"`
	Map       string `json:"map,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	IP        string `json:"ip,omitempty"`
	Port      int    `json:"port,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Timelimit int    `json:"timelimit,omitempty"`
	Fraglimit int    `json:"fraglimit,omitempty"`
	// Deathmatch and Teamplay are KTX's `dm` / `tp` keys — the deathmatch
	// and teamplay cvars in force at match end (ktx/src/stats_json.c:
	// 492-502). Both were parsed and then dropped before schema v75, which
	// left the pipeline guessing at teamplay from serverinfo on demos whose
	// server had already written the answer down.
	//
	// KTX writes each key only when the cvar is non-zero, so 0 here means
	// "the server said zero, or this build wrote no key" — and for Teamplay
	// on a KTX server those are the same statement, because FixRules forces
	// teamplay to 0 outside team/ctf/coop and to 2 inside them
	// (ktx/src/world.c:1674-1691).
	Deathmatch int              `json:"deathmatch,omitempty"`
	Teamplay   int              `json:"teamplay,omitempty"`
	Duration   int              `json:"duration,omitempty"`
	Demo       string           `json:"demo,omitempty"`
	Teams      []string         `json:"teams,omitempty"`
	Players    []DemoInfoPlayer `json:"players,omitempty"`
	RawJSON    string           `json:"rawJson,omitempty"` // For debugging failed parses
}

// DemoInfoPlayer contains player stats from KTX JSON.
type DemoInfoPlayer struct {
	Name        string         `json:"name"`
	Team        string         `json:"team"`
	TopColor    int            `json:"topColor,omitempty"`
	BottomColor int            `json:"bottomColor,omitempty"`
	Ping        int            `json:"ping,omitempty"`
	Login       string         `json:"login,omitempty"`
	Handicap    int            `json:"handicap,omitempty"`
	Bot         *DemoInfoBot   `json:"bot,omitempty"`
	Stats       *DemoInfoStats `json:"stats,omitempty"`
	Dmg         *DemoInfoDmg   `json:"dmg,omitempty"`
	Spree       *DemoInfoSpree `json:"spree,omitempty"`
	// Control is KTX's map-control time in seconds. A POINTER because KTX
	// writes it unconditionally (ktx/src/stats_json.c:362 — unlike
	// `handicap` just below it, which is conditional), so a player who
	// never held control writes a real 0. Older KTX builds omit the key
	// entirely; only a pointer tells "measured zero" from "this build did
	// not record it", and collapsing the two would let a served value
	// vanish for exactly the player the stat says the most about.
	Control *float64                   `json:"control,omitempty"`
	Speed   *DemoInfoSpeed             `json:"speed,omitempty"`
	XferRL  int                        `json:"xferRL,omitempty"`
	XferLG  int                        `json:"xferLG,omitempty"`
	Weapons map[string]*DemoInfoWeapon `json:"weapons,omitempty"`
	Items   map[string]*DemoInfoItem   `json:"items,omitempty"`
}

// DemoInfoBot is the per-player bot block KTX writes when the player slot
// is held by a frogbot. Only present when KTX was built with BOT_SUPPORT
// and the player is a bot.
type DemoInfoBot struct {
	Skill      int  `json:"skill"`
	Customised bool `json:"customised"`
}

// DemoInfoStats contains frag/death stats from KTX JSON.
type DemoInfoStats struct {
	Frags      int `json:"frags"`
	Deaths     int `json:"deaths"`
	TK         int `json:"tk,omitempty"`
	SpawnFrags int `json:"spawn-frags,omitempty"`
	Kills      int `json:"kills,omitempty"`
	Suicides   int `json:"suicides,omitempty"`
}

// DemoInfoDmg contains damage stats from KTX JSON.
type DemoInfoDmg struct {
	Taken        int `json:"taken"`
	Given        int `json:"given"`
	Team         int `json:"team,omitempty"`
	Self         int `json:"self,omitempty"`
	TeamWeapons  int `json:"team-weapons,omitempty"`
	EnemyWeapons int `json:"enemy-weapons,omitempty"`
	TakenToDie   int `json:"taken-to-die,omitempty"`
}

// DemoInfoSpree contains spree stats from KTX JSON.
type DemoInfoSpree struct {
	Max  int `json:"max,omitempty"`
	Quad int `json:"quad,omitempty"`
}

// DemoInfoSpeed contains speed stats from KTX JSON.
type DemoInfoSpeed struct {
	Max float64 `json:"max,omitempty"`
	Avg float64 `json:"avg,omitempty"`
}

// DemoInfoWeapon contains weapon stats from KTX JSON.
type DemoInfoWeapon struct {
	Acc     *DemoInfoAcc     `json:"acc,omitempty"`
	Kills   *DemoInfoKills   `json:"kills,omitempty"`
	Deaths  int              `json:"deaths,omitempty"`
	Pickups *DemoInfoPickups `json:"pickups,omitempty"`
	Damage  *DemoInfoDamage  `json:"damage,omitempty"`
}

// DemoInfoAcc contains accuracy stats from KTX JSON (authoritative).
//
// Real / Virtual are KTX's rhits / vhits (ktx/src/combat.c:1085,1100) and
// exist on rl and gl only. They are NOT a direct/splash split of Hits:
// they count VICTIMS DAMAGED BY A BLAST — one rocket splashing three
// players adds three — while Hits for rl/gl is the direct-impact count
// (ktx/src/weapons.c:994 for rl, :1329 for gl). Real therefore routinely
// exceeds Hits. Virtual is latched before godmode / pentagram / teamplay
// damage-avoidance
// (combat.c:719), so Virtual >= Real and the gap is damage prevented.
type DemoInfoAcc struct {
	Attacks int `json:"attacks"` // Pellet count for SG/SSG
	Hits    int `json:"hits"`
	Real    int `json:"real,omitempty"`
	Virtual int `json:"virtual,omitempty"`
}

// DemoInfoKills contains kill breakdown from KTX JSON.
type DemoInfoKills struct {
	Total int `json:"total,omitempty"`
	Team  int `json:"team,omitempty"`
	Enemy int `json:"enemy,omitempty"`
	Self  int `json:"self,omitempty"`
}

// DemoInfoPickups contains pickup stats from KTX JSON.
type DemoInfoPickups struct {
	Dropped         int `json:"dropped,omitempty"`
	Taken           int `json:"taken,omitempty"`
	TotalTaken      int `json:"total-taken,omitempty"`
	SpawnTaken      int `json:"spawn-taken,omitempty"`
	SpawnTotalTaken int `json:"spawn-total-taken,omitempty"`
}

// DemoInfoDamage contains damage breakdown from KTX JSON.
type DemoInfoDamage struct {
	Enemy int `json:"enemy,omitempty"`
	Team  int `json:"team,omitempty"`
}

// DemoInfoItem contains item stats from KTX JSON.
type DemoInfoItem struct {
	Took int `json:"took,omitempty"`
	Time int `json:"time,omitempty"`
}
