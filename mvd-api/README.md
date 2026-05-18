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
immutable`, `X-Schema-Version: 9`, `X-Cache: HIT|WARM|MISS`, and
`ETag: "<sha>-v9"`. Send `If-None-Match` to get a cheap 304.

| Method | Path | Query params | 200 body |
|---|---|---|---|
| GET | `/healthz` | — | `{ok, schemaVersion}` |
| GET | `/v1/version` | — | `{hash, tag, buildDate}` |
| POST | `/v1/demos/{id}` | — | `{demoId, sha256, fromCache, schemaVersion}` (`loadDemo` — warms the cache) |
| GET | `/v1/demos/{id}/overview` | — | `Overview` (map, teams, top streaks, top powerups, playerUserIDs) |
| GET | `/v1/demos/{id}/demoinfo` | — | `result.DemoInfoResult` (KTX scoreboard — per-player weapon accuracy, kills/deaths/TK, damage, sprees, item counts, RL/LG transfers) |
| GET | `/v1/demos/{id}/metadata` | — | `result.MetadataResult` (full fullserverinfo cvars + KTX match settings: timelimit, fraglimit, spawnmodel, antilag, midair, instagib, …) |
| GET | `/v1/demos/{id}/frags` | `players`, `weapon` | `result.FragResult` (totalFrags + byPlayer + byWeapon + full kill log) |
| GET | `/v1/demos/{id}/loc-graph` | — | `result.LocGraphResult` (per-map loc adjacency + edge weights) |
| GET | `/v1/demos/{id}/chat` | `from`, `to`, `players`, `types` | `[]result.MatchEvent` (chat + teamsay only; types defaults to both) |
| GET | `/v1/demos/{id}/backpacks` | `players`, `weapon` | `[]result.BackpackDrop` (RL/LG drops via `//ktx drop`) |
| GET | `/v1/demos/{id}/items` | `items`, `players`, `kinds` | `result.ItemsResult` (per-item pickup/respawn timeline) |
| GET | `/v1/demos/{id}/weapon-pickups` | `players`, `weapon`, `source` | `[]result.WeaponPickup` (kills-before-next-death; joins to backpacks via `backpackEnt`) |
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
- Empty defaults match the view function defaults — see
  [`mvd-analytics/RESULT_SCHEMA.md`](../mvd-analytics/RESULT_SCHEMA.md)
  for the field-code vocabulary and the reducer registry.

### Response shapes

The view-shaped endpoints (`/buckets`, `/events`, `/stream-slice`,
`/state-at`, `/loc-trails`, `/region-control`) return the
corresponding Go types from
[`mvd-analytics/view`](../mvd-analytics/view) — see RESULT_SCHEMA.md
for `BucketsView`, `EventsView`, `StreamSliceView`, `StateAtView`,
`LocTrailsView`, and `result.RegionControlResult`. They're produced
identically whether reached via the WASM bridge, the CLI, or this
REST surface.

Two API-specific shapes are documented inline below.

#### `loadDemo` — `POST /v1/demos/{id}`

```jsonc
{
  "demoId":        "sha:abc...",      // canonical id for subsequent calls
  "sha256":        "abc...",
  "fromCache":     true,              // false on first call for an uncached demo
  "schemaVersion": 9
}
```

Idempotent. Slow only on cold demos (the hub fetch + parse). Warm
cache returns sub-millisecond.

#### `getOverview` — `GET /v1/demos/{id}/overview`

Curated summary cheap enough to call as a first step after
`loadDemo`. Composed in
[`overview.go`](overview.go) from `result.Match`, `result.Frags`,
`result.Metadata.MatchSettings`, and
`result.TimelineAnalysis.{FragStreaks,PowerupEvents,LocTable,RegionControl}` —
no new analytics, just a shape that surfaces "what was this match"
in one round-trip.

```jsonc
{
  "schemaVersion": 9,
  "filePath":      "abc....mvd.gz",
  "map":           "dm6",
  "gameDir":       "qw",
  "mode":          "4on4",                          // omitempty
  "matchtag":      "qwsl",                          // omitempty
  "duration":      613.4,                           // seconds, parser-derived
  "matchStart":    0,                               // match-relative seconds
  "matchEnd":      613.4,
  "teams": [                                        // omitempty, sorted by frags desc
    { "name": "Die",   "frags": 89 },
    { "name": "okkis", "frags": 76 }
  ],
  "players": [                                      // sorted by frags desc
    { "name": "bps",    "team": "Die",   "frags": 35 },
    { "name": "valla",  "team": "okkis", "frags": 30 }
    // ...
  ],
  "topStreaks": [                                   // omitempty, ≤ 5, sorted by length desc
    { "player": "bps",    "team": "Die",   "weapon": "rl", "length": 7, "start": 234.1, "duration": 18.3 }
    // ...
  ],
  "topPowerups": [                                  // omitempty, ≤ 5, sorted by frags desc
    { "player": "milton", "team": "Die",   "type":   "quad", "start": 412.0, "duration": 29.7, "frags": 5 }
    // ...
  ],
  "locCount":         47,                           // len(TimelineAnalysis.LocTable)
  "hasRegionControl": true,                         // true if /region-control will succeed
  "playerUserIDs":    { "bps": 123, "valla": 456 } // omitempty — for hub.quakeworld.nu/games/<gameId>?track=<userId>
}
```

The top-streaks / top-powerups arrays are capped at 5 each — for the
full lists, walk `TimelineAnalysisResult.FragStreaks` /
`PowerupEvents` via the standard view surface.

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
- `422 demoinfo_unavailable` — demo has no KTX demoinfo block (non-KTX server or aborted match)
- `422 metadata_unavailable` — demo has no metadata (no fullserverinfo / no countdown centerprint)
- `422 frags_unavailable` — demo has no frag log
- `422 locgraph_unavailable` — demo has no loc graph (probably no position track)
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
# {"ok":true,"schemaVersion":9}

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
