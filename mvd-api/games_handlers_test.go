package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
)

// capturingSearcher records the params it was called with and returns a
// canned response, so tests can assert on query→params mapping.
type capturingSearcher struct {
	got  hubfetch.SearchParams
	out  any
	err  error
	seen bool
}

func (c *capturingSearcher) Search(_ context.Context, params hubfetch.SearchParams) (any, error) {
	c.got = params
	c.seen = true
	if c.err != nil {
		return nil, c.err
	}
	if c.out != nil {
		return c.out, nil
	}
	return map[string]any{"limit": 20, "offset": 0, "count": 0, "games": []any{}}, nil
}

func newSearchServer(t *testing.T, sr gameSearcher) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httptest.NewServer(newRouter(&fakeStore{}, logger, "", testUploadConfig, nil, nil, sr))
}

func TestGamesSearch_ParamMapping(t *testing.T) {
	sr := &capturingSearcher{}
	srv := newSearchServer(t, sr)
	defer srv.Close()

	m := getJSON(t, srv.URL+"/v1/games/search?players=bps,valla&teams=Die&map=dm6&mode=4on4&matchtag=qwsl&from=2025-01-01&to=2025-12-31&limit=50&offset=100&roster=1", 200)
	if m["count"] == nil {
		t.Errorf("response missing count: %v", m)
	}
	if !sr.seen {
		t.Fatal("searcher was not invoked")
	}
	g := sr.got
	if len(g.Players) != 2 || g.Players[0] != "bps" || g.Players[1] != "valla" {
		t.Errorf("players = %v; want [bps valla]", g.Players)
	}
	if len(g.Teams) != 1 || g.Teams[0] != "Die" {
		t.Errorf("teams = %v; want [Die]", g.Teams)
	}
	if g.Map != "dm6" || g.Mode != "4on4" || g.Matchtag != "qwsl" {
		t.Errorf("map/mode/matchtag = %q/%q/%q", g.Map, g.Mode, g.Matchtag)
	}
	if g.From != "2025-01-01" || g.To != "2025-12-31" {
		t.Errorf("from/to = %q/%q", g.From, g.To)
	}
	if g.Limit != 50 || g.Offset != 100 || !g.Roster {
		t.Errorf("limit/offset/roster = %d/%d/%v", g.Limit, g.Offset, g.Roster)
	}
}

// Param names resolve case-insensitively, matching every other endpoint.
func TestGamesSearch_CaseInsensitiveNames(t *testing.T) {
	sr := &capturingSearcher{}
	srv := newSearchServer(t, sr)
	defer srv.Close()

	getJSON(t, srv.URL+"/v1/games/search?Map=dm3&Mode=1on1", 200)
	if sr.got.Map != "dm3" || sr.got.Mode != "1on1" {
		t.Errorf("case-insensitive names not honoured: map=%q mode=%q", sr.got.Map, sr.got.Mode)
	}
}

func TestGamesSearch_UpstreamError(t *testing.T) {
	sr := &capturingSearcher{err: errors.New("hub search: status 500: boom")}
	srv := newSearchServer(t, sr)
	defer srv.Close()

	body, status := getRaw(t, srv.URL+"/v1/games/search")
	if status != 502 {
		t.Fatalf("status = %d, want 502 (body=%s)", status, body)
	}
	if !strings.Contains(string(body), `"hub_upstream"`) || !strings.Contains(string(body), "boom") {
		t.Errorf("body does not carry the hub_upstream code + upstream message: %s", body)
	}
}

func TestGamesSearch_InvalidParam(t *testing.T) {
	sr := &capturingSearcher{}
	srv := newSearchServer(t, sr)
	defer srv.Close()

	body, status := getRaw(t, srv.URL+"/v1/games/search?limit=abc")
	if status != 400 {
		t.Fatalf("status = %d, want 400 (body=%s)", status, body)
	}
	if sr.seen {
		t.Error("searcher must not be called when a param fails to parse")
	}
}

// A server with no searcher configured answers 502 hub_upstream rather than
// panicking on a nil dependency.
func TestGamesSearch_NotConfigured(t *testing.T) {
	srv := newSearchServer(t, nil)
	defer srv.Close()

	body, status := getRaw(t, srv.URL+"/v1/games/search")
	if status != 502 {
		t.Fatalf("status = %d, want 502 (body=%s)", status, body)
	}
	if !strings.Contains(string(body), `"hub_upstream"`) || !strings.Contains(string(body), "not configured") {
		t.Errorf("body does not explain the missing config: %s", body)
	}
}
