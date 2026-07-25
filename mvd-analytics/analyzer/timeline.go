package analyzer

import (
	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/config"
	"github.com/mvd-analyzer/mvd-analytics/locvis"
	"github.com/mvd-analyzer/mvd-analytics/mapclip"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// TimelineAnalyzer collects per-event state into result.Streams and
// drives the derived event-shaped outputs (frag events, powerup runs,
// streaks). At schema v7 there is no parse-time bucket grid: every
// event flows into a streamBuilder per player, and finalize derives
// what consumers need (loc resolution + blip filter on the position
// track, region control, loc graph) directly from streams.
//
// The analyzer is split across several files in this package:
//
//   - timeline.go            (this file) state, types, OnEvent
//   - timeline_streams.go    streamBuilder + loc resolution + blip filter
//   - timeline_finalize.go   Finalize orchestration
//   - timeline_powerups.go   powerup pickup/loss event detection
//   - timeline_streaks.go    spawn-to-death frag streak detection
//   - timeline_regions.go    map region control auto-detection + custom defs
type TimelineAnalyzer struct {
	ctx           *Context
	core          *CoreOutputs
	playerState   map[int]*timelinePlayerState
	playerNames   map[int]string // Slot -> player name (from UserInfoEvent)
	playerUserIDs map[int]int    // Slot -> UserID (for Hub viewer track param)
	// occ is the shared wire-slot occupancy tracker (occupancy.go). It
	// spots a mid-match handoff so handleFragUpdate can rebase a
	// reconnecting player's restored frag total (see fragResetPending) and
	// so the per-slot stream state is reset at the handover.
	occ *occupancyTracker
	// fragResetPending[slot] means the slot's occupant just changed
	// mid-match, so the next frag update is a KTX stats restore / initial
	// scoreboard, not a kill. Consumed (cleared) by handleFragUpdate.
	fragResetPending map[int]bool
	rawFrags         []fragEvent      // Raw frag events (player num, time)
	rawDeaths        []deathEvent     // Raw death events (player num, time)
	rawSpawns        []deathEvent     // Raw spawn events (reusing deathEvent type)
	rawDemoMarks     []demoMarkRecord // Raw `//demomark` bookmarks (slot, time, label)
	timing           MatchTimingDetector
	locFinder        *locvis.Finder             // Visibility-aware loc finder for map (nil if no .loc file)
	clipHull         *mapclip.Hull              // Worldspawn player clip hull for floor-height traces (nil if no clip corpus for map)
	visBSP           *bspvis.BSP                // Hull-0 render BSP for liquid state / liquid-surface queries (nil if no BSP for map)
	blipThresholdMs  int                        // Per-player loc smoothing threshold, 0 disables
	regionsOverride  []config.MapRegionOverride // Optional caller-supplied region defs (e.g. CLI -regions). When non-nil, overrides config.RegionsForMap.
	// movers is each inline brush-model entity's wire-state timeline
	// (origin + visibility at demo-relative ms), accumulated from
	// MoverSpawn/MoverState events — NOT gated on match start, the
	// baseline pose arrives at demo open. moverHulls holds the matching
	// submodel clip hulls, built in Finalize alongside clipHull; the
	// floor-height pass poses them per sample (see resolveFloorHeights).
	movers     map[int]*moverTrack
	moverHulls map[int]*mapclip.Hull
}

// UseCoreOutputs is part of the CoreConsumer contract — Timeline
// consumes co.DemoInfo (map name + player team table) and
// co.FragEntries (for streak detection and powerup-frag counts) during
// its Finalize.
func (a *TimelineAnalyzer) UseCoreOutputs(co *CoreOutputs) { a.core = co }

// coreFragEntries is a nil-safe accessor for co.FragEntries; returns
// an empty slice when CoreOutputs hasn't been wired up (e.g. unit tests
// that only exercise OnEvent without going through the registry).
func (a *TimelineAnalyzer) coreFragEntries() []FragEntry {
	if a.core == nil {
		return nil
	}
	return a.core.FragEntries
}

// SetBlipThresholdMs configures the minimum residence a player must log
// in a loc for it to count as stable. Any shorter residence (wall bleed,
// nearest-point flicker at boundaries) is reassigned to an adjacent
// stable loc during finalization before downstream consumers read Li.
// Must be called before Init(). Zero disables the filter.
func (a *TimelineAnalyzer) SetBlipThresholdMs(ms int) {
	a.blipThresholdMs = ms
}

// SetRegionsOverride supplies a caller-defined region list that
// replaces the embedded per-map regions (config.RegionsForMap) for the
// duration of this analyzer run. Used by the CLI -regions flag and by
// tests that want to pin a specific region layout. Must be called
// before Finalize(). Pass nil to clear and fall back to embedded.
func (a *TimelineAnalyzer) SetRegionsOverride(regs []config.MapRegionOverride) {
	a.regionsOverride = regs
}

// fragEvent tracks a frag before team assignment
type fragEvent struct {
	Time      int32 // demo-clock ms
	PlayerNum int
	Delta     int // +N for kills, -N for suicides/teamkills
}

// deathEvent tracks a player death (detected via health transition)
type deathEvent struct {
	Time      int32 // demo-clock ms
	PlayerNum int
}

// demoMarkRecord is one `//demomark` bookmark as it arrives on the wire:
// the demo-clock time, the marking player's slot (-1 if the block was not
// slot-addressed), and the optional argument-tail label. Resolved to a
// result.DemoMarkerEvent in Finalize.
type demoMarkRecord struct {
	Time      int32 // demo-clock ms
	PlayerNum int   // marking player's wire slot; -1 if not slot-addressed
	Label     string
}

// pauseSample is one mvdhidden 0x000A paused_duration block: the demo-relative
// (game) time of the idle frame (ms) and the real wall-clock ms it spanned. The
// game clock is frozen across a pause, so all samples of one pause share a
// Time; Finalize sums DurationMs over each contiguous run.
type pauseSample struct {
	Time       int32
	DurationMs int
}

// timelinePlayerState tracks current state for a single player as the
// parser walks the demo. items is the raw item bitfield from
// svc_updatestat; it's decoded into weapons/powerups/armor type
// before being recorded into the stream builder. isDead flips on
// DeathEvent / SpawnEvent and is consulted by other analyzers
// (frag streaks, etc.); not consumed by the stream emission path.
//
// The accompanying streamBuilder is the append-only historical record
// that becomes result.PlayerStream at finalize. The cursor (this
// struct's fields) tells "what is X right now"; the builder holds
// "every transition we've seen." See state.go for the dedup invariants.
type timelinePlayerState struct {
	items  int // raw item bitfield (weapons, powerups, armor type)
	vitals vitals
	isDead bool
	ammo   ammoCounts
	pos    playerPosition
	frags  int

	// streams accumulates the append-only historical record. Populated
	// in OnEvent alongside the running cursor; flushed to result.Streams
	// in Finalize.
	streams streamBuilder
}

// NewTimelineAnalyzer creates a new timeline analyzer.
func NewTimelineAnalyzer() *TimelineAnalyzer {
	return &TimelineAnalyzer{
		playerState:      make(map[int]*timelinePlayerState),
		playerNames:      make(map[int]string),
		playerUserIDs:    make(map[int]int),
		occ:              newOccupancyTracker(),
		fragResetPending: make(map[int]bool),
	}
}

func (a *TimelineAnalyzer) Name() string { return "timelineAnalysis" }

func (a *TimelineAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *TimelineAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.StatUpdateEvent:
		return a.handleStatUpdate(e)
	case *events.DeathEvent:
		a.handleDeath(e)
	case *events.SpawnEvent:
		a.handleSpawn(e)
	case *events.PrintEvent:
		a.timing.OnPrint(e)
	case *events.IntermissionEvent:
		// svc_intermission is the most reliable end-of-match signal: KTX
		// fires it on timelimit/fraglimit hit even when there's no matching
		// bprint string.
		a.timing.OnIntermission(e.TimeMs)
	case *events.FragUpdateEvent:
		// Track frag events from frag updates (more reliable than stat updates)
		a.handleFragUpdate(e)
	case *events.UserInfoEvent:
		a.handleUserInfo(e)
	case *events.PlayerPositionEvent:
		// Track player positions
		a.handlePositionUpdate(e)
	case *events.MoverSpawnEvent:
		a.handleMoverSpawn(e)
	case *events.MoverStateEvent:
		a.handleMoverState(e)
	case *events.DemoMarkEvent:
		// Player-inserted bookmark. Recorded un-gated (warmup / post-match
		// marks kept) — attribution and match-clock rebase happen in Finalize.
		a.rawDemoMarks = append(a.rawDemoMarks, demoMarkRecord{
			Time:      e.TimeMs,
			PlayerNum: e.PlayerSlot,
			Label:     e.Label,
		})
	}
	return nil
}

// handleUserInfo tracks display names / userids and, when the event ends a
// slot occupancy, resets the per-slot state that must not cross the
// handover.
func (a *TimelineAnalyzer) handleUserInfo(e *events.UserInfoEvent) {
	if e.Player == nil {
		return
	}
	slot := e.Player.Slot
	if e.Player.Name != "" {
		a.playerNames[slot] = e.Player.Name
		// Keep the FIRST valid UserID per slot for the Hub viewer track
		// param; some servers resend userinfo with UserID 0 or corrupted
		// values.
		if a.playerUserIDs[slot] == 0 && e.Player.UserID > 0 {
			a.playerUserIDs[slot] = e.Player.UserID
		}
	}

	closed, opened, _ := a.occ.onUserInfo(e)
	if closed == nil {
		return
	}

	// The slot changed hands, or the server dropped its client. Close the
	// departing occupant's open item intervals here and clear the held
	// state so the next occupant starts from an empty inventory rather
	// than inheriting the slot's stale item bits. Only inside the match
	// window: before it no interval is ever opened, and after it the
	// streams are frozen and finalize closes them at the match end.
	if a.timing.Started && !a.timing.Ended {
		if state := a.playerState[slot]; state != nil {
			state.streams.endOccupancy(e.TimeMs)
			state.items = 0
			if e.Vacated {
				// SV_DropClient zeroes the slot's scoreboard and broadcasts it
				// in the same server frame as this empty userinfo
				// (mvdsv/src/sv_main.c:419-428 and :487-513), so the frag
				// update that just arrived is slot bookkeeping, not a score.
				// Drop the event it produced and rebase the cursor, otherwise
				// a player who leaves on a low score contributes a phantom
				// negative frag and the next occupant inherits a stale
				// baseline.
				a.dropFragEventsAt(slot, e.TimeMs)
				state.frags = 0
			}
		}
	}

	// A fresh connection took the slot mid-match: the next frag update is a
	// KTX stats restore / initial scoreboard rather than a kill. Flag it so
	// handleFragUpdate rebases instead of feeding the value to the
	// corruption guard. Pre-match roster shuffles don't count (frags are 0
	// then anyway).
	if opened != nil && a.timing.Started {
		a.fragResetPending[slot] = true
	}
}

// dropFragEventsAt removes the trailing raw frag events recorded for slot
// at exactly tMs. Events arrive in time order, so they are at the tail.
func (a *TimelineAnalyzer) dropFragEventsAt(slot int, tMs int32) {
	i := len(a.rawFrags)
	for i > 0 && a.rawFrags[i-1].Time == tMs {
		i--
	}
	kept := a.rawFrags[:i]
	for _, f := range a.rawFrags[i:] {
		if f.PlayerNum != slot {
			kept = append(kept, f)
		}
	}
	a.rawFrags = kept
}

func (a *TimelineAnalyzer) handlePositionUpdate(e *events.PlayerPositionEvent) {
	// Always track position cursor, even during warmup (for continuity).
	state := a.getOrCreatePlayerState(e.PlayerNum)
	state.pos = playerPosition{x: e.Origin[0], y: e.Origin[1], z: e.Origin[2]}

	// Stream emission: append every native sample (D11 asymmetry —
	// positions don't dedup). Match-time only; warmup positions would
	// pollute the stream with garbage.
	if a.timing.Started && !a.timing.Ended {
		state.streams.recordPosition(e.TimeMs, e.Origin[0], e.Origin[1], e.Origin[2], e.Angles[0], e.Angles[1])
	}
}

func (a *TimelineAnalyzer) handleFragUpdate(e *events.FragUpdateEvent) {
	state := a.getOrCreatePlayerState(e.PlayerNum)
	if !a.timing.Started {
		return
	}

	// A mid-match occupant change on this wire slot (flagged by onUserInfo)
	// means this frag value is a KTX stats restore / initial scoreboard for
	// the new occupant, not a kill. Adopt it as the new baseline and emit
	// nothing. Without this, a reconnecting player whose frag total KTX
	// restores onto a new slot (gameId 216835: rusti rejoins onto a vacated
	// spectator slot with 16 frags) reads as a huge +delta, gets rejected by
	// the corruption guard below, and — because that guard leaves state.frags
	// at 0 — every later real +1 keeps reading as a huge delta and is also
	// rejected, freezing the player's timeline score for the rest of the match.
	if a.fragResetPending[e.PlayerNum] {
		a.fragResetPending[e.PlayerNum] = false
		state.frags = e.Frags
		return
	}

	// Track frag changes (both increases and decreases)
	// Frags increase on kills, decrease on suicides/teamkills
	if e.Frags != state.frags {
		delta := e.Frags - state.frags
		// Sanity check: filter unreasonable deltas caused by parsing artifacts
		// (e.g., misaligned reads producing garbage frag values).
		// No player can gain or lose >5 frags in a single server frame.
		// When a corrupt value arrives, do NOT update state.frags — keep the
		// last known good value. The next valid update will naturally produce
		// the correct cumulative delta (e.g., corrupt reads 9→272, correction
		// reads 272→10, but by keeping state at 9 the correction gives delta +1).
		if delta >= -5 && delta <= 5 {
			a.rawFrags = append(a.rawFrags, fragEvent{
				Time:      e.TimeMs,
				PlayerNum: e.PlayerNum,
				Delta:     delta,
			})
			state.frags = e.Frags
		}
		// else: corrupt value, don't update state.frags
	}
}

func (a *TimelineAnalyzer) handleStatUpdate(e *events.StatUpdateEvent) error {
	// Ignore all state during countdown/warmup - players have all weapons,
	// infinite ammo, etc. which is meaningless. Match starts fresh with
	// 100 health and base shotgun. After match end, ignore stat updates too:
	// the intermission camera otherwise freezes the last seen value (often a
	// KTX damage-indicator sentinel like health=1000+dmg) into every bucket.
	if !a.timing.Started || a.timing.Ended {
		return nil
	}

	state := a.getOrCreatePlayerState(e.PlayerNum)

	switch e.StatIndex {
	case events.StatHealth:
		// KTX uses health = 1000 + damage as a damage-indicator sentinel
		// (ktx/src/combat.c:1001). Real player health is capped at 250.
		// Drop sentinel values so they don't get sampled into buckets.
		if e.Value <= 250 {
			state.vitals.health = e.Value
			state.streams.recordHealth(e.TimeMs, int16(e.Value))
		}
	case events.StatArmor:
		// Same shape: KTX overwrites armorvalue in pre-match speed-meter
		// and in damage feedback paths with values > 200. Real armor caps
		// at 200 (RA). Reject anything larger.
		if e.Value <= 200 {
			state.vitals.armor = e.Value
			state.streams.recordArmor(e.TimeMs, int16(e.Value))
		}
	case events.StatItems:
		state.items = e.Value
		w, p, at := itemBitsToLoadouts(e.Value)
		state.streams.recordItemFlags(e.TimeMs, w, p)
		state.streams.recordArmorType(e.TimeMs, at)
	case events.StatShells:
		state.ammo.shells = e.Value
		state.streams.recordShells(e.TimeMs, int16(e.Value))
	case events.StatNails:
		state.ammo.nails = e.Value
		state.streams.recordNails(e.TimeMs, int16(e.Value))
	case events.StatRockets:
		state.ammo.rockets = e.Value
		state.streams.recordRockets(e.TimeMs, int16(e.Value))
	case events.StatCells:
		state.ammo.cells = e.Value
		state.streams.recordCells(e.TimeMs, int16(e.Value))
	}
	return nil
}

// handleDeath records the authoritative death transition from the
// parser. Same guard as handleStatUpdate: only match-time events are
// recorded so warmup cycles don't pollute state.
func (a *TimelineAnalyzer) handleDeath(e *events.DeathEvent) {
	if !a.timing.Started || a.timing.Ended {
		return
	}
	state := a.getOrCreatePlayerState(e.PlayerNum)
	a.rawDeaths = append(a.rawDeaths, deathEvent{Time: e.TimeMs, PlayerNum: e.PlayerNum})
	state.streams.recordDeath(e.TimeMs)
	state.isDead = true
}

// handleSpawn is the mirror of handleDeath for the respawn transition —
// or the first-spawn when a player moves from spectator / pre-connect to
// active play. Consumers treat both cases identically.
func (a *TimelineAnalyzer) handleSpawn(e *events.SpawnEvent) {
	if !a.timing.Started || a.timing.Ended {
		return
	}
	state := a.getOrCreatePlayerState(e.PlayerNum)
	a.rawSpawns = append(a.rawSpawns, deathEvent{Time: e.TimeMs, PlayerNum: e.PlayerNum})
	state.streams.recordSpawn(e.TimeMs)
	state.isDead = false
}

// resolveAt resolves a wire slot to its (name, team) at time tMs via the
// canonical ResolveSlotAt chain (session table → userinfo → name→team
// backfill), then adds the timeline-only last-resort fallback of the
// last-seen userinfo name (playerNames) for a slot the shared chain couldn't
// name. Used by fragEvents / powerups / streaks.
func (a *TimelineAnalyzer) resolveAt(slot int, tMs int32) (name, team string) {
	info := ResolveSlotAt(a.core, a.ctx.Players, slot, tMs)
	name, team = info.Name, info.Team
	if name == "" {
		if n, ok := a.playerNames[slot]; ok {
			name = n
		}
	}
	if name != "" && team == "" && a.core != nil {
		team = a.core.Names.TeamForName(name)
	}
	return name, team
}

func (a *TimelineAnalyzer) getOrCreatePlayerState(playerNum int) *timelinePlayerState {
	if s, ok := a.playerState[playerNum]; ok {
		return s
	}
	s := &timelinePlayerState{}
	a.playerState[playerNum] = s
	return s
}
