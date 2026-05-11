package main

import (
	"context"

	"github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwanalytics/view"
)

// MCPBackend is the contract every MCP tool handler depends on. The
// local backend implements it by calling democache + view directly;
// the proxy backend (Step 5) implements it by talking HTTP to a remote
// `qw-mvd serve`.
type MCPBackend interface {
	LoadDemo(ctx context.Context, in LoadDemoInput) (*LoadDemoOutput, error)
	GetOverview(ctx context.Context, in GetOverviewInput) (*Overview, error)
	GetBuckets(ctx context.Context, in GetBucketsInput) (*view.BucketsView, error)
	GetEvents(ctx context.Context, in GetEventsInput) (*view.EventsView, error)
	GetStreamSlice(ctx context.Context, in GetStreamSliceInput) (*view.StreamSliceView, error)
	GetStateAt(ctx context.Context, in GetStateAtInput) (*view.StateAtView, error)
	GetLocTrails(ctx context.Context, in GetLocTrailsInput) (*view.LocTrailsView, error)
	GetRegionControl(ctx context.Context, in GetRegionControlInput) (*result.RegionControlResult, error)
}

// --- Tool input/output structs ---
//
// Each Input embeds the corresponding view options where applicable,
// and adds DemoID so the MCP handler can re-resolve the demo before
// calling the view. Outputs are passed through verbatim — the view
// types are already JSON-shaped.

// LoadDemoInput identifies a demo by exactly one of GameID or SHA256.
type LoadDemoInput struct {
	GameID int    `json:"gameId,omitempty" jsonschema:"hub.quakeworld.nu game id (exactly one of gameId or sha256 required)"`
	SHA256 string `json:"sha256,omitempty" jsonschema:"SHA-256 of a previously-resolved demo (mostly for bookmarking warm cache entries)"`
}

// LoadDemoOutput mirrors POST /v1/demos/{id}.
type LoadDemoOutput struct {
	DemoID        string `json:"demoId"`
	SHA256        string `json:"sha256"`
	FromCache     bool   `json:"fromCache"`
	SchemaVersion int    `json:"schemaVersion"`
}

// GetOverviewInput is just a demoId reference (gameId:N or sha:HEX).
type GetOverviewInput struct {
	DemoID string `json:"demoId" jsonschema:"the demo id from loadDemo: 'gameId:NNNN' or 'sha:HEX'"`
}

// GetBucketsInput re-exposes view.BucketsOptions for the tool surface.
type GetBucketsInput struct {
	DemoID      string            `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	WindowMs    int               `json:"windowMs,omitempty" jsonschema:"bucket size in ms; default 50"`
	StartTime   float64           `json:"startTime,omitempty" jsonschema:"window start in match-relative seconds"`
	EndTime     float64           `json:"endTime,omitempty" jsonschema:"window end in match-relative seconds"`
	Players     []string          `json:"players,omitempty"`
	Fields      []string          `json:"fields,omitempty" jsonschema:"field codes (h, a, rl, lg, ...). Empty = all standard fields"`
	Reducers    map[string]string `json:"reducers,omitempty" jsonschema:"per-field reducer override, e.g. {\"h\":\"min\"}"`
	IncludeTeam bool              `json:"includeTeam,omitempty"`
}

// GetEventsInput re-exposes view.EventsFilter.
type GetEventsInput struct {
	DemoID    string   `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	StartTime float64  `json:"startTime,omitempty"`
	EndTime   float64  `json:"endTime,omitempty"`
	Players   []string `json:"players,omitempty"`
	Types     []string `json:"types,omitempty" jsonschema:"event types: frag, powerup, streak, spawn, death, weapon, item, chat, loc, health, armor"`
}

// GetStreamSliceInput re-exposes view.StreamSliceOptions.
type GetStreamSliceInput struct {
	DemoID    string   `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	StartTime float64  `json:"startTime,omitempty"`
	EndTime   float64  `json:"endTime,omitempty"`
	Players   []string `json:"players,omitempty"`
	Fields    []string `json:"fields,omitempty"`
}

// GetStateAtInput re-exposes view.StateAtOptions.
type GetStateAtInput struct {
	DemoID  string   `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	Time    float64  `json:"time" jsonschema:"required; match-relative seconds"`
	Players []string `json:"players,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

// GetLocTrailsInput re-exposes view.LocTrailsOptions.
type GetLocTrailsInput struct {
	DemoID     string   `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	Players    []string `json:"players,omitempty"`
	MinDwellMs int      `json:"minDwellMs,omitempty"`
	StartTime  float64  `json:"startTime,omitempty"`
	EndTime    float64  `json:"endTime,omitempty"`
}

// GetRegionControlInput re-exposes view.RegionControlOptions.
type GetRegionControlInput struct {
	DemoID   string `json:"demoId" jsonschema:"the demo id (gameId:N or sha:HEX)"`
	WindowMs int    `json:"windowMs,omitempty" jsonschema:"bucket size for per-region state strings; default 50"`
}
