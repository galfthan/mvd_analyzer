# PLAN-api-usability — phase 16.1: API/MCP usability + correctness

> Written 2026-07-10 on `phase-16` (hosting stack tip, post-deploy).
> Numbered 16.1 deliberately: phase 17 remains the mvd-web-on-API
> migration ([PLAN-hosting.md](PLAN-hosting.md) "explicitly after").
> Trigger: first real multi-demo agent session over the hosted MCP
> ("opening spawn → item control on schloss ×15") burned ~45 tool calls
> and most of a context window on work that should have been ~15 small
> calls. This plan turns that usage report into verified findings and
> a work programme. Everything below was audited against the code on
> `phase-16`; file:line refs are to that tree.
>
> **Out of scope (user decision 2026-07-10):** multi-demo / batch
> operations (`getArtifact(demoIds[], …)` etc.) are a separate future
> phase. Nothing here may quietly grow a batch surface.

## Status — ✅ DONE (2026-07-10, branch `phase-16.1` off `phase-16`)

> All three workstreams landed on `phase-16.1` (pushed), each followed
> by an adversarial review pass with confirmed findings fixed in-branch:
>
> - **A** (`4b1d5ed` analyzer: match-start spawn synthesis + `opening`
>   artifact, schema v51 · `6f33364` view: default `pickup` events with
>   spawner identity + spawn `detail{loc}` · `d0a624d` review fixes).
>   Review: 3 agents; one MAJOR — the manifest marked `opening`
>   servable but mvd-api's `eagerArtifacts` map had no accessor, so
>   `getArtifact('opening')` 500'd; fixed + pinned by
>   `TestEveryServableEagerArtifactHasAccessor`.
> - **B** (`385a6f2` items/backpacks/weapon-pickups `from`/`to` + items
>   `summary`, region-control proxy passthrough, MCP `summary:true`
>   defaults for damage/aim/items with response `hint`, searchGames
>   compact rosters + PostgREST `total`; no schema bump ·
>   `bfe5b11` review fixes: closed [from,to] boundaries — weapon-stay
>   zero-length phases at the window edge survived, end bound made
>   inclusive to match every sibling endpoint).
> - **C** (`c825ff0` schema v52: `streams.global.timeBase:"demo"` +
>   `errors[]` notice on no-match-start demos (D9); field errors
>   enumerate all 23 codes with glosses (D6 amended, drift-tested);
>   view envelope switched to correctly-rounded ms/1000 division —
>   goldens diff schemaVersion-only (D8 amended); shots.go stale
>   warmup comments, demoInfo units island, items time sentinels,
>   powerup `duration` dropped from view detail (D10), `getOverview`
>   units-seam note · `1ae916e` review fix: the demoInfo island block
>   itself had KTX units wrong — timelimit is minutes, item
>   `{took, time}` is count + cumulative-hold seconds, not timestamps).
>
> `make test` green (21 packages incl. golden corpus) at every commit.
> Acceptance shape achieved: the schloss opening question is now 1×
> `getArtifact('opening')` per demo (~15 small rows) + optional
> `getItems(endTime:60)` — no `getStateAt(0.3)` workaround, no
> timestamp cross-referencing. F1e partially remains by decision
> (Result-field floats: shots.accuracy, locGraph weights,
> aim.crosshair — documented known-wart per amended D8).

## Phase 16.2 — OpenAPI + REST papercuts (DECIDED 2026-07-10, plan before building)

> ### Status — ✅ DONE (2026-07-10, branch `phase-16.2` off `phase-16.1`)
>
> Landed in six commits, `make test` green at each:
> `0e411d8` (spec skeleton + /docs viewer — RapiDoc 9.3.8 vendored via
> embed.FS, auth-exempt, content-hash ETags) · `51571f3` (drift tests:
> route parity both directions, error-code/artifact/field-code marker
> enums, info.version = schemaVersion) · `1a03f77` (full field-level
> response schemas + golden-response validation) · `5c9fdee` (weapons
> param: REST alias, MCP field rename) · `e385e53` (description
> papercuts + §4.17b table) · `9862a5e` (docs). No schema bump.
>
> **Decisions taken at planning (2026-07-10, this phase's session):**
> - Viewer: RapiDoc (single-file, try-it console with Bearer auth).
> - `weapon` param: `weapons` canonical + REST legacy alias
>   (`qp.CSVAny`, canonical wins); MCP input field renamed outright.
> - Response schemas: **full field-level** (amends the "spec loosely"
>   sketch below) — bootstrapped from the Go structs by reflection,
>   hand-curated, and enforced by a golden-response validation test
>   (`mvd-api/openapi_validate_test.go`): the committed dm3 golden
>   Result is served through the real router and every JSON response
>   (~50 cases: all 35 ops, summary/layout/loc=index/all-fields
>   variants, every error class) must validate against its spec schema;
>   a coverage sweep forces new endpoints into the case table. This
>   relaxed "zero new Go deps" to two TEST-ONLY imports (gopkg.in/
>   yaml.v3 + google/jsonschema-go, already in the workspace); the
>   binary still embeds the spec as bytes.
> - The generic artifact response is a oneOf of 16 resultKey-keyed
>   envelopes; events[].detail types its known keys with
>   additionalProperties kept open; the columnar player wire shape
>   (custom MarshalJSON) is hand-written.
>
> Fallout: the validation harness exposed a latent panic —
> `firstChangeI16/Str` (view/buckets.go) nil-guarded but indexed
> `stream[0]`, so an empty-but-non-nil change stream (any JSON
> round-trip of one) crashed /buckets; fixed with len() guards.
> All papercuts below landed except the declined ones (unchanged).

> User decision: at least three external projects already integrate via
> the REST API, with more expected — a machine-readable spec graduates
> from nice-to-have to due. Detailed planning happens before
> implementation; this section captures the sizing and open questions.
>
> **Scope sketch (from the 2026-07-10 fresh-eyes API review):**
> hand-authored OpenAPI 3.1 (~33 operations, ~15 highly-reused query
> params → components), served as `/openapi.yaml` + a `/docs` viewer
> (vendored single-file asset via embed.FS — no CDN, zero new Go deps).
> Generate the artifact-name enum slice from `ArtifactManifest()` (the
> one machine-readable inventory that already exists). Companion drift
> test in the ARTIFACTS.md style: extract `METHOD /path` patterns from
> router.go and the writeError code enum; fail on spec mismatch.
> **Known-awkward shapes to decide up front:** the `{id}` segment
> (pattern validates `gameId:N|sha:HEX` but loses semantics), the
> generic artifact response (oneOf across ~16 servable artifacts —
> generate it), columnar buckets and events[].detail (dynamic/
> polymorphic keys — spec them loosely, link RESULT_SCHEMA).
> Estimate: ~1-2 days incl. viewer + drift test.
>
> **Deferred REST/MCP papercuts** (fresh-eyes findings acknowledged but
> not acted on; candidates to ride 16.2): singular `weapon` CSV param
> among plural siblings (rename or alias); missing cross-links in the
> items/weapon-pickups/backpacks trio descriptions; getDamage/getAim
> description walls (lead with routing facts, move field vocab to a
> trailing note, drop the duplicated telefrag sentence); a node-name ↔
> curated-route ↔ body-key mapping table in API.md §4.17b; "who won"
> confirmation sentence on getOverview. Declined outright: route
> pluralization renames (breaking, cosmetic); geometry ETag hash (the
> len-proxy caveat is now documented in code).

## Findings (verified)

### F1 — time semantics are clean; the remnants are edges, not streams

The per-producer rebase architecture (each node subtracts
`Clock.MatchStartMs` in its own Finalize) holds everywhere it should:
frags, messages, timeline events/streaks, all player/mover/projectile
streams, damage (incl. the recently rebased telefrags/stomps), shots,
items phases, backpacks, weaponPickups, aim. All API `from`/`to`/`time`
params are match-relative seconds end to end. What remains:

- **F1a** — on a demo with *no detectable match start*, `ToMatch` is the
  identity (`analyzer/clock.go:58-63`) and the **entire result silently
  stays on the demo clock**, indistinguishable from a rebased result.
- **F1b** — `demoInfo` is a deliberate KTX island (verbatim, integer
  *seconds*, KTX's own base — `result/demoinfo.go:125-126`), but
  RESULT_SCHEMA.md never says how (or that you can't) reconcile it with
  match time.
- **F1c** — `result/shots.go:10-13,37-45` still documents the removed
  `Warmup` field and claims the stream is not match-gated; v50 gated it.
  The comment now lies about time semantics.
- **F1d** — items phases: warmup pickups rebase to *negative* times and
  `availableFrom==0` doubles as the "available since match start"
  sentinel (`analyzer/items.go:1315-1330`). Real, useful signal — but
  undocumented.
- **F1e** — float precision. The view layer converts int-ms back to
  seconds with bare `float64(t)*0.001`, reintroducing
  `13.155000000000001` on the API surface: `view/events.go:74-125`,
  `view/trails.go:40-62`, `view/buckets.go` (row layout + envelope),
  `view/streamslice.go:15-30`. Also raw: `shots.accuracy`
  (`analyzer/shots.go:630`), `locGraph` float-seconds accumulators
  (`analyzer/locgraph.go:89-99,190-207` — also a units oddity: seconds
  in an ms schema), `aim.crosshair` float32 columns (`result/aim.go:61-72`).

### F2 — initial spawn is missing from the spawn stream (bug)

`SpawnEvent` fires only on a dead→alive health transition
(`mvd-reader/parser/stats.go:120-134`). A player alive through the
countdown never transitions, so the **match-start respawn — the most
important spawn of the match — never appears** in
`streams.players[].spawns` or `getEvents type=spawn`. Only the
`FragStreakEvent` synthesizer acknowledges it ("first life synthesized
at match start", `result/timeline.go:210-219`). Spawn events also carry
no location anywhere (the events view doesn't join the loc stream), so
even mid-match spawns answer "when" but not "where". Current
workarounds — `getStateAt(0.3)` or `locTrails[0]` — are exactly what an
agent shouldn't have to discover.

### F3 — pickup identity exists in the Result but not in the event stream

The parser is rich: `ItemPickupHintEvent` (ItemEnt + PlayerEnt +
RespawnSec, `mvd-reader/parser/ktx_pickup.go:29-34`),
`BackpackPickupHintEvent`, `ItemSpawnEvent` (EntNum + Kind + Origin),
`ItemStateEvent`. And the Result keeps the authoritative joins:
`items[]` (per-spawner entNum/loc/name incl. `ya_1`/`ya_2`, phases with
takenBy/takenAt), `weaponPickups[]` (world-vs-backpack + backpackEnt +
dropper), `backpacks[]`. But the **events view discards all of it**:
`weapon`/`item` events are synthesized from *held-interval* streams
(`view/events.go:257-274,345-381`) → anonymous `weapon gain: rl`; armor
is an opt-in raw value column where same-tick pickup+damage collapses
into one net delta. There is no `pickup` event type. An agent must make
a second call and cross-reference timestamps to learn *which* YA was
taken — the single biggest call-multiplier in the usage report.

### F4 — filter gaps (each one is "fetch everything, discard most")

| Tool | Gap | Evidence |
|---|---|---|
| getItems | **no from/to**, no summary — full-match phases always | `view/sections.go:398-446`, `mcp_backend.go:198-203` |
| getBackpacks | no from/to | `mcp_backend.go:191-195` |
| getWeaponPickups | no from/to | `mcp_backend.go:214-219` |
| getRegionControl | REST accepts from/to; **MCP proxy drops them** | `mcp_backend_proxy.go:540-542` |
| getEvents/getChat/getItems | no summary mode (frags/damage/aim have one) | `handlers.go:293,336,404` |
| searchGames | `count`=rows-returned, no total/hasMore; full hub rosters (name_color, per-player ping, color arrays) passed through verbatim | `supabase.go:22,129-134` |

### F5 — friction the usage report paid for

- `getStateAt(fields=['loc'])` → `unknown field code loc`: the field
  *code* is `li` (`view/fields.go:19`) but the *output key* is `loc`;
  the error names no valid codes (`view/fields.go:206`) and no tool
  description mentions the mismatch.
- `loadDemo` reads as mandatory (`mcp_tools.go:24,32`) but every
  endpoint auto-resolves `gameId:N`/`sha:HEX` and auto-loads on cache
  miss (`handlers.go:85-102`, `democache/cache.go:266-322`). 15 of the
  session's ~45 calls were unnecessary warm-ups.
- `PowerupEvent` serializes `t`, `endTime`, *and* `duration`
  (`result/timeline.go:196-206`; echoed in view detail
  `view/events.go:101-107`) — one derivable.
- locTrails duplicates every boundary (`e[i] == s[i+1]`,
  `view/trails.go:100-143`).

## Design decisions (veto here, not in review)

> **All decisions walked through and resolved with the user 2026-07-10.**
> D1, D2, D3, D5, D7, D9, D10 confirmed as proposed. **D6 amended**
> (error enumeration only — no field aliases) and **D8 amended**
> (envelope-only rounding — no Result-field precision changes); the
> amended text below is the decision of record.

- **D1 — MCP defaults may diverge from REST toward token-lean.**
  Precedent exists: the proxy already defaults `windowMs` to 1000 vs
  REST's 50 (`mcp_backend_proxy.go:420-423,536-539`). REST keeps
  today's defaults (it serves programs); MCP defaults serve a context
  window. Concretely: `summary` defaults **true** at the MCP layer for
  `getDamage`, `getAim`, and the new `getItems` summary (F4); an agent
  passes `summary:false` to get the full log. `getFrags` keeps the full
  kill log by default — the log *is* the product there and is moderate.
  Every summary response embeds a hint field (e.g.
  `"detail":"pass summary:false for the per-event log"`) so agents can
  self-serve.
- **D2 — pickups become a first-class event type, synthesized in the
  view.** No parser or analyzer change: `view/events.go` joins
  `items[].phases` (takenAt/takenBy + parent name/kind/entNum/loc),
  `weaponPickups[]` (source, dropper), `backpacks[]`. Event shape:
  `{t, type:"pickup", player, detail:{item:"ya_1", kind:"armor",
  entNum, loc, source:"world"|"backpack", dropper?}}`. `pickup` joins
  the **default** type set (discrete, low-frequency, high-value); the
  existing interval-based `weapon`/`item` gain/lose events are
  unchanged (they tell the *holding* story and consumers exist).
- **D3 — the initial spawn is real data, so it lives in the analyzer,
  not the view.** Synthesize one spawn per player at match start in the
  streams finalize (same policy as the FragStreak first-life synthesis,
  which is prior art for "t=0 is a life boundary"). Implementation
  first checks whether the parser can see the actual match-start
  respawn (position teleport + stat reset at countdown end) and uses it
  when present; synthesis at `t=0` is the fallback. This changes
  `spawns[]` counts — schema bump + RELEASE_NOTES + golden regen.
- **D4 — spawn events get a location in the events view** by sampling
  the loc/position stream at spawn time (carry-forward, same lookup
  `getStateAt` uses). View-only; no schema change to streams.
- **D5 — a new eager `opening` artifact** (post-processor node;
  requires `timeline`, `items`, `weapon-pickups`, `roster`, `clock`):
  per player `{name, team, spawnLoc}` + per tracked item the first
  take `{item, kind, entNum, loc, t, takenBy, team}`. Tracked = all
  armors, mega, powerups, RL/LG spawners — i.e. `items[]` entries whose
  kind ∈ {armor, mega, powerup} plus weapon spawners for rl/lg. Served
  for free via `listArtifacts`/`getArtifact`
  (`analyzer/manifest.go:57-82`) — **zero new MCP tools**, which is
  also why this stays useful even with batch deferred: one small call
  per demo instead of three large ones. Not duplicated into
  `getOverview` for now — overview stays a scoreboard; revisit if the
  artifact round-trip proves annoying in practice.
- **D6 — self-describing field errors; NO aliases** *(amended
  2026-07-10 — the original proposal also aliased resolved output names
  to codes, e.g. `loc`→`li`)*. Selector codes stay strict;
  `unknownFieldError` enumerates all valid codes with one-word glosses
  (`li=location, h=health, …`). Applies to state-at, buckets,
  stream-slice. Rationale for dropping aliases: the output keys `loc`
  (resolved name) and `li` (raw locTable index) are two
  *representations* of one field selected by the separate `loc=`
  param — letting `loc` double as a fields-selector spelling *and* the
  representation param invites exactly the confusion it was meant to
  fix. A typo'd agent pays one self-correcting round trip instead.
- **D7 — searchGames goes compact by default.** MCP-side projection:
  keep `id, timestamp, map, mode, matchtag, demo_sha256,
  demo_source_url, teams`, and project each roster entry to
  `{name, team, frags}` (drop `name_color`, `ping`, `color`,
  `team_color`, `is_bot`). `roster:true` opts back into the verbatim
  hub rows. Add honest pagination: request PostgREST
  `Prefer: count=exact` and surface `total` (fallback `hasMore` via
  limit+1 if the hub disallows count). REST is untouched — searchGames
  never transits mvd-api.
- **D8 — precision: fix the view envelope only** *(amended 2026-07-10 —
  the original proposal also rounded `shots.accuracy`, the locGraph
  float-seconds accumulators, and `aim.crosshair` columns)*. Round the
  view-layer ms→seconds conversions to 3 decimals at the point of
  `*0.001` (`view/events.go`, `view/trails.go`, `view/buckets.go` row
  layout + envelope, `view/streamslice.go`) — kills the
  `13.155000000000001` class on the API surface with **no schema bump**
  (values change in the last decimals, shapes don't; goldens pin the
  Result, not view output — verify none of these views leak into
  goldens before assuming that). All **Result** fields stay untouched:
  `shots.accuracy`, locGraph weights, `aim.crosshair` keep full
  precision (F1e stays open as a documented known-wart; revisit only
  with evidence the token cost matters). Airgib heights and state-at
  pos/vel full-precision remains deliberate (v33/v34).
- **D9 — no-match-start demos get flagged, not fixed.** New
  `streams.global.timeBase: "demo"` (omitted in the normal case) plus
  an `overview.errors[]` entry. Surfacing beats coercing: we cannot
  invent a match start, but consumers must be able to tell.
- **D10 — powerup `duration` is dropped from the *view* event detail
  only.** The Result keeps t/endTime/duration (authoritative-data
  policy: redundancy in the stable contract is harmless, and removing
  it breaks consumers for a ~8-byte win). locTrails boundary
  duplication: **keep** — `[s,e)` residences are self-contained and the
  claimed 2× saving is really ~25% of the row; not worth a second
  format. Documented as considered-and-rejected.

## Workstreams (suggested landing order)

Each lands with its docs (RESULT_SCHEMA.md / API.md / mcp README + tool
descriptions / ARTIFACTS.md regen / RELEASE_NOTES.md) and `make test`
incl. golden regen where flagged. Order puts the usage report's
highest-leverage items first.

### 16.1-A — pickup identity + initial spawn (the call-multiplier fixes)
1. **A1** `pickup` event type in `view/events.go` per D2; join tests
   against a golden demo (ya_1 vs ya_2 disambiguation, backpack
   source, dropper). Docs: RESULT_SCHEMA view-shapes + API.md events
   table + mcp getEvents description.
2. **A2** initial-spawn synthesis per D3 (analyzer,
   `timeline_finalize.go` / streams build) — schema bump, golden regen.
3. **A3** spawn-loc join in events view per D4 (rides on A1's plumbing).
4. **A4** `opening` artifact per D5 — new result type + post node +
   `postNodeMeta` entry, `make artifacts-md`, schema bump (can share
   A2's bump if landed together), goldens.

### 16.1-B — filters + MCP defaults (the bloat fixes)
5. **B1** getItems: `from`/`to` (phase overlaps window; parent item
   header always kept) + `summary` (per item: takenCount, byPlayer
   counts, firstTake) — view + handler + proxy + tool schema.
6. **B2** `from`/`to` for backpacks + weapon-pickups (same three
   layers).
7. **B3** proxy forwards `from`/`to` on region-control (REST already
   parses them).
8. **B4** MCP summary defaults per D1 (damage, aim, items) + the
   embedded "how to get detail" hint.
9. **B5** searchGames compact + `total`/`hasMore` per D7 (mvd-mcp only;
   supabase_test gains a real-shape roster fixture — the current mock
   uses a wrong `players` shape, `supabase_test.go:136`).

### 16.1-C — friction + correctness edges
10. **C1** enumerating field errors per D6 (no aliases — amended);
    document the `li`-selects / `loc=`-renders pair in
    state-at/buckets/stream-slice tool descriptions.
11. **C2** tool-description truth pass: loadDemo is optional
    (auto-resolve documented as the norm, loadDemo = warm-up),
    searchGames count semantics, seconds-in/ms-out units note.
12. **C3** precision pass per D8 (amended: envelope only) — view-layer
    ms→seconds rounding to 3 decimals; no Result fields touched, no
    schema bump expected (confirm goldens don't pin view output).
13. **C4** `timeBase:"demo"` flag + overview error per D9 — schema
    bump (can share A-workstream's if landed together).
14. **C5** doc-debt: fix stale `result/shots.go` Warmup comment (F1c);
    RESULT_SCHEMA notes for the demoInfo KTX island (F1b) and items
    negative-time/0-sentinel semantics (F1d); drop `duration` from
    view powerup detail (D10).

### Deferred (explicitly not 16.1)
- Multi-demo / batch fetch (user call, 2026-07-10) — future phase;
  the `opening` artifact is designed so batch later composes with it.
- Columnar/transition-list variants for items phases and locTrails
  (D10 keep; revisit only with fresh usage evidence).
- Overview `initialSpawn` mirror (D5 revisit clause).

## Acceptance test

Re-run the schloss question that motivated this plan. Target shape:
per demo — 1× `getArtifact(opening)` (spawns + first takes, ~small),
optionally 1× `getItems(summary, to=120)` for early-phase depth. ~15-30
small calls total, no timestamp cross-referencing, no `getStateAt(0.3)`
workaround, no full-match item dumps.
