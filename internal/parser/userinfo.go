package parser

import (
	"strconv"
	"strings"

	"github.com/mvd-analyzer/internal/mvd"
)

// UserInfoEvent is emitted when player info is updated
type UserInfoEvent struct {
	Player *mvd.PlayerInfo
	Time   float64
}

func (e *UserInfoEvent) EventType() EventType { return EventUserInfo }
func (e *UserInfoEvent) EventTime() float64   { return e.Time }

// parseUserInfo parses svc_updateuserinfo message
func (p *Parser) parseUserInfo(r *mvd.BufferReader, time float64) error {
	// Read player slot
	slot, err := r.ReadByte()
	if err != nil {
		return err
	}

	if slot >= mvd.MaxClients {
		// Invalid slot, skip
		r.ReadUint32() // user_id
		r.ReadString() // userinfo
		return nil
	}

	// Read user ID
	userID, err := r.ReadUint32()
	if err != nil {
		return err
	}

	// Read userinfo string
	userinfo, err := r.ReadString()
	if err != nil {
		return err
	}

	// Parse userinfo string
	player := p.players[slot]
	if player == nil {
		player = &mvd.PlayerInfo{Slot: int(slot)}
		p.players[slot] = player
	}

	player.UserID = int(userID)
	parseUserInfoString(userinfo, player)

	// Emit event
	return p.emit(&UserInfoEvent{Player: player, Time: time})
}

// parseUserInfoString parses a backslash-delimited userinfo string
// Format: \key1\value1\key2\value2\...
func parseUserInfoString(s string, player *mvd.PlayerInfo) {
	if s == "" {
		return
	}

	// Remove leading backslash if present
	if s[0] == '\\' {
		s = s[1:]
	}

	parts := strings.Split(s, "\\")
	for i := 0; i+1 < len(parts); i += 2 {
		key := parts[i]
		value := parts[i+1]

		switch key {
		case "name":
			player.Name = cleanString(value)
		case "team":
			player.Team = cleanString(value)
		case "topcolor":
			if c, err := strconv.Atoi(value); err == nil {
				player.TopColor = c
			}
		case "bottomcolor":
			if c, err := strconv.Atoi(value); err == nil {
				player.BottomColor = c
			}
		case "spectator":
			player.Spectator = value == "1"
		}
	}
}

// cleanString removes Quake color codes and control characters
func cleanString(s string) string {
	var result []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Handle high-bit characters (Quake gold text)
		if c >= 128 {
			c -= 128
		}
		// Skip control characters except basic printable
		if c >= 32 && c < 127 {
			result = append(result, c)
		}
	}
	return string(result)
}
