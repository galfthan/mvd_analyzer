package analyzer

import (
	"path/filepath"
	"strings"

	"github.com/mvd-analyzer/internal/parser"
)

// MatchAnalyzer extracts match summary information
type MatchAnalyzer struct {
	ctx         *Context
	duration    float64
	fragsByPlayer map[string]int
	deathsByPlayer map[string]int
}

// NewMatchAnalyzer creates a new match analyzer
func NewMatchAnalyzer() *MatchAnalyzer {
	return &MatchAnalyzer{
		fragsByPlayer:  make(map[string]int),
		deathsByPlayer: make(map[string]int),
	}
}

func (a *MatchAnalyzer) Name() string { return "match" }

func (a *MatchAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *MatchAnalyzer) OnEvent(event parser.Event) error {
	// Track duration
	a.duration = event.EventTime()

	// Track frag updates
	if fragEvent, ok := event.(*parser.FragUpdateEvent); ok {
		if fragEvent.PlayerNum >= 0 && fragEvent.PlayerNum < len(a.ctx.Players) {
			if a.ctx.Players[fragEvent.PlayerNum] != nil {
				name := a.ctx.Players[fragEvent.PlayerNum].Name
				a.fragsByPlayer[name] = fragEvent.Frags
			}
		}
	}

	return nil
}

func (a *MatchAnalyzer) Finalize() (interface{}, error) {
	result := &MatchResult{
		Duration: a.duration,
	}

	// Get map name from server data
	if a.ctx.ServerData != nil {
		result.Map = extractMapName(a.ctx.ServerData.LevelName)
		result.GameDir = a.ctx.ServerData.GameDir
	}

	// Collect team stats
	teamFrags := make(map[string]int)

	// Collect player stats
	for i := 0; i < len(a.ctx.Players); i++ {
		p := a.ctx.Players[i]
		if p == nil || p.Name == "" || p.Spectator {
			continue
		}

		stat := PlayerStat{
			Name:  p.Name,
			Team:  p.Team,
			Frags: p.Frags,
		}

		// Use tracked frags if available
		if frags, ok := a.fragsByPlayer[p.Name]; ok {
			stat.Frags = frags
		}

		result.Players = append(result.Players, stat)

		// Aggregate team frags
		if p.Team != "" {
			teamFrags[p.Team] += stat.Frags
		}
	}

	// Build team stats
	for team, frags := range teamFrags {
		result.Teams = append(result.Teams, TeamStat{
			Name:  team,
			Frags: frags,
		})
	}

	return result, nil
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
