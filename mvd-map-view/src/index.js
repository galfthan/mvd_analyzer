// mvd-map-view — the QuakeWorld map renderer from mvd-analyzer, as a
// standalone component.
//
// Extraction is in progress. `MvdMap` owns the renderer state and the camera;
// the per-frame composition and the pointer interaction still live in
// mvd-web/static/app.js and move across group by group, verified against a
// screenshot corpus at each step. The helpers below are exported directly
// because app.js still calls them while that is true.
//
// Everything here is dependency-free ESM and runs unchanged in a browser, in
// a Web Worker, and under `node --test`. It loads nothing — see the loader
// invariant in the README.

export {
    FLOOR_SLAB_DEPTH,
    normalizeMapGeometry,
    pointInTriangle,
    computeMapZRange,
    floorBoundaryEdges,
    floorBoundaryWalls,
    moverPoseAt,
} from './geometry.js';

export {
    MvdMap,
    newState,
    DEATH_X_DURATION,
    ITEM_MARKER_STYLES,
    LEARN_ITEM_STYLES,
    ITEM_KIND_CATEGORY,
    buildVisByPair,
    losCovers,
} from './map.js';

export {
    PITCH_MAX,
    PITCH_MIN,
    YAW_SNAP,
    DEFAULT_PITCH,
    DEFAULT_YAW,
    newCamera,
    is3D,
    refreshTrig,
    setAngles,
    fit,
    project,
    toView,
    toWorld,
    setOrbitCenter,
} from './camera.js';

export {
    processLocationGroups,
    computeRegionOutline,
    groupWorldBBox,
    computeRegionStacking,
    buildFloorModel,
    REGION_STACK_Z_EPS,
    REGION_STACK_OVERLAP_FRAC,
    BACKDROP_FLOOR_RGB,
    FLOOR_BOX_SIDE_RGB,
    FLOOR_TOP_ALPHA,
} from './locgroups.js';

export {
    PLAYER_SYMBOL_BASE_SIZE,
    ARROWHEAD_PX,
    drawPlayerSymbolAt,
    drawBadge,
    drawBadgesAroundCenter,
} from './draw.js';

export { lowerBoundIndex, trailIndexAtTime } from './util.js';

export {
    GlWorld,
    parseColor,
    makeWorldTransform,
    entryDepth,
    buildEntryVertices,
    sortedIndices,
    buildLiquidFaces,
} from './glworld.js';

export {
    bucketTimeSec,
    bucketIndexAtTime,
    bucketIndexAtOrAfter,
    playerValAt,
    playerAliveAt,
    reconstructBucketPlayers,
    teamSnapshot,
    reconstructBucketTeams,
} from './frames.js';

export { hexToRgba, scaleRgbaAlpha, getLocationColor } from './color.js';

export { ITEM_KEYWORDS, normalizeLocationName, findNearestLocation } from './locs.js';

export { REGION_STATE_BY_CHAR, decodeRegionStateChar } from './regions.js';

export {
    BENCH_DURATION_S,
    benchCameraAt,
    percentile,
    summarizeBench,
} from './bench.js';
