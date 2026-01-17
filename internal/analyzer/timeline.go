package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// TimelineAnalyzer tracks time-bucketed player state for timeline visualization
type TimelineAnalyzer struct {
	ctx            *Context
	bucketDuration float64
	playerState    map[int]*timelinePlayerState
	playerNames    map[int]string // Slot -> player name (from UserInfoEvent)
	playerUserIDs  map[int]int    // Slot -> UserID (for Hub viewer track param)
	buckets        []*timelineBucketData
	fragEventsRaw  []fragEventRaw // Raw frag events (player num, time)
	lastSampleTime float64
	matchStartTime float64
	matchStarted   bool
}

// fragEventRaw tracks a frag before team assignment
type fragEventRaw struct {
	Time      float64
	PlayerNum int
}

// timelinePlayerState tracks current state for a single player
type timelinePlayerState struct {
	items   int // Current items (weapons, powerups, armor type)
	health  int
	armor   int
	shells  int
	nails   int
	rockets int
	cells   int
	frags   int // Current frag count
}

// timelineBucketData holds raw aggregated data during analysis
type timelineBucketData struct {
	startTime  float64
	endTime    float64
	playerData map[int]*playerBucketRawData // Keyed by slot
}

// playerBucketRawData holds per-player data for a bucket
type playerBucketRawData struct {
	name      string
	team      string
	hasRL     bool
	hasLG     bool
	hasQuad   bool
	hasPent   bool
	hasRing   bool
	health    int
	armor     int
	armorType string // "ga"/"ya"/"ra"
	shells    int
	nails     int
	rockets   int
	cells     int
}

// NewTimelineAnalyzer creates a new timeline analyzer
func NewTimelineAnalyzer() *TimelineAnalyzer {
	return &TimelineAnalyzer{
		bucketDuration: 1.0, // 1 second buckets for detail resolution
		playerState:    make(map[int]*timelinePlayerState),
		playerNames:    make(map[int]string),
		playerUserIDs:  make(map[int]int),
		buckets:        make([]*timelineBucketData, 0),
	}
}

func (a *TimelineAnalyzer) Name() string { return "timelineAnalysis" }

func (a *TimelineAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *TimelineAnalyzer) OnEvent(event parser.Event) error {
	switch e := event.(type) {
	case *parser.StatUpdateEvent:
		return a.handleStatUpdate(e)
	case *parser.PrintEvent:
		// Detect match start from countdown message
		a.detectMatchStart(e)
	case *parser.FragUpdateEvent:
		// Track frag events from frag updates (more reliable than stat updates)
		a.handleFragUpdate(e)
	case *parser.UserInfoEvent:
		// Track player names and UserIDs for team resolution and Hub viewer links
		if e.Player != nil && e.Player.Name != "" {
			a.playerNames[e.Player.Slot] = e.Player.Name
			// Only update UserID if we don't have one yet, or if the new one is valid
			// Some servers resend userinfo with UserID=0 or corrupted values
			// Keep the first valid UserID we see for each slot
			newUserID := e.Player.UserID
			existingUserID := a.playerUserIDs[e.Player.Slot]
			if existingUserID == 0 && newUserID > 0 {
				// No existing ID, use whatever we got (first valid value)
				a.playerUserIDs[e.Player.Slot] = newUserID
			}
			// Otherwise keep existing UserID - first valid value wins
		}
	}
	return nil
}

func (a *TimelineAnalyzer) detectMatchStart(e *parser.PrintEvent) {
	if a.matchStarted {
		return
	}

	// KTX servers print "The match has begun!" or similar
	// Also detect "Fight!" or countdown end
	msg := e.Message
	if contains(msg, "match has begun") || contains(msg, "Fight!") ||
		contains(msg, "begins in 1") || contains(msg, "Go!") {
		a.matchStartTime = e.Time
		a.matchStarted = true
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (a *TimelineAnalyzer) handleFragUpdate(e *parser.FragUpdateEvent) {
	state := a.getOrCreatePlayerState(e.PlayerNum)

	// Only track if match has started and frag count increased
	if a.matchStarted && e.Frags > state.frags {
		// Store raw event - team will be assigned in Finalize
		a.fragEventsRaw = append(a.fragEventsRaw, fragEventRaw{
			Time:      e.Time,
			PlayerNum: e.PlayerNum,
		})
	}
	state.frags = e.Frags
}

func (a *TimelineAnalyzer) handleStatUpdate(e *parser.StatUpdateEvent) error {
	// Ignore all state during countdown/warmup - players have all weapons,
	// infinite ammo, etc. which is meaningless. Match starts fresh with
	// 100 health and base shotgun.
	if !a.matchStarted {
		return nil
	}

	state := a.getOrCreatePlayerState(e.PlayerNum)

	switch e.StatIndex {
	case mvd.StatHealth:
		state.health = e.Value
	case mvd.StatArmor:

		state.armor = e.Value
	case mvd.StatItems:
		state.items = e.Value
	case mvd.StatShells:
		state.shells = e.Value
	case mvd.StatNails:
		state.nails = e.Value
	case mvd.StatRockets:
		state.rockets = e.Value
	case mvd.StatCells:
		state.cells = e.Value
	}

	// Sample at bucket boundaries - fill ALL buckets since last sample
	currentBucket := int(e.Time / a.bucketDuration)
	lastBucket := int(a.lastSampleTime / a.bucketDuration)

	if currentBucket > lastBucket {
		// Fill all buckets from lastBucket+1 to currentBucket
		for b := lastBucket + 1; b <= currentBucket; b++ {
			bucketTime := float64(b) * a.bucketDuration
			a.sampleCurrentState(bucketTime)
		}
		a.lastSampleTime = e.Time
	}

	return nil
}

func (a *TimelineAnalyzer) sampleCurrentState(time float64) {
	bucket := a.getOrCreateBucket(time)

	// Sample stats per player
	for slot := 0; slot < mvd.MaxClients; slot++ {
		player := a.ctx.Players[slot]
		if player == nil || player.Spectator {
			continue
		}

		state := a.playerState[slot]
		if state == nil {
			continue
		}

		// Only include alive players (health > 0)
		if state.health <= 0 {
			continue
		}

		// Create player data for this bucket
		pData := &playerBucketRawData{
			name:    player.Name,
			team:    player.Team,
			health:  state.health,
			armor:   state.armor,
			shells:  state.shells,
			nails:   state.nails,
			rockets: state.rockets,
			cells:   state.cells,
		}

		// Track weapons
		pData.hasRL = (state.items & mvd.ITRocketLauncher) != 0
		pData.hasLG = (state.items & mvd.ITLightning) != 0

		// Track powerups
		pData.hasQuad = (state.items & mvd.ITQuad) != 0
		pData.hasPent = (state.items & mvd.ITInvulnerability) != 0
		pData.hasRing = (state.items & mvd.ITInvisibility) != 0

		// Track armor type
		if state.items&mvd.ITArmor3 != 0 {
			pData.armorType = "ra"
		} else if state.items&mvd.ITArmor2 != 0 {
			pData.armorType = "ya"
		} else if state.items&mvd.ITArmor1 != 0 {
			pData.armorType = "ga"
		}

		bucket.playerData[slot] = pData
	}
}

func (a *TimelineAnalyzer) getOrCreateBucket(time float64) *timelineBucketData {
	bucketIndex := int(time / a.bucketDuration)

	// Extend buckets array if needed
	for len(a.buckets) <= bucketIndex {
		newBucket := &timelineBucketData{
			startTime:  float64(len(a.buckets)) * a.bucketDuration,
			endTime:    float64(len(a.buckets)+1) * a.bucketDuration,
			playerData: make(map[int]*playerBucketRawData),
		}
		a.buckets = append(a.buckets, newBucket)
	}

	return a.buckets[bucketIndex]
}

func (a *TimelineAnalyzer) getOrCreatePlayerState(playerNum int) *timelinePlayerState {
	if s, ok := a.playerState[playerNum]; ok {
		return s
	}
	s := &timelinePlayerState{}
	a.playerState[playerNum] = s
	return s
}

func (a *TimelineAnalyzer) Finalize() (interface{}, error) {
	// Do a final sample at the end
	if len(a.buckets) > 0 {
		lastBucket := a.buckets[len(a.buckets)-1]
		if lastBucket.endTime > a.lastSampleTime {
			a.sampleCurrentState(lastBucket.endTime)
		}
	}

	// Build a name->team lookup from DemoInfo (authoritative source)
	// Use both exact name and normalized name for matching
	nameToTeam := make(map[string]string)
	normNameToTeam := make(map[string]string) // Normalized names (lowercase, alphanumeric only)
	fragsToTeam := make(map[int]string)       // Frag count -> team (for slot matching)
	fragsToPlayer := make(map[int]string)     // Frag count -> player name (for slot matching)
	if a.ctx.DemoInfo != nil {
		for _, p := range a.ctx.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				nameToTeam[p.Name] = p.Team
				normNameToTeam[normalizePlayerName(p.Name)] = p.Team
				// Map frag count to team and player for slot resolution
				if p.Stats != nil && p.Stats.Frags != 0 {
					fragsToTeam[p.Stats.Frags] = p.Team
					fragsToPlayer[p.Stats.Frags] = p.Name
				}
			}
		}
	}

	// Build slot->team and slot->player mappings from final frag counts
	slotToTeam := make(map[int]string)
	slotToPlayer := make(map[int]string)
	for slot, frags := range a.ctx.FragsBySlot {
		if team, ok := fragsToTeam[frags]; ok {
			slotToTeam[slot] = team
		}
		if player, ok := fragsToPlayer[frags]; ok {
			slotToPlayer[slot] = player
		}
	}

	// Convert raw frag events to final events with player and team info
	fragEvents := make([]TimelineFragEvent, 0, len(a.fragEventsRaw))
	for _, raw := range a.fragEventsRaw {
		playerName := ""
		team := ""

		// First try ctx.Players
		player := a.ctx.Players[raw.PlayerNum]
		if player != nil {
			if player.Name != "" {
				playerName = player.Name
			}
			if player.Team != "" {
				team = player.Team
			}
		}

		// If no player name, try our local tracking
		if playerName == "" {
			if name, ok := a.playerNames[raw.PlayerNum]; ok {
				playerName = name
			}
		}

		// If we have a name but no team, look it up in DemoInfo
		if playerName != "" && team == "" {
			team = nameToTeam[playerName]
			if team == "" {
				team = normNameToTeam[normalizePlayerName(playerName)]
			}
		}

		// If still no player/team, try slot mapping from frag counts
		if playerName == "" {
			playerName = slotToPlayer[raw.PlayerNum]
		}
		if team == "" {
			team = slotToTeam[raw.PlayerNum]
		}

		if team != "" {
			fragEvents = append(fragEvents, TimelineFragEvent{
				Time:   raw.Time,
				Player: playerName,
				Team:   team,
			})
		}
	}

	// Detect powerup pickup events for Key Moments
	powerupEvents := a.detectPowerupEvents(nameToTeam, slotToTeam, slotToPlayer)

	result := &TimelineAnalysisResult{
		BucketDuration: a.bucketDuration,
		MatchStartTime: a.matchStartTime,
		Buckets:        make([]TimelineBucket, len(a.buckets)),
		FragEvents:     fragEvents,
		PowerupEvents:  powerupEvents,
	}

	for i, b := range a.buckets {
		bucket := TimelineBucket{
			StartTime:  b.startTime,
			EndTime:    b.endTime,
			PlayerData: make(map[string]*PlayerBucketData),
			TeamData:   make(map[string]*TeamBucketData),
		}

		// First, build PlayerData from raw player data
		// Also resolve names from slot mappings if needed
		teamAggregates := make(map[string]*teamAggregator)

		for slot, pRaw := range b.playerData {
			// Get player name, falling back to slot mapping
			playerName := pRaw.name
			if playerName == "" {
				playerName = slotToPlayer[slot]
			}
			if playerName == "" {
				continue // Skip if we can't identify the player
			}

			// Get team, falling back to slot mapping
			team := pRaw.team
			if team == "" {
				team = slotToTeam[slot]
			}

			// Build player bucket data
			bucket.PlayerData[playerName] = &PlayerBucketData{
				Team:      team,
				HasRL:     pRaw.hasRL,
				HasLG:     pRaw.hasLG,
				HasQuad:   pRaw.hasQuad,
				HasPent:   pRaw.hasPent,
				HasRing:   pRaw.hasRing,
				Health:    pRaw.health,
				Armor:     pRaw.armor,
				ArmorType: pRaw.armorType,
				Shells:    pRaw.shells,
				Nails:     pRaw.nails,
				Rockets:   pRaw.rockets,
				Cells:     pRaw.cells,
			}

			// Aggregate for team stats
			if team != "" {
				if teamAggregates[team] == nil {
					teamAggregates[team] = &teamAggregator{
						armorByType: make(map[string]int),
					}
				}
				agg := teamAggregates[team]

				// Weapons
				if pRaw.hasRL && pRaw.hasLG {
					agg.playersWithRLLG++
					agg.playersWithWeapons++
				} else if pRaw.hasRL {
					agg.playersWithRL++
					agg.playersWithWeapons++
				} else if pRaw.hasLG {
					agg.playersWithLG++
					agg.playersWithWeapons++
				}

				// Powerups
				if pRaw.hasQuad {
					agg.playersWithQuad++
					agg.playersWithPowerups++
				}
				if pRaw.hasPent {
					agg.playersWithPent++
					agg.playersWithPowerups++
				}
				if pRaw.hasRing {
					agg.playersWithRing++
					agg.playersWithPowerups++
				}

				// Health/Armor
				agg.healthSamples = append(agg.healthSamples, pRaw.health)
				agg.armorSamples = append(agg.armorSamples, pRaw.armor)
				if pRaw.armorType != "" {
					agg.armorByType[pRaw.armorType]++
				}

				// Ammo
				agg.totalShells += pRaw.shells
				agg.totalNails += pRaw.nails
				agg.totalRockets += pRaw.rockets
				agg.totalCells += pRaw.cells
			}
		}

		// Build TeamData from aggregates
		for team, agg := range teamAggregates {
			bucket.TeamData[team] = &TeamBucketData{
				PlayersWithRL:       agg.playersWithRL,
				PlayersWithLG:       agg.playersWithLG,
				PlayersWithRLLG:     agg.playersWithRLLG,
				PlayersWithWeapons:  agg.playersWithWeapons,
				PlayersWithQuad:     agg.playersWithQuad,
				PlayersWithPent:     agg.playersWithPent,
				PlayersWithRing:     agg.playersWithRing,
				PlayersWithPowerups: agg.playersWithPowerups,
				AvgHealth:           average(agg.healthSamples),
				AvgArmor:            average(agg.armorSamples),
				TotalHealth:         sum(agg.healthSamples),
				TotalArmor:          sum(agg.armorSamples),
				ArmorByType:         agg.armorByType,
				TotalShells:         agg.totalShells,
				TotalNails:          agg.totalNails,
				TotalRockets:        agg.totalRockets,
				TotalCells:          agg.totalCells,
			}
		}

		result.Buckets[i] = bucket
	}

	return result, nil
}

// teamAggregator is used during finalization to aggregate player data into team data
type teamAggregator struct {
	playersWithRL       int
	playersWithLG       int
	playersWithRLLG     int
	playersWithWeapons  int
	playersWithQuad     int
	playersWithPent     int
	playersWithRing     int
	playersWithPowerups int
	healthSamples       []int
	armorSamples        []int
	armorByType         map[string]int
	totalShells         int
	totalNails          int
	totalRockets        int
	totalCells          int
}

// average calculates the average of a slice of ints
func average(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	s := 0
	for _, v := range values {
		s += v
	}
	return float64(s) / float64(len(values))
}

// sum calculates the sum of a slice of ints
func sum(values []int) int {
	s := 0
	for _, v := range values {
		s += v
	}
	return s
}

// normalizePlayerName removes non-alphanumeric chars and lowercases for fuzzy matching
// "bad.rotker" and "badrotker" will both become "badrotker"
func normalizePlayerName(name string) string {
	var result []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		// Convert uppercase to lowercase
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		// Keep only alphanumeric
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		}
	}
	return string(result)
}

// detectPowerupEvents scans buckets for powerup pickup/loss transitions
func (a *TimelineAnalyzer) detectPowerupEvents(nameToTeam map[string]string, slotToTeam map[int]string, slotToPlayer map[int]string) []PowerupEvent {
	if len(a.buckets) == 0 {
		return nil
	}

	events := []PowerupEvent{}

	type powerupInfo struct {
		field string
		name  string
	}
	powerupTypes := []powerupInfo{
		{"hasQuad", "quad"},
		{"hasPent", "pent"},
		{"hasRing", "ring"},
	}

	// Track active powerups per player slot per type
	// Map: slot -> powerupType -> startTime (0 if not active)
	activeRuns := make(map[int]map[string]float64)

	for _, bucket := range a.buckets {
		for slot, pData := range bucket.playerData {
			if activeRuns[slot] == nil {
				activeRuns[slot] = make(map[string]float64)
			}

			// Check each powerup type
			for _, pt := range powerupTypes {
				var hasIt bool
				switch pt.field {
				case "hasQuad":
					hasIt = pData.hasQuad
				case "hasPent":
					hasIt = pData.hasPent
				case "hasRing":
					hasIt = pData.hasRing
				}

				startTime := activeRuns[slot][pt.name]

				if hasIt && startTime == 0 {
					// Powerup just picked up
					activeRuns[slot][pt.name] = bucket.startTime
				} else if !hasIt && startTime > 0 {
					// Powerup just lost
					event := a.createPowerupEvent(slot, pt.name, startTime, bucket.startTime, nameToTeam, slotToTeam, slotToPlayer)
					events = append(events, event)
					activeRuns[slot][pt.name] = 0
				}
			}
		}
	}

	// Handle powerups still active at end of demo
	lastBucket := a.buckets[len(a.buckets)-1]
	for slot, runs := range activeRuns {
		for pType, startTime := range runs {
			if startTime > 0 {
				event := a.createPowerupEvent(slot, pType, startTime, lastBucket.endTime, nameToTeam, slotToTeam, slotToPlayer)
				events = append(events, event)
			}
		}
	}

	// Sort by time
	sort.Slice(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	return events
}

// createPowerupEvent creates a PowerupEvent with resolved player info
func (a *TimelineAnalyzer) createPowerupEvent(slot int, powerupType string, startTime, endTime float64, nameToTeam map[string]string, slotToTeam map[int]string, slotToPlayer map[int]string) PowerupEvent {
	event := PowerupEvent{
		Time:        startTime,
		EndTime:     endTime,
		PlayerSlot:  slot,
		PowerupType: powerupType,
		Duration:    endTime - startTime,
	}

	// Set UserID from our tracking map (used for Hub viewer track param)
	if userID, ok := a.playerUserIDs[slot]; ok {
		event.PlayerUserID = userID
	}

	// Resolve player name - try ctx.Players first
	if player := a.ctx.Players[slot]; player != nil {
		event.PlayerName = player.Name
		event.Team = player.Team
		// Also get UserID from player info if not already set
		if event.PlayerUserID == 0 && player.UserID != 0 {
			event.PlayerUserID = player.UserID
		}
	}

	// Fallback to local tracking
	if event.PlayerName == "" {
		if name, ok := a.playerNames[slot]; ok {
			event.PlayerName = name
		}
	}

	// Fallback to slot mapping
	if event.PlayerName == "" {
		event.PlayerName = slotToPlayer[slot]
	}

	// Resolve team if not set
	if event.Team == "" && event.PlayerName != "" {
		event.Team = nameToTeam[event.PlayerName]
	}
	if event.Team == "" {
		event.Team = slotToTeam[slot]
	}

	return event
}
