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

Eight tools, mirroring `mvd-api` 1:1. Inputs are typed Go structs with
JSON-Schema inference; outputs are passed through verbatim as opaque
JSON (so this shim doesn't need to track `mvd-analytics/view` types).

| Tool | Backing endpoint |
|---|---|
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
`mvd-api` error message in `TextContent`. The model can read them and
recover (e.g. by calling `loadDemo` first).

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

## Module dependencies

```
github.com/modelcontextprotocol/go-sdk v1.6.0
```

That's it. No `mvd-analytics`, no `mvd-api`, no parser. Just the MCP
SDK and stdlib (`net/http`, `encoding/json`, `log/slog`, etc.).
