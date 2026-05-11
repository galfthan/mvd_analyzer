package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// hub.quakeworld.nu Supabase. The anon key is public — it's the same
// one shipped in the web bundle (mvd-web/static/app.js).
const (
	supabaseURL    = "https://ncsphkjfominimxztjip.supabase.co/rest/v1/v1_games"
	supabaseAPIKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6Im5jc3Boa2pmb21pbmlteHp0amlwIiwicm9sZSI6ImFub24iLCJpYXQiOjE2OTY5Mzg1NjMsImV4cCI6MjAxMjUxNDU2M30.NN6hjlEW-qB4Og9hWAVlgvUdwrbBO13s8OkAJuBGVbo"

	// Fields the search returns — mirrors the web's SEARCH_SELECT.
	supabaseSearchSelect = "id,timestamp,mode,matchtag,map,teams,players,demo_sha256,demo_source_url"
)

// searcher is the interface searchGames tool depends on, so tests can
// inject an httptest-faked Supabase.
type searcher interface {
	Search(ctx context.Context, in SearchGamesInput) (any, error)
}

// supabaseClient queries hub.quakeworld.nu's PostgREST surface
// directly. mvd-mcp uses this from the MCP shim so the search path
// doesn't route through mvd-api — discovery is the hub's
// responsibility, not ours, and the data is already public.
type supabaseClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newSupabaseClient(timeout time.Duration) *supabaseClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &supabaseClient{
		baseURL: supabaseURL,
		apiKey:  supabaseAPIKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Search runs a hub search with the given filters. Returns the raw
// PostgREST response as `[]any` of game rows (each row is a
// map[string]any with the SEARCH_SELECT fields).
func (s *supabaseClient) Search(ctx context.Context, in SearchGamesInput) (any, error) {
	parts := []string{
		"select=" + url.QueryEscape(supabaseSearchSelect),
		"order=timestamp.desc",
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	parts = append(parts, "limit="+strconv.Itoa(limit))
	if in.Offset > 0 {
		parts = append(parts, "offset="+strconv.Itoa(in.Offset))
	}

	for _, p := range in.Players {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// FTS with apostrophe quoting, AND'd via repeated filters
		// (PostgREST's default semantics for repeats on one column).
		parts = append(parts, "players_fts=fts.'"+url.QueryEscape(p)+"'")
	}
	for _, t := range in.Teams {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// cs (contains) on the team_names text[] column.
		parts = append(parts, "team_names=cs.{"+url.QueryEscape(t)+"}")
	}
	if in.Map != "" {
		parts = append(parts, "map=eq."+url.QueryEscape(in.Map))
	}
	if in.Mode != "" {
		parts = append(parts, "mode=eq."+url.QueryEscape(in.Mode))
	}
	if in.Matchtag != "" {
		parts = append(parts, "matchtag=ilike.%25"+url.QueryEscape(in.Matchtag)+"%25")
	}
	if in.From != "" {
		parts = append(parts, "timestamp=gte."+url.QueryEscape(in.From))
	}
	if in.To != "" {
		// Match the web's behaviour: include the full end day.
		parts = append(parts, "timestamp=lte."+url.QueryEscape(in.To+"T23:59:59"))
	}

	full := s.baseURL + "?" + strings.Join(parts, "&")
	req, err := http.NewRequestWithContext(ctx, "GET", full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", s.apiKey)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("accept-profile", "public")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub search: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var games []any
	if err := json.Unmarshal(body, &games); err != nil {
		return nil, fmt.Errorf("hub search: decode: %w", err)
	}
	return map[string]any{
		"limit":  limit,
		"offset": in.Offset,
		"count":  len(games),
		"games":  games,
	}, nil
}

var _ searcher = (*supabaseClient)(nil)
