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
There is **one accuracy table per weapon**, in the order `lg`, `sg`, `ssg`,
`rl`, `gl`, `ng`, `sng`, `axe`, skipping any weapon nobody fired. The axe
takes the four generic columns the nailguns take (shots / hits / dmg /
hit %): one swing sound per attack is all it carries (schema v71), with no
spread, splash or beam to classify a miss by. It was missing from that list
until now, which silently dropped every axe swing on the tab — 107 of them
on one player in the committed corpus — even though the shot stream has
carried them since v71 and the recon tier recovers their hits.
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
**Hits / Hit % serve three states**, keyed off `aim.hitsMeasured` +
`aim.hitsSource` (schema v73/v74). Measured (`hitsSource: "ktx"`) renders
plainly. On a demo with no wire damage log the measured counters are
withheld and the columns fall back to `weapons[].recon.hits` — the
reconstruction's recovered count over the same fires, so it divides by the
same `Shots` — rendered with the `.stat-recon` dotted underline and a
tooltip naming what it is. Where neither answers, the cell is `—` and its
tooltip says which of the two reasons applies: the tier covers
lg/sg/ssg/axe/rl/gl and stops there (ng/sng are never recovered), and it
carries **no victim split**, so the Victims filter drops back to `—`
outside `All` rather than serving an all-victims number under an
Enemy/Team/Self heading. `hits: 0` inside a present block is a real
"linked nothing" and prints `0`.
Every OTHER hit-derived column — the pellet block, full/partial/miss,
direct/splash/missed, the LG miss types and all their % twins — is withheld
by the analyzer on such a demo and now renders `—` too. It used to print a
fabricated `0`, which beside a recovered 22% hit rate read as a measurement
that contradicted it; the reconstruction recovers hit COUNTS only, never
the breakdown of how a fire hit or missed.
The **Key Moments** tab has ten tables — a two-column grid of powerup
runs beside the longest frag streaks, **Top Damage Windows (10 s)**
beside **Top RL Kills**, **Demo Markers** beside **Top LG Kills** — and
four full-width **highlight** tables read from the stored highlight
catalogue (`result.highlights`, schema v76), where every row carries what
each participant HAD at the instant (health/armor+type, tracked weapons,
powerups, loc; `(spawn)` marks a victim whose spawn stats had not reached
the wire, so the spawn state 100/0 is shown):

- **Direct Rocket Air Hits** (`highlights.airgibs`) — direct enemy rocket hits on airborne
  victims, lethal or not (the Lethal column's `gib` badge marks the ones
  that killed), defaulting to height-above-shooter descending (the
  vertical gap the rocket climbed). Its rows are empty unless the map's
  BSP is provisioned (height needs the clip hull; see `PositionTrack.h`).
- **Discharges** (`highlights.discharges`) — every LG water discharge with
  evidence: the cells dumped, numeric Enemy / Team / Self kill columns, the
  frag delta (enemy − team − self, KTX's scoring), the bounded given
  damage split into enemy / team columns (every victim hit, killed or
  not), and the victims split into Enemy / Team columns — one line per
  victim (name, stack, gear; dimmed when hurt but not killed). Armor and
  powerups carry the timeline's colors and caps ("180 RA", "Quad").
  Default sort: enemy kills, then damage.
- **Quadbores** (`highlights.quadbores`) — self-kills by own rocket or
  grenade while holding quad: quad left (30 s KTX quad minus the time
  held), the quad's frags before the bore, the stack thrown away, anyone
  the same rocket took along. Default sort: quad left descending.
- **Telefrags** (`highlights.telefrags`) — every `tele` death: killer,
  victim, relation, the victim's stack (the ranking scalar), gear, and the
  kind (`telefrag` / `deflect` — the teleporter died on a pentagram holder,
  shown as *survived* / `spawnicide`). Default sort: stack descending, with
  the rows where the teleporter died last.

All four are sortable by any column (`makeSortable`), seek the timeline on
the time cell, and link the Hub replay from the actor's perspective. The
**Demo Markers** table lists the
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
and line of sight. **Top Damage Windows (10 s)** is the top 10 windows by bounded enemy
damage (`metric=damageGiven`, `windowMs=10000`, `dmg=bounded` — the same
family as the kill-burst tables beside it, ties breaking on the window's
frags via the view's complementary tie-break), showing the frag count
beside the damage; **Top RL Kills** (10) and **Top LG
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
lines (`event.type === 'teamsay'`) from the chat column(s); frags and
public `say` are untouched, and the flag resets per demo load. Its columns
are Kills plus one per side — or, in an individual layout that is not a
duel, Kills plus a single "Chat" column (see *A mode with no teams* below).

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
- **Basic Stats shows `eRL` / `eLG`, not kills-with-RL/LG.** Those two
  columns come from `score.byEnemyWeapon` (schema v69) — enemies killed
  while THEY were holding a rocket launcher / lightning gun. Kills made
  *with* each weapon are the Weapon Stats tab's `K` columns, so the
  scoreboard carries the metric that was nowhere rather than the one
  already in two places. The buckets are exclusive, so each cell renders
  **`rl + both`** (resp. `lg + both`): reading the `rl` key alone would
  drop every victim who was carrying both. Measuredness rides the kill
  family — the cell renders `-` exactly when `kills` does.
- **Weapon Stats carries a fourth column per weapon, `eK`.** Of that
  weapon's kills, how many landed on an enemy who was carrying an RL
  and/or LG — the `rl + lg + both` slice of `score.byWeaponVsEnemyWeapon`
  (schema v69), the cross-tab of killer weapon against victim loadout. It
  separates a weapon that wins fights from one that finishes off the
  disarmed. The column shows only that total to keep the table narrow; the
  **full six-bucket breakdown is in each cell's tooltip**, so nothing is
  hidden. A measured `0` prints as `0` — "every one of those kills was on
  someone carrying nothing" is a reading, not a gap — and `-` appears only
  where the `K` cell beside it is also `-`. Both weapon tables now scroll
  inside their own box (`.items-table-wrap`) at 25 columns.
- **Basic Stats has `TDmg` between `Dmg` and `Taken`** — `damage.givenTeam`,
  friendly fire DEALT. It is measured whenever the damage family is present
  (on `src: "reconstructed"` demos — pre-instrumentation, rebuilt damage —
  read every damage figure as an estimate rather than a measurement: ~1%
  median error **at full coverage**, and a FLOOR below it, since a
  `damage.coverage.ratio` under 1 means the evidence never saw part of the
  match at all. The panel banner prints that ratio — see "Reconstructed
  damage" below),
  so `0` there is a real reading rather than a gap, and it is deliberately
  *not* folded into the victim's `Taken`-side story: this is what the player
  put into teammates, a different question from `TK` (which counts only the
  friendly damage that finished someone off). Always 0 in a duel.
- **Basic Stats ends the kill block with `Spree` / `QSpree`** —
  `score.maxSpree` / `score.maxQuadSpree` (schema v74), the longest kill run
  between deaths and the longest run held under quad. They ride the kill
  family's measuredness with everything else in it, so they render `-`
  exactly where `Kills` does, and a team row carries the **best any member
  ran, never a sum**. Our gate does not count a suicide as a kill where KTX's
  does (teamplay off), so a duel or FFA row can read one lower than the
  server's own `spree.max`; the `<th title>` says so.
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

### Reconstructed damage says so, above every table that rides it

Roughly half the archive predates the server's `mvdhidden_dmgdone` log, and
on those demos the damage section is **rebuilt** rather than measured
(`damage.source: "reconstructed"`, schema v71). The WASM build reaches that
path on every eligible demo — `registry.BuildShotStreams = true` in
`cmd/wasm/main.go` is the precondition, and it is set unconditionally — so
this is a normal state in the browser, not an edge case.

Everything downstream inherits its evidence grade from that one field
rather than carrying a copy: the Basic Stats damage columns, Weapon Stats'
`Dmg` and `Acc`, the Aim tab's `Dmg` and recovered `Hits`, and the three
damage-ranked tables on Key Moments (**Top Damage Windows**, **Top RL
Kills**, **Top LG Kills** — their `Dmg`, `Hits` and `Return` figures are the
same bounded family, read through `view.TopWindows` / `view.TopKills`). So
one renderer (`damageProvenanceHtml`) states it once above each of those six
panels, in a `.panel-provenance` banner — always visible, unlike the
`More info` disclosures, because a reconstructed damage figure read as a
measured one is the mistake the banner exists to prevent. Powerup Runs, the
frag streaks and Demo Markers carry no banner: they are frag-log-only and
show no damage-derived figure.

The second line of the banner is `damage.coverage` (schema v74): how much
of the frag-log-visible match the reconstruction's evidence actually
accounts for. **`ratio` is printed as a magnitude, never thresholded** —
the pipeline has no cutoff and neither does this; the only comparison in
the code is against `1`, which is the definition of a complete section
rather than a number the UI picked. A complete section reads "the evidence
accounts for all N of the match's scoreable kills"; an incomplete one takes
the amber `.warn` variant and reads "Damage evidence covers 23% of this
match's kills (22 of 96 scoreable kills) — the figures below are floors,
not measurements". Absent coverage on a reconstructed section is its own
sentence ("the frag log named no scoreable kill, so … was not assessed"),
because silence there would read as "complete". The printed percentage
never reaches 100 while the ratio is below it: a near-complete section
(one uncovered kill out of 200+, which is a normal 4on4) would otherwise
round up to a bold "100%" inside the sentence saying it is incomplete, so
`coveragePct` truncates to two decimals in that window instead.

Per-weapon accuracy carries the same grade at cell level: a
`playerStats.accuracy` family with `src: "reconstructed"` renders its `Acc`
cells inside `.stat-recon`. `derived` and `ktx` do not — both are measured
off the wire, and marking half the corpus would make the mark meaningless.

### The `≠` on an Acc cell: two scales, one column

The WASM entry point applies the KTX overlay (`view.PlayerStats`, in
`withPlayerStatsOverlay`), so the `Acc` column serves whichever accuracy
family the demo has — and they were not all counted the same way. KTX's
own counter is direct impacts for `rl`/`gl` and PELLETS for `sg`/`ssg`;
before v75 the `derived` family counted any fire that landed damage, on
every weapon, which on `rl` is ~4.5× the KTX number. Same column, same
`%`, different question — and the comparison people actually make is
between two screenshots, where a tooltip does not exist.

So a cell whose `hitsConvention` (v74) is not the one KTX uses for that
weapon wears a **`≠`** in the text, with a footnote under the table that
appears exactly when some cell wears it. The rule is per weapon, not per
family (`ktxHitsConvention` mirrors `view.ktxHitsConvention`), and since
v75 every computed tier publishes KTX's convention on every weapon but
one, so the mark has shrunk from four cells to one. A KTX-overlaid family
matches on every weapon and is never marked. A `derived` one is not marked
at all here: its `rl`/`gl` publish direct impacts and its `sg`/`ssg`
pellets. A `reconstructed` one is marked on **`sg`/`ssg` alone** (its
`rl`/`gl` publish `recon.directHits`, v74), which is not an oversight — a
reconstructed damage delta merges every hit landing on one instant, so
dividing its magnitude into pellets would credit one shooter with
another's.

`gl` wore the mark on a `derived` row until v75 and is the interesting
case, because the wire genuinely cannot answer its question: a grenade
that touches a player detonates, and every damage row the server then
writes is flagged splash, so counting non-splash `gl` rows off the wire
reproduces 0.00% of the block's total. The touch is re-derived instead —
from the grenade's tracked flight, its detonation point against the
victim's hull and the 2.5 s fuse — by the same classifier a
pre-instrumentation demo uses (92% of 424 archive player rows exact
against the verbatim block). This build always parses with the projectile
streams that classifier reads, so a `gl` cell here is always on KTX's
scale; a payload from a parse without them says `anyDamage` and is marked
like any other off-scale cell. Two `Acc` figures are comparable exactly
when weapon **and** convention match; the full convention text stays in
the tooltip.

The Aim tab's `Hits` column is deliberately the OTHER number on those two
weapons: it stands in for the withheld measured counter, which is an
any-path count, so a reconstructed `RL` figure there reads higher than the
Summary's `Acc`. The same split now exists on a wire-measured demo, where
the Aim tab's `Hits`/`Hit %` are the any-path counters and the Summary's
`Acc` is KTX's direct-impact question — the Aim tab's own `Direct` column
is the number the Summary shows, on `GL` as well as `RL` since v75. Both
are labelled; neither is a correction of the other. (A `GL` `Direct` can
read ABOVE its `Hits`: it is a touch count from the flight geometry, not a
subset of the fires the linker connected, so a touch whose fire went
unlinked still counts and `Splash` floors at zero.)
A reader with both on screen gets the reconciliation IN THE PAYLOAD, not
only in the source: the Aim cell's tooltip on `rl`/`gl` names its own
convention and the Summary number it differs from, with the size of the
gap (~4x on RL, ~1.5x on GL — `RECON_ANYDAMAGE_NOTE`, appended to
`RECON_ACCURACY_NOTE` for those two weapons only, since everywhere else
the two conventions coincide and the sentence would invent a difference).

Note that `getAccuracyClass` (`accuracy-high/medium/low`) is currently
inert on these cells — the only rule for those classes is scoped to
`#accuracy-table`, an id no longer in the DOM — so the colour thresholds
are not, today, a second cross-convention trap. Anyone re-pointing that
rule must scope the thresholds by convention first.

### The match wall clock

The Summary **Date** cell and the topbar date prefer KTX's own
`demoInfo.date` — the server's stamp, to the second, with its zone
printed. Where the demoinfo block does not exist (the pre-`ktxstats` half
of the archive, which used to render `-`) they fall back to
`streams.global.matchStartUnixMs`, the graded wall-clock anchor added in
schema v72.

**It is rendered in UTC and says "UTC" in the text.** The anchors are Unix
epoch milliseconds and the schema publishes no local-time field on purpose:
the stamp a demo carries was written in whatever zone the server ran in,
and on the old half it carries no zone at all (read as UTC, `assumedUtc`,
accuracy ±14 h). Rendering it in the viewer's zone would invent a precision
the anchor does not have and would move the date under a reader in
Auckland.

The grade is visible, not just hoverable. `matchStartConfidence: "exact"`
renders plainly; every other grade gets a leading **`~`**, and
`contradicted` — the grade where a hard check failed — adds
**`(disputed)`** after the stamp. Both are in the TEXT: the two hedged
grades are also coloured (`.date-unverified` muted, `.date-contradicted`
amber), but a colour alone is exactly what a screenshot in a channel and a
colour-blind reader cannot tell apart, and this is a date somebody may
write down. The anchor is never dropped or coerced on a bad grade — the
schema grades rather than withholds, and so does the UI; source,
`±accuracy` and `matchStartNote` all ride the tooltip.

**Precision follows `matchStartAccuracyMs`, by magnitude.** A stamp printed
to the minute claims a minute. The ±14 h `assumedUtc` grade puts the true
instant anywhere in a 28-hour window, so an anchor accurate to an hour or
worse prints the DAY plus its ± instead — `~2002-08-04 UTC ±14 h`, not
`~2002-08-04 11:44 UTC`. Second-scale and tighter anchors keep the minute.
`formatUtcStamp` switches on the number, like `formatAccuracyMs` beside it,
so a new rung on the accuracy ladder needs no change here.

### A mode with no teams (FFA, race — and every duel)

`match.gameMode.teamBased` (schema v75) is the pipeline's one verdict on
whether a player's team tag names a SIDE. When it is false the Go side lays
the match out with one side per player — `match.teams` one row per player,
every `players[].team` equal to the player's own name, the raw clan tag kept
on `players[].rawTeam` — which is the layout duels have always produced.

**Two questions, two predicates.** The frontend asks "is every player their
own side here?" (LAYOUT) and "was the teamplay ruleset in force?"
(SEMANTICS), and they have different answers on the same demo — a 1v1 on a
CTF server is laid out individually and genuinely was teamplay. So:

| Question | Helper | Reads | Decides |
|---|---|---|---|
| Layout | `isIndividualLayout(result)` | `match.sources.teams === 'individual'` — authoritative, no fallback: the page only ever renders the Result its bundled WASM just produced from a `.mvd` (index.html `accept`), so a Result is always at this build's schema version | Which panels exist, which palette, the scoreboard shape |
| Semantics | `isTeamBasedMode(result)` | `match.gameMode.teamBased === true` — same, no fallback | Whether a same-team quantity can exist |
| Field | `isMultiPlayerIndividual(result)` | `isIndividualLayout && !isDuel` | The one test behind the player palette, the per-player Timeline and the single-column Chat (`individual-field` on `<body>`, set by `setIndividualFieldLayout`) |
| Both | `hasTeammates(result)` | teamplay in force AND not laid out per player | The aim tab's Team victim filter |

`displayResults` puts an `individual-mode` class on `<body>` from the LAYOUT
predicate. The CSS then hides **every team surface**: the Teams panel, the
"Per Team" aggregates in all three tabs, and the scoreboard's `Team` column
(a copy of `Player`) plus `TK` / `TDmg`, which are structurally 0 when
nobody has a teammate. Region control is not driven by that class:
`initRegionControl` hides its two panels itself whenever the layout is
individual and not a duel (`isMultiPlayerIndividual`), because the result
ships `timelineAnalysis.regionControl.regions` (the region GEOMETRY, a
property of the map) for every demo — the panels are NOT hidden by a
missing-data gate, and before this they rendered an editor whose Apply
button only logged a warning. A two-participant match keeps them: it has two
sides.

**Colours in a field of players come from a different palette.**
CLAUDE.md's rule stands, including its stability property: `TEAM_COLORS[i]`
is the colour of `timelineState.teams[i]`, every surface indexes that one
array, and WHICH palette entry lands at each index is decided by NAME, never
by rank, so the colours do not move when the scoreline does.

A **duel** is unchanged from before individual modes existed: two players
are a matchup, so it keeps `assignTeamColors` and the four-entry team
palette (`red`/`blue` claim their own entries, the rest go in name sort
order). Only an individual layout with MORE than two players switches
palettes — `timelineState.teams` is then the frag-sorted PLAYER list (each
row's `team` IS its name, so every existing `teamOrder.indexOf(row.team)`
lookup resolves unchanged) and `setCanonicalTeams` fills the array via
`assignPlayerColors`, twelve `PLAYER_PALETTE` entries handed out in name
sort order. Four entries do not cover a field of eleven; assigning them by
frag RANK instead recoloured the whole board when the result changed.

The topbar names the leader and the field size (`toast 33 · 8 players`)
rather than inventing an "A vs B" matchup; a two-player individual match — a
duel, or a 1v1 FFA — still renders as "A vs B", which is what it is.

**The Timeline tab drops its two-sided views.** Team Status and the Score /
Weapons / Team Health/Armor diverging graphs are structurally two-sided: they
draw `timelineState.teams[0]` against `[1]`. On a duel that is the whole
match; on a multi-player FFA it is the top two PLAYERS and nobody else. So
when the layout is individual and not a duel —
`isMultiPlayerIndividual(result)`, i.e. `isIndividualLayout && !isDuel`,
where `isDuel` needs exactly two `playerStats` rows, so a lone player takes
this layout too; the same test `initRegionControl` and the palette apply — `setIndividualFieldLayout` puts an `individual-field` class
on `<body>`, and the tab becomes the per-player views:

- CSS hides Team Status and the three A↑/B↓ graphs, which takes their
  Team A / Team B legends with them.
- The three per-player drill-downs leave their collapsed `<details>` and
  become panels of their own, in order: **Frags / deaths per player**,
  **Weapons per player**, **Health / armor per player**. The live nodes are
  RE-PARENTED into the panel bodies, never cloned — they own the canvases and
  the `_sig` / `_cells` rebuild cache, and a second copy would render into a
  detached tree. The selected-range label rides along from the Weapons
  heading. The move reverses on the next demo load: `resetUIToCleanState`
  restores the two-sided layout, then `applyDuelModeUI` re-promotes if the new
  demo wants it, so an FFA → 4on4 → duel walk lands on the right layout each
  time.
- Rows are taller there — mini charts 44 → 60 px, weapon-span rows 20 → 28 px
  (`ppMiniHeight` / `ppSpanRowHeight`) — since these three views now carry the
  tab instead of hiding under a graph.
- `timelinePlayersByTeam` returns one group per entry in
  `timelineState.teams` instead of always two, so EVERY player gets a row.
  Before, only the players whose team matched `teams[0]` / `teams[1]` did —
  in an individual layout that is two of N. Group index is still the
  `TEAM_COLORS` index, so the rows carry the player palette exactly as
  CLAUDE.md's rule requires. A player's side is their own name there, so no
  roster lookup is involved.
- **Powerups is hidden.** Its rows are per powerup TYPE, coloured by which of
  two TEAMS holds it, so a field of N paints the top two and greys every
  other run as `other`. `updatePowerupTimeline` hides the panel itself, the
  way `initRegionControl` hides region control. Colouring those spans per
  player is separate work.

A duel (or a 1v1 FFA) is A-vs-B and keeps every panel exactly as before.

**The Chat tab collapses to one chat column.** Its three columns are Kills,
`teams[0]` chat and `teams[1]` chat — a column per side, which is a column
per player only in a duel. In a field of eight, six players' say lines were
split into no column at all and simply did not render. So on the same test —
`isMultiPlayerIndividual(result)` — the same `individual-field` body class
that drives the Timeline also:

- CSS hides `#team-b-chat-header` and `#team-b-messages`. Both remaining
  columns are `flex: 1`, so Kills and Chat split the width; nothing is moved
  or cloned, the hidden container just stays empty.
- `buildFullChat` pours EVERY say line — `say` and `say_team` alike — into
  the one column in time order. In an FFA a `say_team` reaches only the
  players sharing the speaker's clan tag, which makes it narrower than public
  chat but still chat; it keeps its own colour (`.chat-time-marker-msg
  .teamsay`), so the two read apart in the single column.
- Each row is prefixed with the speaker's name (`.chat-speaker`), coloured by
  `chatSpeakerColor` = `TEAM_COLORS[timelineState.teams.indexOf(player)]` —
  CLAUDE.md's canonical index, so a name here is the colour that player
  carries on the scoreboard, the map and the timeline; a speaker who is not
  in that list (a spectator under `sv_spectalk 1`, a mid-match rename) gets
  neutral grey rather than being dropped. With two sides the column heading
  said who was talking; with eight players nothing else does.
  The Kills column is unchanged — obituaries name their own players.
- The heading becomes plain **Chat**. `displayTimelineAnalysis`'s
  `${teams[0]} Chat` write is skipped while the class is on — it re-runs from
  `applyDeferredBuckets` long after `applyDuelModeUI`, so the body class, not
  the result, is what the render paths read.
- **"Hide team chat" stays.** It filters on message TYPE (`say_team`), not on
  side, so it keeps its meaning here.
- Reverses with the Timeline layout: `resetUIToCleanState` calls
  `setIndividualFieldLayout(false)` and restores the default titles, then
  `applyDuelModeUI` re-applies for the new demo.

The time axis, the current-time line and scroll-to-current-time are unchanged
— the axis is its own column and the line spans the scroll inner, so neither
depends on how many message columns there are.

### A recording with no match in it

`result.noMatch` (schema v74) is present exactly when `streams` is absent —
2.03% of the archive. Every stream-fed panel is empty on such a demo, and
the tab used to render headers over nothing with an "Analysis complete!"
status, indistinguishable from a bug.

`displayNoMatch()` now renders the marker's own story at the top of the
Summary tab: a reason-appropriate heading (the five `reason` values are a
total partition, so the title map is exhaustive by contract), the marker's
`detail` sentence verbatim — it is **display-only** by contract, unstable
wording that is never parsed, and every fact in it is a structured field
beside it — and those fields as an evidence grid: status at demo open
(`not sent` where the opening dump carried no `status` key at all, which is
a different reading from any value it could have had), whether the match
clock was ever seen running, the game dir, the frag-log kill count (`0` is
the reading that *defines* `noPlayRecorded`, so it always prints) and the
first usable `dateMarkers` entry, since the whole date family rides here
instead of `streams.global` on such a result.

Under that sentence sits the **plain reading**: what the finding means for
the person holding the file, and an action where one honestly exists. Only
`demoUnreadable` has one — of the 20 such demos in the archive sweep, 16
abort within the first 16 bytes on a "block size" that is plainly ASCII text
(`us\0C`, `rdem`, `le n`), 2 on a `dem_cmd` no MVD carries and 2 on an
unexpected EOF, so the file is either not an MVD or a truncated one and a
fresh download is a real fix. Nothing a reader can do makes a recording that
started mid-match contain its own start, so the other four reasons
translate and stop there. A second line rides the **gamedir** rather than
the reason ("This looks like a fortress demo; this tool analyzes KTX-style
QuakeWorld matches"), because that is where the foreign content sits: 165
of the 170 `noMatchDeclared` demos in the sweep name a non-`qw` gamedir,
`fortress` alone 148 of them, and the same mods turn up under three of the
other reasons. The pipeline's own `detail` strings are untouched — they are
the finding, and display-only by contract either way.

It is deliberately **not** styled as an error. `errors[]` means the
pipeline failed; the marker means the demo holds no match. They coincide on
exactly one reason, and there — `demoUnreadable` — the panel also prints
the reader's own message. The load status line likewise drops to the
neutral `status` class rather than the red `status error`.

The body class `no-match` hides the four Summary panels fed by `streams`
(Basic Stats, Weapon Stats, Item Pickups, Kills by Weapon); Match Info,
Match Settings and Server Info stay, because they have something to say.
The Teams box is hidden only when the frag log named no teams either — a
mid-match recording often carries a real scoreline.

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
wildcards, and matches are listed sorted by date descending in pages
of 100. A footer under the list shows "N of M matches" (M is the exact
total, from PostgREST's `count=exact` Content-Range) with a **Load
more** button that appends the next offset page until every match is
shown. Clicking a row downloads the demo and runs the normal analysis
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
edict numbers across drops, so `entNum` alone would collide. A drop with
no matching pickup is shown as `expired` only when KTX announced the
removal itself — `//ktx expire <ent>`, which reaches the frontend as the
drop row's own `fate: "expired"` (schema v72). Otherwise it is
`unobserved`: over the archive that hint accounts for barely half the
pickup-less drops, the rest being packs the recording simply ended on top
of, and the table used to call all of them expired.

On demos older than KTX 1.38 the drop side is RECONSTRUCTED rather than
hinted (`backpacks[].source == "reconstructed"`, schema v72), and there is
no `//ktx bp` for it to join to. Those rows carry their pickup side on the
drop row instead — `fate` / `picker` / `pickerTeam` / `pickupTime`, read
off the wire's backpack-entity track — and `packDropPickup()` adapts those
fields into the same row shape the join produces, so one renderer, one set
of filters and one Picker column serve both provenances.

Two things stay different on a reconstructed row, because the wire cannot
say them: the status chip is a plain `picked` rather than
`xfer`/`enemy`(`RL`/`LG`), since `hadBefore` is not derivable (KTX ORs the
weapon bit in, so a redundant grab leaves no trace); and the `Kills`
column renders `-` rather than `0`, since kill credit needs `hadBefore`
too. `unobserved` survives as the honest residual — "the wire did not
answer", which is a different fact from `expired`'s observed "nobody took
it".

An EMPTY tab on such a demo is ambiguous and must not be captioned as a
refusal: an absent `backpacks` section means either that no RL/LG pack
dropped or that the reconstruction stood down (frozen weapon state, no
frag log, a fairpacks / yawnmode / bloodfest ruleset), and the Result
shape does not distinguish the two.

Drop markers on the map overlay sit where the pack sat: `origin` is the
victim's position less the 24 units KTX drops the pack by
(`ktx/src/items.c:2703-2704`), on both provenances.

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
| drop `source == "reconstructed"`, `fate == "picked"` | `picked`     |
| drop `source == "reconstructed"`, `fate == "expired"` | `expired`    |
| drop `source == "reconstructed"`, any other `fate`    | `unobserved` |
| no matching pickup, `fate == "expired"` (the `//ktx expire` hint) | `expired`    |
| no matching pickup, no `fate`           | `unobserved` |
| same team as dropper, picker !hadBefore | `xfer`       |
| same team as dropper, picker hadBefore  | `xfer RL/LG` |
| enemy team, picker !hadBefore           | `enemy`      |
| enemy team, picker hadBefore            | `enemy RL/LG`|

The reconstructed branch is decided by `source`, not by "did a pickup row
join" — a reconstructed drop's `entNum` is a real edict now, and reading a
join failure as `expired` there would restate a fact the wire never gave.

The `Kills` column is `weaponPickups[i].kills` — frags the picker
scored with the pack's weapon before their next death (`-` on a
reconstructed row, which has no kill credit). Only
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
