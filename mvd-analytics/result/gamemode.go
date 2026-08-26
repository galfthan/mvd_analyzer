package result

import "strings"

// GameMode is the pipeline's single normalised answer to "what kind of game
// was this" (schema v75). Before it existed the question had five separate
// vocabularies and no translation between them — KTX's demoinfo `mode`
// (`duel`/`team`/`ffa`), the `//finalscores` mode name (`FFA`/`Clan Arena`),
// the countdown centerprint's display spelling (`Duel`/`FFA`/`LGC`), the
// composite serverinfo `mode` key (`4on4-midair`) and the hub's search facet
// — and at least four independent hand-rolled "is this a solo mode" tables
// that disagreed with each other. Everything that needs a mode verdict now
// reads this block; the raw vocabularies stay published verbatim beside it
// (`match.mode`, `metadata.serverInfo.mode`, `metadata.matchSettings.mode`)
// because a consumer comparing them needs both sides untouched.
//
// The resolver is analyzer.resolveGameMode; Sources names which vocabulary
// decided each field, so a consumer can tell a server-authoritative verdict
// from a shape inference without re-deriving the precedence.
type GameMode struct {
	// Canonical is the mode SHAPE, normalised across every vocabulary:
	//
	//	duel     1v1 (KTX `1on1` / gtDuel / demoinfo "duel")
	//	team     NonN teamplay (2on2 ... 10on10, XonX, NonNonN)
	//	ffa      free-for-all — everyone against everyone
	//	ctf      capture the flag
	//	ca       clan arena (round-scored team game)
	//	wipeout  wipeout (round-scored team game)
	//	race     race mode — no combat scoring at all
	//	hoony    HoonyMode / blitz (round-scored)
	//	ra       rocket arena (round-scored, 1v1 within a queue)
	//	coop     cooperative
	//	unknown  no source named a mode
	//
	// Submodes that ride on top of a shape (midair, instagib, lgc,
	// dmgfrags, bloodfest, yawnmode) are NOT canonical values — they are in
	// Submodes, because an instagib game is still an FFA or a 4on4 and a
	// consumer asking "were there teams" must not lose that.
	Canonical string `json:"canonical"`

	// TeamBased is whether teamplay was in force: whether a player's team
	// tag names a SIDE rather than a decoration. It mirrors KTX's own
	// `tp_num()` gate, `(isTeam() || isCTF() || coop) ? teamplay : 0`
	// (ktx/src/g_utils.c:1586-1588) — the mode has to be a team mode AND
	// the teamplay cvar has to be non-zero.
	//
	// False for duel, ffa, race and rocket arena, and for any mode running
	// `teamplay 0`. When it is false the pipeline lays the match out
	// individually: one team per player, `team == name` (see
	// MatchResult.Teams and PlayerStat.RawTeam).
	TeamBased bool `json:"teamBased"`

	// Rounds is whether the score is ROUNDS WON rather than frags — clan
	// arena, wipeout, rocket arena and hoonymode (ktx/src/commands.c:
	// 6867-6886 writes the round count into `//finalscores` on those
	// modes). Published for consumers that compare a scoreline against a
	// frag total; nothing in the pipeline gates on it yet.
	Rounds bool `json:"rounds,omitempty"`

	// Submodes are the ruleset modifiers KTX appends to the serverinfo
	// `mode` key (`umode[-submode...]`, SetMode4ServerInfo,
	// ktx/src/world.c:1475-1543) plus the ones the legacy `k_*` cvars and
	// the countdown table name: midair, instagib, lgc, dmgfrags, race, ra,
	// ca, wo, gm, bloodfest, yawnmode. Sorted, so the JSON is stable.
	// Absent when the demo named none.
	Submodes []string `json:"submodes,omitempty"`

	// Sources names the vocabulary that decided each field.
	Sources GameModeSources `json:"sources"`
}

// GameModeSources is GameMode's per-field provenance, the same idea as
// MatchSources. Every value is a source NAME, never a quality grade.
type GameModeSources struct {
	// Canonical: "ktx" (the demoinfo block's `mode`), "serverinfo" (the
	// composite `mode` key's umode), "countdown" (the KTX countdown
	// centerprint's Mode row), "finalscores" (KTX's end-of-match stuffcmd)
	// or "roster" (the participant shape — a two-player match is a duel).
	// Empty when Canonical is "unknown".
	Canonical string `json:"canonical,omitempty"`
	// TeamBased: "ktx" (the demoinfo block's `tp`), "serverinfo" (the
	// `teamplay` cvar), "countdown" (the countdown Teamplay row), "mode"
	// (the resolved Canonical alone decides — race is never teamplay) or
	// "roster" (the weakest: a team with more than one member). Never
	// empty — the resolver always reaches a verdict.
	TeamBased string `json:"teamBased,omitempty"`
	// Rounds: "mode" — the canonical mode alone decides. Empty when Rounds
	// is false.
	Rounds string `json:"rounds,omitempty"`
	// Submodes: "serverinfo" (the composite `mode` key and/or the legacy
	// k_* cvars) or "countdown". Empty when there are none.
	Submodes string `json:"submodes,omitempty"`
}

// Provenance values for GameModeSources.
const (
	GameModeSrcKTX         = "ktx"
	GameModeSrcServerInfo  = "serverinfo"
	GameModeSrcCountdown   = "countdown"
	GameModeSrcFinalScores = "finalscores"
	GameModeSrcRoster      = "roster"
	GameModeSrcMode        = "mode"
)

// Canonical mode names. Anything not in this list is not a Canonical value.
const (
	GameModeDuel    = "duel"
	GameModeTeam    = "team"
	GameModeFFA     = "ffa"
	GameModeCTF     = "ctf"
	GameModeCA      = "ca"
	GameModeWipeout = "wipeout"
	GameModeRace    = "race"
	GameModeHoony   = "hoony"
	GameModeRA      = "ra"
	GameModeCoop    = "coop"
	GameModeUnknown = "unknown"
)

// CanonicalIsTeamShaped reports whether a canonical mode is one KTX's
// tp_num() gate covers — `isTeam() || isCTF() || coop`
// (ktx/src/g_utils.c:1586-1588). ca and wipeout are team modes: their umode
// init strings set `k_mode 2` (gtTeam) and `teamplay 4` / `teamplay 2`
// (ktx/src/commands.c:4462-4509). "unknown" is NOT team-shaped — a caller
// that has no mode must fall back to something else rather than guess.
//
// It is the shape test alone: whether teamplay was actually ON is
// GameMode.TeamBased, which is this AND a non-zero teamplay cvar.
func CanonicalIsTeamShaped(canonical string) bool {
	switch canonical {
	case GameModeTeam, GameModeCTF, GameModeCoop, GameModeCA, GameModeWipeout:
		return true
	}
	return false
}

// TeamShaped is the nil-safe CanonicalIsTeamShaped read of the descriptor.
func (g *GameMode) TeamShaped() bool {
	return g != nil && CanonicalIsTeamShaped(g.Canonical)
}

// HasSubmode reports whether the named submode token is set.
func (g *GameMode) HasSubmode(name string) bool {
	if g == nil {
		return false
	}
	for _, s := range g.Submodes {
		if s == name {
			return true
		}
	}
	return false
}

// IsTeamBased is the nil-safe read of TeamBased. A nil descriptor (a demo
// analysed by a hand-built registry, or a cached Result from before v75)
// reports false, which is the individual layout — callers that need to tell
// "no descriptor" from "no teams" must check for nil themselves.
func (g *GameMode) IsTeamBased() bool { return g != nil && g.TeamBased }

// ServerinfoMode is the parsed form of KTX's composite serverinfo `mode`
// key: `umode[-submode...]`, built once per level by SetMode4ServerInfo
// (ktx/src/world.c:1475-1543). Umode is the base user mode ("4on4", "ffa",
// "ctf", "wipeout", "ca", "hoonymode", ...) — the um_list name
// (commands.c:4535-4553) — and Submodes are the ruleset modifiers appended
// after it, in the order the server wrote them.
//
// This is the ONE parser for that key. Three call sites used to run their
// own `strings.Split(si["mode"], "-")` with three different token
// selections; the selections differ on purpose (the damage reconstruction
// refuses more modes than the backpack one does) but the split does not,
// and each copy was free to drift on what "the base mode" means.
type ServerinfoMode struct {
	Umode    string
	Submodes []string
}

// ParseServerinfoMode splits the composite serverinfo `mode` value. An empty
// or absent value yields a zero ServerinfoMode; a value with no dash yields
// just the umode.
func ParseServerinfoMode(v string) ServerinfoMode {
	if v == "" {
		return ServerinfoMode{}
	}
	parts := strings.Split(v, "-")
	m := ServerinfoMode{Umode: parts[0]}
	if len(parts) > 1 {
		m.Submodes = parts[1:]
	}
	return m
}

// HasSubmode reports whether the parsed key carried the given submode token.
func (m ServerinfoMode) HasSubmode(tok string) bool {
	for _, s := range m.Submodes {
		if s == tok {
			return true
		}
	}
	return false
}
