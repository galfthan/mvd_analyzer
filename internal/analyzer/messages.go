package analyzer

import (
	"strings"

	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
)

// MessagesAnalyzer captures frags and chat messages for timeline display
type MessagesAnalyzer struct {
	ctx    *Context
	events []MatchEvent
}

// NewMessagesAnalyzer creates a new messages analyzer
func NewMessagesAnalyzer() *MessagesAnalyzer {
	return &MessagesAnalyzer{
		events: make([]MatchEvent, 0),
	}
}

func (a *MessagesAnalyzer) Name() string { return "messages" }

func (a *MessagesAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *MessagesAnalyzer) OnEvent(event parser.Event) error {
	switch e := event.(type) {
	case *parser.PrintEvent:
		return a.handlePrint(e)
	}
	return nil
}

func (a *MessagesAnalyzer) handlePrint(e *parser.PrintEvent) error {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return nil
	}

	// Level 3 is PRINT_CHAT (mm1/mm2 messages)
	if e.Level == mvd.PrintChat {
		// Parse chat message format: "name: message" or "(team) name: message"
		event := a.parseChatMessage(msg, e.Time)
		if event != nil {
			a.events = append(a.events, *event)
		}
		return nil
	}

	// Try to parse as frag (levels 1-2 are typically obituaries)
	if e.Level <= 2 {
		frag := a.parseObituarySimple(msg, e.Time)
		if frag != nil {
			a.events = append(a.events, *frag)
		}
	}

	return nil
}

// parseChatMessage parses a chat message and extracts player, team, and text
func (a *MessagesAnalyzer) parseChatMessage(msg string, time float64) *MatchEvent {
	// Skip server messages and status messages
	if strings.HasPrefix(msg, "[") || strings.Contains(msg, " joined the game") ||
		strings.Contains(msg, " left the game") || strings.Contains(msg, " is ready") ||
		strings.Contains(msg, "The match has") || strings.Contains(msg, "countdown") {
		return nil
	}

	// QW teamsay format: "(playername): message" or "(playername) message"
	if strings.HasPrefix(msg, "(") {
		// Try "(name): " format first (most common)
		if idx := strings.Index(msg, "): "); idx > 0 {
			playerName := msg[1:idx]
			chatText := msg[idx+3:]

			// Find player's team by looking up the player
			team := a.getPlayerTeam(playerName)

			return &MatchEvent{
				Time:    time,
				Type:    "teamsay",
				Player:  playerName,
				Team:    team,
				Message: chatText,
			}
		}
		// Try "(name) " format (space after paren)
		if idx := strings.Index(msg, ") "); idx > 0 {
			playerName := msg[1:idx]
			chatText := msg[idx+2:]

			// Find player's team by looking up the player
			team := a.getPlayerTeam(playerName)

			return &MatchEvent{
				Time:    time,
				Type:    "teamsay",
				Player:  playerName,
				Team:    team,
				Message: chatText,
			}
		}
	}

	// Regular chat format: "name: message"
	colonIdx := strings.Index(msg, ": ")
	if colonIdx <= 0 {
		return nil
	}

	playerName := msg[:colonIdx]
	chatText := msg[colonIdx+2:]

	// Find player's team
	team := a.getPlayerTeam(playerName)

	return &MatchEvent{
		Time:    time,
		Type:    "chat",
		Player:  playerName,
		Team:    team,
		Message: chatText,
	}
}

// parseObituarySimple does a simplified frag parse for the timeline
func (a *MessagesAnalyzer) parseObituarySimple(msg string, time float64) *MatchEvent {
	// Check common kill patterns
	killPatterns := []struct {
		pattern string
		weapon  string
	}{
		{" was telefragged by ", "tele"},
		{" accepts ", "lg"},
		{" rides ", "rl"},
		{" eats ", "gl"},
		{" ate ", "ssg"},
		{" chewed on ", "sg"},
		{" was gibbed by ", "rl"},
		{" was nailed by ", "ng"},
		{" was perforated by ", "sng"},
		{" was ax-murdered by ", "axe"},
		{" was killed by ", "unknown"},
	}

	for _, p := range killPatterns {
		if idx := strings.Index(msg, p.pattern); idx > 0 {
			victim := strings.TrimSpace(msg[:idx])
			rest := msg[idx+len(p.pattern):]
			killer := a.extractKillerName(rest)

			if victim != "" && killer != "" && !isGenericPlayer(victim) && !isGenericPlayer(killer) {
				team := a.getPlayerTeam(killer)
				fragMsg := killer + " killed " + victim

				return &MatchEvent{
					Time:    time,
					Type:    "frag",
					Player:  killer,
					Team:    team,
					Message: fragMsg,
					Victim:  victim,
					Weapon:  p.weapon,
				}
			}
		}
	}

	// Check suicide patterns
	suicidePatterns := []string{
		" suicides", " cratered", " sleeps with the fishes", " burst into flames",
		" was squished", " blew up", " discovers blast radius",
	}

	for _, pattern := range suicidePatterns {
		if idx := strings.Index(msg, pattern); idx > 0 {
			victim := strings.TrimSpace(msg[:idx])
			if victim != "" && !isGenericPlayer(victim) {
				team := a.getPlayerTeam(victim)
				return &MatchEvent{
					Time:    time,
					Type:    "frag",
					Player:  victim,
					Team:    team,
					Message: victim + " died",
					Victim:  victim,
					Weapon:  "suicide",
				}
			}
		}
	}

	return nil
}

// extractKillerName extracts killer name, removing weapon suffixes
func (a *MessagesAnalyzer) extractKillerName(rest string) string {
	suffixes := []string{
		"'s quad shaft", "'s quad rocket", "'s quad pineapple",
		"'s shaft", "'s rocket", "'s pineapple", "'s boomstick", "'s buckshot",
	}

	for _, suffix := range suffixes {
		if idx := strings.Index(rest, suffix); idx > 0 {
			return strings.TrimSpace(rest[:idx])
		}
	}

	// Clean up
	rest = strings.TrimSuffix(rest, "\n")
	rest = strings.TrimSuffix(rest, ".")
	return strings.TrimSpace(rest)
}

// getPlayerTeam returns the team name for a player
func (a *MessagesAnalyzer) getPlayerTeam(name string) string {
	// First try exact match
	for i := 0; i < len(a.ctx.Players); i++ {
		p := a.ctx.Players[i]
		if p != nil && p.Name == name {
			return p.Team
		}
	}

	// Try normalized match (remove dots, special chars, lowercase)
	normalizedName := normalizeName(name)
	for i := 0; i < len(a.ctx.Players); i++ {
		p := a.ctx.Players[i]
		if p != nil && normalizeName(p.Name) == normalizedName {
			return p.Team
		}
	}

	// Try substring match (player name contains the lookup name or vice versa)
	for i := 0; i < len(a.ctx.Players); i++ {
		p := a.ctx.Players[i]
		if p != nil {
			pNorm := normalizeName(p.Name)
			if strings.Contains(pNorm, normalizedName) || strings.Contains(normalizedName, pNorm) {
				return p.Team
			}
		}
	}

	return ""
}

// normalizeName removes dots, special chars and lowercases for matching
func normalizeName(name string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func (a *MessagesAnalyzer) Finalize() (interface{}, error) {
	return &MessagesResult{
		Events: a.events,
	}, nil
}
