package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// A wire slot's item state must not survive an occupancy handover.
//
// Per-slot possession intervals are driven by StatItems bit flips, so an
// occupant who still holds an RL when they leave has an interval that is
// still open. Carrying that open anchor into the next occupancy makes the
// new client appear to hold the item from the instant their userinfo lands
// — on 4on4_l_vs_la[e1m2] that fabricated 3520 ms of RL/SNG/SSG possession
// (and a shareAlive of 1.0) for a connection the server had refused. The
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
