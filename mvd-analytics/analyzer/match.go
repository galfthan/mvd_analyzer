package analyzer

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// MatchAnalyzer extracts match summary information.
//
// The scoreboard is built per *slot occupancy* rather than per slot (see
// occupancy.go): a wire slot can change hands mid-demo, and resolving it to
// its final occupant hands one player's score — or their scoreboard row
// outright — to whoever connected next.
type MatchAnalyzer struct {
	ctx        *Context
	core       *CoreOutputs
	durationMs int32
	timing     MatchTimingDetector

	// occ splits every wire slot into occupancies; scores holds the
	// per-occupancy scoreboard value and participation evidence.
	occ    *occupancyTracker
	scores map[*occupancyRecord]*occupancyScore

	// leaves are the server's own "<name> left the game with N frags"
	// broadcasts, the authoritative recovery for a departing player's
	// score (see closeOccupancy).
	leaves []leaveBroadcast

	// restorePrefixes are the leading texts of the "rejoins the game with N
	// frags" broadcasts — KTX announcing that a reconnecting client was
	// matched to its ghost and its score put back
	// (ktx/src/client.c:1513-1543). See foldStintFrags.
	restorePrefixes []string
}

// occupancyScore is one occupancy's svc_updatefrags cursor plus the
// evidence that its occupant actually played.
type occupancyScore struct {
	frags     int   // live svc_updatefrags value
	prevFrags int   // the value before the most recent change
	fragsAtMs int32 // when the most recent change landed
	moved     bool  // a frag value changed inside the match window
	movedAny  bool  // ... anywhere in the demo
	played    bool  // spawn / death / position sample inside the match window
	playedAny bool  // ... anywhere in the demo

	final     int // frozen score, once the occupancy ended
	finalized bool
}

// leaveBroadcast is one events.PlayerDepartureEvent whose frag count the
// parser could read (see events.PlayerDepartureEvent for the wire form, the
// truncated-prefix tolerance and the guard against a number fragmented
// mid-digits). It is the only place the wire states a departing player's
// final score, because the drop that follows immediately zeroes the slot.
type leaveBroadcast struct {
	name  string
	frags int
	tMs   int32
}

// spectatorFragSentinel — pre-KTX mods publish a spectator's scoreboard
// entry with a large negative frag count so clients sort them below every
// player (observed as -999 five times on dag_caps_e1m2; the same demo
// family's 4on4_l_vs_la[e1m2] happens to carry none). No real player can
// suicide their way to four figures, so a value at or below this is a
// marker, not a score: it is neither recorded as a score, nor adopted from
// a departure broadcast, nor counted as evidence of play.
//
// mvdsv relays the mod's edict value verbatim to the demo: the broadcast
// path is SV_UpdateToReliableMessages (mvdsv/src/sv_send.c:965), whose
// old_frags-changed block writes the live `ent->v->frags` at :985-1006.
// (SV_FullClientUpdate writes the *cached* old_frags instead,
// sv_main.c:490-492.)
const spectatorFragSentinel = -900

// UseCoreOutputs lets Match read demoinfo-resolved display names from
// co.Sessions when building the player-stats table.
func (a *MatchAnalyzer) UseCoreOutputs(co *CoreOutputs) { a.core = co }

// NewMatchAnalyzer creates a new match analyzer
func NewMatchAnalyzer() *MatchAnalyzer {
	return &MatchAnalyzer{
		occ:    newOccupancyTracker(),
		scores: make(map[*occupancyRecord]*occupancyScore),
	}
}

func (a *MatchAnalyzer) Name() string { return "match" }

func (a *MatchAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *MatchAnalyzer) OnEvent(event events.Event) error {
	a.durationMs = event.EventTimeMs()

	switch e := event.(type) {
	case *events.MatchStartEvent:
		a.timing.OnMatchStart(e)
	case *events.PrintEvent:
		a.timing.OnPrint(e)
	case *events.PlayerDepartureEvent:
		a.noteLeaveBroadcast(e)
	case *events.PlayerRejoinEvent:
		// Only the stats-restoring form. "reenters the game without stats"
		// (ktx/src/client.c:1546-1553) is the server saying the opposite.
		if e.WithStats {
			a.restorePrefixes = append(a.restorePrefixes, e.Prefix)
		}
	case *events.IntermissionEvent:
		a.timing.OnIntermission(e.TimeMs)
	case *events.UserInfoEvent:
		if closed, _ := a.occ.onUserInfo(e); closed != nil {
			a.closeOccupancy(closed, e.TimeMs)
		}
	case *events.FragUpdateEvent:
		a.onFragUpdate(e)
	case *events.SpawnEvent:
		a.notePlay(e.PlayerNum, e.TimeMs)
	case *events.DeathEvent:
		a.notePlay(e.PlayerNum, e.TimeMs)
	case *events.PlayerPositionEvent:
		a.notePlay(e.PlayerNum, e.TimeMs)
	}

	return nil
}

// score returns (creating if needed) the score record for the occupancy
// currently holding slot, opening an anonymous occupancy when the slot has
// had no userinfo yet.
func (a *MatchAnalyzer) score(slot int, tMs int32) (*occupancyRecord, *occupancyScore) {
	rec := a.occ.ensure(slot, tMs)
	if rec == nil {
		return nil, nil
	}
	sc := a.scores[rec]
	if sc == nil {
		sc = &occupancyScore{}
		a.scores[rec] = sc
	}
	return rec, sc
}

func (a *MatchAnalyzer) onFragUpdate(e *events.FragUpdateEvent) {
	// The match scoreboard is immutable once the match ends; a frag
	// update after that is next-game bookkeeping, most commonly a
	// reconnecting client's slot re-init to 0 during intermission,
	// which would otherwise clobber the final score (hub 212483:
	// Doomie's 34 reset to 0 by a post-match reconnect; hub 212545:
	// squeeze's 55 likewise). Mid-match reconnects need no special
	// case — KTX re-asserts the restored count via svc_updatefrags
	// right after the rejoin.
	if a.timing.Ended {
		return
	}
	if e.Frags <= spectatorFragSentinel {
		return
	}
	_, sc := a.score(e.PlayerNum, e.TimeMs)
	if sc == nil {
		return
	}
	if e.Frags != sc.frags {
		sc.prevFrags = sc.frags
		sc.frags = e.Frags
		sc.fragsAtMs = e.TimeMs
		sc.movedAny = true
		if a.timing.Started {
			sc.moved = true
		}
	}
}

// notePlay records that the slot's current occupant produced a sample only
// a player in the game world can produce — a spawn, a death, or an entity
// position. A connected-but-not-entered client (a spectator, or a
// connection the server refused because the match was locked) has none of
// these, which is what makes this the participation test.
func (a *MatchAnalyzer) notePlay(slot int, tMs int32) {
	_, sc := a.score(slot, tMs)
	if sc == nil {
		return
	}
	sc.playedAny = true
	if a.timing.Started && !a.timing.Ended {
		sc.played = true
	}
}

// noteLeaveBroadcast records a departure line's frag count.
//
// Broadcasts after the match ends are ignored, for the same reason
// onFragUpdate ignores post-match frag updates: the scoreboard is immutable
// once the match is over, and a player who disconnects during intermission
// is announced on whatever the mod has left in his edict — 0 under any mod
// that resets between games. KTX itself never emits the line then (it
// guards on `match_in_progress == 2`, ktx/src/client.c:2841), but the
// pre-KTX mods this recovery exists for have no such guard.
//
// The spectator sentinel is rejected here for the same reason onFragUpdate
// rejects it: the line renders the edict's frag count verbatim
// (`"%.0f"`, ktx/src/client.c:2843), so a mod that parks a spectator on
// -999 announces exactly that — and this value is adopted as an
// occupancy's final score without further test, then summed into the team
// total. The sentinel is real on this demo family (five times on
// `dag_caps_e1m2`); it just happens not to co-occur with a departure line
// there.
func (a *MatchAnalyzer) noteLeaveBroadcast(e *events.PlayerDepartureEvent) {
	if a.timing.Ended || !e.FragsKnown {
		return
	}
	if e.Frags <= spectatorFragSentinel {
		return
	}
	a.leaves = append(a.leaves, leaveBroadcast{name: e.Name, frags: e.Frags, tMs: e.TimeMs})
}

// closeOccupancy freezes an occupancy's final score at the moment the slot
// changed hands or the server dropped its client.
//
// Two corrections apply, in order:
//
//  1. SV_DropClient zeroes old_frags and then broadcasts the slot state in
//     the same server frame (mvdsv/src/sv_main.c:419-428 and :487-513), so
//     a frag update timestamped at the handover is the server clearing the
//     slot, not a score. Roll it back.
//  2. If the server announced the departure ("<name> left the game with N
//     frags"), take N. That is the server's own accounting rather than our
//     reconstruction, and it is what makes the recovery exact: on
//     4on4_l_vs_la[e1m2] the two announced totals are the difference
//     between the 57 we used to report for team |l| and the 104 the
//     serverinfo `score` key states.
func (a *MatchAnalyzer) closeOccupancy(rec *occupancyRecord, tMs int32) {
	sc := a.scores[rec]
	if sc == nil {
		sc = &occupancyScore{}
		a.scores[rec] = sc
	}
	if sc.finalized {
		return
	}
	v := sc.frags
	if sc.fragsAtMs == tMs && sc.movedAny {
		v = sc.prevFrags
	}
	if n, ok := a.announcedFrags(rec, tMs); ok {
		v = n
	}
	sc.final = v
	sc.finalized = true
}

// announcedFrags returns the frag count the server broadcast for this
// occupancy's departure, if it announced one.
//
// The broadcast only identifies a netname, so on a demo where two people
// share one it would otherwise bleed across occupancies. For a *vacated*
// record the window is therefore a single frame: SV_DropClient runs
// ClientDisconnect's bprint (via PR_GameClientDisconnect) and
// SV_FullClientUpdate's empty userinfo in the same server frame
// (mvdsv/src/sv_main.c:388-428), so the announcement and
// the close carry the same timestamp — verified on both real departures in
// the local corpus (4on4_l_vs_la[e1m2]: DARKLORD announced and dropped at
// t=1088539, shiva at t=1096572; hub 216835: rusti at t=613452).
//
// A record closed by a *takeover* instead — a new userid appearing without
// the server ever broadcasting a drop, which is what a demo that missed the
// drop packet looks like — has no such anchor, so it keeps the whole
// occupancy as its window.
func (a *MatchAnalyzer) announcedFrags(rec *occupancyRecord, tMs int32) (int, bool) {
	if rec.name == "" {
		return 0, false
	}
	want := normalizePlayerName(rec.name)
	best, found := 0, false
	for _, lv := range a.leaves {
		if rec.vacated {
			if lv.tMs != tMs {
				continue
			}
		} else if lv.tMs > tMs || lv.tMs < rec.startMs {
			continue
		}
		if normalizePlayerName(lv.name) != want {
			continue
		}
		best, found = lv.frags, true
	}
	return best, found
}

// restoredNames resolves the "rejoins the game with N frags" broadcasts to
// the normalized netnames they named.
//
// The verdict is per NETNAME and covers the whole demo, because the score
// it carries can cross a scoreboard ROW: two occupancies of one name that
// were live at the same instant are deliberately kept as separate rows
// (rowForKey), and on archive 2eb485602a7a… callen's restore lands on the
// other row from the departure it restores. Reading the broadcast as "every
// stint of this name continued the running total rather than restarting it"
// is the conservative direction — it is the one that cannot double-count.
func (a *MatchAnalyzer) restoredNames() map[string]bool {
	return resolveBroadcastNames(a.restorePrefixes, a.occ.all())
}

// finalFrags is the occupancy's scoreboard value: the frozen one when it
// ended mid-demo, else the live cursor (which the match-end guard in
// onFragUpdate already protects from post-match re-inits).
func (sc *occupancyScore) finalFrags() int {
	if sc.finalized {
		return sc.final
	}
	return sc.frags
}

// participated reports whether this occupancy was a player in the match.
//
// The test is evidence of play *inside the match window*, which replaces
// the end-of-demo Spectator / empty-team gates this function used to
// apply. Those gates were wrong in both directions: a participant who goes
// spectator after the match lost their whole row (hub 212535: wd.dilbert's
// 22 frags), and in FFA — where nobody has a team — an empty team dropped
// every player. Meanwhile they let through a connection the server refused
// at the handshake, which allocates a client slot and emits an
// svc_updateuserinfo for it without ever entering the game.
//
// A demo with no detectable match start (a warmup-only or truncated
// recording) has no window to test against, so evidence from anywhere in
// the demo counts.
func (a *MatchAnalyzer) participated(rec *occupancyRecord, sc *occupancyScore) bool {
	if rec.spectatorThroughout() {
		return false
	}
	if a.timing.Started {
		return sc.played || sc.moved
	}
	return sc.playedAny || sc.movedAny
}

// resolveOccupant returns the display name and team for one occupancy.
//
// The identity session table is authoritative when it is wired: it applies
// the demoinfo display-name join and is the only source that can name the
// *earlier* occupant of a reused slot. The userinfo scalars recorded on
// the occupancy come next, and co.SlotName / ctx.Players last — both key
// on the slot's final occupant, so they are only consulted for a slot that
// only ever had one.
//
// An occupancy the wire never sent a userinfo for (occupancyTracker.ensure
// opened it because a frag or a position arrived on an empty slot) resolves
// to nothing at all once the slot has changed hands, and the caller drops
// it. Every naming source would otherwise hand it the wrong human: the
// identity table extends each slot's last session to +inf
// (identity.go:239), so an anonymous record starting *after* a drop
// resolves to the player who just left — and then wins the roster
// tie-break below on its later startMs and replaces his recovered score
// with 0.
//
// The gate is defence in depth, and the local corpus does not exercise it.
// Measured over the 54 demos: 15 of them deliver a frag update on a slot
// whose occupancy has already closed, and on 1on1_]apollyon[_vs_jogi_[dm4]
// four anonymous records (slots 3-6, opened by the spawn events the
// header's full-state block replays at t=0) reach the roster stage as
// participants on 0 frags — but all four also resolve to the empty name, so
// Finalize's `name == ""` skip drops them anyway, and deleting these three
// lines leaves the Result byte-identical on all 54 demos. They stay because
// the hazard above is a property of the identity table rather than of the
// corpus: any demo whose session tail covers an anonymous post-drop record
// turns that record into a *named* 0-frag row that outranks the real one.
// Re-measure before removing them.
func (a *MatchAnalyzer) resolveOccupant(rec *occupancyRecord) (name, team string) {
	if !rec.sawInfo && !a.soleOccupancy(rec.slot) {
		return "", ""
	}
	if s, ok := a.core.SlotSessionAt(rec.slot, rec.startMs); ok {
		name, team = s.Name, s.Team
	}
	if name == "" {
		name = rec.name
	}
	if team == "" {
		team = rec.team
	}
	if a.soleOccupancy(rec.slot) {
		if name == "" {
			name = a.core.SlotName(rec.slot)
		}
		if p := a.ctx.Players[rec.slot]; p != nil {
			if name == "" {
				name = p.Name
			}
			if team == "" {
				team = p.Team
			}
		}
	}
	return name, team
}

// soleOccupancy reports whether slot was held by exactly one occupancy for
// the whole demo, i.e. whether slot-keyed state can be read safely.
func (a *MatchAnalyzer) soleOccupancy(slot int) bool {
	return a.occ.countForSlot(slot) == 1
}

// rosterRow is one scoreboard line under construction: the stat of the
// latest occupancy folded into it, plus the window of every occupancy it
// covers.
type rosterRow struct {
	stat    PlayerStat
	slot    int
	startMs int32
	windows []rosterWindow
	// stints is one entry per occupancy folded into this row, in occupancy
	// order — which is wire order, i.e. non-decreasing start time.
	// foldStintFrags turns them into the row's score.
	stints []stintScore
}

// stintScore is one occupancy's frozen score as it enters the identity fold:
// what it finished on, and the two signals that say whether it continued the
// identity's running total or restarted from zero.
type stintScore struct {
	frags int
	// identified is occupancyRecord.identified() — whether the wire gave
	// this occupancy a userid of its own. A userid-0 row is not a client
	// connection and so cannot be a fresh stint.
	identified bool
	// restored is set when the server ANNOUNCED a ghost restore for this
	// netname anywhere in the demo (restoredNames).
	restored bool
}

// foldStintFrags turns an identity's occupancies into one scoreboard score.
//
// A stint CONTINUES the identity's running total — its own value already
// contains everything earlier, so it REPLACES the total — when either of two
// value-free signals says the server carried the score over:
//
//   - the stint is a KTX ghost scoreboard row (`!identified`, i.e. userid 0).
//     ghost2scores republishes a departed player's frags on a spare slot the
//     instant the drop lands, and it hardcodes userid 0 in the
//     svc_updateuserinfo it writes (ktx/src/g_utils.c:2318) — it is the only
//     writer that does. The other two things that land on a userid-0 record
//     (an occupancy `ensure` opened for a frag or a position on a slot with
//     no userinfo, and a userid-0 resend) are not connections either, so
//     none of them can be a fresh stint whose score restarted from zero.
//   - the server ANNOUNCED a restore for it ("<name> rejoins the game with N
//     frags", ktx/src/client.c:1513-1543) — read per netname over the whole
//     demo, since a restore can land on a different scoreboard row from the
//     departure it restores (archive 2eb485602a7a…, callen).
//
// Every other stint — an identified connection the server never announced a
// restore for — ADDS, because its score restarted from zero. There is no
// ghost at all in matchless mode (MakeGhost returns at
// ktx/src/client.c:2897, `if (k_matchLess) // no ghost in matchless mode`),
// so a player who leaves and comes back there starts again at zero and the
// server's "left the game with N frags" broadcast for the earlier stint is
// the only record it happened. On ffa-demos/ffa_1[dm2]…, nexus left slot 4
// with 14 frags at 281.6 s and rejoined on slot 3 at 321.0 s, leaving again
// with 0 — and taking the latest stint reported 0 frags against 15 kills,
// breaking the frags == kills − suicides − teamKills identity the frag log
// maintains.
//
// The discriminator is deliberately value-free. It used to compare the first
// frag value the wire attested for a stint against the running total
// (continue iff it opened at or above it), which is wrong wherever the total
// is small or negative — and it is exactly a matchless rejoiner's total that
// is: onFragUpdate records the first value on a CHANGE from the 0 cursor, so
// a fresh row's first attested value is its first kill or suicide, ±1. A
// player who left matchless on 1 frag and scored 5 more on the second row
// folded to 5 instead of 6; one who left on −2 (lava) and scored 3 folded to
// 3 instead of 1.
func foldStintFrags(stints []stintScore) int {
	total := 0
	for _, st := range stints {
		if st.restored || !st.identified {
			total = st.frags
			continue
		}
		total += st.frags
	}
	return total
}

// rosterWindow is one folded occupancy's half-open [start, end) span, with
// the userid that owned it.
type rosterWindow struct {
	start, end int32
	identified bool
}

// rowForKey picks the roster row rec can be folded into: one that was not
// live at the same time as rec. Returns nil when every candidate was, which
// means rec is a different human who happens to share an identity key.
//
// The veto needs BOTH sides to be identified connections
// (occupancyRecord.identified). Two occupancies with userids of their own
// that were live at the same instant are two people, whatever the identity
// key says — but a record with no userid is not a second connection, and
// two of those routinely coexist with a real one:
//
//   - KTX publishes a departed player's *ghost* onto a spare client slot so
//     the scoreboard shows who is expected back. The publisher is
//     `ghost2scores` (ktx/src/g_utils.c:2272-2356), called from
//     `update_ghosts` (:2357-2365) on every GAME_CLIENT_CONNECT /
//     GAME_CLIENT_DISCONNECT (g_main.c:240, :284). It writes
//     SVC_UPDATEUSERINFO with a hardcoded `WriteLong(to, 0)` userid and the
//     `\x83`-prefixed netname, then SVC_UPDATEFRAGS with the ghost edict's
//     frags. On hub gameId 216835 that is
//     `svc_updateuserinfo 10 0 "\name\<0x83> rusti\team\jah\..."` at
//     t=613452 — the 0x83 glyph normalises to '#'. Its frag count is a COPY
//     of the departing player's, so treating it as a second human
//     double-counts him and invents a row.
//
//     The userid test is not a heuristic here: `ghost2scores` and
//     `ghostClearScores` (:2238-2270) are KTX's only two SVC_UPDATEUSERINFO
//     writers and both hardcode 0, so a ghost can never carry a non-zero
//     userid. (Whether a ghost exists at all is gated by `k_lockmode`,
//     `k_matchLess`, `k_no_scoreboard_ghosts`, `isRA()` and `isCA()`.)
//
//   - occupancyTracker.ensure opens a record with userid 0 whenever a frag
//     or position event lands on a slot with no userinfo, which the MVD
//     header's full-state dump does for every free slot.
func rowForKey(candidates []*rosterRow, rec *occupancyRecord) *rosterRow {
	for _, c := range candidates {
		live := false
		for _, w := range c.windows {
			if rec.startMs < w.end && w.start < rec.endMs && w.identified && rec.identified() {
				live = true
				break
			}
		}
		if !live {
			return c
		}
	}
	return nil
}

// identityKey groups occupancies that belong to the same human. The
// identity analyser's cross-reconnect key is used when available; without
// it (hand-built registries, unit tests) the resolved display name is the
// best available proxy.
func (a *MatchAnalyzer) identityKey(rec *occupancyRecord, name string) string {
	if s, ok := a.core.SlotSessionAt(rec.slot, rec.startMs); ok && s.IdentityKey != "" {
		return s.IdentityKey
	}
	return "name:" + name
}

func (a *MatchAnalyzer) Finalize(result *Result) error {
	// Calculate actual match duration
	matchDuration := a.durationMs
	if a.timing.Started && a.timing.StartTime > 0 {
		if a.timing.EndTime > a.timing.StartTime {
			matchDuration = a.timing.EndTime - a.timing.StartTime
		} else {
			// No end detected, use total - start
			matchDuration = a.durationMs - a.timing.StartTime
		}
	}

	mr := &MatchResult{
		Duration: matchDuration,
	}

	// Map is the canonical SHORT name (e1m2, dm2, aerowalk) — the one map
	// identity the whole project keys on — so it resolves through the same
	// accessor every BSP-derived producer uses (demoinfo map, else the
	// serverinfo `map` key). That is why this node requires `metadata`:
	// co.ServerInfoMap is published by MetadataAnalyzer.PopulateCore.
	// MapTitle is the svc_serverdata level title, display-only.
	mr.Map, mr.Sources.Map = a.resolveMap()
	if a.ctx.ServerData != nil {
		mr.MapTitle = cleanLevelTitle(a.ctx.ServerData.LevelName)
		mr.GameDir = a.ctx.ServerData.GameDir
	}
	if mr.Map == "" && mr.MapTitle != "" {
		// Last resort only: no shortname source named a map (no KTX demoinfo
		// block, no serverinfo `map` key, no `//finalscores`). The level
		// title is the sole remaining signal, so a degraded demo still
		// reports *something* rather than an empty identity — accepting that
		// on a map with a distinct title it is a title, not a shortname,
		// which is what Sources.Map is there to say.
		mr.Map, mr.Sources.Map = mr.MapTitle, MatchSrcLevelTitle
	}
	mr.Mode, mr.Sources.Mode = a.resolveMode()
	mr.GameMode = a.core.Mode()

	// Collect team stats
	teamFrags := make(map[string]int)

	// One row per participating *identity*. Occupancies are the unit of
	// scoring (a slot can change hands), but a player who reconnected onto
	// another slot owns several of them and must appear once, with every
	// stint folded into one score: a stint the server carried the total
	// over to REPLACES it (a userid-0 ghost row, or an announced restore),
	// every other stint ADDS. See foldStintFrags.
	a.occ.closeOpen(a.durationMs)
	restored := a.restoredNames()
	var rows []*rosterRow
	byKey := make(map[string][]*rosterRow)
	for _, rec := range a.occ.all() {
		sc := a.scores[rec]
		if sc == nil || !a.participated(rec, sc) {
			continue
		}
		name, team := a.resolveOccupant(rec)
		if name == "" {
			continue
		}
		key := a.identityKey(rec, name)
		stat := PlayerStat{Name: name, Team: team, Frags: sc.finalFrags()}
		// Two occupancies that were live at the same instant cannot be the
		// same human, whatever the identity key says. The key degrades to
		// the display name on a demo with no demoinfo, no *auth and no KTX
		// reconnect print — every pre-KTX demo, i.e. exactly the population
		// this scoreboard was rebuilt for — and identity.go's Source 4 then
		// unions sessions by normalized netname, which strips case and
		// punctuation (names.go:16). Two people called "Player" and
		// "player!" would otherwise collapse into one row, taking one of the
		// two teams off the table with them.
		row := rowForKey(byKey[key], rec)
		if row == nil {
			row = &rosterRow{stat: stat, slot: rec.slot, startMs: rec.startMs}
			byKey[key] = append(byKey[key], row)
			rows = append(rows, row)
		} else if rec.startMs >= row.startMs {
			row.stat, row.slot, row.startMs = stat, rec.slot, rec.startMs
		}
		row.windows = append(row.windows, rosterWindow{rec.startMs, rec.endMs, rec.identified()})
		row.stints = append(row.stints, stintScore{
			frags:      sc.finalFrags(),
			identified: rec.identified(),
			restored:   restored[normalizePlayerName(name)],
		})
	}
	// Emit in wire-slot order of each identity's kept occupancy, which is
	// the order the previous slot-keyed loop produced and is stable across
	// runs.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].slot != rows[j].slot {
			return rows[i].slot < rows[j].slot
		}
		return rows[i].startMs < rows[j].startMs
	})
	for _, row := range rows {
		stat := row.stat
		// The score spans every stint this identity played, not just the
		// last one — see foldStintFrags.
		stat.Frags = foldStintFrags(row.stints)
		// A player who legitimately finishes on 0 frags (kills cancelled by
		// suicides, a short but real appearance) is still a participant — the
		// surface-authoritative-data policy says report them rather than guess
		// they "didn't play".
		mr.Players = append(mr.Players, stat)
		if stat.Team != "" {
			teamFrags[stat.Team] += stat.Frags
		}
	}

	// Build team stats - only include valid team names. Sort by name so the
	// output is byte-stable across runs (Go map iteration is randomized).
	//
	// Skipped entirely in an individual mode: the tags in teamFrags name no
	// sides there, and summing them produces exactly the pseudo-teams this
	// work removed (on ffa_1[tox] a "red" row of 29 frags built from one
	// player and a "rr" row of 53 from another). rebuildIndividualMatch
	// below builds the one-row-per-player layout instead.
	if !a.individual() {
		teamNames := make([]string, 0, len(teamFrags))
		for team := range teamFrags {
			if !isSpectatorTeam(team) {
				teamNames = append(teamNames, team)
			}
		}
		sort.Strings(teamNames)
		for _, team := range teamNames {
			mr.Teams = append(mr.Teams, TeamStat{
				Name:  team,
				Frags: teamFrags[team],
			})
		}
	}

	result.Match = mr

	// Duel: rebuild the participant list and team labels around the
	// player-name-per-player layout the roster owns. This is the old
	// normalizeDuelTeams Match block, moved here so match.Players is born
	// correct — and, importantly, so the demoinfo-authoritative participant
	// merge recovers players the spectator gate above dropped (a frogbot with
	// team "" and no svc_updatefrags), which demoinfo lists but this Finalize
	// filtered out.
	if a.core != nil && a.core.Roster != nil {
		r := a.core.Roster
		// No usable demoinfo: let a 2-participant match decide the duel verdict,
		// the case the old isDuelResult covered via its match-players fallback.
		// Runs before every label-emitting derived producer, so the verdict
		// propagates.
		if a.core.DemoInfo == nil || len(a.core.DemoInfo.Players) == 0 {
			names := make([]string, 0, len(mr.Players))
			for _, p := range mr.Players {
				names = append(names, p.Name)
			}
			r.noteMatchParticipants(names)
		}
		// The duel promotion above can only have made the verdict MORE
		// individual, so refresh the descriptor before the layout reads it.
		a.refineGameMode(mr)
		if a.individual() {
			rebuildIndividualMatch(mr, a.core.DemoInfo, r.Duel())
		}
	}

	a.applyFinalScoresTeams(mr)
	return nil
}

// individual reports whether this match gets the one-side-per-player layout.
// Reads the shared CoreOutputs predicate so the scoreboard, the team-kill
// flags and the player-stats aggregate cannot disagree.
func (a *MatchAnalyzer) individual() bool { return a.core.IndividualMode() }

// refineGameMode folds MatchAnalyzer's own duel promotion back into the
// published descriptor.
//
// The resolver runs in the roster node, before this scoreboard exists, so on
// a demo with no demoinfo block it cannot see the two-participant shape that
// noteMatchParticipants has just established — it reports canonical
// "unknown" with whatever the serverinfo teamplay cvar said. Correcting it
// here is what keeps `match.gameMode` consistent with the layout the very
// same Finalize is about to apply; the descriptor is a pointer shared with
// CoreOutputs, so every producer scheduled after `match` sees the correction
// too.
func (a *MatchAnalyzer) refineGameMode(mr *MatchResult) {
	gm := mr.GameMode
	if gm == nil || !a.core.IsDuel() {
		return
	}
	if gm.Canonical == result.GameModeUnknown {
		gm.Canonical, gm.Sources.Canonical = result.GameModeDuel, result.GameModeSrcRoster
	}
	// Only a TeamBased verdict the TAG CENSUS produced is superseded — the
	// two-participant shape is the better roster-level reading of the same
	// evidence (two duellists who both picked `red` are not a team). A
	// verdict a teamplay cvar, a countdown row or a demoinfo block reached
	// stands: a roster-inferred duel does not get to veto a ruleset that
	// was demonstrably in force (see gamemode.go's precedence note and
	// the CTF-on-dm6 archive demo behind it).
	if gm.Canonical == result.GameModeDuel && gm.Sources.TeamBased == result.GameModeSrcRoster {
		gm.TeamBased = false
	}
}

// resolveMap resolves the canonical map SHORTNAME and names its source. The
// first two steps are CoreOutputs.EffectiveMap split in two so the provenance
// is legible; the third is the `//finalscores` map, which reaches demos with
// no demoinfo block at all. (In a 32 661-demo archive slice carrying the
// directive, none lacked a serverinfo `map` key — so the third step is a
// guard, not a load-bearing path. It exists because a demo whose fullserverinfo
// was cut is exactly the demo that needs it.)
func (a *MatchAnalyzer) resolveMap() (string, string) {
	if a.core == nil {
		return "", ""
	}
	if a.core.DemoInfo != nil && a.core.DemoInfo.Map != "" {
		return a.core.DemoInfo.Map, MatchSrcKTX
	}
	if a.core.ServerInfoMap != "" {
		return a.core.ServerInfoMap, MatchSrcServerInfo
	}
	if fs := a.core.FinalScores; fs != nil && fs.Map != "" {
		return fs.Map, MatchSrcFinalScores
	}
	return "", ""
}

// resolveMode resolves KTX's game-mode name. The demoinfo block wins wherever
// it exists — it is the same server, one code path over
// (ktx/src/stats.c:309 GetMode) — and `//finalscores` fills the pre-ktxstats
// half of the archive, where nothing else states a mode in this vocabulary.
// The serverinfo `mode` key and the countdown table are deliberately NOT in
// this chain: they speak different vocabularies ("1on1", "Duel") and stay
// verbatim on the metadata section.
func (a *MatchAnalyzer) resolveMode() (string, string) {
	if a.core == nil {
		return "", ""
	}
	if a.core.DemoInfo != nil && a.core.DemoInfo.Mode != "" {
		return a.core.DemoInfo.Mode, MatchSrcKTX
	}
	if fs := a.core.FinalScores; fs != nil && fs.Mode != "" {
		return fs.Mode, MatchSrcFinalScores
	}
	return "", ""
}

// applyFinalScoresTeams stamps the team-row provenance and, only when the
// scoreboard produced no team rows at all, adopts the two sides KTX stated in
// `//finalscores`.
//
// It never corrects a derived row, because the two figures are not always the
// same quantity: on Clan Arena and Wipeout KTX's score is ROUNDS WON, not
// frags (ktx/src/commands.c:6867-6886 — measured on the archive as "5" against
// a 241-frag scoreboard), and even on a frag-scored mode KTX counts the kill
// that ends the match while this scoreboard freezes at the match-end latch, so
// a legitimate ±1 shows up. Overwriting would turn both of those into silent
// corruption; reporting both sides — the derived rows here, the wire record
// verbatim on metadata.finalScores — is what lets a consumer see them.
//
// It runs after the duel rebuild so it sees the final rows.
func (a *MatchAnalyzer) applyFinalScoresTeams(mr *MatchResult) {
	if len(mr.Teams) > 0 {
		if a.individual() {
			mr.Sources.Teams = result.MatchSrcIndividual
		} else {
			mr.Sources.Teams = MatchSrcDerived
		}
		return
	}
	// In an individual mode `//finalscores` names the top-two PLAYERS, not
	// two teams (ktx/src/commands.c:6963-6977 fills team1/team2 from the
	// scoreboard's first two rows when there are no teams to name) — on
	// ffa_1[dm2] that is `":f" 36 "SMOK" 35`. Adopting them as teams is
	// exactly the invented-side bug the individual layout exists to remove,
	// and the rows are still published verbatim on metadata.finalScores.
	if a.individual() {
		return
	}
	if a.core == nil || a.core.FinalScores == nil {
		return
	}
	fs := a.core.FinalScores
	if fs.Team1 == "" && fs.Team2 == "" {
		return
	}
	mr.Teams = []TeamStat{
		{Name: fs.Team1, Frags: fs.Score1},
		{Name: fs.Team2, Frags: fs.Score2},
	}
	mr.Sources.Teams = MatchSrcFinalScores
}

// rebuildIndividualMatch reconstructs MatchResult.Players and .Teams around
// the one-side-per-player layout (team == the player's own name) for every
// mode with no teams: a 1v1, and — since v75 — FFA, race and anything else
// the descriptor resolved as not team-based.
//
// It is the same layout in both cases, which is the point: the API shape a
// consumer sees for an FFA is the one it already handles for a duel (the
// frontend's shape test is literally `every p.team === p.name`), so no
// consumer needs a mode string to render an individual scoreboard.
//
// demoinfo, where present, is the source of truth for PARTICIPANTS on a duel:
// its end-of-match snapshot always lists every player it tracked stats for,
// so this recovers a participant the Finalize spectator gate dropped (a
// teamless frogbot) while merging in any per-player frag count already
// tracked. On a non-duel individual mode the derived scoreboard is used
// instead even when demoinfo exists: players come and go in FFA and the
// scoreboard has already reconciled their occupancies (a mid-match leaver's
// announced score, a slot that changed hands), which the demoinfo snapshot's
// flat name list would flatten back out.
//
// The raw userinfo tag survives on PlayerStat.RawTeam — it is real
// information (clan membership), it just names no side.
func rebuildIndividualMatch(mr *MatchResult, di *DemoInfoResult, duel bool) {
	existing := make(map[string]PlayerStat, len(mr.Players))
	for _, p := range mr.Players {
		existing[p.Name] = p
	}
	rebuilt := make([]PlayerStat, 0, len(existing))
	if duel && di != nil && len(di.Players) > 0 {
		for _, dp := range di.Players {
			ps, ok := existing[dp.Name]
			if !ok {
				ps = PlayerStat{Name: dp.Name}
			}
			ps.Team = dp.Name
			if dp.Stats != nil {
				ps.Frags = dp.Stats.Frags
			}
			rebuilt = append(rebuilt, ps)
		}
	} else {
		for _, p := range mr.Players {
			p.Team = p.Name
			rebuilt = append(rebuilt, p)
		}
	}
	for i := range rebuilt {
		if raw := existing[rebuilt[i].Name].Team; raw != "" && raw != rebuilt[i].Name {
			rebuilt[i].RawTeam = raw
		}
	}
	mr.Players = rebuilt

	teams := make([]TeamStat, 0, len(mr.Players))
	for _, p := range mr.Players {
		teams = append(teams, TeamStat{Name: p.Name, Frags: p.Frags})
	}
	mr.Teams = teams
}

// isSpectatorTeam returns true if the team name indicates a spectator
func isSpectatorTeam(team string) bool {
	// Empty team is often a spectator
	if team == "" {
		return true
	}

	// Common spectator team names
	spectatorTeams := []string{
		"spec", "spectator", "specs", "spectators",
		"coop", "observe", "observer",
	}

	teamLower := strings.ToLower(team)
	for _, st := range spectatorTeams {
		if teamLower == st {
			return true
		}
	}

	// Check for non-ASCII characters (garbled text from spectator names)
	for _, r := range team {
		if r < 32 || r > 126 {
			return true
		}
	}

	return false
}

// cleanLevelTitle cleans the svc_serverdata level name into the display
// title published as MatchResult.MapTitle. It is NOT a shortname
// extractor — on most maps the level name is the pretty title ("Castle of
// the Damned"), and no amount of cleaning turns that into "e1m2".
func cleanLevelTitle(levelName string) string {
	// Level name might be like "Schloss Adler by Zaka" or just "dm4".
	// filepath.Base("") is ".", so bail before it invents a title.
	if strings.TrimSpace(levelName) == "" {
		return ""
	}

	// First, try to get base filename if it looks like a path
	name := filepath.Base(levelName)

	// Remove common suffixes
	name = strings.TrimSuffix(name, ".bsp")

	// If there's " by " in it, it's a description - drop the author hint
	if idx := strings.Index(name, " by "); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}

	// If there's a newline, take first line
	if idx := strings.Index(name, "\n"); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}

	return name
}
