# Analytics Schema & API — Report and Design Review

**Scope:** the `mvd-analytics` Result schema (now **v36**) and the
`mvd-api` REST surface (**22 endpoints**) built on top of it.

This document is three things: a human-readable map of what the schema and
API contain (§1–§2), a verification that the API matches the schema (§3),
and a careful review of what could be redefined to make the contract
cleaner (§4). The authoritative field-level references remain
[`mvd-analytics/RESULT_SCHEMA.md`](mvd-analytics/RESULT_SCHEMA.md) (shapes)
and [`mvd-api/API.md`](mvd-api/API.md) (HTTP surface); this report sits
above them.

**Implementation status (schema v36).** R1, R3, R4, R5, R6, R7, R8, R9 from
§4 have since been **implemented**; the per-item text below is kept as the
rationale of record and marked ✅ where done. Still open: **R2** (split
version surfaces), **R10** (a deliberate *keep* — no change wanted), **R11**
(the seconds/ms split — a deliberate trade-off left as is).

| Item | Status | One-line |
|---|---|---|
| R1 | ✅ done | section filtering moved to `view.Frags/Damage/Items/Backpacks/WeaponPickups/Chat` |
| R2 | open | split result-vs-view version surfaces |
| R3 | ✅ done | documented `422`-unavailable vs `200`-empty rule (`view.ErrUnavailable`) |
| R4 | ✅ done | `weapon` is a CSV set on every endpoint (incl. `/backpacks`) |
| R5 | ✅ done | `/region-control` accepts `from`/`to` |
| R6 | ✅ done | `view_error` folded into `invalid_param` |
| R7 | ✅ done | query-param names are case-insensitive (`ciGet`) |
| R8 | ✅ done | demo-scoped `/map-entities` removed; per-map endpoint kept |
| R9 | ✅ done | `MatchResult.startTime`/`endTime` removed (schema v36) |
| R10 | keep | frag triple-representation is intentional — no change |
| R11 | open | seconds-envelope / ms-array split kept (deliberate) |

---

## 1. The schema, for humans

One demo in → one `result.Result` JSON out. Every top-level key is one
analyzer's output, and **`omitempty` means "this demo didn't carry the
signal for it"** — no KTX server ⇒ no `demoInfo`/`damage`; no BSP ⇒ no
floor-height/liquid columns; no frag log ⇒ no `frags`. Think of it in four
tiers:

**Tier A — "what was this match" (cheap summaries).**
`match` (map, duration, corrected scoreboard), `metadata` (server cvars +
parsed match settings), `demoInfo` (**KTX's own end-of-match stats,
verbatim and never transformed** — authoritative for accuracy/damage/item
counts when present).

**Tier B — event logs (chronological lists).**
`frags` (kill log + per-player/per-weapon tallies), `messages` (frags +
chat, with obituary text), `damage` (per-hit log + attacker→victim matrix +
the "EWep" victim-weapon buckets, from the KTX `mvdhidden_dmgdone` stream),
`items` / `backpacks` / `weaponPickups` (pickup timelines + weapon
effectiveness). `timelineAnalysis` holds the *derived* event streams
(frag / death / kill / powerup / streak / airgib) plus map metadata
(`locTable`, `locationData`, `regionControl`).

**Tier C — the canonical state store (`streams`).** The heart of the
schema. For every player, every tracked field — health, armor, ammo,
weapons held, powerups, **position**, loc, spawns/deaths — recorded *at the
rate it actually changed* (sparse change-streams + half-open intervals + a
columnar position track). The position track gained optional per-sample
columns across v24–v35: height-above-floor `h`, liquid `lq`, view angles
`vp`/`vya`, velocity `vx`/`vy`/`vz`. `streams.movers[]` (v35) adds
lift/door/plat poses. `streams.global` holds the match window + the
wall-clock anchor.

**Tier D — static map data (`mapEntities`).** The map's *designed* layout
(item spawns, spawnpoints, teleporters, buttons) from an offline BSP corpus
— identical for every demo on a map.

**Two cross-cutting facts every consumer must internalize:**
- **All stored times are `int32` milliseconds.** Seconds is a *derived*
  view (see §4 R11).
- **Loc names are interned**: streams store integer indices into
  `timelineAnalysis.locTable`; resolve via `/loc-table` or `loc=name`.

The standout architectural decision is Tier C + the on-demand `view`
package: native-rate truth is stored **once**, and every aggregation
(50 ms / 1 s buckets, point-in-time state, loc trails, region control) is
*derived on demand* rather than baked in. That is what lets WASM, CLI,
REST and MCP all serve identical shapes from one code path.

---

## 2. The API, for humans

`mvd-api` is a thin, cacheable shell over the schema. A demo is addressed
by `gameId:NNNN` (hub.quakeworld.nu — fetched & parsed on first touch) or
`sha:HEX` (a warm cache entry). Everything except `loadDemo` is served from
the cached result, sub-millisecond, immutable, ETag'd.

The 22 endpoints fall into five intents:

| Intent | Endpoints | Returns |
|---|---|---|
| **Identity / health** | `POST /v1/demos/{id}` (loadDemo), `GET /healthz`, `GET /v1/version` | identity + schema version |
| **"Whole-match" summaries** | `/overview` (curated; call first), `/demoinfo`, `/metadata` | `Overview`, `result.*Result` |
| **Event logs** (filterable by `players`/`weapon`/`time`/`kind`) | `/frags`, `/damage`, `/chat`, `/items`, `/backpacks`, `/weapon-pickups`, `/loc-graph` | `result.*Result` (filtered) |
| **Per-player state over time** (the `view` layer) | `/state-at`, `/buckets`, `/stream-slice`, `/events`, `/loc-trails`, `/region-control`, `/loc-table` | `view.*View` |
| **Static map data** (no demo needed) | `/v1/maps/{map}/entities`, `/v1/maps/{map}/geometry` | `result.MapEntitiesResult`, `mapgeom.MapRegions` |

**The one piece of design cleverness worth flagging:** four endpoints read
the *same* underlying streams in different shapes — pick by what you're
drawing. `/state-at` = one instant (scrubber tooltip); `/buckets` = a fixed
grid (charts/heatmaps); `/stream-slice` = every raw transition (replay, and
the **only** native-rate ~77 fps position source); `/events` = a discrete
tagged log (kill feed, authoritative spawns/deaths). `API.md` §3 documents
this trade-off well.

The MCP server (`mvd-mcp`) and the WASM build call the **same** `view.*`
functions, so all surfaces stay in lock-step — that is the reason there is
a single `CurrentSchemaVersion` (and the tension R2 raises).

---

## 3. Does the API match the schema?

**Endpoint coverage: yes.** All 22 registered routes (`router.go`) are
documented in `API.md`; every demo endpoint returns either a
`result.XxxResult` (documented in `RESULT_SCHEMA.md`) or a `view.XxxView`.
The doc split is clean: `API.md` owns the HTTP surface (params, units,
caching) and *links* to `RESULT_SCHEMA.md` for shapes rather than
restating them. No endpoint returns a shape that contradicts the schema
doc.

**The contract docs had drifted from the code; fixed in the accompanying
commit:**

| Drift | Was | Now |
|---|---|---|
| `view_error` (400) undocumented | `API.md` attributed unknown-field/reducer to `invalid_param` | added `view_error` row; `invalid_param` re-scoped to param **value** parse failures |
| `map_unavailable` (404) not in central error table | inline only | added to §2.4 table |
| `Result` struct listing in `mvd-analytics/README.md` | missing `Damage` and `Streams` | both added; stale `TimelineAnalysis` comment fixed |
| Stale version numbers | `README.md` "32", `mvd-analytics/README.md` "18", example payloads "20"/"23" | all → `35` |

(Evidence: the server emits `view_error` at `mvd-api/handlers.go:680,688,
724,760,794,835,874` and `map_unavailable` at `mvd-api/map_handlers.go:45,
60,71`.)

---

## 4. Design review — what could be redefined

Ordered by structural weight. Each item: **problem → evidence → impact →
recommendation → cost**. None are applied.

### High value — structural

#### R1. Result-section filtering lives in the REST handler, not in a shared `view` layer

**Problem.** The "event-log" endpoints each re-implement player / weapon /
time / kind filtering *inside the HTTP handler*, while the per-player-state
endpoints push everything into pure `view.*` functions. The contract is
therefore split: the streaming surface is reusable, the log surface is not.

**Evidence.** `mvd-api/handlers.go`: `handleFrags` (190–252), `handleDamage`
(265–375, ~90 lines of bespoke matrix/by-player/scoreboard filtering),
`handleItems` (508–556), `handleBackpacks` (462–486), `handleWeaponPickups`
(567–595), `handleChat` (390–437). Compare `handleBuckets`/`handleEvents`/
`handleStreamSlice`/`handleStateAt`/`handleLocTrails`, which are ~20-line
param-parse-then-delegate wrappers around `view.Buckets`/`view.Events`/…

**Impact.** `mvd-mcp`, WASM, the planned `corpusview`, or any new consumer
that wants "frags for player X involving weapon Y" must re-derive this
logic — it is not part of the stable, tested `view` contract. The filters
also can't be unit-tested in isolation from HTTP.

**Recommendation.** Lift them into pure functions mirroring the view layer:
`view.Frags(r, FragFilter{Players, Weapons})`, `view.Damage(r,
DamageFilter{…})`, `view.Items(r, ItemFilter{…})`, etc. Each handler then
becomes parse-params → call view → encode, exactly like `/buckets`. One
filter implementation, shared across REST/MCP/WASM, independently testable.

**Cost.** Medium. Mechanical extraction + new tests; no wire-shape change
(outputs are identical), so **not** a schema bump. This is the single
biggest consistency win available.

#### R2. One version number conflates two independently-changing surfaces

**Problem.** `CurrentSchemaVersion` bumps for **both** `Result`-struct
changes *and* view-wire changes. A consumer can't tell from the number
*which* surface moved, and the ETag of an endpoint it uses changes even
when only an unrelated surface changed.

**Evidence.** `result/result.go` header comment, e.g. v11: *"The Result
**structure is unchanged**; this bump versions the outward view/query wire
surface … cached view responses are invalidated."* The ETag is
`"<sha>-v<schemaVersion>"` (`handlers.go:66,98`), so a view-only bump
invalidates `/overview`'s client cache too. Recent history shows the view
surface is now the faster-moving one (v11 columnar, v31 view codes, v32
velocity were view-side).

**Impact.** Over-aggressive cache invalidation; coarse feature-detection.
A client pinning `schemaVersion` to gate a feature can't distinguish "the
section I read changed" from "an endpoint I never call changed."

**Recommendation.** Either (a) split into `{ resultSchemaVersion,
viewSchemaVersion }` surfaced in `/healthz`, `/overview`, and the ETag
(`"<sha>-r<R>-v<V>"`), letting each endpoint advertise the version that
actually governs it; or (b) keep one number but publish a small capability
map (`{ feature: minVersion }`) so consumers feature-detect by capability
rather than raw integer. The single-number choice was deliberate for
cache-invalidation *simplicity* — this is a simplicity-vs-precision
trade-off worth re-examining now that the two surfaces decouple in
practice.

**Cost.** Medium, and partially breaking (ETag format). Best bundled with
the next breaking bump.

#### R3. "Section absent" has two inconsistent encodings

**Problem.** When a demo carries no data for a section, some endpoints
return `422 <section>_unavailable` and others return `200` + an empty
array/object. A client cannot predict which, and the boundary is fuzzy.

**Evidence.** `422`: `handleMetadata` (157), `handleLocGraph` (173),
`handleFrags` (195), `handleDamage` (270), `handleDemoInfo` (447),
`handleRegionControl` (862). `200`-empty: `handleBackpacks` (467),
`handleItems` (513), `handleWeaponPickups` (572), `handleChat` (395).
`/frags` is conceptually a list yet 422s; `/items` is a list and 200-empties.

**Impact.** Every consumer needs per-endpoint special-casing to tell
"unavailable" from "empty." `/overview` already exposes `hasRegionControl`
and `errors` to pre-hide panels, which only some endpoints honor.

**Recommendation.** Adopt and **document** one rule: `422` = "this analysis
dimension is *structurally impossible* for this demo" (non-KTX → demoinfo /
damage; no BSP → loc-graph; no frag log → frags); `200`-empty = "available,
nothing happened this match." Then apply it deliberately per endpoint and
state the rule in `API.md` §2.4.

**Cost.** Low (mostly a documented convention + a few handler tweaks);
mildly breaking for clients that branch on today's accidental behavior.

### Medium — real inconsistencies

#### R4. `weapon` means different things on different endpoints

**Problem.** Same param name, divergent semantics: a CSV set on some
endpoints, a single scalar on another.

**Evidence.** `handleFrags`/`handleDamage`/`handleWeaponPickups` build
`weaponSet := csvSet(q.Get("weapon"))` (multi-value). `handleBackpacks`
uses `wantWeapon := strings.ToLower(...q.Get("weapon"))` then `b.Weapon !=
wantWeapon` (`handlers.go:473,480`) — single value, so `weapon=rl,lg`
silently matches nothing on `/backpacks`.

**Impact.** A consumer that learns the param on `/frags` gets surprising
empty results on `/backpacks`.

**Recommendation.** Make `weapon` a CSV set everywhere (the natural QW
filter shape) via the shared filter layer from R1.

**Cost.** Trivial; non-breaking (CSV is a superset of the single value).

#### R5. `/region-control` under-exposes its own view function

**Problem.** The REST endpoint exposes strictly less than the `view`
function it calls.

**Evidence.** `view.RegionControl` accepts a `[StartTime, EndTime)`
sub-window (`RegionControlOptions`, documented in `RESULT_SCHEMA.md` →
"optionally clipped to a `[StartTime, EndTime)` sub-window"), but
`handleRegionControl` (`handlers.go:857–878`) parses only `windowMs` and
passes `view.RegionControlOptions{WindowMs: windowMs}`.

**Impact.** No way over REST to ask "who controlled QUAD between 4:00 and
6:00," though the engine supports it.

**Recommendation.** Plumb `from`/`to` through, matching every other
windowed endpoint.

**Cost.** Trivial; additive.

#### R6. Two 400 codes for "bad query" (`invalid_param` vs `view_error`)

**Problem.** Both are 400 and both mean "your query was malformed"; the
split (param-parse vs view-layer validation) is an implementation artifact,
not a client-meaningful distinction.

**Evidence.** Param parse failures → `invalid_param` (`handlers.go:638–663`
etc.); unknown field code / unknown reducer name rejected *inside*
`view.Buckets` → `view_error` (`handlers.go:680,688,…`).

**Impact.** Low now that both are documented (§3), but a client must catch
two codes to handle "you asked for something invalid."

**Recommendation.** Either collapse to a single `invalid_param` (wrap the
view error at the handler), or keep both and rely on the now-correct docs.
Low urgency.

**Cost.** Trivial.

### Low — naming / cosmetics

#### R7. Query-param casing is mixed

`windowMs`, `minDwellMs`, `includeTeam` are camelCase; `from`, `to`,
`time`, `players`, `fields`, `types`, `weapon`, `source`, `kinds`, `items`,
`loc`, `layout` are lowercase (`params.go`, handler call sites). Harmless
but inconsistent. Pick one convention (accepting both for a deprecation
window is easy). **Cost:** trivial; keep aliases to avoid breaking.

#### R8. Two names/paths for the same map-entities payload

`GET /v1/demos/{id}/map-entities` (hyphenated, demo-scoped) and
`GET /v1/maps/{map}/entities` (un-hyphenated, map-scoped) return the same
`result.MapEntitiesResult` shape (`map_handlers.go:26,41`). Align the
spelling (and consider whether the demo-scoped one is even needed once the
map name is known from `/overview`). **Cost:** trivial; add an alias route.

### Schema-level — what can be redefined

#### R9. `MatchResult.StartTime` / `EndTime` are effectively dead

`StartTime` is "always 0 after normalization" and `EndTime` "== Duration"
(`RESULT_SCHEMA.md` → MatchResult), and both duplicate
`streams.global.matchStart` / `matchEnd`. They carry no information a
consumer can't get elsewhere. **Recommendation:** deprecate, then drop on
the next breaking bump. **Cost:** low; breaking removal, so version-gate it.

#### R10. The intentional frag triple-representation — keep, don't collapse

The same kill appears in `frags.frags[]`, `messages.events[type=frag]`, and
`timelineAnalysis.fragEvents` (lean score-delta channel). This is documented
under "Layered views" and is genuinely useful — each shape serves a
different consumer (classification flags vs obituary text vs cheap delta
timeline). It is also the most common source of consumer confusion.
**Recommendation:** *no schema change* — this is the right kind of
redundancy and matches the repo's "surface authoritative data, don't
filter" principle. The fix is purely doc clarity, which the "Layered views"
table already provides. Listed here so a future cleanup doesn't mistake it
for accidental duplication.

#### R11. Time units split *within a single response* — the #1 ergonomic hazard

**Problem.** `/stream-slice` returns the envelope `startTime`/`endTime` in
**seconds** but its embedded `pos.t` / `h` / interval `s`/`e` arrays in
**int32 milliseconds** — in the same JSON object. `/buckets` columnar uses
ms on its axis while `/buckets` row uses seconds.

**Evidence.** `API.md` §2.1 (the dedicated "one real gotcha" table) and the
captured `/stream-slice` examples (`startTime: 105` next to `pos.t:
[105001,…]`). This is principled — the view envelope is seconds, raw schema
arrays are copied verbatim in their native ms — but it is the thing new
integrators get wrong most.

**Impact.** Off-by-1000 bugs at the boundary between envelope and embedded
arrays.

**Recommendation.** This is a deliberate trade-off between "faithful to the
stored schema" and "internally consistent per response." Faithfulness
currently wins, which is defensible. If revisited, the cleanest options are
(a) expose the raw-stream endpoints fully in ms (so a slice is internally
consistent and the envelope matches its arrays), or (b) convert embedded
arrays to seconds at the view boundary like everything else. Either removes
the mixed-unit object. Worth a *deliberate re-affirmation* rather than
leaving it incidental.

**Cost.** Medium and breaking either way; do not do casually.

---

## 5. Summary

The schema is well-architected — the `streams` + on-demand `view` design
(native-rate truth stored once, every aggregation derived) is the standout,
and it is what keeps WASM/CLI/REST/MCP in lock-step. Both reference docs are
unusually thorough; the drift found was version-number lag and a couple of
emitted-but-undocumented error codes, now corrected (§3).

The highest-leverage **redesign** is **R1** (move section-filtering into the
`view` layer so every consumer shares it — non-breaking). The
highest-leverage **clarification** is **R3** (one documented rule for
"section absent"). Everything else is incremental. R10 is explicitly a
*keep*, not a fix.
