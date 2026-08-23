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
	// The client sees the hub_upstream code and a generic message; the raw
	// upstream detail (which can embed the hub URL/query on a transport
	// failure) is logged server-side, not returned.
	if !strings.Contains(string(body), `"hub_upstream"`) || !strings.Contains(string(body), "game catalog search failed upstream") {
		t.Errorf("body does not carry the hub_upstream code + generic message: %s", body)
	}
	if strings.Contains(string(body), "boom") {
		t.Errorf("body leaks verbatim upstream detail: %s", body)
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

// limit/offset are bounded at the API boundary rather than silently clamped
// downstream (v57 reject-loudly posture): limit above hubfetch.MaxSearchLimit,
// an explicit limit=0, and negative limit/offset all 400 invalid_param; an
// omitted limit keeps the default; a valid limit reaches the searcher
// unchanged.
func TestGamesSearch_LimitOffsetBounds(t *testing.T) {
	t.Run("limit-too-large", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		body, status := getRaw(t, srv.URL+"/v1/games/search?limit=1001")
		if status != 400 {
			t.Fatalf("status = %d, want 400 (body=%s)", status, body)
		}
		if !strings.Contains(string(body), `"invalid_param"`) || !strings.Contains(string(body), "max 1000") {
			t.Errorf("body missing invalid_param/max-1000 detail: %s", body)
		}
		if sr.seen {
			t.Error("searcher must not be called when limit is out of range")
		}
	})

	t.Run("limit-at-cap-reaches-searcher", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		getJSON(t, srv.URL+"/v1/games/search?limit=1000", 200)
		if !sr.seen {
			t.Fatal("searcher was not invoked for limit=1000")
		}
		if sr.got.Limit != hubfetch.MaxSearchLimit {
			t.Errorf("limit = %d; want %d", sr.got.Limit, hubfetch.MaxSearchLimit)
		}
	})

	t.Run("limit-zero-rejected", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		// An EXPLICIT limit=0 is distinguishable from an absent limit and is
		// rejected loudly (v57 posture) rather than treated as the default.
		body, status := getRaw(t, srv.URL+"/v1/games/search?limit=0")
		if status != 400 {
			t.Fatalf("status = %d, want 400 (body=%s)", status, body)
		}
		if !strings.Contains(string(body), `"invalid_param"`) || !strings.Contains(string(body), "omit it for the default 20") {
			t.Errorf("body missing invalid_param/omit-hint detail: %s", body)
		}
		if sr.seen {
			t.Error("searcher must not be called when limit=0")
		}
	})

	t.Run("limit-absent-reaches-searcher-as-default", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		// No limit param: 0 flows to the searcher as the "default" sentinel
		// (hubfetch resolves it to 20).
		getJSON(t, srv.URL+"/v1/games/search", 200)
		if !sr.seen {
			t.Fatal("searcher was not invoked for an absent limit")
		}
		if sr.got.Limit != 0 {
			t.Errorf("limit = %d; want 0 (default sentinel)", sr.got.Limit)
		}
	})

	t.Run("negative-offset", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		body, status := getRaw(t, srv.URL+"/v1/games/search?offset=-1")
		if status != 400 {
			t.Fatalf("status = %d, want 400 (body=%s)", status, body)
		}
		if !strings.Contains(string(body), `"invalid_param"`) {
			t.Errorf("body missing invalid_param code: %s", body)
		}
		if sr.seen {
			t.Error("searcher must not be called when offset is negative")
		}
	})

	t.Run("negative-limit", func(t *testing.T) {
		sr := &capturingSearcher{}
		srv := newSearchServer(t, sr)
		defer srv.Close()

		body, status := getRaw(t, srv.URL+"/v1/games/search?limit=-5")
		if status != 400 {
			t.Fatalf("status = %d, want 400 (body=%s)", status, body)
		}
		if !strings.Contains(string(body), `"invalid_param"`) {
			t.Errorf("body missing invalid_param code: %s", body)
		}
		if sr.seen {
			t.Error("searcher must not be called when limit is negative")
		}
	})
}

// A malformed from/to date is rejected at the API boundary with a 400
// invalid_param instead of reaching the hub and surfacing as a 502
// hub_upstream (review finding: bad search dates were a 502).
func TestGamesSearch_BadDate(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"from-not-a-date", "/v1/games/search?from=notadate"},
		{"from-wrong-length", "/v1/games/search?from=2024-8-1"},
		{"to-not-a-date", "/v1/games/search?to=nope"},
		{"from-impossible-day", "/v1/games/search?from=2024-13-40"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sr := &capturingSearcher{}
			srv := newSearchServer(t, sr)
			defer srv.Close()

			body, status := getRaw(t, srv.URL+tc.url)
			if status != 400 {
				t.Fatalf("status = %d, want 400 (body=%s)", status, body)
			}
			if !strings.Contains(string(body), `"invalid_param"`) {
				t.Errorf("body missing invalid_param code: %s", body)
			}
			if strings.Contains(string(body), `"hub_upstream"`) {
				t.Errorf("bad date must not reach the hub (502): %s", body)
			}
			if sr.seen {
				t.Error("searcher must not be called when a date fails to parse")
			}
		})
	}
}

// A well-formed from/to date passes validation and reaches the searcher.
func TestGamesSearch_ValidDateReachesSearcher(t *testing.T) {
	sr := &capturingSearcher{}
	srv := newSearchServer(t, sr)
	defer srv.Close()

	getJSON(t, srv.URL+"/v1/games/search?from=2024-08-01&to=2024-08-31", 200)
	if !sr.seen {
		t.Fatal("searcher was not invoked for a valid date range")
	}
	if sr.got.From != "2024-08-01" || sr.got.To != "2024-08-31" {
		t.Errorf("from/to = %q/%q; want 2024-08-01/2024-08-31", sr.got.From, sr.got.To)
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
