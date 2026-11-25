package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeSearchAPI stands in for mvd-api's GET /v1/games/search. It captures the
// request URL and Authorization header so tests can assert on how the shim
// builds the proxied request, and can be told to answer with an error
// envelope.
type fakeSearchAPI struct {
	srv      *httptest.Server
	lastURL  *url.URL
	lastAuth string
	code     int
	body     string
}

func newFakeSearchAPI(body string) *fakeSearchAPI {
	f := &fakeSearchAPI{code: 200, body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastURL = r.URL
		f.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.code)
		_, _ = w.Write([]byte(f.body))
	}))
	return f
}

func (f *fakeSearchAPI) Close() { f.srv.Close() }

// newTestSearcher builds the real proxy backend (which is the searcher)
// pointed at the fake mvd-api, with a service key so the auth header is set.
func newTestSearcher(apiURL string) *proxyBackend {
	return newProxyBackend(apiURL, "qwmvd_test-key", 5*time.Second)
}

func TestSearch_HitsGamesSearchEndpoint(t *testing.T) {
	f := newFakeSearchAPI(`{"limit":20,"offset":0,"count":0,"games":[]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	if _, err := s.Search(context.Background(), SearchGamesInput{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if f.lastURL.Path != "/v1/games/search" {
		t.Errorf("path = %q; want /v1/games/search", f.lastURL.Path)
	}
	if f.lastAuth != "Bearer qwmvd_test-key" {
		t.Errorf("Authorization = %q; want Bearer qwmvd_test-key", f.lastAuth)
	}
}

// TestSearch_ParamMapping pins the SearchGamesInput -> query-param mapping:
// players/teams go out as CSV, the scalars one-to-one, roster as 1.
func TestSearch_ParamMapping(t *testing.T) {
	f := newFakeSearchAPI(`{"games":[]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	limit := 50
	in := SearchGamesInput{
		Players:  []string{"bps", "valla"},
		Teams:    []string{"Die", "okkis"},
		Map:      "dm6",
		Mode:     "4on4",
		Matchtag: "qwsl",
		From:     "2025-01-01",
		To:       "2025-12-31",
		Limit:    &limit,
		Offset:   100,
		Roster:   true,
	}
	if _, err := s.Search(context.Background(), in); err != nil {
		t.Fatalf("Search: %v", err)
	}
	q := f.lastURL.Query()
	want := map[string]string{
		"players":  "bps,valla",
		"teams":    "Die,okkis",
		"map":      "dm6",
		"mode":     "4on4",
		"matchtag": "qwsl",
		"from":     "2025-01-01",
		"to":       "2025-12-31",
		"limit":    "50",
		"offset":   "100",
		"roster":   "1",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query %q = %q; want %q (full: %s)", k, got, v, f.lastURL.RawQuery)
		}
	}
}

// TestSearch_OmitsUnsetParams: zero-value fields stay out of the query so the
// REST defaults (limit=20, offset=0, roster=false) apply.
func TestSearch_OmitsUnsetParams(t *testing.T) {
	f := newFakeSearchAPI(`{"games":[]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	if _, err := s.Search(context.Background(), SearchGamesInput{Map: "dm6"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	q := f.lastURL.Query()
	for _, k := range []string{"players", "teams", "mode", "matchtag", "from", "to", "limit", "offset", "roster"} {
		if _, ok := q[k]; ok {
			t.Errorf("unset field leaked into query as %q=%q", k, q.Get(k))
		}
	}
}

// TestSearch_ExplicitZeroLimitForwards: an explicit limit:0 (a non-nil *int)
// must reach the REST boundary as limit=0 so it earns the 400, rather than
// being silently dropped like an omitted limit. This is the whole point of the
// *int field.
func TestSearch_ExplicitZeroLimitForwards(t *testing.T) {
	f := newFakeSearchAPI(`{"games":[]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	zero := 0
	if _, err := s.Search(context.Background(), SearchGamesInput{Limit: &zero}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got, ok := f.lastURL.Query()["limit"]; !ok || got[0] != "0" {
		t.Errorf("explicit limit:0 not forwarded as limit=0 (query: %s)", f.lastURL.RawQuery)
	}
}

// TestSearch_PassesResponseThrough: the mvd-api envelope reaches the caller
// verbatim (numbers decode as JSON float64 through the opaque pass-through).
func TestSearch_PassesResponseThrough(t *testing.T) {
	f := newFakeSearchAPI(`{"limit":20,"offset":0,"count":1,"total":42,"games":[{"id":12345,"map":"dm6"}]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	out, err := s.Search(context.Background(), SearchGamesInput{Mode: "4on4"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	m := out.(map[string]any)
	if m["count"].(float64) != 1 {
		t.Errorf("count = %v; want 1", m["count"])
	}
	if m["total"].(float64) != 42 {
		t.Errorf("total = %v; want 42", m["total"])
	}
	row := m["games"].([]any)[0].(map[string]any)
	if row["id"].(float64) != 12345 || row["map"] != "dm6" {
		t.Errorf("game row not passed through: %+v", row)
	}
}

// TestSearch_HubUpstreamErrorSurfaces: a 502 hub_upstream from mvd-api maps to
// a searcher error whose text carries the API's code + message, so the MCP
// caller sees why search failed (same convention as every proxied tool).
func TestSearch_HubUpstreamErrorSurfaces(t *testing.T) {
	f := newFakeSearchAPI(`{"error":{"code":"hub_upstream","message":"game search is not configured on this server"}}`)
	f.code = http.StatusBadGateway
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	_, err := s.Search(context.Background(), SearchGamesInput{})
	if err == nil {
		t.Fatal("expected an error for 502 hub_upstream")
	}
	if !strings.Contains(err.Error(), "hub_upstream") || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error must surface the API code + message, got %v", err)
	}
}

// TestMCP_SearchGames drives the searchGames tool end-to-end through
// registerTools to the real proxy searcher and a fake mvd-api.
func TestMCP_SearchGames(t *testing.T) {
	f := newFakeSearchAPI(`{"limit":20,"offset":0,"count":1,"games":[{"id":777,"map":"dm6","mode":"1on1"}]}`)
	defer f.Close()
	s := newTestSearcher(f.srv.URL)

	sess := testMCPSessionWith(t, &fakeBackend{}, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "searchGames",
		Arguments: map[string]any{
			"map": "dm6", "mode": "1on1", "players": []any{"bps"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError; content=%+v", res.Content)
	}
	var out map[string]any
	mustDecodeStructured(t, res, &out)
	if out["count"].(float64) != 1 {
		t.Errorf("count = %v; want 1", out["count"])
	}
	// The tool must have forwarded the filters as query params to mvd-api.
	if got := f.lastURL.Query().Get("map"); got != "dm6" {
		t.Errorf("map param not forwarded: %q", got)
	}
}
