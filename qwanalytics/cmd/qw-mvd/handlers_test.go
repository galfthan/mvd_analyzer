package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mvd-analyzer/qwanalytics/internal/democache"
	"github.com/mvd-analyzer/qwanalytics/result"
)

// fakeStore implements demoStore for handler tests without touching
// disk or the hub.
type fakeStore struct {
	byID map[string]*result.Result
	err  error
}

func (f *fakeStore) GetResult(_ context.Context, id democache.DemoID) (*result.Result, democache.CacheMeta, error) {
	if f.err != nil {
		return nil, democache.CacheMeta{}, f.err
	}
	r, ok := f.byID[id.String()]
	if !ok {
		return nil, democache.CacheMeta{}, democache.ErrDemoNotFound
	}
	return r, democache.CacheMeta{
		SHA256:        strings.Repeat("a", 64),
		FromCache:     true,
		SchemaVersion: result.CurrentSchemaVersion,
	}, nil
}

// stubResult builds a minimal but well-formed *Result so handlers
// have something to query.
func stubResult() *result.Result {
	return &result.Result{
		SchemaVersion: result.CurrentSchemaVersion,
		FilePath:      "stub.mvd.gz",
		Match: &result.MatchResult{
			Map:      "dm6",
			GameDir:  "qw",
			Duration: 600.0,
			Players: []result.PlayerStat{
				{Name: "bps", Team: "blue", Frags: 35},
				{Name: "milton", Team: "blue", Frags: 28},
				{Name: "valla", Team: "red", Frags: 30},
			},
			Teams: []result.TeamStat{
				{Name: "blue", Frags: 63},
				{Name: "red", Frags: 30},
			},
		},
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 600},
			Players: []result.PlayerStream{
				{Name: "bps", Team: "blue",
					Health: []result.ChangeI16{{T: 0, V: 100}, {T: 10, V: 50}, {T: 20, V: 100}},
					Armor:  []result.ChangeI16{{T: 0, V: 0}, {T: 5, V: 100}},
					RL:     []result.Interval{{Start: 5, End: 60}},
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "ra", "ya", "rl"},
		},
		Metadata: &result.MetadataResult{
			MatchSettings: &result.MatchSettings{Mode: "Team", Matchtag: "testcup"},
		},
	}
}

func newTestServer(t *testing.T, store demoStore) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httptest.NewServer(newRouter(store, logger))
}

// --- /healthz, /v1/version ---

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, &fakeStore{})
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/healthz", 200)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["schemaVersion"].(float64) != float64(result.CurrentSchemaVersion) {
		t.Errorf("schemaVersion mismatch")
	}
}

func TestVersion(t *testing.T) {
	srv := newTestServer(t, &fakeStore{})
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/version", 200)
	for _, k := range []string{"hash", "tag", "buildDate"} {
		if _, ok := resp[k]; !ok {
			t.Errorf("missing key %q in version response", k)
		}
	}
}

// --- error mapping ---

func TestInvalidDemoID(t *testing.T) {
	srv := newTestServer(t, &fakeStore{})
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/banana/overview")
	if status != 400 {
		t.Errorf("status = %d; want 400", status)
	}
	var env errorEnvelope
	_ = json.Unmarshal(resp, &env)
	if env.Error.Code != "invalid_demo_id" {
		t.Errorf("code = %q; want invalid_demo_id (body=%s)", env.Error.Code, string(resp))
	}
}

func TestDemoNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeStore{byID: map[string]*result.Result{}})
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/overview")
	if status != 404 {
		t.Errorf("status = %d; want 404 (body=%s)", status, string(resp))
	}
	var env errorEnvelope
	_ = json.Unmarshal(resp, &env)
	if env.Error.Code != "demo_not_found" {
		t.Errorf("code = %q; want demo_not_found", env.Error.Code)
	}
}

func TestHubUpstreamError(t *testing.T) {
	srv := newTestServer(t, &fakeStore{err: democache.ErrHubUpstream})
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/overview")
	if status != 502 {
		t.Errorf("status = %d; want 502 (body=%s)", status, string(resp))
	}
	if !errors.Is(democache.ErrHubUpstream, democache.ErrHubUpstream) {
		t.Fatal("sanity: ErrHubUpstream lost identity")
	}
}

// --- happy paths ---

func storeWithStub() *fakeStore {
	return &fakeStore{byID: map[string]*result.Result{"gameId:42": stubResult()}}
}

func TestLoad(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/demos/gameId:42", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cache") == "" {
		t.Errorf("X-Cache header missing")
	}
	if resp.Header.Get("X-Schema-Version") != "7" {
		t.Errorf("X-Schema-Version = %q", resp.Header.Get("X-Schema-Version"))
	}
	if resp.Header.Get("ETag") == "" {
		t.Errorf("ETag missing")
	}
	body, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["demoId"] == nil {
		t.Errorf("demoId missing in load response: %s", string(body))
	}
	if m["schemaVersion"].(float64) != float64(result.CurrentSchemaVersion) {
		t.Errorf("schemaVersion mismatch")
	}
}

func TestOverview(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/demos/gameId:42/overview", 200)
	if resp["map"] != "dm6" {
		t.Errorf("map = %v", resp["map"])
	}
	if resp["matchEnd"].(float64) != 600.0 {
		t.Errorf("matchEnd = %v", resp["matchEnd"])
	}
	if resp["mode"] != "Team" {
		t.Errorf("mode = %v", resp["mode"])
	}
	players, _ := resp["players"].([]any)
	if len(players) != 3 {
		t.Errorf("len(players) = %d; want 3", len(players))
	}
	teams, _ := resp["teams"].([]any)
	if len(teams) != 2 {
		t.Errorf("len(teams) = %d; want 2", len(teams))
	}
}

func TestBuckets_HappyPath(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/demos/gameId:42/buckets?windowMs=1000&fields=h,a", 200)
	if int(resp["windowMs"].(float64)) != 1000 {
		t.Errorf("windowMs = %v; want 1000", resp["windowMs"])
	}
	if _, ok := resp["buckets"].([]any); !ok {
		t.Errorf("buckets not an array: %T", resp["buckets"])
	}
}

func TestBuckets_BadParam(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/buckets?windowMs=banana")
	if status != 400 {
		t.Errorf("status = %d; want 400 (body=%s)", status, string(resp))
	}
}

func TestEvents_Default(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/demos/gameId:42/events", 200)
	if _, ok := resp["events"].([]any); !ok && resp["events"] != nil {
		// view.Events returns {events: []} when no events; nil/absent is also acceptable
		t.Errorf("events shape unexpected: %T", resp["events"])
	}
}

func TestStreamSlice(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/demos/gameId:42/stream-slice?from=0&to=30&fields=h,a", 200)
	if _, ok := resp["players"].([]any); !ok && resp["players"] != nil {
		t.Errorf("players shape unexpected: %T", resp["players"])
	}
}

func TestStateAt_MissingTime(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/state-at?players=bps")
	if status != 400 {
		t.Errorf("status = %d; want 400 (body=%s)", status, string(resp))
	}
}

func TestStateAt_HappyPath(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp := getJSON(t, srv.URL+"/v1/demos/gameId:42/state-at?time=15&fields=h,a&players=bps", 200)
	if resp["t"].(float64) != 15 {
		t.Errorf("t = %v; want 15", resp["t"])
	}
	players, _ := resp["players"].(map[string]any)
	if _, ok := players["bps"]; !ok {
		t.Errorf("bps state missing")
	}
}

func TestLocTrails(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	resp, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/loc-trails?players=bps")
	if status != 200 {
		t.Errorf("status = %d; want 200 (body=%s)", status, string(resp))
	}
}

func TestRegionControl_Unavailable(t *testing.T) {
	// Stub demo has TimelineAnalysis but no RegionControl.
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	_, status := getRaw(t, srv.URL+"/v1/demos/gameId:42/region-control")
	if status != 422 {
		t.Errorf("status = %d; want 422", status)
	}
}

// --- HTTP cache semantics ---

func TestETag_304(t *testing.T) {
	srv := newTestServer(t, storeWithStub())
	defer srv.Close()
	// First request to learn the ETag.
	resp1, err := http.Get(srv.URL + "/v1/demos/gameId:42/overview")
	if err != nil {
		t.Fatal(err)
	}
	etag := resp1.Header.Get("ETag")
	resp1.Body.Close()
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}

	// Second with If-None-Match.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/demos/gameId:42/overview", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 304 {
		t.Errorf("expected 304, got %d", resp2.StatusCode)
	}
}

// --- helpers ---

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d; want %d (body=%s)", url, resp.StatusCode, wantStatus, string(body))
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("GET %s: decode: %v (body=%s)", url, err, string(body))
	}
	return m
}

func getRaw(t *testing.T, url string) ([]byte, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode
}
