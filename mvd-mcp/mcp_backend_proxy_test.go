package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cannedAPI returns a small httptest.Server that mimics mvd-api just
// enough for the proxy backend's HTTP wiring to be exercised. Each
// endpoint returns a tiny canned JSON body.
func cannedAPI(t *testing.T, recordAuth *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/demos/{id}", func(w http.ResponseWriter, r *http.Request) {
		if recordAuth != nil {
			*recordAuth = r.Header.Get("Authorization")
		}
		id := r.PathValue("id")
		if id == "gameId:9999" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"demo_not_found","message":"unknown gameId"}}`))
			return
		}
		fmt.Fprintf(w, `{"demoId":"sha:%s","sha256":"%s","fromCache":false,"schemaVersion":7}`,
			strings.Repeat("a", 64), strings.Repeat("a", 64))
	})
	mux.HandleFunc("GET /v1/demos/{id}/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schemaVersion":7,"map":"dm6","duration":600,"matchStart":0,"matchEnd":600}`))
	})
	mux.HandleFunc("GET /v1/demos/{id}/buckets", func(w http.ResponseWriter, r *http.Request) {
		ms := r.URL.Query().Get("windowMs")
		fmt.Fprintf(w, `{"windowMs":%s,"buckets":[]}`, defaultIfEmpty(ms, "50"))
	})
	mux.HandleFunc("GET /v1/demos/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"events":[]}`))
	})
	mux.HandleFunc("GET /v1/demos/{id}/stream-slice", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"startTime":0,"endTime":0,"players":[]}`))
	})
	mux.HandleFunc("GET /v1/demos/{id}/state-at", func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("time")
		fmt.Fprintf(w, `{"t":%s,"players":{}}`, defaultIfEmpty(t, "0"))
	})
	mux.HandleFunc("GET /v1/demos/{id}/loc-trails", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"players":[]}`))
	})
	mux.HandleFunc("GET /v1/demos/{id}/region-control", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"regions":[],"stats":{}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func defaultIfEmpty(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func TestProxy_LoadDemo(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	out, err := b.LoadDemo(context.Background(), LoadDemoInput{GameID: 42})
	if err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if out.SHA256 == "" || !strings.HasPrefix(out.DemoID, "sha:") {
		t.Errorf("unexpected output %+v", out)
	}
}

func TestProxy_LoadDemo_NotFound(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	_, err := b.LoadDemo(context.Background(), LoadDemoInput{GameID: 9999})
	if err == nil {
		t.Fatalf("expected error")
	}
	pe, ok := err.(*proxyError)
	if !ok {
		t.Fatalf("expected *proxyError, got %T: %v", err, err)
	}
	if pe.Status != 404 || pe.Code != "demo_not_found" {
		t.Errorf("status=%d code=%q; want 404 demo_not_found", pe.Status, pe.Code)
	}
}

func TestProxy_GetOverview(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	out, err := b.GetOverview(context.Background(), GetOverviewInput{DemoID: "gameId:42"})
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	if m["map"] != "dm6" {
		t.Errorf("map=%v; want dm6", m["map"])
	}
}

func TestProxy_GetBuckets_WindowMs(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	out, err := b.GetBuckets(context.Background(), GetBucketsInput{DemoID: "gameId:42", WindowMs: 1000})
	if err != nil {
		t.Fatalf("GetBuckets: %v", err)
	}
	m := out.(map[string]any)
	if m["windowMs"].(float64) != 1000 {
		t.Errorf("windowMs=%v; want 1000", m["windowMs"])
	}
}

// TestProxy_GetBuckets_MCPDefaultIs1s verifies that omitting
// WindowMs in the MCP input forwards windowMs=1000 to the REST API
// (not 50, which is what the API itself defaults to). This is the
// MCP-side ergonomic default to keep buckets responses
// LLM-readable.
func TestProxy_GetBuckets_MCPDefaultIs1s(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	out, err := b.GetBuckets(context.Background(), GetBucketsInput{DemoID: "gameId:42"})
	if err != nil {
		t.Fatalf("GetBuckets: %v", err)
	}
	m := out.(map[string]any)
	if m["windowMs"].(float64) != 1000 {
		t.Errorf("MCP default windowMs=%v; want 1000 (proxy must inject)", m["windowMs"])
	}
}

func TestProxy_GetRegionControl_MCPDefaultIs1s(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Write([]byte(`{"regions":[],"stats":{}}`))
	}))
	defer srv.Close()
	b := newProxyBackend(srv.URL, "", 5*time.Second)

	if _, err := b.GetRegionControl(context.Background(), GetRegionControlInput{DemoID: "gameId:42"}); err != nil {
		t.Fatalf("GetRegionControl: %v", err)
	}
	if !strings.Contains(seenQuery, "windowMs=1000") {
		t.Errorf("expected windowMs=1000 in query; got %q", seenQuery)
	}
}

func TestProxy_GetStateAt(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	out, err := b.GetStateAt(context.Background(), GetStateAtInput{DemoID: "gameId:42", Time: 15})
	if err != nil {
		t.Fatalf("GetStateAt: %v", err)
	}
	m := out.(map[string]any)
	if m["t"].(float64) != 15 {
		t.Errorf("t=%v; want 15", m["t"])
	}
}

func TestProxy_LabelForwardedAsBearer(t *testing.T) {
	var seenAuth string
	srv := cannedAPI(t, &seenAuth)
	b := newProxyBackend(srv.URL, "mcp-test", 5*time.Second)
	if _, err := b.LoadDemo(context.Background(), LoadDemoInput{GameID: 42}); err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if seenAuth != "Bearer mcp-test" {
		t.Errorf("Authorization=%q; want Bearer mcp-test", seenAuth)
	}
}

func TestProxy_EmptyLabel_NoAuthHeader(t *testing.T) {
	var seenAuth string
	srv := cannedAPI(t, &seenAuth)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	if _, err := b.LoadDemo(context.Background(), LoadDemoInput{GameID: 42}); err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if seenAuth != "" {
		t.Errorf("Authorization=%q; want empty", seenAuth)
	}
}

// Smoke test that all the view endpoints decode without error.
func TestProxy_AllView(t *testing.T) {
	srv := cannedAPI(t, nil)
	b := newProxyBackend(srv.URL, "", 5*time.Second)
	ctx := context.Background()

	if _, err := b.GetEvents(ctx, GetEventsInput{DemoID: "gameId:42"}); err != nil {
		t.Errorf("GetEvents: %v", err)
	}
	if _, err := b.GetStreamSlice(ctx, GetStreamSliceInput{DemoID: "gameId:42"}); err != nil {
		t.Errorf("GetStreamSlice: %v", err)
	}
	if _, err := b.GetLocTrails(ctx, GetLocTrailsInput{DemoID: "gameId:42"}); err != nil {
		t.Errorf("GetLocTrails: %v", err)
	}
	if _, err := b.GetRegionControl(ctx, GetRegionControlInput{DemoID: "gameId:42"}); err != nil {
		t.Errorf("GetRegionControl: %v", err)
	}
}
