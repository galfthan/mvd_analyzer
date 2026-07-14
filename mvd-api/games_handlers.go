package main

import (
	"context"
	"net/http"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
)

// gameSearcher runs a hub.quakeworld.nu game search. *hubfetch.Client
// satisfies it; tests inject a fake. It is the one live-upstream dependency
// the demo-analysis handlers do not have, so it is isolated behind this
// seam and its failures map to the hub_upstream 502 convention.
type gameSearcher interface {
	Search(ctx context.Context, params hubfetch.SearchParams) (any, error)
}

// gamesSearchTimeout bounds a single upstream search so a slow/hung hub
// cannot pin a request goroutine indefinitely. The hub normally answers in
// well under a second; this is a generous ceiling, not a target.
const gamesSearchTimeout = 15 * time.Second

// handleGamesSearch: GET /v1/games/search — discovery over
// hub.quakeworld.nu's public game catalog. No demo is involved; this is the
// REST twin of the MCP searchGames tool, sharing the hubfetch query
// implementation so both answer identically.
//
// Query params (case-insensitive names; CSV where a list is accepted):
//
//	players   CSV of player names (FTS, AND'd across names)
//	teams     CSV of team names (contains)
//	map       exact map name
//	mode      exact game mode (1on1, 2on2, 4on4, FFA, …)
//	matchtag  case-insensitive substring of the tournament/event tag
//	from,to   ISO date bounds, inclusive (YYYY-MM-DD)
//	limit     max rows (default 20, capped at 100)
//	offset    pagination offset
//	roster    1/true = verbatim hub rows; default = compact {name,team,frags}
//
// Response: {limit, offset, count, total?, games}. Upstream errors (and a
// server with no hub configured) map to 502 hub_upstream.
func (s *server) handleGamesSearch(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		writeError(w, http.StatusBadGateway, "hub_upstream",
			"game search is not configured on this server")
		return
	}
	q := r.URL.Query()
	p := newQP(q)
	params := hubfetch.SearchParams{
		Players:  p.CSV("players"),
		Teams:    p.CSV("teams"),
		Map:      ciGet(q, "map"),
		Mode:     ciGet(q, "mode"),
		Matchtag: ciGet(q, "matchtag"),
		From:     ciGet(q, "from"),
		To:       ciGet(q, "to"),
		Limit:    p.Int("limit", 0),
		Offset:   p.Int("offset", 0),
		Roster:   p.Bool("roster"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesSearchTimeout)
	defer cancel()
	out, err := s.searcher.Search(ctx, params)
	if err != nil {
		// The search hits a live upstream (or is unconfigured); either way
		// this is a bad-gateway condition, not a client error. The message
		// is hub-supplied / config text — no local cache path leaks.
		writeError(w, http.StatusBadGateway, "hub_upstream", err.Error())
		return
	}
	// Discovery data is public and slow-moving but not immutable per-URL like
	// a demo's analysis, so no long-lived cache headers here — writeJSON's
	// bare 200 lets the default (revalidate) behaviour apply.
	writeJSON(w, http.StatusOK, out)
}
