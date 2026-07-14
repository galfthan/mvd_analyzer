// Package hubfetch resolves and downloads MVD demos from
// hub.quakeworld.nu by game ID, and searches its game catalog. It mirrors
// the fetch flow already used by the web frontend (mvd-web/static/app.js,
// the SUPABASE_URL / CDN path): query the Supabase v1_games endpoint for
// the demo's sha256 + source URL, then try the public CDN before falling
// back to the original recording server.
//
// The Supabase URL, anon key, and CDN base are NOT compiled in — they are
// read from the environment (HUB_SUPABASE_URL / HUB_SUPABASE_KEY /
// HUB_CDN_URL) by NewClient. The anon key authenticates read-only access
// to the public catalog, but keeping it out of the source tree (and out of
// the deploy examples) means it can be rotated without a rebuild. When the
// vars are unset the client returns a clear "hub not configured" error on
// use rather than firing empty requests at a hardcoded host.
//
// Its consumers are the golden test harness
// (mvd-analytics/analyzer/golden_test.go), mvd-api (via
// mvd-api/internal/democache for demo fetch and directly for
// GET /v1/games/search), and the mvd-mcp shim's searchGames tool. It is
// intentionally small and has no dependency on the analyzer or result
// packages so it can be reused for ad-hoc tooling.
package hubfetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Environment variables that configure the hub client. NewClient reads
// them; unset URL or key makes every call return a "hub not configured"
// error (see configErr).
const (
	EnvSupabaseURL = "HUB_SUPABASE_URL" // v1_games PostgREST endpoint
	EnvSupabaseKey = "HUB_SUPABASE_KEY" // Supabase anon key (public, read-only)
	EnvCDNURL      = "HUB_CDN_URL"      // demo CDN base, e.g. https://d.quake.world
)

// maxDownloadBytes caps how many bytes a single demo download may read
// before the client rejects the response, so a broken or hostile upstream
// (the CDN, or a hub-supplied demo_source_url) cannot OOM the process with
// one oversized body. 64 MiB is far above any real MVD. A variable, not a
// const, only so tests can shrink it to exercise the boundary cheaply.
var maxDownloadBytes int64 = 64 << 20

// ErrNotFound is returned by Resolve when the hub has no row for the
// requested gameId (an empty result set). Callers should detect it with
// errors.Is rather than matching the message, so a hub outage whose body
// merely contains "not found" is not misclassified as a 404. See
// democache's classifyHubError.
var ErrNotFound = errors.New("not found")

// GameInfo is the minimal subset of the Supabase row that the
// downloader needs. The real schema has many more fields (teams, mode,
// timestamp, …) — leave them off here so we don't need to track
// schema drift for fields we never read.
type GameInfo struct {
	ID            int    `json:"id"`
	DemoSHA256    string `json:"demo_sha256"`
	DemoSourceURL string `json:"demo_source_url"`
}

// Client is a small wrapper around http.Client so tests can swap in
// httptest.Server URLs. SupabaseURL / APIKey / CDNBase come from the
// environment via NewClient; tests set them directly.
type Client struct {
	HTTP        *http.Client
	SupabaseURL string // v1_games PostgREST endpoint (HUB_SUPABASE_URL)
	APIKey      string // Supabase anon key (HUB_SUPABASE_KEY)
	CDNBase     string // demo CDN base (HUB_CDN_URL)
}

// NewClient returns a Client configured from the environment
// (HUB_SUPABASE_URL / HUB_SUPABASE_KEY / HUB_CDN_URL) with a 30 s HTTP
// timeout. Unset vars leave the corresponding field empty; Resolve /
// Search then return a "hub not configured" error rather than issuing a
// request against an empty host (mvd-api still starts fine for
// purely-local / cached use).
func NewClient() *Client {
	return &Client{
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		SupabaseURL: os.Getenv(EnvSupabaseURL),
		APIKey:      os.Getenv(EnvSupabaseKey),
		CDNBase:     os.Getenv(EnvCDNURL),
	}
}

// configErr reports whether the client has the minimum configuration to
// reach the hub (a Supabase URL and an API key). Callers return this
// before issuing a request so an unconfigured server gives an actionable
// message instead of a confusing empty-host failure. Through democache's
// classifyHubError it surfaces as ErrHubUpstream (a 502), and through
// GET /v1/games/search as hub_upstream — both explaining the missing env.
func (c *Client) configErr() error {
	if c.SupabaseURL == "" || c.APIKey == "" {
		return fmt.Errorf("hub not configured: set %s and %s", EnvSupabaseURL, EnvSupabaseKey)
	}
	return nil
}

// Resolve looks up game metadata by hub gameId. It returns the
// minimal info needed for download. The Supabase REST API answers
// `?id=eq.N` with a JSON array of rows; an empty array means the game
// does not exist.
func (c *Client) Resolve(gameID int) (*GameInfo, error) {
	if err := c.configErr(); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("select", "id,demo_sha256,demo_source_url")
	q.Set("id", "eq."+strconv.Itoa(gameID))
	req, err := http.NewRequest("GET", c.SupabaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.APIKey)
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("accept-profile", "public")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase resolve: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase resolve: status %d: %s", resp.StatusCode, string(body))
	}

	var rows []GameInfo
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("supabase resolve: decode: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("game %d %w", gameID, ErrNotFound)
	}
	return &rows[0], nil
}

// Download fetches the MVD bytes for a resolved game. It tries the
// public CDN first (path: <cdnBase>/<sha[:3]>/<sha>.mvd.gz) and falls
// back to demo_source_url if the CDN copy is missing or unreachable.
func (c *Client) Download(info *GameInfo) ([]byte, error) {
	if info == nil {
		return nil, errors.New("nil GameInfo")
	}

	// Path 1: CDN, when it is configured and we have a sha to address it.
	var cdnErr error
	if c.CDNBase != "" && len(info.DemoSHA256) >= 3 {
		cdnURL := fmt.Sprintf("%s/%s/%s.mvd.gz", c.CDNBase, info.DemoSHA256[:3], info.DemoSHA256)
		data, err := c.fetch(cdnURL)
		if err == nil {
			return data, nil
		}
		cdnErr = err
		// Fall through to source on CDN miss / error.
	}

	// Path 2: original recording server.
	if info.DemoSourceURL != "" {
		data, err := c.fetch(info.DemoSourceURL)
		if err != nil {
			return nil, fmt.Errorf("source download: %w", err)
		}
		return data, nil
	}

	// No source URL to fall back on. When a sha was present, the CDN
	// attempt is the real failure — report it rather than the misleading
	// "no sha256" message.
	if cdnErr != nil {
		return nil, fmt.Errorf("cdn: %v; no demo_source_url fallback", cdnErr)
	}
	return nil, errors.New("no download URL available (no sha256 and no demo_source_url)")
}

func (c *Client) fetch(u string) ([]byte, error) {
	resp, err := c.HTTP.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Read one byte past the cap: if we get maxDownloadBytes+1 bytes the body
	// is over the limit (a body that is exactly at the cap reads as
	// maxDownloadBytes and is accepted). Applies to both the CDN and the
	// demo_source_url paths, since both call fetch. An over-cap read is a
	// plain error here; democache wraps download failures as ErrHubUpstream
	// (never ErrDemoNotFound).
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxDownloadBytes {
		return nil, fmt.Errorf("response exceeds %d-byte cap", maxDownloadBytes)
	}
	return data, nil
}
