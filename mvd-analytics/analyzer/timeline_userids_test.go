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
// session table, so the per-slot userinfo latch remains the fallback. Its
// rule is FIRST valid userid per OCCUPANCY: a corrupted full-userinfo
// resend inside one occupancy (the KTPro class MVD_FORMAT.md documents)
// cannot move it, the server's drop broadcast lets the next connection
// re-latch, and svc_setinfo syntheses — whose UserID is the parser's stale
// cache (mvd-reader/parser/userinfo.go:63-86) — never latch at all.
func TestSlotUserIDLatchKeepsFirstValidPerOccupancy(t *testing.T) {
	a := NewTimelineAnalyzer()
	a.ctx = &Context{}
	info := func(uid int, name string) *events.PlayerInfo {
		return &events.PlayerInfo{Slot: 3, Name: name, UserID: uid}
	}
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 0, Player: info(12, "bogojoker")})
	// A full userinfo resend on the SAME occupancy carrying a different
	// userid: the corrupted-resend shape. First-valid-wins is the only
	// guard against it — Partial does not mark it.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 60000, Player: info(77, "bogojoker")})
	if got := a.playerUserIDs[3]; got != 12 {
		t.Fatalf("playerUserIDs[3] = %d after a corrupt mid-occupancy resend, want the occupancy's first valid 12", got)
	}
	// The server drops the client (SV_DropClient's empty userinfo, which
	// still carries the departing client's userid). The latch goes stale.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 141832, Player: &events.PlayerInfo{Slot: 3, UserID: 77}, Vacated: true})
	// svc_setinfo replay carrying the departed client's cached userid: not
	// a connection, so it must not claim the freed latch.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150269, Player: info(12, "bogojoker"), Partial: true})
	// The rejoin: a new occupancy, so this one latches.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150269, Player: info(25, "bogojoker")})
	// A trailing svc_setinfo with the STALE userid must not move it back.
	a.handleUserInfo(&events.UserInfoEvent{TimeMs: 150300, Player: info(12, "bogojoker"), Partial: true})

	if got := a.playerUserIDs[3]; got != 25 {
		t.Errorf("playerUserIDs[3] = %d, want 25 (the occupancy that followed the drop)", got)
	}
	if got := a.userIDAt(3, 900000); got != 25 {
		t.Errorf("userIDAt with no session table = %d, want the latch value 25", got)
	}
}

// The half-open convention at a handover instant: the two sessions share
// that millisecond (the occupancy tracker closes one and opens the next at
// the same wire time), and t == the boundary belongs to the SUCCESSOR.
func TestUserIDAt_HandoverInstantResolvesToSuccessor(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			5: {
				{StartMs: math.MinInt32, EndMs: 630704, Name: "rusti (FU)", UserID: 37, IdentityKey: "id:0"},
				{StartMs: 630704, EndMs: math.MaxInt32, Name: "niomic(FU)", UserID: 44, IdentityKey: "id:2"},
			},
		},
		nil,
	)
	if got := a.userIDAt(5, 630704); got != 44 {
		t.Errorf("userIDAt at the handover = %d, want 44 (the session that owns [t, …))", got)
	}
	if got := a.userIDAt(5, 630703); got != 37 {
		t.Errorf("userIDAt one ms earlier = %d, want 37", got)
	}
}

// Between a drop and the rejoin the slot belongs to nobody, so no session
// covers the instant and the per-slot latch answers. The sentinel value
// here appears in no session precisely so the assertion can only pass via
// that fallback.
func TestUserIDAt_GapBetweenSessionsFallsBackToTheLatch(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			9: {
				{StartMs: math.MinInt32, EndMs: 141832, Name: "bogojoker", UserID: 12, IdentityKey: "id:0"},
				{StartMs: 150269, EndMs: math.MaxInt32, Name: "bogojoker", UserID: 25, IdentityKey: "id:0"},
			},
		},
		nil,
	)
	a.playerUserIDs[9] = 99
	if got := a.userIDAt(9, 145000); got != 99 {
		t.Errorf("userIDAt inside the empty-slot gap = %d, want the latch fallback 99", got)
	}
	if got := a.userIDAt(9, 140000); got != 12 {
		t.Errorf("userIDAt inside the first session = %d, want 12, not the latch", got)
	}
}

// Two connections of one player on two slots, neither of which the other
// ever occupied: both are the FIRST session on their slot, so both
// resolution windows start at -inf and a start-ordered pick is a tie
// broken by slot index — which here would hand the answer to the DEAD
// first connection (the lower slot). The rule is last session with PLAY.
func TestPlayerUserIDs_OrderedByLastPlayNotSessionStart(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			3: {{StartMs: math.MinInt32, EndMs: math.MaxInt32, Name: "rusti", UserID: 12, IdentityKey: "id:0"}},
			9: {{StartMs: math.MinInt32, EndMs: math.MaxInt32, Name: "rusti", UserID: 25, IdentityKey: "id:0"}},
		},
		map[int][]int32{3: {1000, 600000}, 9: {700000, 900000}},
	)
	if got := finalizeUserIDs(t, a)["rusti"]; got != 25 {
		t.Errorf("playerUserIDs[rusti] = %d, want 25 — the connection that played last, not the lower slot", got)
	}
}

// Frag streaks stamp the userid of the connection that played the run, not
// the demo-wide one. gameId 222649's shape: a streak inside bogojoker's
// pre-timeout stint belongs to userid 12; the id he came back on (25) was
// not issued until nine seconds after that stint ended, so a `track=` link
// built from it resolves to nothing for the whole run.
func TestFragStreaks_UserIDIsTheSessionAtTheRun(t *testing.T) {
	a := sessionFixture(
		map[int][]ResolvedSession{
			9: {
				{StartMs: math.MinInt32, EndMs: 141832, OccStartMs: 0, Name: "bogojoker", UserID: 12, IdentityKey: "id:0"},
				{StartMs: 150269, EndMs: math.MaxInt32, OccStartMs: 150269, Name: "bogojoker", UserID: 25, IdentityKey: "id:0"},
			},
		},
		nil,
	)
	a.timing.EndTime = 900000
	a.core.FragEntries = []FragEntry{
		{Time: 30000, Killer: "bogojoker", Victim: "prey", Weapon: "rl"},
		{Time: 200000, Killer: "bogojoker", Victim: "prey", Weapon: "lg"},
	}
	a.rawSpawns = append(a.rawSpawns,
		deathEvent{Time: 20000, PlayerNum: 9},
		deathEvent{Time: 160000, PlayerNum: 9})
	a.rawDeaths = append(a.rawDeaths,
		deathEvent{Time: 100000, PlayerNum: 9},
		deathEvent{Time: 300000, PlayerNum: 9})

	// The demo-wide map (PlayerUserIDs) reports the live id for both runs;
	// only the per-instant resolution can tell them apart.
	streaks := a.detectFragStreaks(10, nil, newNameUserIDIndex(a.core, map[string]int{"bogojoker": 25}))
	if len(streaks) != 2 {
		t.Fatalf("streaks = %+v, want 2", streaks)
	}
	byStart := map[int32]int{}
	for _, s := range streaks {
		byStart[s.Time] = s.PlayerUserID
	}
	if got := byStart[20000]; got != 12 {
		t.Errorf("pre-timeout run carries userid %d, want 12 (the connection that played it)", got)
	}
	if got := byStart[160000]; got != 25 {
		t.Errorf("post-rejoin run carries userid %d, want 25", got)
	}
}
