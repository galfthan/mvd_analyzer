// mvd-map-view — the QuakeWorld map renderer from mvd-analyzer, as a
// standalone component.
//
// Extraction is in progress: this currently exports the pure helpers the
// renderer is built on. The stateful `MvdMap` class (camera, floor model,
// draw layers, interaction) lands as later groups move across from
// mvd-web/static/app.js. Until then mvd-web calls these directly.
//
// Everything here is dependency-free ESM and runs unchanged in a browser, in
// a Web Worker, and under `node --test`.

export {
    FLOOR_SLAB_DEPTH,
    normalizeMapGeometry,
    pointInTriangle,
    computeMapZRange,
    floorBoundaryEdges,
    floorBoundaryWalls,
    moverPoseAt,
} from './geometry.js';

export { lowerBoundIndex, trailIndexAtTime } from './util.js';

export { hexToRgba, scaleRgbaAlpha, getLocationColor } from './color.js';

export { ITEM_KEYWORDS, normalizeLocationName, findNearestLocation } from './locs.js';

export { REGION_STATE_BY_CHAR, decodeRegionStateChar } from './regions.js';
