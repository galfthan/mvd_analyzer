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
	MapTitle string       `json:"mapTitle,omitempty"`
	GameDir  string       `json:"gameDir"`
	Duration int32        `json:"duration"` // ms
	Players  []PlayerStat `json:"players"`
	Teams    []TeamStat   `json:"teams,omitempty"`
}

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
