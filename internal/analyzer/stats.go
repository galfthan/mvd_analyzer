package analyzer

import (
	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// StatsAnalyzer tracks player stats over time
type StatsAnalyzer struct {
	ctx         *Context
	playerStats map[int]*trackedStats
}

type trackedStats struct {
	maxHealth int
	maxArmor  int
}

// NewStatsAnalyzer creates a new stats analyzer
func NewStatsAnalyzer() *StatsAnalyzer {
	return &StatsAnalyzer{
		playerStats: make(map[int]*trackedStats),
	}
}

func (a *StatsAnalyzer) Name() string { return "stats" }

func (a *StatsAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *StatsAnalyzer) OnEvent(event parser.Event) error {
	statEvent, ok := event.(*parser.StatUpdateEvent)
	if !ok {
		return nil
	}

	stats := a.getOrCreate(statEvent.PlayerNum)

	switch statEvent.StatIndex {
	case mvd.StatHealth:
		if statEvent.Value > stats.maxHealth {
			stats.maxHealth = statEvent.Value
		}
	case mvd.StatArmor:
		if statEvent.Value > stats.maxArmor {
			stats.maxArmor = statEvent.Value
		}
	}

	return nil
}

func (a *StatsAnalyzer) Finalize() (interface{}, error) {
	result := &StatsResult{
		PlayerStats: make(map[string]*PlayerStatsEntry),
	}

	for playerNum, stats := range a.playerStats {
		if a.ctx.Players[playerNum] != nil {
			name := a.ctx.Players[playerNum].Name
			if name != "" {
				result.PlayerStats[name] = &PlayerStatsEntry{
					MaxHealth: stats.maxHealth,
					MaxArmor:  stats.maxArmor,
				}
			}
		}
	}

	return result, nil
}

func (a *StatsAnalyzer) getOrCreate(playerNum int) *trackedStats {
	if s, ok := a.playerStats[playerNum]; ok {
		return s
	}
	s := &trackedStats{}
	a.playerStats[playerNum] = s
	return s
}
