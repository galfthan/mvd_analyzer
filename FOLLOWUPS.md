# Follow-ups

Open items captured at the close of Phase 1 (schema v7). Not an
exhaustive backlog — just the things a future reader (or Claude) needs
to know wasn't done in this push and why.

## Phase 1 warts (intentional trade-offs we're carrying)

These were left in place to keep Phase 1 finishable. Each is correct
within Phase 1's scope; each leaves work for later.

### Parse-time 50 ms bucket structure retained

[`qwanalytics/analyzer/timeline_buckets.go`](qwanalytics/analyzer/timeline_buckets.go)
+ the `a.buckets []*timelineBucketData` field on `TimelineAnalyzer`
are kept as analyzer-internal scaffolding. The plan called for
deletion, but loc resolution (`Finalize` in
[`timeline_finalize.go`](qwanalytics/analyzer/timeline_finalize.go))
and the blip filter
([`timeline_blipfilter.go`](qwanalytics/analyzer/timeline_blipfilter.go))
both walk these buckets directly to smooth nearest-loc flicker at the
50 ms grain. Refactoring those to operate on `result.Streams` is a
larger change than Phase 1 absorbed.

The buckets are no longer exposed on `*Result`; they exist only
during the analyzer run and are discarded at finalize. The header
comment in `timeline_buckets.go` flags the situation.

**Fix path**: rewrite the loc resolution + blip filter to consume
`PlayerStream.Position` (native-rate) directly, smoothing on the
position event rate rather than the 50 ms grid. Then delete
`timeline_buckets.go` and the `buckets` / `populateBucket` /
`exportHighResBuckets` codepath.

### `BuildLocGraph` and `ComputeRegionControl` derive buckets

Both functions take `*Result` and call `view.Buckets(…, LegacyReducerSet)`
+ `view.ToLegacyHighResBuckets` to get a `[]HighResBucket` and walk
that. This works but inherits the v7 vs v6 bucketer-sampling drift
documented in
[`qwanalytics/RESULT_SCHEMA.md`](qwanalytics/RESULT_SCHEMA.md)'s v7
entry: per-edge counts off by 0.3–3.6 %, one borderline edge flipping
teleport↔normal classification.

**Fix path**: rewrite both to walk `Streams` natively without a bucket
intermediate. `BuildLocGraph` becomes "for each player, walk
`PlayerStream.Loc` + `PlayerStream.Position` together, emit edges on
loc transitions." `ComputeRegionControl` becomes "walk
`PlayerStream.Loc` per player + spawn/death gating, sum residence per
region per state." Discrete-event analytics (frags, items, etc.) are
already byte-identical with v6; this fix would close the locgraph
drift too.

Estimated effort: 4–6 hours for both, plus golden regen.

### `LegacyHighResBucket` shim retained

[`qwanalytics/view/legacy.go`](qwanalytics/view/legacy.go) exposes
`LegacyHighResBucket` as a type alias for `result.HighResBucket` and
`ToLegacyHighResBuckets(BucketsView) []HighResBucket`. Required by:

- WASM bridge `getDefaultBuckets` for the existing frontend panels.
- `BuildLocGraph` and `ComputeRegionControl` (until they're
  refactored — see above).

Once the frontend migrates per Phase 1.5 and locgraph/region-control
walk Streams natively, the shim becomes dead code and can be deleted
along with the `HighResBucket` / `HighResPlayerData` /
`HighResTeamData` types in `qwanalytics/result/timeline.go`.

### `playerActiveInWindow` uses a 100 ms position-presence fudge

[`qwanalytics/view/buckets.go`](qwanalytics/view/buckets.go) ::
`positionTouchesWindow` falls back to "any position sample in
`[bStart - 100ms, bEnd)`" when spawn/death streams don't determine
liveness. The fudge is permissive — if we tighten it, synthetic-test
fixtures (no spawn/death) break. Worth a cleaner design that
distinguishes "real demo, no synthetic SpawnEvent" from "test fixture
with no events."

### Worker runs extra bridge calls per analyze

[`qw-web/static/worker.js`](qw-web/static/worker.js) now calls
`getDefaultBuckets()` and `recomputeRegionControl(defaults)`
immediately after `analyzeMVD()` and bundles the JSON results into
the postMessage envelope. Required because the WASM exports live on
the worker's global scope, not `window`, and the existing panels
read `result.timelineAnalysis.highResBuckets` and
`.regionControl.bucketStates` synchronously. Cost: two extra
`view.Buckets` walks (~50–200 ms on a 4on4 demo) per load, on top
of `analyzeMVD` itself.

**Fix path**: fold the legacy-shape bucket build into
`analyzer.TimelineAnalyzer.Finalize` so it happens during the parse
pass instead of as a post-step. Or, when Phase 1.5 migrates panels
to call `getBuckets({windowMs})` per panel via the worker
postMessage protocol, drop both bridge calls from the analyze hot
path entirely.

### `tracks.go` shelved

[`qwanalytics/analyzer/tracks.go`](qwanalytics/analyzer/tracks.go)
operates on derived legacy buckets via `view.Buckets`. Per project
memory it's planned future work for movement-pattern visualisations
(Phase 3 of the original roadmap). Refactor to walk Streams natively
when its analyzer is revived.

### Equivalence test is internal-only

[`qwanalytics/view/equivalence_test.go`](qwanalytics/view/equivalence_test.go)
asserts bucket-count invariants and round-trip (`Buckets → synth
Result → Buckets → equal`). It does **not** compare against a v6
reference. The v7 vs v6 comparison was done ad-hoc during Phase 1
review (see RESULT_SCHEMA.md drift note); not pinned as a test.

If the locgraph drift bothers anyone in practice, add a test that
loads the demos in `corpus.json`, runs both v6 (from
`git show 3b4ea5b:…`) and current code, and asserts non-locgraph
sections are byte-identical.

### Native-rate position cadence not measured

The plan claimed ~77 Hz; the analyzer comment at
[`timeline.go:207`](qwanalytics/analyzer/timeline.go) says ~73 Hz.
Actual mvdsv emission rate varies with `sv_demoPings` and per-player
update rate. Worth measuring once across the corpus and pinning the
expected range as a documentation fact.

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
`timelineState.highResBuckets` reads with direct `getBuckets({windowMs})`
calls per panel. Highest-leverage targets: timeline-graph panels (currently
walk 24 K samples to render 800 px), region-control heatmap when zoomed
out. Map-tab playback (which actually wants every 50 ms position) keeps
its current data flow.

When this lands, the WASM bridge's `getDefaultBuckets` shim and the
legacy-shape types in `view/legacy.go` become dead code and can be
deleted.

## Phase 2 — hosted REST API + MCP server

[Plan v3 §11.1](PLAN-event-streams-and-views-v3.md). Intent only — no
detailed design yet.

**Goal**: open the analytics surface to non-Go / hosted consumers
(AI agents via MCP, third-party integrations via REST, future web
frontends that benefit from server-side caching).

**Rough shape**:

- Single binary `qw-mvd` with subcommands:
  - `qw-mvd serve` — HTTP REST API
  - `qw-mvd mcp` — MCP over stdio (local mode, imports view package directly)
  - `qw-mvd mcp --api URL` — MCP shim that proxies to a remote `serve`
- All subcommands shim over `qwanalytics/view/` — no transport
  reimplements analytics.
- Cache module under `qwanalytics/internal/democache/` — two-tier
  (raw MVD bytes content-hashed; parsed `*Result` schema-versioned).
  Schema bumps invalidate the result tier but keep the MVD tier;
  reparse on next access, no re-fetch from hub.
- Non-secret traffic-label tokens (e.g. `web-community`,
  `mcp-claude`, `cli-script`) for request-source analytics, not auth.
  Open access in v1.
- Tool / endpoint surface: `loadDemo`, `getOverview`, `getBuckets`,
  `getEvents`, `getStreamSlice`, `getStateAt`, `getLocTrails`,
  `getRegionControl`. Demo identity = hub gameId for hub URLs,
  SHA-256 for uploads. `loadDemo` idempotent.

**`make serve` is unchanged in this phase** — the existing WASM web
app stays as-is; the API serves a different audience.

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

1. **Default reducer policy.** Plan v3 D1 picks `last` everywhere to
   match v6 visuals. After traffic patterns from Phase 2 are visible,
   reconsider whether `mean` (vitals), `dominant` (loc), or
   `majority` (held-items at large windows) make AI / stats queries
   less surprising.

2. **Position track encoding.** Phase 1 ships int32 columnar JSON.
   Evaluate delta-encoded varints, fixed-point quantisation, or a
   binary sidecar if Phase 2 traffic shows it's the bottleneck.

3. **Percentile reducers** (`p10` / `p50` / `p90`). Likely needed
   once AI consumers ask for "stress moments." Add `pct(N)` reducer
   factory if usage materialises.

4. **`EventsFilter.EnrichWith`** — auto-resolve `StateAt` for
   selected fields and embed in `Detail`. Wait for usage before
   adding.

5. **`stripStreamPositions` is a mutation.** CLI's
   [`main.go`](qwanalytics/cmd/qw-analyze/main.go) nils
   `PlayerStream.Position` in-place when `-include positions` isn't
   set. Cleaner: a marshalling option that omits the field without
   mutating the original. Minor.

6. **`LegacyReducerSet` duplicates `AllStandardFields`.** Both live
   in [`view/fields.go`](qwanalytics/view/fields.go); one could be
   derived from the other. Minor.

7. **`-bucket` duration → ms conversion.** CLI parses `time.Duration`
   then converts to int milliseconds. A duration > ~24 days would
   overflow int32; not a real concern in practice but a sharp edge.

8. **`#timeline_buckets.go#` and similar emacs autosaves** show up in
   the working tree's untracked list. Consider adding `*#` and
   `.#*` patterns to a global gitignore or the project `.gitignore`.
