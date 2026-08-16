# mvd-map-view

The QuakeWorld map renderer from [mvd-analyzer](../README.md), extracted as a
standalone component so other applications can embed it.

Zero runtime dependencies, plain ESM, no build step. It runs unchanged in a
browser, in a Web Worker, and under `node --test`.

## Who this is for

Two consumers, both real:

1. **mvd-web** — the analyzer UI, which owns a WASM pipeline in-page and a
   large amount of DOM chrome around the canvas.
2. **The MCP Apps viewer** — a `ui://` resource rendered in a sandboxed
   iframe inside a chat host, whose data arrives over postMessage and whose
   size is dictated by the host.

Anything else (a standalone page drawing a map, a third-party app on the REST
API, npm publication, a `<mvd-map>` element) is speculative and must not
drive design decisions until someone actually asks for it.

## The loader invariant

**This package never loads anything.** No `fetch`, no `XMLHttpRequest`, no
dynamic `import()`, no ambient `window` / `document` / `location`, no
webfonts — the only font families used are `monospace` and `sans-serif`.
Every byte arrives through a setter.

The rule is about *ambient* globals, not the DOM as such: the canvas the host
hands over is fair game, including reaching through it. The world bake needs
an offscreen surface and gets it from `canvas.ownerDocument.createElement`,
which works whatever document the canvas belongs to — where a bare
`document.createElement` would assume the renderer and its canvas live in the
same global, and that is exactly the assumption an iframe breaks.

This is not stylistic. The MCP viewer runs behind a strict CSP with no
network origins at all: its geometry arrives over `resources/read` and its
player samples over `tools/call`, as a `FrameSource`. A single `fetch` inside
the renderer would make it unusable there, and the failure would only show up
in a chat host, not in `make test`.

Two consequences for anyone moving code in here:

- The geometry fetch in `app.js` (`initMapView`) stays host-side when the
  rest of that function moves.
- The component takes its canvas **and its size** explicitly. It must never
  measure a container or listen for window resizes — the MCP host controls
  the iframe's dimensions and reports changes via `ui/notifications/size-changed`,
  so sizing is always something the host pushes in.

## Status

**The extraction is complete and the renderer is pure WebGL2.** `MvdMap`
owns the camera, the frame feed, every scene layer, hit testing and the
pointer gesture semantics; mvd-web owns only loaders, measuring and DOM
chrome. The 2D scene painter is gone — see `src/glworld.js`.

Modules:

| Module | Contents |
|---|---|
| `src/camera.js` | the orbit camera — `newCamera`, `project`, `toView`, `toWorld`, `fit`, `setAngles`, `setOrbitCenter`, `is3D`, `refreshTrig`, and the pitch/yaw limits |
| `src/geometry.js` | `normalizeMapGeometry`, `pointInTriangle`, `computeMapZRange`, `floorBoundaryEdges`, `floorBoundaryWalls`, `moverPoseAt`, `FLOOR_SLAB_DEPTH` |
| `src/locgroups.js` | loc regions and the floor model — `processLocationGroups`, `computeRegionOutline`, `groupWorldBBox`, `computeRegionStacking`, `buildFloorModel` |
| `src/map.js` | `MvdMap` — the state container, camera control, the frame feed (`setFrames`/`frameAt`), `render(time)` (which builds the per-frame GL command data: world, movers, overlays, trails, sightlines, actors, labels), the push-only `resize` API, the pointer state machine, focus/follow/reset, loc resolution and hit testing |
| `src/draw.js` | canvas-2D **icon rasterisers** for host chrome (the DOM player-symbol badges) — not a scene layer |
| `src/frames.js` | the columnar bucket-view accessors — `bucketTimeSec`, `bucketIndexAtTime`, `playerValAt`, `reconstructBucketPlayers`, `teamSnapshot` and friends. One implementation shared by the map (via `setFrames`/`frameAt`) and the host's timeline panels |
| `src/glworld.js` | **the renderer.** WebGL2 only: opaque depth-buffered floors and movers (no painter sort), blended liquids/focus-fade, region tints and outlines (cached per-group VBOs restyled by uniforms), shader-dashed quad-lines (trails, sightlines, beams, stems), shaped point sprites (dots, markers, symbols, item squares), textured label billboards, screen-space arrowhead triangles. A lost or unavailable context shows a "WebGL2 required" notice — there is no 2D scene path |
| `src/glatlas.js` | the label atlas — whole strings baked through an offscreen 2D context (used strictly as a texture rasteriser), shelf-packed into one 1024px page |
| `src/util.js` | `lowerBoundIndex`, `trailIndexAtTime` |
| `src/color.js` | `hexToRgba`, `scaleRgbaAlpha`, `getLocationColor` |
| `src/locs.js` | `normalizeLocationName` (**the** canonical loc normalizer), `findNearestLocation`, `ITEM_KEYWORDS` |
| `src/regions.js` | `REGION_STATE_BY_CHAR`, `decodeRegionStateChar` |

The frame composition is `MvdMap.render(time)`: the host pushes the columnar
bucket view in via `setFrames`, and `frameAt(time)` / `regionControlAt(time)`
own the memoised per-frame lookups. Rendering before frames arrive draws the
world with nobody on it — the partially-loaded-timeline state is first-class.
Size arrives only through `resize(cssW, cssH, dpr)` — the host measures
whatever it wants (container, fullscreen element, iframe) and pushes the
result in.

Still in `app.js`: all measuring, all the DOM chrome, all loaders, and the
DOM event glue that forwards pointer events into the component.

A note on data: the renderer reads item spawners from its own
`state.items`, not from an analyzer Result — it has no notion of one, and in
the MCP viewer there is no Result object to reach for. The host sets it.
Every field the actor layer needs works this way; that is the whole point of
the state container.

The intended end state is a `MvdMap` class taking geometry, a static entity
corpus, and a windowed frame source, with the host application owning only
the chrome around the canvas. The design is in `plans/plan-embeddable-map.md`.

`src/draw.js` is no longer a scene layer: the whole scene renders through
`src/glworld.js`, and what remains in draw.js are the icon rasterisers the
HOST chrome uses to bake little player-symbol canvases for DOM panels —
the same "2D context as texture baker" category as the label atlas.

## Use

```js
import { normalizeLocationName, moverPoseAt } from 'mvd-map-view';
```

mvd-web has no bundler — `app.js` is a classic script, because `index.html`
carries inline handlers that call its globals. It therefore loads this
package through a small module bootstrap in `index.html` that publishes the
namespace as `window.MapView`, and `make build` copies `src/*.js` into
`dist/map-view/`. Module scripts are deferred, so the bootstrap runs after
`app.js`'s top level but before `DOMContentLoaded`; every `MapView.*` call
site is inside a function that runs later still.

## Tests

```bash
node --test mvd-map-view/test/*.test.js      # also run by `make test`
```

## Parity testing

The renderer's canvas output is byte-deterministic for a given demo, clock
time and view state, which makes screenshot comparison an exact test rather
than a fuzzy one. Before moving code:

```bash
make build
mvd-web/test/capture-baseline.sh /tmp/mapshots/baseline
```

after moving it:

```bash
make build
mvd-web/test/capture-baseline.sh /tmp/mapshots/after
mvd-web/test/compare-shots.sh /tmp/mapshots/baseline /tmp/mapshots/after
```

The capture covers four demos (dm3 4on4, dm6 2on2, aerowalk 2on2, obsidian
4on4) at several clock times. Each gets ten view states — 3D and top-down,
trails, view/velocity arrows, LOS, PVS, learn mode, rotated and zoomed
cameras — plus four driven through real mouse input: pan drag, right-drag
orbit, wheel zoom and Reset view. Those four matter because the orbit pivot
and the zoom anchor are the only things that invert the projection, so a
broken inverse shows up there and nowhere else. One run has the geometry
fetch blocked, covering the convex-hull fallback. Any `DIFF` is a real
pixel change.

`mvd-web/test/mapshot.py` drives one demo and takes `--demo`, `--out`,
`--times` (match-relative **seconds** — the frontend clock is seconds, unlike
the ms-native API), `--no-geometry` and `--no-webgl`.

One backend, one comparison rule: shots are byte-comparable against a
baseline captured on the same machine + driver (SwiftShader on a GPU-less
box renders deterministically too). Never compare across machines or after
a driver update.
