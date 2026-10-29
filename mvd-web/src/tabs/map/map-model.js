// map-model.js — the demo's map data, held in one headless MvdMap
// (mvd-map-view) for the page: it is never drawn. Every map pane of the
// Watch tab copies its DATA into its own MvdMap each frame
// (src/player/panes/map-sync.js); the panes that list players, items,
// regions and entities read it, and its lookups (frameAt, itemStatus,
// resolvePlayerLoc, regionControlAt) answer for them. Learn mode and the
// entity filters live here too.
//
// The load's displayResults (src/app/load.js) loads a demo into it (`loadMap`, again when the
// deferred 50 ms buckets land — the player positions, trails and region
// states come from them) after `resetMap`; the region set in effect
// (src/state/regions.js) and the team colours (src/state/teams.js) are read
// from their stores. Loading is data only: nothing is drawn here.
//
// The pure parts (symbols, bounds, drops) are src/lib/map.js.

import {
    MvdMap, DEFAULT_YAW, DEFAULT_PITCH, refreshTrig, computeMapZRange, computeRegionStacking, buildVisByPair,
} from '../../../../mvd-map-view/src/index.js';
import { store } from '../../state/store.js';
import { clock, demo, teams, regionControl, reapplyRegions } from '../../state/index.js';
import { mapFileKey } from '../../lib/result.js';
import { locToRegionOf, regionToGroupsOf, regionSides } from '../../lib/regions.js';
import { mapTeams, assignPlayerSymbols, mapBounds, dropEventsOf, posStreamsOf } from '../../lib/map.js';

export const view = new MvdMap();

// The demo whose data is loaded ({ result }), null after a reset: the tab
// rebuilds its legend, items list and controls from it.
export const mapData = store(/** @type {{ result: import('../../lib/result.js').Result } | null} */ (null));

// "The map changed, draw it again" — geometry arrived, regions changed, line
// of sight landed. A counter.
export const changed = store(0);
const touch = () => {
    view.state.renderDirty = true;
    changed.set(changed.get() + 1);
};

// Learn mode (the static entity study view): the tab follows it, the URL
// (src/app/url.js) writes it to the URL (?learn=1) and sets it from a deep link. Every load
// starts in live mode.
export const learnMode = store(false);

// The interaction time is the page clock's (a click hit-tests the symbols
// at it, an orbit pivots on the followed player there).
clock.subscribe(({ t }) => { view.state.currentTime = t; });

// Region control's side A / side B are Go's (rc.teamA / rc.teamB), resolved
// to their canonical colour index by name — so again whenever either the
// canonical order or the region set changes.
const syncControlSides = () => {
    const rc = regionControl.get();
    view.state.controlSides = rc ? regionSides(teams.get().order, rc) : [0, 1];
};

teams.subscribe((t) => {
    view.teamColors = t.colors;
    syncControlSides();
});

// The region set in effect → the overlay and the region status. The demo's
// own regions ('loaded') arrive after each load; an edit on Locs & Regions
// lands in two steps, the resolved regions and then Go's control states.
// Region control needs two sides: a demo the editor is off for (a field of
// players) keeps no control regions, so neither shows.
regionControl.subscribe((rc) => {
    const s = view.state;
    syncControlSides();
    if (!rc) {
        s.controlRegions = null;
        s.locToRegion = {};
        s.bucketStates = null;
        return;
    }
    if (rc.stage === 'loaded') {
        s.controlRegions = rc.regions;
        s.locToRegion = locToRegionOf(rc.regions);
        computeRegionStacking(rc.regions);
        s.bucketStates = rc.bucketStates || null;
        if (!rc.editable) s.controlRegions = null;
        else if (!s.locationGroups && s.locations.length > 0) view.rebuildLocationGroups();
        touch();
        return;
    }
    s.controlRegions = rc.regions;
    if (rc.stage === 'resolved') {
        computeRegionStacking(rc.regions);
        s.locToRegion = locToRegionOf(rc.regions);
        s.regionToGroups = regionToGroupsOf(rc.regions, s.locationGroups);
        return;
    }
    s.bucketStates = rc.bucketStates;
    touch();
});

// resetMap drops the previous demo's data before a load.
export function resetMap() {
    const s = view.state;
    view.setFrames(null);
    Object.assign(s, {
        // regionToGroups too: it is rebuilt only when regions are applied
        // against geometry, so on a map without geometry JSON the previous
        // map's groups (and their floors) would stay tinted.
        locations: [], locationGroups: null, mapGeometry: null, locTable: [''], regionToGroups: {},
        bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0 },
        fullTrails: {}, trailStartTimes: {}, enabledPlayers: {}, dropEvents: [],
        teams: [], playerSymbols: {}, followPlayer: null, zRange: null,
        lastRenderedBucket: null, renderDirty: false,
    });
    mapData.set(null);
}

// loadMap loads `result` into the map: locs, streams, items, entities, the
// bounds, the player symbols and trails, and the geometry (fetched, applied
// when it lands). It resets the view — pan / zoom / angles, follow, focus,
// learn mode, the trail toggles — as every load always has, the deferred
// re-run when the buckets land included.
/** @param {import('../../lib/result.js').Result} result */
export function loadMap(result) {
    const ta = result.timelineAnalysis;
    if (!ta) return;
    const s = view.state;
    const cam = view.camera;

    s.locations = ta.locationData || [];
    s.locTable = ta.locTable || [''];
    s.locationGroups = null;
    s.mapGeometry = null;
    s.focusGroupName = null;
    s.focusNeighbors = null;
    s._floorModel = null;
    s._floorCanvasKey = null;
    s._floorZCache = null;
    s.movers = Array.isArray(result.streams?.movers) ? result.streams.movers : [];
    s.submodelMeshes = null;
    s._moverFaces = null;
    s.posStreams = posStreamsOf(result);
    s.projectiles = result.streams?.projectiles || null;
    s.beams = result.streams?.beams || null;
    s.nails = result.streams?.nails || null;
    // Line of sight is computed lazily (ensureLineOfSight), per demo.
    s.losByPair = {};
    s.pvsByPair = {};
    s.losComputed = false;
    s.losPending = false;
    s.items = Array.isArray(result.items?.items) ? result.items.items : null;
    s.mapEntities = Array.isArray(result.mapEntities?.entities) ? result.mapEntities.entities : [];
    view.buildTeleportArrows();
    s.learnMode = false;
    learnMode.set(false);

    // The BSP-derived floor polygons, when the map has them; otherwise the
    // loc points' convex hulls stay. A reply for a demo no longer loaded
    // (another load reset the map meanwhile) is dropped: it would put the
    // old map's floors under the new one.
    const basename = mapFileKey(result);
    s.mapBasename = basename;
    if (basename) {
        const t0 = performance.now();
        fetch(`maps/${basename}.json`)
            .then(r => r.ok ? r.json() : null)
            .then(geom => {
                console.log(`[mvd-timing] map geometry fetch (async): ${(performance.now() - t0).toFixed(1)} ms`);
                if (!geom || !Array.isArray(geom.locs) || geom.locs.length === 0) return;
                if (mapData.get()?.result !== result) return;
                applyMapGeometry(geom);
            })
            .catch(() => {});
    }

    view.setFrames(ta.bucketView || null);
    s.bounds = mapBounds(s.locations, ta.bucketView);
    view.refit();

    // The default tilted 3D view, no pan / zoom, nobody followed.
    cam.panX = 0;
    cam.panY = 0;
    cam.zoomK = 1;
    cam.yaw = DEFAULT_YAW;
    cam.pitch = DEFAULT_PITCH;
    refreshTrig(cam);
    s.followPlayer = null;

    s.teams = mapTeams(teams.get().order, result);
    s.playerSymbols = assignPlayerSymbols(result, s.teams);
    s.initialized = true;
    view.rebuildTrails();
    s.dropEvents = dropEventsOf(result);
    // The orbit pivots about the map's vertical middle; Reset view returns
    // to it.
    s.zRange = computeMapZRange(s.locations);
    cam.zMid = (s.zRange.lo + s.zRange.hi) / 2;
    cam.zMidDefault = cam.zMid;

    s.renderDirty = true;
    mapData.set({ result });
}

// applyMapGeometry installs a geometry as the floor / wall source; the
// regions in effect are applied again so the overlay's region → loc-group
// lookup points at the new groups. Not on a field of players: its model is
// not editable, no overlay shows, and Go answers a recompute there with
// "region control unavailable".
/** @param {{ locs: unknown[] }} geom  the map's geometry JSON (maps/<map>.json) */
function applyMapGeometry(geom) {
    view.setGeometry(geom);
    // An editable region model exists only once a demo is loaded.
    if (regionControl.get()?.editable) reapplyRegions(/** @type {import('../../data/wasm-source.js').DemoHandle} */ (demo.get()));
    touch();
}

// ensureLineOfSight runs the lazy line-of-sight pass once (the worker; one
// pass fills both the LOS and the PVS overlays). A failure leaves it to be
// asked again; a BSP-less map answers with no intervals and is not asked
// again.
export async function ensureLineOfSight() {
    const s = view.state;
    if (s.losComputed || s.losPending) return;
    s.losPending = true;
    touch();
    try {
        // The Map tab asks only with a demo loaded.
        const handle = /** @type {import('../../data/wasm-source.js').DemoHandle} */ (demo.get());
        const players = JSON.parse(await handle.lineOfSight());
        if (players && players.error) throw new Error(players.error);
        s.losByPair = buildVisByPair(players, 'los');
        s.pvsByPair = buildVisByPair(players, 'pvs');
        s.losComputed = true;
    } catch (err) {
        console.warn('computeLineOfSight:', /** @type {Error} */ (err).message);
    } finally {
        s.losPending = false;
        touch();
    }
}
