package main

import (
	"context"
	"net/http"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
)

// searcher is the interface the searchGames tool depends on, so tests can
// inject an httptest-faked Supabase.
type searcher interface {
	Search(ctx context.Context, in SearchGamesInput) (any, error)
}

// supabaseClient adapts the shared hub search (hubfetch.Client.Search) to
// the MCP searchGames tool. The query semantics and response shape live in
// hubfetch so mvd-api and the MCP shim answer discovery identically; the
// shim keeps this thin adapter only to map SearchGamesInput onto
// hubfetch.SearchParams.
type supabaseClient struct {
	hub *hubfetch.Client
}

func newSupabaseClient(timeout time.Duration) *supabaseClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := hubfetch.NewClient()
	c.HTTP = &http.Client{Timeout: timeout}
	return &supabaseClient{hub: c}
}

// Search runs a hub search with the given filters, delegating to the shared
// hubfetch implementation. The response is the raw {limit, offset, count,
// total?, games} map hubfetch returns.
func (s *supabaseClient) Search(ctx context.Context, in SearchGamesInput) (any, error) {
	return s.hub.Search(ctx, hubfetch.SearchParams{
		Players:  in.Players,
		Teams:    in.Teams,
		Map:      in.Map,
		Mode:     in.Mode,
		Matchtag: in.Matchtag,
		From:     in.From,
		To:       in.To,
		Limit:    in.Limit,
		Offset:   in.Offset,
		Roster:   in.Roster,
	})
}

var _ searcher = (*supabaseClient)(nil)
