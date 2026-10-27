// map-sync.js — copies the Map tab's loaded map DATA into the Player tab's
// own MvdMap (see MapPane.svelte). Data only, by reference: the view state
// (canvas, camera, follow target, trail toggles, overlays) stays each map's
// own. Fields named in mvd-map-view/src/map.js newState().

const DATA = [
    'locations', 'locTable', 'locationGroups', 'locationGroupByName',
    'controlRegions', 'regionToGroups', 'mapGeometry', 'submodelMeshes',
    'items', 'mapEntities', 'teleportArrows', 'bounds',
    'frames', 'bucketStates',
    'teams', 'playerSymbols', 'posStreams', 'movers',
    'fullTrails', 'deathEvents', 'dropEvents', 'trailStartTimes',
    'initialized',
];

// Caches the renderer derives from these, per map instance (map.js sets
// them back to null itself when its own setGeometry / setFrames runs).
const GEOMETRY_CACHES = ['_floorZCache', '_floorModel', '_floorCanvasKey', '_moverFaces', '_moverBBox'];

// syncMapData returns true when the world changed (geometry or bounds), so
// the caller refits the view.
export function syncMapData(src, dst) {
    const geometryChanged = src.mapGeometry !== dst.mapGeometry || src.locationGroups !== dst.locationGroups;
    const boundsChanged = src.bounds !== dst.bounds;
    const framesChanged = src.frames !== dst.frames;
    for (const k of DATA) dst[k] = src[k];
    if (geometryChanged) for (const k of GEOMETRY_CACHES) dst[k] = null;
    if (framesChanged) {
        dst._frameRecon = null;
        dst.lastRenderedBucket = null;
    }
    return geometryChanged || boundsChanged;
}
