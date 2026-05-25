package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// sumDeltasForSlot returns (event count, summed delta) of the raw frag
// events the timeline accumulated for one wire slot.
func sumDeltasForSlot(a *TimelineAnalyzer, slot int) (int, int) {
	n, sum := 0, 0
	for _, f := range a.rawFrags {
		if f.PlayerNum == slot {
			n++
			sum += f.Delta
		}
	}
	return n, sum
}

// TestFragUpdate_ReconnectRebasesScore reproduces gameId 216835: a player
// (rusti) frags up to 16 on slot 7, disconnects, and reconnects onto a
// slot a spectator had been holding (slot 2, userid 13 -> 14). KTX
// restores his 16-frag total onto the new slot as the first frag update.
//
// Before the fix that +16 restore tripped the [-5,5] corruption guard,
// which left the new slot's baseline at 0 so every later real +1 also
// read as a huge delta and was dropped — freezing the player's timeline
// score at 16 for the rest of the match. The handoff must instead rebase
// the slot silently, so the post-reconnect kills accrue as +1 each.
func TestFragUpdate_ReconnectRebasesScore(t *testing.T) {
	a := NewTimelineAnalyzer()
	a.timing.Started = true

	feed := func(ev events.Event) { _ = a.OnEvent(ev) }
	userinfo := func(slot, uid int, name string, tt float64) {
		feed(&events.UserInfoEvent{Player: &events.PlayerInfo{
			Slot: slot, UserID: uid, Name: name, Team: "jah",
		}, Time: tt})
	}
	frag := func(slot, frags int, tt float64) {
		feed(&events.FragUpdateEvent{PlayerNum: slot, Frags: frags, Time: tt})
	}

	// First half: rusti on slot 7, 16 kills.
	userinfo(7, 8, "rusti", 0)
	for f := 1; f <= 16; f++ {
		frag(7, f, float64(f))
	}

	// A spectator already holds slot 2; then rusti reconnects onto it with
	// a new userid. The userid change after match start is the handoff
	// signal.
	userinfo(2, 13, "Evil_ua", 600)
	userinfo(2, 14, "rusti", 613)

	// KTX restores the 16-frag total, then two real kills.
	frag(2, 16, 613) // restore — must NOT emit and must rebase to 16
	frag(2, 17, 629)
	frag(2, 18, 631)

	n7, sum7 := sumDeltasForSlot(a, 7)
	if n7 != 16 || sum7 != 16 {
		t.Errorf("slot 7 (pre-reconnect): got %d events summing %d, want 16/16", n7, sum7)
	}

	n2, sum2 := sumDeltasForSlot(a, 2)
	if n2 != 2 || sum2 != 2 {
		t.Errorf("slot 2 (post-reconnect): got %d events summing %d, want 2/2 (the +16 restore must be silent)", n2, sum2)
	}
	for _, f := range a.rawFrags {
		if f.Delta > 5 || f.Delta < -5 {
			t.Errorf("emitted out-of-range delta %d on slot %d — restore leaked through", f.Delta, f.PlayerNum)
		}
	}
}

// TestFragUpdate_TransientCorruptionStillRejected guards the original
// behaviour the reconnect fix must not regress: a one-off garbage frag
// value mid-occupancy (no userid handoff) is rejected and the baseline is
// held, so the correcting update produces the right cumulative delta.
func TestFragUpdate_TransientCorruptionStillRejected(t *testing.T) {
	a := NewTimelineAnalyzer()
	a.timing.Started = true
	feed := func(ev events.Event) { _ = a.OnEvent(ev) }
	feed(&events.UserInfoEvent{Player: &events.PlayerInfo{Slot: 3, UserID: 42, Name: "p", Team: "red"}, Time: 0})

	// Climb to 9 the honest way (+1 each), so the baseline is legitimately 9.
	for f := 1; f <= 9; f++ {
		feed(&events.FragUpdateEvent{PlayerNum: 3, Frags: f, Time: float64(f)})
	}
	feed(&events.FragUpdateEvent{PlayerNum: 3, Frags: 272, Time: 100}) // garbage: rejected, baseline held at 9
	feed(&events.FragUpdateEvent{PlayerNum: 3, Frags: 10, Time: 101})  // +1 from the held 9: accepted

	n, sum := sumDeltasForSlot(a, 3)
	if n != 10 || sum != 10 {
		t.Errorf("got %d events summing %d, want 10/10 (nine +1s plus the correction; garbage dropped)", n, sum)
	}
}
