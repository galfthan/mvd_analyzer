package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// Supabase API endpoint for QuakeWorld Hub
	supabaseURL = "https://ncsphkjfominimxztjip.supabase.co/rest/v1/v1_games"
	// Public API key (read-only, safe to include)
	apiKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6Im5jc3Boa2pmb21pbmlteHp0amlwIiwicm9sZSI6ImFub24iLCJpYXQiOjE2OTY5Mzg1NjMsImV4cCI6MjAxMjUxNDU2M30.NN6hjlEW-qB4Og9hWAVlgvUdwrbBO13s8OkAJuBGVbo"
)

// Client is a client for the QuakeWorld Hub API
type Client struct {
	httpClient *http.Client
}

// NewClient creates a new Hub API client
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetGame fetches game information by game ID
func (c *Client) GetGame(gameID int) (*GameInfo, error) {
	reqURL := fmt.Sprintf("%s?select=*&id=eq.%d", supabaseURL, gameID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("apikey", apiKey)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("accept-profile", "public")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching game: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var games []GameInfo
	if err := json.NewDecoder(resp.Body).Decode(&games); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if len(games) == 0 {
		return nil, fmt.Errorf("game ID %d not found", gameID)
	}

	return &games[0], nil
}

// DownloadDemo downloads the demo file to the specified path
// It tries the CDN URL first, then falls back to the direct server URL
func (c *Client) DownloadDemo(game *GameInfo, destPath string) error {
	// Try CDN first
	cdnURL := game.GetCDNURL()
	if cdnURL != "" {
		err := c.downloadFile(cdnURL, destPath)
		if err == nil {
			return nil
		}
		// Log CDN failure and try fallback
		fmt.Printf("CDN download failed (%v), trying direct server...\n", err)
	}

	// Fall back to direct server URL
	if game.DemoSourceURL != "" {
		return c.downloadFile(game.DemoSourceURL, destPath)
	}

	return fmt.Errorf("no download URL available for game %d", game.ID)
}

// downloadFile downloads a file from URL to destPath
func (c *Client) downloadFile(url, destPath string) error {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// ParseGameID extracts a game ID from various input formats:
// - Plain number: "188692"
// - Hub URL: "https://hub.quakeworld.nu/games/?gameId=188692"
// - Hub URL with extra params: "https://hub.quakeworld.nu/games/?gameId=188692&from=72&track=1"
func ParseGameID(input string) (int, error) {
	input = strings.TrimSpace(input)

	// Try plain number first
	if id, err := strconv.Atoi(input); err == nil {
		return id, nil
	}

	// Try parsing as URL
	parsed, err := url.Parse(input)
	if err == nil {
		// Check for gameId query parameter
		gameIDStr := parsed.Query().Get("gameId")
		if gameIDStr != "" {
			id, err := strconv.Atoi(gameIDStr)
			if err == nil {
				return id, nil
			}
		}
	}

	// Try regex for any number that looks like a game ID
	re := regexp.MustCompile(`gameId=(\d+)`)
	matches := re.FindStringSubmatch(input)
	if len(matches) >= 2 {
		id, err := strconv.Atoi(matches[1])
		if err == nil {
			return id, nil
		}
	}

	return 0, fmt.Errorf("could not parse game ID from: %s", input)
}

// GenerateDemoFilename creates a filename for the demo based on game info
func (g *GameInfo) GenerateDemoFilename() string {
	// Format: mode_team1_vs_team2[map]timestamp.mvd.gz
	var teamNames []string
	for _, t := range g.Teams {
		teamNames = append(teamNames, sanitizeFilename(t.Name))
	}

	teamsStr := strings.Join(teamNames, "_vs_")
	if teamsStr == "" {
		teamsStr = "unknown"
	}

	timestamp := g.Timestamp.Format("20060102-1504")
	mapName := sanitizeFilename(g.Map)
	if mapName == "" {
		mapName = "unknown"
	}

	return fmt.Sprintf("%s_%s[%s]%s.mvd.gz", g.Mode, teamsStr, mapName, timestamp)
}

// sanitizeFilename removes or replaces characters that are invalid in filenames
func sanitizeFilename(s string) string {
	// Replace problematic characters
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	return replacer.Replace(s)
}
