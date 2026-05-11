# Follow-ups

Open items for the v7 streams refactor. Captures what's left after the
branch lands — not an exhaustive backlog, just the things a future
reader (or Claude) needs to know wasn't done in this push and why.

## Open warts (carried trade-offs)

### `LegacyHighResBucket` shim retained

[`qwanalytics/view/legacy.go`](qwanalytics/view/legacy.go) exposes
`LegacyHighResBucket` as a type alias for `result.HighResBucket` and
`ToLegacyHighResBuckets(BucketsView) []HighResBucket`. Required only
by the WASM bridge's `getDefaultBuckets` for the existing frontend
panels (see "Worker runs extra bridge calls" below).

Once Phase 1.5 lands and panels read directly via per-panel
`getBuckets({windowMs, fields})` calls, the shim becomes dead code
and can be deleted along with `result.HighResBucket` /
`HighResPlayerData` / `HighResTeamData` types in
`qwanalytics/result/timeline.go`.

### Worker runs extra bridge calls per analyze

[`qw-web/static/worker.js`](qw-web/static/worker.js) calls
`getDefaultBuckets()` and `recomputeRegionControl(defaults)`
synchronously after `analyzeMVD()` and bundles the JSON into the
postMessage envelope. Required because WASM exports live on the
worker's global scope (not `window`), and the existing frontend
panels read `result.timelineAnalysis.highResBuckets` and
`.regionControl.bucketStates` synchronously at init.

Cost: a `view.Buckets` walk + a region-control compute (~50–200 ms
on a 4on4 demo) per load, on top of `analyzeMVD` itself. Roughly
4 s of the v7-vs-main reload time gap.

**Fix path**: Phase 1.5 — panels migrate to call
`getBuckets({windowMs: pixel-derived, fields: [needed]})` per panel
via the worker postMessage protocol. Initial paint becomes scoreboard
+ chat + frags only (sub-second on cached WASM); bucket-dependent
panels load lazily on tab open.

### `playerActiveInWindow` 100 ms position-presence fudge

[`qwanalytics/view/buckets.go`](qwanalytics/view/buckets.go) ::
`positionTouchesWindow` falls back to "any position sample in
`[bStart - 100ms, bEnd)`" when the spawn/death streams don't
determine liveness. The fudge is permissive — tightening it breaks
synthetic-test fixtures that have no spawn/death/position events.
Worth a cleaner design that distinguishes "real demo, no synthetic
SpawnEvent yet" from "test fixture with no events."

### Equivalence test is internal-only

[`qwanalytics/view/equivalence_test.go`](qwanalytics/view/equivalence_test.go)
asserts bucket-count invariants and round-trip
(`Buckets → synth Result → Buckets → equal`). It does **not** compare
against a v6 reference. The v7-vs-v6 comparisons during the refactor
were ad-hoc; not pinned as a test.

If main-vs-branch drift bothers anyone in practice, add a test that
loads the demos in `corpus.json`, runs both v6 (from
`git show 3b4ea5b:…`) and current code, and asserts non-locgraph
sections are byte-identical.

### Bucket grid drift vs main (small)

v7's bucket grid is anchored at match-relative `t = 0` (bucket 0
spans `[0, 0.05)`), while v6's was anchored at the wall-clock 50 ms
grid and shifted by `−matchStart` post-process — landing v6's first
surviving bucket at some funny T like 0.037. Same data, different
grid. For lookup at a given time, v6 and v7 pick samples up to one
sample-interval (~13 ms × motion speed = ~4 units at walking)
apart. Most visible during fast moves (rocket jumps, teleporters).

This is a deliberate choice — v7's grid is more predictable
(`bucket[i].T == i × windowMs`, demo-independent). The visual offset
vs main is a one-time cost during the v7 → main transition.

### Native-rate position cadence not measured

The plan claimed ~77 Hz; the analyzer comment used to say ~73 Hz.
Actual mvdsv emission rate varies with `sv_demoPings` and per-player
update rate. Worth measuring once across the corpus and pinning the
expected range.

### Memory pressure during parse not measured

Streams hold every change ever recorded — ~12 MB per 4on4 match,
mostly position. Fine for browser WASM (which routinely allocates
hundreds of MB). Worth measuring on larger demos (long matches, FFA
with many players) before assuming it scales.

### Schema-version-7 PR not yet merged

The work in this branch hasn't been squash-merged to `main`. Once
that lands, the giant 50 MB+ goldens that briefly existed in
`35757a2` will be left behind on the feature-branch commit history;
no need to scrub them.

## Phase 1.5 — frontend panel migration

[Plan v3 §11.0](PLAN-event-streams-and-views-v3.md). Replace
`timelineState.highResBuckets` reads with direct
`getBuckets({windowMs})` calls per panel. Highest-leverage targets:
timeline-graph panels (currently walk 24 K samples to render 800 px),
region-control heatmap when zoomed out. Map-tab playback (which
actually wants every 50 ms position) keeps its current data flow.

When this lands, the WASM bridge's `getDefaultBuckets` shim and the
legacy-shape types in `view/legacy.go` become dead code and can be
deleted.

## Phase 2 — hosted REST API + MCP server (landed)

Implemented at [`qwanalytics/cmd/qw-mvd/`](qwanalytics/cmd/qw-mvd/).
The binary has three subcommands: `serve` (HTTP REST), `mcp` (stdio
MCP, local), and `mcp -api URL` (stdio MCP proxy → hosted serve).
The Plan-v3 §11.1 scope (transports = stdio MCP + REST only; intake =
hub gameId only; cache = two-tier disk; CLI = serve/mcp only) landed
verbatim. See [`qwanalytics/cmd/qw-mvd/README.md`](qwanalytics/cmd/qw-mvd/README.md).

`make serve` (the WASM web app) is unchanged.

### qw-mvd v1 follow-ups

- **No cache eviction.** Tier 1 + tier 2 grow without bound. Ship
  a `qw-mvd cache prune --older-than 30d` (or similar) subcommand
  before the cache becomes a real ops problem.
- **No pre-rendered view tier.** Every REST hit recomputes the view
  from the cached `*Result`. If a hot `(demoId, view, opts)` tuple
  shows up in access logs at meaningful rate, add the third tier
  keyed by `(demoId, schemaVersion, view, optsHash)`.
- **No rate limiting.** Labels are recorded for analytics but not
  acted on. Add per-label / per-IP token bucket if abuse appears.
- **No release pipeline.** `make build-mvd-all` produces binaries
  locally; wire a GitHub Actions workflow that attaches them to
  releases.
- **Windows code-signing.** Unsigned `.exe` triggers SmartScreen.
  Either accept the warning (documented in `CLAUDE_DESKTOP.md`) or
  obtain an Authenticode cert.
- **No remote MCP transport.** Streamable HTTP MCP isn't exposed.
  Once a specific MCP client demands it, add a `/mcp` route that
  uses the SDK's HTTP handler — open access remains acceptable for
  public read-only data, but the MCP spec is moving toward an OAuth
  protected-resource convention, so plan for `.well-known/oauth-
  protected-resource` if real auth is needed.
- **No streaming responses for huge views.** A 4on4 buckets call at
  50ms can exceed 10 MB; encoded as a single JSON document. Move to
  newline-delimited JSON or chunked responses if a client chokes.
- **Toolchain bump.** Pulling in `github.com/modelcontextprotocol/
  go-sdk` v1.6 required Go 1.25; `go.work` and `qwanalytics/go.mod`
  pin via the `toolchain` directive (`go1.25.0`). Older Go versions
  fetch the toolchain automatically via `GOTOOLCHAIN=auto`. Workspace-
  internal modules now have explicit `replace` directives in
  `qw-web/go.mod` and `qwanalytics/go.mod` so `go mod tidy` resolves
  without contacting github.com.

## Phase 3 — cross-demo / corpus tools

[Plan v3 §11.2](PLAN-event-streams-and-views-v3.md). Intent only.

Sits on top of `democache/results/*.gob` from Phase 2 as the corpus.
Tools fetch N cached `*Result`s and run aggregation; the per-demo
`view` API composes naturally across many. Use cases TBD by traffic:
per-player season stats, per-map aggregates, free-form corpus
queries. If cache scales past a few thousand demos and gob-load
becomes slow, evaluate a column store (DuckDB over Parquet, or
SQLite extracted at cache-write time).

## Smaller follow-ups

1. **Position track encoding.** Ships int32 columnar JSON. Evaluate
   delta-encoded varints, fixed-point quantisation, or a binary
   sidecar if Phase 2 traffic shows it's the bottleneck.

2. **Percentile reducers** (`p10` / `p50` / `p90`). Likely needed
   once AI consumers ask for "stress moments." Add `pct(N)` reducer
   factory if usage materialises.

3. **`EventsFilter.EnrichWith`** — auto-resolve `StateAt` for
   selected fields and embed in `Detail`. Wait for usage before
   adding.

4. **`stripStreamPositions` is a mutation.** CLI's
   [`main.go`](qwanalytics/cmd/qw-analyze/main.go) nils
   `PlayerStream.Position` in-place when `-include positions` isn't
   set. Cleaner: a marshalling option that omits the field without
   mutating the original. Minor.

5. **`-bucket` duration → ms conversion.** CLI parses `time.Duration`
   then converts to int milliseconds. A duration > ~24 days would
   overflow int32; not a real concern in practice but a sharp edge.

6. **Drop `PlayerStream.Loc` in favour of `PositionTrack.Li`?** The
   sparse Loc change stream duplicates info already in the dense
   per-position-sample Li column. Keeping both costs ~150 KB JSON
   per match for the convenience of sparse-access consumers (AI
   agents asking "what loc was the player in at time T" naturally
   want the sparse form). Decision documented:
   [keep both](PLAN-event-streams-and-views-v3.md).

7. **`#timeline_buckets.go#` and similar emacs autosaves** show up
   in the working tree's untracked list. Consider adding `*#` and
   `.#*` patterns to `.gitignore`.

8. **CLI smoke test** — there's no end-to-end test that runs
   `qw-analyze -view {buckets|events|stream-slice|state-at|trails|
   region-control}` against the corpus and verifies output shape.
   Worth adding once Phase 2 brings more transports.
