package main

import (
	"context"
	"fmt"
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
//	limit     max rows (omit for the default 20; explicit 0, > 100, or
//	          negative → 400 invalid_param)
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
	p := newQP(r.URL.Query())
	// An explicit limit= in the query string is distinguishable from an absent
	// one (both parse to 0 through p.Int) — capture presence so an explicit
	// limit=0 can be rejected loudly below while an omitted limit keeps the
	// downstream default.
	limitPresent := ciGet(r.URL.Query(), "limit") != ""
	params := hubfetch.SearchParams{
		Players:  p.CSV("players"),
		Teams:    p.CSV("teams"),
		Map:      p.Str("map"),
		Mode:     p.Str("mode"),
		Matchtag: p.Str("matchtag"),
		From:     p.Str("from"),
		To:       p.Str("to"),
		Limit:    p.Int("limit", 0),
		Offset:   p.Int("offset", 0),
		Roster:   p.Bool("roster"),
	}
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	// from/to are calendar dates, not times: validate them at the API
	// boundary as strict YYYY-MM-DD so a malformed value fails fast with a
	// client-side 400 instead of reaching the hub and surfacing as a 502
	// hub_upstream. hubfetch stays unchanged; the MCP searchGames tool
	// proxies this endpoint and inherits the check.
	for _, dp := range [...]struct{ key, v string }{{"from", params.From}, {"to", params.To}} {
		if dp.v == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", dp.v); err != nil || len(dp.v) != 10 {
			writeError(w, http.StatusBadRequest, "invalid_param",
				fmt.Sprintf("invalid %s=%q (want YYYY-MM-DD)", dp.key, dp.v))
			return
		}
	}
	// limit/offset are bounded at the API boundary rather than silently
	// clamped downstream (v57 reject-loudly posture): a limit above the hub's
	// 100-row page cap, or a negative limit/offset, 400s here instead of being
	// quietly corrected. An OMITTED limit stays "default" (hubfetch resolves it
	// to 20); an EXPLICIT limit=0 is distinguishable from absent and is rejected
	// loudly — a caller who typed 0 wants zero rows, which is never useful, so
	// point them at omitting the param instead. hubfetch keeps its own clamp as
	// a server-side belt (search.go).
	if limitPresent && params.Limit == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			"limit must be 1..100; omit it for the default 20")
		return
	}
	if params.Limit > 100 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("invalid limit=%d (max 100)", params.Limit))
		return
	}
	if params.Limit < 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("invalid limit=%d (must be >= 0)", params.Limit))
		return
	}
	if params.Offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("invalid offset=%d (must be >= 0)", params.Offset))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesSearchTimeout)
	defer cancel()
	out, err := s.searcher.Search(ctx, params)
	if err != nil {
		// The search hits a live upstream (or is unconfigured); either way
		// this is a bad-gateway condition, not a client error. The raw error
		// embeds the upstream PostgREST body and, on a transport failure, the
		// full hub URL+query — log it server-side against the request id and
		// hand the client a generic message so no upstream detail leaks.
		s.logger.Error("hub search upstream error",
			"request_id", requestID(r.Context()), "err", err.Error())
		writeError(w, http.StatusBadGateway, "hub_upstream", "game catalog search failed upstream")
		return
	}
	// Discovery data is public and slow-moving but not immutable per-URL like
	// a demo's analysis, so no long-lived cache headers here — writeJSON's
	// bare 200 lets the default (revalidate) behaviour apply.
	writeJSON(w, http.StatusOK, out)
}
