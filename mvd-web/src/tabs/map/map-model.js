// map-model.js — the Map tab's map view (mvd-map-view's MvdMap) and the
// demo's data in it. One instance for the page: the Map tab draws it
// (MapTab.svelte attaches the canvas and owns the view state: camera,
// trails, overlays, learn mode), and the Player tab's map pane copies its
// DATA into its own MvdMap each frame (src/player/panes/map-sync.js).
//
// The load's displayResults (src/app/load.js) loads a demo into it (`loadMap`, again when the
// deferred 50 ms buckets land — the player positions, trails and region
// states come from them) after `resetMap`; the region set in effect
// (src/state/regions.js) and the team colours (src/state/teams.js) are read
// from their stores. Loading is data only: nothing is drawn here. The tab
// redraws on `changed` when it shows.
//
// The pure parts (symbols, bounds, drops, canvas size) are src/lib/map.js.

import {
    MvdMap, DEFAULT_YAW, DEFAULT_PITCH, refreshTrig, computeMapZRange, computeRegionStacking, buildVisByPair,
} from '../../../../mvd-map-view/src/index.js';
import { store } from '../../state/store.js';
import { clock, demo, teams, regionControl, reapplyRegions } from '../../state/index.js';
import { mapFileKey } from '../../lib/result.js';
import { locToRegionOf, regionToGroupsOf } from '../../lib/regions.js';
import { mapTeams, assignPlayerSymbols, mapBounds, dropEventsOf, posStreamsOf, mapCanvasSize } from '../../lib/map.js';

export const view = new MvdMap();

// The demo whose data is loaded ({ result }), null after a reset: the tab
// rebuilds its legend, items list and controls from it.
export const mapData = store(null);

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

teams.subscribe((t) => { view.teamColors = t.colors; });

// The region set in effect → the overlay and the region status. The demo's
// own regions ('loaded') arrive after each load; an edit on Locs & Regions
// lands in two steps, the resolved regions and then Go's control states.
// Region control needs two sides: a demo the editor is off for (a field of
// players) keeps no control regions, so neither shows.
regionControl.subscribe((rc) => {
    const s = view.state;
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

// resizeMap sizes the canvas: the fullscreen panel's canvas container when
// the map is fullscreen, the fixed width at the world's aspect otherwise.
export function resizeMap() {
    const s = view.state;
    const canvas = s.canvas;
    if (!canvas) return;
    const fse = document.fullscreenElement;
    const fs = !!(fse && fse.classList && fse.classList.contains('map-panel'));
    const rect = fs && canvas.parentElement ? canvas.parentElement.getBoundingClientRect() : null;
    const [w, h] = mapCanvasSize(fs, rect, s.bounds);
    view.resize(w, h, window.devicePixelRatio || 1);
}

// resetMap drops the previous demo's data before a load.
export function resetMap() {
    const s = view.state;
    view.setFrames(null);
    if (s.canvas) s.ctx.clearRect(0, 0, s.canvas.width, s.canvas.height);
    Object.assign(s, {
        locations: [], locationGroups: null, mapGeometry: null, locTable: [''],
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
    // loc points' convex hulls stay.
    const basename = mapFileKey(result);
    s.mapBasename = basename;
    if (basename) {
        const t0 = performance.now();
        fetch(`maps/${basename}.json`)
            .then(r => r.ok ? r.json() : null)
            .then(geom => {
                console.log(`[mvd-timing] map geometry fetch (async): ${(performance.now() - t0).toFixed(1)} ms`);
                if (!geom || !Array.isArray(geom.locs) || geom.locs.length === 0) return;
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
    resizeMap();

    s.teams = mapTeams(teams.get().order, result);
    s.playerSymbols = assignPlayerSymbols(result, s.teams);
    if (!s.initialized && s.canvas) s.canvas.style.cursor = 'grab';
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
// lookup points at the new groups.
function applyMapGeometry(geom) {
    view.setGeometry(geom);
    if (regionControl.get()) reapplyRegions(demo.get());
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
        const players = JSON.parse(await demo.get().lineOfSight());
        if (players && players.error) throw new Error(players.error);
        s.losByPair = buildVisByPair(players, 'los');
        s.pvsByPair = buildVisByPair(players, 'pvs');
        s.losComputed = true;
    } catch (err) {
        console.warn('computeLineOfSight:', err.message);
    } finally {
        s.losPending = false;
        touch();
    }
}
