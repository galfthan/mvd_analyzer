package main

import (
	"log/slog"
	"net/http"

	"github.com/mvd-analyzer/mvd-api/internal/portal"
)

// server bundles the per-request dependencies.
//
// The lazy line-of-sight pass is serialised per demo SHA inside the cache
// (EnsureLOS), where the SHA is resolved and the tier-3 artifact is
// read/written, so the server holds no per-demo lock of its own.
type server struct {
	store    demoStore
	logger   *slog.Logger
	mapsDir  string       // directory of per-map geometry JSON; "" disables /geometry
	searcher gameSearcher // hub game search backing GET /v1/games/search; nil disables it

	// upload holds the POST /v1/demos limits; uploadLedger is the per-key
	// daily quota (skipped in no-auth mode). Both are inert when uploads are
	// disabled (upload.maxBytes == 0).
	upload       uploadConfig
	uploadLedger *uploadLedger
}

// uploadConfig bundles the POST /v1/demos knobs (serve.go wires them from
// flags). maxBytes == 0 disables the endpoint entirely; a 0 daily dimension
// disables that dimension of the quota.
type uploadConfig struct {
	maxBytes   int64 // on-wire body cap; 0 disables the endpoint
	dailyBytes int64 // per-key daily byte budget; 0 disables that dimension
	dailyCount int64 // per-key daily demo-count budget; 0 disables that dimension
}

// newRouter returns an http.Handler with every endpoint registered.
// Logging + panic recovery wrap the mux. auth may be nil (no-auth /
// localhost mode); when non-nil the auth + rate-limit middleware is inserted
// between accessLog and recover. p may be nil (portal disabled); when non-nil
// its /portal routes are registered on the mux (and are auth-exempt, so they
// are reachable without an API key even in auth mode — see authExempt).
func newRouter(store demoStore, logger *slog.Logger, mapsDir string, upload uploadConfig, auth *authenticator, p *portal.Portal, searcher gameSearcher) http.Handler {
	s := &server{store: store, logger: logger, mapsDir: mapsDir, searcher: searcher, upload: upload, uploadLedger: newUploadLedger()}
	mux := http.NewServeMux()

	// Portal routes are registered ONLY when -portal is set (p != nil). When
	// off, /portal is not a route at all — it 404s — so today's behaviour is
	// unchanged. When on, the routes sit under the phase-14 /portal exemption
	// and do their own Discord-cookie auth.
	if p != nil {
		p.Register(mux)
	}

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("GET /v1/auth/check", s.handleAuthCheck)

	// Machine-readable API description + browsable viewer (embedded,
	// auth-exempt — the spec is the public contract). /docs/{$} covers the
	// trailing-slash form, which the bare "GET /docs" pattern would 404.
	mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPISpec)
	mux.HandleFunc("GET /docs", s.handleDocs)
	mux.HandleFunc("GET /docs/{$}", s.handleDocs)
	mux.HandleFunc("GET /docs/rapidoc-min.js", s.handleDocsAsset)
	mux.HandleFunc("GET /docs/result-schema", s.handleResultSchema)
	mux.HandleFunc("GET /docs/result-schema.md", s.handleResultSchemaMD)
	mux.HandleFunc("GET /docs/marked.min.js", s.handleMarkedAsset)

	// Automatic DAG surface (Stage 4): the artifact manifest, the generic
	// per-artifact endpoint, and the graph as JSON.
	mux.HandleFunc("GET /v1/artifacts", s.handleArtifactsManifest)
	mux.HandleFunc("GET /v1/graph", s.handleGraph)
	mux.HandleFunc("GET /v1/demos/{id}/artifacts/{name}", s.handleArtifact)

	mux.HandleFunc("POST /v1/demos", s.handleUpload)
	mux.HandleFunc("POST /v1/demos/{id}", s.handleLoad)
	mux.HandleFunc("GET /v1/demos/{id}/overview", s.handleOverview)
	mux.HandleFunc("GET /v1/demos/{id}/demoinfo", s.handleDemoInfo)
	mux.HandleFunc("GET /v1/demos/{id}/metadata", s.handleMetadata)
	mux.HandleFunc("GET /v1/demos/{id}/player-stats", s.handlePlayerStats)
	mux.HandleFunc("GET /v1/demos/{id}/frags", s.handleFrags)
	mux.HandleFunc("GET /v1/demos/{id}/damage", s.handleDamage)
	mux.HandleFunc("GET /v1/demos/{id}/shots", s.handleShots)
	mux.HandleFunc("GET /v1/demos/{id}/aim", s.handleAim)
	mux.HandleFunc("GET /v1/demos/{id}/loc-graph", s.handleLocGraph)
	mux.HandleFunc("GET /v1/demos/{id}/chat", s.handleChat)
	mux.HandleFunc("GET /v1/demos/{id}/backpacks", s.handleBackpacks)
	mux.HandleFunc("GET /v1/demos/{id}/items", s.handleItems)
	mux.HandleFunc("GET /v1/demos/{id}/weapon-pickups", s.handleWeaponPickups)
	mux.HandleFunc("GET /v1/demos/{id}/buckets", s.handleBuckets)
	mux.HandleFunc("GET /v1/demos/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /v1/demos/{id}/stream-slice", s.handleStreamSlice)
	mux.HandleFunc("GET /v1/demos/{id}/state-at", s.handleStateAt)
	mux.HandleFunc("GET /v1/demos/{id}/los", s.handleLOS)
	mux.HandleFunc("GET /v1/demos/{id}/streams/projectiles", s.handleProjectiles)
	mux.HandleFunc("GET /v1/demos/{id}/streams/beams", s.handleBeams)
	mux.HandleFunc("GET /v1/demos/{id}/streams/nails", s.handleNails)
	mux.HandleFunc("GET /v1/demos/{id}/streams/point-effects", s.handlePointEffects)
	mux.HandleFunc("GET /v1/demos/{id}/loc-trails", s.handleLocTrails)
	mux.HandleFunc("GET /v1/demos/{id}/loc-table", s.handleLocTable)
	mux.HandleFunc("GET /v1/demos/{id}/region-control", s.handleRegionControl)
	mux.HandleFunc("GET /v1/demos/{id}/top-windows", s.handleTopWindows)
	mux.HandleFunc("GET /v1/demos/{id}/top-kills", s.handleTopKills)
	mux.HandleFunc("GET /v1/demos/{id}/lives", s.handleLives)
	mux.HandleFunc("GET /v1/demos/{id}/highlights", s.handleHighlights)

	// Hub game discovery (no demo needed; hits a live upstream).
	mux.HandleFunc("GET /v1/games/search", s.handleGamesSearch)

	// Per-map static data (no demo needed).
	mux.HandleFunc("GET /v1/maps/{map}/entities", s.handleMapEntitiesByMap)
	mux.HandleFunc("GET /v1/maps/{map}/geometry", s.handleMapGeometry)

	// Middleware order (outer → inner): request-id runs first so every
	// response — including a CORS preflight short-circuit — carries an
	// X-Request-Id; CORS then answers preflight and stamps Allow-Origin on
	// every response (incl. panics); access log records the final status with
	// that id (and seeds the reqInfo auth writes its identity into); auth
	// validates the key + rate-limits; recover catches handler panics closest
	// to the mux so the request is still logged.
	//
	// Chain: requestID → cors → accessLog → auth → recover → mux.
	//
	// auth sits INSIDE cors so an OPTIONS preflight is answered (204, no key)
	// before auth runs, and INSIDE accessLog so 401s/429s are logged. When
	// auth is nil (localhost mode) it is omitted entirely and the chain is
	// byte-identical to before phase 14.
	inner := http.Handler(recoverMiddleware(logger, mux))
	if auth != nil {
		inner = auth.middleware(inner)
	}
	return requestIDMiddleware(
		corsMiddleware(
			accessLogMiddleware(logger, inner)))
}
