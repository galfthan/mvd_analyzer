package analyzer

import (
	"reflect"
	"testing"
)

// TestRoster_DuelDetection ports the old isDuelResult cases onto the roster
// seams that replaced it: newRoster is the demoinfo authority, and
// noteMatchParticipants is the no-demoinfo match-players fallback that
// MatchAnalyzer drives. matchNames simulates the fallback that would run only
// when demoinfo carried no players.
func TestRoster_DuelDetection(t *testing.T) {
	cases := []struct {
		name       string
		di         *DemoInfoResult
		matchNames []string // participant count MatchAnalyzer would feed the fallback
		want       bool
	}{
		{
			name: "two demoinfo players",
			di:   &DemoInfoResult{Players: []DemoInfoPlayer{{Name: "a"}, {Name: "b"}}},
			want: true,
		},
		{
			name:       "four demoinfo players (match count ignored)",
			di:         &DemoInfoResult{Players: []DemoInfoPlayer{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}},
			matchNames: []string{"a", "b"}, // demoinfo decided; fallback must be vetoed
			want:       false,
		},
		{
			name:       "no demoinfo, two match players",
			di:         nil,
			matchNames: []string{"a", "b"},
			want:       true,
		},
		{
			name: "no demoinfo, no match",
			di:   nil,
			want: false,
		},
		{
			name: "one demoinfo player",
			di:   &DemoInfoResult{Players: []DemoInfoPlayer{{Name: "solo"}}},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRoster(c.di)
			if c.matchNames != nil {
				r.noteMatchParticipants(c.matchNames)
			}
			if got := r.Duel(); got != c.want {
				t.Errorf("Duel() = %v, want %v", got, c.want)
			}
		})
	}
}

// The DemoInfo team rewrite moved from normalizeDuelTeams into
// RosterAnalyzer.PopulateCore (the analyzer that owns DemoInfo). It must
// stamp the synthetic one-player-per-team layout on a 1v1's DemoInfo.
func TestRoster_DemoInfoRewrite(t *testing.T) {
	di := &DemoInfoResult{
		Teams: []string{"green", "kis"},
		Players: []DemoInfoPlayer{
			{Name: "alice", Team: "green"},
			{Name: "bob", Team: "kis"},
		},
	}
	co := &CoreOutputs{DemoInfo: di}
	(&RosterAnalyzer{}).PopulateCore(co)

	if !co.Roster.Duel() {
		t.Fatalf("two demoinfo players should be a duel")
	}
	if len(di.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(di.Players))
	}
	for _, p := range di.Players {
		if p.Team != p.Name {
			t.Errorf("player %q has team %q, want %q", p.Name, p.Team, p.Name)
		}
	}
	if len(di.Teams) != 2 || di.Teams[0] != "alice" || di.Teams[1] != "bob" {
		t.Errorf("DemoInfo.Teams = %v, want [alice bob]", di.Teams)
	}
}

// The Match.Players participant rebuild moved from normalizeDuelTeams into
// MatchAnalyzer.Finalize (rebuildDuelMatch). Regression: the LGC-vs-bot
// scenario where MatchAnalyzer dropped the bot entirely because its team was
// "" and it had no per-slot frag tracking — the demoinfo-authoritative merge
// must reconstruct both participants.
func TestRebuildDuelMatch_FromDemoInfo(t *testing.T) {
	di := &DemoInfoResult{
		Players: []DemoInfoPlayer{
			{Name: "chr1s", Team: "blue",
				Stats: &DemoInfoStats{Frags: 223, Kills: 150, Deaths: 15}},
			{Name: "/ bro", Team: "",
				Stats: &DemoInfoStats{Frags: 72, Kills: 15, Deaths: 39}},
		},
	}
	mr := &MatchResult{
		// MatchAnalyzer only saw chr1s — bot was filtered out.
		Players: []PlayerStat{
			{Name: "chr1s", Team: "blue", Frags: 223},
		},
		Teams: []TeamStat{{Name: "blue", Frags: 223}},
	}
	rebuildIndividualMatch(mr, di, true)

	if len(mr.Players) != 2 {
		t.Fatalf("match.Players after rebuild: got %d players, want 2", len(mr.Players))
	}
	names := map[string]PlayerStat{}
	for _, p := range mr.Players {
		names[p.Name] = p
	}
	chr1s, ok := names["chr1s"]
	if !ok {
		t.Fatalf("chr1s missing from match.Players")
	}
	if chr1s.Team != "chr1s" || chr1s.Frags != 223 {
		t.Errorf("chr1s = %+v, want team=chr1s frags=223", chr1s)
	}
	bro, ok := names["/ bro"]
	if !ok {
		t.Fatalf("/ bro missing from match.Players — LGC regression")
	}
	if bro.Team != "/ bro" || bro.Frags != 72 {
		t.Errorf("bro = %+v, want team=/ bro frags=72", bro)
	}

	if len(mr.Teams) != 2 {
		t.Errorf("match.Teams has %d teams, want 2: %+v", len(mr.Teams), mr.Teams)
	}
}

// Pickup / stream / message producers stamp raw userinfo teams and then apply
// the roster label via co.TeamFor. This is the seam that replaced the per-
// section normalizeDuelTeams pickup/item/backpack/message rewrites: a tracked
// participant relabels to their own name, a non-participant (spectator, open
// phase) keeps its raw team. Producers whose player key isn't a participant
// name (or is empty) pass through unchanged.
func TestRoster_TeamForRelabelsParticipants(t *testing.T) {
	di := &DemoInfoResult{
		Players: []DemoInfoPlayer{
			{Name: "alice", Team: "green"},
			{Name: "bob", Team: ""},
		},
	}
	r := newRoster(di)
	if !r.Duel() {
		t.Fatalf("two demoinfo players should be a duel")
	}
	cases := []struct {
		name, raw, want string
	}{
		{"alice", "green", "alice"}, // picker/dropper participant → own name
		{"bob", "", "bob"},          // teamless participant → own name
		{"", "", ""},                // open phase (no owner) → untouched
		{"speccer", "obs", "obs"},   // non-participant spectator chat → raw team
	}
	for _, c := range cases {
		if got := r.TeamFor(c.name, c.raw); got != c.want {
			t.Errorf("TeamFor(%q,%q) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}

func TestRoster_NoOpForTeamMatches(t *testing.T) {
	// 4 players → not a duel → roster leaves DemoInfo untouched, and TeamFor
	// passes raw teams through.
	di := &DemoInfoResult{
		Teams: []string{"red", "blue"},
		Players: []DemoInfoPlayer{
			{Name: "a", Team: "red"},
			{Name: "b", Team: "red"},
			{Name: "c", Team: "blue"},
			{Name: "d", Team: "blue"},
		},
	}
	co := &CoreOutputs{DemoInfo: di}
	(&RosterAnalyzer{}).PopulateCore(co)

	if co.Roster.Duel() {
		t.Fatalf("4-player match must not be a duel")
	}
	if di.Teams[0] != "red" || di.Teams[1] != "blue" {
		t.Errorf("team names should not be rewritten for 4-player match: %v", di.Teams)
	}
	for _, p := range di.Players {
		if p.Team == p.Name {
			t.Errorf("player %q team rewritten to name in non-duel match", p.Name)
		}
		if got := co.Roster.TeamFor(p.Name, p.Team); got != p.Team {
			t.Errorf("TeamFor(%q,%q)=%q, want raw team passthrough", p.Name, p.Team, got)
		}
	}
}

func TestMergeFragEventsByTime(t *testing.T) {
	a := []TimelineFragEvent{
		{Time: 1000, Player: "a"},
		{Time: 5000, Player: "a"},
		{Time: 10000, Player: "a"},
	}
	b := []TimelineFragEvent{
		{Time: 3000, Player: "b"},
		{Time: 7000, Player: "b"},
	}
	merged := mergeFragEventsByTime(a, b)
	wantTimes := []int32{1000, 3000, 5000, 7000, 10000}
	if len(merged) != len(wantTimes) {
		t.Fatalf("merged len = %d, want %d", len(merged), len(wantTimes))
	}
	for i, fe := range merged {
		if fe.Time != wantTimes[i] {
			t.Errorf("merged[%d].Time = %v, want %v", i, fe.Time, wantTimes[i])
		}
	}
}

// The individual layout applies to demoInfo too, not just to the duel case
// it started as. demoInfo is where several consumers look FIRST for a
// name→team map — locgraph's byTeam, view.regionControl's fallback, and on
// the frontend the map tab's player symbols, the loc heatmap and the
// timeline's per-team player lists — so leaving the decorative clan tags on
// it while every other section named players left those surfaces keyed on
// labels nothing else used.
//
// Shape from the ffa_countdown_dm6_260106 golden (archive 52c1421d…): a KTX
// block with teams ["", "tsc", "red"] over an FFA.
func TestRosterAnalyzer_IndividualRelabelsDemoInfo(t *testing.T) {
	a := NewRosterAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	co := &CoreOutputs{
		DemoInfo: &DemoInfoResult{
			Version: 3, Mode: "ffa",
			Players: []DemoInfoPlayer{
				{Name: "Polish Rifle", Team: ""},
				{Name: "keith", Team: "tsc"},
				{Name: "rdleifriaf", Team: "red"},
			},
		},
		ServerInfo: map[string]string{"mode": "ffa", "deathmatch": "3"},
	}
	a.PopulateCore(co)

	if !co.IndividualMode() || co.Roster.Duel() {
		t.Fatalf("roster = %+v, want individual and not a duel", co.Roster)
	}
	for _, p := range co.DemoInfo.Players {
		if p.Team != p.Name {
			t.Errorf("demoInfo player %q: team = %q, want its own name", p.Name, p.Team)
		}
	}
	want := []string{"Polish Rifle", "keith", "rdleifriaf"}
	if !reflect.DeepEqual(co.DemoInfo.Teams, want) {
		t.Errorf("demoInfo.teams = %v, want %v", co.DemoInfo.Teams, want)
	}
}

// A team game is untouched: the rewrite is gated on the individual layout,
// and a 4on4's tags name real sides.
func TestRosterAnalyzer_TeamGameKeepsDemoInfoTags(t *testing.T) {
	a := NewRosterAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	co := &CoreOutputs{
		DemoInfo: &DemoInfoResult{
			Version: 3, Mode: "team", Teamplay: 2,
			Players: []DemoInfoPlayer{
				{Name: "a", Team: "red"}, {Name: "b", Team: "red"},
				{Name: "c", Team: "blue"}, {Name: "d", Team: "blue"},
			},
			Teams: []string{"red", "blue"},
		},
		ServerInfo: map[string]string{"mode": "2on2", "teamplay": "2"},
	}
	a.PopulateCore(co)

	if co.IndividualMode() {
		t.Fatalf("roster = %+v, want a team layout", co.Roster)
	}
	if co.DemoInfo.Players[0].Team != "red" || !reflect.DeepEqual(co.DemoInfo.Teams, []string{"red", "blue"}) {
		t.Errorf("demoInfo teams rewritten on a team game: %+v / %v", co.DemoInfo.Players, co.DemoInfo.Teams)
	}
}
