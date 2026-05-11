package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires every MCP tool onto the given server. Tools
// that act on a single demo go through `b` (the mvd-api proxy);
// `searchGames` goes through `sr` (a direct hub.quakeworld.nu
// Supabase client — discovery is the hub's responsibility, not
// mvd-api's).
//
// All view-shaped tool outputs are opaque JSON pass-through; only
// LoadDemo is typed, because consumers need to extract `demoId`
// from its result.
func registerTools(s *mcp.Server, b MCPBackend, sr searcher) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "searchGames",
		Description: "Search hub.quakeworld.nu for matches by player names, teams, map, mode, matchtag, or date range. Returns identity + lightweight match summary (gameId, sha256, map, teams w/ scores + rosters, timestamp). Use this first to find demos, then call loadDemo for any you want analyzed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchGamesInput) (*mcp.CallToolResult, any, error) {
		out, err := sr.Search(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "loadDemo",
		Description: "Resolve, fetch, parse, and cache a demo on the mvd-api. Returns the demoId (sha:HEX) to use with subsequent tool calls. Idempotent — cheap on warm cache.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadDemoInput) (*mcp.CallToolResult, *LoadDemoOutput, error) {
		out, err := b.LoadDemo(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getOverview",
		Description: "Return a curated summary of the demo (map, teams, top streaks, top powerups). Use this first to decide which detailed view to query next. Response shape: see mvd-api /v1/demos/{id}/overview.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetOverviewInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetOverview(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getBuckets",
		Description: "Bucketed per-player time series over the match (health, armor, weapons, powerups, ammo, position, loc). Choose a windowMs that matches your visualization or query resolution. Response shape: see mvd-api /v1/demos/{id}/buckets.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetBucketsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetBuckets(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getEvents",
		Description: "Time-ordered discrete events (frags, powerups, streaks, spawns, deaths, chat). Default types exclude high-frequency change events (health/armor/loc). Response shape: see mvd-api /v1/demos/{id}/events.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetEventsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetEvents(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getStreamSlice",
		Description: "Raw native-rate change entries for each requested field inside a time window. Right shape for inspecting a short event in detail (carry-forward at window start; intervals clamped to window).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStreamSliceInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStreamSlice(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getStateAt",
		Description: "Point-in-time state per player at a given match-relative time. Carry-forward for change streams; nearest-sample for position; interval membership for held items/powerups.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStateAtInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStateAt(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getLocTrails",
		Description: "Per-player sequence of loc residences with dwell durations. Use minDwellMs to filter nearest-loc flicker.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLocTrailsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLocTrails(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getRegionControl",
		Description: "Per-region territorial control over the match. Returns per-bucket state strings + match-aggregate percentages per region.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetRegionControlInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetRegionControl(ctx, in)
		return toolResult(out, err)
	})
}

// toolResult adapts (Out, error) to the SDK's tool-handler return
// triplet. On error, returns an isError tool result with the error
// text in TextContent. On success, returns the typed output — the SDK
// serialises it into structuredContent.
func toolResult[Out any](out Out, err error) (*mcp.CallToolResult, Out, error) {
	if err != nil {
		var zero Out
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%v", err)}},
		}, zero, nil
	}
	return nil, out, nil
}
