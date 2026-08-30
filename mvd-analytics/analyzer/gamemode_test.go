package analyzer

import (
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// One resolver, one precedence. Each case names the source that should win
// and the verdict it should produce; the table is the executable form of the
// precedence table in gamemode.go's header comment.
func TestResolveGameMode_Precedence(t *testing.T) {
	ktxDuel := &DemoInfoResult{Version: 3, Mode: "duel", Players: []DemoInfoPlayer{{Name: "a"}, {Name: "b"}}}
	ktxTeam := &DemoInfoResult{Version: 3, Mode: "team", Teamplay: 2, Players: []DemoInfoPlayer{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}}

	// A two-participant roster, the shape a duel verdict is inferred from
	// when nothing else names a mode.
	duelRoster := &Roster{isDuel: true, demoDecided: true,
		participants: map[string]struct{}{"a": {}, "b": {}}, order: []string{"a", "b"}}

	cases := []struct {
		name       string
		di         *DemoInfoResult
		fs         *FinalScores
		ms         *MatchSettings
		si         map[string]string
		roster     *Roster
		counts     map[string]int
		canonical  string
		srcCanon   string
		teamBased  bool
		srcTeam    string
		individual bool
	}{
		{
			name: "demoinfo wins over serverinfo", di: ktxTeam,
			si:        map[string]string{"mode": "ffa", "teamplay": "0"},
			canonical: result.GameModeTeam, srcCanon: result.GameModeSrcKTX,
			teamBased: true, srcTeam: result.GameModeSrcKTX, individual: false,
		},
		{
			name: "demoinfo duel is individual whatever the cvar says", di: ktxDuel,
			si:        map[string]string{"teamplay": "2"},
			canonical: result.GameModeDuel, srcCanon: result.GameModeSrcKTX,
			teamBased: false, srcTeam: result.GameModeSrcMode, individual: true,
		},
		{
			name: "serverinfo umode when no demoinfo",
			si:   map[string]string{"mode": "ffa", "deathmatch": "3"},
			// KTX forces teamplay 0 in FFA (world.c:1652-1655) and the key is
			// simply absent on these servers.
			canonical: result.GameModeFFA, srcCanon: result.GameModeSrcServerInfo,
			teamBased: false, srcTeam: result.GameModeSrcMode, individual: true,
		},
		{
			name:      "serverinfo composite umode is the base mode",
			si:        map[string]string{"mode": "4on4-midair", "teamplay": "2"},
			canonical: result.GameModeTeam, srcCanon: result.GameModeSrcServerInfo,
			teamBased: true, srcTeam: result.GameModeSrcServerInfo, individual: false,
		},
		{
			name:      "countdown when serverinfo names no mode",
			ms:        &MatchSettings{Mode: "Team", Teamplay: 2},
			si:        map[string]string{"teamplay": "2"},
			canonical: result.GameModeTeam, srcCanon: result.GameModeSrcCountdown,
			teamBased: true, srcTeam: result.GameModeSrcServerInfo, individual: false,
		},
		{
			name:      "finalscores when nothing else names a mode",
			fs:        &FinalScores{Mode: "FFA"},
			canonical: result.GameModeFFA, srcCanon: result.GameModeSrcFinalScores,
			teamBased: false, srcTeam: result.GameModeSrcMode, individual: true,
		},
		{
			// The countdown states the settings the match STARTED under; the
			// serverinfo `mode` key states the last usermode command that ran
			// (world.c:1482 over commands.c:4848), which a plain teamplay
			// change leaves stale. Shape from archive b95c35735c4d…:
			// serverinfo mode=1on1, countdown Mode=Team, tp 2.
			name:      "countdown outranks the serverinfo umode",
			ms:        &MatchSettings{Mode: "Team", Teamplay: 2},
			si:        map[string]string{"mode": "1on1"},
			canonical: result.GameModeTeam, srcCanon: result.GameModeSrcCountdown,
			teamBased: true, srcTeam: result.GameModeSrcCountdown, individual: false,
		},
		{
			// A roster-inferred duel may NOT veto a teamplay ruleset that
			// was demonstrably in force. Shape from archive 2a2ed2e9ca…:
			// *gamedir=ctf, teamplay=419, Chipie [red] vs Pain [blue], no
			// demoinfo block and no countdown.
			name:   "roster duel does not veto a teamplay cvar",
			roster: duelRoster,
			si:     map[string]string{"teamplay": "419", "*gamedir": "ctf"},
			counts: map[string]int{"red": 1, "blue": 1},
			// The LAYOUT is still individual — that is Roster.Duel(), not
			// this field: individualLayoutFromMode refuses a roster verdict.
			canonical: result.GameModeDuel, srcCanon: result.GameModeSrcRoster,
			teamBased: true, srcTeam: result.GameModeSrcServerInfo, individual: false,
		},
		{
			name:   "roster duel with no cvar has no teams",
			roster: duelRoster,
			// Both duellists happen to have picked the same colour tag; the
			// tag census would call that a team.
			counts:    map[string]int{"red": 2},
			canonical: result.GameModeDuel, srcCanon: result.GameModeSrcRoster,
			teamBased: false, srcTeam: result.GameModeSrcRoster, individual: false,
		},
		{
			// A demoinfo block that named no mode does not get to have its
			// silence about `tp` read as "teamplay 0" — the mode some OTHER
			// source named says nothing about what this build writes.
			name:      "demoinfo with no mode defers to the teamplay cvar",
			di:        &DemoInfoResult{Version: 1, Players: []DemoInfoPlayer{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}},
			ms:        &MatchSettings{Mode: "Team"},
			si:        map[string]string{"teamplay": "2"},
			canonical: result.GameModeTeam, srcCanon: result.GameModeSrcCountdown,
			teamBased: true, srcTeam: result.GameModeSrcServerInfo, individual: false,
		},
		{
			name:      "roster shape is the last resort and does NOT drive the layout",
			counts:    map[string]int{"red": 4, "blue": 4},
			canonical: result.GameModeUnknown, srcCanon: "",
			teamBased: true, srcTeam: result.GameModeSrcRoster, individual: false,
		},
		{
			name:      "no signal at all",
			canonical: result.GameModeUnknown, srcCanon: "",
			teamBased: false, srcTeam: result.GameModeSrcRoster, individual: false,
		},
		{
			// KTX's `dm` / `tp` are the only keys an old block may omit
			// wholesale; a block with neither a mode nor tp says nothing, and
			// reading its silence as "teamplay 0" would strip the sides off a
			// real team game.
			name:      "demoinfo with no mode and no tp decides nothing",
			di:        &DemoInfoResult{Version: 1, Players: []DemoInfoPlayer{{Name: "a", Team: "red"}, {Name: "b", Team: "red"}, {Name: "c", Team: "blue"}}},
			counts:    map[string]int{"red": 2, "blue": 1},
			canonical: result.GameModeUnknown, srcCanon: "",
			teamBased: true, srcTeam: result.GameModeSrcRoster, individual: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gm := resolveGameMode(c.di, c.fs, c.ms, c.si, c.roster, c.counts)
			if gm.Canonical != c.canonical {
				t.Errorf("canonical = %q, want %q", gm.Canonical, c.canonical)
			}
			if gm.Sources.Canonical != c.srcCanon {
				t.Errorf("sources.canonical = %q, want %q", gm.Sources.Canonical, c.srcCanon)
			}
			if gm.TeamBased != c.teamBased {
				t.Errorf("teamBased = %v, want %v", gm.TeamBased, c.teamBased)
			}
			if gm.Sources.TeamBased != c.srcTeam {
				t.Errorf("sources.teamBased = %q, want %q", gm.Sources.TeamBased, c.srcTeam)
			}
			if got := individualLayoutFromMode(&gm); got != c.individual {
				t.Errorf("individualLayoutFromMode = %v, want %v", got, c.individual)
			}
		})
	}
}

// The FFA case the whole work item exists for: teamplay is 0 and the players
// still wear clan tags, three of them the same one.
func TestResolveGameMode_FFATagsAreNotTeams(t *testing.T) {
	gm := resolveGameMode(nil, &FinalScores{Mode: "FFA"}, nil,
		map[string]string{"mode": "ffa", "deathmatch": "3"}, nil,
		map[string]int{"red": 3, "'tro": 1, "rr": 1})
	if gm.TeamBased {
		t.Errorf("FFA with three players on one tag must not be team-based: %+v", gm)
	}
	if !individualLayoutFromMode(&gm) {
		t.Errorf("FFA must take the individual layout: %+v", gm)
	}
}

func TestResolveGameMode_Submodes(t *testing.T) {
	gm := resolveGameMode(nil, nil, &MatchSettings{Instagib: true},
		map[string]string{"mode": "4on4-midair-df", "k_bloodfest": "1", "teamplay": "2"}, nil, nil)
	want := []string{"bf", "df", "instagib", "midair"}
	if !reflect.DeepEqual(gm.Submodes, want) {
		t.Errorf("submodes = %v, want %v", gm.Submodes, want)
	}
	if gm.Sources.Submodes != result.GameModeSrcServerInfo {
		t.Errorf("sources.submodes = %q, want serverinfo", gm.Sources.Submodes)
	}
	// The composite key's UMODE is still the shape; a submode never becomes
	// the canonical mode.
	if gm.Canonical != result.GameModeTeam {
		t.Errorf("canonical = %q, want team", gm.Canonical)
	}
}

func TestResolveGameMode_Rounds(t *testing.T) {
	for _, c := range []struct {
		mode   string
		rounds bool
	}{{"ca", true}, {"wipeout", true}, {"4on4", false}, {"ffa", false}, {"hoonymode", true}} {
		gm := resolveGameMode(nil, nil, nil, map[string]string{"mode": c.mode}, nil, nil)
		if gm.Rounds != c.rounds {
			t.Errorf("umode %q: rounds = %v, want %v (canonical %q)", c.mode, gm.Rounds, c.rounds, gm.Canonical)
		}
	}
}

func TestParseServerinfoMode(t *testing.T) {
	cases := []struct {
		in    string
		umode string
		subs  []string
	}{
		{"", "", nil},
		{"4on4", "4on4", nil},
		{"4on4-midair", "4on4", []string{"midair"}},
		{"wipeout-wo-df", "wipeout", []string{"wo", "df"}},
	}
	for _, c := range cases {
		got := result.ParseServerinfoMode(c.in)
		if got.Umode != c.umode || !reflect.DeepEqual(got.Submodes, c.subs) {
			t.Errorf("ParseServerinfoMode(%q) = %+v, want umode=%q subs=%v", c.in, got, c.umode, c.subs)
		}
	}
}

// TestCanonicalTables pins each vocabulary table to the strings its KTX
// producer actually writes, and pins the fall-throughs: a name that is not
// KTX's is not guessed at, it yields "" and the resolver moves to the next
// source. The producers are read out of the vendored source by
// TestKTXVocabularyDrift; this is the offline half.
func TestCanonicalTables(t *testing.T) {
	t.Run("countdown", func(t *testing.T) {
		// PrintCountdown's literals (ktx/src/match.c:1511-1571), as they
		// arrive after metadata.go has stripped the spaces, plus the raw
		// spaced spellings and the archive's foreign row.
		for in, want := range map[string]string{
			"Duel": result.GameModeDuel, "D u e l": result.GameModeDuel,
			"Team": result.GameModeTeam, "T e a m": result.GameModeTeam,
			"FFA": result.GameModeFFA, "Ffa": result.GameModeFFA,
			"CTF": result.GameModeCTF, "RACE": result.GameModeRace,
			"COOP": result.GameModeCoop, "C O O P": result.GameModeCoop,
			"RA":      result.GameModeRA,
			"Wipeout": result.GameModeWipeout, "Hoony": result.GameModeHoony,
			"BlitzTDM": result.GameModeHoony,
			// One current server prints CA for wipeout too (17 of the
			// archive's 23): the family, not the shape. The umode decides.
			"CA": "",
			// Rulesets, not shapes — submodeSet takes them.
			"LGC": "", "BLOODFST": "",
			"Unknown": "", "": "",
			// A KTX fork's row (87 archive demos): not named, not guessed.
			"Extinction": "",
			// um_list displaynames, which PrintCountdown never writes.
			"1on1": "", "4on4": "", "Clan Arena": "", "HoonyMode": "",
			"Blitz (2v2)": "", "Tribe of Tjernobyl": "",
		} {
			if got := canonicalFromCountdown(in); got != want {
				t.Errorf("canonicalFromCountdown(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("umode", func(t *testing.T) {
		// All seventeen um_list rows (ktx/src/commands.c:4537-4553), so
		// the offline run pins the whole table.
		for in, want := range map[string]string{
			"1on1": result.GameModeDuel,
			"2on2": result.GameModeTeam, "3on3": result.GameModeTeam, "4on4": result.GameModeTeam,
			"10on10": result.GameModeTeam, "2on2on2": result.GameModeTeam,
			"3on3on3": result.GameModeTeam, "4on4on4": result.GameModeTeam, "XonX": result.GameModeTeam,
			"ffa": result.GameModeFFA, "tot": result.GameModeFFA,
			"ctf":       result.GameModeCTF,
			"hoonymode": result.GameModeHoony, "blitz2v2": result.GameModeHoony, "blitz4v4": result.GameModeHoony,
			"wipeout": result.GameModeWipeout, "ca": result.GameModeCA,
			// A suffix only (world.c:1487-1490): never a base token.
			"race": "",
			// The archive's three foreign base tokens.
			"1": "", "extinction": "", "smashpacktdm": "",
		} {
			if got := canonicalFromUmode(in); got != want {
				t.Errorf("canonicalFromUmode(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("ktx mode", func(t *testing.T) {
		for in, want := range map[string]string{
			// GetMode (stats.c:309-354) and lastscores2str
			// (commands.c:6755-6790), every literal.
			"duel": result.GameModeDuel, "team": result.GameModeTeam,
			"ffa": result.GameModeFFA, "FFA": result.GameModeFFA,
			"ctf": result.GameModeCTF, "CTF": result.GameModeCTF,
			"clan-arena": result.GameModeCA, "Clan Arena": result.GameModeCA,
			"Wipeout": result.GameModeWipeout, "race": result.GameModeRace,
			"hoonymode": result.GameModeHoony, "HoonyMode": result.GameModeHoony,
			"rocket-arena": result.GameModeRA, "RA": result.GameModeRA,
			"instagib": "", "midair": "", "unknown": "",
			// Aliases neither function writes (0 of 50 964 demos).
			"1on1": "", "ca": "", "wo": "", "hoony": "", "blitz": "",
			"rocket arena": "", "coop": "",
		} {
			if got := canonicalFromKTXMode(in); got != want {
				t.Errorf("canonicalFromKTXMode(%q) = %q, want %q", in, got, want)
			}
		}
	})
}

// Archive 144c5e29…: ktx 1.47-dev (QHLAN:28550), countdown Mode "CA",
// serverinfo `wipeout-wo-df`, //finalscores "Wipeout". The countdown row is
// the family on that server's build; the umode is the shape.
func TestResolveGameMode_CountdownCAIsNotAShape(t *testing.T) {
	gm := resolveGameMode(nil, &FinalScores{Mode: "Wipeout"}, &MatchSettings{Mode: "CA", Teamplay: 2},
		map[string]string{"mode": "wipeout-wo-df", "teamplay": "2"}, nil, nil)
	if gm.Canonical != result.GameModeWipeout || gm.Sources.Canonical != result.GameModeSrcServerInfo {
		t.Errorf("canonical = %q from %q, want wipeout from serverinfo", gm.Canonical, gm.Sources.Canonical)
	}
	gm = resolveGameMode(nil, nil, &MatchSettings{Mode: "CA", Teamplay: 2},
		map[string]string{"mode": "ca-ca-df", "teamplay": "2"}, nil, nil)
	if gm.Canonical != result.GameModeCA || gm.Sources.Canonical != result.GameModeSrcServerInfo {
		t.Errorf("canonical = %q from %q, want ca from serverinfo", gm.Canonical, gm.Sources.Canonical)
	}
	// The cost of not trusting the row: with no serverinfo mode the shape
	// comes from //finalscores (unobserved in the archive — all 23 carry
	// the umode), and from nothing at all if that is absent too.
	gm = resolveGameMode(nil, &FinalScores{Mode: "Clan Arena"}, &MatchSettings{Mode: "CA", Teamplay: 2}, nil, nil, nil)
	if gm.Canonical != result.GameModeCA || gm.Sources.Canonical != result.GameModeSrcFinalScores {
		t.Errorf("canonical = %q from %q, want ca from finalscores", gm.Canonical, gm.Sources.Canonical)
	}
	gm = resolveGameMode(nil, nil, &MatchSettings{Mode: "CA", Teamplay: 2}, nil, nil, nil)
	if gm.Canonical != result.GameModeUnknown {
		t.Errorf("canonical = %q, want unknown when nothing but the CA row names a mode", gm.Canonical)
	}
}

// A blitz (team hoony) demo with no demoinfo block: the countdown's
// BlitzTDM row is hoony — the shape GetMode and the lastscores round
// accounting give it — and the Teamplay row makes it a team game.
func TestResolveGameMode_BlitzIsRoundScoredHoony(t *testing.T) {
	gm := resolveGameMode(nil, nil, &MatchSettings{Mode: "BlitzTDM", Teamplay: 2}, nil, nil, nil)
	if gm.Canonical != result.GameModeHoony || gm.Sources.Canonical != result.GameModeSrcCountdown {
		t.Errorf("canonical = %q from %q, want hoony from countdown", gm.Canonical, gm.Sources.Canonical)
	}
	if !gm.TeamBased || gm.Sources.TeamBased != result.GameModeSrcCountdown {
		t.Errorf("teamBased = %v from %q, want true from countdown", gm.TeamBased, gm.Sources.TeamBased)
	}
	if !gm.Rounds {
		t.Error("rounds = false, want true")
	}
	if individualLayoutFromMode(&gm) {
		t.Error("a blitz demo laid out individually")
	}
}

// A countdown whose Mode row names a ruleset gives the shape to the next
// source and the ruleset to submodes — for both such literals.
func TestResolveGameMode_CountdownRulesetRows(t *testing.T) {
	for _, c := range []struct{ row, sub string }{{"LGC", "lgc"}, {"BLOODFST", "bf"}} {
		gm := resolveGameMode(nil, nil, &MatchSettings{Mode: c.row}, map[string]string{"mode": "1on1"}, nil, nil)
		if gm.Canonical != result.GameModeDuel || gm.Sources.Canonical != result.GameModeSrcServerInfo {
			t.Errorf("%s: canonical = %q from %q, want duel from serverinfo", c.row, gm.Canonical, gm.Sources.Canonical)
		}
		if !reflect.DeepEqual(gm.Submodes, []string{c.sub}) || gm.Sources.Submodes != result.GameModeSrcCountdown {
			t.Errorf("%s: submodes = %v from %q, want [%s] from countdown", c.row, gm.Submodes, gm.Sources.Submodes, c.sub)
		}
	}
}
