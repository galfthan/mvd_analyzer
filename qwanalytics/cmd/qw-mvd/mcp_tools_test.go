package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testMCPSession spins up an MCP server with the given backend on an
// in-memory transport and returns a connected client session ready
// for tools/list / tools/call.
func testMCPSession(t *testing.T, backend MCPBackend) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	srv := mcp.NewServer(&mcp.Implementation{Name: "qw-mvd-test", Version: "test"}, nil)
	registerTools(srv, backend)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- srv.Run(context.Background(), serverTransport)
	}()
	t.Cleanup(func() {
		// Closing the client transport via session.Close() ends the
		// server too. Drain serverErrCh to surface any failure.
		select {
		case err := <-serverErrCh:
			if err != nil {
				t.Logf("mcp server exited with: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Logf("mcp server did not exit within 2s")
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "qw-mvd-test-client", Version: "test"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestMCP_ListTools(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: storeWithStub()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{"loadDemo", "getOverview", "getBuckets", "getEvents",
		"getStreamSlice", "getStateAt", "getLocTrails", "getRegionControl"}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	if len(got) != len(want) {
		t.Errorf("got %d tools; want %d (%v)", len(got), len(want), tool_names(res.Tools))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func tool_names(tools []*mcp.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestMCP_LoadDemo_RequiresIdentifier(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: storeWithStub()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{}, // neither gameId nor sha256
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError=true for missing identifier")
	}
}

func TestMCP_LoadDemo_HappyPath(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: storeWithStub()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{"gameId": 42},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; expected success. content=%+v", res.Content)
	}
	// Structured content should match LoadDemoOutput.
	var out LoadDemoOutput
	mustDecodeStructured(t, res, &out)
	if out.SHA256 == "" {
		t.Errorf("SHA256 empty in load output: %+v", out)
	}
	if !strings.HasPrefix(out.DemoID, "sha:") {
		t.Errorf("DemoID = %q; expected sha: prefix", out.DemoID)
	}
}

func TestMCP_GetOverview(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: storeWithStub()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getOverview",
		Arguments: map[string]any{"demoId": "gameId:42"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; expected success. content=%+v", res.Content)
	}
	var out Overview
	mustDecodeStructured(t, res, &out)
	if out.Map != "dm6" {
		t.Errorf("Map = %q; want dm6", out.Map)
	}
	if len(out.Players) != 3 {
		t.Errorf("len(Players) = %d; want 3", len(out.Players))
	}
}

func TestMCP_GetStateAt(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: storeWithStub()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "getStateAt",
		Arguments: map[string]any{
			"demoId": "gameId:42", "time": 15.0, "players": []any{"bps"}, "fields": []any{"h", "a"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; content=%+v", res.Content)
	}
	if res.StructuredContent == nil {
		t.Errorf("StructuredContent missing")
	}
}

func TestMCP_LoadDemo_NotFound(t *testing.T) {
	sess := testMCPSession(t, &localBackend{store: &fakeStore{}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{"gameId": 9999},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError=true for unknown demo")
	}
}

// mustDecodeStructured re-marshals a tool result's StructuredContent
// into the typed Out for assertion.
func mustDecodeStructured(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("StructuredContent is nil")
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-marshal structured content: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode structured content into %T: %v (data=%s)", out, err, string(data))
	}
}
