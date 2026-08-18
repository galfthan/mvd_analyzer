package result

// MetadataResult bundles every demo metadata source we can extract from
// non-payload protocol commands: the bulk `fullserverinfo` cvar dump that
// arrives as a stufftext at connection time, any mid-game serverinfo
// updates, and the parsed match-settings table that KTX renders into the
// countdown centerprint.
type MetadataResult struct {
	// ServerInfo is the union of `\key\value\…` pairs from the initial
	// fullserverinfo stufftext plus every per-key svc_serverinfo update
	// that arrived later in the demo. Last-write-wins for keys that get
	// overwritten (e.g. `status` cycles through Countdown → "3 min left"
	// → "2 min left" → ... → Standby).
	ServerInfo map[string]string `json:"serverInfo,omitempty"`

	// MatchSettings is the parsed view of KTX's countdown centerprint —
	// the most reliable source of match-level cvars.
	MatchSettings *MatchSettings `json:"matchSettings,omitempty"`

	// CountdownText is the raw, color-stripped multi-line text of the
	// last countdown centerprint we observed before the match started.
	CountdownText string `json:"countdownText,omitempty"`

	// FinalScores is KTX's `//finalscores` end-of-match stuffcmd, verbatim
	// (schema v72). Absent on the 36% of the archive whose server never sent
	// it, and on a demo whose copy was garbled (the parser drops a line that
	// fails the shape check rather than guessing at its fields).
	FinalScores *FinalScores `json:"finalScores,omitempty"`
}

// FinalScores is the server's own end-of-match scoreline, as stuffed by KTX's
// `//finalscores` directive (ktx/src/commands.c:6963-6977). Schema v72.
//
// It is reported VERBATIM — no normalisation, no cross-checking against the
// derived scoreboard — because on the pre-ktxstats half of the archive it is
// the only place the wire states a final result at all, and a consumer that
// wants to compare it with Match.Teams needs both sides untouched. Where the
// pipeline does consume it (Match.Map / Match.Mode / Match.Teams fallbacks) the
// use is named in MatchResult.Sources; it never displaces a demoinfo value.
type FinalScores struct {
	// Date is the server's LOCAL wall clock at match end, strftime
	// "%b %d, %H:%M" — no year, no seconds, no timezone ("Sep 29, 21:27").
	// Streams.Global.DateMarkers carries the resolved form: the wall-clock
	// node completes the year from another marker (`yearFrom`) and reports
	// the stamp unresolved, with unixMs 0, when there was none.
	Date string `json:"date,omitempty"`
	// Mode is KTX's lastscores mode name: "duel", "team", "FFA", "CTF",
	// "RA", "Clan Arena", "Wipeout", "HoonyMode", "race", "unknown" (forks
	// add their own — "Extinction" appears in the archive). Same family as
	// the demoinfo `mode` string but not the same spelling ("FFA" vs "ffa",
	// "Clan Arena" vs "clan-arena"); neither is rewritten to match the other.
	Mode string `json:"mode,omitempty"`
	// Map is the canonical short map name, KTX's `mapname`.
	Map string `json:"map,omitempty"`
	// Team1 / Score1 and Team2 / Score2 are the two sides as the server
	// scored them. On a duel the "team" is the player's own name — the same
	// player-as-team layout Match.Teams uses. Scores can be negative
	// (suicides), and an empty team name is observed.
	//
	// WHAT THE SCORE COUNTS IS MODE-DEPENDENT, and this is the field's one
	// real trap. On duel / team / FFA / CTF it is frags — get_scores1/2 sums
	// the sides' `frags` (ktx/src/g_utils.c:1868-1919). On Clan Arena and
	// Wipeout it is ROUNDS WON — lastscore_add switches to CA_get_score_1/2
	// (ktx/src/commands.c:6867-6886) — so a Wipeout demo reports "5" against a
	// scoreboard showing 241 frags, and the two are both right. Read Mode
	// before comparing this with Match.Teams.
	//
	// Even on a frag-scored mode the two can differ by one: KTX counts the
	// frag that ENDS the match, while the scoreboard here freezes at the
	// match-end latch and the svc_updatefrags for that last kill lands on or
	// after it (observed on 3 of 120 archive demos; the frag LOG has the kill
	// either way — Match.Players[].Kills shows it).
	Team1  string `json:"team1,omitempty"`
	Score1 int    `json:"score1"`
	Team2  string `json:"team2,omitempty"`
	Score2 int    `json:"score2"`
}

// MatchSettings is the structured view of the settings KTX publishes at
// match start: the countdown centerprint table, plus the extra rows
// ShowMatchSettings broadcasts as level-2 prints beside it. All fields are
// optional — only those that appeared for this particular demo are
// populated.
//
// Sources: ktx/src/match.c PrintCountdown() — search for `strlcat(text, ...)`
// to see the centerprint format strings — and ShowMatchSettings()
// (match.c:2077-2141) for the broadcast rows.
type MatchSettings struct {
	Mode       string `json:"mode,omitempty"`       // "Duel" / "Team" / "FFA" / "LGC" / "CA" / "CTF" / etc.
	Deathmatch int    `json:"deathmatch,omitempty"` // 0..5
	Teamplay   int    `json:"teamplay,omitempty"`   // QW teamplay setting
	Timelimit  int    `json:"timelimit,omitempty"`  // minutes
	Fraglimit  int    `json:"fraglimit,omitempty"`
	Spawnmodel string `json:"spawnmodel,omitempty"` // "QW" / "KTS" / "KT" / "KTX" / "KT2" — see respawn_model_name_short
	SpawnK     *int   `json:"spawnK,omitempty"`     // numeric k_spw value (0..4) decoded from Spawnmodel
	Antilag    int    `json:"antilag,omitempty"`    // 0/1/2
	Overtime   string `json:"overtime,omitempty"`   // "5" minutes, or "sd" for sudden death
	Powerups   string `json:"powerups,omitempty"`   // "on" / "off" / "QPRS"
	Dmgfrags   bool   `json:"dmgfrags,omitempty"`
	NoItems    bool   `json:"noItems,omitempty"`
	Midair     bool   `json:"midair,omitempty"`
	Instagib   bool   `json:"instagib,omitempty"`
	Yawnmode   bool   `json:"yawnmode,omitempty"`
	Airstep    bool   `json:"airstep,omitempty"`
	VWep       bool   `json:"vwep,omitempty"`
	Noweapon   string `json:"noweapon,omitempty"` // disabled weapons, e.g. "gl" or "gl axe"
	Matchtag   string `json:"matchtag,omitempty"` // tournament/event tag, e.g. "qwsldraft"
	// Fairpacks is the k_frp ruleset for what a dropped backpack contains:
	// "best weapon" (k_frp 1) or "last weapon fired" (k_frp 2). Empty means
	// the default, k_frp 0 — "the weapon the victim was holding" — because
	// KTX broadcasts the row ONLY when the setting is non-default
	// (ShowMatchSettings, ktx/src/match.c:2086-2107). Absence is therefore
	// informative here, not unknown.
	Fairpacks string `json:"fairpacks,omitempty"`
	SOCDv2    string `json:"socdv2,omitempty"` // "stats" / "warn" / "block"
}
