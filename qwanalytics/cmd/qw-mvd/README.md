# qw-mvd — REST + stdio MCP for QuakeWorld demo analytics

`qw-mvd` is the network-facing binary that hosts the `qwanalytics/view`
query surface over HTTP REST and stdio MCP. It backs every endpoint
with a two-tier disk cache that resolves and downloads demos from
[hub.quakeworld.nu](https://hub.quakeworld.nu) on demand.

Phase 1 (schema v7 / canonical `Streams`) made the analytics
streamable; this binary is the network adapter on top of it.

## Subcommands

```
qw-mvd serve                # HTTP REST API
qw-mvd mcp                  # MCP over stdio, local (parses on this machine)
qw-mvd mcp -api URL         # MCP over stdio, proxies tool calls to a remote serve
qw-mvd version              # build info
```

`qw-mvd serve` and `qw-mvd mcp` (local) both rely on the on-disk cache
under `-cache-dir` (default `$XDG_CACHE_HOME/qw-mvd` or
`~/.cache/qw-mvd`). A schema bump invalidates the parsed-`Result` tier
but keeps the raw-MVD tier — the next access re-parses without
re-downloading from the hub.

## REST endpoints

All paths are under the base URL (default `http://localhost:8080`).
The `{id}` segment is one of:

- `gameId:NNNN` — a numeric hub.quakeworld.nu game id (server fetches
  the MVD if not cached locally)
- `sha:HEX` — the 64-char SHA-256 of a demo already in the local cache
  (mostly for bookmarking warm cache entries)

Successful 2xx responses set `Cache-Control: public, max-age=86400,
immutable`, `X-Schema-Version: 7`, `X-Cache: HIT|WARM|MISS`, and
`ETag: "<sha>-v7"`. Send `If-None-Match` to get a cheap 304.

| Method | Path | Query params | 200 body |
|---|---|---|---|
| GET | `/healthz` | — | `{ok, schemaVersion}` |
| GET | `/v1/version` | — | `{hash, tag, buildDate}` |
| POST | `/v1/demos/{id}` | — | `{demoId, sha256, fromCache, schemaVersion}` (`loadDemo` — warms the cache) |
| GET | `/v1/demos/{id}/overview` | — | `Overview` (map, teams, top streaks, etc.) |
| GET | `/v1/demos/{id}/buckets` | `windowMs`, `from`, `to`, `players`, `fields`, `reducers`, `includeTeam` | `view.BucketsView` |
| GET | `/v1/demos/{id}/events` | `from`, `to`, `players`, `types` | `view.EventsView` |
| GET | `/v1/demos/{id}/stream-slice` | `from`, `to`, `players`, `fields` | `view.StreamSliceView` |
| GET | `/v1/demos/{id}/state-at` | `time` (required), `players`, `fields` | `view.StateAtView` |
| GET | `/v1/demos/{id}/loc-trails` | `from`, `to`, `players`, `minDwellMs` | `view.LocTrailsView` |
| GET | `/v1/demos/{id}/region-control` | `windowMs` | `result.RegionControlResult` |

**Query conventions:**
- `players`, `fields`, `types` are comma-separated.
- `reducers` is a comma-separated list of `field=name` pairs, e.g. `h=min,a=last`.
- Times are match-relative seconds (float); `windowMs` is an integer.
- Field codes match the canonical view vocabulary: `h, a, at, li, pos, rl, lg, gl, ssg, sng, q, pe, r, sh, nl, rk, cl, sp, d`. See `qwanalytics/view/fields.go`.

**Error envelope** (4xx, 5xx):
```json
{ "error": { "code": "demo_not_found", "message": "gameId 0" } }
```

Stable error codes:
- `400 invalid_demo_id` — malformed `{id}` or query param
- `400 invalid_param` — view-layer rejection (unknown field, bad reducer)
- `400 missing_param` — required param missing (e.g. `time` on state-at)
- `404 demo_not_found` — hub has no row for this gameId
- `422 region_control_unavailable` — demo has no region-control layout
- `502 hub_upstream` — network / 5xx from hub
- `500 internal` / `500 panic` — unexpected

## MCP tools

Every REST endpoint has a 1:1 MCP tool counterpart with structured
input/output (JSON Schema inferred from Go struct tags via the
official `github.com/modelcontextprotocol/go-sdk`).

| Tool | Backs |
|---|---|
| `loadDemo(gameId or sha256)` | `POST /v1/demos/{id}` |
| `getOverview(demoId)` | `/overview` |
| `getBuckets(demoId, windowMs, ...)` | `/buckets` |
| `getEvents(demoId, types, ...)` | `/events` |
| `getStreamSlice(demoId, from, to, fields, ...)` | `/stream-slice` |
| `getStateAt(demoId, time, fields, ...)` | `/state-at` |
| `getLocTrails(demoId, minDwellMs, ...)` | `/loc-trails` |
| `getRegionControl(demoId, windowMs)` | `/region-control` |

`demoId` is the string returned by `loadDemo` (`sha:HEX`) or any
`gameId:NNNN` reference. Tool errors come back as MCP `isError: true`
results with the error message in `TextContent` — the model can read
them and recover (e.g. by calling `loadDemo` first).

## Local vs. proxy MCP

```
# Local mode — this machine parses the demo and caches it.
qw-mvd mcp -cache-dir /var/lib/qw-mvd

# Proxy mode — forward every tool call to a hosted REST API.
qw-mvd mcp -api https://qw-mvd.example.com -label mcp-claude
```

Proxy mode is the recommended distribution shape: ship the binary,
run it locally for MCP, but offload the heavy parse + cache work to a
hosted `qw-mvd serve`. The local binary is a thin shim — every tool
call becomes an HTTP request.

The `-label` flag (or `Authorization: Bearer <label>` on REST) is **not
authentication** — it's a non-secret request-source tag (`mcp-claude`,
`web-community`, `cli-script`, ...). It's recorded in access logs for
analytics; the server never validates it.

## Cache layout

Under `-cache-dir`:

```
mvd/<sha[:2]>/<sha>.mvd.gz             # tier 1 — raw bytes from hub
results/v<N>/<sha[:2]>/<sha>.gob       # tier 2 — parsed *Result (per schema version)
index/games/<gameId>.txt               # gameId → sha map
```

A 4-on-4 demo typically occupies ~3–7 MB in tier 1 and ~3–10 MB in
tier 2. There is no automatic eviction in this version; documented as
a follow-up.

## Smoke tests

```sh
qw-mvd serve -addr :8080 &
curl -s localhost:8080/healthz
curl -s -X POST localhost:8080/v1/demos/gameId:12345
curl -s 'localhost:8080/v1/demos/gameId:12345/overview' | jq .
curl -s 'localhost:8080/v1/demos/gameId:12345/buckets?windowMs=1000&fields=h,a' | jq '.buckets | length'

# Test the proxy round-trip.
qw-mvd mcp -api http://localhost:8080 < tests/example.jsonl | jq .
```

## Claude Desktop integration

See [`CLAUDE_DESKTOP.md`](CLAUDE_DESKTOP.md) for a copy-paste config
snippet for both local and proxy modes on Windows / macOS / Linux.
