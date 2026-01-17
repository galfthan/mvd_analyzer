package hub

import (
	"encoding/json"
	"time"
)

// GameInfo represents a game from the QuakeWorld Hub API
type GameInfo struct {
	ID            int       `json:"id"`
	DemoSHA256    string    `json:"demo_sha256"`
	DemoSourceURL string    `json:"demo_source_url"`
	Map           string    `json:"map"`
	Mode          string    `json:"mode"`
	Matchtag      string    `json:"matchtag"`
	ServerName    string    `json:"server_hostname"`
	Timestamp     time.Time `json:"timestamp"`
	Teams         []Team    `json:"teams"`
	Players       []Player  `json:"players"`
}

// Team represents a team in a game
type Team struct {
	Name  string `json:"name"`
	Frags int    `json:"frags"`
	Ping  int    `json:"ping"`
	Color []int  `json:"color"`
}

// UnmarshalJSON handles the nested JSON structure for teams
func (t *Team) UnmarshalJSON(data []byte) error {
	// Teams come as JSON strings within the array, need to handle both cases
	var raw struct {
		Name  string `json:"name"`
		Frags int    `json:"frags"`
		Ping  int    `json:"ping"`
		Color []int  `json:"color"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Name = raw.Name
	t.Frags = raw.Frags
	t.Ping = raw.Ping
	t.Color = raw.Color
	return nil
}

// Player represents a player in a game
type Player struct {
	Name  string `json:"name"`
	Team  string `json:"team"`
	Frags int    `json:"frags"`
	Ping  int    `json:"ping"`
	IsBot bool   `json:"is_bot"`
	Color []int  `json:"color"`
}

// UnmarshalJSON handles the nested JSON structure for players
func (p *Player) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name  string `json:"name"`
		Team  string `json:"team"`
		Frags int    `json:"frags"`
		Ping  int    `json:"ping"`
		IsBot bool   `json:"is_bot"`
		Color []int  `json:"color"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Team = raw.Team
	p.Frags = raw.Frags
	p.Ping = raw.Ping
	p.IsBot = raw.IsBot
	p.Color = raw.Color
	return nil
}

// GetCDNURL returns the CDN URL for downloading the demo
func (g *GameInfo) GetCDNURL() string {
	if g.DemoSHA256 == "" || len(g.DemoSHA256) < 3 {
		return ""
	}
	prefix := g.DemoSHA256[:3]
	return "https://d.quake.world/" + prefix + "/" + g.DemoSHA256 + ".mvd.gz"
}

// GetViewerURL returns the URL to view this game in the online viewer
func (g *GameInfo) GetViewerURL() string {
	return "https://hub.quakeworld.nu/games/?gameId=" + string(rune(g.ID+'0'))
}

// GetViewerURLWithTime returns the URL to view this game at a specific time and player
func (g *GameInfo) GetViewerURLWithTime(seconds int, playerIndex int) string {
	return "https://hub.quakeworld.nu/games/?gameId=" + itoa(g.ID) + "&from=" + itoa(seconds) + "&track=" + itoa(playerIndex)
}

// itoa converts int to string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
