package hubfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearchSelect is the column set the game search returns — mirrors the
// web's SEARCH_SELECT and the fields the MCP searchGames tool documents.
const SearchSelect = "id,timestamp,mode,matchtag,map,teams,players,demo_sha256,demo_source_url"

// SearchParams are the filters for a hub game search. All fields are
// optional; an empty SearchParams returns the most recent matches. The
// zero value of Limit resolves to 20 (capped at 100).
type SearchParams struct {
	Players  []string // FTS on players_fts, AND'd across multiple
	Teams    []string // team names that must appear in team_names (contains)
	Map      string   // map name, exact match
	Mode     string   // game mode, exact match
	Matchtag string   // tournament/event tag, case-insensitive substring
	From     string   // ISO date lower bound, inclusive (YYYY-MM-DD)
	To       string   // ISO date upper bound, inclusive (YYYY-MM-DD)
	Limit    int      // max rows (default 20, capped at 100)
	Offset   int      // pagination offset
	Roster   bool     // true = verbatim hub rows; false = compact {name,team,frags}
}

// Search runs a hub game search with the given filters against the
// PostgREST v1_games surface. It returns the response as a
// map[string]any with keys {limit, offset, count, games} plus total when
// the hub reports a Content-Range. Rosters are compacted to
// {name, team, frags} unless params.Roster is set.
func (c *Client) Search(ctx context.Context, params SearchParams) (any, error) {
	parts := []string{
		"select=" + url.QueryEscape(SearchSelect),
		"order=timestamp.desc",
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	parts = append(parts, "limit="+strconv.Itoa(limit))
	if params.Offset > 0 {
		parts = append(parts, "offset="+strconv.Itoa(params.Offset))
	}

	for _, p := range params.Players {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// FTS with apostrophe quoting, AND'd via repeated filters
		// (PostgREST's default semantics for repeats on one column).
		parts = append(parts, "players_fts=fts.'"+url.QueryEscape(p)+"'")
	}
	for _, t := range params.Teams {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// cs (contains) on the team_names text[] column.
		parts = append(parts, "team_names=cs.{"+url.QueryEscape(t)+"}")
	}
	if params.Map != "" {
		parts = append(parts, "map=eq."+url.QueryEscape(params.Map))
	}
	if params.Mode != "" {
		parts = append(parts, "mode=eq."+url.QueryEscape(params.Mode))
	}
	if params.Matchtag != "" {
		parts = append(parts, "matchtag=ilike.%25"+url.QueryEscape(params.Matchtag)+"%25")
	}
	if params.From != "" {
		parts = append(parts, "timestamp=gte."+url.QueryEscape(params.From))
	}
	if params.To != "" {
		// Match the web's behaviour: include the full end day.
		parts = append(parts, "timestamp=lte."+url.QueryEscape(params.To+"T23:59:59"))
	}

	full := c.SupabaseURL + "?" + strings.Join(parts, "&")
	req, err := http.NewRequestWithContext(ctx, "GET", full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", SupabaseAPIKey)
	req.Header.Set("Authorization", "Bearer "+SupabaseAPIKey)
	req.Header.Set("accept-profile", "public")
	// Ask PostgREST for the total match count (Content-Range: 0-19/1234)
	// so pagination is honest: `count` is rows-in-this-page, `total` is
	// all matching rows. With a count preference PostgREST may answer
	// 206 Partial Content for a partial page — that is success here.
	req.Header.Set("Prefer", "count=exact")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub search: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("hub search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var games []any
	if err := json.Unmarshal(body, &games); err != nil {
		return nil, fmt.Errorf("hub search: decode: %w", err)
	}
	if !params.Roster {
		compactRosters(games)
	}
	out := map[string]any{
		"limit":  limit,
		"offset": params.Offset,
		"count":  len(games),
		"games":  games,
	}
	if total, ok := parseContentRangeTotal(resp.Header.Get("Content-Range")); ok {
		out["total"] = total
	}
	return out, nil
}

// compactRosters projects each game row's players array down to
// {name, team, frags} in place. The verbatim hub rows carry per-player
// ping, color arrays, name_color, team_color and is_bot — detail an
// agent picking demos never reads and that multiplies the payload ~4x
// (roster:true opts back in). Non-object entries pass through verbatim.
func compactRosters(games []any) {
	for _, g := range games {
		row, ok := g.(map[string]any)
		if !ok {
			continue
		}
		players, ok := row["players"].([]any)
		if !ok {
			continue
		}
		compact := make([]any, 0, len(players))
		for _, pl := range players {
			pm, ok := pl.(map[string]any)
			if !ok {
				compact = append(compact, pl)
				continue
			}
			c := make(map[string]any, 3)
			for _, k := range []string{"name", "team", "frags"} {
				if v, ok := pm[k]; ok {
					c[k] = v
				}
			}
			compact = append(compact, c)
		}
		row["players"] = compact
	}
}

// parseContentRangeTotal extracts the total from a PostgREST
// Content-Range header ("0-19/1234", or "*/0" for an empty result).
// ok=false when the header is absent or the total is unknown ("/*").
func parseContentRangeTotal(cr string) (int, bool) {
	i := strings.LastIndexByte(cr, '/')
	if i < 0 {
		return 0, false
	}
	total, err := strconv.Atoi(cr[i+1:])
	if err != nil {
		return 0, false
	}
	return total, true
}
