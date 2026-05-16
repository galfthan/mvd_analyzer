package result

// MatchResult contains match summary information. Time fields are
// integer milliseconds (schema v8).
type MatchResult struct {
	Map       string       `json:"map"`
	GameDir   string       `json:"gameDir"`
	Duration  int32        `json:"duration"` // ms
	StartTime int32        `json:"startTime,omitempty"`
	EndTime   int32        `json:"endTime,omitempty"`
	Players   []PlayerStat `json:"players"`
	Teams     []TeamStat   `json:"teams,omitempty"`
}

// PlayerStat represents a player's final statistics. Per-player kill
// and death counts live in FragResult.ByPlayer (and DemoInfoResult for
// KTX demos); MatchResult is the lightweight non-KTX-fallback view and
// only carries the canonical QW frag tally.
type PlayerStat struct {
	Name  string `json:"name"`
	Team  string `json:"team"`
	Frags int    `json:"frags"`
}

// TeamStat represents a team's statistics.
type TeamStat struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
}
