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

	// Unpaired holds the teamkill obituaries that name only ONE party and
	// whose other party the recovery could not identify. Every entry in
	// Frags names both sides — that is what makes it usable for per-player
	// tallies — so these cannot live there; but the obituary IS on the wire
	// and dropping it silently lost a real death from the log. Exactly one
	// of Killer / Victim is the placeholder "teammate": killer-named forms
	// ("X loses another friend") whose coincident DeathEvent could not be
	// matched, and victim-named ones ("X was telefragged by his teammate")
	// whose killer neither co-location nor the frag penalty resolved.
	//
	// Consumers must NOT fold these into per-player counts (the placeholder
	// is not a player). They are here so that a consumer can see the death
	// happened and read its CAUSE: the victim-named forms carry the real
	// weapon (tele / stomp — KTX prints one phrasing per deathtype), which
	// is what lets the damage reconstruction type such a kill positionally
	// instead of pricing the victim's whole corpse drop as team damage.
	// Schema v75.
	Unpaired []FragEntry `json:"unpaired,omitempty"`
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
