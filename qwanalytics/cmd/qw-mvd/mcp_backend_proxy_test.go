package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// proxyServer spins up an in-process REST server (the same handlers
// real `qw-mvd serve` uses) plus a proxy backend pointing at it.
// Returns the proxy and a cleanup func.
func proxyServer(t *testing.T) (MCPBackend, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(newRouter(storeWithStub(), logger))
	t.Cleanup(srv.Close)
	return newProxyBackend(srv.URL, "test-label", 5*time.Second), srv.URL
}

func TestProxy_LoadDemo(t *testing.T) {
	backend, _ := proxyServer(t)
	out, err := backend.LoadDemo(context.Background(), LoadDemoInput{GameID: 42})
	if err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if out.SHA256 == "" {
		t.Errorf("SHA256 empty: %+v", out)
	}
	if out.DemoID == "" {
		t.Errorf("DemoID empty: %+v", out)
	}
}

func TestProxy_GetOverview(t *testing.T) {
	backend, _ := proxyServer(t)
	out, err := backend.GetOverview(context.Background(), GetOverviewInput{DemoID: "gameId:42"})
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if out.Map != "dm6" {
		t.Errorf("Map = %q; want dm6", out.Map)
	}
	if len(out.Players) != 3 {
		t.Errorf("len(Players) = %d; want 3", len(out.Players))
	}
}

func TestProxy_GetBuckets(t *testing.T) {
	backend, _ := proxyServer(t)
	out, err := backend.GetBuckets(context.Background(), GetBucketsInput{
		DemoID: "gameId:42", WindowMs: 1000, Fields: []string{"h", "a"},
	})
	if err != nil {
		t.Fatalf("GetBuckets: %v", err)
	}
	if out.WindowMs != 1000 {
		t.Errorf("WindowMs = %d; want 1000", out.WindowMs)
	}
}

func TestProxy_GetStateAt(t *testing.T) {
	backend, _ := proxyServer(t)
	out, err := backend.GetStateAt(context.Background(), GetStateAtInput{
		DemoID: "gameId:42", Time: 15, Players: []string{"bps"}, Fields: []string{"h", "a"},
	})
	if err != nil {
		t.Fatalf("GetStateAt: %v", err)
	}
	if out.Time != 15 {
		t.Errorf("Time = %v; want 15", out.Time)
	}
}

func TestProxy_GetEvents(t *testing.T) {
	backend, _ := proxyServer(t)
	out, err := backend.GetEvents(context.Background(), GetEventsInput{DemoID: "gameId:42"})
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	_ = out // shape verified — handlers_test.go does deeper check
}

func TestProxy_DemoNotFound_PropagatesAsError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(newRouter(&fakeStore{}, logger))
	defer srv.Close()
	backend := newProxyBackend(srv.URL, "", 5*time.Second)

	_, err := backend.GetOverview(context.Background(), GetOverviewInput{DemoID: "gameId:99"})
	if err == nil {
		t.Fatalf("expected error for unknown demo")
	}
	pe, ok := err.(*proxyError)
	if !ok {
		t.Fatalf("expected *proxyError, got %T: %v", err, err)
	}
	if pe.Status != 404 {
		t.Errorf("Status = %d; want 404", pe.Status)
	}
	if pe.Code != "demo_not_found" {
		t.Errorf("Code = %q; want demo_not_found", pe.Code)
	}
}

func TestProxy_LabelForwardedAsBearer(t *testing.T) {
	// Capture Authorization at a custom handler in front of the real router.
	var seenAuth string
	inner := newRouter(storeWithStub(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			seenAuth = a
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()

	backend := newProxyBackend(srv.URL, "mcp-test", 5*time.Second)
	if _, err := backend.LoadDemo(context.Background(), LoadDemoInput{GameID: 42}); err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if seenAuth != "Bearer mcp-test" {
		t.Errorf("Authorization = %q; want %q", seenAuth, "Bearer mcp-test")
	}
}
