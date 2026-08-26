package analyzer

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// CoreOutputs is the typed bundle of state-reconstruction results
// that derived analysers consume during their Finalize. It replaces
// the previous mechanism — shared mutable Context fields like
// ctx.DemoInfo and ctx.FragEntries written by one analyser's Finalize
// and read by the next, with no compile-time guarantee that the
// writer ran first.
//
// The registry builds this struct incrementally: each CoreProducer
// publishes via PopulateCore right after its own Finalize, and every
// CoreConsumer receives the running struct just before its Finalize.
// A consumer may rely only on the fields whose producers its declared
// dag.go edges schedule first — the edges, not tiers or registration
// order, drive the sequencing.
//
// Adding a field here is the right place when an analyser's Finalize
// would otherwise need to peek into another analyser's intermediate
// state. Keep the field → producing-node table in
// WRITING_AN_ANALYZER.md §1 in sync when you do.
type CoreOutputs struct {
	// DemoInfo is the parsed KTX demoinfo JSON, populated from the
	// demoinfo analyser's Finalize. Nil when the demo has no demoinfo
	// hidden message (older demos, non-KTX servers).
	DemoInfo *DemoInfoResult

	// Names resolves a display-name string back to its demoinfo team.
	// Produced by the demoinfo node (PopulateCore) alongside DemoInfo, so
	// callers don't each rebuild their own nameToTeam map. Nil-safe:
	// TeamForName returns "" when the table itself is nil.
	Names *NameTable

	// FragEntries is the canonical frag-event log emitted by the frag
	// analyser. Used by timeline (streaks, powerup-frag counts) and
	// weapon_pickups (kill attribution). Nil when the demo had no
	// obituaries or the frag analyser was not registered.
	FragEntries []FragEntry

	// VictimNamedTeamkills are teamkill obituaries that name only the
	// victim ("X was telefragged by his teammate"). Produced by the frag
	// node alongside FragEntries. The killer is the
	// generic "teammate", so they never enter FragEntries; the
	// recoverTelefragTeamkills post-processor recovers the killer from
	// position co-location + the teamkiller's -1 frag-delta.
	VictimNamedTeamkills []FragEntry

	// Slots is the per-slot resolved player view, produced by the
	// demoinfo node (PopulateCore): Name is the demoinfo
	// display name when the slot matches a demoinfo entry (via login or
	// name join), otherwise the userinfo name from ctx.Players[slot].
	// Team is the userinfo team (the demoinfo team override only kicks
	// in via NameTable lookups).
	//
	// Slots replaces the previous mid-Finalize patch in registry.go that
	// rewrote ctx.Players[slot].Name in place — the patch was the worst
	// instance of cross-analyser shared mutable state in the audit. Now
	// every Finalize site that wants the display name reads
	// co.Slots[slot].Name instead, and ctx.Players keeps its on-the-wire
	// userinfo values untouched.
	//
	// Slots maps one slot → one *final* occupant, which is wrong when a
	// player reconnects onto another slot and their old slot is reused
	// (or stamped with a late userinfo name). Finalize sites that have an
	// event timestamp should prefer SlotIdentityAt(slot, tMs) instead;
	// Slots remains for the few callers with no time to key on.
	Slots map[int]SlotInfo

	// Sessions is the per-slot, time-sorted, identity-resolved occupancy
	// list produced by the identity analyser. Each ResolvedSession covers
	// a half-open [StartMs, EndMs) window of a wire slot and carries the
	// canonical identity (cross-reconnect-unified) that owned the slot
	// during it. Nil when the identity analyser was not registered.
	Sessions map[int][]ResolvedSession

	// Clock is the match-relative time base every producer converts to at
	// Finalize (see clock.go). Produced by ClockAnalyzer (a CoreProducer with
	// no dependencies). Nil when the clock analyser was not registered (hand-built
	// registries / unit tests) — MatchStartMs / ToMatch are nil-safe and
	// resolve to demo time in that case.
	Clock *Clock

	// Roster is the canonical player/team table with the duel (player-name-as-
	// team) rewrite folded in (see roster.go). Produced by RosterAnalyzer, whose
	// `requires` edge on "demoinfo" means it sees the fully-populated DemoInfo.
	// Every producer reads TeamFor to stamp final team labels at emission,
	// replacing the old whole-Result normalizeDuelTeams rewrite. Nil when the
	// roster analyser was
	// not registered — TeamFor / Duel are nil-safe and pass raw teams through.
	Roster *Roster

	// ServerInfoMap is the serverinfo `map` key MetadataAnalyzer parsed from the
	// `fullserverinfo` stufftext (e.g. "dm3"). Produced by the metadata node
	// (PopulateCore). It is the KTX-independent map identifier every
	// BSP-derived producer falls back to when the KTX demoinfo block is absent
	// — read it through EffectiveMap, not directly. Empty when no serverinfo
	// map was seen.
	ServerInfoMap string

	// FinalScores is KTX's `//finalscores` end-of-match record, published by
	// the metadata node (PopulateCore). It is the only wire statement of a
	// final scoreline on the pre-ktxstats half of the archive; the match node
	// reads it for the map / mode / team rows it has no source of its own for,
	// naming the use in MatchResult.Sources. Nil when the demo carried no
	// (parseable) directive.
	FinalScores *FinalScores

	// ServerInfo is the merged `fullserverinfo` cvar table, published by the
	// metadata node (PopulateCore) so the mode resolver and the ruleset
	// gates read one copy instead of each reaching into result.Metadata.
	// Nil when the demo carried no fullserverinfo at all.
	ServerInfo map[string]string

	// MatchSettings is the parsed KTX countdown centerprint, published by
	// the metadata node (PopulateCore) beside ServerInfo. Nil when the demo
	// carried no countdown (every matchless recording).
	MatchSettings *MatchSettings

	// GameMode is the normalised mode descriptor (result.GameMode),
	// published by the roster node (PopulateCore) — which is why roster
	// requires `metadata`. Every producer that has to decide "were there
	// teams" reads it through IndividualMode / ModeTeamShaped rather than
	// re-deriving a mode table of its own. Nil when the roster node was not
	// registered.
	//
	// The match node REFINES it in place after its own duel promotion (a
	// demo with no demoinfo and exactly two participants), so a producer
	// finalizing after `match` may see a canonical the earlier ones did
	// not. Nothing reads Canonical before then.
	GameMode *result.GameMode

	// ServerStatus is the serverinfo `status` key tracked over time rather
	// than last-write-wins, published by the metadata node (PopulateCore).
	// The no-match post-processor reads it: `metadata.serverInfo["status"]`
	// names the state at demo END, while "what did the server say at demo
	// open" and "did it ever say a game was running" are what separate a
	// recording that begins mid-game from one that caught an idle server.
	// Zero-valued when the demo carried no `status` key at all.
	ServerStatus ServerStatus

	// PackEntities is every continuous appearance of a `progs/backpack.mdl`
	// entity on the wire, in match-relative ms, published by the backpacks
	// node (PopulateCore). It is the raw entity evidence the backpack-linkage
	// post binds reconstructed drops to; see analyzer/backpacks.go for how a
	// life is delimited and analyzer/backpack_linkage.go for what is read
	// off it. Empty on a demo whose recorder carried no entity stream.
	PackEntities []PackEntityLife
}

// EffectiveMap resolves which map (hence which BSP / loc corpus) the demo was
// recorded on for Finalize-time consumers, independent of whether the KTX
// demoinfo block is present: the demoinfo map name if it exists, else the
// serverinfo `map` key (ServerInfoMap). Returns "" when neither source names a
// map. This mirrors result.Result.EffectiveMap for the post-hoc path; see that
// method for why the fallback matters (2024-era demoinfo-less recorders).
func (co *CoreOutputs) EffectiveMap() string {
	if co == nil {
		return ""
	}
	if co.DemoInfo != nil && co.DemoInfo.Map != "" {
		return co.DemoInfo.Map
	}
	return co.ServerInfoMap
}

// MatchStartMs returns the demo→match shift published on the Clock, or 0 when
// no clock is wired or no match start was detected. Nil-safe for the several
// unit tests that build a CoreOutputs without a clock.
func (co *CoreOutputs) MatchStartMs() int32 {
	if co == nil || co.Clock == nil {
		return 0
	}
	return co.Clock.MatchStartMs
}

// TeamFor returns the final team label a producer should stamp for a record it
// attributes to name: the player's own name in a 1v1 (born-correct duel
// rewrite), else rawTeam unchanged. Nil-safe on co and its Roster, so producers
// call it uniformly whether or not a roster is wired.
func (co *CoreOutputs) TeamFor(name, rawTeam string) string {
	if co == nil {
		return rawTeam
	}
	return co.Roster.TeamFor(name, rawTeam)
}

// IsDuel reports whether the roster classified the match as a 1v1. Nil-safe on
// co and its Roster.
func (co *CoreOutputs) IsDuel() bool {
	if co == nil {
		return false
	}
	return co.Roster.Duel()
}

// IndividualMode reports whether this match is laid out INDIVIDUALLY: every
// player is their own side, so a userinfo `team` tag is decoration and no
// pair of players is ever "on the same team". It is the one predicate the
// team-sensitive producers share — team-kill flagging (frag.go), team-damage
// classification (damage.go / shots.go), the scoreboard's team rows
// (match.go), the per-team player-stats aggregate and the region-control
// layout — so they cannot disagree about a demo the way the four separate
// mode tables this replaced did.
//
// True for a 1v1 (the roster's own two-participant verdict, which the
// project already treats as authoritative) and for any mode the descriptor
// resolved as not team-based from a source that actually saw a mode or a
// teamplay cvar. Nil-safe: with no CoreOutputs, no roster and no descriptor
// it is false, which is the pre-v75 behaviour of taking every tag at face
// value.
func (co *CoreOutputs) IndividualMode() bool {
	if co == nil {
		return false
	}
	if co.Roster.Duel() {
		return true
	}
	return individualLayoutFromMode(co.GameMode)
}

// Mode returns the published mode descriptor. Nil-safe on co, so a producer
// with no CoreOutputs (hand-built registry, unit test) simply publishes no
// descriptor rather than panicking.
func (co *CoreOutputs) Mode() *result.GameMode {
	if co == nil {
		return nil
	}
	return co.GameMode
}

// ModeAllowsTeamplay reports whether the resolved mode leaves KTX's tp_num()
// gate open — `(isTeam() || isCTF() || coop) ? teamplay : 0`
// (ktx/src/g_utils.c:1586-1588) — i.e. whether the raw teamplay cvar means
// anything at all on this demo.
//
// An UNRESOLVED mode leaves it open, which is the asymmetry that matters: a
// demo that named no mode has not told us it was NOT teamplay, and the
// caller's other gates (a non-empty matching team pair) still have to agree
// before anything is nullified. Only a mode that positively says every
// player fights alone closes it. Nil-safe, with the same reading.
func (co *CoreOutputs) ModeAllowsTeamplay() bool {
	if co == nil || co.GameMode == nil {
		return true
	}
	switch co.GameMode.Canonical {
	case "", result.GameModeUnknown:
		return true
	}
	return co.GameMode.TeamShaped()
}

// ResolvedSession is one contiguous occupancy of a wire slot, resolved
// to the canonical (reconnect-unified) player identity. IdentityKey is
// equal for every session the same human played, so stream merging can
// group on it; see identityKeys (identity.go) for how it is derived and
// what it is and is not stable against.
type ResolvedSession struct {
	// StartMs / EndMs are the RESOLUTION window: the half-open interval a
	// lookup on this slot resolves to this identity. The first session on a
	// slot is widened back to MinInt32 and the last forward to MaxInt32 so
	// an event on either edge (before the first userinfo, after the last)
	// still resolves — they are lookup bounds, not observations.
	StartMs int32
	EndMs   int32
	// OccStartMs / OccEndMs are the OBSERVED occupancy boundaries, unwidened
	// — what the wire actually said. OccStartMs is the first userinfo that
	// attested this connection's userid (occupancyRecord.attestedStartMs);
	// OccEndMs is the drop broadcast, the next connection's userinfo, or
	// MaxInt32 when the client was still on the slot at the end of the
	// recording. Anything PUBLISHED to a consumer must use this pair: the
	// widened one above claims a connection existed before it did.
	OccStartMs int32
	OccEndMs   int32
	Name       string
	Team       string
	// WireName is this occupancy's own netname, where Name is the identity's
	// canonical (last-session, demoinfo-preferred) one. The two differ after
	// a rename or an mvdsv `(N)` duplicate-name prefix, and a consumer
	// joining our rows against a live engine roster at some instant needs
	// the name that was on the wire THEN. Folded through qNormalizeTable
	// like every other name in the pipeline.
	WireName string
	// Auth is the `*auth` login from userinfo, set by mvdsv for
	// authenticated players (mvd-reader parser/userinfo.go:177 for the
	// per-key svc_setinfo path, :229 for a full userinfo string). It is the
	// wire-side source for the login the KTX demoinfo block also carries,
	// so a demo without that block can still report who was playing.
	// Empty for unauthenticated players and on servers that do not set it.
	Auth string
	// UserID is the wire userid of the connection that held the slot for
	// this session — the value a hub `track=` link resolves against. It is
	// per-session on purpose: a userid identifies a *connection*, not a
	// human (SV_GenerateUserID, mvdsv/src/sv_main.c:538-556, reissues ids
	// from a rotating pool), so the same person reconnecting draws a new
	// one and the slot they vacated hands its old one to whoever takes it
	// next. Anything keyed slot-wide therefore names the wrong connection
	// after a handover or a rejoin. 0 when the occupancy carried no userid
	// of its own (see occupancyRecord.identified — an inferred occupancy,
	// a userid-0 resend, or KTX's ghost scoreboard row).
	UserID int
	// SpectateStint marks the occupancy a live player on this slot entered
	// by going spectator (see occupancyRecord.spectateStint). Such a session
	// is resolved against — an event on the slot still belongs to whoever is
	// there — but is not PUBLISHED as a play window: it answers none of the
	// questions result.PlayerSession exists for.
	SpectateStint bool
	IdentityKey   string
}

// SlotInfo holds the per-slot resolved player name and team that
// downstream Finalize sites read. See CoreOutputs.Slots for the
// resolution rules.
type SlotInfo struct {
	Name string
	Team string
}

// SlotName returns the resolved display name for slot. Equivalent to
// co.Slots[slot].Name with nil-safety on co; returns "" when the slot
// has no recorded entry.
func (co *CoreOutputs) SlotName(slot int) string {
	if co == nil {
		return ""
	}
	return co.Slots[slot].Name
}

// SlotSessionAt returns the session covering slot at the given time and
// whether one was found. It is SlotIdentityAt without the fallback, for
// callers that need the session's IdentityKey (and so must be able to tell
// "no session table" from "resolved").
func (co *CoreOutputs) SlotSessionAt(slot int, tMs int32) (ResolvedSession, bool) {
	if co == nil {
		return ResolvedSession{}, false
	}
	for i := range co.Sessions[slot] {
		s := co.Sessions[slot][i]
		if tMs >= s.StartMs && tMs < s.EndMs {
			return s, true
		}
	}
	return ResolvedSession{}, false
}

// nameUserIDIndex resolves (canonical identity name, demo-clock ms) to the
// userid of the connection that identity held at that instant. It is the
// name-keyed sibling of SlotSessionAt, for the two userid carriers whose
// event has no wire slot left on it: frag streaks (built after the per-slot
// spawns and deaths have been merged under one identity name) and airgibs
// (built post-hoc from the name-keyed damage log). Both used to stamp the
// demo-wide "last session with play" id from
// TimelineAnalysis.PlayerUserIDs, which for an event inside an EARLIER
// session of a rejoiner is an id that did not exist yet — a hub `track=`
// link that silently resolves to nothing for the whole run. The shape is
// gameId 222649's (bogojoker times out on userid 12 and returns on 25
// under one name); no cached demo happens to place a streak or an airgib
// in the earlier stint, which is why nothing caught it. result.go's v66
// note promises each event carrier reports the session that held the slot
// at its own timestamp; this is what delivers that off the slot-less
// surfaces.
//
// Windows are half-open [start_i, start_{i+1}) over the identity's sessions
// ordered by attested start (ResolvedSession.OccStartMs — the first
// userinfo that named the connection), the same convention SlotSessionAt
// resolves on, so an event landing exactly at a handover belongs to the
// successor. The earliest window is widened back to MinInt32, mirroring the
// ±inf widening the per-slot table carries: an event before the first
// userinfo (a match-start synthesised spawn on a recording that began
// mid-game) still belongs to the connection that was there.
//
// Keys are the canonical identity names — the same keys
// TimelineAnalysis.PlayerUserIDs uses, NOT the `name#slot` form the streams
// builder emits when two identities share a display name. A lookup under a
// suffixed name finds no windows and falls back to the name-keyed map,
// exactly as the map-only stamping it replaces did.
type nameUserIDIndex struct {
	windows  map[string][]nameUserIDWindow
	fallback map[string]int
}

type nameUserIDWindow struct {
	startMs int32
	slot    int
	userID  int
}

// newNameUserIDIndex builds the index from the published session table,
// with fallback (the demo-wide name→userid map) answering for names the
// table does not carry. Nil-safe on co: with no session table every lookup
// is the fallback, which is the pre-v66 behaviour.
func newNameUserIDIndex(co *CoreOutputs, fallback map[string]int) *nameUserIDIndex {
	x := &nameUserIDIndex{windows: make(map[string][]nameUserIDWindow), fallback: fallback}
	if co == nil {
		return x
	}
	slots := make([]int, 0, len(co.Sessions))
	for slot := range co.Sessions {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		for _, s := range co.Sessions[slot] {
			// An occupancy the wire never gave a userid of its own (an
			// inferred occupancy, a userid-0 resend, KTX's ghost scoreboard
			// row) names no connection, and its window would otherwise
			// shadow the real one it overlaps — the ghost carries the
			// departed player's name, so it lands under the same key.
			if s.Name == "" || s.UserID <= 0 {
				continue
			}
			x.windows[s.Name] = append(x.windows[s.Name],
				nameUserIDWindow{startMs: s.OccStartMs, slot: slot, userID: s.UserID})
		}
	}
	for name, ws := range x.windows {
		sort.Slice(ws, func(i, j int) bool {
			if ws[i].startMs != ws[j].startMs {
				return ws[i].startMs < ws[j].startMs
			}
			return ws[i].slot < ws[j].slot
		})
		ws[0].startMs = math.MinInt32
		x.windows[name] = ws
	}
	return x
}

// at returns the userid to stamp on an event attributed to name at
// demo-clock time tMs: the connection live at that instant, or the
// fallback map's demo-wide pick when the name has no sessions. 0 when
// neither source knows the name.
func (x *nameUserIDIndex) at(name string, tMs int32) int {
	if x == nil {
		return 0
	}
	ws := x.windows[name]
	for i := len(ws) - 1; i >= 0; i-- {
		if tMs >= ws[i].startMs {
			return ws[i].userID
		}
	}
	return x.fallback[name]
}

// SlotIdentityAt returns the canonical identity that owned slot at the
// given time (integer ms). It consults the per-slot session table so
// events that happened before a reconnect/slot-reuse resolve to the
// player who was actually there, not the slot's final occupant. Falls
// back to the final-occupant Slots entry when no session covers tMs
// (e.g. the identity analyser was not registered, or an out-of-range
// timestamp).
func (co *CoreOutputs) SlotIdentityAt(slot int, tMs int32) SlotInfo {
	if co == nil {
		return SlotInfo{}
	}
	for i := range co.Sessions[slot] {
		s := co.Sessions[slot][i]
		if tMs >= s.StartMs && tMs < s.EndMs {
			return SlotInfo{Name: s.Name, Team: s.Team}
		}
	}
	return co.Slots[slot]
}

// ResolveSlotAt resolves a wire slot to the (name, team) that owned it at
// time tMs, applying the full fallback chain the derived analyzers share.
// It replaces the per-analyzer copies (TimelineAnalyzer.resolveAt,
// DamageAnalyzer.resolveAt, WeaponPickupsAnalyzer.identityAt,
// FragAnalyzer.resolveDeathName, ItemAnalyzer.resolveAttributions) that had
// drifted apart on which fallback layers they applied.
//
// The chain, in order:
//
//  1. the reconnect-aware CoreOutputs session table (SlotIdentityAt) — so a
//     player's pre-reconnect events resolve to who was actually on the slot
//     then, not the slot's final occupant;
//  2. the live userinfo entry in players[slot] for any field the session
//     table left blank (name, then team) — this is also what keeps unit
//     tests that only wire up ctx.Players (no CoreOutputs) resolving;
//  3. the demoinfo name→team table to backfill a team when we have a name
//     but still no team (the KTX auth-name / late-join case).
//
// Nil-safe on co (SlotIdentityAt and TeamForName both tolerate nil) and
// bounds-safe on slot. players is taken by value to match findPlayerByName's
// convention (an array of pointers — a cheap copy).
func ResolveSlotAt(co *CoreOutputs, players [events.MaxClients]*events.PlayerInfo, slot int, tMs int32) SlotInfo {
	info := co.SlotIdentityAt(slot, tMs)
	if info.Name == "" || info.Team == "" {
		if slot >= 0 && slot < len(players) {
			if p := players[slot]; p != nil {
				if info.Name == "" {
					info.Name = p.Name
				}
				if info.Team == "" {
					info.Team = p.Team
				}
			}
		}
	}
	if info.Name != "" && info.Team == "" && co != nil {
		info.Team = co.Names.TeamForName(info.Name)
	}
	return info
}

// CoreConsumer is the optional interface for analysers that need
// access to CoreOutputs before their Finalize runs. The registry
// checks for this interface and invokes UseCoreOutputs in registration
// order, so an implementer is guaranteed to see every core output
// produced by an analyser registered earlier than itself.
type CoreConsumer interface {
	UseCoreOutputs(co *CoreOutputs)
}

// CoreProducer is the optional interface for analysers that contribute
// fields to CoreOutputs after their own Finalize runs. The registry
// invokes PopulateCore on every implementer immediately after the
// analyser's Finalize, so any analyser registered later in the slice
// (Core or Derived) sees the produced fields.
type CoreProducer interface {
	PopulateCore(co *CoreOutputs)
}
