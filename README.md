# MVD Analyzer

A three-layer toolkit for QuakeWorld demo analysis. MVD bytes go in one end,
structured analysis comes out the middle, and browser/CLI/AI consumers pick
up whatever they need from the Result JSON at the far end.

## Architecture

```
  ┌─────────────┐   Event schema   ┌─────────────┐   Result schema   ┌──────────────┐
  │   Source    │ ───────────────▶ │  Analytics  │ ────────────────▶ │   Consumer   │
  │  (Layer 1)  │                  │  (Layer 2)  │                   │  (Layer 3)   │
  └─────────────┘                  └─────────────┘                   └──────────────┘
   MVD file, QTV                    Pipeline of                       Web UI, CLI,
   stream, JSON                     analyzers over                    AI review
   replayer                         event stream                      agent, bulk
                                                                      batch tool
```

The *schemas* — events and results — are the real contracts. Implementations
on either side can come and go as long as the schemas hold.

### Five Go modules in one workspace

The repo is a Go workspace (`go.work`) binding five sibling modules:

| Module | Path | Role |
|---|---|---|
| [mvd-reader](mvd-reader/README.md)       | `mvd-reader/`       | Event schema + MVD source (Layer 1)               |
| [mvd-analytics](mvd-analytics/README.md) | `mvd-analytics/`    | Analysis pipeline + Result schema + view API (L2) |
| [mvd-api](mvd-api/README.md)             | `mvd-api/`          | HTTP REST server on top of `mvd-analytics/view`   |
| [mvd-mcp](mvd-mcp/README.md)             | `mvd-mcp/`          | Distributable stdio MCP shim that talks to mvd-api |
| [mvd-web](mvd-web/README.md)             | `mvd-web/`          | Browser UI + WASM glue (Layer 3)                  |

Each module has its own `go.mod`, is tested in isolation, and can be extracted
to its own repo later. Until that's needed, the workspace keeps
cross-layer iteration fast: one git tree, one PR per change.

### Why layered?

Splitting ingestion, analytics, and transport into separate layers lets each
grow on its own timeline. Today's concrete shape:

- **Layer 1 (`mvd-reader`)** is the only place that knows the MVD binary
  format. A future QTV live-stream source would sit beside the MVD source
  and emit the same events — downstream analytics wouldn't change.
- **Layer 2 (`mvd-analytics`)** is the only place that knows how to compute
  match summaries, frag streaks, timeline buckets, or loc-graphs. New
  analytics land here. The `view/` sub-package turns the canonical `Streams`
  into bucketed timelines, event lists, point-in-time state, and loc trails.
  Analytics never peeks at MVD bytes; it consumes events.
- **Layer 3 consumers** read `Result` or call `view/` and produce something
  user-facing. There are four today:
  - `mvd-analytics/cmd/qw-analyze` — offline CLI (one demo → JSON / md / events).
  - `mvd-api` — hosted REST API + two-tier on-disk cache.
  - `mvd-mcp` — tiny stdio MCP shim that forwards every tool call to a
    running `mvd-api`. Distributable as a small `.exe` for Claude Desktop /
    Cursor / Claude Code.
  - `mvd-web` — browser UI compiled to WASM.

## Quick start

### Analyze a demo at the command line

```bash
go run ./mvd-analytics/cmd/qw-analyze demo.mvd.gz                 # Result JSON to stdout
go run ./mvd-analytics/cmd/qw-analyze -format md demo.mvd.gz      # human summary
go run ./mvd-analytics/cmd/qw-analyze -format events demo.mvd.gz  # line-delimited events
```

### Run the web UI locally

```bash
make serve                                  # http://localhost:8080
```

### Build the WASM bundle for deploy

```bash
make build                                  # output in dist/
```

### Serve the REST API (`mvd-api`)

```bash
make build-api                              # ./dist/mvd-api
./dist/mvd-api -addr :8080                  # HTTP REST on top of mvd-analytics/view
make build-api-linux build-api-darwin build-api-windows
```

`mvd-api` hosts the analytics surface for non-Go consumers
(third-party integrations, the MCP shim, a future web frontend that
benefits from server-side caching). See
[`mvd-api/README.md`](mvd-api/README.md) for the endpoint table.

### Distribute MCP locally (`mvd-mcp`)

```bash
make build-mcp                              # ./dist/mvd-mcp
./dist/mvd-api -addr :8080 &                # local API (or point at a remote one)
./dist/mvd-mcp -api http://localhost:8080   # stdio MCP shim, forwards tools over HTTP
make build-mcp-windows                      # dist/mvd-mcp-windows-amd64.exe for Claude Desktop
```

`mvd-mcp` is a thin (~5 MB) stdio MCP server. It has no analytics
code of its own — every tool call is a forwarded HTTP request to a
running `mvd-api`. The split lets you ship a small distributable
binary for end-users (Claude Desktop / Cursor / Claude Code) without
bundling the parser. See
[`mvd-mcp/README.md`](mvd-mcp/README.md) and
[`mvd-mcp/CLAUDE_DESKTOP.md`](mvd-mcp/CLAUDE_DESKTOP.md).

Other Makefile targets: `make test`, `make fmt`, `make clean`, `make help`.

## The contracts

### Event schema (Layer 1 → 2)

Defined in [`mvd-reader/events`](mvd-reader/events/events.go). A `Source` is a
pull-style iterator:

```go
type Source interface {
    Next() (Event, error)   // returns io.EOF at clean end
    Close() error
}
```

Concrete event types are plain structs: `ServerDataEvent`, `UserInfoEvent`,
`PrintEvent`, `StatUpdateEvent`, `FragUpdateEvent`, `PlayerPositionEvent`,
`DamageEvent`, `DemoInfoEvent`, `IntermissionEvent`, `StuffTextEvent`,
`CenterPrintEvent`, `ServerInfoEvent`, `DeathEvent`, `SpawnEvent`,
`ItemSpawnEvent`, `ItemStateEvent`, `BackpackDropHintEvent`,
`ItemPickupHintEvent`, `BackpackPickupHintEvent`,
`ItemPickupPrintEvent`, `BackpackPickupPrintEvent`. Domain types
carried by events — `ServerData`, `PlayerInfo`, `PlayerState`,
`Stats` — are source-agnostic.

`DeathEvent` / `SpawnEvent` are derived events the parser synthesises
from `StatHealth` edges so analytics never has to reconstruct
death/spawn by comparing samples across the sampling boundary.
`ItemSpawnEvent` / `ItemStateEvent` are derived from the entity-state
stream (`svc_spawnbaseline` + `svc_packetentities` /
`svc_deltapacketentities`): every item's identity and
pickup/respawn transitions come out of the wire directly — no KTX
prints, no BSP preprocessing. `ItemPickupHintEvent` /
`BackpackPickupHintEvent` / `BackpackDropHintEvent` carry KTX's
authoritative `//ktx took`, `//ktx bp`, `//ktx drop` directives — the
touch-level pickup attribution that entity-state alone can only
approximate. They only fire on KTX servers; non-KTX sources get
entity-state and stats deltas. `ItemPickupPrintEvent` /
`BackpackPickupPrintEvent` parse the per-client "You got the X"
prints that target the picking player via `dem_single`; they fill
the gap where `//ktx took` is silent (ammo boxes, H15/H25, non-RL/LG
backpacks) but only survive to the MVD for players who set `msg 0`
in their client config (see `mvd-reader/MVD_FORMAT.md` for the
server-side `messagelevel` filter that strips PRINT_LOW in most
competitive demos).

To write a new source: implement `events.Source`, emit the concrete event
types as you decode your wire format. That's it. See
[`mvd-reader/source/mvd`](mvd-reader/source/mvd/source.go) for the reference
implementation backed by MVD files.

### Result schema (Layer 2 → 3)

Defined in [`mvd-analytics/result`](mvd-analytics/result/result.go). `Result` is
a JSON-serializable struct with sub-results from every analyzer that ran:
match, frags, messages, demoinfo, timeline analysis, metadata, locgraph,
items (per-item pickup / respawn timeline — works on any MVD source),
backpacks (RL/LG drops attributed to the dropping player via KTX's
`//ktx drop` hint), and weaponPickups (every slot-weapon acquisition —
world spawners and RL/LG backpacks — with a kills-before-next-death
effectiveness metric; joins to backpacks via `backpackEnt` ==
`backpacks[].entNum`). At schema v7 `streams` is the canonical
event-rate storage — every per-player field (vitals, weapons, ammo,
position) recorded at the rate it actually changed. Bucketed,
event-list, and point-in-time views are produced on demand by the
`mvd-analytics/view` query API (also surfaced via the CLI's `-view`
flag and the WASM bridge's `getBuckets` / `getEvents` /
`getStreamSlice` / `getStateAt` exports).

Every breaking change bumps `CurrentSchemaVersion` (currently `7`).
Consumers can pin or feature-detect by reading `result.schemaVersion`.
The full per-field reference lives in
[mvd-analytics/RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md).

### Running the pipeline

```go
import (
    "github.com/mvd-analyzer/mvd-analytics/analyzer"
    mvdsource "github.com/mvd-analyzer/mvd-reader/source/mvd"
)

src, err := mvdsource.Open("demo.mvd.gz")
if err != nil { ... }
defer src.Close()

reg := analyzer.NewDefaultRegistry()
res, err := reg.AnalyzeSource(src, "demo.mvd.gz")
// res is *result.Result; marshal to JSON, inspect, etc.
```

Swap the source and the rest keeps working:

```go
src := myQTVClient.Open(...)       // implements events.Source
res, err := reg.AnalyzeSource(src, "live")
```

## Repository layout

```
mvd-analyzer/
  go.work                  Workspace — names the five modules
  Makefile                 Top-level coordinator (build / serve / test / fmt)
  netlify.toml             Netlify deploy config
  README.md                This file

  mvd-reader/              Module: ingestion layer (Layer 1)
    events/                Public contract — Source, Event types, domain types
    mvd/                   MVD wire decoder (internal)
    parser/                Messages → events (internal)
    mvdfile/               Gzip-aware reader
    source/mvd/            Source implementation for MVD files

  mvd-analytics/           Module: analysis pipeline (Layer 2)
    analyzer/              Analyzer interface + Context + CoreOutputs + Registry
    result/                JSON result schema (stable contract)
    view/                  Pure query API: Buckets, Events, StreamSlice, StateAt, ...
    loc/                   .loc parser + embedded corpus (466 maps)
    hubfetch/              Resolve + download from hub.quakeworld.nu (used by mvd-api)
    mapgen/                Quake 1 BSP reader + floor-face extraction
    diagnostic/            Opt-in bulk validation harness
    cmd/mapgen/            Developer tool: BSP → per-loc floor-polygon JSON
    cmd/qw-analyze/        Offline CLI: demo → json|md|events

  mvd-api/                 Module: REST host + on-disk cache (Layer 3, server)
    main.go, serve.go      HTTP entry
    handlers.go, router.go REST endpoints over mvd-analytics/view
    overview.go            Curated demo summary
    internal/democache/    Two-tier disk cache (raw MVD + parsed Result)

  mvd-mcp/                 Module: distributable stdio MCP shim
    main.go                Stdio MCP entry
    mcp_backend_proxy.go   Forwards each tool call as HTTP to a remote mvd-api
    No mvd-analytics import — outputs are opaque JSON pass-through

  mvd-web/                 Module: browser UX + WASM glue (Layer 3, frontend)
    static/                index.html, app.js, worker.js, styles.css, maps/
    cmd/wasm/              WASM entry (exports analyzeMVD to JS)

  demos/                   Corpus for regression + manual testing (untracked)
```

## Documentation

- [mvd-reader/README.md](mvd-reader/README.md) — ingestion layer, how to add a source
- [mvd-reader/MVD_FORMAT.md](mvd-reader/MVD_FORMAT.md) — MVD binary format spec with ezQuake references
- [mvd-analytics/README.md](mvd-analytics/README.md) — pipeline, how to add an analyzer, Result schema
- [mvd-analytics/RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md) — Result JSON schema reference (every field, every section)
- [mvd-api/README.md](mvd-api/README.md) — REST endpoint table, cache layout, smoke tests
- [mvd-mcp/README.md](mvd-mcp/README.md) — stdio MCP shim, distribution
- [mvd-mcp/CLAUDE_DESKTOP.md](mvd-mcp/CLAUDE_DESKTOP.md) — Claude Desktop / Claude Code config snippets
- [mvd-web/README.md](mvd-web/README.md) — browser UI, build and deploy

## Testing

```bash
make test                                               # all modules
go test ./mvd-analytics/analyzer/                         # single package
go test -v -run TestDiagnosticParseDemos \
    ./mvd-analytics/diagnostic/                           # opt-in demo corpus
```

### Golden corpus

`make test` runs `TestGoldenCorpus` (in `mvd-analytics/analyzer/golden_test.go`)
against a manifest of hub.quakeworld.nu game IDs in
[`mvd-analytics/testdata/corpus.json`](mvd-analytics/testdata/corpus.json).
On first run it downloads each demo into
`mvd-analytics/testdata/cache/<gameId>.mvd.gz` (gitignored); subsequent runs
hit the cache and stay offline. Each demo's `Result` JSON is pinned
against `mvd-analytics/testdata/golden/<label>.json`.

What is pinned: everything except `filePath`. At schema v7 the
canonical event-rate storage is `streams` (per-player change streams +
intervals + native position track) — bucketed views are produced on
demand by `mvd-analytics/view.Buckets` and not stored. Per-player time
series in `streams.players[]` are sliced to three 15 s windows
(`[0, 15]`, `[60, 75]`, last 15 s) before comparison — the native
position track alone would otherwise run ~10 MB per 4on4 demo and
swamp the git history (see [`golden_test.go`](mvd-analytics/analyzer/golden_test.go)
`sampleStreams`). The three windows are enough sampling to catch
stream-emitter / bucketer drift while keeping the committed corpus
~4 MB per 4on4. Bucketed-view behavior is exercised through the unit
tests in `mvd-analytics/view/equivalence_test.go`.

The manifest ships with nine demos (three each of 1on1, 2on2, 4on4).
Add entries by appending to the JSON array; labels follow
`mode_team1_team2_DDMMYY_map` (or player names for 1on1, where
`team_names` is null on the hub).

Workflow when an analyzer change shifts output:

```bash
make test
# TestGoldenCorpus fails with first-diff-line per demo.
# Inspect the change, then if it was intended:
go test ./mvd-analytics/analyzer/... -run TestGoldenCorpus -args -update-golden
git diff mvd-analytics/testdata/golden/   # review
git add mvd-analytics/testdata/golden/    # commit alongside the analyzer change
```

(The `-update-golden` flag is registered only in the analyzer test
package; wider scopes like `./mvd-analytics/...` fail in `mapgen` with
"flag provided but not defined".)

The pipeline also has a CLI for ad-hoc bulk diffs:

```bash
go run ./mvd-analytics/cmd/qw-analyze -bulk -out-dir /tmp/before -format json demos/
# ... change ...
go run ./mvd-analytics/cmd/qw-analyze -bulk -out-dir /tmp/after  -format json demos/
diff -r /tmp/before /tmp/after
```

## Known limitations

1. **Weapon switching scripts**: QW players use scripts that switch weapons
   faster than MVD stat updates, causing RL/GL shot undercounting in
   MVD-based tracking. KTX demoinfo stats (when available) are authoritative.

2. **Auth name override**: When players authenticate via mvdsv,
   `sv_forcenick` can set the userinfo name to the login. The analyzer
   resolves display names from KTX demoinfo via `*auth` login join.

3. **Same-tick item insta-regrab**: If an item respawns and is picked up
   again within a single server tick (camped spawn), the wire never
   emits a "visible" transition for that cycle. The items analyzer
   recovers these via two synthesis paths (KTX `//ktx took` hint-driven
   for armors/MH/weapons/powerups; stat-delta + position for small
   healths and ammo), so per-touch counts now match KTX's authoritative
   `tooks` on 8 of 9 corpus demos. The remaining residual is bounded
   to small healths in rare edge cases (damage-in-same-frame). See
   [mvd-reader/MVD_FORMAT.md#item-tracking-via-entity-state](mvd-reader/MVD_FORMAT.md#item-tracking-via-entity-state)
   and [mvd-analytics/analyzer/items.md](mvd-analytics/analyzer/items.md#insta-regrab-synthesis).

## Reference sources

| Project | Description |
|---|---|
| [KTX](https://github.com/QW-Group/ktx) | Server mod — damage calc, demoinfo JSON, hidden message types |
| [mvdsv](https://github.com/QW-Group/mvdsv) | MVD server — demo recording, userinfo handling |
| [ezQuake](https://github.com/QW-Group/ezquake-source) | Client — demo parsing, character encoding |

## License

mvd-analyzer is released under the MIT License — see [LICENSE](LICENSE).

It analyzes demo files from QuakeWorld, whose Quake engine is GPL-
licensed; this repo only consumes the wire format and does not
incorporate engine source.

## Acknowledgments

- [QW-Group](https://github.com/QW-Group) for KTX, mvdsv, ezQuake, and mvdparser
- The QuakeWorld community for demo format documentation
