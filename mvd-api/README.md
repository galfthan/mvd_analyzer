# mvd-api — REST host for QuakeWorld demo analytics

`mvd-api` exposes [`mvd-analytics/view`](../mvd-analytics/view) as a
hosted HTTP REST API, backed by a two-tier on-disk cache that
resolves and downloads demos from
[hub.quakeworld.nu](https://hub.quakeworld.nu) on demand.

It's the server-side companion to [`mvd-mcp`](../mvd-mcp/README.md)
(the distributable stdio MCP shim that forwards tool calls to this
binary).

## Usage

```
mvd-api [-addr ADDR] [-cache-dir PATH] [-log-format text|json]
mvd-api version
```

| Flag | Default | Description |
|---|---|---|
| `-addr`        | `:8080`                                 | Listen address |
| `-cache-dir`   | `$XDG_CACHE_HOME/qw-mvd` or `~/.cache/qw-mvd` | On-disk cache root |
| `-log-format`  | `text`                                  | Access log format: `text` or `json` |

Schema bumps in `mvd-analytics` invalidate the parsed-`Result` tier
but keep the raw-MVD tier — the next access re-parses without
re-downloading from the hub.

## REST endpoints

All paths under the base URL (default `http://localhost:8080`). The
`{id}` segment is one of:

- `gameId:NNNN` — numeric hub.quakeworld.nu game id (server fetches
  the MVD if not cached locally)
- `sha:HEX` — 64-char SHA-256 of a demo already in the local cache
  (mostly for bookmarking warm cache entries)

Successful 2xx responses set `Cache-Control: public, max-age=86400,
immutable`, `X-Schema-Version: 7`, `X-Cache: HIT|WARM|MISS`, and
`ETag: "<sha>-v7"`. Send `If-None-Match` to get a cheap 304.

| Method | Path | Query params | 200 body |
|---|---|---|---|
| GET | `/healthz` | — | `{ok, schemaVersion}` |
| GET | `/v1/version` | — | `{hash, tag, buildDate}` |
| POST | `/v1/demos/{id}` | — | `{demoId, sha256, fromCache, schemaVersion}` (`loadDemo` — warms the cache) |
| GET | `/v1/demos/{id}/overview` | — | `Overview` (map, teams, top streaks, top powerups) |
| GET | `/v1/demos/{id}/buckets` | `windowMs`, `from`, `to`, `players`, `fields`, `reducers`, `includeTeam` | `view.BucketsView` |
| GET | `/v1/demos/{id}/events` | `from`, `to`, `players`, `types` | `view.EventsView` |
| GET | `/v1/demos/{id}/stream-slice` | `from`, `to`, `players`, `fields` | `view.StreamSliceView` |
| GET | `/v1/demos/{id}/state-at` | `time` (required), `players`, `fields` | `view.StateAtView` |
| GET | `/v1/demos/{id}/loc-trails` | `from`, `to`, `players`, `minDwellMs` | `view.LocTrailsView` |
| GET | `/v1/demos/{id}/region-control` | `windowMs` | `result.RegionControlResult` |

### Query conventions

- `players`, `fields`, `types`: comma-separated; URL-decode once.
- `reducers`: comma-separated `field=name` pairs (e.g. `h=min,a=last`).
- Times are match-relative seconds (float). `windowMs` is integer.
- Empty defaults match the view function defaults
  (see `mvd-analytics/view/fields.go` for field codes).

### Error envelope (4xx, 5xx)

```json
{ "error": { "code": "demo_not_found", "message": "gameId 0" } }
```

Stable codes:

- `400 invalid_demo_id` — malformed `{id}` or query param
- `400 invalid_param` — view-layer rejection (unknown field, bad reducer)
- `400 missing_param` — required param missing (e.g. `time` on state-at)
- `404 demo_not_found` — hub has no row for this gameId
- `422 region_control_unavailable` — demo has no region-control layout
- `502 hub_upstream` — network / 5xx from hub
- `500 internal` / `500 panic` — unexpected

## Authentication

There is none. The data is public and read-only. The optional
`Authorization: Bearer <label>` header (or `?label=` query param) is
**not validated** — it's a non-secret request-source tag captured in
the access log for analytics. Common labels: `mcp-claude-desktop`,
`web-community`, `cli-script`.

## Cache layout

Under `-cache-dir`:

```
mvd/<sha[:2]>/<sha>.mvd.gz             # tier 1 — raw bytes from hub
results/v<N>/<sha[:2]>/<sha>.gob       # tier 2 — parsed *Result, per schema version
index/games/<gameId>.txt               # gameId → sha map
```

A 4-on-4 demo typically occupies ~3–7 MB in tier 1 and ~3–10 MB in
tier 2. There is no automatic eviction; documented as a follow-up
(see [`FOLLOWUPS.md`](../FOLLOWUPS.md)).

## Smoke tests

```bash
mvd-api -addr :8080 -cache-dir /tmp/mvd-cache &

curl -s localhost:8080/healthz
# {"ok":true,"schemaVersion":7}

curl -s -X POST localhost:8080/v1/demos/gameId:12345
# first call:  fromCache:false
# second call: fromCache:true

curl -s 'localhost:8080/v1/demos/gameId:12345/overview' | jq '.map, .duration, .teams'

curl -s 'localhost:8080/v1/demos/gameId:12345/buckets?windowMs=1000&fields=h,a' \
  | jq '.buckets | length'

curl -s 'localhost:8080/v1/demos/gameId:12345/state-at?time=65&fields=h,a,rl,pos' | jq .

# Cache header sanity
curl -sI 'localhost:8080/v1/demos/gameId:12345/overview' | grep -i 'x-cache\|etag'

# Error mapping
curl -s -w 'HTTP %{http_code}\n' 'localhost:8080/v1/demos/banana/overview'    # 400 invalid_demo_id
curl -s -w 'HTTP %{http_code}\n' 'localhost:8080/v1/demos/gameId:0/overview'  # 404 demo_not_found
```

## Build

```bash
make build-api                              # ./dist/mvd-api
make build-api-{linux,darwin,windows}       # cross-compile targets
make build-all-platforms                    # everything + mvd-mcp targets
```

## Pairing with mvd-mcp

For MCP clients (Claude Desktop, Cursor, Claude Code), run `mvd-api`
either hosted or on localhost, then point
[`mvd-mcp`](../mvd-mcp/README.md) at it:

```bash
mvd-api -addr :8080 &
mvd-mcp -api http://localhost:8080
```

See [`mvd-mcp/CLAUDE_DESKTOP.md`](../mvd-mcp/CLAUDE_DESKTOP.md) for
client config snippets.
