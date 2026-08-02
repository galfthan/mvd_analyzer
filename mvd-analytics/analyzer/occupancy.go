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
//     connection took the slot (SV_GenerateUserID, mvdsv/src/sv_main.c:538-556,
//     hands out ids from a rotating pool and checks uniqueness only against
//     clients that are not cs_free — so a userid is unique among *live*
//     connections, not unique over the demo, and a freed slot's id can be
//     reissued. A change of userid is still a reliable handover signal;
//     equality across a gap is not evidence of the same connection. The
//     pool's 1..99 range is modern-mvdsv only — 2002-era demos carry
//     four-digit ids — so the only portable claim is that a real
//     connection's userid is non-zero);
//   - a *vacate* — the empty-userinfo broadcast the server sends when it
//     drops a client (see events.UserInfoEvent.Vacated). This one is the
//     only signal available when nobody takes the freed slot afterwards,
//     which is the common case for a timeout near the end of a match.
//
// A drop ends an occupancy, full stop. There is no "the client came back
// on the same userid so the drop did not count" rule: the wire never
// re-broadcasts a dropped client's userinfo, and the events that look like
// it are svc_setinfo syntheses carrying the parser's cached userid (see
// events.UserInfoEvent.Partial). Such events are excluded here from
// creating, closing or resuming a record.
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
// (mvd-reader/parser/userinfo.go:120-127), so a retained pointer would
// later read the *next* player's identity.
type occupancyRecord struct {
	slot    int
	userID  int
	startMs int32
	endMs   int32 // math.MaxInt32 while open

	// uidStartMs is when the wire first attested THIS record's userid. It
	// equals startMs for a record opened by a userinfo that carried one,
	// and is later for a record opened unidentified (userid 0) that adopted
	// one afterwards — see the adoption branch in onUserInfo. Meaningless
	// (and unread) while userID is still 0.
	//
	// The gap between startMs and uidStartMs is not cosmetic: KTX's ghost
	// scoreboard row opens an occupancy with userid 0 (ghost2scores,
	// ktx/src/g_utils.c:2272-2356), and the next real connection on that
	// slot is adopted into it rather than splitting it. The record then
	// spans a window that begins before the connection it names — verified
	// on gameId 222649 slot 12, where the ghost of a dropped bogojoker
	// opens at 141832 and herbie's uid-26 connection is folded in later.
	// Anything PUBLISHING a session window must report from uidStartMs
	// (see attestedStartMs), not startMs, or it claims a userid was live
	// before the wire ever mentioned it.
	uidStartMs int32

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

// identified reports whether the wire gave this occupancy a userid of its
// own — i.e. whether it is a client connection rather than something that
// merely addressed the slot. Three things land in a record with userid 0:
// an occupancy `ensure` opened because a frag or a position arrived on a
// slot with no userinfo, a userid-0 resend, and KTX's ghost scoreboard
// entry (see the note in match.go's rowForKey). None of them is evidence of
// a distinct connection.
func (r *occupancyRecord) identified() bool { return r.userID != 0 }

// attestedStartMs is the earliest time the wire attested this occupancy's
// *connection* — startMs normally, but the adoption time when the record
// was opened by something that carried no userid of its own and picked one
// up later (see uidStartMs). It is what a published session window starts
// at; startMs is what internal resolution keys on, because an event on the
// slot before the adoption still belongs to this record.
func (r *occupancyRecord) attestedStartMs() int32 {
	if r.identified() && r.uidStartMs > r.startMs {
		return r.uidStartMs
	}
	return r.startMs
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
// (never occupied, or dropped and not yet retaken).
func (t *occupancyTracker) current(slot int) *occupancyRecord {
	if r := t.cur[slot]; r != nil && r.open() {
		return r
	}
	return nil
}

// all returns every record in observation order.
func (t *occupancyTracker) all() []*occupancyRecord { return t.records }

// countForSlot returns how many occupancies the slot has had so far. Its
// one caller is MatchAnalyzer.soleOccupancy, which tests for exactly 1:
// anything else means the slot changed hands, so slot-keyed state (the
// caller's SlotName / ctx.Players tables) names the wrong occupant.
func (t *occupancyTracker) countForSlot(slot int) int {
	n := 0
	for _, r := range t.records {
		if r.slot == slot {
			n++
		}
	}
	return n
}

// onUserInfo applies one svc_updateuserinfo / svc_setinfo. It returns the
// records it closed and opened, either of which may be nil:
//
//	(nil, rec)  a slot became occupied
//	(old, new)  a new connection took over an occupied slot
//	(old, nil)  the server dropped the slot's client
//	(nil, nil)  a plain userinfo update to the open occupancy, or an event
//	            that carries no occupancy information at all
func (t *occupancyTracker) onUserInfo(e *events.UserInfoEvent) (closed, opened *occupancyRecord) {
	if e == nil || e.Player == nil {
		return nil, nil
	}
	slot := e.Player.Slot
	if slot < 0 || slot >= events.MaxClients {
		return nil, nil
	}
	cur := t.current(slot)

	uid := e.Player.UserID

	// An svc_setinfo synthesis carries the parser's cached scalars, not a
	// wire snapshot (events.UserInfoEvent.Partial). It may refine whoever
	// currently holds the slot — a mid-stint rename or team switch — but it
	// can never open, close or resume an occupancy, because its userid was
	// never on the wire and mvdsv emits one (`*auth` cleared by SV_Logout,
	// sv_login.c:644-646) both for the client being dropped and for the
	// *next* client's connect handshake.
	if e.Partial {
		if cur != nil {
			t.note(cur, e.Player)
		}
		return nil, nil
	}

	if e.Vacated {
		// The server dropped this slot's client — but only when the update
		// carries the client's own userid. SV_DropClient leaves userid set
		// and SV_FullClientUpdate writes it (mvdsv/src/sv_main.c:419-428,
		// :509-511), whereas an empty userinfo with userid 0 is the
		// per-client client-table replay described on
		// events.UserInfoEvent.Vacated: every occupied slot emptied for one
		// dem_single frame and immediately restated (25 times on
		// demo-test-data/mvd/special-cases/4on4_l_vs_la[e1m2].mvd, all eight
		// players at once). Treating those as drops would shred every
		// occupancy on the demo.
		//
		// A vacate on an already-empty slot is the MVD header's full-state
		// block enumerating free slots and carries no information.
		if cur == nil || uid == 0 {
			return nil, nil
		}
		cur.endMs = e.TimeMs
		cur.vacated = true
		return cur, nil
	}

	if cur == nil {
		return nil, t.open(slot, uid, e.Player, e.TimeMs)
	}
	if uid != 0 && cur.userID != 0 && uid != cur.userID {
		cur.endMs = e.TimeMs
		return cur, t.open(slot, uid, e.Player, e.TimeMs)
	}
	if cur.userID == 0 && uid != 0 {
		cur.userID = uid
		cur.uidStartMs = e.TimeMs
	}
	t.note(cur, e.Player)
	return nil, nil
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
	r := &occupancyRecord{slot: slot, userID: uid, startMs: tMs, uidStartMs: tMs, endMs: math.MaxInt32}
	t.note(r, p)
	t.cur[slot] = r
	t.records = append(t.records, r)
	return r
}

// note folds one userinfo snapshot into a record. Name/team/auth are
// carry-forward (a userinfo resend can omit a key), the spectator flag is
// absolute — svc_updateuserinfo replaces the whole string, so an absent
// *spectator key means "not a spectator" (parseUserInfoString resets it at
// mvd-reader/parser/userinfo.go:204 and only the `*spectator` key at
// :231-237 can set it again).
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
