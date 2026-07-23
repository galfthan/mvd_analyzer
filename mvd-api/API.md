# mvdanalyzer-api — HTTP integration guide

Integration guide for building **custom web frontends and tools** on top
of **mvdanalyzer-api** (the `mvd-api` server binary) — the hosted REST
surface over QuakeWorld demo analytics.

This document is the **high-level guide**: getting started, the
cross-cutting conventions (units, caching, errors, auth, CORS), how to
choose the right endpoint, and task recipes. It deliberately does NOT
enumerate endpoints or restate shapes — that reference exists exactly
once, in the OpenAPI 3.1 document the server itself serves at
**`/openapi.yaml`** (browsable at **`/docs`**): every route, parameter,
full field-level response schema, and error code, pinned to the code by
drift tests and validated against golden-corpus responses. Deep
field-level *semantics* of the analysis output (the Result contract
shared with the Go library and WASM builds) live in
[`mvd-analytics/RESULT_SCHEMA.md`](../mvd-analytics/RESULT_SCHEMA.md).
JSON snippets here are **real captured output** (trimmed with `…`),
shown for orientation, not as a second schema.

For the operator-facing view (flags, cache layout, build, smoke tests)
see [`README.md`](README.md). For the MCP wrapper see
[`mvd-mcp/README.md`](../mvd-mcp/README.md) — it forwards the same calls.

---

## 1. Getting started

Base URL defaults to `http://localhost:8080`. A demo is addressed by an
`{id}` segment:

- **`gameId:NNNN`** — a numeric [hub.quakeworld.nu](https://hub.quakeworld.nu)
  game id. On first use the server fetches and parses the MVD; subsequent
  calls hit the cache.
- **`sha:HEX`** — the 64-char SHA-256 of the demo's *uncompressed* MVD
  content: a demo already in the local cache (returned by `loadDemo`;
  good for bookmarking a warm entry) or one you uploaded with
  `uploadDemo` (`POST /v1/demos`).

Don't have a `gameId` yet? **`GET /v1/games/search`** discovers demos in
the hub catalog by player / team / map / mode / matchtag / date, returns
`{limit, offset, count, total?, games}`, and each row's `id` becomes the
`gameId:<id>` you feed the flow below. Each row also carries
`server_hostname` (the QW server the game was played on). The `players`
filter is a **case-insensitive** full-text search (unlike the per-demo
endpoints' exact, case-sensitive `players` filter). Its `from`/`to` are
**calendar dates**, strict `YYYY-MM-DD` — a malformed date 400s
`invalid_param` (it is not the match-relative `from`/`to` the per-demo
endpoints use).

Typical frontend flow:

```
POST /v1/demos/gameId:12345              → warm the cache, get the sha id
GET  /v1/demos/gameId:12345/overview     → "what was this match" in one call
GET  /v1/demos/gameId:12345/<detail>     → drill into a specific panel
```

`loadDemo` and `uploadDemo` are the only calls that can be slow (cold
fetch/upload + parse). Everything else is served from the cached
`*Result`, typically sub-millisecond.

A machine-readable **OpenAPI 3.1** description of the whole surface is
served at **`GET /openapi.yaml`** (embedded in the binary; drift tests pin
its routes, error codes and enums to the code, and its response schemas
are validated against golden-corpus responses), with a browsable viewer
at **`GET /docs`** (vendored RapiDoc — no external requests). Both are
reachable without an API key.

---

## 2. Conventions (read this once)

### 2.1 Time units — one rule: everything is int32 ms

**Every time value in the API is int32 milliseconds** — request params
and response fields, REST and MCP alike (schema v57, the pure-ms model).
There is no unit selection and no per-endpoint split. `timeUnit`, where
present, is a **constant `"ms"` self-description echo**:

> **Every `/v1/demos/{id}/*` JSON response that carries time values
> echoes a top-level `"timeUnit": "ms"`, including `/artifacts/{name}`
> (which since v57 echoes a `timeUnit` sibling of the resultKey when the
> section carries time). `/loc-graph` echoes too — its node weights are
> aggregate durations, int32 ms since v57. The lone exception is
> `/demoinfo` (KTX's own mixed units). Responses with no time value at
> all — `/loc-table`, `/metadata`, and the no-time artifacts
> (`/artifacts/{metadata,map-entities}`) — carry no echo.**

The four formerly bare-array endpoints wrap their array in an object so
the echo has a home: `/chat` → `{timeUnit, messages:[…]}`, `/airgibs` →
`{timeUnit, airgibs:[…]}`, `/backpacks` → `{timeUnit, backpacks:[…]}`,
`/weapon-pickups` → `{timeUnit, pickups:[…]}`.

**Field-name conventions — the dense/sparse key rule.** The per-item
time key follows what the data scales with; both spellings are int32 ms
(the name never encodes the unit). **Event-scaled** sparse lists and
singleton timestamps carry the descriptive **`time`**: the event lists
(`/frags`, `/damage`, `/shots`, `/chat`, `/backpacks`,
`/weapon-pickups`, `/airgibs`), `/events` rows, `/buckets?layout=row`
rows, `/state-at`'s envelope, and `/items?summary=true`'s `firstTake`.
**Sample-rate-scaled** dense arrays carry the terse **`t`**: `/aim`'s
crosshair `t` + `lgRamp` `since`, the columnar `/buckets` grid, and the
raw stream tracks embedded in `/stream-slice` (`h:[{ "t":105000,… }]`,
`pos.t:[…]`, `rl:[{ "s","e" }]`). Other descriptively-named times
(`endTime`, `availableFrom`/`takenAt`/`respawnAt`, `nextDeathTime`,
`dropTime`, `duration`, `start`/`end`) are int32 ms too. Envelope bounds
are `start`/`end` on `/stream-slice` and `/loc-trails`, and
`start`/`windowMs` on the columnar `/buckets` axis
(`time(i)=start+i*windowMs`).

The exceptions — time-carrying responses that still don't echo:

- **`/demoinfo` is the KTX units island** — KTX's own clock, a mix of
  native units (see RESULT_SCHEMA.md §DemoInfoResult), so no single echo
  describes it. This is the **sole seconds surface** in the API.
- **`/artifacts/{name}`** serves the raw stored result section under its
  resultKey (the exact-bytes escape hatch). Since v57 it also echoes a
  top-level `"timeUnit": "ms"` **sibling** of the resultKey whenever the
  section carries time — `frag`, `damage`, `shots`, `aim`, `opening`,
  `match`, `messages`, `timeline`, `items`, `backpacks`, `weapon-pickups`,
  `loc-graph` (e.g. `/artifacts/messages` → `{timeUnit, messages:{…}}`).
  loc-graph's node weights are aggregate durations (int32 ms since v57),
  so it echoes too. The no-time artifacts — `metadata`, `map-entities` —
  carry no echo, and `/artifacts/demoinfo` is the KTX units island. The
  underlying stored schema is all int32 ms — see RESULT_SCHEMA.md §"Time
  units". `/artifacts/los` is a materialized artifact that aliases the
  `/los` body and carries its `"ms"` echo like the curated endpoint.

Separately, the responses with **no time value at all** — `/loc-table`
and `/metadata` — carry no echo because there is no time value to unit.

`/overview`'s `timing` block is consistent with the rule: its wall-clock
fields are explicitly `*Ms`-named (`demoOffset`, `demoStartUnixMs`,
`demoStartAccuracyMs`, `pauses[].atMs`/`.durationMs`).

**Query inputs follow the same rule**: `from`/`to`/`time` on demo
endpoints are **integer milliseconds**. A non-integer value (e.g.
`from=10.5`) 400s with `invalid_param` and an `(integer milliseconds)`
hint rather than misfiltering. The only date-typed params are search
`from`/`to` (calendar dates `YYYY-MM-DD`), which are not times.

> **⚠️ The ms tripwire only catches NON-INTEGER forms.** `from=10.5` or
> `from=1e3` reject because they are not integers — but a whole-number
> value that *was* meant as seconds, e.g. `from=60` (intending 60 s), is a
> perfectly valid integer ms and **cannot be detected**. A pre-v57 caller
> that passed integer seconds migrates **silently** to a window 1000× too
> small — `from=60` now means 60 ms, not 60 s, and quietly returns almost
> nothing instead of erroring. Audit every caller that passed integer
> seconds and multiply by 1000.

### 2.2 Query parameters

Parameter **names are case-insensitive**; the canonical (documented)
spelling is camelCase — `windowMs`, `minDwellMs`, `includeTeam` — but
`windowms` / `WindowMs` resolve too. (Documented exception: search's
`matchtag` is a lowercase single word, mirroring the KTX serverinfo key,
not camelCase.) Parameter **values** are case-sensitive
for player names (QW names are case-significant) and case-insensitive for
weapon / item / kind / loc / layout tokens.

An **unrecognised parameter name is rejected**, not ignored: a query key an
endpoint does not accept 400s with code `unknown_param`, naming the offender
and the accepted keys. The global `label` traffic-source tag is accepted on
every endpoint. Enum-valued params likewise reject an unknown **value** with
`400 invalid_param` (e.g. `/events?types=bogus`) instead of matching nothing.

- **`players`, `fields`, `types`** — comma-separated lists; URL-decode
  once. Omit `players` to get all; omit `fields`/`types` to get the
  endpoint's default set.
- **`weapons`** — comma-separated weapon tokens (`rl,lg,…`) on `/frags`,
  `/damage`, `/backpacks`, `/weapon-pickups`. A CSV set on every one of
  them since schema v36 (`/backpacks` previously took a single value).
  The pre-16.2 singular spelling `weapon` remains an accepted legacy
  alias; `weapons` wins when both are present.
- **`reducers`** (`/buckets`) — comma-separated `field=name` pairs, e.g.
  `reducers=h=min,a=last`. Names come from the reducer registry in
  RESULT_SCHEMA.md.
- **`from` / `to`** — match-relative **integer milliseconds**. Omit for
  the whole match. Honoured by `events`, `stream-slice`, `loc-trails`,
  `chat`, `region-control`, `aim`, and `frags` / `damage`. A non-integer
  value 400s `invalid_param` with an `(integer milliseconds)` hint.
- **`summary`** (`/frags`, `/damage`, `/aim`, `/items`) — `1`/`true` drops
  the big per-event log / sample arrays / phase timeline and returns only
  the aggregates.
- **`dmg`** (`/damage`) — which damage **family** to return:
  `raw` (the unbound wire value, byte-stable pre-v54 shape), `bounded`
  (KTX's scoreboard reconstruction — armor absorbed + health capped to the
  victim's remaining health, in the raw field names), or `both` (raw plus
  additive `bounded` fields). The default is **`bounded`** for both
  summaries and the full log (effective damage is what the scoreboard
  means; raw overkill is the opt-in). A *defaulted* request on a demo
  whose reconstruction was skipped (midair / instagib / dmgfrags modes)
  falls back to `raw`; an *explicit* `dmg=bounded` there is a
  `422 bounded_unavailable`. Unfiltered bounded summaries source the
  per-player figures from KTX's exact scoreboard (`boundedSource: "ktx"`).
- **`time`** — match-relative **integer milliseconds**; **required** on
  `/state-at`. A non-integer value 400s `invalid_param` with an `(integer
  milliseconds)` hint.
- **`windowMs`** — integer milliseconds (`/buckets`, `/region-control`).
  ⚠️ **Defaults to 50 ms when omitted** — on a 20-minute match that is
  ~24,000 windows per field per player. Always pass an explicit
  `windowMs` sized to your question (the hosted MCP layer injects 5000).
  All time params are ms: `windowMs`, `from`, and `to` alike.
- **There is deliberately no `limit`/`offset` pagination** on the
  per-demo endpoints: the data is time-series, so the size controls are
  the `from`/`to` window, `players`/`fields` scoping, and `summary`.
  `limit`/`offset` pagination applies only to the game-discovery
  endpoint `GET /v1/games/search` (page until `offset + count >= total`).
  There `limit` defaults to 20 and is capped at 100 — a `limit` above 100,
  or a negative `limit`/`offset`, is rejected with `400 invalid_param`
  (no longer silently clamped).
- **`loc`** — `name` (default) resolves loc indices to names; `index`
  returns the raw `LocTable` index for index-based math (decode via
  `/loc-table`). Honoured by `buckets`, `events`, `stream-slice`,
  `state-at`, `loc-trails`.
- **`layout`** (`/buckets` only) — `column` (default, compact) or `row`.
  See the `/buckets` operation in `/docs`.

The valid **field codes** (`h`, `a`, `rl`, `pos`, `view`, `hgt`, `lq`,
`vel`, `sp`, `d`, …) and **reducer names** are listed once in
[RESULT_SCHEMA.md §Field vocabulary / Reducer registry](../mvd-analytics/RESULT_SCHEMA.md#field-vocabulary).
Note (schema v31+): `pos` is **strictly x/y/z** (+ the per-sample loc
label `li`). The player's **view direction** is the opt-in `view` field
(raw `angle16` pitch/yaw state after `svc_playerinfo` delta
carry-forward, decode `deg = uint16(v)*360/65536` — equivalently
`deg = ((v mod 65536) + 65536) mod 65536 × 360 / 65536` — pitch > 180° =
looking up). These `vp`/`vya` view angles (and `/state-at`'s `view`) are
**raw angle16 wire shorts**; contrast `/aim`'s `dyaw`/`dpitch`, which are
already **float degrees** off the target. Floor height is `hgt`; liquid state is `lq`;
**velocity** (vx/vy/vz, Quake units/sec, schema v32) is `vel`.
Height/liquid no longer ride along `pos` — request each by code.
Note (schema v33+): the coordinate values `pos` x/y/z, `vel` vx/vy/vz,
and `hgt` are **`float32`** Quake units (sub-unit precise — earlier
versions rounded them to whole `int32` units), so expect fractional
numbers in those arrays. In the dense outputs (`stream-slice` tracks and
`buckets` columns) they are serialized **rounded to 3 decimals**
(lossless for eighth-unit positions; trims the float tail on velocity),
so a value reads `-58.333`, not `-58.333332`. The point-in-time
`state-at` values are emitted at full float32 precision (low volume, so
not rounded). Only the **time axes** stay int32 ms (above). The `hgt`
no-floor sentinel is `-1000000000` (was `-2147483648`).

### 2.3 Caching (use it — the data is immutable)

Successful 2xx responses on the **per-demo** endpoints set:

```
Cache-Control: public, max-age=86400, immutable
ETag: "<sha>-v<schemaVersion>"
X-Schema-Version: <n>
X-Cache: HIT | WARM | MISS
```

A demo's analysis never changes for a given schema version, so frontends
should cache aggressively and send `If-None-Match: "<etag>"` for a cheap
`304`. A schema bump changes the ETag suffix and invalidates client
caches automatically.

Two families carry a **different ETag shape**:

- The generic artifact endpoint uses a **finer per-artifact** form
  `"<sha>-<name>@v<schemaVersion>"` (e.g. `"abc…-frag@v49"`), so a client can
  revalidate one artifact independently.
- The binary-static endpoints `/v1/artifacts` and `/v1/graph` depend
  only on the schema version, so their ETag is `"artifacts-v<n>"` /
  `"graph-v<n>"` (no sha). They set `Cache-Control` + `ETag` +
  `X-Schema-Version` but **no `X-Cache`** (nothing demo-cached to report).
- The per-map endpoints `/v1/maps/{map}/entities` and `/geometry` are
  demo-independent statics: ETags `"ents-<map>-v<corpusVersion>"` and
  `"geo-<map>-<size>"`, with **only** `Cache-Control` + `ETag` (no
  `X-Schema-Version` / `X-Cache`).

`POST /v1/demos/{id}` (the warm-up call) is a non-cacheable action: it
returns `X-Cache` / `X-Schema-Version` but **no** `Cache-Control` / `ETag`.
Error responses carry `Cache-Control: no-store` and no `ETag`.

Every response — success or error — also carries `X-Request-Id: <hex>`,
a per-request id echoed in the server access log (see §2.4).

### 2.4 Errors

Non-2xx responses use a stable envelope:

```json
{ "error": { "code": "demo_not_found", "message": "gameId 0" } }
```

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `invalid_demo_id` | malformed `{id}` |
| 400 | `invalid_param` | malformed **or rejected** query parameter — bad number, malformed `reducers` pair, unknown `loc`/`layout` token, unknown `fields` code, unknown reducer name, or an unknown enum value (e.g. `/events`/`/chat` `types`) |
| 400 | `unknown_param` | an unrecognised query parameter **name** — the message names the offending key and the endpoint's accepted keys. The global `label` traffic-source tag is accepted everywhere |
| 400 | `missing_param` | required param absent (e.g. `time` on `/state-at`) |
| 401 | `unauthorized` | **auth mode only** — missing / invalid / revoked API key on a protected route. Carries `WWW-Authenticate: Bearer`. The body is deliberately generic and never says whether the key was absent vs revoked (see §2.5). |
| 429 | `rate_limited` | **auth mode only** — per-key rate limit exceeded. Carries `Retry-After: <seconds>`; wait that long and retry (see §2.5). |
| 404 | `demo_not_found` | hub has no row for this gameId |
| 404 | `map_unavailable` | no entity corpus / geometry for this map (`/v1/maps/{map}/…`) |
| 404 | `artifact_unknown` | no servable artifact of that name (`/v1/demos/{id}/artifacts/{name}`) |
| 422 | `demoinfo_unavailable` | non-KTX server or aborted match |
| 422 | `metadata_unavailable` | no fullserverinfo / countdown centerprint |
| 422 | `frags_unavailable` | no frag log |
| 422 | `damage_unavailable` | no KTX `mvdhidden_dmgdone` damage stream |
| 422 | `bounded_unavailable` | `dmg=bounded` on a demo whose bounded reconstruction was skipped (midair / instagib / dmgfrags mode) |
| 422 | `shots_unavailable` | no shot data (no weapon fires decoded) |
| 422 | `aim_unavailable` | no aim data (needs shots + position/view streams) |
| 422 | `locgraph_unavailable` | no position track |
| 422 | `los_unavailable` | `/los` on a map with no usable visibility BSP (no map name, BSP not provisioned, or a provisioned BSP that fails to parse) — never cached, so a retry after provisioning succeeds |
| 422 | `opening_unavailable` | no detected match start (`/v1/demos/{id}/artifacts/opening`) |
| 422 | `region_control_unavailable` | no region-control layout for this map |
| 422 | `airgibs_unavailable` | no timeline analysis (BSP-less maps return `[]`, not this) |
| 502 | `hub_upstream` | network / 5xx from the hub |
| 500 | `internal` | unexpected server error (see below) |

**5xx bodies are generic.** A `500 internal` never echoes the underlying
error text (it can embed local cache paths or upstream URLs). The body is a
fixed message plus the request id — `"internal server error (request id
<hex>)"` — and the real error is logged server-side keyed by that same
`X-Request-Id`. Quote the id when reporting a problem. `4xx` messages stay
specific and safe (they're user-actionable and path-free). The former
`panic` code is gone: a handler panic is now a plain `500 internal`.

(Schema v36 folded the former `view_error` code into `invalid_param`: a
bad query parameter is one error class regardless of whether it failed
syntactic parsing or view-layer validation.)

**Available vs unavailable — the `422` rule.** A `422 <section>_unavailable`
means the demo **structurally lacks the signal** that section needs — a
non-KTX server has no `demoinfo`/`damage`, a demo without a position track
has no `loc-graph`, a map without a region layout has no `region-control`.
These are **expected** for some demos; treat them as "this panel is
unavailable for this demo", not a hard failure, and use `/overview`
(`hasRegionControl`, `errors`) to hide panels up front. Endpoints whose data
is always computable or list-shaped — `/items`, `/backpacks`,
`/weapon-pickups`, `/chat` — instead return **`200` with an empty body**
when there's nothing, never `422`.

**Null vs `[]`.** A governed response's **top-level array is never
`null`** — an empty result is `[]` (`/events`, `/stream-slice` `players`,
`/loc-trails` `players`, and the list endpoints above). Nested arrays
deeper in a body may still be `null`; check before iterating.

### 2.5 Authentication

mvd-api runs in one of two modes, chosen by the operator's `-auth-dir` flag.

**No-auth (localhost) mode — the default.** No key is required. The optional
`Authorization: Bearer <label>` header (or `?label=`) is **not validated** —
it's a non-secret source tag for the access log (`web-community`,
`cli-script`, …). This is the historical behaviour and is unchanged.

**Keyed (hosted) mode.** When the operator runs with `-auth-dir DIR`, every
route under `/v1/` — plus `POST /v1/demos/{id}` — requires an API key:

```
Authorization: Bearer qwmvd_<...>
```

- Keys look like `qwmvd_` followed by a URL-safe base64 blob. **The key is a
  secret** — treat it like a password: send it only over HTTPS, never put it
  in a URL, a query string, or a public repo. The server stores only a hash;
  a lost key cannot be recovered, only re-issued.
- Missing, malformed, or revoked keys get `401 unauthorized` with
  `WWW-Authenticate: Bearer`. The body never distinguishes those cases.
- Exempt from the key requirement: `GET /healthz`, `GET /v1/version`,
  `GET /openapi.yaml` + `GET /docs` (the API description and its viewer —
  the public contract, embedded bytes only), the `/portal/*` prefix (its
  own sign-in), and any `OPTIONS` preflight.
- **`GET /v1/auth/check`** → `204 No Content` for a live key, `401` otherwise.
  Use it to test a key without side effects:
  `curl -sSD- -o/dev/null -H "Authorization: Bearer qwmvd_…" https://host/v1/auth/check`.
- Requests are rate-limited **per key** (not per IP). Over the limit →
  `429 rate_limited` + `Retry-After: <seconds>`. Two classes exist: normal
  (portal) keys and looser `service` keys (issued to first-party apps).

**Getting a key.** On a deployment that runs with `-portal`, a user signs in
with Discord at **`https://<host>/portal`** and self-services one key (sign in
→ *Generate key* → copy it once). Regenerating revokes the old key. First-party
apps get a `service` key from the operator instead (the `keys` CLI). See
[mvd-api/README.md — "The Discord key portal"](README.md#the-discord-key-portal-getting-a-key)
for the full flow. The portal is off unless the operator enables it.

### 2.6 CORS (browser clients)

The API is CORS-enabled for any origin — it's read-only and
unauthenticated, so `*` is safe:

```
Access-Control-Allow-Origin: *
Access-Control-Expose-Headers: ETag, X-Cache, X-Schema-Version, X-Request-Id
```

`Expose-Headers` is what lets browser JS actually read those response
headers (notably `ETag`, for conditional GETs). Preflight `OPTIONS` on any
path returns `204` with `Access-Control-Allow-Methods: GET, POST, OPTIONS`,
`Access-Control-Allow-Headers: Authorization, Content-Type, If-None-Match`,
and `Access-Control-Max-Age`. Preflight needs no auth.

**CORS + auth interaction.** In keyed mode, CORS still runs *outside* the
auth check, so an `OPTIONS` preflight is answered (`204`, no key) before auth
is consulted — a browser's automatic preflight never fails on the missing
`Authorization` header. The actual `GET`/`POST` that follows still needs the
key. `Access-Control-Allow-Origin: *` and a credentialed `Authorization`
header coexist because the key travels as a plain header, not a cookie (the
CORS credentials mode that `*` forbids applies to cookies, not bearer tokens).

### 2.7 API versioning

Two version numbers move independently, and they mean different things:

- **The `/v1` path prefix is the compatibility contract.** It is bumped
  only for a *breaking* change — a field removed or renamed, an enum value
  withdrawn, a type changed incompatibly, a default behaviour altered. As
  long as you stay on `/v1`, existing integrations keep working.
- **`schemaVersion` (the ETag `-v<n>` suffix, `X-Schema-Version`, and the
  OpenAPI `info.version`) is a regeneration counter.** It ticks on *every*
  observable change to the analysis output, including purely **additive**
  ones — a new field, a new event type, a new enum value. It keys caches
  and ETags, so bumping it invalidates stale client caches automatically; a
  schema-version increase is **not** a signal that anything broke.

Within `/v1`, clients must **ignore unknown fields and unknown enum
values** so that additive changes (which only raise `schemaVersion`) never
require a client update. Pin behaviour to `/v1`, treat `schemaVersion` as a
cache key, and let new fields/enum members flow through unread until you
choose to consume them. (For example, schema v58 added the `demomark`
event type to `/events` — a no-`types` caller simply began seeing new rows.)

---

## 3. Choosing the right endpoint

For per-player state over time, four endpoints read the same underlying
streams but in different shapes. Pick by what you're drawing:

| You want… | Use | Why |
|---|---|---|
| A value **at one instant** (tooltip, scrubber readout) | **`/state-at`** | One carry-forward sample per field at `time`. |
| A **series/trend** on a fixed grid (charts, heatmaps) | **`/buckets`** | One reduced value per `windowMs` window. |
| **Every raw transition** in a window (native-rate detail, replay) | **`/stream-slice`** | Unreduced entries + carry-forward at window start. |
| A **discrete event log** (kill feed, life events, powerups) | **`/events`** | Tagged event list; authoritative for spawns/deaths. |

Concrete consequences:

- **Native-rate positions (~77 fps)** come **only** from
  `/stream-slice?fields=pos`. `/buckets` and `/state-at` down-sample
  position to one sample per window / instant.
- **Spawns & deaths**: `/events?types=spawn,death` is the authoritative
  log. `/stream-slice?fields=sp,d` gives the raw ms timestamp arrays.
  `/buckets?fields=sp,d` only yields a per-window bool (lossy — collapses
  a same-window death+respawn).

---

## 4. Endpoint reference — served by the API itself

The per-endpoint reference lives in the OpenAPI 3.1 document embedded in
the server, so it cannot drift from the running code:

- **`GET /openapi.yaml`** — machine-readable: all operations, reusable
  parameters, full field-level response schemas, the error-code /
  artifact / field-code enums. Drift tests pin it to the router and the
  code enums; a golden-corpus validation test checks every response
  schema against real responses.
- **`GET /docs`** — the same document rendered as a browsable reference
  (vendored RapiDoc; try-it console with Bearer auth; no external
  requests).

Both are reachable without an API key. Operation semantics that used to
be spelled out here — filtering/recompute rules, unit seams, the
artifact node↔resultKey mapping table, availability behaviour — are in
the operations' descriptions there. Field-level semantics of the
underlying Result sections stay in
[`RESULT_SCHEMA.md`](../mvd-analytics/RESULT_SCHEMA.md), which the
server also renders standalone at **`GET /docs/result-schema`**.

---

## 5. Recipes

Common frontend features → the call that backs them.

- **Match header / scoreboard** → `GET /overview` (one call: teams,
  players, top streaks/powerups, degraded flag).
- **Kill feed with obituaries** → `GET /events?types=frag` (use
  `/frags` if you need the `isSuicide`/`isTeamKill` flags instead).
- **Score-over-time line** → `GET /events?types=frag`, accumulate
  `delta` client-side; or `/buckets?fields=sp,d` for activity density.
- **Health/armor chart for a player** → `GET /buckets?fields=h,a&windowMs=1000&players=X`
  (smooth grid) or `/stream-slice?fields=h,a&from=…&to=…` (every change).
- **Map replay / movement trails (~77 fps)** → `GET /stream-slice?fields=pos&players=X&from=…&to=…`
  — the only native-rate position source. Stitch windows for the full
  match. Remember positions are **int32 ms**.
- **Aim arrows / sightlines / "who's looking at whom" (~77 fps)** →
  add `view` to the fields: `GET /stream-slice?fields=pos,view&players=X&from=…&to=…`.
  Decode `vp`/`vya` with `deg = uint16(v)*360/65536`; forward vector
  `= (cos p·cos y, cos p·sin y, −sin p)`.
- **Speed curve / bunny-hop analysis** → add `vel`:
  `GET /stream-slice?fields=vel&players=X&from=…&to=…`. Speed =
  `hypot(vx,vy,vz)`, horizontal = `hypot(vx,vy)`; expect ±1-unit
  quantization noise on the raw derivative, smooth client-side if needed.
- **Scrubber tooltip (state at playhead)** → `GET /state-at?time=T&fields=h,a,rl,pos`
  (add `view`/`hgt`/`lq` for look direction / height / liquid).
- **Life events / deaths timeline** → `GET /events?types=spawn,death`.
- **"Who controlled QUAD?"** → `GET /region-control?windowMs=10000`,
  read `stats.QUAD.byPlayer`.
- **Loc heatmap / movement graph** → `GET /loc-graph` (aggregate) or
  `/loc-trails` (per-player sequence with dwell).
- **Draw the map (items, spawns, teleporters as overlays)** → `GET /v1/maps/{map}/entities`
  (map name from `/overview`); add `GET /v1/maps/{map}/geometry` for
  floor polygons to render underneath.
- **Weapon effectiveness** → `GET /demoinfo` (KTX accuracy/damage) or
  `/weapon-pickups` (kills-before-next-death).
- **Analyze a local demo file (no hub gameId)** → upload it, then use the
  returned `demoId` with any per-demo GET:

  ```
  curl -sS -X POST --data-binary @match.mvd.gz \
       -H 'Authorization: Bearer <key>' \
       https://<host>/v1/demos
  # → {"demoId":"sha:…","sha256":"…","fromCache":false,"schemaVersion":…}
  curl -sS -H 'Authorization: Bearer <key>' \
       https://<host>/v1/demos/sha:…/overview
  ```

  Raw `.mvd` and gzipped `.mvd.gz` both work (sniffed by gzip magic; the
  `sha` is always of the uncompressed content, so both forms yield the
  same id). Re-uploading a known demo is an idempotent cache hit. Limits:
  64 MiB on the wire, 512 MiB decompressed, plus a per-key daily quota in
  auth mode. Note that an uploaded demo has **no owner** — any key holder
  who knows its `sha` can read the analysis — and it lives in the shared
  cache eviction pool, so after eviction a `sha:` GET returns 404 and the
  client simply re-uploads.

When fetching positions or any raw stream in `index` loc mode, fetch
`/loc-table` once and decode client-side.
