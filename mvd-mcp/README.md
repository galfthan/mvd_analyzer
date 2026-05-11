# mvd-mcp — stdio MCP shim for QuakeWorld demo analytics

`mvd-mcp` is a small (~5 MB) stdio MCP server that forwards every tool
call as an HTTP request to a running [`mvd-api`](../mvd-api/README.md).
It carries no analytics code of its own — the binary is a wire-protocol
shim, and the response shapes are owned by `mvd-api`.

Why split it from `mvd-api`?

- **Distribution.** End-users (Claude Desktop, Cursor, Claude Code)
  install one tiny binary; the heavy parser + cache lives on the
  server. The bundled-binary version was ~15 MB; this one is ~5 MB.
- **Stability.** `mvd-mcp` only depends on the REST wire contract.
  Analytics-side refactors don't force a shim release.
- **Future-extractable.** No `mvd-analytics` import — this module
  can be moved to its own repo when there's demand.

## Usage

```
mvd-mcp -api URL [-label TAG] [-timeout SECONDS]
mvd-mcp version
```

| Flag | Default | Description |
|---|---|---|
| `-api`      | (required) | Base URL of a running `mvd-api` (e.g. `https://mvd-api.example.com` or `http://localhost:8080`) |
| `-label`    | `""`        | Non-secret request-source tag forwarded as `Authorization: Bearer <label>`. Used for access-log analytics on the API side. |
| `-timeout`  | `60`        | Per-request HTTP timeout in seconds |

## Tool surface

Nine tools. Inputs are typed Go structs with JSON-Schema inference;
outputs are passed through as opaque JSON (the shim doesn't need to
track `mvd-analytics/view` types).

| Tool | Backing endpoint |
|---|---|
| `searchGames(players, teams, map, mode, matchtag, from, to, limit, offset)` | hub.quakeworld.nu Supabase — **not** mvd-api |
| `loadDemo(gameId or sha256)` | `POST /v1/demos/{id}` |
| `getOverview(demoId)` | `GET /v1/demos/{id}/overview` |
| `getBuckets(demoId, windowMs, ...)` | `GET /v1/demos/{id}/buckets` |
| `getEvents(demoId, types, ...)` | `GET /v1/demos/{id}/events` |
| `getStreamSlice(demoId, from, to, fields, ...)` | `GET /v1/demos/{id}/stream-slice` |
| `getStateAt(demoId, time, fields, ...)` | `GET /v1/demos/{id}/state-at` |
| `getLocTrails(demoId, minDwellMs, ...)` | `GET /v1/demos/{id}/loc-trails` |
| `getRegionControl(demoId, windowMs)` | `GET /v1/demos/{id}/region-control` |

`demoId` is the string returned by `loadDemo` (`sha:HEX`) or any
`gameId:NNNN` reference.

Tool errors come back as MCP `isError: true` results with the
upstream error message in `TextContent`. The model can read them and
recover (e.g. by calling `loadDemo` first).

### Why search bypasses mvd-api

Discovery (finding demos by player names, teams, map, etc.) is
hub.quakeworld.nu's job — `mvd-mcp` queries its public Supabase
endpoint directly, the same way the web frontend does. `mvd-api` is
narrowly responsible for "given a known demoId, fetch the bytes,
parse, cache, and serve analytics views." We don't shadow-host hub
search.

The Supabase anon key is public (shipped in the web bundle) and the
request shape mirrors the web's exactly, so there's no second source
of truth for the search semantics.

## Local MCP

The shim has no local-cache mode. For local MCP, run `mvd-api` on
`localhost` and point the shim at it:

```bash
mvd-api -addr :8080 -cache-dir ~/.cache/mvd-api &
mvd-mcp -api http://localhost:8080 -label local-mcp
```

Two binaries, ~zero startup cost. The deliberate trade-off vs. a
bundled binary is that the shim stays tiny and the wire contract
stays clean.

## Client integration

See [`CLAUDE_DESKTOP.md`](CLAUDE_DESKTOP.md) for copy-paste config
snippets for Claude Desktop, Claude Code, and Cursor, on Windows /
macOS / Linux.

## Build

```bash
make build-mcp                              # host platform
make build-mcp-windows                      # dist/mvd-mcp-windows-amd64.exe
make build-mcp-darwin                       # dist/mvd-mcp-darwin-{amd64,arm64}
make build-mcp-linux                        # dist/mvd-mcp-linux-amd64
make build-all-platforms                    # everything above + mvd-api targets
```

## Typical session shape

1. `searchGames({player: "bps", map: "dm6"})` → list of recent
   matches with rosters, scores, dates — directly from the hub. Cheap.
   No `mvd-api` round-trip; agent can filter / rank from the rows.
2. `loadDemo({gameId: 12345})` → tells `mvd-api` to fetch + parse +
   cache. Slow only on cold demos.
3. `getOverview` / `getBuckets` / `getStateAt` / ... → analytics for
   the chosen demo. Fast on warm cache.

If the answer is in the search-result rows alone (e.g. "what was
the score?"), the agent should stop there — no need to parse.

## Module dependencies

```
github.com/modelcontextprotocol/go-sdk v1.6.0
```

That's it. No `mvd-analytics`, no `mvd-api`, no parser. Just the MCP
SDK and stdlib (`net/http`, `encoding/json`, `log/slog`, etc.).
