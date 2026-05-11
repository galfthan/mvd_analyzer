package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires every qw-mvd tool onto the given MCP server.
// Both the local mode (Step 4) and the proxy mode (Step 5) call this
// with their respective MCPBackend implementations.
func registerTools(s *mcp.Server, b MCPBackend) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "loadDemo",
		Description: "Resolve, fetch, parse, and cache a demo. Returns the demoId (sha:HEX) to use with subsequent tool calls. Idempotent — cheap on warm cache.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadDemoInput) (*mcp.CallToolResult, *LoadDemoOutput, error) {
		out, err := b.LoadDemo(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getOverview",
		Description: "Return a curated summary of the demo (map, teams, top streaks, top powerups). Use this first to decide which detailed view to query next.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetOverviewInput) (*mcp.CallToolResult, *Overview, error) {
		out, err := b.GetOverview(ctx, in)
		return toolResult(out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getBuckets",
		Description: "Bucketed per-player time series over the match (health, armor, weapons, powerups, ammo, position, loc). Choose a windowMs that matches your visualization or query resolution.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetBucketsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetBuckets(ctx, in)
		return toolResult[any](out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getEvents",
		Description: "Time-ordered discrete events (frags, powerups, streaks, spawns, deaths, chat). Default types exclude high-frequency change events (health/armor/loc).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetEventsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetEvents(ctx, in)
		return toolResult[any](out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getStreamSlice",
		Description: "Raw native-rate change entries for each requested field inside a time window. Right shape for inspecting a short event in detail (carry-forward at window start; intervals clamped to window).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStreamSliceInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStreamSlice(ctx, in)
		return toolResult[any](out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getStateAt",
		Description: "Point-in-time state per player at a given match-relative time. Carry-forward for change streams; nearest-sample for position; interval membership for held items/powerups.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStateAtInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStateAt(ctx, in)
		return toolResult[any](out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getLocTrails",
		Description: "Per-player sequence of loc residences with dwell durations. Use minDwellMs to filter nearest-loc flicker.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLocTrailsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLocTrails(ctx, in)
		return toolResult[any](out, err)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "getRegionControl",
		Description: "Per-region territorial control over the match. Returns per-bucket state strings + match-aggregate percentages per region.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetRegionControlInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetRegionControl(ctx, in)
		return toolResult[any](out, err)
	})
}

// toolResult adapts (Out, error) to the SDK's tool-handler return
// triplet. On error, returns an isError tool result with the error
// text in a TextContent (per MCP semantics for tool failures the
// model can recover from). On success, returns the typed output —
// the SDK serialises it into structuredContent.
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
