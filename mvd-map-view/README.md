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

**Extraction in progress.** The package currently owns the renderer's pure
helpers; the stateful renderer (camera, floor model, draw layers, pointer
interaction) still lives in `mvd-web/static/app.js` and moves across in
groups. Each group is verified byte-identical against a screenshot corpus
before it lands — see [Parity testing](#parity-testing).

Moved so far:

| Module | Contents |
|---|---|
| `src/camera.js` | the orbit camera — `newCamera`, `project`, `toView`, `toWorld`, `fit`, `setAngles`, `setOrbitCenter`, `is3D`, `refreshTrig`, and the pitch/yaw limits |
| `src/geometry.js` | `normalizeMapGeometry`, `pointInTriangle`, `computeMapZRange`, `floorBoundaryEdges`, `floorBoundaryWalls`, `moverPoseAt`, `FLOOR_SLAB_DEPTH` |
| `src/locgroups.js` | loc regions and the floor model — `processLocationGroups`, `computeRegionOutline`, `groupWorldBBox`, `computeRegionStacking`, `buildFloorModel` |
| `src/map.js` | `MvdMap` — the state container, camera, projection helpers, region focus, the floor-model cache, movers, weapon-fire overlays and the world layer (`drawWorld`) |
| `src/draw.js` | the canvas-2D primitives — `drawTriangleListFill`, `renderSolidEntries`, `drawMoverMesh`, `drawLiquidVolume`, `drawRegionOutline`, `fillRegion`, the player symbol, badges, death markers and arrows |
| `src/util.js` | `lowerBoundIndex`, `trailIndexAtTime` |
| `src/color.js` | `hexToRgba`, `scaleRgbaAlpha`, `getLocationColor` |
| `src/locs.js` | `normalizeLocationName` (**the** canonical loc normalizer), `findNearestLocation`, `ITEM_KEYWORDS` |
| `src/regions.js` | `REGION_STATE_BY_CHAR`, `decodeRegionStateChar` |

Still in `app.js`: the per-frame composition (`renderMap`), player / item /
entity / trail drawing, the LOS and region overlays, pointer interaction, and
all the DOM chrome and loaders.

The intended end state is a `MvdMap` class taking geometry, a static entity
corpus, and a windowed frame source, with the host application owning only
the chrome around the canvas. The design is in `plans/plan-embeddable-map.md`.

Everything in `src/draw.js` takes its context, geometry and projection
explicitly — no renderer state is read from module scope. That is what makes
it the layer a WebGL backend replaces wholesale.

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
node --test mvd-map-view/test/      # also run by `make test`
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
the ms-native API) and `--no-geometry`.
