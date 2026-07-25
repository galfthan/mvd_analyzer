package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// This file adds the automatic API surface over the analyzer DAG (Stage 4 of
// PLAN-improve-analytics.md §7): a manifest of every servable artifact, a
// generic materialise-and-serve endpoint keyed on the artifact name, and the
// DAG as JSON. The manifest and graph are static per binary (a pure function
// of the registered specs), so they cache under an ETag keyed on the schema
// version; the per-demo generic endpoint carries a finer per-artifact ETag.
//
// The generic endpoint is a thin generic accessor, not a re-implementation of
// the curated filters: it resolves the name through the closed
// analyzer.ServableArtifact registry (no user input reaches the filesystem
// beyond a validated name), accepts no query parameters (parameterised reads
// are the view endpoints), and serves the artifact's Result section as-is.

// eagerArtifact describes how the generic endpoint extracts one servable
// eager artifact's section from the base (cached) Result. extract returns the
// raw section; when it can return view.ErrUnavailable (the object-shaped
// sections that need a demo capability), code/msg name the 422. Sections that
// are always computable / list-shaped leave code empty and never error.
type eagerArtifact struct {
	extract func(*result.Result) (any, error)
	code    string
	msg     string
	// echoMs is true when the artifact's stored section carries time values
	// (all int32 ms in the v57 pure-ms model), so the envelope gains a
	// "timeUnit":"ms" self-description echo (matching the curated endpoints).
	// loc-graph echoes: its node weights are aggregate durations, int32 ms
	// since v57. False only for the genuinely time-free sections (metadata,
	// map-entities) and the /demoinfo KTX-native-units island. Audited per
	// section against the backing result.* struct.
	echoMs bool
}

// eagerArtifacts maps each servable eager artifact (by DAG node name) to its
// section accessor. It routes through the view availability accessors where
// one exists (so the 422-vs-200 convention matches the curated endpoints) and
// otherwise returns the raw section at 200. shots/aim serve their stream-
// enriched sections here: since phase 12 the base parse is always-full, so the
// spatial weapon-fire streams (and the splits they feed) are on every Result.
var eagerArtifacts = map[string]eagerArtifact{
	"demoinfo": {extract: func(r *result.Result) (any, error) { return view.DemoInfo(r) },
		code: "demoinfo_unavailable", msg: "this demo has no KTX demoinfo block (likely non-KTX or pre-match abort)"},
	"frag": {extract: func(r *result.Result) (any, error) { return view.Frags(r, view.FragOptions{}) },
		code: "frags_unavailable", msg: "this demo has no frag log", echoMs: true},
	"metadata": {extract: func(r *result.Result) (any, error) { return view.Metadata(r) },
		code: "metadata_unavailable", msg: "this demo has no metadata (no fullserverinfo / no countdown centerprint)"},
	// Dmg "both" keeps the artifact "the stored section as-is": the view's
	// unset default is the raw strip, which would silently delete the
	// stored bounded family from an endpoint contracted to serve it.
	"damage": {extract: func(r *result.Result) (any, error) { return view.Damage(r, view.DamageOptions{Dmg: "both"}) },
		code: "damage_unavailable", msg: "this demo has no damage data (no KTX mvdhidden_dmgdone stream)", echoMs: true},
	"shots": {extract: func(r *result.Result) (any, error) { return view.Shots(r) },
		code: "shots_unavailable", msg: "this demo has no shot data (no weapon fires decoded)", echoMs: true},
	"aim": {extract: func(r *result.Result) (any, error) { return view.Aim(r, view.AimOptions{}) },
		code: "aim_unavailable", msg: "this demo has no aim data (needs shots + position/view streams)", echoMs: true},
	"loc-graph": {extract: func(r *result.Result) (any, error) { return view.LocGraph(r) },
		code: "locgraph_unavailable", msg: "this demo has no loc graph (probably no position track was emitted)", echoMs: true},
	"opening": {extract: func(r *result.Result) (any, error) {
		if r.Opening == nil {
			return nil, view.ErrUnavailable
		}
		return r.Opening, nil
	},
		code: "opening_unavailable", msg: "this demo has no opening (no detected match start)", echoMs: true},

	// Always-computable / list-shaped sections: 200 with the raw section (which
	// may be null/empty), never 422 — the same convention the curated endpoints
	// use for these.
	"match":          {extract: func(r *result.Result) (any, error) { return r.Match, nil }, echoMs: true},
	"messages":       {extract: func(r *result.Result) (any, error) { return r.Messages, nil }, echoMs: true},
	"timeline":       {extract: func(r *result.Result) (any, error) { return r.TimelineAnalysis, nil }, echoMs: true},
	"items":          {extract: func(r *result.Result) (any, error) { return r.Items, nil }, echoMs: true},
	"map-entities":   {extract: func(r *result.Result) (any, error) { return r.MapEntities, nil }},
	"backpacks":      {extract: func(r *result.Result) (any, error) { return r.Backpacks, nil }, echoMs: true},
	"weapon-pickups": {extract: func(r *result.Result) (any, error) { return r.WeaponPickups, nil }, echoMs: true},
	// player-stats is computed for every demo, so it never 422s. It routes
	// through the view so the artifact carries the same KTX overlay the
	// curated endpoint serves — the stored section is derived-only, and
	// serving it raw here would hand two different answers to the same
	// question. echoMs covers window.*Ms and hold.*.ms/longestMs; the
	// shares and efficiency are unitless ratios.
	"player-stats": {extract: func(r *result.Result) (any, error) {
		return view.PlayerStats(r, view.PlayerStatsOptions{})
	},
		code: "playerstats_unavailable", msg: "this demo produced no player streams (degraded parse) — note a missing KTX demoinfo block is NOT a reason for this", echoMs: true},
}

// handleArtifactsManifest: GET /v1/artifacts — the manifest of every DAG node
// (name, requires, provides, mutates, lazy, cost, resultKey, servable,
// description). Static per binary; ETag keyed on the schema version.
func (s *server) handleArtifactsManifest(w http.ResponseWriter, r *http.Request) {
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	s.writeStaticCacheHeaders(w, "artifacts")
	if staticRevalidated(w, r, "artifacts") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": result.CurrentSchemaVersion,
		"artifacts":     analyzer.ArtifactManifest(),
	})
}

// handleGraph: GET /v1/graph — the analyzer DAG as JSON (nodes with cost /
// resultKey / lazy + the artifact edges), exactly analyzer.ExportGraph("json").
// Static per binary; ETag keyed on the schema version.
func (s *server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	s.writeStaticCacheHeaders(w, "graph")
	if staticRevalidated(w, r, "graph") {
		return
	}
	body, err := analyzer.ExportGraph("json")
	if err != nil {
		s.writeInternal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// handleArtifact: GET /v1/demos/{id}/artifacts/{name} — materialise and serve
// any servable artifact by name. The name is resolved through the closed
// registry; unknown / non-servable names 404. No query params are accepted.
func (s *server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	meta, ok := analyzer.ServableArtifact(name)
	if !ok {
		writeError(w, http.StatusNotFound, "artifact_unknown",
			fmt.Sprintf("no servable artifact %q (discover names via GET /v1/artifacts or the listArtifacts MCP tool)", name))
		return
	}
	// Artifacts are parameter-free; parameterised reads are the view endpoints
	// (plan §3.4/§7). Reject any query param (other than the global `label`)
	// rather than silently ignoring it. newQP marks nothing, so Unknown()
	// flags every non-label key as unknown_param.
	if writeUnknownParam(w, newQP(r.URL.Query()).Unknown()) {
		return
	}
	id, err := democache.ParseDemoID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_demo_id", err.Error())
		return
	}

	if meta.Lazy {
		s.serveLazyArtifact(w, r, id, name)
		return
	}

	res, cm, err := s.store.GetResult(r.Context(), id)
	if err != nil {
		s.mapStoreError(w, r, err)
		return
	}
	setArtifactCacheHeaders(w, cm, name)
	if artifactRevalidated(w, r, cm, name) {
		return
	}
	ea, known := eagerArtifacts[name]
	if !known {
		// Manifest says servable but no accessor is wired — a programmer error.
		// Rides the generic-500 path (F19): the detail goes to the log keyed by
		// the request id, not to the client.
		s.writeInternal(w, r, fmt.Errorf("no accessor for servable artifact %q", name))
		return
	}
	section, err := ea.extract(res)
	if err != nil {
		s.writeUnavailable(w, r, err, ea.code, ea.msg)
		return
	}
	body := map[string]any{meta.ResultKey: section}
	if ea.echoMs {
		// Self-description echo (v57 pure-ms): the section carries match-position
		// time values, all int32 ms. The no-time-field artifacts and /demoinfo
		// carry no echo; the lazy /artifacts/los echoes via losBody.
		body["timeUnit"] = string(view.UnitMs)
	}
	writeJSON(w, http.StatusOK, body)
}

// serveLazyArtifact materialises the los artifact through the same store hook
// the curated /los endpoint uses, and serves its body (reusing losBody — no
// forked shapes). los is the only lazy artifact since phase 12 folded
// shot-streams into the always-full base parse.
func (s *server) serveLazyArtifact(w http.ResponseWriter, r *http.Request, id democache.DemoID, name string) {
	switch name {
	case "los":
		res, meta, err := s.store.EnsureLOS(r.Context(), id)
		if err != nil {
			if errors.Is(err, analyzer.ErrNoBSP) {
				writeError(w, http.StatusUnprocessableEntity, "los_unavailable",
					"line of sight needs the map's visibility BSP, which is not available for this demo")
				return
			}
			s.mapStoreError(w, r, err)
			return
		}
		setArtifactCacheHeaders(w, meta, name)
		if artifactRevalidated(w, r, meta, name) {
			return
		}
		writeJSON(w, http.StatusOK, losBody(res))
	default:
		// Unreachable: only los is marked lazy in the manifest.
		s.writeInternal(w, r, fmt.Errorf("unhandled lazy artifact %q", name))
	}
}

// --- per-artifact and static cache headers ---

// artifactETag is the finer per-artifact ETag (plan §7): "<sha>-<name>@v<n>".
func artifactETag(meta democache.CacheMeta, name string) string {
	return fmt.Sprintf(`"%s-%s@v%d"`, meta.SHA256, name, meta.SchemaVersion)
}

func setArtifactCacheHeaders(w http.ResponseWriter, meta democache.CacheMeta, name string) {
	setCacheHeaders(w, meta)
	w.Header().Set("ETag", artifactETag(meta, name)) // override the global "<sha>-v<n>" form
}

func artifactRevalidated(w http.ResponseWriter, r *http.Request, meta democache.CacheMeta, name string) bool {
	etag := artifactETag(meta, name)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// staticETag keys the binary-static endpoints (/v1/artifacts, /v1/graph) on
// the schema version — the only thing that changes their bodies.
func staticETag(kind string) string {
	return fmt.Sprintf(`"%s-v%d"`, kind, result.CurrentSchemaVersion)
}

func (s *server) writeStaticCacheHeaders(w http.ResponseWriter, kind string) {
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("X-Schema-Version", fmt.Sprintf("%d", result.CurrentSchemaVersion))
	w.Header().Set("ETag", staticETag(kind))
}

func staticRevalidated(w http.ResponseWriter, r *http.Request, kind string) bool {
	etag := staticETag(kind)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}
