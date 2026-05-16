package analyzer

import "sort"

// detectPowerupEvents derives PowerupEvent records from each player's
// streamBuilder interval lists (Quad / Pent / Ring). Each closed
// interval becomes one PowerupEvent. Replaces v6's per-bucket scan;
// the streamBuilder already records open / close transitions exactly
// at the events that flipped them, so this is just a translation.
func (a *TimelineAnalyzer) detectPowerupEvents(names *NameTable, slotToTeam map[int]string, slotToPlayer map[int]string) []PowerupEvent {
	if len(a.playerState) == 0 {
		return nil
	}

	events := []PowerupEvent{}
	for slot, state := range a.playerState {
		if state == nil {
			continue
		}
		// Close any still-open intervals at the timing detector's end
		// time (or the latest position sample) so finalize doesn't
		// drop ongoing powerup runs. All time arithmetic is int32 ms;
		// EndTime is float64 seconds and is converted at the boundary.
		var matchEndMs int32
		if a.timing.EndTime > 0 {
			matchEndMs = msTime(a.timing.EndTime)
		} else if len(state.streams.posT) > 0 {
			matchEndMs = state.streams.posT[len(state.streams.posT)-1]
		}
		state.streams.quad.closeAtMatchEnd(matchEndMs)
		state.streams.pent.closeAtMatchEnd(matchEndMs)
		state.streams.ring.closeAtMatchEnd(matchEndMs)

		appendRuns := func(runs []intervalRecord, kind string) {
			for _, r := range runs {
				events = append(events, a.createPowerupEvent(slot, kind, r.start, r.end, names, slotToTeam, slotToPlayer))
			}
		}
		appendRuns(state.streams.quad.closed, "quad")
		appendRuns(state.streams.pent.closed, "pent")
		appendRuns(state.streams.ring.closed, "ring")
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})
	return events
}

// createPowerupEvent creates a PowerupEvent with resolved player info.
// startTime/endTime are int32 ms (schema v8).
func (a *TimelineAnalyzer) createPowerupEvent(slot int, powerupType string, startTime, endTime int32, names *NameTable, slotToTeam map[int]string, slotToPlayer map[int]string) PowerupEvent {
	event := PowerupEvent{
		Time:        startTime,
		EndTime:     endTime,
		PlayerSlot:  slot,
		PowerupType: powerupType,
		Duration:    endTime - startTime,
	}

	if userID, ok := a.playerUserIDs[slot]; ok {
		event.PlayerUserID = userID
	}

	if name := slotToPlayer[slot]; name != "" {
		event.PlayerName = name
	}
	if t := slotToTeam[slot]; t != "" {
		event.Team = t
	}
	if player := a.ctx.Players[slot]; player != nil {
		if event.PlayerName == "" {
			event.PlayerName = player.Name
		}
		if event.Team == "" {
			event.Team = player.Team
		}
		if event.PlayerUserID == 0 && player.UserID != 0 {
			event.PlayerUserID = player.UserID
		}
	}
	if event.PlayerName == "" {
		if name, ok := a.playerNames[slot]; ok {
			event.PlayerName = name
		}
	}
	if event.Team == "" && event.PlayerName != "" {
		event.Team = names.TeamForName(event.PlayerName)
	}

	return event
}
