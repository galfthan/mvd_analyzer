package result

// FragResult contains frag analysis results.
type FragResult struct {
	TotalFrags int                     `json:"totalFrags"`
	Frags      []FragEntry             `json:"frags"`
	ByWeapon   map[string]int          `json:"byWeapon"`
	ByPlayer   map[string]*PlayerFrags `json:"byPlayer"`

	// KillsMeasured is whether kill ATTRIBUTION was observable on this demo
	// at all — the demo-global verdict, decided once by the analyzer
	// (analyzer.killsMeasurable) and stored here so that every consumer reads
	// one answer instead of re-deriving the rule.
	//
	// False means the log is EMPTY on a demo where the scoreboard shows
	// deaths: every obituary went unmatched, because the server printed them
	// in a form this pipeline does not parse. Deaths still count — they come
	// from the protocol death events, not the obituaries — and that is
	// precisely what makes the zeros dangerous, since a row reading 0 kills /
	// 92 deaths looks measured. Consumers must present kills, teamkills,
	// suicides, efficiency and the per-weapon split as UNMEASURED rather than
	// zero when this is false (result.PlayerStatsScore omits them;
	// view.MeasuredSources.Frags reports it).
	//
	// True on a demo that simply has no deaths to contradict — there the
	// zeros are honest measurements. Schema v65.
	KillsMeasured bool `json:"killsMeasured"`
}

// FragEntry represents a single frag event. Time is match-relative
// milliseconds (schema v8).
type FragEntry struct {
	Time       int32  `json:"time"`
	Killer     string `json:"killer"`
	Victim     string `json:"victim"`
	Weapon     string `json:"weapon"`
	IsSuicide  bool   `json:"isSuicide,omitempty"`
	IsTeamKill bool   `json:"isTeamKill,omitempty"`
}

// PlayerFrags holds per-player frag statistics.
type PlayerFrags struct {
	Kills     int            `json:"kills"`
	Deaths    int            `json:"deaths"`
	TeamKills int            `json:"teamkills,omitempty"` // Teammates this player killed (KTX "tk"). Killer-named obituaries only; see frag analyzer.
	ByWeapon  map[string]int `json:"byWeapon"`
}
