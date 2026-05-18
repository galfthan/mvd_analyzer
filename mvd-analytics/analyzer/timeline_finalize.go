package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/locvis"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// Finalize converts the raw per-bucket player state collected during parsing
// into the TimelineAnalysisResult shipped to the frontend. This is the
// orchestration step — most of the heavy lifting is delegated to the
// aggregate / powerup / streak / region helpers.
func (a *TimelineAnalyzer) Finalize(result *Result) error {
	// Try to load loc file from DemoInfo.Map if not already loaded
	if a.locFinder == nil && a.core != nil && a.core.DemoInfo != nil && a.core.DemoInfo.Map != "" {
		if finder, err := locvis.LoadForMap(a.core.DemoInfo.Map); err == nil {
			a.locFinder = finder
		}
	}

	// (Loc resolution + blip filter now run on the per-position-sample
	// PositionTrack.Li column directly; see resolveLocsAndFilterBlips
	// below.)

	// Use the shared name->team lookup from CoreOutputs (built once
	// after the demoinfo analyser finalises).
	var names *NameTable
	if a.core != nil {
		names = a.core.Names
	}

	// Bridge slot↔demoinfo via login join / name join.
	resolved := a.ctx.ResolveSlotDemoInfo()
	slotToTeam := make(map[int]string)
	slotToPlayer := make(map[int]string)
	for slot, di := range resolved {
		if di.Team != "" {
			slotToTeam[slot] = di.Team
			slotToPlayer[slot] = di.Name
		}
	}

	// Convert raw frag events to final events with player and team info
	fragEvents := make([]TimelineFragEvent, 0, len(a.rawFrags))
	for _, raw := range a.rawFrags {
		// Prefer the demoinfo-resolved name (via slotToPlayer) so the
		// emitted player name matches what the timeline buckets and the
		// frontend's demoinfo-keyed Team Status panel expect. Fall back to
		// the userinfo name only when neither the login join nor the name
		// join matched this slot to a demoinfo entry.
		playerName := slotToPlayer[raw.PlayerNum]
		team := slotToTeam[raw.PlayerNum]

		if playerName == "" {
			if player := a.ctx.Players[raw.PlayerNum]; player != nil {
				playerName = player.Name
				if team == "" {
					team = player.Team
				}
			}
		}
		if playerName == "" {
			if name, ok := a.playerNames[raw.PlayerNum]; ok {
				playerName = name
			}
		}

		// If we still have a name but no team, look it up in DemoInfo by name.
		if playerName != "" && team == "" {
			team = names.TeamForName(playerName)
		}

		if team != "" {
			fragEvents = append(fragEvents, TimelineFragEvent{
				Time:   msTime(raw.Time),
				Player: playerName,
				Team:   team,
				Delta:  raw.Delta,
			})
		}
	}

	// Detect powerup pickup events for Key Moments
	powerupEvents := a.detectPowerupEvents(names, slotToTeam, slotToPlayer)

	// Count frags during each powerup run
	for i := range powerupEvents {
		pe := &powerupEvents[i]
		for _, fe := range a.coreFragEntries() {
			if fe.Killer != pe.PlayerName || fe.IsSuicide || fe.IsTeamKill {
				continue
			}
			if fe.Time >= pe.Time && fe.Time <= pe.EndTime {
				pe.Frags++
			}
		}
	}

	// Export location data for map visualization
	var locationData []MapLocation
	if a.locFinder != nil {
		locs := a.locFinder.Locations()
		locationData = make([]MapLocation, len(locs))
		for i, l := range locs {
			locationData[i] = MapLocation{
				X:    l.X,
				Y:    l.Y,
				Z:    l.Z,
				Name: l.Name,
			}
		}
	}

	// Build slot->name mapping for exports.
	//
	// Prefer the DemoInfo-derived name (resolved above via login join or
	// name join) over the live userinfo name. The two can differ when
	// the userinfo "name" field is an auth/login string but the player's
	// actual displayed netname is a different (often colored) string —
	// the frontend joins timeline data against DemoInfo player names, so
	// we must export the same name DemoInfo did or the per-player health/
	// armor stack disappears for that player.
	slotToName := make(map[int]string)
	for slot := 0; slot < events.MaxClients; slot++ {
		if name := slotToPlayer[slot]; name != "" {
			slotToName[slot] = name
		} else if player := a.ctx.Players[slot]; player != nil && player.Name != "" {
			slotToName[slot] = player.Name
		} else if name := a.playerNames[slot]; name != "" {
			slotToName[slot] = name
		}
	}

	// Resolve every native-rate position sample's nearest loc, smooth
	// short-residence wall-bleed via the blip filter, and emit the
	// resulting sparse Loc change stream into each player's stream
	// builder. Returns the ordered locTable we'll ship in Result.
	locTable, locIndex := a.resolveLocsAndFilterBlips()
	// Drop the table entirely if only the sentinel slot exists — JSON
	// omitempty will then skip the field on the wire.
	if len(locTable) <= 1 {
		locTable = nil
	}
	_ = locIndex // used by the regions builder below if regions are configured

	// Build name -> UserID mapping for Hub viewer links
	playerUserIDsByName := make(map[string]int)
	for slot, userID := range a.playerUserIDs {
		if userID > 0 {
			name := slotToName[slot]
			if name != "" {
				playerUserIDsByName[name] = userID
			}
		}
	}

	// Detect top 5 longest frag streaks for Key Moments
	fragStreaks := a.detectFragStreaks(10, names, playerUserIDsByName)

	// Build result.TimelineAnalysis (with regions but no BucketStates
	// yet) and then result.Streams — both are needed by
	// regionControlPost (which calls view.RegionControl) to fill in
	// BucketStates/Stats from streams.
	result.TimelineAnalysis = &TimelineAnalysisResult{
		MatchStartTime: msTime(a.timing.StartTime),
		FragEvents:     fragEvents,
		PowerupEvents:  powerupEvents,
		FragStreaks:    fragStreaks,
		LocationData:   locationData,
		LocTable:       locTable,
		PlayerUserIDs:  playerUserIDsByName,
	}

	matchEnd := a.timing.EndTime
	if matchEnd == 0 {
		// Fall back to latest position sample if timing didn't observe
		// an explicit end (e.g. demo cut short before intermission).
		// posT is int32 ms (schema v8); convert to seconds for the
		// comparison against the float64 EndTime placeholder.
		for _, state := range a.playerState {
			if n := len(state.streams.posT); n > 0 {
				if t := float64(state.streams.posT[n-1]) * 0.001; t > matchEnd {
					matchEnd = t
				}
			}
		}
	}
	if streams := a.buildStreamsResult(slotToName, slotToTeam, a.timing.StartTime, matchEnd); streams != nil {
		result.Streams = streams
	}

	// Region control: detect regions + resolve team labels. The
	// per-bucket classification (BucketStates, Stats) is filled by the
	// regionControlPost post-processor, which calls view.RegionControl
	// on the assembled Result. We keep the analyzer-side work here
	// because region detection depends on locFinder + region overrides
	// + the analyzer's slot-to-team mapping (none of which view/
	// should reach for).
	if a.locFinder != nil {
		regions := a.buildControlRegions()
		for i := range regions {
			seen := make(map[string]struct{}, len(regions[i].Points))
			locs := make([]string, 0, len(regions[i].Points))
			for _, p := range regions[i].Points {
				if p.Name == "" {
					continue
				}
				if _, ok := seen[p.Name]; ok {
					continue
				}
				seen[p.Name] = struct{}{}
				locs = append(locs, p.Name)
			}
			sort.Strings(locs)
			regions[i].Locs = locs
		}
		if len(regions) > 0 {
			regionControl := &RegionControlResult{Regions: regions}

			teamSet := make(map[string]struct{})
			for _, t := range slotToTeam {
				if t != "" {
					teamSet[t] = struct{}{}
				}
			}
			if len(teamSet) == 2 {
				teamNames := make([]string, 0, 2)
				if a.core != nil && a.core.DemoInfo != nil && len(a.core.DemoInfo.Teams) == 2 {
					di := a.core.DemoInfo.Teams
					if _, ok0 := teamSet[di[0]]; ok0 {
						if _, ok1 := teamSet[di[1]]; ok1 {
							teamNames = append(teamNames, di[0], di[1])
						}
					}
				}
				if len(teamNames) != 2 {
					teamNames = teamNames[:0]
					for t := range teamSet {
						teamNames = append(teamNames, t)
					}
					sort.Strings(teamNames)
				}
				regionControl.TeamA = teamNames[0]
				regionControl.TeamB = teamNames[1]
			}
			result.TimelineAnalysis.RegionControl = regionControl
		}
	}
	return nil
}
