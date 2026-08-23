package result

// MatchResult contains match summary information. Time fields are
// integer milliseconds (schema v8). The match window itself lives in
// streams.global (matchStart/matchEnd); read Duration for "how long was
// the match".
type MatchResult struct {
	// Map is the canonical SHORT map identifier — "e1m2", "dm2",
	// "aerowalk". It is THE map identity across the whole project: the
	// join key against searchGames rows and the serverinfo `map` key, and
	// the basename every BSP / loc / geometry lookup is named by. Resolved
	// exactly like EffectiveMap (demoinfo map, else serverinfo `map`); see
	// analyzer/match.go Finalize for the degraded last resort.
	Map string `json:"map"`
	// MapTitle is the level TITLE the server announced in svc_serverdata
	// ("Castle of the Damned" on e1m2, "Claustrophobopolis" on dm2),
	// cleaned of the `.bsp` suffix and trailing ` by <author>` hints.
	// DISPLAY ONLY — never a file key, a join key, or any other
	// identifier: it is free-form text a mapper chose, it is not unique,
	// and on many demos it is absent. Omitted when svc_serverdata named no
	// level.
	MapTitle string `json:"mapTitle,omitempty"`
	// Mode is KTX's own game-mode name (schema v72): "duel", "team", "FFA",
	// "CTF", "RA"/"rocket-arena", "Clan Arena"/"clan-arena", "Wipeout",
	// "HoonyMode"/"hoonymode", "race", "midair", "instagib". The two
	// spellings per mode are not an accident: the value is verbatim from
	// whichever source named it (demoinfo's GetMode, ktx/src/stats.c:309, or
	// the `//finalscores` lastscores2str name, commands.c:6755), and
	// Sources.Mode says which — coercing them into one vocabulary would
	// invent a value neither server sent. Empty when the demo carries
	// neither source.
	//
	// This is NOT the serverinfo `mode` key ("1on1", "4on4-midair"), which
	// is a third vocabulary and stays available verbatim under
	// metadata.serverInfo, nor the countdown table's display spelling
	// ("Duel"), which stays under metadata.matchSettings.mode.
	Mode     string       `json:"mode,omitempty"`
	GameDir  string       `json:"gameDir"`
	Duration int32        `json:"duration"` // ms
	Players  []PlayerStat `json:"players"`
	Teams    []TeamStat   `json:"teams,omitempty"`
	// Sources names where the resolved identity fields above came from, so a
	// consumer can tell a server-authoritative value from a pipeline-derived
	// one without re-deriving the precedence. Schema v72.
	Sources MatchSources `json:"sources"`
}

// MatchSources is MatchResult's per-field provenance roll-up (schema v72),
// the same idea as PlayerStatsResult.Sources. Every value is a source NAME,
// never a quality grade.
type MatchSources struct {
	// Map: "ktx" (the demoinfo block's map), "serverinfo" (the `map` cvar),
	// "finalscores" (KTX's end-of-match stuffcmd), or "levelTitle" (the
	// degraded last resort — the svc_serverdata level title, which on a
	// titled map is not a shortname at all). Empty when no source named a map.
	Map string `json:"map,omitempty"`
	// Mode: "ktx" (demoinfo) or "finalscores". Empty with Mode.
	Mode string `json:"mode,omitempty"`
	// Teams: "derived" — the rows are the per-player scoreboard summed by
	// team, the normal case — or "finalscores", meaning the scoreboard
	// produced no team rows at all and the two sides come from KTX's
	// end-of-match stuffcmd instead. Empty when there are no team rows.
	Teams string `json:"teams,omitempty"`
}

// Provenance values for MatchSources.
const (
	MatchSrcKTX         = "ktx"
	MatchSrcServerInfo  = "serverinfo"
	MatchSrcFinalScores = "finalscores"
	MatchSrcLevelTitle  = "levelTitle"
	MatchSrcDerived     = "derived"
)

// PlayerStat is a player's final scoreboard line. Frags is the canonical
// QW net score (from the svc_updatefrags scoreboard, frozen at match end).
// Kills, Deaths and Suicides are the corrected counts from the frag log —
// they fix the KTX demoinfo stats, which credit several self / positional
// deaths to the wrong entity: a pentagram-deflect telefrag (dtTELE2) inflates
// the deflector's kills, and a world-dealt suicide (fall / lava / squish /
// drown) bumps the world entity's counter instead of the victim's
// (ktx/src/client.c:4951), so demoinfo undercounts suicides. Kills/Deaths
// come from FragResult.ByPlayer; Suicides is the per-victim count of
// IsSuicide frag entries. All 0 when the demo carried no frag log;
// per-weapon kills stay in FragResult.ByPlayer.ByWeapon.
type PlayerStat struct {
	Name     string `json:"name"`
	Team     string `json:"team"`
	Frags    int    `json:"frags"`
	Kills    int    `json:"kills"`
	Deaths   int    `json:"deaths"`
	Suicides int    `json:"suicides"`
}

// TeamStat represents a team's statistics.
type TeamStat struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
}
