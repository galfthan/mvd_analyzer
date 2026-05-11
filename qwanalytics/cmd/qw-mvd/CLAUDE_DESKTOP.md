# Claude Desktop integration

Two ways to wire `qw-mvd` into Claude Desktop / Cursor / Claude Code:

1. **Proxy mode (recommended).** Run `qw-mvd mcp -api <hosted-url>`
   locally; the hosted REST server does the parsing and caching.
   You only download a tiny stdio binary; demos are processed once
   per the host.
2. **Local mode.** Run `qw-mvd mcp` locally; it parses + caches
   on your machine. Useful when there's no hosted service to point
   at, or for offline use after the cache is warm.

## Configuration file

Claude Desktop reads `claude_desktop_config.json` at:

| OS | Path |
|---|---|
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

Add a `mcpServers.qw-mvd` entry. Restart Claude Desktop after editing.

### Proxy mode (Windows)

```json
{
  "mcpServers": {
    "qw-mvd": {
      "command": "C:\\Tools\\qw-mvd.exe",
      "args": [
        "mcp",
        "-api", "https://qw-mvd.example.com",
        "-label", "mcp-claude"
      ]
    }
  }
}
```

### Proxy mode (macOS / Linux)

```json
{
  "mcpServers": {
    "qw-mvd": {
      "command": "/usr/local/bin/qw-mvd",
      "args": [
        "mcp",
        "-api", "https://qw-mvd.example.com",
        "-label", "mcp-claude"
      ]
    }
  }
}
```

### Local mode

```json
{
  "mcpServers": {
    "qw-mvd": {
      "command": "/usr/local/bin/qw-mvd",
      "args": ["mcp", "-cache-dir", "/var/lib/qw-mvd"]
    }
  }
}
```

## Where to get the binary

Cross-compiled binaries for Linux, macOS (Intel + Apple Silicon), and
Windows live in `dist/` after running `make build-mvd-all` from the
mvd-analyzer repo root. Once a release process is in place these will
be attached to GitHub Releases.

On Windows, unsigned `.exe` files trigger a SmartScreen warning on
first run. Right-click → Properties → Unblock, or click "More info →
Run anyway" in the dialog. Code-signing is a follow-up; for a community
tool the warning is acceptable.

On macOS, Gatekeeper will refuse to run an unsigned binary. The same
right-click → Open workaround applies, or `xattr -d com.apple.quarantine
qw-mvd-darwin-arm64`.

## Smoke test

After Claude Desktop restarts, the tool list should include the eight
qw-mvd tools (`loadDemo`, `getOverview`, `getBuckets`, `getEvents`,
`getStreamSlice`, `getStateAt`, `getLocTrails`, `getRegionControl`).

Ask the assistant something like:

> Load hub game 12345 and tell me the top three frag streaks.

It should call `loadDemo` first, then `getOverview` (which surfaces
`topStreaks`).
