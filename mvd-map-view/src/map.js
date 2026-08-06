// MvdMap — the stateful renderer container.
//
// This is the object a host application holds. It owns the camera, the
// geometry, the derived render caches and the playback clock; the host owns
// everything around the canvas (chrome, panels, URL state) and every loader.
//
// Read the loader invariant in the package README before adding anything
// here: this class must never fetch, never touch `document` or `window`, and
// never measure its container. Data comes in through setters; size comes in
// through resize(). The MCP Apps viewer runs behind a CSP with no network
// origins and inside an iframe the host sizes, and a violation of either rule
// is invisible until it fails there.
//
// Extraction is in progress. Today the class owns the state bag and the
// camera, and mvd-web drives it through the same field names it used when
// this was a module-level object in app.js — that equivalence is what keeps
// the move verifiable a group at a time. The render methods follow.

import { newCamera } from './camera.js';

// newState returns the renderer's mutable state. Fields are grouped by what
// they answer: what the map IS, what is being SHOWN, and what has been
// DERIVED and cached from those two.
export function newState() {
    return {
        // Target surface. The host supplies both; the component never looks
        // either of them up.
        canvas: null,
        ctx: null,

        // What the map is.
        locations: [],        // MapLocation[] — loc points, positions + names
        locationGroups: null, // cached processed loc regions
        locationGroupByName: null,
        mapGeometry: null,    // BSP-derived per-loc polygons (optional)
        submodelMeshes: null, // { submodelId -> tris } from corpus v4 geom.submodels
        mapEntities: [],      // the map's designed entity layout
        teleportArrows: [],   // precomputed entrance→exit world-coord pairs
        bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0 },

        // Who is on it.
        teams: [],
        playerSymbols: {},    // playerName -> { symbol, team, teamIdx }
        posStreams: {},       // name -> PositionTrack for stream-sourced animation
        movers: [],           // brush-model pose timelines

        // Playback clock.
        currentTime: 0,
        isPlaying: false,
        playbackSpeed: 1,
        animationFrameId: null,
        lastRenderTime: 0,

        // What is being shown.
        trailDuration: 10,
        fullTrails: {},
        trailStartTimes: {},
        enabledPlayers: {},
        showViewArrows: false,
        showVelArrows: false,
        showLos: false,
        showPvs: false,
        followPlayer: null,
        learnMode: false,
        focusGroupName: null,
        focusNeighbors: null,
        fullscreen: false,
        entityFilters: {
            weapon: true, armor: true, health: true, ammo: true, powerup: true,
            teleporter: true, spawn: false, button: false, door: false,
        },

        // Derived / lazy.
        losByPair: {},
        pvsByPair: {},
        losComputed: false,
        losPending: false,

        // Redraw bookkeeping.
        initialized: false,
        lastRenderedBucket: null,
        renderDirty: false,
    };
}

export class MvdMap {
    constructor(canvas = null, opts = {}) {
        this.state = newState();
        this.camera = newCamera();
        if (canvas) this.attach(canvas);
        this.options = opts;
    }

    // attach binds the canvas the renderer draws into. Separate from the
    // constructor because the host may build the map before its canvas is in
    // the document (mvd-web constructs on script load, attaches on demo load).
    attach(canvas) {
        this.state.canvas = canvas;
        this.state.ctx = canvas.getContext('2d');
    }
}
