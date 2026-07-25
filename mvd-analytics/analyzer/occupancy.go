package analyzer

import (
	"math"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// Wire-slot occupancy tracking, shared by every analyser that has to tell
// one occupant of a client slot from the next.
//
// A QuakeWorld server addresses clients by slot index, and a slot is
// recycled: when a client leaves, the next connection can land on the same
// index. Anything keyed on the slot alone (frags, item state, streams)
// therefore attributes the previous occupant's data to the new one unless
// the handover is modelled explicitly. Two wire signals bound an
// occupancy, and both are needed:
//
//   - a *userid change* on the slot's svc_updateuserinfo — a fresh
//     connection took the slot (mvdsv hands out a unique userid per
//     connection, SV_GenerateUserID, mvdsv/src/sv_main.c:540);
//   - a *vacate* — the empty-userinfo broadcast the server sends when it
//     drops a client (see events.UserInfoEvent.Vacated). This one is the
//     only signal available when nobody takes the freed slot afterwards,
//     which is the common case for a timeout near the end of a match.
//
// A userid of 0 is a resend artefact (some servers null the id on a
// userinfo rebroadcast) and never splits an occupancy — it is adopted into
// the open record instead, mirroring the timeline's "first valid UserID
// wins".
//
// The tracker is deliberately dumb: it records boundaries and the latest
// userinfo scalars, and leaves interpretation (identity unification,
// scoring, participation) to its callers. IdentityAnalyzer and
// MatchAnalyzer each keep one so the two cannot disagree on where an
// occupancy ends.

// occupancyRecord is one contiguous tenancy of a wire client slot.
//
// Scalars are copied off events.PlayerInfo rather than referenced: the
// parser mutates the same *PlayerInfo in place on the next occupancy
// (mvd-reader/parser/userinfo.go:72-82), so a retained pointer would later
// read the *next* player's identity.
type occupancyRecord struct {
	slot    int
	userID  int
	startMs int32
	endMs   int32 // math.MaxInt32 while open

	name string // latest non-empty netname seen this occupancy
	team string // latest non-empty team
	auth string // latest non-empty *auth login

	// spectator is the latest *spectator flag; sawInfo records whether any
	// userinfo was observed at all and sawPlayer whether one was ever seen
	// NOT flagged as a spectator. The end-of-occupancy flag alone is not a
	// participation test: a player who goes spectator after the match still
	// played it. An occupancy with no userinfo at all (sawInfo false) says
	// nothing either way.
	spectator bool
	sawInfo   bool
	sawPlayer bool

	// vacated marks an occupancy the server ended by dropping the client
	// (as opposed to one that merely ran to the end of the demo, or was
	// superseded by a new connection on the same slot).
	vacated bool
}

// spectatorThroughout reports whether every userinfo seen for this
// occupancy flagged its client as a spectator. False when no userinfo was
// seen at all — absence of the flag is not evidence of spectating.
func (r *occupancyRecord) spectatorThroughout() bool { return r.sawInfo && !r.sawPlayer }

// open reports whether the occupancy has no recorded end yet.
func (r *occupancyRecord) open() bool { return r.endMs == math.MaxInt32 }

// covers reports whether tMs falls inside the half-open occupancy window.
func (r *occupancyRecord) covers(tMs int32) bool {
	return tMs >= r.startMs && tMs < r.endMs
}

// occupancyTracker follows the tenancy of every client slot across a demo.
type occupancyTracker struct {
	cur     map[int]*occupancyRecord // slot -> currently open record
	records []*occupancyRecord       // every record, in observation order
}

func newOccupancyTracker() *occupancyTracker {
	return &occupancyTracker{cur: make(map[int]*occupancyRecord)}
}

// current returns the open record for slot, or nil when the slot is empty
// (never occupied, or dropped and not yet retaken). A dropped record stays
// parked internally so the same connection can resume it — see
// onUserInfo — but it is not the slot's current occupant.
func (t *occupancyTracker) current(slot int) *occupancyRecord {
	if r := t.cur[slot]; r != nil && r.open() {
		return r
	}
	return nil
}

// all returns every record in observation order.
func (t *occupancyTracker) all() []*occupancyRecord { return t.records }

// onUserInfo applies one svc_updateuserinfo / svc_setinfo. It returns the
// records it closed, opened and reopened, any of which may be nil:
//
//	(nil, rec, nil)  a slot became occupied
//	(old, new, nil)  a new connection took over an occupied slot
//	(old, nil, nil)  the server dropped the slot's client
//	(nil, nil, rec)  a dropped client's own userid came back on the slot,
//	                 so the drop did not end the occupancy after all
//	(nil, nil, nil)  a plain userinfo update to the open occupancy
func (t *occupancyTracker) onUserInfo(e *events.UserInfoEvent) (closed, opened, reopened *occupancyRecord) {
	if e == nil || e.Player == nil {
		return nil, nil, nil
	}
	slot := e.Player.Slot
	if slot < 0 || slot >= events.MaxClients {
		return nil, nil, nil
	}
	cur := t.cur[slot]

	uid := e.Player.UserID

	if e.Vacated {
		// The server dropped this slot's client — but only when the update
		// carries the client's own userid. SV_DropClient leaves userid set
		// and SV_FullClientUpdate writes it (mvdsv/src/sv_main.c:419-428,
		// :487-513), whereas an empty userinfo with userid 0 is a resend
		// artefact of the same shape as the userid-0 rule above: 2002-era
		// servers periodically re-broadcast every occupied slot as
		// `svc_updateuserinfo <slot> 0 ""` immediately followed by the real
		// string (observed at t=25867 and t=87091 on
		// demo-test-data/mvd/special-cases/4on4_l_vs_la[e1m2].mvd, for all
		// eight players at once). Treating those as drops would shred every
		// occupancy on the demo.
		//
		// A vacate on an already-empty slot is the MVD header's full-state
		// block enumerating free slots and carries no information.
		if cur == nil || uid == 0 || !cur.open() {
			return nil, nil, nil
		}
		cur.endMs = e.TimeMs
		cur.vacated = true
		return cur, nil, nil
	}

	// A vacated record stays parked on the slot so the *same* connection
	// coming back can resume it. A userid is per-connection
	// (SV_GenerateUserID), so an update carrying the departed client's own
	// id means the drop signal did not in fact end the occupancy — seen on
	// gameId 216835, where slot 7's userinfo is re-broadcast 72 s after
	// rusti's drop. Splitting there would leave the slot unowned across the
	// gap and silently drop whatever the wire still says about it.
	if cur != nil && !cur.open() {
		if uid == 0 || uid == cur.userID {
			cur.endMs = math.MaxInt32
			cur.vacated = false
			t.note(cur, e.Player)
			return nil, nil, cur
		}
		delete(t.cur, slot)
		cur = nil
	}

	if cur == nil {
		return nil, t.open(slot, uid, e.Player, e.TimeMs), nil
	}
	if uid != 0 && cur.userID != 0 && uid != cur.userID {
		cur.endMs = e.TimeMs
		return cur, t.open(slot, uid, e.Player, e.TimeMs), nil
	}
	if cur.userID == 0 && uid != 0 {
		cur.userID = uid
	}
	t.note(cur, e.Player)
	return nil, nil, nil
}

// ensure returns the open record for slot, opening an anonymous one at tMs
// when the slot has no userinfo yet. Callers that key non-userinfo events
// (frags, spawns, positions) on an occupancy use this so a slot whose
// userinfo never arrived — or a hand-built unit-test event stream — still
// gets exactly one record.
func (t *occupancyTracker) ensure(slot int, tMs int32) *occupancyRecord {
	if slot < 0 || slot >= events.MaxClients {
		return nil
	}
	if cur := t.cur[slot]; cur != nil && cur.open() {
		return cur
	}
	return t.open(slot, 0, nil, tMs)
}

// closeOpen closes every still-open record at endMs. Called at Finalize so
// consumers see complete windows.
func (t *occupancyTracker) closeOpen(endMs int32) {
	for _, r := range t.records {
		if r.open() {
			r.endMs = endMs
		}
	}
}

func (t *occupancyTracker) open(slot, uid int, p *events.PlayerInfo, tMs int32) *occupancyRecord {
	r := &occupancyRecord{slot: slot, userID: uid, startMs: tMs, endMs: math.MaxInt32}
	t.note(r, p)
	t.cur[slot] = r
	t.records = append(t.records, r)
	return r
}

// note folds one userinfo snapshot into a record. Name/team/auth are
// carry-forward (a userinfo resend can omit a key), the spectator flag is
// absolute — svc_updateuserinfo replaces the whole string, so an absent
// *spectator key means "not a spectator" (parseUserInfoString,
// mvd-reader/parser/userinfo.go:152-157).
func (t *occupancyTracker) note(r *occupancyRecord, p *events.PlayerInfo) {
	if p == nil {
		return
	}
	if p.Name != "" {
		r.name = p.Name
	}
	if p.Team != "" {
		r.team = p.Team
	}
	if p.Auth != "" {
		r.auth = p.Auth
	}
	r.spectator = p.Spectator
	r.sawInfo = true
	if !p.Spectator {
		r.sawPlayer = true
	}
}
