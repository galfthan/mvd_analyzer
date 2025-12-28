// Package analyzer provides pluggable analysis modules for MVD demos.
package analyzer

import (
	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// Context provides shared state for analyzers
type Context struct {
	ServerData *mvd.ServerData
	Players    [mvd.MaxClients]*mvd.PlayerInfo
}

// Analyzer is the interface for analysis modules
type Analyzer interface {
	// Name returns the unique identifier for this analyzer
	Name() string

	// Init is called before parsing begins
	Init(ctx *Context) error

	// OnEvent receives parsed events during parsing
	OnEvent(event parser.Event) error

	// Finalize is called after parsing completes and returns results
	Finalize() (interface{}, error)
}

// Result holds results from all analyzers
type Result struct {
	FilePath    string             `json:"filePath"`
	Duration    float64            `json:"duration"`
	Match       *MatchResult       `json:"match,omitempty"`
	Frags       *FragResult        `json:"frags,omitempty"`
	Stats       *StatsResult       `json:"stats,omitempty"`
	WeaponStats *WeaponStatsResult `json:"weaponStats,omitempty"`
	Errors      []string           `json:"errors,omitempty"`
}

// MatchResult contains match summary information
type MatchResult struct {
	Map       string       `json:"map"`
	GameDir   string       `json:"gameDir"`
	Duration  float64      `json:"duration"`
	StartTime float64      `json:"startTime,omitempty"`
	EndTime   float64      `json:"endTime,omitempty"`
	Players   []PlayerStat `json:"players"`
	Teams     []TeamStat   `json:"teams,omitempty"`
}

// PlayerStat represents a player's final statistics
type PlayerStat struct {
	Name   string `json:"name"`
	Team   string `json:"team"`
	Kills  int    `json:"kills"`
	Deaths int    `json:"deaths"`
	Frags  int    `json:"frags"`
}

// TeamStat represents a team's statistics
type TeamStat struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
}

// FragResult contains frag analysis results
type FragResult struct {
	TotalFrags int         `json:"totalFrags"`
	Frags      []FragEntry `json:"frags"`
	ByWeapon   map[string]int `json:"byWeapon"`
	ByPlayer   map[string]*PlayerFrags `json:"byPlayer"`
}

// FragEntry represents a single frag event
type FragEntry struct {
	Time       float64 `json:"time"`
	Killer     string  `json:"killer"`
	Victim     string  `json:"victim"`
	Weapon     string  `json:"weapon"`
	IsSuicide  bool    `json:"isSuicide,omitempty"`
	IsTeamKill bool    `json:"isTeamKill,omitempty"`
}

// PlayerFrags holds per-player frag statistics
type PlayerFrags struct {
	Kills  int            `json:"kills"`
	Deaths int            `json:"deaths"`
	ByWeapon map[string]int `json:"byWeapon"`
}

// StatsResult contains stats tracking results
type StatsResult struct {
	PlayerStats map[string]*PlayerStatsEntry `json:"playerStats"`
}

// PlayerStatsEntry holds final stats for a player
type PlayerStatsEntry struct {
	MaxHealth int `json:"maxHealth"`
	MaxArmor  int `json:"maxArmor"`
}

// WeaponStatsResult contains weapon usage statistics
type WeaponStatsResult struct {
	PlayerStats map[string]*PlayerWeaponStatsEntry `json:"playerStats"`
}

// PlayerWeaponStatsEntry holds weapon stats for a player
type PlayerWeaponStatsEntry struct {
	Weapons     map[string]*WeaponStatEntry `json:"weapons"`
	Environment *EnvironmentalDamage        `json:"environment,omitempty"`
}

// EnvironmentalDamage tracks damage received from environmental sources
type EnvironmentalDamage struct {
	Lava    int `json:"lava,omitempty"`    // Damage from lava
	Slime   int `json:"slime,omitempty"`   // Damage from slime
	Drown   int `json:"drown,omitempty"`   // Damage from drowning
	Fall    int `json:"fall,omitempty"`    // Fall damage
	Squish  int `json:"squish,omitempty"`  // Crush damage (world-attributed)
	Trigger int `json:"trigger,omitempty"` // trigger_hurt damage
}

// WeaponStatEntry holds statistics for a single weapon
type WeaponStatEntry struct {
	Shots      int     `json:"shots"`
	Hits       int     `json:"hits"`
	Damage     int     `json:"damage"`
	Overkill   int     `json:"overkill,omitempty"`
	TeamDamage int     `json:"teamDamage,omitempty"`
	SelfDamage int     `json:"selfDamage,omitempty"`
	Accuracy   float64 `json:"accuracy"`
}
