# mvd-mcp — MCP shim for QuakeWorld demo analytics

`mvd-mcp` is a small (~5 MB) MCP server that forwards every tool call as
an HTTP request to a running [`mvd-api`](../mvd-api/README.md). It
carries no analytics code of its own — the binary is a wire-protocol
shim, and the response shapes are owned by `mvd-api`.

It has two transports:

- **stdio** (default) — one process per client, launched by the client
  (Claude Desktop, Cursor, Claude Code). This is the local mode.
- **streamable HTTP** (`-http ADDR`) — a long-lived server for hosted
  use. MCP itself is unauthenticated; the shim authenticates to mvd-api
  with its own service key. See [Hosted / HTTP mode](#hosted--http-mode)
  below.

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
mvd-mcp -api URL [-label TAG] [-timeout SECONDS]       # stdio (default)
mvd-mcp -http ADDR -api URL [-timeout SECONDS]         # streamable HTTP
mvd-mcp version
```

| Flag | Default | Description |
|---|---|---|
| `-api`      | (required) | Base URL of a running `mvd-api` (e.g. `https://mvd-api.example.com` or `http://localhost:8080`) |
| `-http`     | `""`        | Serve MCP over streamable HTTP on `ADDR` (e.g. `:8081`) instead of stdio. See [Hosted / HTTP mode](#hosted--http-mode). Mutually exclusive with stdio. |
| `-label`    | `""`        | **stdio only.** Non-secret request-source tag forwarded as `Authorization: Bearer <label>`. Used for access-log analytics on the API side. Ignored in `-http` mode, and superseded by `MVD_API_KEY` when that is set. |
| `-timeout`  | `60`        | Per-request HTTP timeout in seconds |

| Env var | Description |
|---|---|
| `MVD_API_KEY` | API key forwarded as `Authorization: Bearer` on every proxied `mvd-api` call. Required (as an operator-issued **service** key) when the target `mvd-api` runs with `-auth-dir`; unnecessary against a local no-auth `mvd-api`. An env var, not a flag, so the secret never shows in `ps`. This is the shim's **only** configuration secret — it needs no hub credentials, since `searchGames` proxies to `mvd-api` too. |

## Tool surface

Twenty-six tools. Inputs are typed Go structs with JSON-Schema inference
(this file); outputs are passed through as opaque JSON — see
[`../mvd-api/README.md`](../mvd-api/README.md) for the response shape
of each per-demo endpoint, and
[`../mvd-analytics/RESULT_SCHEMA.md`](../mvd-analytics/RESULT_SCHEMA.md)
for the view types (`BucketsView`, `EventsView`, etc.), the field-code
vocabulary, and the reducer registry.

**Stability.** The tool surface is covered by the API's compatibility
policy ([`mvd-api/API.md` §2.7](../mvd-api/API.md#27-api-versioning-and-stability)),
which is **not yet a freeze**: most change is additive — new tools,
parameters and result fields — but a schema upgrade can still withdraw or
reshape a documented result field, as v70 did to `getOverview`. The shim and
`mvd-api` deploy in lockstep, so a tool and the route behind it always change
together, and every change is written up against its `schemaVersion` in
RELEASE_NOTES. Where a tool's default differs from the
REST default because an agent-facing default is more useful (e.g.
`getLocTrails` defaults `minDwellMs` to 250, REST to 0), the tool
description says so.

| Tool | Backing |
|---|---|
| `searchGames` | `mvd-api` `GET /v1/games/search` |
| `loadDemo` | `mvd-api` `POST /v1/demos/{id}` |
| `getOverview` | `mvd-api` `GET /v1/demos/{id}/overview` |
| `getDemoInfo` | `mvd-api` `GET /v1/demos/{id}/demoinfo` |
| `getPlayerStats` | `mvd-api` `GET /v1/demos/{id}/player-stats` |
| `getMetadata` | `mvd-api` `GET /v1/demos/{id}/metadata` |
| `getFrags` | `mvd-api` `GET /v1/demos/{id}/frags` |
| `getDamage` | `mvd-api` `GET /v1/demos/{id}/damage` |
| `getAim` | `mvd-api` `GET /v1/demos/{id}/aim` |
| `getLocGraph` | `mvd-api` `GET /v1/demos/{id}/loc-graph` |
| `getChat` | `mvd-api` `GET /v1/demos/{id}/chat` |
| `getBackpacks` | `mvd-api` `GET /v1/demos/{id}/backpacks` |
| `getItems` | `mvd-api` `GET /v1/demos/{id}/items` |
| `getMapEntitiesByMap` | `mvd-api` `GET /v1/maps/{map}/entities` |
| `getWeaponPickups` | `mvd-api` `GET /v1/demos/{id}/weapon-pickups` |
| `getBuckets` | `mvd-api` `GET /v1/demos/{id}/buckets` |
| `getEvents` | `mvd-api` `GET /v1/demos/{id}/events` |
| `getStreamSlice` | `mvd-api` `GET /v1/demos/{id}/stream-slice` |
| `getPointEffects` | `mvd-api` `GET /v1/demos/{id}/streams/point-effects` |
| `getStateAt` | `mvd-api` `GET /v1/demos/{id}/state-at` |
| `getLocTrails` | `mvd-api` `GET /v1/demos/{id}/loc-trails` |
| `getLocTable` | `mvd-api` `GET /v1/demos/{id}/loc-table` |
| `getRegionControl` | `mvd-api` `GET /v1/demos/{id}/region-control` |
| `getTopWindows` | `mvd-api` `GET /v1/demos/{id}/top-windows` |
| `getTopKills` | `mvd-api` `GET /v1/demos/{id}/top-kills` |
| `getLives` | `mvd-api` `GET /v1/demos/{id}/lives` |
| `listArtifacts` | `mvd-api` `GET /v1/artifacts` |
| `getArtifact` | `mvd-api` `GET /v1/demos/{id}/artifacts/{name}` |

`demoId` is the string returned by `loadDemo` (`sha:HEX`) or any
`gameId:NNNN` reference.

### Curated tools vs. the generic artifact pair

Every tool above except the last two is **curated**: each wraps one
analytics section with a hand-written description and (where useful)
`players`/`weapons`/window filters — that ergonomics is the product
surface, and it stays. The last two are the **generic** DAG accessor:
`listArtifacts` returns the fetchable-artifact catalog — servable
artifacts only, trimmed to `{name, resultKey, cost, lazy, description}`
at the MCP layer (the full DAG manifest with `requires`/`provides`
edges and internal nodes stays on REST `/v1/artifacts` + `/v1/graph`) —
and `getArtifact` fetches one servable artifact by name. Heed size
notes in descriptions: `timeline` is one of the largest sections;
prefer the windowed views (getEvents/getBuckets/getRegionControl). This is how the automatic API
surface (plan §7) is realized here: a **new** analytics artifact becomes
reachable through `getArtifact` with **zero** new hand-written tools,
while the common sections keep their rich curated tools. Prefer the
curated tool when one exists (it filters and documents); reach for
`getArtifact` for artifacts that have no dedicated tool. `getArtifact`
takes no filters — parameterised reads are the curated view tools.

Tool errors come back as MCP `isError: true` results with the
upstream error message in `TextContent`. The model can read them and
recover (e.g. by calling `loadDemo` first).

### REST endpoints without an MCP tool

A few `mvd-api` REST endpoints have **no** curated MCP tool yet:
`/los`, `/shots`, `/streams/*`, and `/airgibs`. This asymmetry is
deliberate for now — those views are large or specialised, and adding
tools is deferred (they can still be fetched at the REST layer, and
`/los` is reachable generically via `getArtifact name=los`). See
[`../mvd-api/API.md`](../mvd-api/API.md) for the full endpoint list, or
the machine-readable OpenAPI spec the server itself serves at
`/openapi.yaml` (browsable at `/docs`).

## Hosted / HTTP mode

`mvd-mcp -http :8081 -api http://localhost:8080` serves MCP over
**streamable HTTP** instead of stdio, for hosting the service on the
public internet. This is the transport a remote client (Claude Code
`--transport http`, or any streamable-HTTP MCP client) connects to.

### Auth model

**MCP requests need no authentication.** Requiring a per-request bearer
key proved too cumbersome for the clients this transport exists for —
web AI chat connectors (claude.ai, ChatGPT, …) that can't easily attach
custom headers — and every tool is read-only over public demo data. API
keys remain the model for the REST API, which is aimed at services and
bulk integrations.

Upstream, the shim authenticates *itself*: the operator issues one
**service** key (`mvd-api keys issue -service -note mvd-mcp`) and hands
it to the shim via the `MVD_API_KEY` env var. Every proxied REST call
forwards it as `Authorization: Bearer`, so `mvd-api` keeps full key
auth, and its per-key rate limit on that one key acts as the global
throttle on anonymous MCP traffic.

Two escape hatches:

- A request that **does** carry `Authorization: Bearer qwmvd_…` has that
  key forwarded upstream instead of the service key — per-key
  attribution and rate limiting still work for keyed clients.
- A bearer that is *not* a `qwmvd_` key (e.g. an OAuth token a chat
  platform attaches on its own) is ignored rather than breaking the
  session.

The server also exposes a `GET /healthz` for liveness probes. The MCP
handler is mounted at `/mcp` (and `/mcp/`).

`-label` has no effect in HTTP mode; mvd-mcp logs a warning if it is set.

### Client config

Claude Code:

```sh
claude mcp add --transport http mvdanalyzer https://mvdanalyzer.com/mcp
```

JSON config form (e.g. `.mcp.json`):

```json
{
  "mcpServers": {
    "mvdanalyzer": {
      "url": "https://mvdanalyzer.com/mcp"
    }
  }
}
```

Web chat connectors (claude.ai custom connectors and the like) just take
the URL `https://mvdanalyzer.com/mcp` — no headers or OAuth needed.

For the full deployment (Caddy TLS, systemd units, provisioning runbook)
see [`../deploy/README.md`](../deploy/README.md).

### stdio vs. HTTP

| | stdio (default) | HTTP (`-http`) |
|---|---|---|
| Transport | stdin/stdout | streamable HTTP |
| Lifecycle | one process per client | one long-lived server |
| MCP auth | none (local process) | none |
| Upstream auth | `MVD_API_KEY`, else `-label` (non-secret tag) | `MVD_API_KEY` service key; per-request `qwmvd_` bearer overrides |
| Use | local (Desktop/Cursor/Code) | hosted / remote |

### Input schemas

The Go types below are what `registerTools` declares; the MCP SDK
infers their JSON Schemas from struct tags and exposes them via
`tools/list`. Source of truth:
[`mcp_backend.go`](mcp_backend.go).

Every tool that maps to a demo endpoint (getOverview, getFrags,
getDamage, getAim, getChat, getBackpacks, getItems, getWeaponPickups,
getBuckets, getRegionControl, getEvents, getStreamSlice, getPointEffects,
getStateAt, getLocTrails, getLocGraph, getTopWindows, getTopKills,
getLives) and carries
match-position time echoes a
top-level constant `timeUnit` (`"ms"`) — every time value in the API is
int32 ms (pure-ms model). getLocGraph is on that list because
its node weights are int32-ms durations, and getArtifact responses carry
the echo for every time-bearing section too. The exceptions:
getDemoInfo is the KTX island (mixed KTX-native units), and
getMetadata, getLocTable and getMapEntities have no
match-position time to unit in the first place. There is no
unit-selection input. Field-name conventions follow the dense/sparse
rule: sparse event lists and singleton timestamps use `time`,
sample-rate-scaled dense arrays use `t` — both int32 ms — and descriptive
names (`startTime`, `endTime`, `nextDeathTime`, …) are int32 ms too, the
unit named by the constant `timeUnit` echo. See
[mvd-api/API.md §2.1](../mvd-api/API.md) for the per-endpoint values.

#### `searchGames(...)`

All fields optional; an empty filter returns the most recent matches.

| Param | Type | Default | Description |
|---|---|---|---|
| `players`  | `string[]` | — | Player names; FTS on `players_fts`, AND'd across multiple |
| `teams`    | `string[]` | — | Team names; `contains` on `team_names` |
| `map`      | `string`   | — | Map name, exact match (e.g. `dm6`) |
| `mode`     | `string`   | — | Game mode, exact match (`1on1`, `2on2`, `4on4`, `FFA`) |
| `matchtag` | `string`   | — | Tournament/event tag, case-insensitive substring (e.g. `qwsl`) |
| `from`     | `string`   | — | ISO date lower bound, inclusive (YYYY-MM-DD) |
| `to`       | `string`   | — | ISO date upper bound, inclusive (YYYY-MM-DD) |
| `limit`    | `int`      | 20 | Max rows per page; capped at 1000 (the hub's own page ceiling). Omit for the default; an explicit `0` is rejected `400 invalid_param` |
| `offset`   | `int`      | 0 | Pagination offset |
| `roster`   | `bool`     | `false` | `true` = verbatim hub rows with full roster detail (per-player `ping`, `color` arrays, `name_color`, `team_color`, `is_bot`). Default = compact rows: `players` projected to `{name, team, frags}`. |

Output: `{ limit, offset, count, total?, games: [row, ...] }`. `count`
is the rows in THIS page; `total` is all matching rows (from the
PostgREST `count=exact` preference — omitted if the hub doesn't report
it). Page with `limit`/`offset` until `offset+count >= total`. Row
fields: `id, timestamp, mode, matchtag, map, teams, players,
demo_sha256, demo_source_url` (`players` compacted unless
`roster: true`).

#### `loadDemo({gameId | sha256})`

Warms `mvd-api`'s cache for the demo and returns the canonical
`demoId` (`sha:HEX`). Idempotent — and **optional**: every analysis
tool accepts `gameId:N` directly and auto-loads on first use; loadDemo
just front-loads the parse cost.

| Param | Type | Description |
|---|---|---|
| `gameId` | `int`    | hub.quakeworld.nu game id |
| `sha256` | `string` | 64-char hex of a demo already in the local cache |

Exactly one of `gameId` / `sha256` must be set.

Output: `LoadDemoOutput` —
`{ demoId, sha256, fromCache, schemaVersion }`. The `demoId` is what
every subsequent per-demo tool expects.

#### `getOverview({demoId})`

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | `gameId:N` or `sha:HEX` |

Output: `Overview` —
see [`../mvd-api/README.md`](../mvd-api/README.md#rest-endpoints).
Carries the per-demo capability manifest (`available`), the
`timing.matchStart*` anchor family that answers "when was this match
played" (schema v72 — always read `matchStartConfidence` with the value),
and THREE signals about the health of the result, deliberately separate:
`errors` (analyzer level), `parseWarnings` (reader level, omitted on a
clean parse) and `noMatch` (schema v74) on the 2% of the archive that
yields no analyzable match. `noMatch` is present exactly when the `streams`
block is absent, and names the reason (`midMatchRecording` /
`matchStartUnannounced` / `noMatchDeclared` / `noPlayRecorded` /
`demoUnreadable`) with the wire evidence behind it, so "no match here" is
distinguishable from "the parse failed" without guessing. Read it FIRST —
it decides whether the other two describe a partial match or nothing at
all, and `demoUnreadable` is the one reason that means both. Such a demo
is not a failed parse: do not report it as one and do not retry it.

#### `getDemoInfo({demoId})`

KTX demoinfo blob pass-through — the authoritative scoreboard.

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | — |

Output: `result.DemoInfoResult`. Per-player `Weapons.<rl|lg|gl|ssg|ng>.acc`
(hit accuracy), `Weapons.<...>.kills`, `Items.<RA|YA|GA|MH|...>.count`,
`Dmg.taken/given`, `Spree.{quad,run,ring,pent}.{frags,duration}`,
RL/LG transfers, etc. Errors with `demoinfo_unavailable` (422) if the
demo has no KTX demoinfo block (rare; non-KTX or aborted matches).

#### `getPlayerStats({demoId, players?, teams?})`

The canonical per-player and per-team statistics — the one-call
scoreboard, available on **every** demo including old ones with no KTX
demoinfo block.

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | — |
| `players` | `string[]` | restrict to these players; also drops the team rows, which are whole-team sums |
| `teams` | `string[]` | restrict to these teams |

Output: `result.PlayerStatsResult` — per row `score` (corrected
frags/kills/deaths/suicides/teamKills + `efficiency`, plus `maxSpree` /
`maxQuadSpree`), `damage`, `accuracy`, `pickups.byKind`, and `hold`
(`weapons` / `armor` / `powerups`), plus a `window` carrying the
denominators (`matchMs` / `presentMs` / `aliveMs` / `deadMs`).

Every family carries `src` (`"derived"` | `"ktx"` |
`"derived:unbounded"` on damage | `"reconstructed"` on damage rebuilt
for a pre-instrumentation demo, and on `accuracy` whose `hits` come from
the aim reconstruction tier), with a `sources` roll-up — `getDemoInfo` stays the verbatim KTX block to diff against.
The response keeps the same shape regardless of demo age: on a demo with
no KTX block, `accuracy` is derived from the decoded fire stream — linked
to the wire damage log where the demo carries one (`src: "derived"`, on
KTX's own convention per weapon since schema v75), rebuilt from the
reconstruction where it does not (`src: "reconstructed"`, whose `sg`/`ssg`
count trigger pulls rather than KTX's pellets); read
`accuracy.byWeapon[].hitsConvention`, not `src`, before comparing across
demos, `damage.takenEnemy` / `takenToDie` come from the per-hit log, and
`login` from the `*auth` userinfo key. A value that cannot be measured
stays ABSENT rather than becoming a zero — notably
`accuracy.byWeapon[].hits` on a demo with no WIRE damage stream for the
weapons the aim reconstruction tier does not cover (see below).
`efficiency`, `shareAlive` and `shareMatch` are RATIOS in [0,1], not
percentages.

**Reconstructed accuracy hits.** On a demo whose damage section is
itself `reconstructed` (no wire `mvdhidden_dmgdone` stream),
`accuracy.byWeapon[].hits` is FILLED from the published aim
reconstruction tier — the same counts `getAim` publishes as
`weapons[].recon.hits` — instead of being withheld, and the family's
`src` becomes `"reconstructed"` to mark the evidence grade. Only the
weapons that tier validated carry a number: `lg`, `sg`, `ssg`, `axe`,
`rl`, `gl`. `ng` / `sng` keep `hits` ABSENT — the tier validated no nail
recovery, so the withhold inherits; read that absence as "not recovered
for this weapon", never as "no hits". A family whose weapons all fall
outside the tier stays `src: "derived"`, `attacks` being shot-derived
either way. Grade the numbers before diffing against KTX: on a
`reconstructed` family `lg` agrees to 0.9% in aggregate and so do `rl`
(1.25%, 46.5% of rows exact) and `gl` (3.55% aggregate but 89.6% of rows
exact, one-sided by design), because since schema v74 those two publish
KTX's OWN direct-impact count. On a `derived` family they do not — there
`hits` counts a fire that landed damage by any path and reads ~4x higher
on `rl`, ~1.5x on `gl`, against KTX's touch counter
(`ktx/src/weapons.c:994`, `:1329`). `attacks` matches KTX to the row on
every single-projectile weapon (98–100% exact).

**`hitsConvention` is the machine-readable form of that warning**, and
the field to gate a cross-demo comparison on — `src` states the evidence
GRADE and says nothing about what is counted. It sits on every weapon
that carries `hits`: `anyDamage` (a fire that landed damage by any path;
`lg` / `ng` / `sng` / `axe` on every source, plus `gl` on a `derived`
family — the wire cannot see the touch KTX counts there — and
`sg` / `ssg` on a `reconstructed` one), `directImpact` (the projectile
TOUCHED a player: KTX's `rl` / `gl`, a `reconstructed` family's `rl` /
`gl` since v74, and a `derived` family's `rl` since v75) or `pellets`
(KTX's `sg` / `ssg` and, since v75, a `derived` family's — the one
convention where `attacks` counts pellets too). Per WEAPON, because
one `src: "ktx"` row uses all three at once. Two rows are comparable
exactly when weapon AND convention match; absent beside a present `hits`
only on a `src: "mixed"` team row. Deriving KTX's own convention on an
old demo took two attempts: the wire damage log's splash flag reproduces
KTX's `rl` count on every row, the pre-instrumentation half carries no
such flag, and the first substitute — an explosion endpoint within 48
units — answered `gl` and not `rl` (+80%). v74 replaces it with the
flight's trajectory against the player hull plus the flat-110
direct-damage constant, at 1.25% / 3.55% aggregate (46.5% / 89.6% of
rows exact).

**Sprees.** `score.maxSpree` is the longest run of kills between deaths;
`score.maxQuadSpree` the longest run of kills made while holding the
quad (it resets on death *and* on a fresh quad pickup, mirroring KTX
`items.c:2180`). They are the derived equivalent of the KTX demoinfo
block's `spree.max` / `spree.quad`, which 54% of the archive has no
block to carry — always derived, never overlaid from KTX. They ride
`score.kills`' `killsMeasured` gate, i.e. they are present and absent
together with `kills` / `suicides` / `teamKills` / `efficiency` /
`byWeapon` / `byEnemyWeapon` / `byWeaponVsEnemyWeapon`, so a `0` inside a
present family is an observed zero. A team
row carries the BEST any member ran, never a sum. One deliberate
divergence from KTX: its increment gate is `strneq(attackerteam,
targteam) || !tp_num()` (`ktx/src/client.c:4865`), so wherever teamplay
is OFF — every duel, every FFA — a player's own SUICIDE bumps their
streak in the same call that latches it. Ours counts only the kills
`score.kills` counts, so a duel with self-kills reads exactly 1 lower
per affected streak. Withheld-and-compared against the verbatim KTX
block on 188 archive demos / 665 player rows: `maxQuadSpree` 99.8%
exact, `maxSpree` 92.9% overall, 96.5% on rows whose `kills` already
agrees with KTX, and 99.6% on rows where kills agree and the player
never suicided. The large disagreements have an observable signature:
`kills: 0` beside a large positive `frags` means the streak inherited
the kill side's gap and is unknown, not zero. A reconnect does NOT reset
the streak (KTX restores the whole stats struct from its ghost entity,
`ktx/src/client.c:1515`), and a mid-match team switch cannot happen —
KTX refuses one while a match is in progress.

**Possession time is unique to this tool.** KTX never writes weapon hold
time into the demoinfo block, and its armor hold time overcounts (the
clock keeps running after the armor is chewed to zero), so our armor
numbers read LOWER than a KTX end-of-match table by design.
`hold.armor.none` — time alive with no armor at all — has no KTX
equivalent.

Errors with `playerstats_unavailable` (422) only on a parse degraded to
no player streams; a missing KTX block is served normally.

#### `getMetadata({demoId})`

Server cvars + KTX match settings. Used to answer "what ruleset
was this played under".

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | — |

Output: `result.MetadataResult` — `serverInfo` (map of cvar →
value), `matchSettings` (mode, timelimit, fraglimit, antilag,
spawnmodel/spawnK, midair, instagib, overtime, powerups, vwep,
noweapon, matchtag, …), `countdownText` (raw KTX
countdown centerprint) and `finalScores` (KTX's `//finalscores`
end-of-match scoreline, verbatim: date without a year, mode, map and
the two sides — present on 64% of demos against demoinfo's 46%, and
counting ROUNDS rather than frags on Clan Arena / Wipeout).

#### `getFrags({demoId, ...})`

Frag aggregates + the full kill log. Cheaper than aggregating
`getEvents(types:["frag"])` client-side.

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `players`   | `string[]` | all | Restrict aggregates + log to entries involving these (killer OR victim) |
| `weapons`   | `string[]` | all | Restrict aggregates + log to these weapon codes (`rl`, `lg`, `gl`, `ssg`, `sng`, `ng`, `axe`, `sg`, …) |
| `startTime` | `integer` | match start | Window start, match-relative **milliseconds** (keep kills at `time ≥ startTime`) |
| `endTime`   | `integer` | match end | Window end, match-relative **milliseconds** (keep kills at `time ≤ endTime`) |
| `summary`   | `bool` | `false` | Return only aggregates, dropping the kill log. **Deliberately the opposite default from getDamage/getAim/getItems**: a kill log is one row per frag — small, and usually the point of the call. |

When any scoping filter (`players` / `weapons` / `startTime` / `endTime`) is
set, **every** aggregate is recomputed from the filtered kill log (consistent
with the entries shown); with none set the authoritative stored totals are
returned. Filtered aggregates are log-sourced and may differ slightly from the
unfiltered totals for reconnect / unresolved-name edge cases.

Output: `result.FragResult` —
`{ totalFrags, byPlayer: {name: {kills, deaths, byWeapon}}, byWeapon: {weapon: count}, frags: [{time, killer, victim, weapon, isSuicide, isTeamKill}, ...] }`.

#### `getDamage({demoId, ...})`

Per-hit damage aggregates + log — decoded from the wire KTX
`mvdhidden_dmgdone` stream (`source: "ktx"`), or rebuilt from
spectator-visible state on pre-instrumentation demos
(`source: "reconstructed"`; ~1% match-total estimates). Cheaper than
aggregating
`getEvents(types:["damage"])` client-side; use that for the raw
time-ordered per-hit log.

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `players`   | `string[]` | all | Restrict aggregates + log to entries involving these (attacker OR victim) |
| `weapons`   | `string[]` | all | Restrict to these **attacker** weapon codes (`rl`, `lg`, `gl`, `ssg`, `sng`, `sg`, `tele`, …) |
| `startTime` | `integer` | match start | Window start, match-relative **milliseconds** (keep hits at `time ≥ startTime`) |
| `endTime`   | `integer` | match end | Window end, match-relative **milliseconds** (keep hits at `time ≤ endTime`) |
| `summary`   | `bool` | **`true`** (MCP-only default) | Aggregates only, the big per-hit damage log dropped. Pass `false` for the full log (REST `/damage` defaults to the full log; the defaulted MCP response carries a `hint` field saying how to opt out). |

The damage output — the aggregates AND the per-hit `events` log — is
**match-only**: out-of-match (warmup / post-match) hits are dropped at the
source and never appear (schema v50).

When any scoping filter (`players` / `weapons` / `startTime` / `endTime`) is
set, **every** aggregate (`totalDamage`, `byPlayer`, `byWeapon`, `matrix`) is
recomputed from the filtered per-hit log — this also populates `matrix` /
`events` on filtered responses (previously null). Because `events` is
match-gated at the source, an all-players recompute reproduces the stored
totals exactly. With none set the authoritative stored totals are returned.

Output: `result.DamageResult` — `{ totalDamage, byWeapon, byPlayer: {name:
{given, taken, givenTeam, givenSelf, takenEnv, byWeapon, byWeaponTeam,
byWeaponSelf, enemyVsSg,
enemyVsMid, enemyVsLg, enemyVsRl, enemyVsBoth, ewep}}, matrix: [{attacker,
victim, damage, byWeapon}], events: [{time, attacker, victim, weapon,
damage, victimWep, ...}], scoreboard }`. The three per-weapon maps split
`given` / `givenTeam` / `givenSelf` by the **attacker's** weapon on the
same keys (telefrags and stomps are excluded from all three; `matrix` and
the `enemyVs*` buckets stay enemy-only). `byWeaponTeam` / `byWeaponSelf`
are `omitempty` but **measured whenever the damage family is present** —
an absent key means "dealt none with that weapon", never "not measured". **EWep** (= `enemyVsLg +
enemyVsRl + enemyVsBoth`) is damage dealt to enemies *holding* RL/LG,
keyed on the **victim's** inventory. Amounts are **unbound** (include
overkill; a telefrag reports 9999), so totals run higher than the KTX
scoreboard — see `scoreboard` for the cross-check.

#### `getAim({demoId, players?, startTime?, endTime?, summary?})`

Per-player aim analysis. Check the top-level `hitsMeasured` flag first:
when false (reconstructed/absent damage — most old demos) every
hit-derived counter is withheld rather than fabricated as zero, and
only shots, crosshair error and ramp timing remain. `hitsSource` says
which evidence there was (`ktx` / `reconstructed` / absent): on a
`reconstructed` demo the measured counters stay withheld, but a
recovered hit count is published separately as
`weapons[].recon.hits` — accuracy is `recon.hits / shots`, for
`lg`/`sg`/`ssg`/`axe` and (schema v74) `rl`/`gl`, never for `ng`/`sng`,
and never merged into `hits`. Start with
`players[].weapons` (per-weapon shots/hits, SG/SSG pellet stats +
full/partial/miss fires, RL/GL direct/splash/missed, the LG
miss/blocked/out-of-range whiff split); the
columnar `crosshair` (per-hitscan-fire angular error, normalized so ±1 =
the hitbox edge, with hit + attributed target) and `lgRamp` (per-LG-cell
hit vs ms since the shaft opened) blocks are large — reach for them only
when per-shot detail is needed.

**`summary: true` is the MCP-layer default** — it returns just the compact
per-player `weapons` aggregates and drops the large per-fire `crosshair` +
`lgRamp` sample arrays, which otherwise dominate the payload and can overflow
context. Pass `summary: false` for the full arrays (REST `/aim` defaults to
the full shape; the defaulted MCP response carries a `hint` field saying how
to opt out).

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | — |
| `players` | `string[]` | scope to these shooters. With no time window, selects their **match-wide** aim; with a window, restricts the recompute. |
| `startTime` | `integer` | window start, match-relative **milliseconds**. Setting a window **recomputes** aim over the shots in it, so every figure (weapons, crosshair, lgRamp) scopes to the window. |
| `endTime` | `integer` | window end, match-relative milliseconds. |
| `summary` | `bool` | **default `true` (MCP-only)**: only the `weapons` aggregates, the per-fire `crosshair`/`lgRamp` arrays dropped. Pass `false` for the full arrays. |

With no time window the **stored** match-wide aim is served (no recompute);
`players`/`summary` still apply. The `shots`/`damage` inputs aim derives from
are already match-only, so aim never includes warmup / post-match fires.

Output: `result.AimResult` — see
[RESULT_SCHEMA.md §AimResult](../mvd-analytics/RESULT_SCHEMA.md#aimresult-aim).
`mode` flags attribution quality per player: `duel` (exact, one enemy) or
`team` (nearest-crosshair-enemy heuristic). The first call on a demo may be
slow — the API builds the projectile/beam streams on first request.

#### `getLocGraph({demoId})`

Per-map adjacency graph of named locations.

| Param | Type | Description |
|---|---|---|
| `demoId` | `string` (required) | — |

Output: `result.LocGraphResult` —
`{ locs: [{ name, x, y, z, ... }, ...], edges: [{ from, to, weight, ... }, ...] }`.
Useful for movement-pattern reasoning ("what's adjacent to RA?",
"which paths connect quad to RL?").

#### `getChat({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `startTime` | `integer` | match start | Window start, match-relative milliseconds |
| `endTime`   | `integer` | match end | Window end, match-relative milliseconds |
| `players`   | `string[]` | all | Restrict to these speakers |
| `types`     | `string[]` | `["chat","teamsay"]` | Narrow to one of the two |

Output: `{ messages: []result.MatchEvent }` — each entry has `time`,
`type` (`chat` or `teamsay`), `player`, `team`, `message` (raw with
ezQuake markup), `messageClean` (markup stripped). Cleaner shape than
`getEvents(types:["chat"])` when you only want chat. (mvd-api returns a
`{timeUnit, messages}` envelope; the MCP tool passes it through.)

#### `getBackpacks({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`  | `string` (required) | — | — |
| `players` | `string[]` | all | Restrict to drops by these dropper names |
| `weapons` | `string[]` | both | Dropped-weapon codes (`rl`, `lg`); forwarded as a CSV set, matching REST `/backpacks` |
| `startTime`/`endTime` | `integer` | full match | Match-relative **milliseconds**; windows the drop time |

Output: `{ backpacks: []result.BackpackDrop }` — each entry has `time`,
`player` (dropper), `team`, `weapon` (`rl`/`lg`), `origin` (XYZ), `loc`
(resolved name), `entNum` (server edict) and `source` (schema v72); a
`reconstructed` row additionally carries `fate` and its companions below,
and a `ktx` row carries `fate: "expired"` when KTX announced the timeout.
(REST returns a `{timeUnit, backpacks}` envelope; the MCP tool passes it
through.)

`source` is `ktx` on demos that carried KTX's `//ktx drop` hint (KTX
≥ 1.38, 49% of the archive) and `reconstructed` on older demos, where the
pipeline replays KTX's own `DropBackpack` rule instead — validated at
99.97% precision and recall against the hints. **The two provenances carry
the pickup side differently**:

- A `ktx` row's `entNum` is the hinted edict and joins to
  `weapon-pickups[].backpackEnt`, which is where its pickup lives.
  `picker` / `pickerTeam` / `pickupTime` are absent. `fate` appears only
  as `expired`, and only when KTX announced the 120 s removal in its third
  backpack directive `//ktx expire` — the one statement the pickup join
  cannot make. An ABSENT `fate` on a `ktx` row means "ask
  `getWeaponPickups`", never "nobody took it": across the archive only
  half the drops with no pickup row carry the expiry hint, the rest being
  packs the recording ended on top of.
- A `reconstructed` row has no pickup hint to join to, so the pickup side
  is on the row itself (schema v72): `fate` (`picked` / `expired` /
  `unobserved`), plus `pickupTime` and `picker` / `pickerTeam` when the
  wire named exactly one player, read off the bound backpack-entity track.
  Its `entNum` is that bound entity — no longer 0, but still joining to no
  pickup row. Measured against the `//ktx bp` hints with both hints
  withheld: 100% precision, 96.1% recall on picked-vs-not, 99.98% of named
  pickers correct. Read `unobserved` as "the wire did not answer", not as
  "nobody took it"; a `picked` row with no `picker` means two players were
  on the pack and nothing separated them.

An absent section is AMBIGUOUS: it means either that no RL/LG pack dropped
or that the pass stood down rather than guess, and the wire shape does not
distinguish the two. `origin` is the pack's own position — the victim's
origin less the 24 units KTX drops it by (`ktx/src/items.c:2703-2704`) —
on both provenances.

#### `getItems({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`  | `string` (required) | — | — |
| `items`   | `string[]` | all | Item name or kind token, case-insensitive. A kind matches every instance of a type (`YA` → `ya_1`, `ya_2`; `RA`; `MH`; `Quad`; `Pent`; `Ring`; `RL`; `LG`; `GL`; `SSG`; `SNG`; `NG`); a suffixed name matches one instance (`ya_1`). |
| `players` | `string[]` | all | Restrict phases to those taken by these names; phases with no `takenBy` survive |
| `kinds`   | `string[]` | all | Category, case-insensitive: `armor`, `mega`, `health`, `powerup`, `weapon`, `ammo`. A raw kind token (`ra`, `quad`, …) also matches. |
| `startTime`/`endTime` | `integer` | full match | Match-relative **milliseconds**. Timeline mode keeps phases **overlapping** the window; summary mode counts takes **inside** it. `endTime: 60000` = the opening minute. |
| `summary` | `bool` | **`true`** (MCP-only default) | Per-item take aggregates `{takenCount, byPlayer, firstTake}` instead of the full phase timeline. Pass `false` for every phase (REST `/items` defaults to the timeline; the defaulted MCP response carries a `hint` field saying how to opt out). |

Summary output: `{ items: [{ name, kind, entNum, loc?, takenCount,
byPlayer?: {name: n}, firstTake?: { time, takenBy?, team? } }] }` — `time`
in match-relative milliseconds. The one-call shape for "who took which YA, and
who got there first".

Timeline output (`summary: false`): `result.ItemsResult` —
`{ items: [{ name, kind, entNum, x, y, z, loc, phases: [...] }, ...] }`.
`name` is unique per item (suffixed when a map has several of a kind:
`ya_1`, `ya_2`, `mh_1`); `kind` is the raw token (`ra`/`ya`/`mh`/`quad`/
`rl`/…). Each phase: `availableFrom`, `takenAt`, `takenBy`, `team`,
`respawnAt`.

#### `getWeaponPickups({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`  | `string` (required) | — | — |
| `players` | `string[]` | all | Restrict to picks by these names |
| `weapons` | `string[]` | all | `rl`, `lg`, `gl`, `ssg`, `sng`, `ng` |
| `source`  | `string`   | both | `world` (spawner) or `backpack` (RL/LG drop) |
| `startTime`/`endTime` | `integer` | full match | Match-relative **milliseconds**; windows the pickup time |

Output: `{ pickups: []result.WeaponPickup }` — each entry has `time`,
`player`, `team`, `weapon`, `source`, `hadBefore`, `kills` (before
picker's next death), `nextDeathTime`, plus for backpack pickups
`backpackEnt`, `dropper`, `dropperTeam`, `dropTime`. Joins to
`getBackpacks` via `backpackEnt` == `backpacks[].entNum`. (REST returns a
`{timeUnit, pickups}` envelope; the MCP tool passes it through.)

#### `getBuckets({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`      | `string` (required) | — | — |
| `windowMs`    | `int`     | **5000** (MCP default) | Bucket size in ms. The REST API itself defaults to 50 — the MCP proxy injects 5000 when the caller omits it: 50 ms emits ~24K buckets per match and even 1 s is ~1200 per field per player, while 5 s resolves the trend/control questions a bucketed timeline answers (a quad run is 30 s). Pass an explicit value (1000, 50, …) to override either way; for instants use getStateAt, for exact events getEvents. |
| `startTime`   | `integer` | match start | Window start, match-relative milliseconds |
| `endTime`     | `integer` | match end | Window end, match-relative milliseconds |
| `players`     | `string[]` | all | Restrict to these player names |
| `fields`      | `string[]` | all standard | Field codes — see RESULT_SCHEMA.md |
| `reducers`    | `{[code]: name}` | per-field defaults | Reducer-name override per field |
| `includeTeam` | `bool`    | `false` | Also emit per-team aggregates per bucket |
| `loc`         | `string`  | `name` | Ignored for `layout=column` (always the raw `li` index + a `locTable` legend in the envelope to decode it — no `getLocTable` call needed) |
| `layout`      | `string`  | **`column`** | `column` = compact column-major `ColumnarBuckets` (one array per `(player,field)`, `time(i)=start+i*windowMs`, booleans 0/1); `row` = one self-describing object per bucket. Prefer column for series/trend reads; use `getStateAt` for snapshots. |

Output: `view.ColumnarBuckets` (default) or `view.BucketsView` (`layout=row`)
— see [RESULT_SCHEMA.md → Buckets](../mvd-analytics/RESULT_SCHEMA.md#buckets).

#### `getEvents({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `startTime` | `integer` | match start | Match-relative milliseconds |
| `endTime`   | `integer` | match end | Match-relative milliseconds |
| `players`   | `string[]` | all | — |
| `types`     | `string[]` | discrete-event default set | `frag, powerup, streak, spawn, death, weapon, item, chat, pickup, demomark, airgib, pause` (default), opt-in: `loc, health, armor, damage, telefrag, stomp` |

Output: `view.EventsView` —
`{ events: [{ time, type, player, detail }, …] }`. Per-type `detail`
keys are in RESULT_SCHEMA.md.

#### `getStreamSlice({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `startTime` | `integer` | — | **At least one of startTime/endTime is required at the MCP layer** — an unwindowed slice is native-rate entries for the whole match, the biggest payload this service can emit. Keep windows tens of thousands of ms (tens of seconds). Match-relative milliseconds. (REST `/stream-slice` stays unwindowed.) |
| `endTime`   | `integer` | — | See `startTime`. Match-relative milliseconds. |
| `players`   | `string[]` | all | — |
| `fields`    | `string[]` | all standard | — |

Output: `view.StreamSliceView`. Per-player change-stream entries
inside the window (carry-forward entry prepended at `startTime`;
intervals clamped to the window). Every player also carries **`alive`**,
the canonical stored lives clamped to the window — read it instead of
re-deriving liveness from the `sp`/`d` markers. It is not field-gated and
has three states: `null` liveness not measurable, `[]` measured but never
alive in this window, `[…]` the lives.

#### `getPointEffects({demoId, types?})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId` | `string` (required) | — | — |
| `types`  | `string[]` | all | Effect names to keep: `spike`, `superspike`, `gunshot`, `explosion`, `tarexplosion`, `wizspike`, `knightspike`, `lavasplash`, `teleport`, `blood`, `lightningblood`, or `te<code>` for an unnamed code. Blood usually dominates row count — filter to keep the payload small. |

Output: the point-effect temp-entity stream (schema v71) as columnar
parallel arrays `t`/`ty`/`c`/`x`/`y`/`z`, plus a `types` legend mapping
the TE codes present in the demo to names (kept whole under a filter,
so a filtered response still shows what else it could ask for).
`explosion` is the exact rocket/grenade detonation point, `blood` is
hitscan damage striking a player, `lightningblood` an LG cell
connecting — the wire evidence the damage reconstruction consumes on
pre-instrumentation demos. The `c` count byte's packaging varies per
server generation; never read it as a damage magnitude (`getDamage` is
the damage answer). `pointEffects: null` means the demo carried no
point effects; a filter matching nothing returns empty columns.

#### `getStateAt({demoId, time, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`  | `string`  (required) | — | — |
| `time`    | `integer` (required) | — | Match-relative milliseconds |
| `players` | `string[]` | all | — |
| `fields`  | `string[]` | all standard minus `sp`/`d` | Spawn/death timestamps are rejected — they're events, not state |

Output: `view.StateAtView` — `{ time, players: { name: {...fields} } }`.
Change streams resolve to "latest entry ≤ time" (carry-forward);
intervals to membership; position to nearest sample.

Two fields come from outside the `fields` selector; only the first is
ungated:

- **`alive`** — rides every row whatever `fields` asks for: the
  canonical stored liveness at the instant, never re-derived from the
  `sp`/`d` markers. `true`/`false` = measured, `null` = liveness was not
  measurable for that player.
- **`posAgeMs`** — `time` minus the timestamp of the snapped position
  sample (positive = carried forward from an earlier sample, negative =
  the nearest sample is a later one). The reported position is an
  unbounded carry-forward by design, so check this before trusting
  "where was X at `time`": the occupancy surfaces (`getRegionControl`,
  `getLocGraph`, `getLocTrails`) discard a sample once its age reaches
  250 ms. Present only when `fields` asked for at least one positional
  field (`pos`, `view`, `hgt`, `lq`, `vel`) **and** a sample resolved, so
  its absence under a non-positional field set says nothing about the
  demo's position track.

#### `getLocTrails({demoId, ...})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`     | `string` (required) | — | — |
| `players`    | `string[]` | all | — |
| `minDwellMs` | `int`     | **250** (MCP default; REST 0) | Drop residences shorter than this (nearest-loc flicker), folded into neighbour. Pass `0` explicitly for the raw stream. |
| `startTime`  | `integer` | match start | Match-relative milliseconds |
| `endTime`    | `integer` | match end | Match-relative milliseconds |

Output: `view.LocTrailsView` —
`{ players: [{ name, sequence: [{ start, end, loc }, …] }, …] }`.

#### `getRegionControl({demoId, windowMs?, startTime?, endTime?, regions?})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`   | `string` (required) | — | — |
| `windowMs` | `int` | **5000** (MCP default) | Bucket size for the per-region state strings. Same MCP-vs-REST split as `getBuckets`: REST default is 50, MCP proxy injects 5000 to keep `bucketStates` lengths manageable (a 20-min match: 240 chars per region instead of 24K). |
| `startTime`/`endTime` | `integer` | full match | Match-relative **milliseconds**; windows the bucket range |
| `regions` | `string` | **`summary`** (MCP default) | Polygon detail: `full` (each region's ~6 KB `points` polygon included — needed only to draw the map overlay), `summary` (points stripped; `name`/`locs`/`centroidX`/`centroidY` kept), `none` (regions list omitted). Same MCP-vs-REST divergent default as `getItems` `summary`: REST defaults to `full`, the MCP proxy injects `summary` and a defaulted response carries a `hint`. Pass `regions:'full'` for the points. |

Output: `result.RegionControlResult`. Errors with
`region_control_unavailable` (HTTP 422) if the demo's map has no
region layout. See RESULT_SCHEMA.md for the encoding of
`bucketStates` (per-region one-char-per-bucket string) and `stats`
(match-aggregate percentages).

#### `getTopWindows({demoId, metric?, mode?, windowMs?, gapMs?, limit?, perPlayer?, ...})`

Each player's best stretches, under one of two segmentations. `mode:"fixed"`
(the default) is *"in these `windowMs` ms this
player scored higher on `metric` than in any other stretch of the same
length."* `mode:"gap"` is *"a window is a maximal run of scoring events no
more than `gapMs` apart; its score is their sum"* — the stretch lasts as long
as he kept doing it, not as long as a stopwatch said.

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `metric`    | `string` | `frags` | What the windows are ranked by, case-insensitive: `frags`, `deaths` (a player's WORST stretch), `netFrags`, `damageGiven`, `damageTaken`, `netDamage`, `shots`, `hits`. Any **summable** per-event quantity; ratios (accuracy) are deliberately absent — they don't sum, so "the best window" is undefined for them. Read them off the stats block instead. |
| `mode`      | `string` | **`fixed`** | The segmentation, case-insensitive: `fixed` (every window `windowMs` long) or `gap` (maximal runs of scoring events no more than `gapMs` apart). Each mode takes exactly ONE knob and **rejects the other's** with a `400` — `gapMs` under `fixed`, `windowMs` under `gap`. Under `gap`: rows are disjoint per player by construction, `end` is the run's **last** event (a lone event is a legitimate row with `durationMs` 0), signed metrics cluster on **all** their events (a death both extends a `netFrags` run and lowers its score), and a run **may span the player's own death** — `getLives` is the per-life view. Everything else — metric vocabulary, ranking + tie-break, both caps, `weapons`, `minScore`, the stats block, `score` == the same-named stat — is identical in both modes. |
| `windowMs`  | `int` | **30000** | **Fixed mode's** window length in **milliseconds** — the main knob (5000 = damage bursts, 30000 = hot streaks, 120000 = map-phase dominance). Sweep it; there is deliberately no adaptive mode. Bounded to `[1, match duration]`: omit for the default, and an out-of-range value (a `0`, a negative, or anything longer than the match) is rejected `400 invalid_param` naming the bound rather than clamped. Rejected under `mode:"gap"`, and absent from a gap response. |
| `gapMs`     | `int` | **none — required under `mode:"gap"`** | Gap mode's knob in **milliseconds**: consecutive scoring events no more than `gapMs` apart belong to the same window. It has **no default** on purpose — measured inter-**kill** gaps run p50 ≈ 11–12 s while inter-**damage-event** gaps run p50 ≈ 1.0–1.1 s, so no one value serves both, and omitting it under `mode:"gap"` is a `400` naming the starting points: **~10000 for the frag metrics, ~3000 for the damage and shot metrics**. Bounded to `[1, match duration]`, out-of-range rejected rather than clamped. Rejected under `mode:"fixed"`. |
| `limit`     | `int` | **10** | Total windows across all players, max 200; **negative = uncapped**. Applied AFTER `perPlayer`. Omit for the default; an explicit `0` is rejected `400 invalid_param` (an omitted MCP integer arrives as 0, so 0 cannot mean "uncapped"). |
| `perPlayer` | `int` | uncapped | Max windows from any ONE player. Applied BEFORE `limit`, so `perPlayer:3, limit:10` is "the top 10, at most 3 from anyone" — set it to spread a leaderboard across the roster. Same rule as `limit`: omit for the default, negative for uncapped. (REST rejects an explicit `perPlayer=0`; from this shim a `0` never leaves the query, so it simply reads as unset.) |
| `players`   | `string[]` | all | Restrict to these **subject** players (whose windows are ranked) |
| `weapons`   | `string[]` | all | Restrict the **scoring** events only. Valid tokens depend on the metric's own source — the frag log knows `hook`/`water`, the damage log `explobox`/`drown`, the shot stream only what can be fired; `water` and `drown` are accepted interchangeably. A bad token 400s naming the full valid set. |
| `startTime` | `integer` | match start | Window-search lower bound, match-relative **milliseconds**. Bounds where a window may **start**, not what it covers |
| `endTime`   | `integer` | match end | Window-search upper bound. It too bounds the **start**: a window anchored at `endTime` still runs the full `windowMs` past it, so `endTime:1000, windowMs:30000` gives the best window *anchored* in the first second, covering up to 31 s. Shrink `windowMs` to constrain what a window covers. Under `mode:"gap"` the same rule applies to the run's first event — a cluster anchored before `endTime` still runs to its own last event past it |
| `dmg`       | `string` | **`bounded`** | Damage family for the damage metrics and the stats block: `raw` \| `bounded`. `both` is rejected for **every** metric — a score and a stats block use one family, not two. |
| `minScore`  | `int` | `1` | Drop windows scoring below this many points **of the chosen metric** — a score threshold, not a duration (the ms-valued filter on `getLives` is `minMs`). Matters for the net metrics, which go negative. An explicit `0` IS honoured (keep the windows that broke even). |

Output: `{ timeUnit, scoredBy, dmg, boundedMode, mode, windowMs | gapMs,
limit, perPlayer, measured, windows: [{ rank, player, team, start, end, score,
…stats }, …] }`. `mode` is echoed on **every** response, and exactly one of
`windowMs` (fixed) / `gapMs` (gap) accompanies it — never infer the
segmentation from which knob is present.
Fixed windows are anchored at **real event times** (not a grid) and taken
greedily non-overlapping per player; gap windows are the runs themselves, so
they are disjoint by construction. Either way the result is ONE FLAT list
sorted by score — group by player client-side with one reduce.

`getTopKills` is the adjacent surface, not a substitute: it is the per-**kill**
view (kill-anchored, life-clipped, ranked by the burst that produced one kill,
rows overlap) where gap windows are the per-**stretch** view (no kill required,
rows disjoint, ranked by the metric's sum). They collapse onto each other only
at `metric:"frags", weapons:["rl"], gapMs:3000`, where RL inter-kill gaps of
p50 ≈ 11.8 s leave 85% of clusters singletons — use 8000–15000 for multi-kill
stretches and ~3000 on the damage metrics for rampages.

`scoredBy` (`{metric, weapons, dmg}`) sits on the **envelope, not on each
row**: one query means one rule. It is also the only place the metric is
echoed. It matters because `weapons` scopes the *scoring* events while
the stats block still describes everything that happened, so
`metric:"damageGiven", weapons:["lg"]` can report `score` 445 beside a
`damageGiven` of 650. Without a `weapons` filter the two are equal
exactly — telefrags and stomps score just as they fold — and `weapons`
selects those too, so `weapons:["tele"]` scores telefrags alone.

`dmg`/`boundedMode` echo the damage family the **stats block** was
computed in and this demo's bounded-reconstruction state, exactly as
`getDamage` echoes them. They ride the envelope under **every** metric,
not just the damage ones — a window picked by `metric:"frags"` still
reports `damageGiven`, and that number has a family. Read `dmg` rather
than assuming the `bounded` default took: a defaulted request silently
falls back to `raw` on a demo whose reconstruction was skipped. Both are
absent only on a demo with no damage stream (`measured.damage: false`).

Errors with `top_windows_unavailable` (422) when the demo carries no
source stream for the chosen metric, and `bounded_unavailable` (422) —
under any metric — for an **explicit** `dmg:"bounded"` on a demo whose
bounded reconstruction was skipped. Missing loc data only omits
`locs`/`eventLocs` and never fails the request.

#### `getTopKills({demoId, gapMs?, contestedMs?, limit?, players?, weapons?, minDamage?, ...})`

The match's hardest kill **bursts**, ranked by burst damage — the
highlight-reel view. For each enemy kill the burst is the contiguous run
of **killing-weapon** hits the killer landed on that victim leading up to
it, clipped below by the start of the victim's current life.

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`      | `string` (required) | — | — |
| `gapMs`       | `int` | **3000** | The **capture** gap in milliseconds: a hit joins the run while it lands within `gapMs` of the run's earliest hit so far. Max 5000; omit for the default, and an out-of-range value (a `0`, a negative, or above 5000) is rejected `400 invalid_param` naming the range. **Do not lower it to get tighter bursts** — filter on `maxGapMs` instead (see below). |
| `contestedMs` | `int` | **4000** | Window each row's `returnDamage` sums the victim's damage back over, in ms before the kill. Max 30000, same rejection rule. It sets the window only; the threshold that calls a kill "contested" is yours. |
| `limit`       | `int` | **20** | Rows returned, max 200; **negative = uncapped**. Omit for the default; an explicit `0` is rejected `400 invalid_param` (an omitted MCP integer arrives as 0). 20 rather than `getTopWindows`' 10 because the per-weapon narrowing below thins the list. |
| `players`     | `string[]` | all | Restrict to these **killers**. There is deliberately no victim-side filter — it would be a different question. |
| `weapons`     | `string[]` | all | Restrict the **killing** weapon, which is also the burst's own weapon. Burst-capable tokens only (`rl,lg,gl,ssg,sng,ng,sg,axe,unknown`) — positional/environmental causes are rejected since no damage event can anchor them; a bad token 400s naming the valid set. |
| `minDamage`   | `int` | `0` | Drop bursts below this much burst damage — the same figure the rows are ranked by. A negative value is rejected. |
| `startTime`   | `integer` | match start | Earliest **kill** time, match-relative ms. It bounds the kill, not the burst: a kept row's run may reach back before it. |
| `endTime`     | `integer` | match end | Latest kill time. |
| `dmg`         | `string` | **`bounded`** | Damage family for `damage` **and** `returnDamage`: `raw` \| `bounded`. `both` is rejected — one response is one family, and `damage` is the ranking key, so the two families can order the list differently. |

Output: `{ timeUnit, dmg, boundedMode, gapMs, contestedMs, limit,
measured, kills: [{ rank, killer, victim, team, time, weapon, damage,
hits, spanMs, maxGapMs, victimWep, returnDamage }, …] }`. The `gapMs` /
`contestedMs` echoes are the **resolved** values — they are what the row
numbers mean.

**The burst is the killing weapon's run**, and that is the question this
tool answers: *"how hard was this burst with this weapon"*. On ~8% of
measured kills it therefore **understates** what produced the kill — a
rocket softens, a shotgun finishes, and the row reads `weapon: "sg",
damage: 16` for a kill that took 250 across weapons. Deliberate, not a
defect; the cross-weapon question is a `getDamage` one. A
different-weapon hit inside the run neither joins it nor breaks it.

**Narrow with `maxGapMs`, never with `gapMs`.** The capture default is
generous because truncation is unrecoverable downstream while over-merge
is filterable: a baked 1200 ms gap truncated 11% of rl and 23% of sg
bursts (worst measured, a 291-damage triple-rocket kill reported as
**2**). To get your product's tighter burst, keep the rows whose
`maxGapMs` is within that weapon's cadence — LG ≈ 1200 ms, RL ≈ 2300 ms
— every **kept** row then carries its gap-`g` value exactly (an
over-merged row is dropped, not truncated; ask with `gapMs=g` when the
remainder matters), because dropping hits
from a run only widens gaps. `spanMs` is the **display** figure ("291 dmg
in 1.7 s") and is not a valid narrowing rule.

**Two semantics worth knowing before you filter.** Telefrags, stomps and
squishes carry no damage event, so they produce **no row** — absent from
this ranking only, still in `getFrags` and `getDamage`. And kills by an
**already-dead killer stay in**: the walk consults the *victim's*
liveness and never the killer's, so a rocket in flight when its shooter
died still ranks. That is the spawnluck / went-down-swinging highlight
this tool exists for.

`killer`/`victim` are the frag log's names, so joining to
`getPlayerStats` rows works by **name** — except where two identities
share a display name, which that tool suffixes `name#slot` while the logs
keep the bare name; strip the suffix to join.

Errors with `top_kills_unavailable` (422) when the demo lacks the frag
log, the damage log, or measurable liveness — the last because the burst
walk is clipped by the victim's current life start, and without that clip
a burst absorbs the victim's *previous* life on precisely the rows that
rank highest. `bounded_unavailable` (422) for an **explicit**
`dmg:"bounded"` on a demo whose bounded reconstruction was skipped; a
defaulted one falls back to `raw` and says so in the `dmg` echo.

#### `getLives({demoId, players?, startTime?, endTime?, minMs?, dmg?, summary?})`

One row per spawn-to-death run — the natural unit of QuakeWorld
analysis, and the variable-length counterpart to `getTopWindows`.

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId`    | `string` (required) | — | — |
| `players`   | `string[]` | all | Restrict to these players' lives |
| `startTime` | `integer` | match start | Match-relative **milliseconds**; lives **overlapping** the window are kept, not only those contained |
| `endTime`   | `integer` | match end | See `startTime` |
| `minMs`     | `integer` | `0` (keep all) | Drop lives shorter than this many milliseconds — useful for filtering out spawn-frag lives |
| `dmg`       | `string` | **`bounded`** | Damage family for the per-life stats block: `raw` \| `bounded`; `both` is rejected |
| `summary`   | `boolean` | **`true`** (MCP default) | Token economy. A whole 4on4 match is ~400 lives (~240 KB), and `startTime`/`endTime` only *select* rows — each kept row still carries its whole attribution window — so they are not a size control. `summary` keeps **every row and every scalar** and drops the per-row collections `itemsTaken`, `locs`, `eventLocs`, `victims`, `byWeapon`, `damageByWeapon` (~40% of the bytes). Same MCP-vs-REST divergent default as `getDamage`/`getAim`/`getItems`: REST defaults to `false`, the proxy injects `true`, and a defaulted response carries a `hint`. Pass `summary:false` (with `players`/`startTime`/`endTime`) for one life's detail. |

Output: `{ timeUnit, dmg, boundedMode, measured, lives: [{ player,
team, index, start, end, attrStart, attrEnd, endReason, spawnLoc,
deathLoc, killedBy, deathWeapon, itemsTaken, weaponsHeld, …stats }, …] }`
— time-ordered per player, players in name order. `dmg`/`boundedMode`
are the same envelope echoes `getTopWindows` carries.

Three things to know before summing:

- **Lives partition the match.** Each life is attributed every event
  from its own start to the start of the next (match start / match end
  at the edges), so a **posthumous** kill — a rocket landing after its
  shooter died — counts for the life that fired it, so per-life stats sum
  to `getFrags`' `frags[]` rows on the frag side and to `getDamage`'s
  **non-summary** aggregate on the damage side (not to its `events[]`
  rows: a telefrag or stomp folds its value into the totals without a
  per-hit row of its own). They do **not**
  necessarily sum to the `byPlayer` scoreboards, which count deaths the
  log never recorded (a `DF_DEAD`/`STAT_HEALTH` death with no obituary).
  `durationMs` stays **alive time** while the counts cover the wider
  attribution window, so a rate derived from a count over it runs high —
  slightly, summed over a whole match; by tens of percent on a single row
  where a short life is followed by a long dead gap. Every row carries
  that window as `attrStart`/`attrEnd`, so divide by
  `attrEnd - attrStart` when you want the exact one.
- **`deaths` is not a 0/1 flag and does not imply `endReason`.** It counts
  the frag-log death rows attributed to the life, so it is 0 whenever no
  row names the player at that instant — including on lives whose
  `endReason` *is* `death` — and it can **exceed 1**: a life also carries
  any death recorded in the dead gap that followed it (the KTX `dtTELE2`
  deflection — measured across 11 558 corpus lives, 12 rows with 2 and
  one with 3). Read `endReason` for how the life ended.
- **A filtered response does not reconcile.** `startTime`/`endTime`
  select lives but each still carries its whole attribution window, and
  `minMs` drops a life together with the events inside its window. An
  inverted window (`startTime` > `endTime`) selects nothing and returns
  `lives: []`, matching `getFrags`, `getDamage` and `getTopWindows`.

`endReason` is `death` | `matchEnd` | `leftGame`, always present —
`killedBy` alone used to conflate all three, and is additionally absent
on a death that no obituary named (reachable, but not observed in the
42-demo cached corpus: 0 of its 11364 death-ended lives).
`itemsTaken` is **not** omitempty: `null` means the demo has no item
timeline, `[]` means it has one and this life took nothing. Under the
default `summary:true` it is `null` on every row and carries no signal —
read `measured.items` on the envelope.

Errors with `lives_unavailable` (422) when the demo carries no
per-player streams to segment, and equally when it has streams but
liveness was never **measurable** on any of them — serving `lives: []`
there would read as "nobody ever lived", which is a different and false
claim from "we could not tell". The envelope's `measured.liveness`
carries the same fact on the responses that do get served, so it is only
ever `false` on `getTopWindows`. A demo with no damage stream still
yields lives.

#### `listArtifacts({})`

No parameters. Output: the fetchable-artifact catalog
`{ schemaVersion, artifacts: [{ name, resultKey, cost, lazy,
description }, …] }` — trimmed at the MCP layer to servable artifacts
and routing-relevant fields (the full DAG manifest with
`requires`/`provides`/`mutates` edges and internal nodes lives on REST
`/v1/artifacts` + `/v1/graph`). Static per schema version. The
authoritative catalog is the generated
[`../mvd-analytics/ARTIFACTS.md`](../mvd-analytics/ARTIFACTS.md). Call
this to discover artifacts beyond the curated tools.

#### `getArtifact({demoId, name})`

| Param | Type | Default | Description |
|---|---|---|---|
| `demoId` | `string` (required) | — | — |
| `name`   | `string` (required) | — | Artifact name from `listArtifacts` (e.g. `frag`, `damage`, `loc-graph`, `los`). Must be `servable`. |

Output: the artifact's Result section under its `resultKey` (e.g.
`name: "frag"` → `{ "frags": … }`). `los` is
materialised on demand (first call may be slow). No filters — for
filtered reads use the curated tools. Errors with `artifact_unknown`
(HTTP 404) for an unknown or non-servable name.

### Search routes through mvd-api

Discovery (finding demos by player names, teams, map, etc.) is
hub.quakeworld.nu's data, but the shim does **not** talk to the hub
directly. `searchGames` proxies to `mvd-api`'s `GET /v1/games/search`
like every other tool, so `mvd-api` is the single egress point and the
only place the hub connection (URL + anon key) is configured. The shim
holds no hub secrets and needs no hub env vars.

This was once the one exception — the shim queried the hub's public
Supabase endpoint itself — but that split meant two code paths to the
hub and hub credentials in two places. Now that `mvd-api` owns the
search endpoint, the shim is a uniform proxy: one backend, one key.

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
   matches with rosters, scores, dates from the hub catalog (via
   `mvd-api`'s `GET /v1/games/search`). Cheap — no demo parse; the
   agent can filter / rank from the rows.
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
