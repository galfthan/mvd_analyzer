package analyzer

import (
	"math"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

func ui(slot, uid int, name string, tMs int32) *events.UserInfoEvent {
	return &events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: slot, UserID: uid, Name: name},
		TimeMs: tMs,
	}
}

func vacate(slot, uid int, name string, tMs int32) *events.UserInfoEvent {
	e := ui(slot, uid, name, tMs)
	e.Vacated = true
	return e
}

// A fresh connection on a slot ends the previous occupancy and starts a
// new one; a userid-0 resend does neither.
func TestOccupancyTracker_UserIDChangeSplits(t *testing.T) {
	tr := newOccupancyTracker()

	if closed, opened, _ := tr.onUserInfo(ui(7, 4948, "shiva", 0)); closed != nil || opened == nil {
		t.Fatalf("first userinfo: closed=%v opened=%v, want (nil, rec)", closed, opened)
	}
	// Resend with userid 0 — same occupancy.
	if closed, opened, _ := tr.onUserInfo(ui(7, 0, "shiva", 25867)); closed != nil || opened != nil {
		t.Errorf("userid-0 resend split the occupancy: closed=%v opened=%v", closed, opened)
	}
	closed, opened, _ := tr.onUserInfo(ui(7, 5796, "Sectoid", 1114326))
	if closed == nil || opened == nil {
		t.Fatalf("userid change: closed=%v opened=%v, want both", closed, opened)
	}
	if closed.name != "shiva" || closed.endMs != 1114326 {
		t.Errorf("closed = %+v, want shiva ending at 1114326", closed)
	}
	if opened.name != "Sectoid" || opened.startMs != 1114326 {
		t.Errorf("opened = %+v, want Sectoid starting at 1114326", opened)
	}
	if n := len(tr.all()); n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}

// The drop broadcast (empty userinfo carrying the client's own userid)
// ends the occupancy even though nobody takes the slot afterwards.
func TestOccupancyTracker_VacateEndsOccupancy(t *testing.T) {
	tr := newOccupancyTracker()
	tr.onUserInfo(ui(13, 5046, "DARKLORD", 0))

	closed, opened, _ := tr.onUserInfo(vacate(13, 5046, "DARKLORD", 1088539))
	if closed == nil || opened != nil {
		t.Fatalf("vacate: closed=%v opened=%v, want (rec, nil)", closed, opened)
	}
	if !closed.vacated || closed.endMs != 1088539 {
		t.Errorf("closed = %+v, want vacated at 1088539", closed)
	}
	if tr.current(13) != nil {
		t.Errorf("current(13) = %+v, want nil — the slot is empty", tr.current(13))
	}
}

// Two empty-userinfo shapes are NOT drops and must leave the occupancy
// alone: the MVD header's enumeration of free slots, and the userid-0
// re-broadcast old servers send for every occupied slot (t=25867 on
// 4on4_l_vs_la[e1m2] does it for all eight players at once).
func TestOccupancyTracker_VacateIgnoredWithoutUserID(t *testing.T) {
	tr := newOccupancyTracker()

	// Free slot in the header block.
	if closed, opened, _ := tr.onUserInfo(vacate(20, 0, "", 0)); closed != nil || opened != nil {
		t.Errorf("vacate on an empty slot produced closed=%v opened=%v", closed, opened)
	}
	if n := len(tr.all()); n != 0 {
		t.Errorf("records = %d, want 0 — a free slot is not an occupancy", n)
	}

	tr.onUserInfo(ui(1, 5100, "space", 0))
	if closed, _, _ := tr.onUserInfo(vacate(1, 0, "space", 25867)); closed != nil {
		t.Errorf("userid-0 empty userinfo closed the occupancy: %+v", closed)
	}
	if cur := tr.current(1); cur == nil || !cur.open() {
		t.Errorf("current(1) = %+v, want the still-open occupancy", cur)
	}
}

// A userid is per-connection, so the departed client's own id coming back
// on the slot means the drop did not end the occupancy (gameId 216835
// re-broadcasts slot 7's userinfo 72 s after rusti's drop). Resuming keeps
// the slot owned across the gap instead of leaving a hole nothing resolves
// in.
func TestOccupancyTracker_SameUserIDReopens(t *testing.T) {
	tr := newOccupancyTracker()
	tr.onUserInfo(ui(7, 8, "rusti", 0))
	tr.onUserInfo(vacate(7, 8, "rusti", 613452))

	closed, opened, reopened := tr.onUserInfo(ui(7, 8, "rusti", 685676))
	if closed != nil || opened != nil || reopened == nil {
		t.Fatalf("same-userid return: closed=%v opened=%v reopened=%v, want (nil,nil,rec)", closed, opened, reopened)
	}
	if reopened.endMs != math.MaxInt32 || reopened.vacated {
		t.Errorf("reopened = %+v, want an open, non-vacated record", reopened)
	}
	if n := len(tr.all()); n != 1 {
		t.Errorf("records = %d, want 1 — the occupancy never actually ended", n)
	}

	// A *different* userid after the drop is a genuine new occupant.
	tr2 := newOccupancyTracker()
	tr2.onUserInfo(ui(7, 8, "rusti", 0))
	tr2.onUserInfo(vacate(7, 8, "rusti", 613452))
	if _, opened, _ := tr2.onUserInfo(ui(7, 15, "Luk", 700000)); opened == nil {
		t.Errorf("a new userid after a drop did not open a new occupancy")
	}
	if n := len(tr2.all()); n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}

// spectatorThroughout only fires when userinfo was actually seen, and a
// single non-spectator sighting is enough to call the occupancy a player's
// — a participant who goes spectator after the match still played it.
func TestOccupancyRecord_SpectatorThroughout(t *testing.T) {
	tr := newOccupancyTracker()
	rec := tr.ensure(4, 0)
	if rec.spectatorThroughout() {
		t.Errorf("an occupancy with no userinfo must not read as a spectator")
	}

	spec := &events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 2, UserID: 142, Name: "adm<ego", Spectator: true},
	}
	tr.onUserInfo(spec)
	if got := tr.current(2); got == nil || !got.spectatorThroughout() {
		t.Errorf("current(2) = %+v, want a spectator-throughout occupancy", got)
	}

	tr.onUserInfo(ui(5, 35, "wd.dilbert", 0))
	postMatchSpec := &events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 5, UserID: 35, Name: "wd.dilbert", Spectator: true},
		TimeMs: 613971,
	}
	tr.onUserInfo(postMatchSpec)
	if got := tr.current(5); got == nil || got.spectatorThroughout() {
		t.Errorf("current(5) = %+v, want a player who merely ended as a spectator", got)
	}
}
