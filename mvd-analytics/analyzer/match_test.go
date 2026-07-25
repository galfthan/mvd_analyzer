package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// A reconnect during intermission re-initializes the slot with
// svc_updatefrags 0, which must not clobber the final score — the match
// scoreboard is immutable once the match ends. Observed on hub 212483
// (Doomie: 34 → 0) and 212545 (squeeze: 55 → 0); the pre-phase-1 0-frag
// filter masked the corruption by dropping the player entirely.
func TestMatchAnalyzer_PostMatchFragResetIgnored(t *testing.T) {
	a := NewMatchAnalyzer()
	ctx := &Context{}
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "Doomie", Team: "BhB"}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 5000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 34, TimeMs: 1197000})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match is over\n", TimeMs: 1210000})
	// Post-match reconnect: the server re-inits the slot's scoreboard.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 0, TimeMs: 1213000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if res.Match == nil || len(res.Match.Players) != 1 {
		t.Fatalf("match.players = %+v, want one entry", res.Match)
	}
	if got := res.Match.Players[0].Frags; got != 34 {
		t.Errorf("Frags = %d, want 34 (post-match reset must not clobber the final score)", got)
	}
	if len(res.Match.Teams) != 1 || res.Match.Teams[0].Frags != 34 {
		t.Errorf("Teams = %+v, want BhB with 34", res.Match.Teams)
	}
}

// In-match updates (including a mid-match reconnect's restore, which KTX
// re-asserts itself) apply normally.
func TestMatchAnalyzer_InMatchFragUpdatesApply(t *testing.T) {
	a := NewMatchAnalyzer()
	ctx := &Context{}
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "rusti", Team: "jah"}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 5000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 16, TimeMs: 571000})
	// Mid-match reconnect: re-init to 0, then the server restores.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 0, TimeMs: 613000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 16, TimeMs: 614000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 0, Frags: 17, TimeMs: 629000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if got := res.Match.Players[0].Frags; got != 17 {
		t.Errorf("Frags = %d, want 17", got)
	}
}

// --- roster and frag attribution ------------------------------------

// matchUserInfo / matchVacate build the userinfo events the roster path
// keys on. A vacate is the server's drop broadcast: empty userinfo,
// client's own userid (see events.UserInfoEvent.Vacated).
func matchUserInfo(slot, uid int, name, team string, tMs int32) *events.UserInfoEvent {
	return &events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: slot, UserID: uid, Name: name, Team: team},
		TimeMs: tMs,
	}
}

func matchVacate(slot, uid int, name, team string, tMs int32) *events.UserInfoEvent {
	e := matchUserInfo(slot, uid, name, team, tMs)
	e.Vacated = true
	return e
}

// A player who leaves mid-match keeps the score the server announced for
// them, and the connection that lands on their freed slot is not a player.
//
// Reproduces 4on4_l_vs_la[e1m2] slot 7: shiva times out on 26 frags, the
// server broadcasts the departure and zeroes the slot in the same frame,
// and 17.8 s later a connection the server refuses (match locked) takes
// the slot without ever spawning.
func TestMatchAnalyzer_DepartureFragsFromBroadcast(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(7, 4948, "shiva", "|l|", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 20000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 7, TimeMs: 20100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 26, TimeMs: 1000000})
	// Timeout: broadcast, then the slot is cleared, then the empty userinfo.
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "shiva timed out\n", TimeMs: 1096572})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "shiva left the game with 26 frag", TimeMs: 1096572})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 0, TimeMs: 1096572})
	_ = a.OnEvent(matchVacate(7, 4948, "shiva", "|l|", 1096572))
	// Refused connection: a slot, a userinfo, a zero frag update, no play.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 0, TimeMs: 1114326})
	_ = a.OnEvent(matchUserInfo(7, 5796, "Sectoid", "sll", 1114326))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match is over\n", TimeMs: 1117846})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 {
		t.Fatalf("match.players = %+v, want just shiva", res.Match.Players)
	}
	got := res.Match.Players[0]
	if got.Name != "shiva" || got.Frags != 26 {
		t.Errorf("player = %+v, want shiva with 26 frags", got)
	}
	if len(res.Match.Teams) != 1 || res.Match.Teams[0].Frags != 26 {
		t.Errorf("teams = %+v, want one team on 26", res.Match.Teams)
	}
}

// With no departure broadcast (a mod that prints nothing), the score is
// still recoverable: SV_DropClient's frag reset lands in the same server
// frame as the empty userinfo, so it is rolled back rather than recorded.
// The rule is deliberately "the value before the drop", not the maximum —
// a frag can legitimately be lost to a suicide.
func TestMatchAnalyzer_DepartureResetRolledBack(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(1, 5, "test", "sdf", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 1, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 5, TimeMs: 50000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 4, TimeMs: 60000}) // suicide
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 0, TimeMs: 74295})
	_ = a.OnEvent(matchVacate(1, 5, "test", "sdf", 74295))

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 || res.Match.Players[0].Frags != 4 {
		t.Errorf("match.players = %+v, want test on 4 (the value before the drop, not the peak 5)",
			res.Match.Players)
	}
}

// A participant who goes spectator after the match is still a participant.
// Reproduces hub 212535: wd.dilbert plays the whole match, goes spec 13.9 s
// after it ends, and the slot's frags reset — the end-of-demo spectator
// gate used to drop his whole row.
func TestMatchAnalyzer_PostMatchSpectatorKeepsRow(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(4, 35, "wd.dilbert", "pys", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 0})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 4, TimeMs: 100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 21, TimeMs: 586894})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match is over\n", TimeMs: 600009})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 0, TimeMs: 613971})
	spec := matchUserInfo(4, 35, "wd.dilbert", "pys", 613971)
	spec.Player.Spectator = true
	_ = a.OnEvent(spec)

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 || res.Match.Players[0].Frags != 21 {
		t.Errorf("match.players = %+v, want wd.dilbert on 21", res.Match.Players)
	}
}

// FFA: nobody has a team, and an empty team used to read as "spectator" —
// which deleted every player on the demo. Participation is evidence of
// play, so a teamless player is a player.
func TestMatchAnalyzer_FFAEmptyTeamIsNotSpectator(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(3, 9, "/ tincan", "", 0))
	obs := matchUserInfo(6, 12, "adm<ego", "", 0)
	obs.Player.Spectator = true
	_ = a.OnEvent(obs)
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 3, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 3, Frags: 8, TimeMs: 60000})
	// A spectator that some mods publish with a large negative sentinel.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 6, Frags: -999, TimeMs: 60000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 {
		t.Fatalf("match.players = %+v, want just the teamless player", res.Match.Players)
	}
	if got := res.Match.Players[0]; got.Name != "/ tincan" || got.Frags != 8 || got.Team != "" {
		t.Errorf("player = %+v, want / tincan on 8 with no team", got)
	}
	if len(res.Match.Teams) != 0 {
		t.Errorf("teams = %+v, want none in FFA", res.Match.Teams)
	}
}

// A connection that never entered the game is not a player, even though it
// allocated a slot, emitted a userinfo and received a zero frag update.
// dag_caps_e1m2's jOn is the mild form: spec=false, mid-match, no spawn.
func TestMatchAnalyzer_ConnectionWithoutPlayIsNotAPlayer(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(matchUserInfo(13, 1315, "jOn", "oc", 702432))
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 13, Frags: 0, TimeMs: 702432})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 0 {
		t.Errorf("match.players = %+v, want none — jOn never entered the game", res.Match.Players)
	}
}
