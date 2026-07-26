package analyzer

import (
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

// setinfo builds the event parseSetInfo synthesises for one key/value:
// the Player snapshot is the parser's cache, so uid is whatever the last
// full userinfo left on the slot.
func setinfo(slot, cachedUID int, cachedName string, tMs int32) *events.UserInfoEvent {
	e := ui(slot, cachedUID, cachedName, tMs)
	e.Partial = true
	return e
}

// A fresh connection on a slot ends the previous occupancy and starts a
// new one; a userid-0 resend does neither.
func TestOccupancyTracker_UserIDChangeSplits(t *testing.T) {
	tr := newOccupancyTracker()

	if closed, opened := tr.onUserInfo(ui(7, 4948, "shiva", 0)); closed != nil || opened == nil {
		t.Fatalf("first userinfo: closed=%v opened=%v, want (nil, rec)", closed, opened)
	}
	// Resend with userid 0 — same occupancy.
	if closed, opened := tr.onUserInfo(ui(7, 0, "shiva", 25867)); closed != nil || opened != nil {
		t.Errorf("userid-0 resend split the occupancy: closed=%v opened=%v", closed, opened)
	}
	closed, opened := tr.onUserInfo(ui(7, 5796, "Sectoid", 1114326))
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

	closed, opened := tr.onUserInfo(vacate(13, 5046, "DARKLORD", 1088539))
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
// client-table replay old servers send for every occupied slot (t=25867 on
// 4on4_l_vs_la[e1m2] does it for all eight players at once).
func TestOccupancyTracker_VacateIgnoredWithoutUserID(t *testing.T) {
	tr := newOccupancyTracker()

	// Free slot in the header block.
	if closed, opened := tr.onUserInfo(vacate(20, 0, "", 0)); closed != nil || opened != nil {
		t.Errorf("vacate on an empty slot produced closed=%v opened=%v", closed, opened)
	}
	if n := len(tr.all()); n != 0 {
		t.Errorf("records = %d, want 0 — a free slot is not an occupancy", n)
	}

	tr.onUserInfo(ui(1, 5100, "space", 0))
	if closed, _ := tr.onUserInfo(vacate(1, 0, "space", 25867)); closed != nil {
		t.Errorf("userid-0 empty userinfo closed the occupancy: %+v", closed)
	}
	if cur := tr.current(1); cur == nil || !cur.open() {
		t.Errorf("current(1) = %+v, want the still-open occupancy", cur)
	}
}

// A drop is a drop. The events that used to look like "the same connection
// came back" are svc_setinfo syntheses replaying the parser's CACHED
// userid, and mvdsv emits one during the NEXT client's connect handshake
// (SV_Login -> SV_Logout -> `svc_setinfo <slot> "*auth" ""`,
// mvdsv/src/sv_login.c:588 and :644-646). Reopening on them erased a real
// departure on five of the local demos.
//
// Ground truth for the shape, hub gameId 216835 slot 7: two
// svc_updateuserinfo only (rusti's at t=0 and the empty drop at t=613452);
// the t=685676 message is `svc_setinfo 7 "*auth" ""`, and Luk's real
// userinfo does not arrive until t=766898.
func TestOccupancyTracker_SetInfoNeverReopensADrop(t *testing.T) {
	tr := newOccupancyTracker()
	tr.onUserInfo(ui(7, 8, "rusti", 0))
	tr.onUserInfo(vacate(7, 8, "rusti", 613452))

	if closed, opened := tr.onUserInfo(setinfo(7, 8, "rusti", 685676)); closed != nil || opened != nil {
		t.Fatalf("setinfo after a drop: closed=%v opened=%v, want (nil, nil)", closed, opened)
	}
	if cur := tr.current(7); cur != nil {
		t.Errorf("current(7) = %+v, want nil — the slot is empty until someone connects", cur)
	}
	rec := tr.all()[0]
	if !rec.vacated || rec.endMs != 613452 {
		t.Errorf("rusti's record = %+v, want it still closed as vacated at 613452", rec)
	}

	// The genuine next occupant still opens a record of their own.
	_, opened := tr.onUserInfo(ui(7, 15, "Luk", 766898))
	if opened == nil || opened.name != "Luk" || opened.startMs != 766898 {
		t.Fatalf("Luk's userinfo opened %+v, want a record starting at 766898", opened)
	}
	if n := len(tr.all()); n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}

// Even the departed client's own userid arriving on a full userinfo is a
// new occupancy: mvdsv recycles userids out of a 1..99 pool and only checks
// them against clients that are not cs_free (SV_GenerateUserID,
// mvdsv/src/sv_main.c:538-556), so equality across a gap proves nothing.
func TestOccupancyTracker_UserInfoAfterDropOpensNewRecord(t *testing.T) {
	tr := newOccupancyTracker()
	tr.onUserInfo(ui(7, 8, "rusti", 0))
	tr.onUserInfo(vacate(7, 8, "rusti", 613452))

	closed, opened := tr.onUserInfo(ui(7, 8, "someone else", 700000))
	if closed != nil {
		t.Errorf("closed = %+v, want nil — the previous record was already closed by the drop", closed)
	}
	if opened == nil || opened.startMs != 700000 {
		t.Fatalf("opened = %+v, want a new record starting at 700000", opened)
	}
	if n := len(tr.all()); n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}

// A setinfo DOES refine the occupant who currently holds the slot — that is
// the mid-stint rename / team switch parseSetInfo exists for.
func TestOccupancyTracker_SetInfoUpdatesTheOpenOccupancy(t *testing.T) {
	tr := newOccupancyTracker()
	tr.onUserInfo(ui(3, 4609, "dag", 0))

	renamed := setinfo(3, 4609, "dag2", 5000)
	renamed.Player.Team = ".la."
	if closed, opened := tr.onUserInfo(renamed); closed != nil || opened != nil {
		t.Fatalf("setinfo rename: closed=%v opened=%v, want (nil, nil)", closed, opened)
	}
	cur := tr.current(3)
	if cur == nil || cur.name != "dag2" || cur.team != ".la." {
		t.Errorf("current(3) = %+v, want the same record renamed to dag2 on .la.", cur)
	}
	if n := len(tr.all()); n != 1 {
		t.Errorf("records = %d, want 1", n)
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
