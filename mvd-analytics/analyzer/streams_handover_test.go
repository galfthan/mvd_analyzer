package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// A wire slot's item state must not survive an occupancy handover.
//
// Per-slot possession intervals are driven by StatItems bit flips, so an
// occupant who still holds an RL when they leave has an interval that is
// still open. Carrying that open anchor into the next occupancy makes the
// new client appear to hold the item from the instant their userinfo lands
// — on 4on4_l_vs_la[e1m2] that fabricated 3520 ms of RL/SNG/SSG possession
// spanning the whole of a refused connection's stint. The
// departing player keeps the interval, closed at the moment they left.
func TestStreams_ItemStateDoesNotCrossOccupancyHandover(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 4948, Name: "shiva"},
	})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	// shiva picks up an RL and never drops it.
	_ = a.OnEvent(&events.StatUpdateEvent{
		PlayerNum: 7, StatIndex: events.StatItems,
		Value: events.ITRocketLauncher, TimeMs: 900_000,
	})
	// He times out: the server clears the slot and broadcasts it.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 4948, Name: "shiva"},
		TimeMs: 1_096_572, Vacated: true,
	})
	// A refused connection takes the freed slot and never sends a stat.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 5796, Name: "Sectoid"},
		TimeMs: 1_114_326,
	})

	st := a.playerState[7]
	if st == nil {
		t.Fatal("no state for slot 7")
	}
	if st.streams.rl.held {
		t.Errorf("slot 7 still holds an open RL interval after the handover")
	}
	if n := len(st.streams.rl.closed); n != 1 {
		t.Fatalf("closed rl intervals = %d, want 1", n)
	}
	if got := st.streams.rl.closed[0]; got.start != 900_000 || got.end != 1_096_572 {
		t.Errorf("rl interval = [%d,%d), want [900000,1096572) — the departing player's, ending when he left",
			got.start, got.end)
	}

	// The next occupant's window carries nothing.
	a.UseCoreOutputs(&CoreOutputs{Sessions: map[int][]ResolvedSession{
		7: {
			{StartMs: minInt32, EndMs: 1_114_326, Name: "shiva", IdentityKey: "id:0"},
			{StartMs: 1_114_326, EndMs: maxInt32, Name: "Sectoid", IdentityKey: "id:1"},
		},
	}})
	streams := a.buildStreamsResult(nil, nil, 0, 1_117_846)
	if findStream(streams, "Sectoid") {
		t.Errorf("Sectoid got a stream built purely from the previous occupant's item bits")
	}
	if !findStream(streams, "shiva") {
		t.Errorf("shiva lost his own stream")
	}
}

// Change streams dedup against the slot builder's last value, which spans
// occupancies. If the arriving occupant's first value equals the departing
// one's last, the sample is suppressed and the new player's stream fragment
// starts with no value at all — the same leak the item intervals above fix,
// one level down. The handover cuts the dedup floor.
func TestStreams_ChangeStreamDedupResetsAtHandover(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 4948, Name: "shiva"},
	})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	// shiva ends his stint on 100 health / 50 armour / 10 rockets.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatHealth, Value: 100, TimeMs: 900_000})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatArmor, Value: 50, TimeMs: 900_000})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatRockets, Value: 10, TimeMs: 900_000})
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 4948, Name: "shiva"},
		TimeMs: 1_000_000, Vacated: true,
	})
	// The next occupant spawns on exactly the same numbers.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 5796, Name: "Sectoid"},
		TimeMs: 1_010_000,
	})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatHealth, Value: 100, TimeMs: 1_020_000})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatArmor, Value: 50, TimeMs: 1_020_000})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatRockets, Value: 10, TimeMs: 1_020_000})
	// ... and a genuinely repeated value right after is still deduped.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 7, StatIndex: events.StatHealth, Value: 100, TimeMs: 1_021_000})

	b := a.playerState[7].streams
	for _, tc := range []struct {
		name string
		col  []changeI16
	}{
		{"health", b.health}, {"armor", b.armor}, {"rockets", b.rockets},
	} {
		last := tc.col[len(tc.col)-1]
		if last.t != 1_020_000 {
			t.Errorf("%s stream ends at t=%d, want a sample at 1020000 for the new occupant (col=%v)",
				tc.name, last.t, tc.col)
		}
	}
	if n := len(b.health); n != 2 {
		t.Errorf("health samples = %d (%v), want exactly 2 — one per occupant, the 1021000 repeat deduped", n, b.health)
	}
}

// A mid-match reconnect arrives as vacate-then-connect: the tracker reports
// a close with no open, then an open with no close. The frag-reset flag has
// to be armed on that second event, otherwise the mod's stats restore
// reaches the corruption guard in handleFragUpdate as a huge delta, is
// rejected, and — because the guard deliberately leaves state.frags
// untouched — every later real +1 is rejected too, freezing the player's
// timeline score for the rest of the match.
//
// Shape from hub gameId 216835: rusti is dropped on 16 frags and KTX
// restores the same 16 onto the next connection (ktx/src/client.c:1464-1490).
func TestTimeline_FragResetArmedOnVacateThenReconnect(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	// rusti's userinfo comes off the MVD header's full-state block, before
	// the match starts — the real wire order on 216835 (MVD_FORMAT.md,
	// "svc_setinfo carries no identity"), so nothing is armed for the first
	// half.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 8, Name: "rusti"}, TimeMs: 0,
	})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	for i := 1; i <= 16; i++ {
		_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: i, TimeMs: int32(10_000 * i)})
	}
	// The drop, then the reconnect on a fresh userid.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 8, Name: "rusti"}, TimeMs: 613_452, Vacated: true,
	})
	if !a.fragResetPending[7] {
		// A vacate closes the occupancy but opens nothing, so the flag is not
		// armed yet — the connect below is what arms it.
		_ = a.OnEvent(&events.UserInfoEvent{
			Player: &events.PlayerInfo{Slot: 7, UserID: 21, Name: "rusti"}, TimeMs: 613_500,
		})
	}
	if !a.fragResetPending[7] {
		t.Fatal("the reconnect did not arm fragResetPending — the restore below will be rejected")
	}
	// KTX re-asserts the restored total, then rusti gets one more frag.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 16, TimeMs: 614_000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: 17, TimeMs: 629_000})

	if got := a.playerState[7].frags; got != 17 {
		t.Errorf("state.frags = %d, want 17 — the restore must rebase, not freeze the cursor", got)
	}
	sum := 0
	for _, f := range a.rawFrags {
		if f.PlayerNum == 7 {
			sum += f.Delta
		}
	}
	if sum != 17 {
		t.Errorf("timeline frag deltas sum to %d, want 17", sum)
	}
}

// The reconnect does not land on the slot that was just freed. SV_DropClient
// leaves the departing client in cs_zombie (mvdsv/src/sv_main.c:412) and
// SVC_DirectConnect takes the first cs_free slot (CountPlayersSpecsVips,
// :1137-1145), so the returning player gets the lowest index nobody is on —
// typically a *lower* one, and on a recording that started mid-game one with
// no earlier occupancy in the demo at all.
//
// The rebase must therefore arm on a mid-match connect to a slot the demo
// has never seen occupied. Shape from gameId 216835 with the recording
// starting after the spectator on slot 2 had already left: rusti is dropped
// from slot 7 on 16 frags and KTX restores them onto slot 2.
func TestTimeline_FragResetArmedOnFirstOccupancyOfSlot(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 8, Name: "rusti"}, TimeMs: 0,
	})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	for i := 1; i <= 16; i++ {
		_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 7, Frags: i, TimeMs: int32(10_000 * i)})
	}
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 7, UserID: 8, Name: "rusti"}, TimeMs: 613_452, Vacated: true,
	})
	// He comes back on slot 2, which this recording has never seen occupied.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 2, UserID: 21, Name: "rusti"}, TimeMs: 613_500,
	})
	if !a.fragResetPending[2] {
		t.Fatal("a first occupancy of slot 2 did not arm fragResetPending — the restore below will be rejected")
	}
	// KTX restores the 16 onto the new slot, then rusti gets one more frag.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 2, Frags: 16, TimeMs: 614_000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 2, Frags: 17, TimeMs: 629_000})

	if got := a.playerState[2].frags; got != 17 {
		t.Errorf("state.frags = %d, want 17 — the restore must rebase, not freeze the cursor", got)
	}
	sum := 0
	for _, f := range a.rawFrags {
		if f.PlayerNum == 2 {
			sum += f.Delta
		}
	}
	if sum != 1 {
		t.Errorf("slot 2 frag deltas sum to %d, want 1 — the restored 16 is a rebase, the later kill is a +1", sum)
	}
}

// The other half of the same rule: a genuinely new connection mid-match
// (nobody reconnecting, no stats to restore) must not lose its first real
// frag to the rebase.
//
// The order below is the wire's, not a convenient one. SV_FullClientUpdate
// writes svc_updatefrags FIRST and svc_updateuserinfo LAST into a single
// buffer (mvdsv/src/sv_main.c:481-513) which sv_send.c:1060-1064 copies
// verbatim into the demo as one dem_all block, so the new client's own 0
// arrives BEFORE the userinfo that arms the rebase — and the update AFTER
// it is the player's first kill. A rebase that fires on any value therefore
// eats that kill; only rebasing a value the corruption guard would have
// rejected leaves it alone. Measured: with an unconditional rebase this
// slot scores 15, not 16.
func TestTimeline_FragResetOnFreshConnectEatsNoDelta(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 1000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 0, TimeMs: 300_000})
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 4, UserID: 31, Name: "latecomer"}, TimeMs: 300_000,
	})
	for i := 1; i <= 16; i++ {
		_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: i, TimeMs: int32(300_000 + 1000*i)})
	}
	// The arm is spent on the first update after the connect, so a garbage
	// read later in the same occupancy is still the corruption guard's
	// business — it must be rejected, not adopted as a baseline.
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 272, TimeMs: 400_000})
	_ = a.OnEvent(&events.FragUpdateEvent{PlayerNum: 4, Frags: 17, TimeMs: 401_000})

	n, sum := sumDeltasForSlot(a, 4)
	if n != 17 || sum != 17 {
		t.Errorf("slot 4: %d frag events summing %d, want 17/17 — the rebase must not swallow the first real kill, "+
			"and a stale arm must not adopt the 272", n, sum)
	}
}

// The wielded-weapon column is recorded THROUGH the countdown — it is
// delta-coded, so the match-start rebase needs the latest pre-match value —
// which makes it the one column a PRE-match handover can corrupt. Every
// other change stream is gated on the match window and simply has nothing to
// dedup against yet.
//
// Shape: a roster shuffle during warmup hands a slot from one player to
// another while both happen to be holding the shotgun. Without a dedup cut
// the arriving player's sample is suppressed as a repeat of the departing
// player's, the session slice then keeps the departing player's entry out of
// the arriving player's stream, and the arriving player reaches match start
// with no aw sample at all — so every death of theirs before their next
// weapon switch reconstructs no backpack.
func TestStreams_ActiveWeaponDedupResetsAtPreMatchHandover(t *testing.T) {
	a := NewTimelineAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatal(err)
	}
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 5, UserID: 11, Name: "leaver"}, TimeMs: 0,
	})
	// Warmup: the departing player is holding the shotgun.
	_ = a.OnEvent(&events.StatUpdateEvent{
		PlayerNum: 5, StatIndex: events.StatActiveWeapon,
		Value: events.ITShotgun, TimeMs: 10_000,
	})
	_ = a.OnEvent(&events.PlayerPositionEvent{PlayerNum: 5, Origin: [3]float32{10, 0, 0}, TimeMs: 10_000})
	// Still pre-match, the slot changes hands.
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 5, UserID: 11, Name: "leaver"}, TimeMs: 20_000, Vacated: true,
	})
	_ = a.OnEvent(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 5, UserID: 12, Name: "joiner"}, TimeMs: 21_000,
	})
	// The arriving player is holding the shotgun too.
	_ = a.OnEvent(&events.StatUpdateEvent{
		PlayerNum: 5, StatIndex: events.StatActiveWeapon,
		Value: events.ITShotgun, TimeMs: 22_000,
	})
	_ = a.OnEvent(&events.PrintEvent{Level: 2, Message: "The match has begun!\n", TimeMs: 30_000})
	// In-match they take the RL.
	_ = a.OnEvent(&events.StatUpdateEvent{
		PlayerNum: 5, StatIndex: events.StatActiveWeapon,
		Value: events.ITRocketLauncher, TimeMs: 90_000,
	})
	_ = a.OnEvent(&events.PlayerPositionEvent{PlayerNum: 5, Origin: [3]float32{20, 0, 0}, TimeMs: 90_000})

	col := a.playerState[5].streams.activeWeapon
	if len(col) != 3 {
		t.Fatalf("aw column = %v, want 3 samples — the handover makes the 22000 repeat unconditional", col)
	}
	if col[1].t != 22_000 || col[1].v != events.ITShotgun {
		t.Errorf("aw[1] = %+v, want the arriving occupant's own sample at t=22000", col[1])
	}

	// And the arriving player's stream carries it: their fragment opens with
	// a weapon, not blank.
	a.UseCoreOutputs(&CoreOutputs{Sessions: map[int][]ResolvedSession{
		5: {
			{StartMs: minInt32, EndMs: 21_000, Name: "leaver", IdentityKey: "id:0"},
			{StartMs: 21_000, EndMs: maxInt32, Name: "joiner", IdentityKey: "id:1"},
		},
	}})
	streams := a.buildStreamsResult(nil, nil, 30_000, 200_000)
	var joiner *result.PlayerStream
	for i := range streams.Players {
		if streams.Players[i].Name == "joiner" {
			joiner = &streams.Players[i]
		}
	}
	if joiner == nil {
		t.Fatal("no stream for joiner")
	}
	if len(joiner.ActiveWeapon) == 0 || joiner.ActiveWeapon[0].T != 22_000 {
		t.Errorf("joiner aw = %v, want it to open at t=22000 with their own sample", joiner.ActiveWeapon)
	}
}
