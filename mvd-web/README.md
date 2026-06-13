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
  - `maps/` — pre-generated per-map floor polygon JSON (version 2:
    per-vertex x,y,z — drives the map tab's 3D view). Committed; the
    frontend fetches `maps/<basename>.json` at demo load.
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
  maps/                       pre-generated map geometry
  locs/                       .loc files copied from mvd-analytics/loc/data
  bsps/                       BSP files from `make bsps` for the locvis
                              visibility filter (skipped if bsps/ is empty)
```

### Netlify deploy

`netlify.toml` at the repo root chains `make bsps && make build` and
publishes `dist/`. Every push to a branch with Netlify connected
rebuilds and deploys. `make bsps` runs on Netlify's build container
(it has `curl` and `bash`), fetches the ~14 competitive-map BSPs from
the public mirrors documented in `scripts/fetch-bsps.sh`, and verifies
each sha256 — a missing or corrupt BSP hard-fails the deploy, which
is preferred to a silent V1-everywhere regression.

## Layout

A slim top bar (wordmark + commit-hash version + GitHub link) sits
above a Grafana-style frame: a fixed left **sidebar** with one button
per analysis tab, and a **main pane** that fills the rest of the
viewport (no width cap). Sidebar order is `Search`, `Summary`,
`Timeline`, `Chat`, `Map`, `Locs & Regions`, `Key Moments`, `Pack Drops`.
The **Key Moments** tab has three tables: powerup runs, longest frag
streaks, and a full-width **Airborne Rocket Gibs** table — enemy rocket
hits on airborne victims (`timelineAnalysis.airgibs`), sortable by any
column and defaulting to height-above-shooter descending (the vertical
gap the rocket climbed). Its rows are empty unless the map's BSP is
provisioned (height needs the clip hull; see `PositionTrack.h`).

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

`cmd/wasm/main.go` also exports `getDemoInfo()`, which returns just the
KTX demoinfo summary (`result.DemoInfo` — map, players, teams, scores,
date) from the pinned `lastResult` as JSON. It is zero extra cost (the
data is already computed) and lets a consumer read the match summary
without re-marshalling the full Result. Note: the demoinfo block is
written near the **end** of the MVD stream, so obtaining it still
requires decoding the whole demo — cheap to *read*, not cheap to *skip
ahead to*.

The WASM boundary is the only place that bridges Go and JS. The rest of
the frontend is dependency-free JS plus a sprinkle of CSS.

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

The map starts in the classic top-down 2D view and can be rotated into
3D: the **3D** button tilts the camera to a default oblique view, and
right-drag (or Ctrl/Cmd+drag) rotates freely — horizontal motion spins
the map (yaw), vertical motion tilts it (pitch, from top-down all the
way to a horizontal side elevation at 0°). Yaw lightly snaps to the
four cardinal directions (±2°) so "look straight along x / y" is easy
to hit; the snap can be dragged through (the drag applies absolute
deltas from its start). **Reset view** and double-click return to
top-down. Left-drag pan and wheel zoom-about-cursor work at any
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

**Floor boxes** — in Solid mode the floor renders as one solid box
`FLOOR_SLAB_DEPTH` (20 units) tall rather than zero-height planes:
`floorBoundaryWalls` extrudes only the floor's *outer* boundary (edges
shared by exactly one floor triangle across all regions) down 20u into
flat side walls. Internal loc-region boundaries are shared by two
triangles, so they cancel and leave no walls inside a continuous floor —
the box is clean, with thickness showing at the perimeter, steps and
raised platforms. (In non-solid tilted views the older per-region "skirt"
still hints depth at 20u.)

**Solid mode** — for maps whose geometry JSON is version 3 (carries
wall triangles), a **Solid** toggle appears: floors and walls are
painter-sorted by projected camera depth and drawn near-opaque, so upper
floors genuinely occlude lower ones and the map reads as an architectural
model. Floors use a single flat per-region tone (plus darker slab sides);
**walls** keep per-face Lambert shading — the floor Lambert looked patchy,
so it was removed. Players, items and overlays still draw on top — seeing
the action through walls is the point. The sorted model renders into an
offscreen canvas keyed by the full camera state; steady playback just
blits it (~1 ms), only rotation/pan/zoom/focus changes re-render. Code:
`mapSolidEntries` / `renderSolidEntries` / `drawSolidWorld`.

**Movers** — on version-4 geometry (carries `submodels`) plus a result
with `streams.movers` (schema v32), lifts/doors/plats animate at their
demo-streamed poses during playback. Each is drawn as its actual submodel
mesh, offset by the pose origin binary-searched for the current time
(`moverPoseAt`), Lambert-shaded per triangle (shade quantized so coplanar
faces share it — the triangulation vanishes, the real lift shape shows).
Faces are **backface-culled** (BSP normals are outward; only the near hull
draws) and **opaque**, so a closed mover renders solid with no see-through
or painter-sort flicker. Painter-sorted, batched by shade, no stroke; same
renderer in both modes (in Solid mode drawn *after* the time-free world
blit). A mover sampled `vis=false` is hidden. Missing either piece (older
geometry, or a demo with no movers) is a graceful no-op. Code:
`drawMovers` / `moverPoseAt` / `moverShadedMesh` / `drawMoverMesh`.

**Liquids** — version-4 geometry also carries `liquids` (water/slime/lava
volume meshes). Rendered as a shaded, depth-sorted translucent solid
(`drawLiquidVolume`): each face is Lambert-shaded so the top surface reads
brighter than the descending sides, and faces paint back-to-front, so the
body reads as a 3D volume with visible depth (water blue, slime green,
lava orange). In flat mode they draw above the region fills and below the
outlines/players; in Solid mode they bake into the offscreen cache on top
of the opaque world (the liquid count is part of the cache key). Liquids
are not pickable in edit mode but round-trip through Export JSON.

Everything is drawn through one orbit-camera orthographic projection
(`projectWorld` in `app.js`): floor geometry uses the per-vertex heights
in the version-2 map JSON, so each floor renders at its real level, and
player tracks, player symbols, items, death/drop markers, loc labels and
the region-control / occupancy overlays all project through the same
transform. At exact top-down the projection degenerates to the old 2D
transform — the default view is pixel-identical to the previous 2D map,
and the painter's sort (projected camera depth) degenerates to the old
z-sort. Floor fills stay semi-transparent, so tilted views read as a
see-through architectural model rather than occluding solids; opaque
markers (players, items, entities) are depth-sorted per frame.

Version-1 geometry files (e.g. a stale browser cache) are upgraded on
load by `normalizeMapGeometry`, which flattens each region to its median
z — top-down looks identical, 3D shows flat-per-region floors.
Version-2 files work fully except Solid mode (no walls). The
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

## Map geometry edit mode (`?edit=1`)

A hidden cleanup tool for the committed geometry corpus, enabled by
adding `edit=1` to the URL. A toolbar appears under the map controls
and canvas clicks switch from follow/focus to triangle selection:

- **Select** — click highlights the connected coplanar *patch* under
  the cursor (≈ the original BSP face, merging same-plane neighbours);
  a dropdown switches to single-triangle mode. Shift-click adds/toggles.
  Hovered triangles get a live outline. In Solid mode wall triangles
  are selectable too. Adjacent zero-area slivers (degenerate fan
  triangles in older corpus files) are swept into the patch so deleting
  it leaves no stranded, unselectable line behind.
- **Delete / Undo** — `Del` removes the selection (e.g. stray roof or
  out-of-bounds detail the extractor kept); `Ctrl+Z` undoes. Undo
  snapshots are whole-array references — deletion replaces arrays,
  never mutates — so the stack is cheap and exact.
- **Export JSON** — downloads the current geometry as a version-4 file
  (per-vertex tris + walls, coordinates rounded to 2 decimals), round-
  tripping any `liquids` / `submodels` / `pruned` blocks (not editable, so
  passed through unchanged), drop-in compatible with
  `mvd-web/static/maps/<map>.json`.
- **Regenerate** — re-runs the mapgen extraction *in the browser*
  against the server-hosted BSP (`bsps/<map>.bsp`, the core-map set
  fetched by `make bsps`), with tunable thresholds: floor slope
  (`floorNormalZ`, 0.7 ≈ 45°), roof cap (`ceilingMaxAboveLoc`), and
  standing-origin height. The WASM export `generateMapGeometry`
  (cmd/wasm) parses the BSP via the same `fetchBspSync` host callback
  the locvis filter uses and calls `mapgeom.BuildParams`; the result
  replaces the live geometry, ready for further editing and export.

Code: the `editState` section in `app.js` (`pickEditTriAt`,
`expandPatch`, `editDeleteSelection`, `exportEditedGeometry`,
`regenerateGeometryFromBsp`).

## Locs & Regions tab

(`data-tab="loc-graph"`; the URL slug is now `locs-regions`, with
`loc-graph` still accepted — see the tab-alias note below.) Top to bottom:
**Region Control**, then a standalone **Metric** selector, then the loc
**graph** and **heatmap**. All read `result.locGraph` (loc nodes weighted
by time-spent, transition edges; per-player and per-team breakdowns baked
onto every node) plus `demoInfo.{teams,players}` / `mapState.controlStats`
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
triangle — x,y,z per vertex), version 3 (adds a top-level `walls`
triangle list for the map tab's Solid mode), or version 4 (adds optional
`liquids` water/slime/lava volume meshes and `submodels` brush-model
lifts/doors, and drops degenerate zero-area triangles). The frontend is
presence-based and accepts every version (v1 — 6 floats, XY only — is
flattened to each region's median z on load; missing `walls`/`liquids`/
`submodels` simply render nothing). A usage-pruned file carries a
`pruned` provenance block.
See
[mvd-analytics/README.md](../mvd-analytics/README.md) (the `cmd/mapgen`
entry) and `CLAUDE.md`'s quick reference for the workflow.

## Module boundary

mvd-web depends on mvd-reader (to open MVD byte streams) and mvd-analytics
(to run the pipeline). It has no source of its own that mvd-reader or
mvd-analytics depends on.
