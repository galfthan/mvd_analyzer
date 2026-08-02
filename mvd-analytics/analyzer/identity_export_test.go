package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// The published identity export (schema v66): result.PlayerStream.Identity
// plus the per-connection Sessions list, mirrored onto
// playerStats.players[]. The contract these tests pin is what a consumer
// reads it as — "these two rows are one person", and "this row was slot S /
// userid U during [t1,t2)" — which is exactly what a third-party client had
// resorted to rebuilding with a fuzzy name matcher.

// exportSessions drives the timeline's stream builder over one slot of
// synthetic play and returns the single emitted stream.
func exportSessions(t *testing.T, sessions map[int][]ResolvedSession, play map[int][2]int32, matchEndMs int32) *result.Streams {
	t.Helper()
	a := NewTimelineAnalyzer()
	a.timing.Started = true
	for slot, span := range play {
		a.playerState[slot] = newStreamState(span[0], span[1])
	}
	a.UseCoreOutputs(&CoreOutputs{Sessions: sessions})
	return a.buildStreamsResult(nil, nil, 0, matchEndMs)
}

func streamNamed(t *testing.T, s *result.Streams, name string) result.PlayerStream {
	t.Helper()
	if s == nil {
		t.Fatal("nil streams")
	}
	for _, p := range s.Players {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no stream named %q (have %d)", name, len(s.Players))
	return result.PlayerStream{}
}

// A client still connected when the recording stopped has no wire event to
// close its window, so the published end is match end — the same place
// every other open interval in Streams closes. Anything else would either
// leak MaxInt32 into JSON or invent a departure.
func TestSessionExport_OpenEndClosesAtMatchEnd(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			7: {{
				StartMs: minInt32, EndMs: maxInt32,
				OccStartMs: 12_000, OccEndMs: maxInt32,
				Name: "rusti", WireName: "rusti", UserID: 8, IdentityKey: "s7u8",
			}},
		},
		map[int][2]int32{7: {0, 600_000}}, 600_000)

	p := streamNamed(t, streams, "rusti")
	if p.Identity != "s7u8" {
		t.Errorf("identity = %q, want s7u8", p.Identity)
	}
	if len(p.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(p.Sessions))
	}
	if got := p.Sessions[0]; got.StartMs != 12_000 || got.EndMs != 600_000 {
		t.Errorf("session window = [%d,%d), want [12000,600000) — observed start, match-end close",
			got.StartMs, got.EndMs)
	}
	if got := p.Sessions[0]; got.Slot != 7 || got.UserID != 8 || got.Name != "rusti" {
		t.Errorf("session = %+v, want slot 7 / uid 8 / wire name rusti", got)
	}
}

// One human, two connections: the reconnect case the whole feature exists
// for. One identity, two windows, two userids, in time order.
func TestSessionExport_ReconnectIsOneIdentityTwoSessions(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			// Reconnect onto a different slot (gameId 216835's shape).
			7: {{
				StartMs: minInt32, EndMs: 603_204,
				OccStartMs: 0, OccEndMs: 603_204,
				Name: "rusti", WireName: "rusti", UserID: 8, IdentityKey: "s7u8",
			}},
			2: {{
				StartMs: 603_204, EndMs: maxInt32,
				OccStartMs: 603_204, OccEndMs: maxInt32,
				// mvdsv renames a connection that collides with a name
				// already on the server, so the wire name of the second
				// connection differs from the identity's canonical one.
				Name: "rusti", WireName: "(1)rusti", UserID: 14, IdentityKey: "s7u8",
			}},
		},
		map[int][2]int32{7: {0, 600_000}, 2: {610_000, 1_200_000}}, 1_200_000)

	if n := len(streams.Players); n != 1 {
		t.Fatalf("want one merged row, got %d", n)
	}
	p := streamNamed(t, streams, "rusti")
	if len(p.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want 2", p.Sessions)
	}
	// Each session reports the name that was ON THE WIRE during it — the
	// point of publishing WireName beside the canonical Name. A consumer
	// joining these windows against a live roster at some instant needs
	// the name the engine had then.
	if p.Sessions[0].Name != "rusti" || p.Sessions[1].Name != "(1)rusti" {
		t.Errorf("session names = %q,%q, want rusti then (1)rusti (the wire names, not the canonical one)",
			p.Sessions[0].Name, p.Sessions[1].Name)
	}
	if p.Sessions[0].UserID != 8 || p.Sessions[1].UserID != 14 {
		t.Errorf("userids = %d,%d, want 8 then 14 (time order)", p.Sessions[0].UserID, p.Sessions[1].UserID)
	}
	if p.Sessions[0].Slot != 7 || p.Sessions[1].Slot != 2 {
		t.Errorf("slots = %d,%d, want 7 then 2", p.Sessions[0].Slot, p.Sessions[1].Slot)
	}
}

// Two rows that are NOT one person keep distinct identities. gameId
// 220637: mvdsv renamed the second connection `(1)rusti (FU)` and KTX
// scored the two separately, so merging them would contradict the
// authoritative record.
func TestSessionExport_UnrelatedRowsKeepDistinctIdentities(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			5: {{
				StartMs: minInt32, EndMs: 620_599,
				OccStartMs: 0, OccEndMs: 620_599,
				Name: "rusti (FU)", WireName: "rusti (FU)", UserID: 37, IdentityKey: "s5u37",
			}},
			9: {{
				StartMs: 609_644, EndMs: maxInt32,
				OccStartMs: 609_644, OccEndMs: maxInt32,
				Name: "(1)rusti (FU)", WireName: "(1)rusti (FU)", UserID: 43, IdentityKey: "s9u43",
			}},
		},
		map[int][2]int32{5: {0, 600_000}, 9: {700_000, 1_200_000}}, 1_200_000)

	a := streamNamed(t, streams, "rusti (FU)")
	b := streamNamed(t, streams, "(1)rusti (FU)")
	if a.Identity == b.Identity {
		t.Errorf("both rows carry identity %q — they are two humans by KTX's own reckoning", a.Identity)
	}
	if a.Sessions[0].UserID != 37 || b.Sessions[0].UserID != 43 {
		t.Errorf("userids = %d / %d, want 37 / 43", a.Sessions[0].UserID, b.Sessions[0].UserID)
	}
}

// An occupancy the wire never gave a userid is not a connection and is not
// published. KTX's ghost scoreboard row (userid hardcoded 0) carries the
// departed player's name, so the unifier folds it into that player — on
// gameId 216835 it would otherwise add a userid-less slot-10 window
// OVERLAPPING the slot rusti had actually reconnected onto.
func TestSessionExport_UnidentifiedOccupancyIsNotPublished(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			7: {{
				StartMs: minInt32, EndMs: 603_204,
				OccStartMs: 0, OccEndMs: 603_204,
				Name: "rusti", WireName: "rusti", UserID: 8, IdentityKey: "s7u8",
			}},
			10: {{ // the KTX ghost edict, same identity, no userid
				StartMs: 603_204, EndMs: maxInt32,
				OccStartMs: 603_204, OccEndMs: maxInt32,
				Name: "rusti", WireName: "# rusti", UserID: 0, IdentityKey: "s7u8",
			}},
		},
		map[int][2]int32{7: {0, 600_000}, 10: {700_000, 1_200_000}}, 1_200_000)

	p := streamNamed(t, streams, "rusti")
	if len(p.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want only the real connection", p.Sessions)
	}
	if p.Sessions[0].UserID != 8 {
		t.Errorf("published session userid = %d, want 8", p.Sessions[0].UserID)
	}
	if p.Identity == "" {
		t.Error("identity must survive even when a session is withheld")
	}
}

// A connection first attested AFTER the match ended is not a trackable
// in-match window, and publishing it would emit StartMs > EndMs (windows
// close at match end), contradicting the half-open contract. The shape is
// real: a spectator connecting postgame to say gg, whom the identity
// unifier folds onto a player who left mid-match.
func TestSessionExport_PostgameConnectionIsWithheld(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			7: {{
				StartMs: minInt32, EndMs: 610_000,
				OccStartMs: 0, OccEndMs: 590_000,
				Name: "rusti", WireName: "rusti", UserID: 8, IdentityKey: "s7u8",
			}},
			3: {{ // same human, reconnects after the match is over
				StartMs: 610_000, EndMs: maxInt32,
				OccStartMs: 610_000, OccEndMs: maxInt32,
				Name: "rusti", WireName: "rusti", UserID: 21, IdentityKey: "s7u8",
			}},
		},
		map[int][2]int32{7: {0, 590_000}, 3: {610_000, 620_000}}, 600_000)

	p := streamNamed(t, streams, "rusti")
	if len(p.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want only the in-match connection", p.Sessions)
	}
	for _, s := range p.Sessions {
		if s.StartMs > s.EndMs {
			t.Errorf("session %+v has StartMs > EndMs — the window contract is half-open [start,end)", s)
		}
	}
	if p.Sessions[0].UserID != 8 {
		t.Errorf("published session userid = %d, want 8", p.Sessions[0].UserID)
	}
}

// Three states, and they are distinct: no identity table at all (a
// degraded parse or a registry without the identity analyser) leaves both
// fields absent rather than inventing a key.
func TestSessionExport_NoIdentityTableLeavesBothAbsent(t *testing.T) {
	a := NewTimelineAnalyzer()
	a.timing.Started = true
	a.playerState[7] = newStreamState(0, 600_000)
	streams := a.buildStreamsResult(map[int]string{7: "rusti"}, map[int]string{7: "jah"}, 0, 600_000)

	p := streamNamed(t, streams, "rusti")
	if p.Identity != "" || p.Sessions != nil {
		t.Errorf("identity=%q sessions=%+v, want both absent without an identity table", p.Identity, p.Sessions)
	}
}

// A player whose every occupancy was unidentified keeps the identity (the
// grouping is still real) with an empty session list — "no connection the
// wire named" is a different statement from "no identity".
func TestSessionExport_IdentityWithoutAnyPublishableSession(t *testing.T) {
	streams := exportSessions(t,
		map[int][]ResolvedSession{
			7: {{
				StartMs: minInt32, EndMs: maxInt32,
				OccStartMs: 0, OccEndMs: maxInt32,
				Name: "rusti", WireName: "rusti", UserID: 0, IdentityKey: "s7u0",
			}},
		},
		map[int][2]int32{7: {0, 600_000}}, 600_000)

	p := streamNamed(t, streams, "rusti")
	if p.Identity != "s7u0" {
		t.Errorf("identity = %q, want s7u0", p.Identity)
	}
	if len(p.Sessions) != 0 {
		t.Errorf("sessions = %+v, want none (no occupancy carried a userid)", p.Sessions)
	}
}

// --- the ghost trim, at the occupancy layer ---

// KTX's ghost row opens an occupancy with userid 0 that the next real
// connection is ADOPTED into (occupancy.go's uid-0 rule), so the record
// spans a window beginning before the connection it ends up naming.
// Verified on gameId 222649 slot 12: the ghost of a dropped bogojoker
// opens at 141832 and herbie's uid-26 userinfo lands at 362206 — 3.7
// minutes of a window that is not his. Resolution must keep the early
// bound (an event on the slot in between still belongs to this record);
// the PUBLISHED window must not.
func TestOccupancy_AdoptedRecordAttestsFromItsUserinfo(t *testing.T) {
	occ := newOccupancyTracker()
	occ.onUserInfo(&events.UserInfoEvent{ // KTX ghost: name, no userid
		Player: &events.PlayerInfo{Slot: 12, UserID: 0, Name: "# bogojoker"}, TimeMs: 141_832,
	})
	occ.onUserInfo(&events.UserInfoEvent{ // herbie connects onto the same slot
		Player: &events.PlayerInfo{Slot: 12, UserID: 26, Name: "herbie"}, TimeMs: 362_206,
	})

	recs := occ.all()
	if len(recs) != 1 {
		t.Fatalf("uid-0 adoption should keep one record, got %d", len(recs))
	}
	r := recs[0]
	if r.startMs != 141_832 {
		t.Errorf("startMs = %d, want the record to still resolve from 141832", r.startMs)
	}
	if got := r.attestedStartMs(); got != 362_206 {
		t.Errorf("attestedStartMs = %d, want 362206 — the userinfo that named this connection", got)
	}
}

// A record opened by a userinfo that carried a userid attests from its own
// start; nothing is trimmed in the ordinary case.
func TestOccupancy_IdentifiedRecordAttestsFromStart(t *testing.T) {
	occ := newOccupancyTracker()
	occ.onUserInfo(&events.UserInfoEvent{
		Player: &events.PlayerInfo{Slot: 3, UserID: 9, Name: "rusti"}, TimeMs: 5_000,
	})
	r := occ.all()[0]
	if got := r.attestedStartMs(); got != 5_000 {
		t.Errorf("attestedStartMs = %d, want 5000", got)
	}
}

// End to end through the identity analyser: the ghost window is trimmed
// off the published bound while the lookup bound stays wide.
func TestIdentity_GhostWindowIsNotPublishedAsHerbies(t *testing.T) {
	a := NewIdentityAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, e := range []*events.UserInfoEvent{
		{Player: &events.PlayerInfo{Slot: 9, UserID: 12, Name: "bogojoker"}, TimeMs: 0},
		{Player: &events.PlayerInfo{Slot: 9, UserID: 12, Name: "bogojoker"}, TimeMs: 141_832, Vacated: true},
		{Player: &events.PlayerInfo{Slot: 12, UserID: 0, Name: "# bogojoker"}, TimeMs: 141_832},
		{Player: &events.PlayerInfo{Slot: 9, UserID: 25, Name: "bogojoker"}, TimeMs: 150_269},
		{Player: &events.PlayerInfo{Slot: 12, UserID: 26, Name: "herbie"}, TimeMs: 362_206},
	} {
		if err := a.OnEvent(e); err != nil {
			t.Fatalf("onEvent: %v", err)
		}
	}
	co := &CoreOutputs{}
	a.PopulateCore(co)

	if n := len(co.Sessions[12]); n != 1 {
		t.Fatalf("slot 12 sessions = %d, want 1 (ghost adopted)", n)
	}
	s := co.Sessions[12][0]
	if s.OccStartMs != 362_206 {
		t.Errorf("published start = %d, want 362206 — herbie's own userinfo", s.OccStartMs)
	}
	if s.StartMs != minInt32 {
		t.Errorf("lookup start = %d, want the widened bound left alone", s.StartMs)
	}

	// And the reconnect on slot 9 keeps both real boundaries.
	if n := len(co.Sessions[9]); n != 2 {
		t.Fatalf("slot 9 sessions = %d, want 2", n)
	}
	if got := co.Sessions[9][0].OccEndMs; got != 141_832 {
		t.Errorf("first session ends at %d, want 141832 (the drop broadcast)", got)
	}
	if got := co.Sessions[9][1].OccStartMs; got != 150_269 {
		t.Errorf("second session starts at %d, want 150269 (the rejoin userinfo)", got)
	}
}

// --- identity keys ---

// The exported key is derived from the identity's FIRST session (slot +
// userid) rather than from a union-find array index, so it is reproducible
// from the demo bytes by a consumer with their own parse.
func TestIdentityKeys_DerivedFromFirstSession(t *testing.T) {
	sess := []*occupancyRecord{
		{slot: 7, userID: 8, startMs: 0, uidStartMs: 0},
		{slot: 2, userID: 14, startMs: 603_204, uidStartMs: 603_204},
		{slot: 5, userID: 37, startMs: 100, uidStartMs: 100},
	}
	uf := newUnionFind(len(sess))
	uf.union(0, 1) // the two rusti sessions are one human

	keys := identityKeys(sess, uf)
	if got := keys[uf.find(0)]; got != "s7u8" {
		t.Errorf("unified identity key = %q, want s7u8 (its earliest session)", got)
	}
	if got := keys[uf.find(2)]; got != "s5u37" {
		t.Errorf("solo identity key = %q, want s5u37", got)
	}
}

// A userid reissued to the same slot for an unrelated later occupancy
// would collide. Since the streams builder GROUPS on this key, a collision
// would splice two players into one row, so it is broken with the start
// time rather than left to chance.
func TestIdentityKeys_CollisionIsBroken(t *testing.T) {
	sess := []*occupancyRecord{
		{slot: 7, userID: 8, startMs: 0, uidStartMs: 0},
		{slot: 7, userID: 8, startMs: 900_000, uidStartMs: 900_000},
	}
	uf := newUnionFind(len(sess)) // deliberately NOT unified

	keys := identityKeys(sess, uf)
	if keys[uf.find(0)] == keys[uf.find(1)] {
		t.Fatalf("two identities share key %q — one row would claim the other's stream", keys[uf.find(0)])
	}
	if got := keys[uf.find(1)]; got != "s7u8@900000" {
		t.Errorf("disambiguated key = %q, want s7u8@900000", got)
	}
}

// Appending the start time once is not enough: a third occupancy of the
// same slot+userid attested at the same millisecond would re-collide with
// the second, and the streams builder groups on this key.
func TestIdentityKeys_RepeatedCollisionKeepsBreaking(t *testing.T) {
	sess := []*occupancyRecord{
		{slot: 7, userID: 8, startMs: 0, uidStartMs: 0},
		{slot: 7, userID: 8, startMs: 900_000, uidStartMs: 900_000},
		{slot: 7, userID: 8, startMs: 900_000, uidStartMs: 900_000},
	}
	uf := newUnionFind(len(sess)) // three distinct identities

	keys := identityKeys(sess, uf)
	seen := make(map[string]bool, len(sess))
	for i := range sess {
		k := keys[uf.find(i)]
		if seen[k] {
			t.Fatalf("key %q issued twice — two identities would be spliced into one row", k)
		}
		seen[k] = true
	}
}

// Ties in attested start are broken by the lower slot, so the key of an
// identity whose two first-observed occupancies begin on the same
// millisecond is reproducible rather than dependent on record order.
func TestIdentityKeys_EqualStartsBreakOnSlot(t *testing.T) {
	sess := []*occupancyRecord{
		{slot: 7, userID: 8, startMs: 100, uidStartMs: 100},
		{slot: 2, userID: 14, startMs: 100, uidStartMs: 100},
	}
	uf := newUnionFind(len(sess))
	uf.union(0, 1) // one human on two slots from the same instant

	if got := identityKeys(sess, uf)[uf.find(0)]; got != "s2u14" {
		t.Errorf("identity key = %q, want s2u14 (equal starts break on the lower slot)", got)
	}
}

// --- playerStats mirror ---

// playerStats rows carry the stream's identity so /player-stats answers the
// question on its own; a scoreboard-only row (in the KTX block, no stream)
// carries neither, because there is no occupancy to attribute.
func TestPlayerStats_IdentityMirrorsStreamsAndSkipsScoreboardOnlyRows(t *testing.T) {
	res := &Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 600_000},
			Players: []result.PlayerStream{{
				Name:     "rusti",
				Team:     "jah",
				Identity: "s7u8",
				Sessions: []result.PlayerSession{{StartMs: 0, EndMs: 600_000, Slot: 7, UserID: 8, Name: "rusti"}},
				Alive:    []result.Interval{{Start: 0, End: 600_000}},
			}},
		},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "rusti", Team: "jah"},
			{Name: "herbie", Team: "tpa"}, // connected, never streamed
		}},
	}
	playerStatsPost(res, &CoreOutputs{})

	if res.PlayerStats == nil || len(res.PlayerStats.Players) != 2 {
		t.Fatalf("want 2 rows, got %+v", res.PlayerStats)
	}
	for _, row := range res.PlayerStats.Players {
		switch row.Name {
		case "rusti":
			if row.Identity != "s7u8" || len(row.Sessions) != 1 || row.Sessions[0].UserID != 8 {
				t.Errorf("streamed row = %q / %+v, want the stream's identity and sessions", row.Identity, row.Sessions)
			}
		case "herbie":
			if row.Identity != "" || row.Sessions != nil {
				t.Errorf("scoreboard-only row carries identity %q / %+v, want neither", row.Identity, row.Sessions)
			}
		}
	}
}
