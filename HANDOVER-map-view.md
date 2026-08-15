# Handover — map renderer extraction (`mvd-map-view`)

> **DELETE THIS FILE BEFORE MERGING TO MAIN.** It is a working handover for an
> in-progress branch, not documentation. Everything in it that is worth keeping
> permanently belongs in `mvd-map-view/README.md`, `mvd-web/README.md` or a
> commit message — if you find something here that has outlived the branch,
> move it there rather than merging this file.

Branch: `map-view-extract`. Base: `main` at `2112227`. Seven commits, each one
verified and independently revertable.

---

## What this branch is doing

Pulling the map renderer out of `mvd-web/static/app.js` (12,201 lines at the
start) into `mvd-map-view/` — a dependency-free ESM package — so it can be
mounted both by mvd-web and by an MCP Apps `ui://` viewer. No analytics, API
or schema changes; no user-visible behaviour change at all, by construction.

Full design notes live in `plans/plan-embeddable-map.md`, which is
**gitignored and therefore not on this branch** (repo convention: plans are
never committed). The decisions that must survive the move are reproduced
below so this branch is self-sufficient.

---

## Read this first: the parity harness

The renderer's canvas output is **byte-deterministic** for a given demo, clock
time and view state, on a given machine. That makes screenshot comparison an
exact test, and it is the only thing standing between this refactor and a
silent visual regression. Every commit on this branch was gated on it.

```bash
make build
mvd-web/test/capture-baseline.sh /tmp/mapshots/before   # BEFORE touching code
# ... make changes ...
make build
mvd-web/test/capture-baseline.sh /tmp/mapshots/after
mvd-web/test/compare-shots.sh /tmp/mapshots/{before,after}    # expect diff=0
```

126 shots: four demos (dm3 4on4, dm6 2on2, aerowalk 2on2, obsidian 4on4) at
two or three clock times, each in ten view states plus four driven through
real mouse input (pan drag, right-drag orbit, wheel zoom, Reset view), plus
one run with the geometry fetch blocked for the convex-hull fallback path.

Notes that cost me time:

- **Capture the baseline before editing.** If you have already edited, build a
  throwaway worktree at the last good commit and point `mapshot.py --dist` at
  its `dist/`: `git worktree add --detach /tmp/wt <sha>`, symlink `bsps/` into
  it, `make build` there. Do **not** `git checkout <ref> -- .` on a dirty tree.
- The mouse-driven shots exist because the orbit pivot and the zoom anchor are
  the only paths that invert the projection. A broken inverse is invisible in
  the state-poked shots.
- `mapshot.py --times` is in **seconds** (see the units trap below).

## Prerequisites on a fresh machine

1. `make bsps` — the LOS/PVS shots and the real floor geometry need
   `bsps/*.bsp`. Without them those shots differ from mine.
2. Demo cache: `go test ./mvd-analytics/analyzer/... -run TestGoldenCorpus`
   fetches the corpus into `mvd-analytics/testdata/cache/` (gitignored). The
   harness reads four demos from there: 212260, 211805, 212535, 212483.
3. `node` (18+) for `node --test`, and Python `playwright` + chromium for the
   harness. Both were already provisioned on the machine I used.
4. Baselines are **machine-local**. Do not compare PNGs across machines; only
   before/after pairs captured on the same box mean anything.

---

## Done

| Commit | What moved |
|---|---|
| `facb3f7` | pure helpers — geometry normalisation, boundary extrusion, loc naming, colours, binary search. Plus: the package, the module bootstrap, the harness, the first JS tests |
| `e961aaa` | the orbit camera — projection, inverse, fit, angle clamp, orbit pivot |
| `2ef536a` | loc regions + floor model |
| `9c98a1a` | the 13 canvas draw primitives |
| `06a85d3` | `MvdMap` container (state bag + camera); loader invariant pinned by test |
| `402e243` | world layers → `MvdMap.drawWorld` (floors, liquids, movers, projectiles/beams, outlines, labels) |
| `16fdedf` | actor layers → the z-sorted item/player pass, badges, stems, arrows, item phase clock, entity view |
| `17ffadb` | merge of main. Semantic fix the textual merge missed: TEAM_COLORS is now a per-match permutation (assignTeamColors), so setCanonicalTeams re-points mapView.teamColors — without that the canvas painted the unpermuted palette while the DOM showed the permuted one |
| (2b)      | batch 2b — trails, LOS/PVS lines + buildVisByPair/losCovers, occupied-region + region-control overlays, resolvePlayerLoc, pickLocGroupAt, hitTestPlayerSymbol (takes the frame's player map as an argument — the frame source is still host-side). Harness: the LOS/PVS states now wait out the lazy worker raycast (`!mapState.losPending`), the only nondeterminism ever caught in the shots |
| (3)       | `MvdMap.render(time, bucket, controlStates)` — the whole frame composition; app.js renderMap is a thin wrapper resolving the two host-side time-indexed inputs. `resize(cssW, cssH, dpr)` / `refit()` as the push-only size API — all measuring (fullscreen, DPR) stays in app.js. `rebuildLocationGroups()` replaces the app-side wrapper |
| (4)       | the frames seam — the columnar accessors move to `src/frames.js` (one implementation; app.js keeps wrapper names for the panels), the view is pushed in via `setFrames`, and `frameAt(time)` / `regionControlAt(time)` own the memoised lookup. `render(time)` and `hitTestPlayerSymbol(cx, cy, time)` lose their host-data parameters. NOTE: this is the synchronous half of the planned FrameSource — see "Not done" |

State: `app.js` ~9,900 lines. `mvd-map-view/` ~2,600 lines of source,
94 unit tests. `make test` green; full-shot parity on every commit (the set is
140 shots since the LOS-wait fix).

Current boundary:

```
mvd-map-view/   camera · geometry · loc regions · floor model · draw primitives
                · MvdMap state container · world layers · actor layers
                · trails · LOS/PVS lines · occupancy + region-control overlays
                · loc resolution · hit-testing
                · render(time, bucket, controlStates) · resize/refit push API
app.js          frame source (bucket reconstruction + region-control lookup)
                · precomputeFullTrails · pointer interaction · all measuring
                · all DOM chrome · all loaders
```

## Not done

In the order I would do it:

1. **The async half of `FrameSource`** — the original plan called for
   `{duration, coarse(), window(fromMs,toMs), markers(fromMs,toMs)}`, async
   from the start. What landed is the synchronous seam: `setFrames(view)` +
   `frameAt(time)`, with "no frames yet" as a first-class state (which is the
   partially-loaded-timeline behaviour the async design wanted pinned).
   Every frame lookup now goes through `frameAt`, so window-granular fetching
   is a change to ONE method plus an adapter that calls `setFrames` as
   windows arrive — not a render-path retrofit. Deliberately deferred until
   the MCP viewer exists to drive the window/marker semantics ("two
   consumers" rule: don't shape the API on a consumer that isn't asking yet).
2. **Pointer interaction** (`installMapInteraction`) as component API with
   host-supplied event wiring.
3. **Public surface**: setters (`setGeometry`/`setEntities`/`setLocTable`/
   `setLocations`/`setFrames`/`setMarkers`), `seek(ms)`/`play()`/`pause()`,
   `follow()`, `setOverlays()`, and `on('select'|'camera'|'hoverloc')`.
4. **Then** the two follow-on parts, in this order: WebGL backend
   (perf/effects, see below), then the MCP Apps viewer in `mvd-mcp`.

---

## Decisions that must survive

- **Two consumers, and only two: mvd-web and the MCP Apps viewer.** A
  standalone page, a third-party REST consumer, npm publication and a
  `<mvd-map>` element are speculative and must not shape the API until
  someone asks. (`package.json` is `private: true`, license field is a
  placeholder — settle that before any publication.)
- **The loader invariant.** The package never loads anything: no `fetch`,
  XHR, dynamic `import()`, ambient `window`/`document`/`location`, no
  webfonts. A test greps every module for these. It is a source-level lint,
  so it is defeatable by anything indirect — it exists to catch the accident
  of dragging a `document.getElementById` along with a moved layer. The rule
  is about *ambient* globals, not the DOM: `canvas.ownerDocument.createElement`
  is correct and is how the world bake gets its offscreen surface.
- **Canvas-2D stays for now.** Do not fold a WebGL rewrite into the
  extraction; parity becomes unprovable. WebGL is a backend swap behind
  `src/draw.js`, which is why every primitive there takes its context,
  geometry and projection explicitly and reads nothing from module scope.
- **mvd-web has no bundler and `app.js` must stay a classic script**
  (`index.html` has inline handlers calling its globals, e.g.
  `onclick="loadFromHub()"`). The package is ESM; `index.html` bridges with a
  module bootstrap that sets `window.MapView` and calls
  `window.onMapViewReady(MapView)`, which `app.js` defines and uses to
  allocate the container. Module scripts are deferred, so that hook runs after
  `app.js`'s top level and before `DOMContentLoaded`. **The MCP bundle is the
  one place that will need a bundling step** (a `ui://` resource is a single
  self-contained blob, so relative imports cannot resolve) — esbuild or a
  small concatenator; the module graph is 8 files and acyclic.
- **Team colours are a constructor option** (`opts.teamColors`). The canonical
  frag-sorted team ordering stays in mvd-web (see the rule in `CLAUDE.md`);
  the component only takes the palette it indexes.

## Traps found the hard way

- **The frontend clock is in SECONDS.** `mapState.currentTime`,
  `timelineState.duration`, `DEATH_X_DURATION` — all seconds, unlike the
  ms-native API and the item phase fields (int32 ms), which is why the item
  clock promotes before comparing. When the public API lands it should take
  **ms** and convert at exactly one boundary. My first baseline capture was
  silently taken past match end because of this.
- **Two inputs arrive after `displayResults()`**: the BSP geometry on its own
  fetch, and the 50 ms bucket view on the worker's deferred `buckets` message
  (`applyDeferredBuckets`). The harness waits for both; without the second the
  map draws a world with no players in it and the shots look plausible.
- **A moved read needs a moved write.** Batch 2a switched the item source to
  `state.items` and did not populate it: every item marker vanished from the
  map while everything else drew fine. 103/126 shots differed; `node --check`,
  `make test` and the browser console were all clean. Check what sets a field,
  not just what reads it.
- When bulk-extracting `const` blocks, scan with balanced brackets and strip
  trailing comments before testing for `;`. Scanning for a line ending in `;`
  runs into the next function on any line that ends in a comment.
- `plans/` is gitignored; do not expect the plan file on this branch.

## WebGL, if you get there

Measured, not assumed: the static world is already baked to an offscreen
canvas, so fixed-camera playback is cheap; the bake key includes
`yaw/pitch/zoomK/panX/panY`, so **every camera-motion frame rebakes** —
reallocating the cache backing store, re-sorting on angle change, and doing
`fill()` + `stroke()` per colour batch (the stroke is the AA seam hack in
`renderSolidEntries`, roughly doubling rasterization). Corpus scale: dm3
1,470 triangles, median map 1,819, p90 4,839, worst (maphub_v2) 24,510.

Do the cheap 2D wins first — reuse the cache canvas instead of reallocating,
and replace the seam stroke with a triangle inset or one pre-merged path per
colour. If rotation is smooth after that, WebGL becomes an effects decision
rather than a perf one.

---

> **Reminder: delete this file before merging to main.**
