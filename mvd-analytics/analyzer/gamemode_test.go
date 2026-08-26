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

	cases := []struct {
		name       string
		di         *DemoInfoResult
		fs         *FinalScores
		ms         *MatchSettings
		si         map[string]string
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
			gm := resolveGameMode(c.di, c.fs, c.ms, c.si, nil, c.counts)
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
