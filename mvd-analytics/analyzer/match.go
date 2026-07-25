package analyzer

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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

// leaveBroadcast is one parsed "<name> left the game with N frags" line.
type leaveBroadcast struct {
	name  string
	frags int
	tMs   int32
}

// leaveMarker is the KTX / kmod departure broadcast. KTX emits it from
// ClientDisconnect while a match is running
// (ktx/src/client.c:2843, `G_bprint(PRINT_HIGH, "%s left the game with
// %.0f frags\n", self->netname, self->s.v.frags)`; the bot path repeats it
// at bot_commands.c:388), and the pre-KTX kmod/qwe mods use the same
// wording. It is the only place the wire states a departing player's final
// score, because the drop that follows immediately zeroes the slot.
//
// The trailing "frags" is deliberately truncated to "frag": old servers
// split a broadcast across several svc_print fragments, so
// 4on4_l_vs_la[e1m2] carries "DARKLORD left the game with 21 frag" and
// "s\n" as two events.
const leaveMarker = " left the game with "

// spectatorFragSentinel — pre-KTX mods publish a spectator's scoreboard
// entry with a large negative frag count so clients sort them below every
// player (observed as -999 on dag_caps_e1m2 and 4on4_l_vs_la[e1m2]; mvdsv
// relays the mod's edict value verbatim, SV_FullClientUpdate,
// mvdsv/src/sv_main.c:487-489). No real player can suicide their way to
// four figures, so a value at or below this is a marker, not a score:
// it is neither recorded as a score nor counted as evidence of play.
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
	case *events.PrintEvent:
		a.timing.OnPrint(e)
		a.noteLeaveBroadcast(e)
	case *events.IntermissionEvent:
		a.timing.OnIntermission(e.TimeMs)
	case *events.UserInfoEvent:
		closed, _, reopened := a.occ.onUserInfo(e)
		if closed != nil {
			a.closeOccupancy(closed, e.TimeMs)
		}
		if reopened != nil {
			// The drop did not end the occupancy after all — see
			// occupancyTracker.onUserInfo. Un-freeze the score so the rest
			// of the stint keeps counting.
			if sc := a.scores[reopened]; sc != nil {
				sc.finalized = false
			}
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

// noteLeaveBroadcast records a departure line's frag count. See
// leaveMarker for the wire form and why the match is on a truncated
// prefix.
func (a *MatchAnalyzer) noteLeaveBroadcast(e *events.PrintEvent) {
	if e.Level == events.PrintChat {
		return
	}
	i := strings.Index(e.Message, leaveMarker)
	if i <= 0 {
		return
	}
	name := e.Message[:i]
	rest := e.Message[i+len(leaveMarker):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j == 0 {
		return
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return
	}
	a.leaves = append(a.leaves, leaveBroadcast{name: name, frags: n, tMs: e.TimeMs})
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
// occupancy's departure, if it announced one. The broadcast precedes the
// drop in the same frame, so the search window is the occupancy itself.
func (a *MatchAnalyzer) announcedFrags(rec *occupancyRecord, tMs int32) (int, bool) {
	if rec.name == "" {
		return 0, false
	}
	want := normalizePlayerName(rec.name)
	best, found := 0, false
	for _, lv := range a.leaves {
		if lv.tMs > tMs || lv.tMs < rec.startMs {
			continue
		}
		if normalizePlayerName(lv.name) != want {
			continue
		}
		best, found = lv.frags, true
	}
	return best, found
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
func (a *MatchAnalyzer) resolveOccupant(rec *occupancyRecord) (name, team string) {
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
	n := 0
	for _, rec := range a.occ.all() {
		if rec.slot == slot {
			n++
		}
	}
	return n == 1
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

	// Get map name from server data
	if a.ctx.ServerData != nil {
		mr.Map = extractMapName(a.ctx.ServerData.LevelName)
		mr.GameDir = a.ctx.ServerData.GameDir
	}

	// Collect team stats
	teamFrags := make(map[string]int)

	// One row per participating *identity*. Occupancies are the unit of
	// scoring (a slot can change hands), but a player who reconnected onto
	// another slot owns several of them and must appear once — with the
	// score of their latest stint, which is the total the server restored
	// and re-asserted (ktx/src/client.c:1513-1538).
	a.occ.closeOpen(a.durationMs)
	type rosterRow struct {
		stat    PlayerStat
		slot    int
		startMs int32
	}
	rows := make(map[string]*rosterRow)
	var order []string
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
		row := rows[key]
		if row == nil {
			rows[key] = &rosterRow{stat: stat, slot: rec.slot, startMs: rec.startMs}
			order = append(order, key)
			continue
		}
		if rec.startMs >= row.startMs {
			row.stat, row.slot, row.startMs = stat, rec.slot, rec.startMs
		}
	}
	// Emit in wire-slot order of each identity's kept occupancy, which is
	// the order the previous slot-keyed loop produced and is stable across
	// runs.
	sort.SliceStable(order, func(i, j int) bool {
		ri, rj := rows[order[i]], rows[order[j]]
		if ri.slot != rj.slot {
			return ri.slot < rj.slot
		}
		return ri.startMs < rj.startMs
	})
	for _, key := range order {
		stat := rows[key].stat
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
		if r.Duel() {
			rebuildDuelMatch(mr, a.core.DemoInfo)
		}
	}
	return nil
}

// rebuildDuelMatch reconstructs MatchResult.Players and .Teams for a 1v1 around
// the synthetic one-player-per-team layout (team == the player's own name).
//
// When demoinfo is present it is the source of truth for participants — its
// end-of-match snapshot always lists every player it tracked stats for, so this
// recovers a participant the Finalize spectator gate dropped (a teamless
// frogbot) while merging in any per-player frag count already tracked. With no
// demoinfo it falls back to the existing two-player list, just relabelling
// teams. Mirrors the old normalizeDuelTeams Match block exactly.
func rebuildDuelMatch(mr *MatchResult, di *DemoInfoResult) {
	existing := make(map[string]PlayerStat, len(mr.Players))
	for _, p := range mr.Players {
		existing[p.Name] = p
	}
	rebuilt := make([]PlayerStat, 0, len(existing))
	if di != nil && len(di.Players) > 0 {
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

// extractMapName extracts the map name from the level name
func extractMapName(levelName string) string {
	// Level name might be like "Schloss Adler by Zaka" or just "dm4"
	// We want to extract just the map identifier

	// First, try to get base filename if it looks like a path
	name := filepath.Base(levelName)

	// Remove common suffixes
	name = strings.TrimSuffix(name, ".bsp")

	// If there's " by " in it, it's a description - try to get first word
	if idx := strings.Index(name, " by "); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}

	// If there's a newline, take first line
	if idx := strings.Index(name, "\n"); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}

	return name
}
