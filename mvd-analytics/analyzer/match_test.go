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

// departure is the parser's decoded "<name> left the game with N frags"
// broadcast (events.PlayerDepartureEvent).
func departure(name string, frags int, tMs int32) *events.PlayerDepartureEvent {
	return &events.PlayerDepartureEvent{Name: name, Frags: frags, FragsKnown: true, TimeMs: tMs}
}

// A player who leaves mid-match keeps the score the server announced for
// them, and the connection that lands on their freed slot is not a player.
//
// Reproduces 4on4_l_vs_la[e1m2] slot 7: shiva times out, the server
// broadcasts the departure and zeroes the slot in the same frame, and
// 17.8 s later a connection the server refuses (match locked) takes the
// slot without ever spawning.
//
// The broadcast says 26 while our own svc_updatefrags cursor is on 20 —
// deliberately different, so this pins the *broadcast* path. With the
// cursor and the broadcast agreeing, the rollback in closeOccupancy alone
// produces the right answer and the test proves nothing (which is exactly
// how it was possible to delete the broadcast lookup and keep every test
// green). A gap like this is what a mod that scores outside
// svc_updatefrags, or a demo that lost the last frag packet, looks like.
func TestMatchAnalyzer_DepartureFragsFromBroadcast(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(7, 4948, "shiva", "|l|", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 20000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 7, TimeMs: 20100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 20, TimeMs: 1000000})
	// Timeout: broadcast, then the slot is cleared, then the empty userinfo.
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "shiva timed out\n", TimeMs: 1096572})
	_ = a.OnEvent(departure("shiva", 26, 1096572))
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
		t.Errorf("player = %+v, want shiva with 26 frags (the server's own count, not our cursor's 20)", got)
	}
	if len(res.Match.Teams) != 1 || res.Match.Teams[0].Frags != 26 {
		t.Errorf("teams = %+v, want one team on 26", res.Match.Teams)
	}
}

// The departure broadcast only names a netname, so it must not travel
// beyond the frame the departure happened in. Two people sharing a netname
// otherwise hand each other their scores.
//
// SV_DropClient prints ClientDisconnect's bprint and broadcasts the empty
// userinfo in the same server frame (mvdsv/src/sv_main.c:395-428), which is
// what makes the same-frame rule safe: both real departures in the local
// corpus satisfy it.
func TestMatchAnalyzer_DepartureBroadcastDoesNotBleedToAnotherOccupancy(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(1, 11, "bob", "aaa", 0))
	_ = a.OnEvent(matchUserInfo(2, 12, "bob", "bbb", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 1, TimeMs: 1100})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 2, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 3, TimeMs: 60000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 2, Frags: 40, TimeMs: 80000})
	// The first bob leaves, announced on 3.
	_ = a.OnEvent(departure("bob", 3, 70000))
	_ = a.OnEvent(matchVacate(1, 11, "bob", "aaa", 70000))
	// The second bob is dropped 20 s later; his own broadcast was lost to
	// print fragmentation, so nothing announces him.
	_ = a.OnEvent(matchVacate(2, 12, "bob", "bbb", 90000))

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	frags := map[string]int{}
	for _, p := range res.Match.Players {
		frags[p.Team] = p.Frags
	}
	if frags["aaa"] != 3 || frags["bbb"] != 40 {
		t.Errorf("match.players = %+v, want aaa on 3 and bbb on 40 — the first bob's broadcast must not reach the second",
			res.Match.Players)
	}
}

// Two humans who happen to share a netname are two scoreboard rows.
//
// On a demo with no demoinfo, no *auth and no KTX reconnect print —
// every pre-KTX demo — identity.go's Source 4 unions sessions by
// normalized netname, and normalizePlayerName strips case and all
// punctuation (names.go:16), so "player" and "player!" land in one
// identity group and arrive here under one IdentityKey. The only thing
// left that can tell them apart is that both occupancies were live at the
// same instant.
func TestMatchAnalyzer_OverlappingOccupanciesAreNotOneIdentity(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	// What identity.go publishes for this demo: one group, two sessions.
	a.UseCoreOutputs(&CoreOutputs{Sessions: map[int][]ResolvedSession{
		1: {{StartMs: minInt32, EndMs: maxInt32, Name: "player", Team: "aaa", IdentityKey: "id:0"}},
		2: {{StartMs: minInt32, EndMs: maxInt32, Name: "player!", Team: "bbb", IdentityKey: "id:0"}},
	}})

	_ = a.OnEvent(matchUserInfo(1, 11, "player", "aaa", 0))
	_ = a.OnEvent(matchUserInfo(2, 12, "player!", "bbb", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 1, TimeMs: 1100})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 2, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 3, TimeMs: 60000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 2, Frags: 40, TimeMs: 80000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 2 {
		t.Fatalf("match.players = %+v, want two rows — the two occupancies overlap in time", res.Match.Players)
	}
	if len(res.Match.Teams) != 2 {
		t.Errorf("teams = %+v, want both aaa and bbb", res.Match.Teams)
	}
}

// The same identity key on two occupancies that do NOT overlap is one
// human reconnecting, and stays one row carrying the later stint's score.
func TestMatchAnalyzer_DisjointOccupanciesStayOneIdentity(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(1, 11, "rusti", "jah", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 1, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 1, Frags: 16, TimeMs: 60000})
	_ = a.OnEvent(departure("rusti", 16, 70000))
	_ = a.OnEvent(matchVacate(1, 11, "rusti", "jah", 70000))
	// Same human, new connection, other slot.
	_ = a.OnEvent(matchUserInfo(5, 21, "rusti", "jah", 71000))
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 5, TimeMs: 71100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 5, Frags: 19, TimeMs: 90000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 || res.Match.Players[0].Frags != 19 {
		t.Errorf("match.players = %+v, want one rusti row on 19 (the later stint)", res.Match.Players)
	}
}

// The match scoreboard is frozen at match end, and a departure broadcast is
// no exception. KTX guards its own print on `match_in_progress == 2`
// (ktx/src/client.c:2841), but the pre-KTX mods this recovery exists for do
// not — so a player who disconnects during intermission would be announced
// on whatever his edict holds and overwrite his real score.
//
// Shape from hub 212535: wd.dilbert finishes on 21, the match ends at
// 600009, the slot is re-inited at 613971.
func TestMatchAnalyzer_PostMatchDepartureBroadcastIgnored(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(4, 35, "dilbert", "pys", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 0})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 4, TimeMs: 100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 21, TimeMs: 586894})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match is over\n", TimeMs: 600009})
	_ = a.OnEvent(departure("dilbert", 0, 613971))
	_ = a.OnEvent(matchVacate(4, 35, "dilbert", "pys", 613971))

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 || res.Match.Players[0].Frags != 21 {
		t.Errorf("match.players = %+v, want dilbert on 21 — the post-match broadcast must not win", res.Match.Players)
	}
}

// An occupancy the wire never named — opened by occupancyTracker.ensure
// because a frag update landed on an empty slot — must not be resolved
// against the player who just vacated it. The identity table extends each
// slot's last session to +inf, so it would name the departed player, and
// then win the roster tie-break on its later startMs and replace his
// recovered score with 0.
func TestMatchAnalyzer_AnonymousRecordAfterVacateIsNotTheDepartedPlayer(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	a.UseCoreOutputs(&CoreOutputs{Sessions: map[int][]ResolvedSession{
		7: {{StartMs: minInt32, EndMs: maxInt32, Name: "shiva", Team: "|l|", IdentityKey: "id:0"}},
	}})

	_ = a.OnEvent(matchUserInfo(7, 4948, "shiva", "|l|", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 7, TimeMs: 1100})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 26, TimeMs: 900000})
	_ = a.OnEvent(departure("shiva", 26, 1000000))
	_ = a.OnEvent(matchVacate(7, 4948, "shiva", "|l|", 1000000))
	// Play events on the freed slot with no userinfo of their own.
	_ = a.OnEvent(&events.SpawnEvent{PlayerNum: 7, TimeMs: 1010000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 0, TimeMs: 1010000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 1 || res.Match.Players[0].Frags != 26 {
		t.Errorf("match.players = %+v, want shiva alone on 26", res.Match.Players)
	}
}

// The large negative frag value pre-KTX mods publish for a spectator's
// scoreboard entry is a sort marker, not a score: it must neither be
// recorded nor count as evidence that the client played. Observed as -999
// five times on dag_caps_e1m2.
func TestMatchAnalyzer_SpectatorFragSentinelIgnored(t *testing.T) {
	a := NewMatchAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}

	_ = a.OnEvent(matchUserInfo(2, 77, "cam", "oc", 0))
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 2, Frags: -999, TimeMs: 60000})

	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatal(err)
	}
	if len(res.Match.Players) != 0 {
		t.Errorf("match.players = %+v, want none — a -999 sort marker is not a score and not evidence of play",
			res.Match.Players)
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
