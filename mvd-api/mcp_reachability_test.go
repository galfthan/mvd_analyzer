package main

// MCP reachability: every demo-scoped REST GET *view* endpoint must be
// reachable from the MCP surface, so a future endpoint cannot ship
// MCP-invisible the way the retired /v1/demos/{id}/airgibs once did (its
// data lived only on a REST endpoint no curated tool proxied and on no
// event type getEvents emitted; retired at schema v76 — the list's only
// home is /highlights, proxied by getHighlights). The spec's demo-scoped GET paths are the source of truth; each
// must map to one of three reachability kinds:
//
//   - tool:      a curated MCP tool proxies the endpoint. The tool list is
//                HAND-MAINTAINED — mvd-mcp is deliberately decoupled from this
//                module (no import), so there is nothing to pin the names
//                against; keep them in step with mvd-mcp/mcp_tools.go by hand.
//   - eventType: the endpoint's data rides the /events stream — assert the
//                type is in view.KnownEventTypes.
//   - artifact:  reachable via getArtifact — assert analyzer.ServableArtifact
//                confirms the name is servable.
//
// The test fails both ways: a spec demo path with no coverage entry, and a
// coverage entry whose path no longer exists (a stale entry).

import (
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// mcpCoverage records HOW a demo-scoped REST GET view endpoint is reachable
// from the MCP surface. Exactly one field is set.
type mcpCoverage struct {
	tool      string // curated MCP tool name (see mvd-mcp/mcp_tools.go; hand-maintained)
	eventType string // the endpoint's data rides getEvents under this type
	artifact  string // reachable via getArtifact under this artifact name
}

// demoViewCoverage maps each demo-scoped REST GET view endpoint (spec path
// pattern) to its MCP reachability. Add a row when a new demo view endpoint
// lands, or the test fails with an actionable message.
var demoViewCoverage = map[string]mcpCoverage{
	"/v1/demos/{id}/overview":       {tool: "getOverview"},
	"/v1/demos/{id}/demoinfo":       {tool: "getDemoInfo"},
	"/v1/demos/{id}/player-stats":   {tool: "getPlayerStats"},
	"/v1/demos/{id}/metadata":       {tool: "getMetadata"},
	"/v1/demos/{id}/frags":          {tool: "getFrags"},
	"/v1/demos/{id}/damage":         {tool: "getDamage"},
	"/v1/demos/{id}/aim":            {tool: "getAim"},
	"/v1/demos/{id}/loc-graph":      {tool: "getLocGraph"},
	"/v1/demos/{id}/chat":           {tool: "getChat"},
	"/v1/demos/{id}/backpacks":      {tool: "getBackpacks"},
	"/v1/demos/{id}/items":          {tool: "getItems"},
	"/v1/demos/{id}/weapon-pickups": {tool: "getWeaponPickups"},
	"/v1/demos/{id}/buckets":        {tool: "getBuckets"},
	"/v1/demos/{id}/events":         {tool: "getEvents"},
	"/v1/demos/{id}/stream-slice":   {tool: "getStreamSlice"},
	"/v1/demos/{id}/state-at":       {tool: "getStateAt"},
	"/v1/demos/{id}/loc-trails":     {tool: "getLocTrails"},
	"/v1/demos/{id}/loc-table":      {tool: "getLocTable"},
	"/v1/demos/{id}/region-control": {tool: "getRegionControl"},
	"/v1/demos/{id}/top-windows":    {tool: "getTopWindows"},
	"/v1/demos/{id}/top-kills":      {tool: "getTopKills"},
	"/v1/demos/{id}/lives":          {tool: "getLives"},
	"/v1/demos/{id}/highlights":     {tool: "getHighlights"},

	// The three projectile/beam/nail dense streams share the getStreamSlice
	// tool (fields=rk/nl/…), not a per-stream tool.
	"/v1/demos/{id}/streams/projectiles": {tool: "getStreamSlice"},
	"/v1/demos/{id}/streams/beams":       {tool: "getStreamSlice"},
	"/v1/demos/{id}/streams/nails":       {tool: "getStreamSlice"},

	// Point effects are not per-player, so getStreamSlice cannot carry
	// them — they get their own curated tool.
	"/v1/demos/{id}/streams/point-effects": {tool: "getPointEffects"},

	// Dense per-sample surfaces with no curated tool — reachable via
	// getArtifact.
	"/v1/demos/{id}/shots": {artifact: "shots"},
	"/v1/demos/{id}/los":   {artifact: "los"},
}

// demoViewExempt are demo-scoped GET paths that are NOT view endpoints
// needing coverage: the generic artifact accessor is itself the getArtifact
// reachability mechanism, not a thing to be reached.
var demoViewExempt = map[string]bool{
	"/v1/demos/{id}/artifacts/{name}": true,
}

func eventTypeKnown(t string) bool {
	for _, k := range view.KnownEventTypes {
		if k == t {
			return true
		}
	}
	return false
}

func TestMCPReachability(t *testing.T) {
	// Collect demo-scoped GET view paths from the spec.
	specDemoPaths := map[string]bool{}
	for op := range specPaths(t) {
		if !strings.HasPrefix(op, "GET ") {
			continue
		}
		path := strings.TrimPrefix(op, "GET ")
		if !strings.HasPrefix(path, "/v1/demos/{id}/") {
			continue
		}
		if demoViewExempt[path] {
			continue
		}
		specDemoPaths[path] = true
	}
	if len(specDemoPaths) < 20 {
		t.Fatalf("found only %d demo-scoped GET view paths in the spec — has the path enumeration regressed?", len(specDemoPaths))
	}

	// (1) Every spec demo view path must have a real coverage entry.
	for path := range specDemoPaths {
		cov, ok := demoViewCoverage[path]
		if !ok {
			t.Errorf("REST endpoint %q has no MCP coverage — add a curated tool, event type, or servable artifact, then record it in demoViewCoverage", path)
			continue
		}
		switch {
		case cov.eventType != "":
			if !eventTypeKnown(cov.eventType) {
				t.Errorf("%q claims event type %q but it is not in view.KnownEventTypes — add it to view.Events or fix the entry", path, cov.eventType)
			}
		case cov.artifact != "":
			if _, ok := analyzer.ServableArtifact(cov.artifact); !ok {
				t.Errorf("%q claims servable artifact %q but analyzer.ServableArtifact reports it is not servable", path, cov.artifact)
			}
		case cov.tool != "":
			// Hand-maintained against mvd-mcp/mcp_tools.go; mvd-mcp is
			// decoupled from this module, so there is no import to verify the
			// name against.
		default:
			t.Errorf("%q has an empty coverage entry — set tool, eventType, or artifact", path)
		}
	}

	// (2) Every coverage entry must reference a live spec path (no stale
	// entries left behind when an endpoint is renamed or removed).
	for path := range demoViewCoverage {
		if !specDemoPaths[path] {
			t.Errorf("coverage entry %q no longer matches a demo-scoped GET view path in openapi.yaml — remove the stale entry", path)
		}
	}
}
