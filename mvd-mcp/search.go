package main

import (
	"context"
	"net/url"
)

// searcher is the interface the searchGames tool depends on, so tests can
// inject a fake mvd-api. *proxyBackend satisfies it: search is proxied to
// mvd-api's GET /v1/games/search like every other tool, so the shim has a
// single egress point (mvd-api) and holds no hub configuration or secrets.
type searcher interface {
	Search(ctx context.Context, in SearchGamesInput) (any, error)
}

// Search proxies a game-catalog search to mvd-api's GET /v1/games/search,
// translating SearchGamesInput into the endpoint's query params (players and
// teams as CSV; the rest one-to-one). The response — the {limit, offset,
// count, total?, games} envelope — is passed through verbatim. Reuses the
// proxy backend's do/fetchOpaque path, so it shares the Authorization header,
// retry, and error-mapping (a 502 hub_upstream surfaces the API's message)
// with every per-demo tool. mvd-api owns the hub connection; the shim needs
// no hub config of its own.
func (p *proxyBackend) Search(ctx context.Context, in SearchGamesInput) (any, error) {
	q := query{}
	q.csv("players", in.Players)
	q.csv("teams", in.Teams)
	q.str("map", in.Map)
	q.str("mode", in.Mode)
	q.str("matchtag", in.Matchtag)
	q.str("from", in.From)
	q.str("to", in.To)
	q.intv("limit", in.Limit)
	q.intv("offset", in.Offset)
	q.boolean("roster", in.Roster)
	return p.fetchOpaque(ctx, "GET", "/v1/games/search", url.Values(q))
}

var _ searcher = (*proxyBackend)(nil)
