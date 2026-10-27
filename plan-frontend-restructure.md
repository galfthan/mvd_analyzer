# Plan — mvd-web on Svelte, behind a data layer

Status: phases A–D done (2026-10-27), E in progress. C covers the
clock, teams and current-demo stores; pov and URL state come with the
components that use them (E). D renders the top bar, sidebar and layout
in Svelte with the active tab in a store; the load overlay and search
stay legacy until their tabs are ported.
Branch: to be created off `player-tab`. The Player tab does not merge
until at least phases A–E below are done.

## Why

`mvd-web/static/app.js` is ~12.4k lines and ~400 top-level functions,
sharing global state that every feature changes in place (`currentResult`,
`mapState`, `timelineState`, `playerState`, `playlistState`, …), with the
DOM updated by hand. Every new feature adds direct links between the others:
`setCurrentTime` calls into the panes, the panes borrow the Map tab's
canvas, the tab switch mounts/unmounts panes. Movable / collapsible /
resizable panes and side panels are where that stops working.

What already works and is kept as-is: `mvd-map-view`, `mvd-fte-player`
and `static/playlist.js` — small framework-free ESM modules with their
own node tests. The new code wraps them; it does not rewrite them.

## Goals and non-goals

Goals:

1. A **data layer**: the page asks one source for demo data and never
   touches the WASM worker or the raw Result directly.
2. A **Svelte 5 + Vite** app shell, with tabs moved over one at a time
   while the rest of `app.js` keeps running.
3. The **Player tab** (tab, POV bar, panes, playlist, rerouted hub links)
   ported first, since it is the part that needs the new structure.

Non-goals (for this plan):

- **Switching the data source to mvd-api** — a separate later step. The
  data layer is shaped so it is a second implementation, nothing more.
- **Leaving Netlify.** The site stays a static Netlify deploy.
- **Redesign.** Tabs are ported with the same look and behaviour; visual
  changes go in their own commits after a tab is ported. The Player tab's
  panes are the exception, since their layout is what we are changing.

## Tooling

- `mvd-web/package.json` + `package-lock.json` (committed), Vite,
  `@sveltejs/vite-plugin-svelte`, `svelte`, `svelte-check`. Plain JS with
  JSDoc types, no TypeScript, to match the rest of the repo.
- Source in `mvd-web/src/`; files served as-is (worker.js, wasm_exec.js,
  maps/, vendor/) move to `mvd-web/public/`.
- `mvd-map-view` and `mvd-fte-player` are imported by relative path (Vite
  bundles them). `fte-player/player.html` stays a separate page at the
  same URL — the iframe needs it — as a second Vite entry or copied
  through `public/`.
- **dist/ layout stays the same** for everything fetched at runtime by URL:
  `analyzer.wasm`, `worker.js`, `bsps/`, `locs/`, `mapents/`, `maps/`,
  `fte-player/`. Vite's hashed bundles go to `dist/assets/`.
- `make build`: Go WASM build as now, then `npm ci && npm run build`
  into `dist/`, then the corpus copies. The `<meta name="build">` stamp
  and the wasm hash check (`checkBuildCoherence`) carry over.
- Netlify: the build image has node; pin it (`NODE_VERSION` in
  netlify.toml or `.nvmrc`). Cache headers stay `no-cache` everywhere
  (Netlify merges overlapping header rules, so an `immutable` rule for the
  hashed `dist/assets/*` would combine with `/*`'s `no-cache`).
- `make test`: node becomes required for mvd-web (today it is skipped
  without node). Adds `svelte-check`. Pure modules (data layer, stores,
  playlist) keep `node --test`.

## Data layer (`mvd-web/src/data/`)

One interface, two implementations over time:

```
DemoSource
  load(input)  → DemoHandle        input: { bytes, name } | { hubGameId }
DemoHandle
  ref                               { key, gameId, title, demoOffset } — the
                                    same identity playlist.js uses ('hub:N' / 'file:name')
  section(name) → Promise<…>        a Result section: 'match', 'demoInfo',
                                    'streams', 'timelineAnalysis', 'aim', …
  ready(name)   → Promise<void>     resolves when a deferred section has arrived
                                    (bucketView, regionControl.bucketStates)
  view(fn, opts) → Promise<…>       view queries: topWindows, topKills,
                                    stateAt, streamSlice, … (today's viewQueryInWorker)
  recomputeRegions(def), lineOfSight()
  demoBlob                          the demo itself, for the player iframe
```

- **`WasmSource`** (this plan): wraps today's worker protocol
  (`analyzeInWorker`, the deferred `buckets` message,
  `recomputeInWorker`, `computeLosInWorker`, `viewQueryInWorker`) and the
  hub client (supabase lookup, demo fetch with mirror fallback). It holds
  the whole Result and answers `section()` from it.
- **`ApiSource`** (later step): the same calls mapped to mvd-api
  endpoints. Names in `section()` / `view()` follow the mvd-analytics
  `view` package and the API paths, so the mapping is 1:1 where it can be
  (see the audit below).
- **Deferred data** becomes `ready('buckets')` instead of
  `applyDeferredBuckets` re-running every initialiser and re-clicking
  the active tab.
- Rule: components and stores get data only through the handle. During
  the transition, a legacy bridge exposes the handle to `app.js` and keeps
  `currentResult` filled for code not yet ported.

## Shared state (`mvd-web/src/state/`, Svelte runes modules)

| Store | Holds | Replaces |
|---|---|---|
| `demo` | current `DemoHandle`, load status/progress, errors | `currentResult`, load overlay state |
| `clock` | match time, playing, speed, who drives it (engine / timeline) | `setCurrentTime`, `mapState.currentTime`, the playback engine |
| `teams` | canonical team order + colours (`assignTeamColors` / `assignPlayerColors`) | `TEAM_COLORS`, `setCanonicalTeams`, `getTeamOrder` |
| `pov` | tracked player | `playerState` track + map follow target |
| `playlist` | clips, active run (logic stays in `playlist.js`) | `playlistState` |
| `layout` | pane set/order/sizes, collapsed panels (localStorage) | `mvd.playerPanes.v1` code |
| `urlState` | `?gameId=`, `tab`, time, playlist fragment | URL State Sharing section |

- **The clock is the hot path** (~60 Hz from the engine). Canvases (map,
  timeline graphs, panes) subscribe with a plain callback and draw in rAF;
  only coarse things (current clip, labels) use reactive state. Large
  tables never re-render on clock ticks.
- **Team colours** keep the CLAUDE.md rule unchanged, but move to one
  module; CLAUDE.md is updated to point there. The legacy `TEAM_COLORS`
  becomes a view of the store until the last legacy reader is gone.

## Shell and panes

- `App.svelte`: sidebar, tab routing (`?tab=`), load overlay, search,
  file drop, version tag.
- **Legacy tabs**: a `LegacyTab` host component that owns the tab's
  existing markup and calls `app.js`'s init/render for it, so unported tabs
  work unchanged inside the new shell.
- **Panes**: use **dockview-core** (framework-free core; Svelte components
  are mounted into its panels) for resizable, movable, collapsible panes
  and tabbed groups — rather than growing our own column. A short spike
  first checks bundle size, dark-theme styling and touch behaviour; if it
  doesn't fit, a small Svelte pane column (what the PoC does) is the
  fallback.
- The same layout primitives back the "collapsible side panel" wanted on
  other tabs (Map items panel, Timeline drill-downs).

## Player tab port (required before merging `player-tab`)

Components: `PlayerFrame` (wraps `FteController`, iframe lifecycle,
end-of-demo restart, sound), `PovBar`, `ClipBar` (In/Out/Add, `i`/`o`/`c`),
`Playlist` (list, edit, reorder, generate, run, hub/share links),
and panes `MapPane`, `HealthArmorPane`, `MomentsPane`, `FragFeedPane`.

- **Map pane gets its own `MvdMap` instance** instead of borrowing the
  Map tab's canvas — the PoC's shared camera / follow target goes away.
  This needs the map's data (geometry, streams, items, locs) routed
  through the data layer rather than pushed by `initMapView`, which is
  the main piece of work in this phase.
- **Rerouted hub links** become one shared click handler (Svelte action
  or document-level, as now) that talks to the `playlist` and `clock`
  stores, so legacy tabs' links keep working.
- Behaviour is the PoC's (mvd-web/README.md#player-tab); the open
  questions in `plan-player-tab.md` can be settled during the port.

## Phases

| | Phase | Done when |
|---|---|---|
| A | Tooling: Vite + Svelte build, `app.js` loaded unchanged, Netlify green | deploy preview behaves exactly like today |
| B | Data layer + `WasmSource`; `app.js` loads demos through it (legacy bridge) | no direct worker/hub calls left in `app.js` |
| C | Stores: clock, teams, pov, urlState; legacy code reads/writes through them | `setCurrentTime` / `TEAM_COLORS` are thin wrappers |
| D | Svelte shell (sidebar, tabs, overlay, search) with `LegacyTab` hosts | all tabs work inside the new shell |
| E | Player tab in Svelte, panes on dockview, own Map pane instance | Player tab section deleted from `app.js` → **player-tab can merge** |
| F | Remaining tabs, smallest/least coupled first: Chat, Pack drops, Pickups, Key moments, Summary, Aim, Locs & regions, Timeline, Map | each port deletes its `app.js` section; done when `app.js` is gone |

Not everything in `app.js` becomes a component. The parts without a DOM
(playerStats accessors, bucket-view accessors, the data-preparation
sections, the shared matrix model, frag precompute, team helpers) move to
plain ESM modules under `src/lib/` with node tests, the way `playlist.js`
already did, when the first ported tab needs them.

Rough size of what F moves (current line counts): Summary + tables
~2.5k, Timeline + graphs ~2.4k, Map ~3.5k, Locs & regions ~0.9k, Aim
~0.9k, Pickups ~1.1k, Chat ~0.4k, the rest smaller.

## Testing

- `node --test` for the data layer (against a fake source and the golden
  Result in `mvd-analytics/testdata/golden/`), stores and `playlist.js`.
- `svelte-check` in `make test`.
- Headless Playwright (the PoC setup) as a smoke test per tab: load hub
  game 212260 and a local file, open each tab, no console errors, before/after
  screenshots compared with `mvd-web/test/compare-shots.sh`.
- Real-GPU check by the owner at the end of A, E and each F tab.

## Docs to update as phases land

`CLAUDE.md` (mvd-web layer description, team-colour location, node now
required for `make test`), `mvd-web/README.md` (build, layout, data
layer), top-level `README.md` (repo layout), `Makefile` help,
`RELEASE_NOTES.md` (user-visible changes only — the port itself should
have none outside the Player tab).

## Risks

- **Two systems at once** during D–F: the legacy bridge must be the only
  place where new code and `app.js` touch, or the transition gets the same
  tangle as today.
- **Netlify build** now depends on npm: lockfile committed, node version
  pinned, `npm ci` only.
- **Clock performance**: a reactive 60 Hz value fanning out to large
  components would regress playback; canvases subscribe directly.
- **Scope creep**: ports are not redesigns (see non-goals).

## For the later API step: what the page reads (first pass)

Grep-level, not complete — a full audit is the first task of the API
step. Top-level Result sections `app.js` reads, and the mvd-api endpoint
that likely covers each (`mvd-api/openapi/openapi.yaml`):

| Result section (fields seen) | Likely endpoint | Notes |
|---|---|---|
| `match` (teams, players, map, duration, gameMode, sources), `noMatch` | `/overview` | check `noMatch` / `sources` are there |
| `demoInfo` (players, teams, map, date) | `/demoinfo` | |
| `metadata` (serverInfo, matchSettings) | `/metadata` | |
| `playerStats` (players, teams) | `/player-stats` | |
| `frags` (frags, byWeapon) | `/frags` | |
| `damage` (byWeapon, source, coverage) | `/damage` | |
| `aim` (players, hitsMeasured, hitsSource) | `/aim` | |
| `items`, `weaponPickups`, `backpacks` | `/items`, `/weapon-pickups`, `/backpacks` | |
| `messages` | `/chat` | |
| `highlights` | `/highlights` | |
| `locGraph`, loc tables | `/loc-graph`, `/loc-table`, `/loc-trails` | |
| `timelineAnalysis.bucketView` | `/buckets` | |
| `timelineAnalysis.regionControl` (+ bucketStates, stats) | `/region-control` | custom-region recompute needs a POST — **gap?** |
| `timelineAnalysis` powerupEvents, fragStreaks, demoMarkers, locationData, playerUserIDs | `/events`? | **gap?** — no obvious single endpoint |
| `streams.players`, `streams.movers`, `streams.global` | `/stream-slice`, `/state-at` | map playback reads every frame — **payload/latency to measure** |
| `streams.projectiles` / `beams` / `nails` | `/streams/*` | |
| `mapEntities` | — | MCP has a by-map lookup; **gap?** for per-demo |
| line of sight | `/los` | |
| top windows / top kills | `/top-windows`, `/top-kills` | already view queries today |
| demo bytes for the player | hub / demo source URL | local files would need upload (`/v1/demos` POST) |

## Open questions

- PR shape: one PR with the Player tab plus phases A–E, then one PR per
  tab in F? Or land A–D first on its own, with the Player tab after?
- dockview vs our own pane column — decide after the spike.
- Port all tabs before merging `player-tab`, or only through E?
