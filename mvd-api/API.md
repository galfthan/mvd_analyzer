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
`{timeUnit, preMs, airgibs:[…]}`, `/backpacks` → `{timeUnit, backpacks:[…]}`,
`/weapon-pickups` → `{timeUnit, pickups:[…]}`. (`/airgibs` also echoes the
`preMs` pre-hit look-back its list was detected with — see §2.2.)

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
`demoStartAccuracyMs`, `matchStartUnixMs`, `matchStartAccuracyMs`,
`matchEndUnixMs`, `pauses[].atMs`/`.durationMs`).

To answer *when a match was played*, read `timing.matchStartUnixMs`
(schema v72) rather than `demoStartUnixMs`: it comes from the date
markers the server printed on the wire, so it is present on ~95% of demos
where the server-clock anchor reaches ~25%. It is graded, not filtered —
check `matchStartConfidence` (`exact` / `unverified` / `contradicted`)
and, when it is not `exact`, `matchStartNote`, which names the check that
failed (typically an unset server clock stamping a date before its own
binary was released).

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
  `/damage`, `/backpacks`, `/weapon-pickups`, `/top-windows`, `/top-kills`. A CSV set on
  every one of them since schema v36 (`/backpacks` previously took a single
  value).
  Each endpoint validates the tokens against its **own closed vocabulary**
  and rejects an unknown token with `400 invalid_param` (naming the valid
  set) rather than matching nothing: core codes are `rl,lg,gl,ssg,sng,ng,
  sg,axe`; `/damage` also takes the pseudo-codes `tele`/`stomp` (positional
  kills) plus death-type/environmental codes (`hook,explobox,squish,lava,
  slime,drown,fall,trigger,suicide,unknown`); `/frags` also takes the
  obituary cause codes (`hook,rail,squish,explobox,fall,lava,slime,water,
  world,tele,stomp,unknown,suicide,teamkill`); `/backpacks` accepts only
  `rl,lg`;
  `/weapon-pickups` only `ssg,ng,sng,gl,rl,lg`. On `/top-windows` the
  vocabulary follows the chosen `metric`'s **own source** — the frag one for
  `frags`/`deaths`/`netFrags`, the damage one for the damage metrics, and
  fire-derived `rl,lg,gl,ssg,sng,ng,sg,axe` for `shots`/`hits` — so `weapons=lava`
  is meaningful on `metric=deaths` and a 400 on `metric=shots`. On
  `/top-kills` it filters the **killing** weapon (which is also the burst's
  own weapon) against the burst-capable subset only — `rl,lg,gl,ssg,sng,ng,
  sg,axe,unknown`; a positional or environmental cause can never anchor a
  burst, so those tokens are a 400 here, not a silent empty list. Since schema
  v65 **`water` and `drown` are accepted interchangeably** in both the frag
  and damage vocabularies: it is one event the two logs spell differently, and
  a caller should not have to know which log backs which metric (additive —
  no emitted token changed). The pre-16.2 singular
  spelling `weapon` remains an accepted legacy alias; `weapons` wins when
  both are present.
- **`preMs`** (`/airgibs`) — the pre-hit look-back in ms (default 100,
  range `0..1000`): an airgib victim must read clear air (at or above the
  airborne height threshold) at every pre-impact sample of
  `[hit − preMs, hit − 40ms]` — the tick preceding the window decides when
  it holds no sample (old coarse-tick demos) — and no sample beside the
  hit may read ground contact. Samples near the damage stamp can already
  carry the rocket's own knockback, which over-reports height but cannot
  fake a grounded reading. `preMs=0` turns the pre-hit gate off (the
  pre-v72 rule); the default serves the stored list, any other value
  recomputes. Every response echoes the effective value.
- **`reducers`** (`/buckets`) — comma-separated `field=name` pairs, e.g.
  `reducers=h=min,a=last`. Names come from the reducer registry in
  RESULT_SCHEMA.md.
- **`from` / `to`** — match-relative **integer milliseconds**. Omit for
  the whole match. Honoured by `events`, `stream-slice`, `loc-trails`,
  `chat`, `region-control`, `aim`, `frags` / `damage`, and
  `top-windows` / `lives` / `top-kills` (on `top-kills` they bound the
  **kill**, not the burst behind it, which may reach back before `from`). A non-integer
  value 400s `invalid_param` with an `(integer milliseconds)` hint.
  Two endpoints do **not** clip to the window and you have to know which:
  on `/lives` it **selects** rows — lives *overlapping* it are kept, each
  still carrying its whole attribution window — and on `/top-windows` it
  bounds where a window may **start**, so a window anchored at `to` still
  runs the full `windowMs` past it (`to=1000&windowMs=30000` is "the best
  window anchored in the first second", covering up to 31 s). Neither is a
  size control; both are deliberate — clipping a top window's scoring at
  `to` while its stats block ran unclipped made `score` and the stats
  disagree. An **inverted** window (`from` > `to`) selects nothing
  everywhere: `/frags`, `/damage`, `/top-windows`, `/lives` and `/top-kills`
  all serve a 200 whose row array is empty — nested inside the usual
  envelope object (`frags`, `events`, `windows`, `lives`, `kills`
  respectively) — rather than rejecting the range or ignoring a bound.
- **`summary`** (`/frags`, `/damage`, `/aim`, `/items`, `/lives`) —
  `1`/`true` drops the big per-event log / sample arrays / phase timeline
  and returns only the aggregates. On `/lives`, where the rows *are* the
  answer, it instead keeps every row and every scalar and drops the per-row
  breakdown collections (`itemsTaken`, `locs`, `eventLocs`, `victims`,
  `byWeapon`, `damageByWeapon`) — ~40% of a 400-row 4on4 response. Note
  `itemsTaken` is then `null` on every row and says nothing about the demo;
  `measured.items` on the envelope remains the authority.
- **`dmg`** (`/damage`, `/top-windows`, `/lives`, `/top-kills`) — which damage **family** to return:
  `raw` (the unbound wire value, byte-stable pre-v54 shape), `bounded`
  (KTX's scoreboard reconstruction — armor absorbed + health capped to the
  victim's remaining health, in the raw field names), or `both` (raw plus
  additive `bounded` fields). The default is **`bounded`** for both
  summaries and the full log (effective damage is what the scoreboard
  means; raw overkill is the opt-in). A *defaulted* request on a demo
  whose reconstruction was skipped (`skipped:*` — midair / instagib /
  dmgfrags / the clan-arena family ca / wipeout / ra / lgc / race)
  falls back to `raw`; an *explicit* `dmg=bounded` there is a
  `422 bounded_unavailable`. Unfiltered bounded summaries source the
  per-player figures from KTX's exact scoreboard (`boundedSource: "ktx"`).
  `/top-windows`, `/lives` and `/top-kills` follow the same default and the
  same fallback, but reject **`dmg=both`** with a `400`: a score, a
  per-interval stats block and a burst each use one family, not two. On `/top-windows` it selects the family of
  the whole **stats block**, so it applies under **every** metric and not
  only the damage ones — a window picked by `metric=frags` still reports
  `damageGiven`, and that number has a family. The default, the fallback and
  the explicit-`bounded` 422 all hold under every metric alike. On
  `/top-kills` it selects the family of both `damage` and `returnDamage`, and
  since `damage` is the **ranking** key the two families can order the list
  differently. All three echo the resolved family and the demo's reconstruction state as
  envelope `dmg` / `boundedMode`, exactly as `/damage` does; read `dmg`
  rather than assuming the default took. Both echoes are absent only on a
  demo with no damage stream at all (`measured.damage: false`).

  **Damage provenance (`source`, schema v71).** `/damage` responses carry
  `source: "ktx"` when the log was decoded from the wire's damage stream
  (raw per-hit values measured; bounded is arithmetic over them) or
  `source: "reconstructed"` when the demo
  predates that instrumentation and the section was rebuilt from the
  state streams (health/armor deltas + beams / projectile flights / fire
  sounds / position tracks / the frag log). Reconstructed magnitudes are
  near-exact; attribution is best-effort — treat per-player match totals
  as ~1% estimates and prefer aggregates over individual hits (accuracy
  tables: `mvd-analytics/damagerecon/ACCURACY.md`). That ~1% assumes the
  recording carried the evidence, so a reconstructed section also carries
  **`coverage`** (schema v74) — `{kills, covered, ratio}`, the share of
  the frag log's weapon kills whose damage it accounts for. **Read it
  before quoting a reconstructed figure as a match total**: a small class
  of archive recordings barely broadcast the health/armor stat channel,
  and `ratio` is what distinguishes a quiet match from a section that is
  a fraction of the frag-log-visible one. Measured over the full
  10 702-demo sweep: 99.0% read ≥ 0.95 (median 1.000), 0.80% are that
  broken class (0.182 median, 0.488 worst), 0.18% fall between them
  spanning 0.500–0.944 — a hard bimodal core with a thin gradient tail,
  so read `ratio` as a magnitude, not as a two-valued flag. **Its
  denominator is the frag log**, which bounds what it can see: a loss
  that removes obituaries and damage evidence TOGETHER — a recording that
  starts late, a hole in the stream, a demo cut short — shrinks `kills`
  and `covered` in step and reads a clean 1.000 over the surviving
  fraction. Check the match clock and `/demoinfo` / `//finalscores`
  against that; `coverage` answers how much of the frag-log-visible match
  is in the section, not whether the recording was complete. It is also
  one number for the whole demo: it does not localize to a player, so a
  mid-band figure may be one unobserved victim beside a perfect one.
  Nothing is gated on it. Absent on `source: "ktx"`, whose coverage would
  be the constant 1, and on a reconstructed section whose frag log names
  no scoreable kill — there the absence means completeness was never
  assessed, not that it was zero. Whole-match like `source` itself — a
  filter carries it through unrescoped. Distinct from
  `boundedSource` above, which records the KTX-scoreboard substitution
  WITHIN a KTX-sourced summary. The raw temp-entity evidence behind the
  reconstruction (detonation points, blood telemetry) is itself
  browsable at `/streams/point-effects`. Only `/damage` (and `/player-stats` via
  its `src` labels) echoes provenance in-band; the other damage-shaped
  endpoints (`/top-kills`, `/lives`, `/top-windows`, `/buckets`,
  `/events`) serve reconstructed figures WITHOUT a marker on old demos —
  check `/damage`'s `source` (or `/player-stats` `src`) when the grade
  matters.
- **`time`** — match-relative **integer milliseconds**; **required** on
  `/state-at`. A non-integer value 400s `invalid_param` with an `(integer
  milliseconds)` hint.
- **`windowMs`** — integer milliseconds (`/buckets`, `/region-control`,
  `/top-windows`).
  ⚠️ On `/buckets` and `/region-control` it **defaults to 50 ms when
  omitted** — on a 20-minute match that is
  ~24,000 windows per field per player. Always pass an explicit
  `windowMs` sized to your question (the hosted MCP layer injects 5000).
  On `/top-windows` it means something different — the *length of each
  candidate window*, defaulting to 30000 — and it is the endpoint's main
  knob rather than a resolution setting: sweep it (5000 for damage bursts,
  30000 for hot streaks, 120000 for map-phase dominance).
  All time params are ms: `windowMs`, `from`, and `to` alike. An explicit
  `windowMs=0` is rejected with `400 invalid_param` (omit it for the default)
  on every endpoint that takes it. On `/top-windows` it is bounded on **both**
  sides — a negative value gets the same "must be >= 1" message a `0` does,
  and anything longer than the match duration is a `400` naming the bound
  rather than a window whose `end` had to be clamped.
- **There is deliberately no `limit`/`offset` pagination** on the
  per-demo endpoints: the data is time-series, so the size controls are
  the `from`/`to` window, `players`/`fields` scoping, and `summary`.
  `limit`/`offset` pagination applies only to the game-discovery
  endpoint `GET /v1/games/search` (page until `offset + count >= total`).
  There `limit` defaults to 20 (omit the param) and is capped at 1000 —
  the hub's own PostgREST page ceiling, so a bigger single page is
  impossible upstream. An explicit `limit=0`, a `limit` above 1000, or a
  negative `limit`/`offset` is rejected with `400 invalid_param` (no
  longer silently clamped).
  `/top-windows` and `/top-kills` also take a `limit`, but as a **ranking
  cut-off, not a page**: there is no `offset` and no next page, because the
  response is already the top *n* of a total order. It defaults to 10 (20 on
  `/top-kills`, whose per-weapon narrowing thins the list), caps at 200, and
  a **negative** value means uncapped. An explicit `limit=0` is rejected
  `400 invalid_param` — an omitted MCP integer argument arrives as `0`, so
  reading it as "uncapped" would make a forgotten argument look deliberate —
  and so is anything above 200, never a silent clamp. Its sibling cap
  `perPlayer` follows the **same** rule (omit for the default, negative for
  uncapped, explicit `0` rejected) so learning one teaches the other, and so
  do `/top-kills`' `gapMs` and `contestedMs` (out of range → a `400` naming
  the range, never a clamp). The score threshold on `/top-windows` is
  `minScore`, not `min` — the bare name read as a duration next to `/lives`'
  `minMs`; the damage threshold on `/top-kills` is `minDamage`.
- **`loc`** — `name` (default) resolves loc indices to names; `index`
  returns the raw `LocTable` index for index-based math (decode via
  `/loc-table`). Honoured by `buckets`, `events`, `stream-slice`,
  `state-at`, `loc-trails`.
- **`layout`** (`/buckets` only) — `column` (default, compact) or `row`.
  See the `/buckets` operation in `/docs`.
- **`regions`** (`/region-control` only) — `full` (default) ships each
  region's polygon `points` (~6 KB); `summary` strips them (name / locs /
  centroids kept); `none` omits the `regions` list entirely. `bucketStates`
  and `stats` are identical across all three. The hosted MCP layer defaults
  to `summary`.

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
Height/liquid no longer ride along `pos` — request each by code. The
**wielded weapon** (schema v72) is the opt-in `aw` field: the victim's
`STAT_ACTIVEWEAPON` `IT_*` bit — 1 SG, 2 SSG, 4 NG, 8 SNG, 16 GL, 32 RL,
64 LG, 4096 axe, 0 nothing held — which is a different question from the
`rl`/`lg`/… inventory intervals.
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

### 2.3 Caching (store, but revalidate every use)

Successful 2xx responses on the **per-demo** endpoints set:

```
Cache-Control: no-cache
ETag: "<sha>-v<schemaVersion>"
X-Schema-Version: <n>
X-Cache: HIT | WARM | MISS
```

`no-cache` does **not** mean "don't store" — it means a stored copy must
be revalidated before every use. Keep the body cached and send
`If-None-Match: "<etag>"` on each request: a demo's analysis never
changes for a given schema version, so the normal answer is a cheap
`304`. Because the ETag embeds the schema version, that mandatory
revalidation is also a version check — a deploy that bumps the schema
misses the ETag and the client re-downloads the new shape immediately,
instead of serving a stale one out of cache (which the previous
`max-age=86400, immutable` policy allowed for up to a day).

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
| 400 | `invalid_param` | malformed **or rejected** query parameter — bad number, malformed `reducers` pair, unknown `loc`/`layout` token, unknown `fields` code, unknown reducer name, an unknown enum value (e.g. `/events`/`/chat` `types`), a `weapons` token outside the endpoint's closed vocabulary, an out-of-range `limit`/`offset` (or explicit `limit=0`), or an explicit `windowMs=0` |
| 400 | `unknown_param` | an unrecognised query parameter **name** — the message names the offending key and the endpoint's accepted keys. The global `label` traffic-source tag is accepted everywhere |
| 400 | `missing_param` | required param absent (e.g. `time` on `/state-at`) |
| 401 | `unauthorized` | **auth mode only** — missing / invalid / revoked API key on a protected route. Carries `WWW-Authenticate: Bearer`. The body is deliberately generic and never says whether the key was absent vs revoked (see §2.5). |
| 429 | `rate_limited` | **auth mode only** — per-key rate limit exceeded. Carries `Retry-After: <seconds>`; wait that long and retry (see §2.5). |
| 404 | `demo_not_found` | hub has no row for this gameId |
| 404 | `map_unavailable` | no entity corpus / geometry for this map (`/v1/maps/{map}/…`) |
| 404 | `artifact_unknown` | no servable artifact of that name (`/v1/demos/{id}/artifacts/{name}`) |
| 422 | `demoinfo_unavailable` | non-KTX server or aborted match |
| 422 | `playerstats_unavailable` | parse degraded to no player streams. **Not** raised for a missing KTX block — `/player-stats` serves those normally |
| 422 | `metadata_unavailable` | no fullserverinfo / countdown centerprint |
| 422 | `frags_unavailable` | no frag log |
| 422 | `damage_unavailable` | no damage section at all: no KTX `mvdhidden_dmgdone` stream AND the reconstruction stood down (no player streams, or a midair/instagib/dmgfrags mode). Since schema v71 pre-instrumentation demos normally serve a **reconstructed** section instead of this 422 — check the response's `source` field (see "Damage provenance" in §2) |
| 422 | `bounded_unavailable` | `dmg=bounded` on a demo whose bounded reconstruction was skipped (`skipped:*` mode — midair / instagib / dmgfrags / ca / wipeout / ra / lgc / race) |
| 422 | `shots_unavailable` | no shot data (no weapon fires decoded) |
| 422 | `aim_unavailable` | no aim data (needs shots + position/view streams) |
| 422 | `locgraph_unavailable` | no position track |
| 422 | `los_unavailable` | `/los` on a map with no usable visibility BSP (no map name, BSP not provisioned, or a provisioned BSP that fails to parse) — never cached, so a retry after provisioning succeeds |
| 422 | `opening_unavailable` | no detected match start (`/v1/demos/{id}/artifacts/opening`) |
| 422 | `region_control_unavailable` | no region-control layout for this map |
| 422 | `airgibs_unavailable` | no timeline analysis (BSP-less maps return `[]`, not this) |
| 422 | `top_windows_unavailable` | no source stream for the chosen `metric` (frag log / damage stream / shot stream). Missing loc data only omits `locs`/`eventLocs` — it does not raise this |
| 422 | `lives_unavailable` | no per-player streams to segment into lives, **or** streams on none of which liveness was measurable (serving `lives: []` there would read as "nobody ever lived"). A missing damage stream does **not** raise it — `/lives` serves those demos with the damage fields at measured zero |
| 422 | `top_kills_unavailable` | no frag log, no damage log, **or** no measurable liveness — the burst walk is clipped by the victim's current life start, and without that clip the top-ranked rows are exactly the contaminated ones |
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
non-KTX server has no `demoinfo` (and only a `skipped:*`-mode or
stream-less demo lacks `damage` — the reconstruction covers most
pre-instrumentation ones), a demo without a position track
has no `loc-graph`, a map without a region layout has no `region-control`.
These are **expected** for some demos; treat them as "this panel is
unavailable for this demo", not a hard failure. Better still, **don't
probe at all**: `/overview`'s `available` block carries one flag per view,
each mirroring the very predicate behind that view's `422`, so a `false`
there is exactly the `422` you would have received. It is the only way to
learn the BSP-derived ones (`height`, `liquid`, `los`) — those turn on the
server's map provisioning rather than on the demo, so the same demo answers
differently on two deployments. Use it (with `errors` and, for
reader-level gaps, `parseWarnings`) to hide panels up front.

When almost every flag reads `false` at once, look at **`noMatch`**
(schema v74) before concluding anything went wrong — and read it before
`errors` / `parseWarnings` in general, since it decides whether those
describe a partial match or nothing at all. It is present exactly when the
`streams` block is absent — 2% of the QuakeWorld archive — and it names the
reason: `midMatchRecording` (the recording starts after the match began),
`matchStartUnannounced` (the server ran a match and none of the four
match-start signals the reader knows produced an analyzable result — rare
since schema v75, which salvaged every demo carrying this reason in the
archive sweep), `noMatchDeclared` (no match declaration
this pipeline can see, yet kills were parsed — usually unmanaged play on a
mod with no match state, see `gameDir`, but possibly a managed match on a
mod whose declarations we cannot read), `noPlayRecorded` (the same absent
declaration and no kills parsed — usually an idle or aborted server) or
`demoUnreadable` (a truncated read — the only reason that ALSO means a
failure, with the reader's message in `errors`). `detail` is the same
verdict as a sentence you can show a user; it is unstable display text, so
never parse it — every fact in it is a structured field beside it. Such a
demo is not a failed parse and retrying it changes nothing. Endpoints whose data
is always computable or list-shaped — `/items`, `/backpacks`,
`/weapon-pickups`, `/chat` — instead return **`200` with an empty body**
when there's nothing, never `422`.

**Null vs `[]`.** A governed response's **top-level array is never
`null`** — an empty result is `[]` (`/events`, `/stream-slice` `players`,
`/loc-trails` `players`, `/top-windows` `windows`, `/lives` `lives`,
`/top-kills` `kills`, and
the list endpoints above). Nested arrays
deeper in a body may still be `null`; check before iterating. One nested
`null` is **deliberate and load-bearing**: `/lives`' per-row `itemsTaken`
is `null` when the demo carries no item timeline at all and `[]` when it
has one and that life took nothing — the same signal as the envelope's
`measured.items`. (Under `/lives?summary=1` it is `null` on every row and
carries no signal at all; read `measured.items`.) `/stream-slice`'s
per-player `alive` is the same kind of signal: `null` = liveness was not
measurable, `[]` = measured and never alive in **this window**, `[…]` =
the lives. `/state-at`'s `alive` carries the same `null` as a scalar.

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

### 2.7 API versioning and stability

**`/v1` is not frozen yet, and this section says so plainly rather than
promising a stability that has not been earned.** Most change here is
additive — new endpoints and new response fields appear all the time. But a
schema upgrade **can still withdraw a documented field or change what one
means, inside `/v1`**.

That is not hypothetical. Schema **v70** removed `topKills`, `topStreaks`,
`topPowerups` and `hasRegionControl` from `/overview`, after they were
measured at 78-88% of that response and found to be copies of `/top-kills`,
`/lives` and `/events` — a strictly better endpoint, and a break for anyone
who was reading those fields.

So: build against this API, and expect to re-check it occasionally. Don't
pin a response shape you cannot revisit.

Two version numbers move independently, and they mean different things:

- **The `/v1` path prefix is the surface, not yet a frozen contract.** It
  is where everything lives and it is not going to move under you without
  a version bump and a written-up reason — but "without a version bump" is
  the guarantee today, not "never".
- **`schemaVersion` (the ETag `-v<n>` suffix, `X-Schema-Version`, and the
  OpenAPI `info.version`) is a regeneration counter.** It ticks on *every*
  observable change to the analysis output, most of them purely additive —
  a new field, a new endpoint, a new event type, a new enum value. It keys
  caches and ETags, so bumping it invalidates stale client caches
  automatically. **While `/v1` is still moving it is also your break
  signal**: when it changes, read the release notes before assuming
  nothing you depend on moved.

#### What you can rely on today

- **Nothing changes silently.** Every observable change bumps
  `schemaVersion` and is written up in
  [RELEASE_NOTES.md](../RELEASE_NOTES.md) against the version that carries
  it, including what was removed and why.
- **The spec is not aspirational.** `openapi.yaml` is generated from the
  running code, drift-tested against it, and validated against real golden
  responses in CI — so if the spec and the server disagree, CI fails
  before you do.
- **The MCP surface moves in lockstep.** `mvd-mcp` forwards every call to
  this API and the two deploy together, so a REST change and its tool
  change land at the same moment; you will never see a shim describing an
  endpoint that no longer behaves that way.
- **A correctness fix is still not a break** (see below) — those keep the
  field's name and type.

#### Where this is heading

The intended destination, in the near future, is the contract this section
used to claim outright:

- **`/v1` grows, it doesn't shift.** Documented fields and endpoints don't
  change meaning, change type, or disappear.
- **A breaking change ships as a new route** (`/v2/<endpoint>`) served
  **alongside** the old one, not as a replacement, so nothing forces a
  same-day migration.
- **Old routes retire on notice**: a minimum of 8 weeks after the
  deprecation is announced, and in practice only once measured usage of the
  old route has drained — every request carries a key, so "is anyone still
  on this?" is measured rather than guessed.

Most of the machinery that policy needs already exists — versioned release
notes, a drift-tested spec, per-key usage measurement, lockstep deploys.
What is missing is the decision to stop moving the shape, and that is worth
earning rather than announcing early: the surface is still finding the
right cut, and v70 is what that looks like in practice. **Read the rules
above as the destination, not as a promise already in force.**
- **A correctness fix is not a break.** When a field's *value* changes
  because it was being computed wrongly, the field keeps its name and its
  type, and its documented meaning is not *replaced* — at most it is
  **narrowed to the semantics the field was always meant to have**, with
  the documentation updated to say so plainly. Schema v64 is the pattern:
  loc and region occupancy stopped counting dead players and unobserved
  time, so "time spent in a loc" is now documented as "**alive, observed**
  time spent in a loc" — the same quantity the field was always intended
  to report, finally measured. Such fixes ship inside `/v1` and ride
  `schemaVersion` like any other regeneration, and are called out in
  [RELEASE_NOTES.md](../RELEASE_NOTES.md) with the direction and rough
  magnitude of the change. Giving a field a *different* meaning is a
  break and goes through `/v2`. If you have pinned a golden file, expect
  it to move; if you have pinned a *shape*, it won't.

#### What is, and isn't, covered

**Covered by the contract** — the documented request parameters, the
documented response fields and their types, documented enum members, and
documented status codes.

**Not covered, and free to change without a version bump** — the ordering
of array elements where no order is documented, rounding of derived
floating-point values, whether an empty/zero field is omitted
(`omitempty`) or emitted, rate-limit thresholds, and which demos return
`422 <section>_unavailable` (a demo's capabilities are a property of the
recording, not of the API).

**Adding a member to a default set is additive**, even though it changes
the rows you receive. Schema v58 added the `demomark` event type to
`/events` *and* to its default type set, so a caller that omits `types`
simply began seeing new rows. Filter explicitly if you need a fixed set.

**Changing the default *value* of a parameter is a break**, and goes
through `/v2`. Widening a default set leaves everything you already
received in place and adds to it; changing a default `windowMs`,
`limit` or `minDwellMs` instead makes the default-path response
*different* — a caller who never passed the parameter gets other numbers
without having opted into anything, which is the same reason an altered
default *behaviour* is a break.

**The MCP tool surface follows this policy too.** `mvd-mcp` forwards
every call to `mvd-api` and the two deploy in lockstep, so tool names,
tool parameters and the meaning of their results move under exactly the
rules above — including the part where, until the freeze, a schema
upgrade can withdraw or reshape one. v70 changed `getOverview`'s result
and its tool description in the same deploy as the REST route. MCP tool *defaults* may differ from the REST
defaults where an agent-facing default is more useful (e.g.
`getLocTrails` defaults `minDwellMs` to 250 while REST defaults it to
0); each such difference is documented in the tool description and is
itself covered by the rule above.

#### What your client must do in return

1. **Ignore unknown fields and unknown enum values.** Never treat an
   unrecognised field as an error — additive evolution depends on it.
2. **Treat [`/openapi.yaml`](/openapi.yaml) as the contract.** Undocumented
   behaviour you happen to observe may change without notice.
3. **Pin behaviour to `/v1`, treat `schemaVersion` as a cache key** — not
   as a compatibility signal — and let new fields flow through unread until
   you choose to consume them.

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

- **Native-rate positions (server-configured cadence, typically 13-40 ms per sample — see below)** come **only** from
  `/stream-slice?fields=pos`. `/buckets` and `/state-at` down-sample
  position to one sample per window / instant.
- **Spawns & deaths**: `/events?types=spawn,death` is the authoritative
  log. `/stream-slice?fields=sp,d` gives the raw ms timestamp arrays.
  `/buckets?fields=sp,d` only yields a per-window bool (lossy — collapses
  a same-window death+respawn).
- **Liveness is served, not re-derived.** Never compute "was X alive at
  t" from the `sp`/`d` markers: `/stream-slice` carries `alive` (the
  stored lives, clamped to the window) and `/state-at` carries `alive`
  (`true`/`false`/`null`) on every row, whatever `fields` asks for. The
  obvious re-derivation — "last spawn after last death" — latches on a
  death and respawn sharing a millisecond and reports the player dead for
  the rest of that life (100.7 s of one player's match, measured).
- **A `/state-at` position can be arbitrarily old.** The row reports the
  nearest position sample with no staleness bound (deliberate, and
  unchanged), so it also reports **`posAgeMs`** — `time` minus that
  sample's timestamp. `/region-control`, `/loc-graph` and `/loc-trails`
  drop a sample once its age reaches 250 ms; apply the same rule to
  `posAgeMs` and the four endpoints answer "where was X" consistently.

### Aggregates: whole match, chosen stretches, or per life?

A second family answers *"how did this player do?"* — they differ in how
the match is cut up before the counting starts:

| You want… | Use | Why |
|---|---|---|
| **Whole-match** totals + the raw per-event logs | **`/frags`**, **`/damage`**, **`/player-stats`** | Authoritative stored aggregates; `/player-stats` is the one-call scoreboard. `/frags` carries `killsMeasured` — `false` means its zero `kills` are not measurements. |
| A **fixed grid** of equal windows (charts) | **`/buckets`** | Every window, in order, whether or not anything happened in it. |
| The **best stretches** — "when was X hot?" | **`/top-windows`** | Ranked windows, chosen for you by a metric you pick — fixed-length (`windowMs`) or gap-delimited (`mode=gap`, `gapMs`). |
| The **natural unit** — one row per spawn-to-death life | **`/lives`** | Variable-length, and a partition, so per-life sums reconcile with the per-event **logs** (not necessarily the `byPlayer` scoreboards — see below). |
| The **hardest single kills** — "find me the clips" | **`/top-kills`** | Ranked kill BURSTS: for each kill, the run of killing-weapon hits that produced it. |

Concrete consequences:

- **`/top-windows` finds the windows; `/buckets` enumerates them.** Reach
  for top windows when the question is "which 30 seconds" rather than
  "what did each 30 seconds look like". Under the default `mode=fixed`,
  `windowMs` is the whole knob
  — 5000 for damage bursts, 30000 for hot streaks, 120000 for map-phase
  dominance — so sweep it rather than expecting one right value.
- **`mode=gap` lets the play decide the length.** A gap window is a
  maximal run of scoring events no more than `gapMs` apart, scored by
  their sum — "the stretch lasted as long as he kept doing damage, not as
  long as a stopwatch said". Use it when a fixed length is doing silent
  editorial work (a 4-second double kill and a 10-second rampage both
  reporting as "a 10 s window"). `gapMs` is **required** there and has no
  default, because the metrics' cadences differ by an order of magnitude:
  start at **~10000 for the frag metrics** and **~3000 for the damage and
  shot metrics**. Each mode rejects the other's knob with a 400, and the
  envelope always echoes `mode` plus exactly one of `windowMs` / `gapMs`.
  Gap rows are disjoint per player, `end` is the run's last event (a lone
  event is a legitimate row with `durationMs` 0), and a run may span the
  player's own death — `/lives` is the per-life view. There is still
  deliberately no adaptive mode with a tuning constant in it.
- **`/top-windows mode=gap` vs `/top-kills`** — adjacent, not
  substitutes:

  | | `/top-kills` | `/top-windows mode=gap` |
  |---|---|---|
  | Unit / row | one kill + the burst that produced it | a stretch of sustained output |
  | Ranked by | per-kill burst damage ("hardest kill") | the metric's sum ("best stretch") |
  | Kill required? | yes (kill-anchored) | no — a 300-damage stretch with zero kills is a row |
  | Life boundary | clipped at the killer's life start | not clipped; a run may span the player's own death |
  | Rows overlap? | yes (each kill its own walk) | never (disjoint by construction) |
  | Extras | `returnDamage`, victim weapon, `maxGapMs` narrowing | the full per-interval stats block |

  They meet only in the degenerate regime: at
  `metric=frags&weapons=rl&gapMs=3000`, RL inter-kill gaps run
  p50 ≈ 11.8 s, so 85% of clusters are singletons and you have
  `/top-kills`' unit with none of its content. At frag gaps of
  8000–15000 gap mode answers what `/top-kills` structurally cannot
  (multi-kill stretches), and at damage gaps around 3000 it is the
  rampage view.
- **Rows tied on `score` are ranked by a fixed complementary metric**
  (`damageGiven` under `frags`/`netFrags`/`shots`/`hits`, `frags` under
  `damageGiven`/`netDamage`, `damageTaken` under `deaths`, `deaths` under
  `damageTaken`), then by start time. Ties are the common case — on
  `metric=frags` most of a page holds the same small integer — and there is
  no parameter for it. The secondary is read unscoped and in the response's
  own damage family, so it is exactly the same-named field of the row's
  stats block: `weapons=` scopes the score, never the tie-break.
- **Only `/lives` reconciles per interval.** Lives partition the match, so
  summing a player's per-life `kills` / `deaths` / `shots` gives what the
  `/frags` `frags[]` rows hold for them, and summing `damageGiven` /
  `damageTaken` gives `/damage`'s **non-summary** aggregate for them
  (not its `events[]` rows — a telefrag or stomp folds its value into the
  totals without a per-hit row — and not an unfiltered bounded *summary*,
  which serves KTX's scoreboard instead of the reconstruction). That is
  *not* always the same as
  the `byPlayer` scoreboards, which have other sources: a death detected
  from `DF_DEAD` / `STAT_HEALTH` with no obituary counts on the scoreboard
  and leaves no log row for any life to carry, so per-life deaths can come
  in under `/frags` `byPlayer` (and `/frags`' own log and `byPlayer`
  already disagree there). It also holds only on an **unfiltered** call,
  and `durationMs` is alive time while the counts cover a slightly wider
  attribution window — each life publishes that window as
  `attrStart`/`attrEnd`, so divide by `attrEnd - attrStart` when you want an
  exact rate. Top windows overlap nothing per player but cover
  only part of the match, so they never sum to anything.
- **Both share one stats block** (`IntervalStats`) and both carry a
  `measured` block on the envelope —
  `{frags, damage, shots, locs, items, liveness}`. Every numeric stat is
  emitted even at zero, so read `measured`, never a field's absence, to
  tell an unmeasured source from a measured zero. Two of the flags are
  worth knowing individually: `measured.frags` is the demo-global
  **kill-attribution** verdict (`/frags`' own `killsMeasured`), not merely
  "a frag log exists", and `measured.liveness` says whether the
  spawn-to-death segmentation was measurable at all — `/lives` 422s when it
  is false, so you only ever see it false on `/top-windows`.
- **`/top-kills` ranks kills, not stretches.** It is the third cut of the
  same primitive — time windows, lives, and now the kill itself — but it is
  not an interval reduction and carries no stats block; each row is the burst
  that produced one kill. Two things to know before you consume it: the burst
  is the **killing weapon's** run (on ~8% of kills that understates a
  mixed-weapon kill — that is the endpoint's question, and `/damage` answers
  the other one), and `gapMs` is a **capture** gap you narrow client-side with
  each row's `maxGapMs`, never by lowering `gapMs` itself. Positional kills
  (telefrag/stomp) produce no row; kills by an already-dead killer stay in.
- `/top-windows`, `/lives` and `/top-kills` are computed on demand from the
  stored event logs — no extra parse, and cached under the same per-demo ETag
  as everything else.

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
  players, top streaks/powerups, degraded flag). **Branch on
  `gameMode.teamBased`, never on the mode string.** `overview.mode` is the
  server's own DISPLAY spelling (`Duel`, `FFA`, `Clan Arena`) and there are
  five such vocabularies in the archive with no translation between them;
  `gameMode` (schema v75) is the normalised verdict, with a `sources` block
  naming which vocabulary decided each field. When `teamBased` is false —
  every duel, FFA, race — the match is laid out with ONE SIDE PER PLAYER:
  `teams` is one row per player, every `players[].team` equals the player's
  own name (the raw clan tag rides on `match.players[].rawTeam`), and there
  are no team aggregates or region control to render. That is the same shape
  duels have always returned, so a client that already handles a duel
  scoreboard handles FFA with no new code.
- **Kill feed with obituaries** → `GET /events?types=frag` (use
  `/frags` if you need the `isSuicide`/`isTeamKill` flags instead).
- **Score-over-time line** → `GET /events?types=frag`, accumulate
  `delta` client-side; or `/buckets?fields=sp,d` for activity density.
- **Health/armor chart for a player** → `GET /buckets?fields=h,a&windowMs=1000&players=X`
  (smooth grid) or `/stream-slice?fields=h,a&from=…&to=…` (every change).
- **Map replay / movement trails (native cadence)** → `GET /stream-slice?fields=pos&players=X&from=…&to=…`
  — the only native-rate position source. Stitch windows for the full
  match. Remember positions are **int32 ms**.
- **Aim arrows / sightlines / "who's looking at whom" (native cadence)** →
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
- **"When was X hot?" / match highlights** →
  `GET /top-windows?metric=netFrags&windowMs=30000` (add `perPlayer=1` to
  spread the list across the roster, `players=X` for one player's
  ranking, `limit=-1` for all of them). `weapons=` scopes the *scoring*
  events, so `metric=damageGiven&weapons=lg` finds the best LG stretch
  and still reports everything that happened in it.
- **Highlight reel ("find me the clips")** → `GET /top-kills` (20 rows at
  the documented defaults; `/overview` stopped inlining them in v70), then filter
  client-side — keep the rows whose `maxGapMs` is within your weapon's
  cadence (LG ≈ 1200 ms, RL ≈ 2300 ms), whose `victimWep` says the victim was
  armed, and whose `returnDamage` clears whatever *you* call contested. That
  narrowing is exact per kept row: a row with `maxGapMs <= g` carries its
  gap-`g` value verbatim, an over-merged row drops rather than truncates —
  `?gapMs=g` is the exact walk when the remainders matter.
  Use `GET /top-kills` when you want more than 20 rows or a server-side
  filter — `weapons=rl&minDamage=150&limit=50`, or `gapMs=` for an exact
  recompute at another capture gap.
- **Per-life breakdown ("how did that life go?")** → `GET /lives`
  (`minMs=1000` drops spawn-frag lives). One row per spawn-to-death run
  with `endReason`, `spawnLoc`/`deathLoc`, `killedBy`, `itemsTaken`,
  `weaponsHeld` and the same stats block `/top-windows` uses. A whole 4on4
  match is ~400 rows — narrow with `players` / `from` / `to` / `minMs`, or
  ask for `summary=1` first and re-request the lives you care about in
  full.
- **"Who controlled QUAD?"** → `GET /region-control?windowMs=10000`,
  read `stats.QUAD.byPlayer`.
- **Loc heatmap / movement graph** → `GET /loc-graph` (aggregate) or
  `/loc-trails` (per-player sequence with dwell).
- **Draw the map (items, spawns, teleporters as overlays)** → `GET /v1/maps/{map}/entities`
  (map name from `/overview`); add `GET /v1/maps/{map}/geometry` for
  floor polygons to render underneath.
- **"How did each player do?" (one call)** → `GET /player-stats`.
  Corrected scoreboard + damage + accuracy + pickup tallies + possession
  time in one row per player and per team, on **every** demo including
  ones with no KTX block. Prefer it over stitching `/demoinfo` +
  `/frags` + `/damage` yourself; read each family's `src` to see whether
  a number came from KTX, was derived here, or was reconstructed.
  `score.maxSpree` / `score.maxQuadSpree` (longest kill run between
  deaths, and while holding the quad) are the derived equivalent of the
  KTX block's `spree.max` / `spree.quad` — so they answer "best streak?"
  on the pre-ktxstats half of the archive too. They are never overlaid
  from KTX and are gated with `kills`; a team row carries the best any
  member ran, not a sum. They deliberately exclude the self-kill that
  KTX's own gate lets bump a streak wherever teamplay is off, so a duel
  with suicides reads 1 lower per affected streak than the KTX block.
- **"Are these two rows the same person?" / "which userid do I `track=`
  at time t?"** → `GET /player-stats`, read `identity` and `sessions[]`.
  A player who reconnects while their old connection is still spawned is
  renamed `(N)<name>` by mvdsv and scored as two players by KTX; this API
  reproduces that split faithfully, and equal `identity` values are what
  tell you the two rows are one human. `sessions[]` is the per-connection
  `{startMs, endMs, slot, userId, name}` window list — use the `userId`
  whose window contains your instant, rather than `/overview`'s single
  per-player id (which is the last connection that had play). `identity`
  is **demo-local**: never persist it or compare it across demos; the
  cross-demo identity is `login`. Every other name-keyed view (`/lives`,
  `/top-windows`, `/frags`, `/damage`, `/buckets`) joins to these rows by
  the player **name** — with one exception: when two identities share a
  display name, `/player-stats` and the stream views suffix both rows `name#slot`
  to keep them apart while the frag and damage logs keep the bare name, so
  strip the `#…` suffix to join (and expect the answer to cover both
  same-named players).
- **"How long did X hold the RL / RA?" / "how long with no armor?"** →
  `GET /player-stats`, read `hold.weapons.rl` / `hold.armor.ra` /
  `hold.armor.none`. This exists nowhere else: KTX never writes weapon
  hold time into the demoinfo block, and its armor time overcounts (its
  clock keeps running after the armor is chewed to zero), so ours reads
  lower on purpose. Use the emitted `shareAlive` / `shareMatch` rather
  than dividing yourself — on a team row the denominators are team time.
- **Weapon effectiveness** → `GET /player-stats` (accuracy + per-weapon
  pickups + hold time), `GET /demoinfo` (KTX's own verbatim numbers, the
  audit trail), or `/weapon-pickups` (kills-before-next-death).
  Check `accuracy.src` before you trust the hit side: on a demo with no
  wire damage stream `accuracy.byWeapon[].hits` is filled from the aim
  reconstruction tier and the family reads `"reconstructed"` — a weaker
  evidence grade, covering only `lg` / `sg` / `ssg` / `axe` / `rl` /
  `gl`, with `hits` still ABSENT on `ng` / `sng` (not recovered, which
  is not the same as no hits). `attacks` is shot-derived on every demo
  and matches KTX to the row on the single-projectile weapons. Since
  schema v75 EVERY family answers KTX's own question per weapon:
  `sg`/`ssg` count pellets on both sides of the ratio, `rl` counts
  direct impacts (`reconstructed` since v74, `derived` since v75, both
  within 1.3% / 0.02% of `/demoinfo` in aggregate), and
  `lg`/`ng`/`sng`/`axe` count a connecting fire, which is KTX's own
  event for them. ONE exception: `gl` on a `derived` family still counts
  any damage path, because a grenade that touches a player explodes and
  every damage row the wire then carries is flagged splash — the touch
  KTX counts is simply not on the wire. Read
  `accuracy.byWeapon[].hitsConvention` rather than re-deriving that rule
  — `anyDamage` | `directImpact` | `pellets`, present whenever `hits`
  is, per WEAPON because one `src: "ktx"` row uses all three at once.
  Two rows are comparable exactly when weapon and convention match, so
  gate any cross-demo or cross-era aggregation on it; ignoring it turns
  a ~4x definition change on `rl` into a trend.
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
