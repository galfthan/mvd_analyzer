package analyzer

import "sort"

// detectPowerupEvents derives PowerupEvent records from each player's
// streamBuilder interval lists (Quad / Pent / Ring). Each closed
// interval becomes one PowerupEvent. Replaces v6's per-bucket scan;
// the streamBuilder already records open / close transitions exactly
// at the events that flipped them, so this is just a translation.
//
// matchEndMs is the single effective match end computed once in Finalize
// and shared with buildStreamsResult's stream finalize, so a still-open
// powerup run closes at the same instant as the weapon intervals (F13).
func (a *TimelineAnalyzer) detectPowerupEvents(matchEndMs int32) []PowerupEvent {
	if len(a.playerState) == 0 {
		return nil
	}

	// Iterate slots in ascending order so the event list is built in a
	// fixed order before sorting; a Go map range over a.playerState would
	// otherwise shuffle same-ms ties across runs (and under GOMAXPROCS
	// variation), which the stable sort below then locks in.
	slots := make([]int, 0, len(a.playerState))
	for slot := range a.playerState {
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	events := []PowerupEvent{}
	for _, slot := range slots {
		state := a.playerState[slot]
		if state == nil {
			continue
		}
		// Close any still-open intervals at the shared match end so finalize
		// doesn't drop ongoing powerup runs.
		state.streams.quad.closeAtMatchEnd(matchEndMs)
		state.streams.pent.closeAtMatchEnd(matchEndMs)
		state.streams.ring.closeAtMatchEnd(matchEndMs)

		appendRuns := func(runs []intervalRecord, kind string) {
			for _, r := range runs {
				events = append(events, a.createPowerupEvent(slot, kind, r.start, r.end))
			}
		}
		appendRuns(state.streams.quad.closed, "quad")
		appendRuns(state.streams.pent.closed, "pent")
		appendRuns(state.streams.ring.closed, "ring")
	}

	// Stable sort by start time; equal-time events keep the deterministic
	// build order above (slot ascending, then quad→pent→ring, then interval
	// order) so the output is byte-stable.
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})
	return events
}

// createPowerupEvent creates a PowerupEvent with resolved player info.
// startTime/endTime are int32 ms (schema v8).
func (a *TimelineAnalyzer) createPowerupEvent(slot int, powerupType string, startTime, endTime int32) PowerupEvent {
	event := PowerupEvent{
		Time:        startTime,
		EndTime:     endTime,
		PlayerSlot:  slot,
		PowerupType: powerupType,
		Duration:    endTime - startTime,
	}

	// Resolve the identity that held the slot when the powerup run began
	// (startTime), so a quad/pent/ring run picked up before a reconnect
	// is credited to the right player.
	event.PlayerName, event.Team = a.resolveAt(slot, startTime)
	event.Team = a.core.TeamFor(event.PlayerName, event.Team)
	event.PlayerUserID = a.userIDAt(slot, startTime)

	return event
}

// buildDemoMarkers resolves every collected `//demomark` bookmark to a
// DemoMarkerEvent. Attribution mirrors createPowerupEvent: the marking
// slot is resolved to name/team at the mark's demo time, the team stamped
// through the born-correct duel rewrite (co.TeamFor), and the userid read
// off the session that held the slot at that instant (userIDAt). A mark that was not slot-addressed (PlayerNum -1) carries no
// attribution and is emitted with just its time and label. `/demomark` is
// CF_BOTH in KTX (ktx/src/commands.c:1027) — spectators can mark too, and
// their slot resolves like any client slot — so Spectator carries the
// roster's `*spectator` state (the same current-state approximation
// match.go uses) to let consumers tell the two apart. All marks are
// kept — including warmup / post-match ones — per the
// surface-authoritative-data rule; the Time rebase to the match clock and
// negative warmup times happen in rebaseToMatch.
func (a *TimelineAnalyzer) buildDemoMarkers() []DemoMarkerEvent {
	if len(a.rawDemoMarks) == 0 {
		return nil
	}
	markers := make([]DemoMarkerEvent, 0, len(a.rawDemoMarks))
	for _, m := range a.rawDemoMarks {
		ev := DemoMarkerEvent{
			Time:       m.Time,
			PlayerSlot: m.PlayerNum,
			Label:      m.Label,
		}
		if m.PlayerNum >= 0 {
			ev.PlayerName, ev.Team = a.resolveAt(m.PlayerNum, m.Time)
			ev.Team = a.core.TeamFor(ev.PlayerName, ev.Team)
			ev.PlayerUserID = a.userIDAt(m.PlayerNum, m.Time)
			if player := a.ctx.Players[m.PlayerNum]; player != nil {
				ev.Spectator = player.Spectator
			}
		}
		markers = append(markers, ev)
	}
	return markers
}
