package analyzer

import (
	"math"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// Userid attribution across slot handovers and reconnects.
//
// A userid names a *connection*, not a human: mvdsv hands them out from a
// rotating pool (SV_GenerateUserID, sv_main.c:538-556), so a slot that
// changes hands and a player who times out and rejoins both draw fresh
// ones. The pipeline used to latch the first userid seen on a slot and
// report it for the whole demo, which sent hub `track=<id>` links to the
// wrong player (gameId 220637) or to a connection that no longer existed
// (222649).
//
// The corpus invariant in userid_observed_test.go catches the first shape —
// reporting somebody else's id. It CANNOT catch the second: after a
// same-name rejoin the stale id was genuinely observed with that name, it
// is just dead. These fixtures pin that case, and the selection rule that
// resolves it, directly.

// sessionFixture builds a TimelineAnalyzer whose Finalize will publish
// playerUserIDs from a hand-built session table, with play evidence
// recorded on each slot at the given times.
func sessionFixture(sessions map[int][]ResolvedSession, play map[int][]int32) *TimelineAnalyzer {
	a := NewTimelineAnalyzer()
	a.ctx = &Context{}
	a.timing.Started = true
	a.timing.StartTime = 0
	a.timing.Ended = true
	a.timing.EndTime = 1200.0
	a.core = &CoreOutputs{Sessions: sessions}
	for slot, times := range play {
		st := a.getOrCreatePlayerState(slot)
		for _, t := range times {
			st.streams.recordSpawn(t)
		}
	}
	return a
}

func finalizeUserIDs(t *testing.T, a *TimelineAnalyzer) map[string]int {
	t.Helper()
	var res Result
	if err := a.Finalize(&res); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.TimelineAnalysis == nil {
		t.Fatal("nil TimelineAnalysis")
	}
	return res.TimelineAnalysis.PlayerUserIDs
}

// gameId 222649: bogojoker times out on userid 12 and reconnects onto the
// same slot as userid 25, under the same name. Both sessions had play and
// the identity unifier folds them into one name, so a keep-first rule
// reports 12 — an id that stopped existing sixteen minutes before the end
// of the demo. The live id is the one a `track=` needs.
func TestPlayerUserIDs_SameNameRejoinReportsTheLiveID(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			9: {
				{StartMs: math.MinInt32, EndMs: 141832, Name: "bogojoker", UserID: 12, IdentityKey: "id:0"},
				{StartMs: 150269, EndMs: math.MaxInt32, Name: "bogojoker", UserID: 25, IdentityKey: "id:0"},
			},
		},
		map[int][]int32{9: {5000, 140000, 160000, 900000}},
	)
	if got := finalizeUserIDs(t, a)["bogojoker"]; got != 25 {
		t.Errorf("playerUserIDs[bogojoker] = %d, want 25 (the session live at the end, not the dead first connection)", got)
	}
}

// The mirror case: the last session never spawned (a reconnect right at
// the end, or a return as a spectator). Then the last session WITH play is
// the answer — the rule is "last session with play", not "last session".
func TestPlayerUserIDs_SessionWithoutPlayIsNotSelected(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			9: {
				{StartMs: math.MinInt32, EndMs: 141832, Name: "bogojoker", UserID: 12, IdentityKey: "id:0"},
				{StartMs: 150269, EndMs: math.MaxInt32, Name: "bogojoker", UserID: 25, IdentityKey: "id:0"},
			},
		},
		map[int][]int32{9: {5000, 140000}},
	)
	if got := finalizeUserIDs(t, a)["bogojoker"]; got != 12 {
		t.Errorf("playerUserIDs[bogojoker] = %d, want 12 (the rejoin never played)", got)
	}
}

// gameId 220637: rusti reconnects onto a spectator's slot and mvdsv renames
// him `(1)rusti (FU)` (a name collision with his own still-live first
// connection). Two names, two ids, and the id of the spectator who held
// slot 9 first (42, gone at t=26 s) must not surface anywhere.
func TestPlayerUserIDs_HandoverKeepsEachConnectionsID(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			5: {
				{StartMs: math.MinInt32, EndMs: 630704, Name: "rusti (FU)", UserID: 37, IdentityKey: "id:0"},
				{StartMs: 636307, EndMs: math.MaxInt32, Name: "niomic(FU)", UserID: 44, IdentityKey: "id:2"},
			},
			9: {
				// Pit: a spectator, so no play evidence — a phantom entry
				// under a name that appears nowhere else in the result.
				{StartMs: math.MinInt32, EndMs: 619749, Name: "Pit", UserID: 42, IdentityKey: "id:1"},
				{StartMs: 619749, EndMs: math.MaxInt32, Name: "(1)rusti (FU)", UserID: 43, IdentityKey: "id:3"},
			},
		},
		map[int][]int32{5: {1000, 600000}, 9: {700000, 900000}},
	)
	ids := finalizeUserIDs(t, a)
	for name, want := range map[string]int{"rusti (FU)": 37, "(1)rusti (FU)": 43} {
		if ids[name] != want {
			t.Errorf("playerUserIDs[%q] = %d, want %d", name, ids[name], want)
		}
	}
	if uid, ok := ids["Pit"]; ok {
		t.Errorf("playerUserIDs carries %q = %d for a session with no play", "Pit", uid)
	}
	if _, ok := ids["niomic(FU)"]; ok {
		t.Errorf("playerUserIDs carries niomic(FU), whose session had no play")
	}
}

// Powerup runs (and `//demomark` bookmarks) stamp a userid of their own,
// through their own resolution path. It has to be the session that held
// the slot when the run started — not the slot's final occupant, which is
// what ctx.Players holds and what this path used to fall back to.
func TestPowerupUserIDIsTheSessionAtPickup(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			5: {
				{StartMs: math.MinInt32, EndMs: 630704, Name: "rusti (FU)", Team: "-fu-", UserID: 37, IdentityKey: "id:0"},
				{StartMs: 636307, EndMs: math.MaxInt32, Name: "niomic(FU)", Team: "-fu-", UserID: 44, IdentityKey: "id:2"},
			},
		},
		map[int][]int32{5: {1000, 600000}},
	)
	// The slot's FINAL occupant, as ctx.Players records it — the value the
	// old fallback would have stamped on every run on this slot.
	a.ctx.Players[5] = &events.PlayerInfo{Slot: 5, Name: "niomic(FU)", UserID: 44}

	early := a.createPowerupEvent(5, "quad", 300000, 330000)
	if early.PlayerUserID != 37 || early.PlayerName != "rusti (FU)" {
		t.Errorf("run before the handover: got %q/%d, want %q/37", early.PlayerName, early.PlayerUserID, "rusti (FU)")
	}
	late := a.createPowerupEvent(5, "quad", 700000, 730000)
	if late.PlayerUserID != 44 || late.PlayerName != "niomic(FU)" {
		t.Errorf("run after the handover: got %q/%d, want %q/44", late.PlayerName, late.PlayerUserID, "niomic(FU)")
	}
}

// With no identity analyser wired (a hand-built registry) there is no
// session table, so the per-slot userinfo latch remains the fallback. It
// now keeps the LAST valid userid rather than the first, and ignores
// svc_setinfo syntheses, whose UserID is the parser's stale cache
// (mvd-reader/parser/userinfo.go:63-86).
func TestSlotUserIDLatchIgnoresPartialAndKeepsLatest(t *testing.T) {
	a := NewTimelineAnalyzer()
	a.ctx = &Context{}
	info := func(uid int, name string) *events.PlayerInfo {
		return &events.PlayerInfo{Slot: 3, Name: name, UserID: uid}
	}
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 0, Player: info(12, "bogojoker")})
	// svc_setinfo replay carrying the departed client's cached userid.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150269, Player: info(12, "bogojoker"), Partial: true})
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150269, Player: info(25, "bogojoker")})
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150300, Player: info(25, "bogojoker"), Partial: true})
	if got := a.playerUserIDs[3]; got != 25 {
		t.Errorf("playerUserIDs[3] = %d, want 25 (latest non-partial userinfo)", got)
	}
	if got := a.userIDAt(3, 900000); got != 25 {
		t.Errorf("userIDAt with no session table = %d, want the latch value 25", got)
	}
}
