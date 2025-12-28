package analyzer

import (
	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// WeaponStatsAnalyzer tracks weapon usage statistics
type WeaponStatsAnalyzer struct {
	ctx         *Context
	playerStats map[int]*playerWeaponStats
}

type playerWeaponStats struct {
	// Current state for tracking
	activeWeapon int
	shells       int
	nails        int
	rockets      int
	cells        int
	health       int // Current health for damage capping

	// Accumulated stats per weapon
	weapons map[string]*weaponData
}

type weaponData struct {
	Shots      int // Tracked via ammo decreases
	Hits       int // Tracked via damage events (non-splash direct hits)
	Damage     int // Effective damage dealt to enemies (capped at target health)
	Overkill   int // Damage beyond what was needed to kill (raw - effective)
	TeamDamage int // Damage dealt to teammates
	SelfDamage int // Self-inflicted damage
}

// NewWeaponStatsAnalyzer creates a new weapon stats analyzer
func NewWeaponStatsAnalyzer() *WeaponStatsAnalyzer {
	return &WeaponStatsAnalyzer{
		playerStats: make(map[int]*playerWeaponStats),
	}
}

func (a *WeaponStatsAnalyzer) Name() string { return "weaponstats" }

func (a *WeaponStatsAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *WeaponStatsAnalyzer) OnEvent(event parser.Event) error {
	switch e := event.(type) {
	case *parser.StatUpdateEvent:
		return a.handleStatUpdate(e)
	case *parser.DamageEvent:
		return a.handleDamage(e)
	}
	return nil
}

func (a *WeaponStatsAnalyzer) handleStatUpdate(e *parser.StatUpdateEvent) error {
	stats := a.getOrCreate(e.PlayerNum)

	switch e.StatIndex {
	case mvd.StatHealth:
		stats.health = e.Value

	case mvd.StatActiveWeapon:
		stats.activeWeapon = e.Value

	case mvd.StatShells:
		if stats.shells > 0 && e.Value < stats.shells && e.Value >= 0 {
			decrease := stats.shells - e.Value
			// Only count as shots if active weapon uses shells
			if a.isShellWeapon(stats.activeWeapon) {
				weapon := a.getShellWeapon(stats.activeWeapon)
				ammoPerShot := 1
				if stats.activeWeapon == mvd.ITSuperShotgun {
					ammoPerShot = 2
				}
				shots := decrease / ammoPerShot
				if shots > 0 {
					a.recordShot(stats, weapon, shots)
				}
			}
		}
		stats.shells = e.Value

	case mvd.StatNails:
		if stats.nails > 0 && e.Value < stats.nails && e.Value >= 0 {
			decrease := stats.nails - e.Value
			// Only count as shots if active weapon uses nails
			if a.isNailWeapon(stats.activeWeapon) {
				weapon := a.getNailWeapon(stats.activeWeapon)
				ammoPerShot := 1
				if stats.activeWeapon == mvd.ITSuperNailgun {
					ammoPerShot = 2
				}
				shots := decrease / ammoPerShot
				if shots > 0 {
					a.recordShot(stats, weapon, shots)
				}
			}
		}
		stats.nails = e.Value

	case mvd.StatRockets:
		if stats.rockets > 0 && e.Value < stats.rockets && e.Value >= 0 {
			decrease := stats.rockets - e.Value
			// Only count as shots if active weapon uses rockets
			if a.isRocketWeapon(stats.activeWeapon) {
				weapon := a.getRocketWeapon(stats.activeWeapon)
				a.recordShot(stats, weapon, decrease) // 1 rocket per shot for both GL and RL
			}
		}
		stats.rockets = e.Value

	case mvd.StatCells:
		if stats.cells > 0 && e.Value < stats.cells && e.Value >= 0 {
			decrease := stats.cells - e.Value
			// Only count if active weapon is LG
			if a.isLGWeapon(stats.activeWeapon) {
				a.recordShot(stats, "lg", decrease) // 1 cell per LG "tick"
			}
		}
		stats.cells = e.Value
	}

	return nil
}

func (a *WeaponStatsAnalyzer) handleDamage(e *parser.DamageEvent) error {
	weapon := mvd.DeathTypeToWeapon(e.DeathType)
	if weapon == "unknown" {
		return nil
	}

	// Track attacker's damage output
	if e.Attacker >= 0 && e.Attacker < mvd.MaxClients {
		attacker := a.getOrCreate(e.Attacker)
		wd := attacker.getWeapon(weapon)

		if e.Attacker == e.Victim {
			// Self-damage (cap at own health)
			victim := a.getOrCreate(e.Victim)
			effectiveDmg := e.Damage
			if victim.health > 0 && effectiveDmg > victim.health {
				effectiveDmg = victim.health
			}
			wd.SelfDamage += effectiveDmg
		} else {
			// Get victim's health for damage capping
			victim := a.getOrCreate(e.Victim)
			rawDamage := e.Damage
			effectiveDamage := rawDamage

			// Cap damage at victim's current health (like KTX does)
			if victim.health > 0 && effectiveDamage > victim.health {
				effectiveDamage = victim.health
			}
			overkill := rawDamage - effectiveDamage

			// Check if team damage
			isTeamDamage := a.isTeamDamage(e.Attacker, e.Victim)
			if isTeamDamage {
				wd.TeamDamage += effectiveDamage
			} else {
				wd.Damage += effectiveDamage
				wd.Overkill += overkill
				// Count hits from direct damage (not splash) to enemies only
				if !e.IsSplash {
					wd.Hits++
				}
			}

			// Update victim's health (approximate - may go negative on death)
			if victim.health > 0 {
				victim.health -= effectiveDamage
				if victim.health < 0 {
					victim.health = 0
				}
			}
		}
	}

	return nil
}

// isTeamDamage checks if attacker and victim are on the same team
func (a *WeaponStatsAnalyzer) isTeamDamage(attackerNum, victimNum int) bool {
	if a.ctx == nil {
		return false
	}
	attacker := a.ctx.Players[attackerNum]
	victim := a.ctx.Players[victimNum]
	if attacker == nil || victim == nil {
		return false
	}
	// Same team if team names match and are non-empty
	return attacker.Team != "" && attacker.Team == victim.Team
}

// recordShot records shots fired for a weapon
func (a *WeaponStatsAnalyzer) recordShot(stats *playerWeaponStats, weapon string, count int) {
	if weapon == "" {
		return
	}
	wd := stats.getWeapon(weapon)
	wd.Shots += count
}

// isShellWeapon checks if active weapon uses shells
func (a *WeaponStatsAnalyzer) isShellWeapon(activeWeapon int) bool {
	return activeWeapon == mvd.ITShotgun || activeWeapon == mvd.ITSuperShotgun
}

// isNailWeapon checks if active weapon uses nails
func (a *WeaponStatsAnalyzer) isNailWeapon(activeWeapon int) bool {
	return activeWeapon == mvd.ITNailgun || activeWeapon == mvd.ITSuperNailgun
}

// isRocketWeapon checks if active weapon uses rockets
func (a *WeaponStatsAnalyzer) isRocketWeapon(activeWeapon int) bool {
	return activeWeapon == mvd.ITGrenadeLauncher || activeWeapon == mvd.ITRocketLauncher
}

// isLGWeapon checks if active weapon uses cells
func (a *WeaponStatsAnalyzer) isLGWeapon(activeWeapon int) bool {
	return activeWeapon == mvd.ITLightning || activeWeapon == mvd.ITSuperLightning
}

// getShellWeapon determines if SG or SSG based on active weapon
func (a *WeaponStatsAnalyzer) getShellWeapon(activeWeapon int) string {
	switch activeWeapon {
	case mvd.ITSuperShotgun:
		return "ssg"
	case mvd.ITShotgun:
		return "sg"
	default:
		// If weapon doesn't match, guess based on common usage
		// SSG is more commonly used
		if activeWeapon&mvd.ITSuperShotgun != 0 {
			return "ssg"
		}
		return "sg"
	}
}

// getNailWeapon determines if NG or SNG based on active weapon
func (a *WeaponStatsAnalyzer) getNailWeapon(activeWeapon int) string {
	switch activeWeapon {
	case mvd.ITSuperNailgun:
		return "sng"
	case mvd.ITNailgun:
		return "ng"
	default:
		if activeWeapon&mvd.ITSuperNailgun != 0 {
			return "sng"
		}
		return "ng"
	}
}

// getRocketWeapon determines if GL or RL based on active weapon
func (a *WeaponStatsAnalyzer) getRocketWeapon(activeWeapon int) string {
	switch activeWeapon {
	case mvd.ITRocketLauncher:
		return "rl"
	case mvd.ITGrenadeLauncher:
		return "gl"
	default:
		if activeWeapon&mvd.ITRocketLauncher != 0 {
			return "rl"
		}
		return "gl"
	}
}

func (a *WeaponStatsAnalyzer) Finalize() (interface{}, error) {
	result := &WeaponStatsResult{
		PlayerStats: make(map[string]*PlayerWeaponStatsEntry),
	}

	for playerNum, stats := range a.playerStats {
		if a.ctx.Players[playerNum] != nil {
			name := a.ctx.Players[playerNum].Name
			if name != "" && len(stats.weapons) > 0 {
				entry := &PlayerWeaponStatsEntry{
					Weapons: make(map[string]*WeaponStatEntry),
				}
				for weapon, wd := range stats.weapons {
					entry.Weapons[weapon] = &WeaponStatEntry{
						Shots:      wd.Shots,
						Hits:       wd.Hits,
						Damage:     wd.Damage,
						Overkill:   wd.Overkill,
						TeamDamage: wd.TeamDamage,
						SelfDamage: wd.SelfDamage,
						Accuracy:   calculateAccuracy(wd.Shots, wd.Hits),
					}
				}
				result.PlayerStats[name] = entry
			}
		}
	}

	return result, nil
}

func calculateAccuracy(shots, hits int) float64 {
	if shots == 0 {
		return 0
	}
	return float64(hits) / float64(shots) * 100
}

func (a *WeaponStatsAnalyzer) getOrCreate(playerNum int) *playerWeaponStats {
	if s, ok := a.playerStats[playerNum]; ok {
		return s
	}
	s := &playerWeaponStats{
		weapons: make(map[string]*weaponData),
	}
	a.playerStats[playerNum] = s
	return s
}

func (s *playerWeaponStats) getWeapon(name string) *weaponData {
	if wd, ok := s.weapons[name]; ok {
		return wd
	}
	wd := &weaponData{}
	s.weapons[name] = wd
	return wd
}
