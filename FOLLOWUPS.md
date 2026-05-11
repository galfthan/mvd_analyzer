# Follow-ups

Open items across the mvd-analytics pipeline and the mvd-api / mvd-mcp
transport surface. Not an exhaustive backlog — just things a future
reader (or Claude) needs to know were deliberately left undone and why.

The branch state this captures:

- **7f26610** — Schema v7 / canonical `Streams` (Phase 1).
- **4519dd8** — `qw-mvd` REST + stdio MCP server (Phase 2).
- *(unstaged, this session)* — split into `mvd-api` / `mvd-mcp` and rename
  workspace modules (`qwdemo` → `mvd-reader`, `qwanalytics` →
  `mvd-analytics`, `qw-web` → `mvd-web`).

## Phase 1.5 — frontend panel migration

[Plan v3 §11.0](PLAN-event-streams-and-views-v3.md). Replace
`timelineState.highResBuckets` reads with direct
`getBuckets({windowMs})` calls per panel. Highest-leverage targets:

- Timeline-graph panels (currently walk ~24 K samples to render
  ~800 px) — ask for the resolution they actually render.
- Region-control heatmap when zoomed out.

Map-tab playback keeps the current data flow (it really wants every
50 ms position).

When this lands, the WASM bridge's `getDefaultBuckets` shim and the
legacy-shape types in `view/legacy.go` become dead code.

**Acceptance:** timeline-graph and region-control panels stop reading
`timelineState.highResBuckets`. Visual behaviour unchanged or better.
Initial paint becomes scoreboard + chat + frags only (sub-second on
cached WASM); bucket-dependent panels load lazily on tab open. RAM
on a 20-min match measurably drops in DevTools Memory.

## Phase 2 carryovers — mvd-api / mvd-mcp v1

Phase 2 landed in 4519dd8 (initially as a single `qw-mvd` binary
with `serve`/`mcp` subcommands) and then split (this session) into
two single-purpose binaries:

- [`mvd-api`](mvd-api/) — HTTP REST host on top of `mvd-analytics/view`,
  with a two-tier on-disk cache (raw MVD + parsed Result).
- [`mvd-mcp`](mvd-mcp/) — stdio MCP shim that forwards each tool call
  over HTTP to a running `mvd-api`. Has no `mvd-analytics` import; the
  binary stays small and the wire contract is owned by mvd-api.

For local-only MCP, run `mvd-api` on `localhost` and point
`mvd-mcp -api http://localhost:8080` at it.

`make serve` (the WASM web app) is unchanged.

### Operational gaps

- **No cache eviction.** Tier 1 (`mvd/`) and tier 2 (`results/v7/`)
  grow without bound. Per-demo footprint is ~3–7 MB raw +
  ~3–10 MB gob; a year of community traffic is fine but not
  forever. Ship a `mvd-api cache prune --older-than 30d` (or
  `--max-size 50GB`) subcommand before it becomes a real ops
  problem.
- **No cache stats / introspection.** Operators can't ask "how
  many demos cached?" or "what's the LRU hit rate?" without
  shelling out. A `mvd-api cache stats` subcommand or `/v1/debug/
  cache` endpoint would help.
- **No release pipeline.** `make build-all-platforms` produces
  binaries locally; wire a GitHub Actions workflow that builds the
  cross-compile targets on tag and attaches them to a Release.
- **Cache disk write failures are swallowed.** `democache.cache.go`
  uses `_ = writeFileAtomic(...)` for the gameId index and result
  gob; a full disk silently degrades to "parse every time." Add a
  structured warning via `log/slog`.
- **/healthz is trivially OK.** Returns `{ok:true}` unconditionally.
  A production-grade health check should at least verify the cache
  directory is writable.
- **No TLS / no reverse proxy guidance.** `mvd-api` is HTTP-
  only. Documented deployment story: stick it behind nginx / Caddy.
  Add an example config snippet to the README if more than one
  operator deploys.
- **No CORS headers.** Browser-direct REST consumption from a
  different origin would fail preflight. Not a current concern (the
  WASM web app is bundled with its own analyzer); add `Access-
  Control-Allow-Origin: *` for view-shaped endpoints if a JS client
  ever shows up.
- **No streaming responses.** A 4on4 buckets call at 50 ms windowMs
  can exceed 10 MB encoded as a single JSON document. Move to
  newline-delimited JSON or chunked transfer if a real client
  chokes.

### Surface gaps

- **No remote MCP transport.** Streamable HTTP MCP isn't exposed.
  Once a specific MCP client demands it, mvd-api could grow a `/mcp`
  route using the SDK's HTTP handler — open access remains
  acceptable for public read-only data, but the MCP spec is moving
  toward an OAuth protected-resource convention, so plan for
  `.well-known/oauth-protected-resource` if real auth is needed.
- **No pre-rendered view tier.** Every REST hit recomputes the view
  from the cached `*Result`. If a hot `(demoId, view, opts)` tuple
  shows up at meaningful rate in access logs, add tier 3 keyed by
  `(demoId, schemaVersion, view, optsHash)`.
- **No rate limiting.** Labels (`Authorization: Bearer <label>`)
  are recorded for analytics but not acted on. Add per-label /
  per-IP token bucket if abuse appears.
- **`loadDemo` is the only way to write to the cache.** No
  multi-demo prewarm endpoint. If you operate a public hub mirror,
  add `POST /v1/cache/warm` that takes a list of gameIds.

### Testing gaps

- **Real-demo gob round-trip not exercised.** `democache`
  unit tests use a stub parser returning a synthetic
  `result.Result`. The real `*Result` graph is much richer
  (`TimelineAnalysisResult`, `LocGraph`, `WeaponPickups`, etc.);
  gob serialization survives by Go-type-system construction, but
  a single integration test that parses one corpus demo end-to-end
  + round-trips through `encodeResult`/`decodeResult` would catch
  a silently changed field.
- **`BuildOverview` has no direct unit test.** Covered indirectly
  by `handleOverview` tests. A dedicated test fixture with edge
  cases (empty teams, missing TimelineAnalysis, no Metadata)
  belongs in `mvd-api/overview_test.go`.
- **MCP proxy equivalence not pinned.** `mcp_backend_proxy_test.go`
  exercises each tool through the proxy against an in-process
  serve, but doesn't assert the proxy returns the same shape as
  the local backend on the same demo. A side-by-side equivalence
  test would catch a regression where, say, query-param encoding
  loses a default.
- **No test that `mvd-api version` / `mvd-mcp version` / unknown-subcommand dispatch
  exits with the right code.** `main.go`'s argv handling is
  untested.

### Distribution gaps

- **Windows code-signing.** Unsigned `.exe` triggers SmartScreen.
  Either accept the warning (documented in `CLAUDE_DESKTOP.md`)
  or obtain an Authenticode cert.
- **macOS notarization.** Same story with Gatekeeper. The
  `xattr -d com.apple.quarantine` workaround is documented; real
  fix is an Apple Developer account ($99/yr).
- **`CLAUDE_DESKTOP.md` doesn't cover Claude Code.** The same
  `.mcp.json` shape works for Claude Code (in repo root) but the
  config path and discovery rules differ from Claude Desktop. Add
  a section to `mvd-mcp/CLAUDE_DESKTOP.md`.

- **Local MCP "convenience mode" removed.** The previous bundled
  binary had a `qw-mvd mcp` (no `-api`) mode that ran a local cache
  in-process. Post-split, local MCP requires running `mvd-api` on
  localhost (just two binaries, ~zero startup cost). If install
  friction matters more than binary size, an `mvd-mcp -embedded`
  mode that spawns `mvd-api` as a subprocess is conceivable.

### Toolchain note (informational)

Pulling in `github.com/modelcontextprotocol/go-sdk v1.6.0` required
Go 1.25. `go.work` and `mvd-analytics/go.mod` use the `toolchain
go1.25.0` directive so older Go installations auto-fetch via
`GOTOOLCHAIN=auto`. Workspace-internal modules now have explicit
`replace` directives in `mvd-web/go.mod` and `mvd-analytics/go.mod` so
`go mod tidy` resolves without trying to contact github.com for the
placeholder `v0.0.0` versions.

## Phase 3 — cross-demo / corpus tools

[Plan v3 §11.2](PLAN-event-streams-and-views-v3.md). Intent only.

Sits on top of `democache/results/v7/*.gob` from Phase 2 as the
corpus. Tools fetch N cached `*Result`s and run aggregation; the
per-demo `view` API composes naturally across many. Use cases TBD
by traffic: per-player season stats, per-map aggregates, free-form
corpus queries. If cache scales past a few thousand demos and
gob-load becomes slow, evaluate a column store (DuckDB over
Parquet, or SQLite extracted at cache-write time).

Concrete prerequisites (when this becomes real work):

- `mvd-api cache list` / `cache stats` (already wanted operationally;
  doubles as Phase 3 enumeration primitive).
- A streaming iterator over the corpus that doesn't load every
  `*Result` into memory at once.
- A query language or REST surface for cross-demo aggregations.
  Maintaining `mvd-analytics/view/` for per-demo + a new corpus view
  layer at `mvd-analytics/corpusview/` is the natural split.

## Pickup-attribution data quality

From `NOTES-pickup-attribution-quality.md` (working notes, not
checked in). Pre-existing issues that survived Phase 1 + Phase 2 —
neither refactor touched the analyzers themselves.

1. **`items.go` nearest-player tie-breaking** — fixed in `fd70394`
   (pre-Phase-1). Map-iteration nondeterminism replaced with
   stable slot ordering.
2. **`backpacks.go` records auth name instead of display name.**
   On auth-override demos this breaks downstream joins on player
   name. Simple fix; affects the map-tab overlay today.
3. **`items.go` reads stale positions** when attributing pickups.
   Should filter by recency or surface a weak-attribution flag.
   Worth quantifying against the KTX-authoritative pickup counts
   in the diagnostic harness first.
4. **`items.go` has no max-distance gate** on nearest-player
   selection. Degenerate when no one is near the pickup spawner
   (the "nearest" can be implausibly far).

Recommended triage order: §2 first (one-line fix, visible bug),
then §3 with a divergence harness against KTX counts, then §4.
