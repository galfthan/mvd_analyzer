package analyzer

import (
	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// TimelineAnalyzer tracks time-bucketed player state for timeline visualization
type TimelineAnalyzer struct {
	ctx            *Context
	bucketDuration float64
	playerState    map[int]*timelinePlayerState
	buckets        []*timelineBucketData
	lastSampleTime float64
	matchStartTime float64
	matchStarted   bool
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
}

// timelineBucketData holds raw aggregated data during analysis
type timelineBucketData struct {
	startTime float64
	endTime   float64
	teamData  map[string]*teamBucketRawData
}

// teamBucketRawData holds per-team aggregated data
type teamBucketRawData struct {
	// Granular weapon tracking
	playersWithRL   int // RL only
	playersWithLG   int // LG only
	playersWithRLLG int // Both RL and LG

	// Granular powerup tracking
	playersWithQuad int
	playersWithPent int
	playersWithRing int

	// Legacy totals (for backward compat)
	playersWithWeapons  int
	playersWithPowerups int

	// Health/armor
	healthSamples []int
	armorSamples  []int
	armorByType   map[string]int // ga/ya/ra -> count

	// Ammo
	totalShells  int
	totalNails   int
	totalRockets int
	totalCells   int
}

// NewTimelineAnalyzer creates a new timeline analyzer
func NewTimelineAnalyzer() *TimelineAnalyzer {
	return &TimelineAnalyzer{
		bucketDuration: 1.0, // 1 second buckets for detail resolution
		playerState:    make(map[int]*timelinePlayerState),
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

func (a *TimelineAnalyzer) handleStatUpdate(e *parser.StatUpdateEvent) error {
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

	// Aggregate stats by team
	for slot := 0; slot < mvd.MaxClients; slot++ {
		player := a.ctx.Players[slot]
		if player == nil || player.Team == "" || player.Spectator {
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

		teamData := a.getOrCreateTeamData(bucket, player.Team)

		// Track weapons granularly
		hasRL := (state.items & mvd.ITRocketLauncher) != 0
		hasLG := (state.items & mvd.ITLightning) != 0

		if hasRL && hasLG {
			teamData.playersWithRLLG++
			teamData.playersWithWeapons++
		} else if hasRL {
			teamData.playersWithRL++
			teamData.playersWithWeapons++
		} else if hasLG {
			teamData.playersWithLG++
			teamData.playersWithWeapons++
		}

		// Track powerups granularly
		hasQuad := (state.items & mvd.ITQuad) != 0
		hasPent := (state.items & mvd.ITInvulnerability) != 0
		hasRing := (state.items & mvd.ITInvisibility) != 0

		if hasQuad {
			teamData.playersWithQuad++
			teamData.playersWithPowerups++
		}
		if hasPent {
			teamData.playersWithPent++
			teamData.playersWithPowerups++
		}
		if hasRing {
			teamData.playersWithRing++
			teamData.playersWithPowerups++
		}

		// Track health and armor
		teamData.healthSamples = append(teamData.healthSamples, state.health)
		teamData.armorSamples = append(teamData.armorSamples, state.armor)

		// Track armor type
		if state.items&mvd.ITArmor3 != 0 {
			teamData.armorByType["ra"]++
		} else if state.items&mvd.ITArmor2 != 0 {
			teamData.armorByType["ya"]++
		} else if state.items&mvd.ITArmor1 != 0 {
			teamData.armorByType["ga"]++
		}

		// Track ammo totals
		teamData.totalShells += state.shells
		teamData.totalNails += state.nails
		teamData.totalRockets += state.rockets
		teamData.totalCells += state.cells
	}
}

func (a *TimelineAnalyzer) getOrCreateBucket(time float64) *timelineBucketData {
	bucketIndex := int(time / a.bucketDuration)

	// Extend buckets array if needed
	for len(a.buckets) <= bucketIndex {
		newBucket := &timelineBucketData{
			startTime: float64(len(a.buckets)) * a.bucketDuration,
			endTime:   float64(len(a.buckets)+1) * a.bucketDuration,
			teamData:  make(map[string]*teamBucketRawData),
		}
		a.buckets = append(a.buckets, newBucket)
	}

	return a.buckets[bucketIndex]
}

func (a *TimelineAnalyzer) getOrCreateTeamData(bucket *timelineBucketData, team string) *teamBucketRawData {
	if bucket.teamData[team] == nil {
		bucket.teamData[team] = &teamBucketRawData{
			armorByType: make(map[string]int),
		}
	}
	return bucket.teamData[team]
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

	result := &TimelineAnalysisResult{
		BucketDuration: a.bucketDuration,
		MatchStartTime: a.matchStartTime,
		Buckets:        make([]TimelineBucket, len(a.buckets)),
	}

	for i, b := range a.buckets {
		bucket := TimelineBucket{
			StartTime: b.startTime,
			EndTime:   b.endTime,
			TeamData:  make(map[string]*TeamBucketData),
		}

		for team, data := range b.teamData {
			bucket.TeamData[team] = &TeamBucketData{
				// Granular weapons
				PlayersWithRL:   data.playersWithRL,
				PlayersWithLG:   data.playersWithLG,
				PlayersWithRLLG: data.playersWithRLLG,

				// Granular powerups
				PlayersWithQuad: data.playersWithQuad,
				PlayersWithPent: data.playersWithPent,
				PlayersWithRing: data.playersWithRing,

				// Legacy totals
				PlayersWithWeapons:  data.playersWithWeapons,
				PlayersWithPowerups: data.playersWithPowerups,

				// Health/Armor
				AvgHealth:   average(data.healthSamples),
				AvgArmor:    average(data.armorSamples),
				TotalHealth: sum(data.healthSamples),
				TotalArmor:  sum(data.armorSamples),

				// Details
				ArmorByType:  data.armorByType,
				TotalShells:  data.totalShells,
				TotalNails:   data.totalNails,
				TotalRockets: data.totalRockets,
				TotalCells:   data.totalCells,
			}
		}

		result.Buckets[i] = bucket
	}

	return result, nil
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
