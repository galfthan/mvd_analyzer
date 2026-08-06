# mvd-map-view

The QuakeWorld map renderer from [mvd-analyzer](../README.md), extracted as a
standalone component so other applications can embed it.

Zero runtime dependencies, plain ESM, no build step. It runs unchanged in a
browser, in a Web Worker, and under `node --test`.

## Status

**Extraction in progress.** The package currently owns the renderer's pure
helpers; the stateful renderer (camera, floor model, draw layers, pointer
interaction) still lives in `mvd-web/static/app.js` and moves across in
groups. Each group is verified byte-identical against a screenshot corpus
before it lands — see [Parity testing](#parity-testing).

Moved so far:

| Module | Contents |
|---|---|
| `src/geometry.js` | `normalizeMapGeometry`, `pointInTriangle`, `computeMapZRange`, `floorBoundaryEdges`, `floorBoundaryWalls`, `moverPoseAt`, `FLOOR_SLAB_DEPTH` |
| `src/util.js` | `lowerBoundIndex`, `trailIndexAtTime` |
| `src/color.js` | `hexToRgba`, `scaleRgbaAlpha`, `getLocationColor` |
| `src/locs.js` | `normalizeLocationName` (**the** canonical loc normalizer), `findNearestLocation`, `ITEM_KEYWORDS` |
| `src/regions.js` | `REGION_STATE_BY_CHAR`, `decodeRegionStateChar` |

The intended end state is a `MvdMap` class taking geometry, a static entity
corpus, and a windowed frame source, with the host application owning only
the chrome around the canvas. The design is in `plans/plan-embeddable-map.md`.

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
4on4) at several clock times in ten view states each — 3D and top-down,
trails, view/velocity arrows, LOS, PVS, learn mode, rotated and zoomed
cameras — plus one run with the geometry fetch blocked so the convex-hull
fallback path is covered. 100 shots; any `DIFF` is a real pixel change.

`mvd-web/test/mapshot.py` drives one demo and takes `--demo`, `--out`,
`--times` (match-relative **seconds** — the frontend clock is seconds, unlike
the ms-native API) and `--no-geometry`.
