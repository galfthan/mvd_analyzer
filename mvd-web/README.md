# mvd-web

Layer 3 of the mvd-analyzer workspace: a browser UI for the analysis
pipeline, built as a Go WASM bundle plus a small static frontend that
talks to it through a JS shim.

## What's in the box

- `cmd/wasm/` — WASM entry point. Exports `analyzeMVD(bytes, filename)`
  for the parse-and-pin call, plus the query API as bridge functions:
  `getDefaultBuckets()` (50 ms column-major `ColumnarBuckets` for the
  Timeline/Map panels), `getBuckets(optsJSON)` (row or column via
  `opts.layout`), `getEvents(filterJSON)`, `getStreamSlice(optsJSON)`,
  `getStateAt(optsJSON)`, `getLocTrails(optsJSON)`, and
  `recomputeRegionControl(regionsJSON)`. All take a JSON-string argument
  (or none for `getDefaultBuckets`) and return a JSON string; under the
  hood they call into `mvd-analytics/view` over the cached `lastResult`.
- `static/` — the browser frontend.
  - `index.html`, `styles.css`, `app.js` — main page and the tabbed
    analyzer UI (scoreboard, timeline, map, chat, loc graph, ...).
  - `worker.js` — wraps the WASM module in a Web Worker so analysis
    doesn't block the main thread. Provides the host callbacks the
    WASM side calls synchronously: `fetchLocSync(mapName)` for the
    per-map `.loc` corpus and `fetchBspSync(mapName)` for the per-map
    BSP used by the visibility-aware loc attribution (locvis). Sync
    XHR is still allowed inside Web Workers.
  - `wasm_exec.js` — Go runtime glue, copied from the Go toolchain at
    build time.
  - `vendor/` — the frontend's only third-party runtime code, vendored
    (no CDN): Cytoscape + fcose for the loc graph and the Rajdhani/Inter
    web fonts. Pinned, sha256-recorded and committed; see
    [`vendor/README.md`](static/vendor/README.md).
  - `maps/` — pre-generated per-map floor polygon JSON (version 2:
    per-vertex x,y,z — drives the map tab's 3D view). Committed; the
    frontend fetches `maps/<basename>.json` at demo load. `<basename>`
    comes from `mapFileKey()`, which mirrors Go's `Result.EffectiveMap()`
    — `demoInfo.map`, then the serverinfo `map` key, then `match.map`
    (also a shortname), so a demo with no KTX block still finds its
    geometry. `match.mapTitle` is the display-only level title ("Castle
    of the Damned") and never a file key; the topbar and the Summary
    map cell show the shortname and carry the title only as the map
    cell's tooltip — nothing else reads it.
  - `probe.html` — tiny dev page used to probe runtime features.

## Build and deploy

From the repo root:

```bash
make build                    # -> dist/
make serve                    # build + python3 -m http.server 8080
```

`make build` produces:

```
dist/
  analyzer.wasm               ~4 MB, the WASM bundle
  wasm_exec.js                Go glue
  index.html, styles.css,
  app.js, worker.js           frontend
  vendor/                     vendored Cytoscape + fcose + web fonts
  maps/                       pre-generated map geometry
  locs/                       .loc files copied from mvd-analytics/loc/data
  bsps/                       BSP files from `make bsps` for the locvis
                              visibility filter (skipped if bsps/ is empty)
```

### Deploying

Production is served from the API host (mvdanalyzer.com): Caddy routes
the reserved API prefixes (`/v1/*`, `/docs*`, `/openapi.yaml`,
`/healthz`, `/portal*`, `/mcp*`) to the backends and everything else to
the published `dist/` as static files — same-origin with the API, so
the app needs no CORS. See [`deploy/README.md`](../deploy/README.md)
("Publish the web bundle") for the rsync step and the two rules that
keep the path split sound (never ship an asset under a reserved
prefix; new API paths must extend the Caddy matcher).

### Netlify deploy (branch previews)

`netlify.toml` at the repo root chains `make bsps && make build` and
publishes `dist/`. Every push to a branch with Netlify connected
rebuilds and deploys — kept as the branch-preview mechanism now that
production lives on the API host. `make bsps` runs on Netlify's build
container (it has `curl` and `bash`), fetches the ~14 competitive-map
BSPs from the public mirrors documented in `scripts/fetch-bsps.sh`, and
verifies each sha256 — a missing or corrupt BSP hard-fails the deploy,
which is preferred to a silent V1-everywhere regression.

## Layout

A slim top bar (wordmark + commit-hash version + an "API & MCP" link
to the portal at mvdanalyzer.com/portal — API docs, keys, MCP setup,
admin contact — + GitHub link) sits
above a Grafana-style frame: a fixed left **sidebar** with one button
per analysis tab, and a **main pane** that fills the rest of the
viewport (no width cap). Sidebar order is `Search`, `Summary`,
`Timeline`, `Chat`, `Map`, `Locs & Regions`, `Key Moments`, `Pack Drops`,
`Pickups`, `Aim Stats`.

The **Aim Stats** tab (experimental) is a thin renderer over the Go-computed
`result.aim` block: all-players accuracy tables (counts plus
share-of-fires % columns, so players with different shot volumes compare
directly, under a two-row header that names each column group) and —
driven by a per-player picker inside the Crosshair placement
panel, the only place it applies — a smoothed crosshair-density image
(hitscan; a Gaussian-smoothed
2-D histogram on canvas with a colorbar, hull box marked; radius 1 ≈ the
hitbox edge, so it's range-comparable) split into LG and SG, per-axis
**yaw / pitch marginal histograms** stacked under each image (zero-centered
bins over the image's extents; their clamp edge bins keep the outliers the
image drops, and the on-hull |n| ≤ 1 band is shaded), an LG shaft-time
(ramp) histogram in the same style under the LG image (bars = hit % per
100 ms cell on a dynamic labelled scale, bar opacity = sample size), a
rocket direct/splash panel, and an LG-whiffs split. All geometry/attribution lives
in `mvd-analytics/analyzer/aim.go`; the tab only bins and paints. Target
attribution: hits use the server-confirmed victim (exact in duels and team
games alike); misses are exact in duels and a labeled nearest-crosshair
heuristic in team games, only among enemies alive at the fire time.
A **Victims** filter (All / Enemy / Team / Self) slices every panel by who
the shots hit — the tables read the Go-computed per-bucket counter slices
(`WeaponAim.enemy/team/self`), the heatmaps/marginals filter samples by the
per-sample `team` flag, and the LG ramp rescores its bars; **All** (the
default) matches the server's authoritative numbers (KTX counts team and
self hits too). Duels hide the Team option; Self (rl/gl self-splash —
rocket jumps) has no crosshair samples, tables only. The **Dmg** column
follows the filter too (since schema v63): `result.aim` carries no
damage, so it is joined by player name from `playerStats.damage` and
reads `byWeapon` / `byWeaponTeam` / `byWeaponSelf` per the active
victim. Measuredness comes from exactly the two rules
`result.PlayerStatsDamage` documents — the family's presence for the
enemy and team maps, `damage.taken != null` for the self one — so a
measured zero renders `0` and only an unmeasured split renders `-`.
**All** sums the three splits, which is correct only when all three are
measured; where the self split is not (a KTX-block-without-stream demo)
the cell shows a `≥`-prefixed lower bound whose tooltip names what is
missing, never a silently partial total.
The **Key Moments** tab has seven tables in a two-column grid — powerup
runs beside the longest frag streaks, **Top Frag Runs (10 s)** beside
**Top RL Kills**, **Demo Markers** beside **Top LG Kills** — and a
full-width **Airborne Rocket Gibs** table — enemy rocket hits on airborne victims
(`timelineAnalysis.airgibs`), sortable by any column and defaulting to
height-above-shooter descending (the vertical gap the rocket climbed).
Its rows are empty unless the map's BSP is provisioned (height needs the
clip hull; see `PositionTrack.h`). The **Demo Markers** table lists the
user-inserted `/demomark` bookmarks (`timelineAnalysis.demoMarkers`) —
Time, Player, Team, Note, and a Hub Watch link — so a viewer can jump
straight to the moments players flagged in-game; it is routinely empty
since demos rarely carry markers, and warmup marks show a negative time.

The three ranked lists are the tab's only **view queries**: they are not
stored `Result` fields but `view.TopWindows` / `view.TopKills` rankings
computed on demand from the demo the WASM module still holds, so they
fill in a moment after the rest of the tab has painted. The Go exports
(`getTopWindows`, `getTopKills`) live on the worker's global like every
other one, so the main page reaches them through a `viewQuery` worker
message (`viewQueryInWorker`) — the same round-trip as region recompute
and line of sight. **Top Frag Runs (10 s)** is the top 10 windows by
enemy kills (`metric=frags`, `windowMs=10000`), showing the window's own
`damageGiven` beside the frag count; **Top RL Kills** (10) and **Top LG
Kills** (5) are the hardest kill bursts per weapon — the run of killing-weapon hits leading up to
the kill — at the per-weapon gaps `gapMs=2300` (RL) and `1200` (LG),
with the burst's damage, hits, span, the victim's weapon class and the
damage the victim dealt back. Every query names `dmg=bounded`
explicitly (the *view* default is raw, unlike mvd-api's), so a demo
whose bounded family was never reconstructed (`BoundedMode` `skipped:*`)
shows that table's empty state rather than ranking a different family
under the same heading. Each table settles independently: a query the
demo cannot answer (no damage log, no measurable liveness) leaves only
its own empty state up. Hub links follow the tab's usual rule — a slot
number is not a userid, so a name `timelineAnalysis.playerUserIDs` does
not resolve gets no link at all.

The **Powerup Runs** table carries a two-input display filter (**min s**
= 5, **min frags** = 3 by default, both editable down to 0, reset per
demo load): a run is listed only if it clears *both*, so short and
low-value runs drop out — a quad cycles every 60 s, making 0-2-frag runs
routine noise — while `timelineAnalysis.powerupEvents` stays complete. When the filter empties an otherwise non-empty table the panel
says so instead of claiming the demo had no powerups.

The **Timeline** tab stacks its panels in reading order — team rosters,
Score, Powerups, Weapons, Team Health/Armor, Region Control. That order
is pure DOM order in `index.html`; the JS lists that mirror it
(`updateDetailView`, `TIMELINE_CANVAS_IDS`, the pan/zoom and
current-time-indicator installs) are kept in the same sequence on
purpose. The roster tables above them are **sortable by Player, Frags,
Health or Armor**, defaulting to player name ascending so rows don't
swap places under the reader as the playhead moves; both sides share one
sort state, and a delegated click handler on `.team-status-panel`
survives the innerHTML rebuild that happens on every tick. First click
sorts names A→Z and numbers biggest-first; clicking the active column
flips it.

The **Chat** tab has a **Hide team chat** checkbox that drops `say_team`
lines (`event.type === 'teamsay'`) from the two chat columns; frags and
public `say` are untouched, and the flag resets per demo load.

The Search tab is the first tab and is always available — it holds the
file picker, the hub-URL load row, and the filter form for browsing
the hub. The other tabs are always present in the sidebar; until a
demo is loaded they show a short "Load or search a demo to begin"
placeholder (CSS-driven via a `body.no-demo` class). After a successful
load the placeholder is hidden, the Summary tab activates, and the
real content renders.

On viewports below 800 px the sidebar reflows into a horizontal scroll
strip above the main pane.

## How the pieces fit

1. User drops an MVD file on the Search tab, pastes a hub.quakeworld.nu
   URL, or picks a row from the search results.
2. `app.js` hands the bytes to `worker.js` via `postMessage`.
3. The worker calls `analyzeMVD(bytes, filename)` on the WASM instance.
4. WASM code (`cmd/wasm/main.go`) runs the mvd-analytics default pipeline
   and marshals the Result to JSON. The worker posts this back
   **immediately** as a `result` message — the main thread renders the
   Summary and the other non-bucket tabs right away.
5. **Then**, off the critical path, the worker runs the two bridge
   calls — `getDefaultBuckets()` (builds the 50 ms column-major
   `ColumnarBuckets` via `view.BucketsColumnar`) and
   `recomputeRegionControl(defaults)` (region-control bucket states at
   50 ms, same grid) — and posts them as a second `buckets` message.
   These are expensive (the bucket build alone is ~1 s in WASM), and
   only the Timeline/Map tabs need them, so deferring them roughly halves
   time-to-interactive. The Timeline/Map panels read the columnar view
   through the bucket-view accessors in `app.js` (`bucketIndexAtTime`,
   `playerValAt`, `reconstructBucketPlayers`, `teamSnapshot`, …).
6. On `result`, `app.js` parses the JSON, clears the no-demo class,
   switches to the Summary tab, and renders all tabs. The main-thread
   inits are cheap, so they run now even though the bucket-derived
   fields are still empty — the scoreboard, chat, pack drops, pickups,
   key moments and loc graph are fully populated; only the timeline
   graph, map trails and region overlay are blank. On the later
   `buckets` message, `applyDeferredBuckets()` stashes the payload onto
   `result.timelineAnalysis.bucketView` / `.regionControl
   .bucketStates`, **re-runs** the bucket-dependent inits
   (`initRegionControlData`, `displayTimelineAnalysis`, `initMapView`),
   and re-renders the active tab so Timeline/Map fill in. The win is
   purely that the worker no longer blocks on the bucket build before
   delivering the result.

### The Summary tab reads `playerStats`, and only `playerStats`

Every table on the Summary tab — Basic Stats, Weapon Stats, Item Pickups
& Drops, and the three per-team variants — renders
`result.playerStats` (schema v63). It replaced a four-source join across
`match.players`, `frags.byPlayer`, `frags.frags` and `demoInfo` that
lived in `app.js` and that the REST and MCP consumers never got; the
merge now happens once, in Go.

Two consequences worth knowing:

- **`analyze()` applies the KTX overlay before marshalling**
  (`withPlayerStatsOverlay` in `cmd/wasm/main.go`). The analyzer *stores*
  the fully derived section on purpose, so the golden corpus records what
  the pipeline computed; folding in KTX's own numbers where KTX is the
  better source is a read-time step (`view.PlayerStats`). mvd-api runs it
  in its handler, and this WASM entry point is the web's equivalent read
  boundary. Without it the browser would see accuracy, the KTX damage
  splits and ping/handicap/bot as if every demo lacked a demoinfo block.
- **There is no scoreboard-only fallback any more.** `playerStats` is
  computed for every demo and degrades families to `src: "derived"`
  rather than dropping them, so a demo with no KTX block renders the full
  tab (a 2003 kmod duel is the regression case). The one class with no
  section at all is a parse that produced no player streams — a race demo
  has no match — and that renders as empty tables.
- **Absent is rendered `-`, never `0`.** A field the pipeline could not
  measure is omitted rather than zeroed, so the tables test for absence:
  the kill side of `score` (kills / suicides / teamKills / efficiency /
  the by-weapon split) goes missing together on a demo whose obituary log
  yielded nothing while deaths were still counted, and `ping` is KTX-only.
  Frags and deaths are measured on those demos and still print. In the
  Basic Stats tables a zero that *was* measured still prints as `0`;
  the Weapon Stats table and the possession (`s`) columns of Item Pickups
  are the exception by design — a weapon the player never touched or an
  item never held renders `-` rather than a zero.
- **Item Pickups carries possession, not a separate panel.** Each item's
  group is `took | s`: the pickup count, then the seconds it was held
  from our own hold integral (KTX writes no weapon hold time at all, and
  its armor clock keeps counting after the armor is chewed to zero). MH
  has no `s` column — mega health is consumed on pickup — and RL/LG add
  `drop | xfer`. A possession cell's tooltip carries held ms, the share
  of *alive* time, the run count and the row's alive/present/match
  window: a share is only as good as the window it divides by, and a
  player the streams barely saw can get a presence window that is a
  floor rather than a measurement — the denominators name that. The
  cell sorts on raw ms (`data-sort-value`), not on the rendered seconds.
- **The three "Per Team" tables hide when `playerStats.teams` is empty**
  (body class `no-team-rows`), which is FFA and duels — otherwise FFA
  rendered three headers over an empty tbody.

`cmd/wasm/main.go` also exports `getDemoInfo()`, which returns just the
KTX demoinfo summary (`result.DemoInfo` — map, players, teams, scores,
date) from the pinned `lastResult` as JSON. It is zero extra cost (the
data is already computed) and lets a consumer read the match summary
without re-marshalling the full Result. Note: the demoinfo block is
written near the **end** of the MVD stream, so obtaining it still
requires decoding the whole demo — cheap to *read*, not cheap to *skip
ahead to*.

The WASM boundary is the only place that bridges Go and JS. The rest of
the frontend has **no runtime CDN dependencies**: its own vanilla JS plus
a sprinkle of CSS, with Cytoscape + fcose (the Locs & Regions loc graph)
and the Rajdhani/Inter web fonts vendored under
[`static/vendor/`](static/vendor/README.md) — pinned, sha256-recorded,
committed and copied to `dist/` by `make build`, so the app works when
unpkg / Google Fonts are unreachable. To bump a vendored version: replace
the file (keeping the version in its name), update the matching
`<script>` / `<link>` in `index.html`, and update the row in
`static/vendor/README.md` (see that file for the full procedure).

### Panel explanations live behind one disclosure

Several panels need a paragraph explaining what their numbers mean and
where they come from — Item Pickups & Drops (Summary), Region Control and
Loc Heatmap (Locs & Regions), and both Aim panels. That prose is kept in
full, but collapsed, so a panel opens with its table rather than with a
wall of text. The idiom is a single one in `index.html`:

```html
<details class="panel-info">
    <summary>More info</summary>
    <div class="aim-desc">…the full explanation…</div>
</details>
```

`.panel-info` (styles.css) is the disclosure chrome, visually derived
from `.per-player-detail` — the older per-player `<details>` blocks on
the Timeline tab, which stay as they are. The body element keeps
whichever prose class it already had (`.panel-note`, `.aim-desc`,
`.locheatmap-desc`), so only the collapsing is shared, not the type
scale. Use the same bare `More info` label for any new one — an ⓘ
glyph was tried and dropped, since the vendored Rajdhani/Inter subsets
carry no U+24D8 and it rendered as tofu. Short single-line captions
(`.locmetric-hint`, `.map-entity-hint`) stay inline and do **not** get
a disclosure.

## Performance timing (console)

Every demo load prints a structured per-stage breakdown to the browser
console (look for the `[mvd-timing]` group) and stashes the same object
on `window.__mvdTimings`. It is dev-facing instrumentation only — there
is no UI for it. Stages reported, in load order:

- **wasm load** (one-time): fetch + `instantiateStreaming` + `go.run`,
  timed in `worker.js` and sent on the `ready` message.
- **network**: `gameInfoFetch` (Supabase metadata) and `demoDownload`,
  timed on the main thread in `app.js`.
- **WASM analyze**: total wall time of the `analyzeMVD` call, plus the
  Go-side per-phase split from `getAnalysisTimings()` — `init`,
  `eventPass` (decode + gzip + all OnEvent dispatch), one
  `finalize:<analyzer>` row per analyzer (so `finalize:timelineAnalysis` — the
  loc-resolution work — is isolated), one `post:<name>` row per
  post-processor (`locGraphPost`, `regionControlPost`), and `marshal`.
- **loc/bsp fetch**: per-map `fetchLocSync` / `fetchBspSync` durations.
  These run **synchronously inside** the `analyzeMVD` call, so their
  time is already included in the WASM analyze wall time *and* inside
  `finalize:timelineAnalysis`. To get the **pure loc-resolution compute**,
  subtract `locFetch + bspFetch` from `finalize:timelineAnalysis`.
- **result JSON.parse** (main thread), each tab render
  (`displayTimelineAnalysis`, `displayKeyMoments`, `displayPackDrops`,
  `displayPickupsTab`, `initMapView`, `initLocGraphView`), and the
  async `map geometry fetch` (logged separately as it resolves after
  the UI is shown).

The breakdown reflects **time-to-interactive** — it ends when the Summary
and non-bucket tabs are painted. The deferred 50 ms bucket build
(`getDefaultBuckets` + `recomputeRegionControl`) runs after that and is
logged on its own line (`deferred bucket build (off critical path)`),
followed by a `Timeline/Map ready` line when `applyDeferredBuckets`
finishes wiring those tabs.

This exists to replace guesswork about where load time goes (e.g. "is
loc the slowest?") with measured data before optimizing. It is what
surfaced the two big costs — the parse event pass and the (now deferred)
bucket build.

## Demo search

The Search tab queries the same Supabase `v1_games` table that the
hub-loader uses (so no backend of our own) and lets the user filter by
player, team, map, mode (1v1 / 2v2 / 4v4 / FFA / CTF), game tag, and
date range. All filters are AND-combined, empty fields act as
wildcards, and the latest 20 matches sorted by date descending are
listed. Clicking a row downloads the demo and runs the normal analysis
pipeline; the user lands on the Summary tab.

Search state is reflected in the URL so links are shareable. Supported
params: `player`, `team`, `map`, `mode`, `tag`, `from`, `to`. For
example:

- `?player=nexus` opens the page on the Search tab with the player
  field pre-filled and the search auto-executed.
- `?player=nexus&mode=1on1&map=aerowalk` pre-fills three fields.
- `?gameId=212607&player=nexus` loads the demo (and lands on Summary)
  *and* pre-populates the Search tab; clicking Search shows the
  filters and the result list.

The demo-load URL parameter is `gameId` (matching hub.quakeworld.nu's
own URL scheme); the legacy `?hub=<id>` form is still accepted on read
for any links that already exist in the wild.

## Loc files at runtime

WASM builds do not embed the `.loc` corpus (would add ~6.7 MB to the
bundle). Instead, when the analyzer needs a loc file, it calls
`fetchLocSync(mapName)`, which the worker implements as a synchronous
XHR against `locs/<name>.loc`. `make build` copies the corpus from
`mvd-analytics/loc/data/` into `dist/locs/`.

## BSPs at runtime (visibility filter)

The locvis visibility filter (see [`mvd-analytics/locvis/`](../mvd-analytics/locvis/))
loads per-map BSP files on demand via `fetchBspSync(mapName)`, which
worker.js implements identically to `fetchLocSync` but against
`bsps/<name>.bsp`. `make bsps` populates a gitignored top-level
`bsps/` directory from the curated set in
[`scripts/fetch-bsps.sh`](../scripts/fetch-bsps.sh) — id-stock maps
(dm2/dm3/dm6/e1m2) from [id-maps-gpl](https://github.com/quakeworld/id-maps-gpl)
gzipped, community competitive maps from
[maps.quakeworld.nu/core](https://maps.quakeworld.nu/core/), each
sha256-pinned. `make build` then copies them into `dist/bsps/`. When
a map has no BSP available the WASM side returns `null` and locvis
transparently degrades to the V1 Euclidean nearest-neighbour
attribution — no UI change beyond losing the wall-bleed correction
for that map. Skipping `make bsps` entirely is supported for local
dev; the build still works, you just get V1 everywhere.

The Netlify deploy chains `make bsps && make build`, so production
gets the visibility filter on every push.

## Pickups tab

Two panels — Weapon Pickups (RL / LG / GL / SNG) and Item Pickups
(armors, MH, powerups) — each with a per-team and a per-player table.
Every *spawn entity* gets its own column (`RL @ ra-room`, taken from
`result.items[].phases`), RL/LG add a `pack` column, and each kind
closes with a `Σ` verify cell that compares the analytics-derived count
against KTX's own counter (silent on agreement, red ✗ on divergence).
A mode selector switches both tables between "All pickups" (Σ vs KTX
`total-taken`) and "First pickup per life" (Σ vs KTX `taken`).

Each kind then ends in a possession column, `<kind> s` — total seconds
that kind was held, joined by name from `playerStats.hold`
(`weapons` for RL/LG/GL/SNG, `armor` for RA/YA/GA, `powerups` for
Quad/Pent/Ring), with the same tooltip and raw-ms `data-sort-value` as
the Summary tab's possession cells. Two properties are deliberate:

- **One column per kind, never per entity.** Possession is an integral
  over the player's inventory stream; it knows only *that* the player
  held an RL, not which pad or pack it came from. A map with two RL
  spawns still gets exactly one `RL s` column.
- **It does not follow the mode selector.** "All" vs "first per life"
  changes what is counted, not how long anything was held.
- **MH has no possession column** — mega health is consumed on pickup,
  so `playerStats` carries no hold stat for it (same as the Summary
  table).

These rows come from `demoInfo.players`, not `playerStats`, so the join
is by player name and by team name (`playerStats.teams[].name`); a demo
whose `playerStats` lacks the row or the hold family renders `-`.

## Pack Drops tab

The Pack Drops tab shows every RL / LG backpack drop as one row,
joined with its pickup outcome. The drop side comes from
`result.backpacks`; the pickup side from `result.weaponPickups` entries
with `source == "backpack"`, joined on `(backpackEnt, dropTime)` —
the compound key is needed because QW servers recycle backpack
edict numbers across drops, so `entNum` alone would collide. A drop
with no matching pickup is shown as `expired`.

The "RL / LG only" scope is a wire-protocol limit, not a UI
decision: KTX's `//ktx drop` and `//ktx bp` directives fire only
for RL/LG packs, and the print-based fallback for other pack
classes is stripped from competitive MVDs by mvdsv's `messagelevel`
filter. See [`mvd-reader/MVD_FORMAT.md` → Practical gap — non-RL/LG
backpack pickups on competitive demos](../mvd-reader/MVD_FORMAT.md#svc_stufftext-9)
for the full mechanics.

Columns: Time, Dropper, Drop Team, Weapon, Drop (hub link),
Status, Picker, Pick Team, Kills, Run (hub link). Five filter
dropdowns above the table narrow rows by Dropper, Drop Team,
Picker, Pick Team, or Status label; each dropdown is populated
from the distinct values present in the current demo, and
selections persist across demo reloads when the same value is
still available in the new data.

Status column derivation:

| condition                               | label        |
|-----------------------------------------|--------------|
| no matching pickup                      | `expired`    |
| same team as dropper, picker !hadBefore | `xfer`       |
| same team as dropper, picker hadBefore  | `xfer RL/LG` |
| enemy team, picker !hadBefore           | `enemy`      |
| enemy team, picker hadBefore            | `enemy RL/LG`|

The `Kills` column is `weaponPickups[i].kills` — frags the picker
scored with the pack's weapon before their next death. Only
pickups that actually granted the weapon (the picker didn't have
it yet) are eligible for kill credit; redundant grabs — where
`hadBefore` is true and the pickup didn't give the picker anything
new — always show 0 and are dimmed. The denial semantics still
show through the status chip (`enemy RL`, `xfer RL`).

The `Drop` and `Run` columns are hub.quakeworld.nu replay links.
`Drop` spans 10 s leading into the drop, tracking the dropper;
`Run` spans 3 s before pickup to the picker's next death (or +15 s
if they survived to match end), tracking the picker.

## Map-tab item overlay

When the result contains an `items` field (any MVD source — KTX,
ktpro, CustomTF, etc.), the map tab renders every tracked item as a
small square and surfaces a sidebar panel listing each item with
live status (`up` or countdown to respawn) and its loc region.
Armors render as solid-filled coloured squares (RA/YA/GA); weapons,
MH and powerups are black squares with a coloured outline matching
the timeline palette plus a short text label (RL, LG, MH, Q, P, …).
Items currently taken are dimmed on the map and highlighted-dim in
the sidebar so verifying the event stream against gameplay is
visual. The panel updates live during playback via the 200 ms
full-sync tick in `animatePlayback`.

## Map-tab 3D view

The map opens in a default **isometric** view — yaw 45°, tilted 55°
from top-down (≈ the true isometric angle), so floors at different
heights separate at a glance and the layout reads from a corner. The
**3D** button toggles between this isometric view and the classic
top-down 2D view; right-drag (or Ctrl/Cmd+drag) rotates freely —
horizontal motion spins the map (yaw), vertical motion tilts it (pitch,
from top-down all the way to a horizontal side elevation at 0°). Yaw
lightly snaps to the four cardinal directions (±2°) so "look straight
along x / y" is easy to hit; the snap can be dragged through (the drag
applies absolute deltas from its start). **Reset view** and double-click
return to the default isometric view. Left-drag pan and wheel
zoom-about-cursor work at any
rotation (the zoom anchor is solved in view space, so it stays exact
even at pitch 0), as does click-to-follow (rotating does not drop
follow mode; panning does).

Each orbit drag pivots about what you're looking at: the followed
player if follow mode is on, else the focused region's centroid (at its
real floor height), else the world point currently at canvas center —
so "pan/zoom to a place, then rotate" orbits that place. The pivot swap
is pan-compensated (`setOrbitCenter`), so the view never jumps; Reset
view restores the default pivot (map center, mid height).

**Region focus** — clicking a loc region (on floor, not on a player
symbol) focuses it: the region and its XY-neighbors (bounding boxes
within ~160 units) render brighter and more solid while everything
else — fills, outlines, labels, region-control tint — fades to a faint
sketch. Click the same region, click empty space, press Escape, or
Reset view to clear. Code: `setFocusGroup` / `pickLocGroupAt` /
`focusTier`.

**Player animation source** — symbol positions (and the floor-height `H`
the anchor stem uses) come from the native-rate `result.streams.players[].pos`
tracks, binary-searched at the current time (`streamPosAt` /
`augmentPlayerData`, a non-mutating overlay on the cached bucket); the
state badges (health/armor/weapons) still read the bucket view. Orbit
pivot and click-to-follow hit-testing read the same stream position so
they line up with the drawn symbol. Trails stay on the bucket view.

**Floor anchor stems** — in any tilted view, each player symbol hangs a
thin team-colored stem down to the floor surface beneath it, ending in a
small ground dot. The drop is `z − 24 − H` using the per-sample
floor-height `H` (measured from the bottom of the player's bounding box,
which sits 24 below the origin) — so it is accurate on lifts (the floor
pass stands players on movers, which a static floor scan can't see) and
the stem is a direct visual readout of `H`. Falls back to a barycentric
scan of the floor geometry (`playerFloorZ`, memoised) when `H` is absent
(no BSP) or `NoFloor` (over a void).

**Floor model (the default view)** — the floor is a flat, near-opaque,
depth-sorted model (`buildFloorModel`): every region renders in one
neutral backdrop tone by default (colouring each loc by its own hue was
visual noise — colour now means *a player is here*, see the occupied
overlay below), with no Lambert/normal shading — from overhead it reads
dead flat. The floor's outer boundary is
extruded `FLOOR_SLAB_DEPTH` (10 units) down into flat box sides so the
floor reads as a solid 10u slab. `floorBoundaryEdges` finds that boundary
(edges shared by exactly one floor triangle across all regions + backdrop
— the true perimeter plus internal step risers); interior loc-region
boundaries are shared by two triangles and excluded, so no walls appear
inside a continuous floor. `floorBoundaryWalls` extrudes the edges into
side triangles. Because everything is **near-opaque and painter-sorted**,
a higher floor cleanly *covers* a lower one rather than tinting it through
translucency (the translucent stacking used to read as "shading"); the box
sides read as solid thickness, not a dark smear. Players, items, liquids
and overlays all draw live on top. `renderSolidEntries` also strokes each
fill-batch with its own colour at a hairline width, sealing the
anti-aliasing seams between adjacent triangles so a continuous floor reads
as one clean surface instead of showing its triangulation as a mesh.

**Occupied-region overlay** — a region a living player currently stands
in is tinted by the team(s) present (`drawOccupiedRegionsOverlay`): one
team → that team's canonical colour, both teams → white (contested),
drawn live over the neutral floor with a brighter outline and bold label.
This is the *only* place a region takes on colour, so a coloured patch
always means "someone is here". Team membership comes from the canonical
`playerSymbols[name].teamIdx`, so it matches team colours everywhere else.

`playerSymbols` is built by `assignPlayerSymbols()` from the KTX demoinfo
roster where the demo has one and from `playerStats.players` otherwise —
it is what the draw loop, the legend, the trail dropdown and the sidebar
roster all key off, so a demo with no KTX block would otherwise render a
map with nobody on it. Team colours still come from `getTeamOrder()` /
`timelineState.teams`, never from either roster's own order. The legend
lists each team's players **by name**, not in roster order; its column
headers stay `makeSortable`-clickable for any other order.

**View / velocity arrows** — two optional per-player toggles, **View**
and **Vel**, draw 3D arrows from each player's origin (`drawPlayerArrows`
/ `drawWorldArrow`). View is a fixed-length (64u) facing indicator built
from the stream's `vya`/`vp` view angles (Quake forward vector). Vel
encodes the stream's `vx`/`vy`/`vz` velocity with length proportional to
speed (5 u/s per world unit) in the player's team colour, hidden below
10 u/s. Both project the shaft through the orbit camera with a screen-space
arrowhead at the projected tip, so they tilt correctly with the view.

**LOS / PVS overlays** — two optional per-player toggles, **LOS** and
**PVS**, draw inter-player visibility lines on the map (`drawLosLines` →
`drawVisLines`). **LOS** is geometric line of sight: a line between two
players who currently have a clear sightline (origin-to-origin ray
against the map's BSP clip hull and any moving movers); it is
directional, so a one-way sightline shows on the looker's side only.
**PVS** is the server-reproduced potential-visibility set — whether a
live mvdsv would have sent that opponent's entity to this player's client
that frame (wire-exact against `SV_PlayerVisibleToClient`), regardless of
occlusion. PVS ⊇ LOS by construction, so PVS draws first as thinner,
fainter lines (`PVS_STYLE`) under the thicker LOS lines (`LOS_STYLE`),
and the PVS-minus-LOS gap reads as occlusion-tolerant proximity. Both
toggles ride **one** lazy compute pass over the already-parsed demo
(`ensureLosComputed` → the `computeLineOfSight` worker export; the
`mapState.losComputed` latch fills `losByPair` and `pvsByPair` together
on first need), so the first toggle-on incurs the heaviest
position-derived pass and later toggles are instant. BSP-gated — the
toggles are inert on maps without a provisioned BSP.

The floor model renders into an offscreen canvas keyed by the full camera
state (`drawCachedWorld`); steady playback just blits it (~1 ms), only
rotation/pan/zoom/focus changes re-render. The painter sort scatters
same-colour triangles so per-frame batching would cost many `fill()`
calls — hence the bitmap cache. Code: `buildFloorModel` /
`renderSolidEntries` / `drawCachedWorld`.

(An earlier occluding **Solid** mode drew the map's vertical walls on top
of the floor model; it was removed, and the generator no longer emits the
`walls` triangle list it needed — see "Map geometry versions" below.)

**Movers** — on version-4 geometry (carries `submodels`) plus a result
with `streams.movers` (schema v32), lifts/doors/plats animate at their
demo-streamed poses during playback. Each is drawn as a moving piece of
floor: the submodel mesh offset by the pose origin binary-searched for the
current time (`moverPoseAt`), **backface-culled** to its near hull (the
submodel triangulation winds so its normals point into the solid, so the
near hull is the faces whose normal points away from the camera) and
filled **once** as a single flat silhouette at the same near-opaque alpha
as the floor tops, a touch lighter than the backdrop floor so the moving
piece stays legible (`MOVER_FILL`). When a player is riding it (their XY
within the posed footprint and z within a player-height window of its top,
`playerOnMover`) it takes the brighter `MOVER_FILL_ACTIVE` tone so it
stands out like an occupied region. One fill at one alpha → no per-face
double-blend, no painter-sort flicker. A mover sampled `vis=false` is hidden. Missing
either piece (older geometry, or a demo with no movers) is a graceful
no-op. Code: `drawMovers` / `moverPoseAt` / `moverMeshFaces` /
`drawMoverMesh`.

**Weapon fire** — with `streams.projectiles` / `streams.beams` (schema
v40, built by the WASM map analysis), rocket/grenade flights and LG bolts
overlay the map during playback. Each in-flight rocket/grenade is a small
dot interpolated along its spawn→despawn segment at the current time
(orange for `rl`, green for `gl`); each LG bolt is a brief light-blue line
from muzzle to impact, flashed for ~60 ms around its instant. Both are
columnar parallel-array streams; absent (e.g. a non-WASM result that
didn't build them) is a graceful no-op. Nails (`streams.nails`, small
yellow dots) render the same way and are now built by the web parse too
(`BuildNails` in `cmd/wasm/main.go`, added alongside `BuildShotStreams`):
the map overlay lights up automatically and the Aim tab's ng/sng blocks
fill in. Nails are the highest-volume stream but add only ~3–4% to the
parse, all in browser memory (no extra download). Code: `drawProjectiles`
/ `drawBeams` / `drawFlightDots`.

**Liquids** — version-4 geometry also carries `liquids` (water/slime/lava
volume meshes). Rendered as a shaded, depth-sorted translucent solid
(`drawLiquidVolume`): each face is Lambert-shaded so the top surface reads
brighter than the descending sides, and faces paint back-to-front, so the
body reads as a 3D volume with visible depth (water blue, slime green,
lava orange). The per-face alpha is kept low (`LIQUID_ALPHA`) so the
volume reads as a faint tint rather than dominating the floor under it.
They draw live above the region fills and below the outlines/players.

Everything is drawn through one orbit-camera orthographic projection
(`projectWorld` in `app.js`): floor geometry uses the per-vertex heights
in the version-2 map JSON, so each floor renders at its real level, and
player tracks, player symbols, items, death/drop markers, loc labels and
the region-control / occupancy overlays all project through the same
transform. At exact top-down (the **3D** toggle's other state) the
projection degenerates to the old 2D transform — pixel-identical to the
previous 2D map — and the painter's sort (projected camera depth)
degenerates to the old z-sort. Opaque markers (players, items, entities)
are depth-sorted per frame.

Version-1 geometry files (e.g. a stale browser cache) are upgraded on
load by `normalizeMapGeometry`, which flattens each region to its median
z — top-down looks identical, 3D shows flat-per-region floors.
Version-2+ files work fully. The
height-based player-symbol size scaling (higher = up to 25% larger) is a
2D-only cue and is disabled while the camera is tilted. Camera state
lives in `_wtc` (`yaw`, `pitch`, orbit center `cx/cy/zMid`); rotation
goes through `setMapCamera`.

## Map-tab "Learn map" mode

When the result contains a `mapEntities` field (the static per-map
layout from the embedded corpus — see
[`mvd-analytics/RESULT_SCHEMA.md`](../mvd-analytics/RESULT_SCHEMA.md#mapentitiesresult-mapentities)),
the map controls show a **Learn map** toggle. It switches the canvas to
a static study view: players, trails and time-based overlays are
hidden, the floor/loc base is kept, and the map's designed entities are
drawn — item spawns, player spawnpoints, teleporters and buttons.

A sidebar checklist toggles categories (Weapons, Armor, Health, Ammo,
Powerups, Teleporters, Spawns, Buttons, Doors); spawns/buttons/doors
start off to reduce clutter. Teleporters draw an arrow from each
entrance to its exit (paired by `teleportSrc.target` ==
`teleportDst.targetName`). Entities reuse the same `worldToCanvas`
transform and item palette as playback, so they sit exactly where
players do. The corpus is fetched in-browser by `fetchMapEntsSync`
(`worker.js`) from `mapents/<map>.json` (deployed by `make build`); the
toggle is hidden when no corpus exists for the map.

Below the canvas, a sortable table (standard `.stats-table` style,
expanding with the tab — no inner scrollbar) lists every visible
entity — Class (cleartext: Armor, Weapon, …), Type (kind: ra, h25, …),
Name, Loc, and Destination. Teleporters collapse to one row per
entrance→exit pair, with the entrance in **Loc** (where the trigger
sits) and the exit it leads to in **Destination**. The table respects
the category filters and rebuilds via `buildEntityTable`.

Learn mode is reflected in the URL as `?learn=1` (alongside `tab=map`),
so a study view is directly link-shareable; `applyUrlState` restores it
on load when the map has a corpus. Code: `drawMapEntities` /
`setLearnMode` / `buildEntityTable` in `app.js`.

## Locs & Regions tab

(`data-tab="loc-graph"`; the URL slug is now `locs-regions`, with
`loc-graph` still accepted — see the tab-alias note below.) Top to bottom:
**Region Control**, then a standalone **Metric** selector, then the loc
**graph** and **heatmap**. All read `result.locGraph` (loc nodes weighted
by **alive, observed** time-spent — dead players and unobserved holes are
excluded since schema v64 — transition edges; per-player and per-team
breakdowns baked onto every node) plus `demoInfo.{teams,players}` / `mapState.controlStats`
— no extra analyzer pass.

The **Metric** selector (`#locgraph-metric`, its own panel above the graph
so it clearly governs both loc views but *not* Region Control) reweights
the loc graph and heatmap by combat posture, yielding a **self-contained
graph per case** — its own nodes *and* edges: *Full time* (all observed
time), *With RL / LG* (the `armed` LocWeights / LocEdgeWeights), *Without
RL / LG* (`unarmed`, the complement), *With Quad* (`quad`), or *With Pent*
(`pent`). It drives node sizes (occupancy: `getLocMetric` →
`metricWeightsOf` → `nodeWeightFor`), edge widths (movement:
`metricEdgeWeightsOf` → `edgeWeightFor`, edges absent from the case are
pruned and locs with no presence dimmed), and the heatmap (which renders
for every metric, including the sparse quad / pent cases).
`populateLocMetricOptions` hides the metrics a given demo has no data for
(presence of the node's `armed`/`quad`/`pent` sub-object == availability;
e.g. quad usually absent in 1v1), and falls back to *Full time* if the
current pick goes away — so a metric can't leave an empty graph + table.

- The **movement graph** — a Cytoscape.js node/edge diagram with the
  filter / edge-mode / layout controls, driven by `initLocGraphView` and
  `buildOrRefreshCytoscape`.
- The **Loc Heatmap** (`buildLocHeatmap`) — rows are locs (busiest
  first); the leading columns are the **teams** (every member's time
  combined), then one column per **player** grouped by team, with a
  separator before the player block. Cell intensity is that column's
  share of its (metric) time in the loc, normalised **per column** to its
  own busiest loc (sqrt-curved). The team columns are dropped for duels
  and single-team demos.
- **Region Control** (`buildRegionHeatmap`) — the region definition
  editor (`buildRegionConfig`, group locs into named regions; save/load
  JSON) plus the per-region control matrix: rows are regions, columns are
  the seven control states (teamA control/weak, contested, cont. weak,
  empty, teamB weak/control). Moved here from the Map tab; the live
  region *overlay* and *status* still render on the Map. Initialised by
  `initRegionControl` (from `initMapView`) and recomputed through the
  `recomputeRegionControl` WASM bridge on edits (`renderRegionControlFromGo`).
  Cells are normalised **per region** to that region's busiest control
  state (Empty excluded — it is filler, not a control state, and would
  swamp the scale) so each row spans the full colormap; the printed %
  stays the absolute match fraction.

The two matrices share one renderer, `renderHeatmapTable`, fed a
policy-free model — `{ rows:[{name,cells:[{i,p}]}], columns:[{label,full,
team,teamIdx,…}], teamCols, cellTitle }` where `i` is a 0..1 intensity
(normalisation already baked in by the `build*` function) and `p` the
printed %. It renders a sortable `.stats-table` (crisp text + free column
sorting via `makeSortable`, tbody built with the shared `renderTableRows`
helper) rather than a canvas; each cell is viridis-shaded
(`heatColorRGB` / `HEAT_STOPS`, mirrored by the CSS `.heatmap-legend-bar`
gradient — chosen for red/green colour-vision-deficiency safety) with a
contrast-aware ink and a `data-sort-value`. Team identity rides on the
canonical `TEAM_COLORS`-by-`timelineState.teams` mapping (see the repo
CLAUDE.md "Team colors" convention) as a colored underline on the
relevant column headers. Player column headers show a truncated name with
the full name on the header `title` — QuakeWorld's in-game short name
(`cl_fakename`) is a client-side say_team text prefix, not carried in the
demo stream, so there's no per-player short name to read.

**Tab URL alias.** The tab's internal `data-tab` stayed `loc-graph` (so
JS / CSS selectors are unchanged), but the rename to "Locs & Regions" gave
it the canonical URL slug `locs-regions`. `switchTab` / `applyUrlState`
run incoming `?tab` through `resolveTabName` (`locs-regions → loc-graph`)
and `updateUrlState` writes `locs-regions`, so new links use the new slug
while old `?tab=loc-graph` links keep resolving.

## Regenerating map geometry

Per-map floor polygon JSON under `static/maps/` is produced by the
`mapgen` developer tool, which reads Quake 1 BSPs from an off-repo
working directory. Files are geometry version 2 (9 floats per
triangle — x,y,z per vertex), version 3 (added a top-level `walls`
triangle list for the since-removed Solid mode — the generator no longer
emits it, though the reader still tolerates it in older files), or
version 4 (adds optional `liquids` water/slime/lava volume meshes and
`submodels` brush-model lifts/doors, and drops degenerate zero-area
triangles). The frontend is presence-based and accepts every version
(v1 — 6 floats, XY only — is flattened to each region's median z on load;
missing `walls`/`liquids`/`submodels` simply render nothing). A
usage-pruned file carries a `pruned` provenance block.
See
[mvd-analytics/README.md](../mvd-analytics/README.md) (the `cmd/mapgen`
entry) and `CLAUDE.md`'s quick reference for the workflow.

## Module boundary

mvd-web depends on mvd-reader (to open MVD byte streams) and mvd-analytics
(to run the pipeline). It has no source of its own that mvd-reader or
mvd-analytics depends on.
