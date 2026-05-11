package analyzer

// PlayerTrack contains all movement data for a single player.
type PlayerTrack struct {
	Team  string      `json:"team"`
	Lives []LifeTrack `json:"lives"`
}

// LifeTrack represents one life (spawn to death) for a player.
type LifeTrack struct {
	SpawnTime float64         `json:"spawnTime"`
	DeathTime float64         `json:"deathTime,omitempty"` // omit if still alive at match end
	Positions []TrackPosition `json:"positions"`
}

// TrackPosition is a single loc transition during a life.
type TrackPosition struct {
	Time     float64 `json:"time"`
	Location string  `json:"location"`
}

// TracksResult is the top-level structure for track export.
type TracksResult struct {
	Map     string                  `json:"map"`
	Players map[string]*PlayerTrack `json:"players"`
}

// ExtractTracks walks each player's PositionTrack and emits per-life
// movement tracks (spawn→death sequences of loc transitions).
//
// At schema v7 it operates on result.Streams natively: PositionTrack
// (with the Li column populated by the analyzer's loc resolution +
// blip filter) drives loc transitions; Spawns/Deaths timestamps drive
// life boundaries. No bucket intermediate.
//
// Currently shelved scaffolding for upcoming movement-pattern
// visualisations — the analyzer that wraps this is not registered
// in NewDefaultRegistry, so ExtractTracks has no production callers
// today. When the analyzer is revived, this implementation is the
// production-ready entry point.
func ExtractTracks(result *Result) *TracksResult {
	if result == nil || result.TimelineAnalysis == nil || result.Streams == nil {
		return nil
	}
	timeline := result.TimelineAnalysis

	mapName := ""
	if result.DemoInfo != nil {
		mapName = result.DemoInfo.Map
	}

	tracks := &TracksResult{
		Map:     mapName,
		Players: make(map[string]*PlayerTrack),
	}

	teamByName := make(map[string]string)
	if result.DemoInfo != nil {
		for _, p := range result.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				teamByName[p.Name] = p.Team
			}
		}
	}

	resolveLoc := func(li int16) string {
		if li > 0 && int(li) < len(timeline.LocTable) {
			return timeline.LocTable[li]
		}
		return ""
	}

	matchEnd := result.Streams.Global.MatchEnd

	for _, p := range result.Streams.Players {
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}

		spawns := p.Spawns
		deaths := p.Deaths

		var (
			sIdx, dIdx int
			alive      bool
			curLife    *LifeTrack
			lives      []LifeTrack
		)

		// processBoundaries advances the spawn/death cursors to time
		// `t` (inclusive), opening / closing lives along the way.
		// Spawn at exact t opens a life *before* the position sample
		// at t is evaluated; death at exact t closes it before — so
		// a sample that lands exactly on a death boundary is treated
		// as already-dead.
		processBoundaries := func(t float64) {
			for sIdx < len(spawns) || dIdx < len(deaths) {
				var nextT float64
				isSpawn := false
				switch {
				case sIdx < len(spawns) && (dIdx >= len(deaths) || spawns[sIdx] <= deaths[dIdx]):
					nextT = spawns[sIdx]
					isSpawn = true
				default:
					nextT = deaths[dIdx]
				}
				if nextT > t {
					return
				}
				if isSpawn {
					if !alive {
						alive = true
						curLife = &LifeTrack{SpawnTime: nextT}
					}
					sIdx++
				} else {
					if alive && curLife != nil {
						curLife.DeathTime = nextT
						lives = append(lives, *curLife)
						curLife = nil
					}
					alive = false
					dIdx++
				}
			}
		}

		for i := range pt.T {
			t := float64(pt.T[i])
			processBoundaries(t)
			if !alive || curLife == nil {
				continue
			}
			locName := resolveLoc(pt.Li[i])
			if locName == "" {
				continue
			}
			n := len(curLife.Positions)
			if n == 0 || curLife.Positions[n-1].Location != locName {
				curLife.Positions = append(curLife.Positions, TrackPosition{
					Time: t, Location: locName,
				})
			}
		}

		// Drain remaining boundaries past the last position sample
		// (e.g. a death at match end with no further positions).
		processBoundaries(matchEnd)

		// Finalize any life still open at match end (player alive
		// when the demo cut). DeathTime stays zero — JSON omitempty
		// will drop it.
		if alive && curLife != nil && len(curLife.Positions) > 0 {
			lives = append(lives, *curLife)
		}

		if len(lives) > 0 {
			tracks.Players[p.Name] = &PlayerTrack{
				Team:  teamByName[p.Name],
				Lives: lives,
			}
		}
	}

	return tracks
}
