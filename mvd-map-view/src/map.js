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

import {
    newCamera, project, is3D, fit, setAngles, setOrbitCenter, toView, toWorld,
    DEFAULT_YAW, DEFAULT_PITCH,
} from './camera.js';
import { moverPoseAt, pointInTriangle, normalizeMapGeometry } from './geometry.js';
import {
    buildFloorModel, processLocationGroups, groupWorldBBox, computeRegionOutline,
} from './locgroups.js';
import { scaleRgbaAlpha, hexToRgba } from './color.js';
import { ARROWHEAD_PX } from './draw.js';
import { lowerBoundIndex, trailIndexAtTime } from './util.js';
import { normalizeLocationName, findNearestLocation } from './locs.js';
import {
    bucketTimeSec, bucketIndexAtTime,
    reconstructBucketPlayers, reconstructBucketTeams,
} from './frames.js';
import { decodeRegionStateChar } from './regions.js';
import { GlWorld, buildLiquidFaces, parseColor } from './glworld.js';
import { GlAtlas } from './glatlas.js';

// Fixed light for face shading — high, slightly off-axis so faces pointing
// different directions separate tonally (used by the liquid volumes).
const SOLID_LIGHT = (() => {
    const l = [0.35, 0.25, 0.9];
    const n = Math.hypot(l[0], l[1], l[2]);
    return [l[0] / n, l[1] / n, l[2] / n];
})();

// A mover reads as a moving piece of floor: one flat silhouette at the same
// opacity as the floor tops, a touch lighter than the backdrop so it stays
// legible at rest, brighter again while a player is riding it.
const MOVER_FILL = 'rgba(96, 107, 140, 0.92)';
const MOVER_FILL_ACTIVE = 'rgba(150, 170, 215, 0.95)';

// A player counts as "riding" a posed mover when their XY lands within its
// footprint and their z sits within a player-height window of its top surface.
const MOVER_RIDE_Z_BELOW = 24; // tolerance under the top (interp / step noise)
const MOVER_RIDE_Z_ABOVE = 56; // ~player height above the top

// Liquid volumes (water/slime/lava) from corpus v4: each face Lambert-shaded
// so the top surface reads brighter than the descending sides, painted back to
// front so the body reads as a 3D volume rather than a flat silhouette.
const LIQUID_BASE = {
    water: [64, 128, 255],
    slime: [80, 200, 80],
    lava:  [255, 120, 40],
};
const LIQUID_ALPHA = 0.15; // per-face; back-to-front stacking deepens it

// Colours for the weapon-fire overlays.
const PROJECTILE_COLORS = { rl: '#ff7733', gl: '#66cc44' };
const NAIL_COLOR = '#ffe066';
const BEAM_COLOR = 'rgba(150, 200, 255, 0.85)';
// A beam flashes for this half-window (ms) around its instant.
const BEAM_FLASH_MS = 60;

const ARMOR_COLORS = {
    ra: 'rgb(255, 50, 50)',
    ya: 'rgb(255, 200, 0)',
    ga: 'rgb(0, 180, 0)',
};

// Badge layout / colours used by the map view to draw inventory icons around
// each player marker. Hoisted from the map-rendering section so the palette
// lives next to the rest of the theme.
const BADGE_DEFS = [
    { angle:   0, key: 'q',   letter: 'Q', color: 'rgb(0, 150, 255)' },
    { angle:  45, key: 'rl',  letter: 'R', color: 'rgb(255, 107, 107)' },
    { angle:  90, key: 'lg',  letter: 'L', color: 'rgb(0, 217, 255)' },
    { angle: 135, key: 'sng', letter: 'N', color: 'rgb(180, 140, 100)' },
    { angle: 180, key: 'mh',  letter: 'M', color: 'rgb(0, 200, 83)' },
    { angle: 225, key: 'arm', letter: 'A', color: null },
    { angle: 270, key: 'pe',  letter: 'P', color: 'rgb(255, 0, 0)' },
    { angle: 315, key: 'r',   letter: 'I', color: 'rgb(255, 235, 59)' },
];

// ─── Per-player 3D arrows: view direction + velocity ────────────────────────
//
// Both arrows are true 3D: the shaft runs from the player origin to a
// world-space tip (projected through the orbit camera), and a small arrowhead
// is drawn at the projected tip, oriented along the projected shaft. The view
// arrow is a short fixed-length facing indicator; the velocity arrow's length
// encodes speed at VEL_UNITS_PER_MAP_UNIT u/s per world unit.
const ANGLE16_TO_RAD = (360 / 65536) * (Math.PI / 180); // raw angle16 → radians

// Slack added when searching for the supporting floor, so interpolation
// noise on ramps / step edges doesn't make the search miss the surface the
// player is actually standing on.
const FLOOR_SNAP_TOLERANCE = 4;

const ITEM_DIM_ALPHA = 0.35;  // alpha multiplier when item is taken

// Item kind → filter category.
export const ITEM_KIND_CATEGORY = {
    rl: 'weapon', lg: 'weapon', gl: 'weapon', ssg: 'weapon', sng: 'weapon', ng: 'weapon',
    ra: 'armor', ya: 'armor', ga: 'armor',
    mh: 'health', h25: 'health', h15: 'health',
    shells: 'ammo', nails: 'ammo', rockets: 'ammo', cells: 'ammo',
    quad: 'powerup', pent: 'powerup', ring: 'powerup', suit: 'powerup',
};

const ITEM_MARKER_SIZE = 20;  // 25% larger than the prior 16 px baseline

// Display metadata per item kind. Armors render as a solid-coloured
// square with black text; weapons / MH / powerups as a black square
// with a coloured outline and text in the outline colour. Kinds not
// listed here (ammo, small health) are skipped on the map and in the
// sidebar.
export const ITEM_MARKER_STYLES = {
    ra:   { fill: 'rgb(255, 50, 50)',   outline: null,                   label: 'RA', textColor: '#000' },
    ya:   { fill: 'rgb(255, 200, 0)',   outline: null,                   label: 'YA', textColor: '#000' },
    ga:   { fill: 'rgb(0, 180, 0)',     outline: null,                   label: 'GA', textColor: '#000' },
    mh:   { fill: '#000',               outline: 'rgb(0, 200, 83)',      label: 'MH' },
    rl:   { fill: '#000',               outline: 'rgb(255, 107, 107)',   label: 'RL' },
    lg:   { fill: '#000',               outline: 'rgb(0, 217, 255)',     label: 'LG' },
    ssg:  { fill: '#000',               outline: '#aaaaaa',              label: 'SS' },
    gl:   { fill: '#000',               outline: '#c78a3a',              label: 'GL' },
    ng:   { fill: '#000',               outline: '#8090a0',              label: 'NG' },
    sng:  { fill: '#000',               outline: 'rgb(180, 140, 100)',   label: 'SN' },
    quad: { fill: '#000',               outline: 'rgb(0, 150, 255)',     label: 'Q'  },
    pent: { fill: '#000',               outline: 'rgb(255, 0, 0)',       label: 'P'  },
    ring: { fill: '#000',               outline: 'rgb(255, 235, 59)',    label: 'I'  },
};

// Items are biased this much below their real z when sorting against
// players, so a player standing at the same floor as an item (same z)
// draws on top. An item only occludes a player when its z exceeds the
// player's by at least this clearance — i.e. the item sits on a real
// level above the player.
const ITEM_Z_TOP_THRESHOLD = 48;

// Item markers reuse the playback palette plus the kinds it omits (ammo,
// small health, suit). Structural entities get their own glyphs.
export const LEARN_ITEM_STYLES = Object.assign({}, ITEM_MARKER_STYLES, {
    h25:     { fill: '#000', outline: 'rgb(0, 200, 83)',  label: 'H'  },
    h15:     { fill: '#000', outline: '#6f8f6f',          label: 'h'  },
    shells:  { fill: '#000', outline: '#b0a070',          label: 'sh' },
    nails:   { fill: '#000', outline: '#8090a0',          label: 'nl' },
    rockets: { fill: '#000', outline: 'rgb(255,107,107)', label: 'rk' },
    cells:   { fill: '#000', outline: 'rgb(0,217,255)',   label: 'cl' },
    suit:    { fill: '#000', outline: '#00e676',          label: 'ES' },
});

// result.NoFloor sentinel in PositionTrack.H — no floor to measure from.
const MAP_NO_FLOOR = -2147483648;

// A standing player's origin sits this far above the floor (mins.z = -24 in
// standard Quake 1).
const PLAYER_ORIGIN_ABOVE_FLOOR = 24;

// The teleporter accent, shared by the entrance marker, the exit disc and
// the link arrows.
const TELEPORT_COLOR = '#b388ff';

const STRUCTURAL_STYLES = {
    spawn:       { fill: '#15151f',       outline: '#888',         label: 'S' },
    teleportSrc: { fill: '#1a0a2a',       outline: TELEPORT_COLOR, label: 'T' },
    teleportDst: { fill: TELEPORT_COLOR,  outline: TELEPORT_COLOR, label: '', circle: true },
    button:      { fill: '#000',          outline: '#ff9800',      label: 'B' },
    door:        { fill: '#000',          outline: '#a1887f',      label: 'D' },
};


const VEL_ARROW_MIN_SPEED = 10;       // u/s below which no velocity arrow is drawn

const VEL_UNITS_PER_MAP_UNIT = 5;     // 5 u/s of speed → 1 world unit of arrow

const VIEW_ARROW_COLOR = 'rgba(245, 245, 255, 0.92)';

const VIEW_ARROW_LEN = 64;            // world units — shows facing clearly

export const DEATH_X_DURATION  = 2.0;  // seconds an "X" death marker stays on the map

// regionActiveTint: tint for a region by the team(s) currently in it — one
// team → that team's canonical colour, both teams → white (contested). Drawn
// over the neutral floor, so colour here always means "a player is here".
const REGION_TINT_ALPHA = 0.3;
const REGION_TINT_CONTESTED = `rgba(235, 235, 245, ${REGION_TINT_ALPHA})`;

// Stack-aware opacity boost: regions with no overlapping, higher-z region
// currently occupied are drawn at this multiple of their base alpha, so a
// lower deck standing alone reads cleanly rather than washing out against an
// empty upper deck's tint. Clamped final alpha to 0.5 so regions never
// become opaque.
const REGION_OPACITY_BOOST = 1.9;

// Line-of-sight / PVS debug colours. White when both players see each other;
// the one-way case is coloured by which of the pair (as ordered in the name
// list) is the sole seer — red = the first, blue = the second. The colours are
// debug-arbitrary, not team colours. The PVS palette is the same hues at a
// lower alpha so the (much denser) potential-visibility lines read as a faint
// backdrop under the solid LOS lines.
const LOS_STYLE = {
    width: 3,
    mutual: 'rgba(255,255,255,0.65)',
    first: 'rgba(255,80,80,0.65)',
    second: 'rgba(90,150,255,0.65)',
};
const PVS_STYLE = {
    width: 1.5,
    mutual: 'rgba(255,255,255,0.35)',
    first: 'rgba(255,80,80,0.35)',
    second: 'rgba(90,150,255,0.35)',
};

// Slightly larger than the base symbol radius so the click-to-follow hit
// area stays generous even when a high-deck / max-zoom player renders at
// the 1.5 * 1.25 ≈ 1.88x upper bound.
const FOLLOW_HIT_RADIUS_PX = 24;

// Two named regions are "neighbors" when their world-XY bounding boxes are
// within this many units of touching — roughly one corridor width.
const FOCUS_NEIGHBOR_MARGIN = 160;

// Pointer interaction tuning. Pan: left-drag. Rotate (3D orbit): the host
// maps right-drag / Ctrl+drag to 'orbit' — horizontal motion spins the map
// (yaw), vertical motion tilts it (pitch). Zoom: wheel, centered on cursor.
// A press that moves less than CLICK_MAX_MOTION_PX is a click.
const CLICK_MAX_MOTION_PX = 5;
const ZOOM_MIN = 0.5;
const ZOOM_MAX = 12;
const ORBIT_YAW_PER_PX = 0.008;   // rad of yaw per horizontal pixel
const ORBIT_PITCH_PER_PX = 0.005; // rad of pitch per vertical pixel

// buildVisByPair flattens the worker's per-player [{name, los/pvs:[{o,iv}]}]
// reply into byPair[lookerName][targetName] = [{s,e},…] for the given field
// ('los' or 'pvs'). The track's o indexes the players array; resolve it to that
// player's name. Both metrics are asymmetric, so each direction is stored under
// its own looker.
export function buildVisByPair(players, field) {
    const byPair = {};
    if (!Array.isArray(players)) return byPair;
    const idxToName = players.map(p => p && p.name);
    for (const p of players) {
        if (!p || !Array.isArray(p[field])) continue;
        const byTarget = (byPair[p.name] ||= {});
        for (const tr of p[field]) {
            const other = idxToName[tr.other];
            if (other != null && Array.isArray(tr.intervals)) byTarget[other] = tr.intervals;
        }
    }
    return byPair;
}

// losCovers reports whether the half-open [s,e) interval list (ascending,
// match-relative ms) covers tMs. Linear is fine — a pair has few intervals.
export function losCovers(iv, tMs) {
    if (!iv) return false;
    for (let i = 0; i < iv.length; i++) {
        if (tMs >= iv[i].s && tMs < iv[i].e) return true;
        if (iv[i].s > tMs) break;
    }
    return false;
}

// newState returns the renderer's mutable state. Fields are grouped by what
// they answer: what the map IS, what is being SHOWN, and what has been
// DERIVED and cached from those two.
export function newState() {
    return {
        // Target surface. The host supplies both; the component never looks
        // either of them up. Size arrives only through resize() — the
        // component never measures a container (loader invariant: the MCP
        // host owns the iframe's dimensions and pushes changes in).
        canvas: null,
        ctx: null,
        canvasCssW: 0,
        canvasCssH: 0,
        dpr: 1,

        // What the map is.
        locations: [],        // MapLocation[] — loc points, positions + names
        locTable: [''],       // interned loc-name table; index 0 = no-loc sentinel
        locationGroups: null, // cached processed loc regions
        locationGroupByName: null,
        controlRegions: null, // region-control definitions (host-supplied)
        regionToGroups: {},   // region name -> [loc groups] for the control overlay
        mapGeometry: null,    // BSP-derived per-loc polygons (optional)
        submodelMeshes: null, // { submodelId -> tris } from corpus v4 geom.submodels
        items: null,          // item spawners with their phase timelines
        mapEntities: [],      // the map's designed entity layout
        teleportArrows: [],   // precomputed entrance→exit world-coord pairs
        bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0 },

        // The frame feed: the column-major ColumnarBuckets view (frames.js
        // documents the shape), pushed in whole via setFrames. Null until it
        // arrives — the map draws a world with nobody on it, which is exactly
        // the partially-loaded-timeline state a windowed source produces too.
        frames: null,
        bucketStates: null,   // region name → per-bucket control-state string

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
        deathEvents: [],      // time-sorted {t, wx, wy, wz, teamIdx} death frames
        dropEvents: [],       // backpack drops, same shape + weapon
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
        // Scratch projection point, reused by toCanvas. Bound projection
        // callbacks so draw primitives can be handed a plain function.
        this._pt = { x: 0, y: 0, depth: 0 };
        this._toCanvas = (x, y, z) => this.toCanvas(x, y, z);
        this._toCanvasNew = (x, y, z) => this.toCanvasNew(x, y, z);
        if (canvas) this.attach(canvas);
        this.options = opts;
        // Host-facing events ('follow', 'camera') and the pointer-drag state
        // machine. The host wires DOM events to the pointer methods and
        // mirrors the emitted changes into its own chrome.
        this._listeners = {};
        // WebGL world backend, created lazily on the first drawn frame.
        // _glFailed latches a failed/lost context so every later frame takes
        // the 2D fallback without re-probing.
        this._glWorld = null;
        this._glFailed = false;
        this._drag = {
            active: false,
            button: -1,
            mode: 'pan', // 'pan' | 'orbit'
            startX: 0, startY: 0,
            lastX: 0, lastY: 0,
            yaw0: 0, pitch0: 0, // camera angles at orbit-drag start
            moved: false,
        };
        // Team palette, indexed by a team's position in the host's frag-sorted
        // team order. mvd-web owns that canonical ordering and passes its own
        // array; the default reproduces it for a host that passes none.
        this.teamColors = opts.teamColors || ['#ff5050', '#50a0ff', '#4ecdc4', '#ffc107'];
    }

    // attach binds the canvas the renderer draws into. Separate from the
    // constructor because the host may build the map before its canvas is in
    // the document (mvd-web constructs on script load, attaches on demo load).
    attach(canvas) {
        this.state.canvas = canvas;
        this.state.ctx = canvas.getContext('2d');
    }

    // resize sets the canvas surface to cssW × cssH CSS pixels, backed by a
    // physical-pixel bitmap sized for `dpr` so lines and text render at
    // device resolution. Push-only: the host measures whatever it wants
    // (container, fullscreen element, iframe) and hands the result in. All
    // draw code works in CSS pixels; render() applies setTransform(dpr, ...)
    // so ctx operations map from CSS → physical automatically. Refits the
    // world→canvas map; pan/zoom/angles survive (a resize must not throw
    // away the user's view).
    resize(cssW, cssH, dpr = 1) {
        const s = this.state;
        const canvas = s.canvas;
        if (!canvas) return;
        s.dpr = dpr;
        s.canvasCssW = cssW;
        s.canvasCssH = cssH;
        canvas.width = Math.round(cssW * dpr);
        canvas.height = Math.round(cssH * dpr);
        canvas.style.width = cssW + 'px';
        canvas.style.height = cssH + 'px';
        this.refit();
    }

    // refit recomputes the world→canvas linear map from the current bounds
    // and surface size. Also re-centres the orbit pivot on the map's XY
    // centre (see camera.fit); zMid is the caller's to manage.
    refit() {
        const s = this.state;
        if (!s.canvas) return;
        const cssW = s.canvasCssW || s.canvas.width;
        const cssH = s.canvasCssH || s.canvas.height;
        fit(this.camera, s.bounds, cssW, cssH);
    }

    // ─── Projection ─────────────────────────────────────────────────────────

    // toCanvas reuses a scratch point: safe only for values consumed before
    // the next call (a moveTo/lineTo pair). toCanvasNew allocates, for points
    // that get stored or held alongside another.
    toCanvas(x, y, z) {
        return project(this.camera, x, y, z, this._pt);
    }

    toCanvasNew(x, y, z) {
        return project(this.camera, x, y, z, { x: 0, y: 0, depth: 0 });
    }

    // iconScale: capped upscale applied to player symbols, item markers and
    // loc labels so they stay legible as the user zooms in. Linear ramp from
    // 1.0 at zoomK=1, reaching the 1.5x cap around zoomK≈4.3.
    iconScale() {
        const k = this.camera.zoomK || 1;
        if (k <= 1) return 1;
        return Math.min(1.5, 1 + (k - 1) * 0.15);
    }

    // ─── Region focus ───────────────────────────────────────────────────────

    // focusTier: which styling tier a named group falls into, or null when no
    // region is focused.
    focusTier(name) {
        const s = this.state;
        if (!s.focusGroupName) return null;
        if (name === s.focusGroupName) return 'focus';
        if (s.focusNeighbors && s.focusNeighbors.has(name)) return 'near';
        return 'far';
    }

    // tierFill: how a group's base fill reacts to the active focus.
    tierFill(group, tier) {
        const base = group.color.fill;
        if (tier === 'focus') return scaleRgbaAlpha(base, 6, 0.5);
        if (tier === 'near')  return scaleRgbaAlpha(base, 3.5, 0.32);
        if (tier === 'far')   return scaleRgbaAlpha(base, 0.3);
        return base;
    }

    // farFadePredicate: null when nothing is focused, which lets the solid
    // renderer skip the per-entry test entirely.
    farFadePredicate() {
        if (!this.state.focusGroupName) return null;
        return (name) => this.focusTier(name) === 'far';
    }

    // ─── Floor model ────────────────────────────────────────────────────────

    // floorModel: the depth-sortable render list for the current geometry and
    // groups, rebuilt only when either changes.
    floorModel() {
        const s = this.state;
        const m = s._floorModel;
        if (m && m.geom === s.mapGeometry && m.groups === s.locationGroups) return m;
        s._floorModel = buildFloorModel(s.mapGeometry, s.locationGroups || []);
        return s._floorModel;
    }

    // ─── Movers ─────────────────────────────────────────────────────────────

    // moverMeshFaces: a submodel's per-triangle outward-test normals, cached
    // by id. Translation preserves normals, so this is pose-independent, and
    // only the sign matters — they are left unnormalized.
    moverMeshFaces(sub) {
        const s = this.state;
        let cache = s._moverFaces;
        if (!cache) cache = s._moverFaces = {};
        if (cache[sub]) return cache[sub];
        const tris = s.submodelMeshes[sub];
        const faces = [];
        for (let i = 0; i + 8 < tris.length; i += 9) {
            const ux = tris[i + 3] - tris[i],     uy = tris[i + 4] - tris[i + 1], uz = tris[i + 5] - tris[i + 2];
            const vx = tris[i + 6] - tris[i],     vy = tris[i + 7] - tris[i + 1], vz = tris[i + 8] - tris[i + 2];
            const nx = uy * vz - uz * vy, ny = uz * vx - ux * vz, nz = ux * vy - uy * vx;
            faces.push({ off: i, nx, ny, nz });
        }
        cache[sub] = faces;
        return faces;
    }

    // moverLocalBBox: a submodel's local-space bounds, cached per id. A pose is
    // a pure translation, so the world footprint is this box shifted by it.
    moverLocalBBox(sub) {
        const s = this.state;
        let cache = s._moverBBox;
        if (!cache) cache = s._moverBBox = {};
        if (cache[sub]) return cache[sub];
        const mesh = s.submodelMeshes[sub];
        let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity, minZ = Infinity, maxZ = -Infinity;
        for (let i = 0; i + 2 < mesh.length; i += 3) {
            const x = mesh[i], y = mesh[i + 1], z = mesh[i + 2];
            if (x < minX) minX = x; if (x > maxX) maxX = x;
            if (y < minY) minY = y; if (y > maxY) maxY = y;
            if (z < minZ) minZ = z; if (z > maxZ) maxZ = z;
        }
        cache[sub] = { minX, maxX, minY, maxY, minZ, maxZ };
        return cache[sub];
    }

    playerOnMover(pose, sub, players) {
        const bb = this.moverLocalBBox(sub);
        const minX = bb.minX + pose.x, maxX = bb.maxX + pose.x;
        const minY = bb.minY + pose.y, maxY = bb.maxY + pose.y;
        const topZ = bb.maxZ + pose.z;
        for (const p of players) {
            if (p.x < minX || p.x > maxX || p.y < minY || p.y > maxY) continue;
            if (p.z < topZ - MOVER_RIDE_Z_BELOW || p.z > topZ + MOVER_RIDE_Z_ABOVE) continue;
            return true;
        }
        return false;
    }

    // livingPlayersAtFrame: the frame's living players, for mover-ride tests.
    // Drawn from the player data the frame composition stashed.
    livingPlayersAtFrame() {
        const pd = this.state._framePlayerData;
        const out = [];
        if (!pd) return out;
        for (const d of Object.values(pd)) {
            if (!d) continue;
            if (d.d || (d.h !== undefined && d.h <= 0)) continue;
            if (d.x === 0 && d.y === 0) continue;
            out.push(d);
        }
        return out;
    }


    // ─── Weapon-fire overlays ───────────────────────────────────────────────




    // ─── World layers ───────────────────────────────────────────────────────


    // scratchCanvas: an offscreen surface for the world bake, taken from the
    // document that owns the target canvas — NOT the ambient `document`, which
    // this package must not touch (see the loader invariant). The host always
    // hands us a real canvas element, in mvd-web and in an MCP iframe alike.
    scratchCanvas() {
        return this.state.canvas.ownerDocument.createElement('canvas');
    }


    // drawWorldGL: render floors + liquids through the WebGL backend into an
    // internal offscreen canvas and blit it under the 2D layers. Returns
    // false when GL is off, unavailable, or lost — the caller falls back to
    // the 2D painter, so a dead driver degrades to the old path instead of a
    // black map. The blit keeps the component's single-canvas contract: the
    // host still sees one canvas, and the DOM never changes.
    drawWorldGL(ctx, floorModel) {
        const s = this.state;
        if (this._glFailed) return false;
        if (!this._glWorld) {
            const glw = GlWorld.create(this.scratchCanvas());
            if (!glw) {
                this._glFailed = true;
                return false;
            }
            this._glWorld = glw;
        }
        const glw = this._glWorld;
        if (glw.gl.isContextLost()) {
            this._glFailed = true;
            this._glWorld = null;
            return false;
        }
        glw.syncFloor(floorModel, s.focusGroupName, this.farFadePredicate());
        // Liquid faces are static per geometry (fixed light): identity-cache
        // them next to the geometry they came from.
        const geom = s.mapGeometry;
        if (s._liquidFacesGeom !== geom) {
            s._liquidFaces = buildLiquidFaces(geom && geom.liquids,
                                              LIQUID_BASE, LIQUID_ALPHA, SOLID_LIGHT);
            s._liquidFacesGeom = geom;
        }
        glw.syncLiquids(s._liquidFaces);
        const canvas = s.canvas;
        const cssW = s.canvasCssW || canvas.width;
        const cssH = s.canvasCssH || canvas.height;
        glw.render(this.camera, cssW, cssH, canvas.width, canvas.height,
                   this._glDynamic(canvas.width / cssW));
        ctx.save();
        ctx.setTransform(1, 0, 0, 1, 0, 0);
        ctx.drawImage(glw.canvas, 0, 0);
        ctx.restore();
        return true;
    }

    // _glDynamic collects the per-frame dynamic world content for the GL
    // backend: posed movers (with the riding-player highlight), the region
    // outlines and overlay tints, live projectile/nail dots and beam
    // flashes. `pxScale` converts CSS-pixel sizes to the physical pixels the
    // GL surface rasterises at.
    _glDynamic(pxScale) {
        const s = this.state;
        const tMs = s.currentTime * 1000;
        const atlas = this._atlas();
        const iconScale = this.iconScale();
        const dyn = {
            movers: [], points: [], lines: [],
            baseFills: [], regionOutlines: [], fills: [], fillOutlines: [],
            labels: [], boldLabels: [], actorBatches: [],
            atlas,
        };

        // Loc-blob maps (no triangle geometry → no floor model): the flat
        // translucent underlay the 2D painter used to draw, as blended fills
        // under everything else.
        if (!this.floorModel()) {
            const focused = !!s.focusGroupName;
            const backdropTris = s.mapGeometry && s.mapGeometry.backdropTris;
            if (backdropTris && backdropTris.length >= 9) {
                dyn.baseFills.push({
                    tris: backdropTris,
                    tint: parseColor(focused ? 'rgba(70, 80, 110, 0.14)' : 'rgba(70, 80, 110, 0.35)'),
                });
            }
            for (const group of s.locationGroups || []) {
                if (group.tris && group.tris.length >= 9) {
                    dyn.baseFills.push({
                        tris: group.tris,
                        tint: parseColor(this.tierFill(group, this.focusTier(group.name))),
                    });
                }
            }
        }

        // Loc labels — every named region, tier-faded like the 2D pass.
        const labelFont = `${Math.round(12 * iconScale * pxScale)}px monospace`;
        for (const group of s.locationGroups || []) {
            const tier = this.focusTier(group.name);
            const color = tier === 'far'
                ? scaleRgbaAlpha(group.color.text, 0.35)
                : group.color.text;
            const sp = this._glLabelSprite(atlas, group.name, labelFont, color,
                group.centroid.x, group.centroid.y, group.centroid.z);
            if (sp) dyn.labels.push(sp);
        }

        // Thin baseline outlines around every traced region, styled by the
        // focus tier — the quiet strokes the occupied overlay brightens.
        for (const group of s.locationGroups || []) {
            if (!group.tris || group.tris.length < 9) continue;
            const outline = computeRegionOutline(group);
            if (!outline || outline.length < 6) continue;
            const tier = this.focusTier(group.name);
            let stroke = 'rgba(180, 180, 180, 0.22)';
            let width = 1;
            if (tier === 'focus')    { stroke = 'rgba(255, 255, 255, 0.85)'; width = 1.5; }
            else if (tier === 'far') { stroke = 'rgba(180, 180, 180, 0.1)'; }
            dyn.regionOutlines.push({
                outline, tint: parseColor(stroke), halfWidth: (width / 2) * pxScale,
            });
        }

        // Overlay tints — region control under the occupancy highlight, both
        // playback-only (learn mode studies the bare map).
        s._frameOccupiedNames = null;
        if (!s.learnMode) {
            if (s.controlRegions && s.regionToGroups) {
                const controlStates = this.regionControlAt(s.currentTime);
                const ctrl = controlStates ? this.computeControlFills(controlStates) : null;
                if (ctrl) {
                    for (const { group, tint } of ctrl) {
                        dyn.fills.push({ tris: group.tris, tint: parseColor(tint) });
                    }
                }
            }
            const ov = s._framePlayerData
                ? this.computeOccupiedOverlay(s._framePlayerData) : null;
            if (ov) {
                s._frameOccupiedNames = ov.names;
                const outlineTint = parseColor('rgba(220, 220, 220, 0.7)');
                for (const { group, tint } of ov.groups) {
                    dyn.fills.push({ tris: group.tris, tint: parseColor(tint) });
                    const outline = computeRegionOutline(group);
                    if (outline && outline.length >= 6) {
                        dyn.fillOutlines.push({
                            outline, tint: outlineTint, halfWidth: 0.5 * pxScale,
                        });
                    }
                }
                // Bold labels over the dimmer baseline ones — a black shadow
                // sprite offset one pixel under the white text (both reuse
                // one white atlas entry, restyled by tint).
                const boldFont = `bold ${Math.round(12 * iconScale * pxScale)}px monospace`;
                const groupsByName = s.locationGroupByName || {};
                for (const name of ov.names) {
                    const g = groupsByName[name];
                    if (!g) continue;
                    const shadow = this._glLabelSprite(atlas, g.name, boldFont,
                        'rgba(255, 255, 255, 0.95)',
                        g.centroid.x, g.centroid.y, g.centroid.z,
                        pxScale, pxScale, [0, 0, 0, 0.65 / 0.95]);
                    if (shadow) dyn.boldLabels.push(shadow);
                    const sp = this._glLabelSprite(atlas, g.name, boldFont,
                        'rgba(255, 255, 255, 0.95)',
                        g.centroid.x, g.centroid.y, g.centroid.z);
                    if (sp) dyn.boldLabels.push(sp);
                }
            }
        }

        if (s.movers && s.movers.length > 0 && s.submodelMeshes) {
            const players = this.livingPlayersAtFrame();
            for (const m of s.movers) {
                const mesh = s.submodelMeshes[m.sub];
                if (!mesh || mesh.length < 9) continue;
                const pose = moverPoseAt(m, tMs);
                if (!pose || !pose.vis) continue;
                const active = players.length > 0 && this.playerOnMover(pose, m.sub, players);
                dyn.movers.push({
                    sub: m.sub, tris: mesh,
                    x: pose.x, y: pose.y, z: pose.z,
                    tint: parseColor(active ? MOVER_FILL_ACTIVE : MOVER_FILL),
                });
            }
        }

        const collectFlights = (pr, radius, colorOf) => {
            if (!pr || !Array.isArray(pr.s) || pr.s.length === 0) return;
            for (let i = 0; i < pr.s.length; i++) {
                const t0 = pr.s[i], t1 = pr.e[i];
                if (tMs < t0 || tMs > t1) continue;
                const f = t1 > t0 ? (tMs - t0) / (t1 - t0) : 0;
                dyn.points.push({
                    x: pr.sx[i] + (pr.ex[i] - pr.sx[i]) * f,
                    y: pr.sy[i] + (pr.ey[i] - pr.sy[i]) * f,
                    z: pr.sz[i] + (pr.ez[i] - pr.sz[i]) * f,
                    size: radius * 2 * pxScale,
                    color: parseColor(colorOf(pr.w ? pr.w[i] : null)),
                });
            }
        };
        collectFlights(s.projectiles, 3, (w) => PROJECTILE_COLORS[w] || '#ffffff');
        collectFlights(s.nails, 1.5, () => NAIL_COLOR);

        // Trails and sightlines draw under the weapon fire, so collect them
        // into dyn.lines / dyn.points first (playback only, like the 2D
        // pass ordering in render()).
        if (!s.learnMode) {
            this._collectTrails(dyn, pxScale);
            if (s.showPvs) this._collectVisLines(dyn, s.pvsByPair, PVS_STYLE, pxScale);
            if (s.showLos) this._collectVisLines(dyn, s.losByPair, LOS_STYLE, pxScale);
        }

        const bm = s.beams;
        if (bm && Array.isArray(bm.t) && bm.t.length > 0) {
            const beamColor = parseColor(BEAM_COLOR);
            for (let i = 0; i < bm.t.length; i++) {
                if (Math.abs(bm.t[i] - tMs) > BEAM_FLASH_MS) continue;
                dyn.lines.push({
                    sx: bm.sx[i], sy: bm.sy[i], sz: bm.sz[i],
                    ex: bm.ex[i], ey: bm.ey[i], ez: bm.ez[i],
                    halfWidth: 0.75 * pxScale,
                    color: beamColor,
                });
            }
        }

        // The actor pass: items + players (or the learn-mode entity view),
        // plus the fading death/drop markers.
        this._glActors(dyn, pxScale);
        return dyn;
    }

    // ─── GL actor pass ──────────────────────────────────────────────────────
    //
    // The z-sorted items-and-players composition, the death/drop markers,
    // the loc labels and the learn-mode entity view as GL data. Text comes
    // from the label atlas (whole strings baked once, drawn as billboards);
    // shapes are point sprites; arrowheads are screen-space triangles. The
    // ordered batch list preserves painter order across primitive types, so
    // a nearer player's circle still covers a farther player's letter.

    _atlas() {
        if (!this._labelAtlas) {
            this._labelAtlas = new GlAtlas(this.state.canvas.ownerDocument);
        }
        return this._labelAtlas;
    }

    // _batcher: an ordered command list that merges consecutive same-type
    // primitives into one draw.
    _batcher(batches) {
        return (type, item) => {
            const last = batches[batches.length - 1];
            if (last && last.type === type) last.items.push(item);
            else batches.push({ type, items: [item] });
        };
    }

    // _glLabelSprite: a centred label billboard at a world anchor.
    _glLabelSprite(atlas, text, font, color, x, y, z, dxPx = 0, dyPx = 0, tint = null, stroke = null) {
        const e = atlas.get(text, font, color, stroke);
        if (!e) return null;
        return {
            x, y, z, e, tint,
            offX: -e.w / 2 + dxPx,
            offY: -e.midY + dyPx,
            w: e.w, h: e.h,
        };
    }

    // _glActors fills dyn with everything the 2D actor layer drew:
    // items + players (z-sorted, mixed), stems, arrows, badges, the fading
    // death/drop markers, and — in learn mode — the entity study view.
    _glActors(dyn, pxScale) {
        const s = this.state;
        const atlas = this._atlas();
        const time = s.currentTime;
        const playerData = s._framePlayerData;
        const push = this._batcher(dyn.actorBatches);
        const iconScale = this.iconScale();

        if (s.learnMode) {
            this._glMapEntities(dyn, atlas, push, pxScale, iconScale);
            return;
        }

        const zRange = s.zRange || { lo: 0, hi: 0 };
        const zSpan = is3D(this.camera) ? 0 : (zRange.hi - zRange.lo);
        const tilted = is3D(this.camera);

        // Same drawable list + sort key as the 2D pass.
        const drawables = [];
        if (s.items && s.items.length > 0) {
            for (const item of s.items) {
                const style = ITEM_MARKER_STYLES[item.kind];
                if (!style) continue;
                const pos = this.toCanvasNew(item.x, item.y, item.z);
                drawables.push({
                    kind: 'i',
                    sortDepth: pos.depth - ITEM_Z_TOP_THRESHOLD * this.camera.sinPitch,
                    item, style,
                });
            }
        }
        if (playerData) {
            for (const [name, data] of Object.entries(playerData)) {
                if (data.x === 0 && data.y === 0) continue;
                const symbolInfo = s.playerSymbols[name];
                if (!symbolInfo) continue;
                const pos = this.toCanvasNew(data.x, data.y, data.z);
                drawables.push({ kind: 'p', sortDepth: pos.depth, name, data, symbolInfo });
            }
        }
        drawables.sort((a, b) => a.sortDepth - b.sortDepth);

        const itemSize = ITEM_MARKER_SIZE * iconScale * pxScale;
        const itemFontPx = Math.round(10 * iconScale * pxScale);
        const itemFont = `bold ${itemFontPx}px -apple-system, BlinkMacSystemFont, sans-serif`;
        const DIM = [ITEM_DIM_ALPHA, ITEM_DIM_ALPHA, ITEM_DIM_ALPHA, ITEM_DIM_ALPHA];

        for (const d of drawables) {
            if (d.kind === 'i') {
                this._glItemMarker(push, atlas, d.item, d.style, time,
                                   itemSize, itemFont, DIM, pxScale);
            } else {
                if (tilted) this._glPlayerStem(push, d.name, d.data, d.symbolInfo, pxScale);
                if (s.showViewArrows || s.showVelArrows) {
                    this._glPlayerArrows(dyn, push, d.data, d.symbolInfo, pxScale);
                }
                this._glPlayerSymbol(push, atlas, d.data, d.symbolInfo,
                                     iconScale, zRange, zSpan, pxScale);
            }
        }

        // Recent-death and backpack-drop markers, fading over
        // DEATH_X_DURATION — on top of the actors like the 2D pass.
        const deaths = s.deathEvents;
        if (deaths && deaths.length > 0) {
            for (const e of deaths) {
                const dt = time - e.t;
                if (dt < 0 || dt > DEATH_X_DURATION) continue;
                const alpha = 1 - dt / DEATH_X_DURATION;
                const [r, g, b] = parseColor(this.teamColors[e.teamIdx] || '#ff5050');
                push('points', {
                    x: e.wx, y: e.wy, z: e.wz,
                    size: 16 * pxScale, shape: 1,
                    color: [r * alpha, g * alpha, b * alpha, alpha],
                });
            }
        }
        const drops = s.dropEvents;
        if (drops && drops.length > 0) {
            const dropFont = `bold ${Math.round(28 * pxScale)}px sans-serif`;
            for (const e of drops) {
                const dt = time - e.t;
                if (dt < 0 || dt > DEATH_X_DURATION) continue;
                const alpha = 1 - dt / DEATH_X_DURATION;
                const fill = e.weapon === 'rl' ? 'rgb(255, 107, 107)'
                    : e.weapon === 'lg' ? 'rgb(0, 217, 255)' : '#ffffff';
                const sp = this._glLabelSprite(atlas, 'D', dropFont, fill,
                    e.wx, e.wy, e.wz, 0, 0, [alpha, alpha, alpha, alpha], '#000000');
                if (sp) push('sprites', sp);
            }
        }
    }

    _glItemMarker(push, atlas, item, style, time, size, font, DIM, pxScale) {
        const up = this.isItemUp(item, time);
        const tint = up ? null : DIM;
        const mul = (c) => {
            if (up) return c;
            return [c[0] * ITEM_DIM_ALPHA, c[1] * ITEM_DIM_ALPHA,
                    c[2] * ITEM_DIM_ALPHA, c[3] * ITEM_DIM_ALPHA];
        };
        push('points', {
            x: item.x, y: item.y, z: item.z,
            size, shape: 3, color: mul(parseColor(style.fill)),
        });
        if (style.outline) {
            push('points', {
                x: item.x, y: item.y, z: item.z,
                size, shape: 4, param: (3 * pxScale) / size,
                color: mul(parseColor(style.outline)),
            });
        }
        if (style.label) {
            const sp = this._glLabelSprite(atlas, style.label, font,
                style.textColor || style.outline || '#fff',
                item.x, item.y, item.z, 0, 1, tint);
            if (sp) push('sprites', sp);
        }
    }

    _glPlayerSymbol(push, atlas, data, symbolInfo, iconScale, zRange, zSpan, pxScale) {
        // Per-player z-based size scale, identical to the 2D pass.
        let zScale = 1;
        if (zSpan > 0) {
            let t = ((data.z || 0) - zRange.lo) / zSpan;
            if (t < 0) t = 0;
            if (t > 1) t = 1;
            zScale = 1 + 0.25 * t;
        }
        const k = iconScale * zScale;
        const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
        const teamCol = parseColor(teamHex);
        // Circle: team ring (radius 13k, stroke 2k) over a dark disc.
        push('points', {
            x: data.x, y: data.y, z: data.z,
            size: 28 * k * pxScale, shape: 0, color: teamCol,
        });
        push('points', {
            x: data.x, y: data.y, z: data.z,
            size: 24 * k * pxScale, shape: 0, color: parseColor('#0a0a15'),
        });
        const letterFont = `bold ${Math.round(16 * k * pxScale)}px monospace`;
        const sp = this._glLabelSprite(atlas, symbolInfo.symbol, letterFont, teamHex,
            data.x, data.y, data.z);
        if (sp) push('sprites', sp);

        const badges = this.getActiveBadges(data);
        if (badges.length > 0) {
            const orbitR = 14 * k * pxScale;
            const badgeR = 5 * k * pxScale;
            const badgeFont = `bold ${Math.round(badgeR * 1.2)}px monospace`;
            for (const b of badges) {
                const rad = (b.angle - 90) * Math.PI / 180;
                const bx = orbitR * Math.cos(rad);
                const by = orbitR * Math.sin(rad);
                push('points', {
                    x: data.x, y: data.y, z: data.z,
                    size: badgeR * 2, shape: 0,
                    color: parseColor(b.color || '#b4b4b4'),
                    offX: bx, offY: by,
                });
                const bsp = this._glLabelSprite(atlas, b.letter, badgeFont, '#000000',
                    data.x, data.y, data.z, bx, by);
                if (bsp) push('sprites', bsp);
            }
        }
    }

    _glPlayerStem(push, name, data, symbolInfo, pxScale) {
        const z = data.z || 0;
        let bottomZ;
        if (typeof data.fh === 'number') {
            bottomZ = z - PLAYER_ORIGIN_ABOVE_FLOOR - data.fh;
        } else {
            const floorZ = this.playerFloorZ(name, data.x, data.y, z);
            bottomZ = floorZ !== null ? floorZ : z - PLAYER_ORIGIN_ABOVE_FLOOR;
        }
        const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
        push('lines', {
            sx: data.x, sy: data.y, sz: z,
            ex: data.x, ey: data.y, ez: bottomZ,
            halfWidth: 1.5 * pxScale,
            color: parseColor(hexToRgba(teamHex, 0.55)),
        });
        push('points', {
            x: data.x, y: data.y, z: bottomZ,
            size: 5 * pxScale, shape: 0,
            color: parseColor(hexToRgba(teamHex, 0.7)),
        });
    }

    _glPlayerArrows(dyn, push, data, symbolInfo, pxScale) {
        const s = this.state;
        const ox = data.x, oy = data.y, oz = data.z;
        if (s.showVelArrows && typeof data.vx === 'number') {
            const speed = Math.hypot(data.vx, data.vy, data.vz);
            if (speed > VEL_ARROW_MIN_SPEED) {
                const sc = 1 / VEL_UNITS_PER_MAP_UNIT;
                const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
                this._glWorldArrow(push, ox, oy, oz,
                    data.vx * sc, data.vy * sc, data.vz * sc,
                    parseColor(hexToRgba(teamHex, 0.9)), 1.75 * pxScale, pxScale);
            }
        }
        if (s.showViewArrows && typeof data.vya === 'number') {
            const yaw = data.vya * ANGLE16_TO_RAD;
            const pitch = (data.vp || 0) * ANGLE16_TO_RAD;
            const cp = Math.cos(pitch);
            this._glWorldArrow(push, ox, oy, oz,
                cp * Math.cos(yaw) * VIEW_ARROW_LEN,
                cp * Math.sin(yaw) * VIEW_ARROW_LEN,
                -Math.sin(pitch) * VIEW_ARROW_LEN,
                parseColor(VIEW_ARROW_COLOR), 1.5 * pxScale, pxScale);
        }
    }

    // _glWorldArrow: shaft as a world-space line, arrowhead as a
    // screen-space triangle at the projected tip (matching drawWorldArrow).
    _glWorldArrow(push, ox, oy, oz, dx, dy, dz, color, halfWidth, pxScale) {
        const a = this.toCanvasNew(ox, oy, oz);
        const b = this.toCanvasNew(ox + dx, oy + dy, oz + dz);
        const sx = (b.x - a.x), sy = (b.y - a.y);
        const slen = Math.hypot(sx, sy);
        if (slen < 1 / pxScale) return;
        push('lines', {
            sx: ox, sy: oy, sz: oz,
            ex: ox + dx, ey: oy + dy, ez: oz + dz,
            halfWidth, color,
        });
        const ux = sx / slen, uy = sy / slen;
        const hl = ARROWHEAD_PX, hw = ARROWHEAD_PX * 0.6;
        const bx = b.x * pxScale, by = b.y * pxScale;
        push('tris', {
            pts: [
                bx, by,
                (b.x - ux * hl - uy * hw) * pxScale, (b.y - uy * hl + ux * hw) * pxScale,
                (b.x - ux * hl + uy * hw) * pxScale, (b.y - uy * hl - ux * hw) * pxScale,
            ],
            color,
        });
    }

    // _glMapEntities: the learn-mode entity study view — teleport link
    // arrows under depth-ordered markers, like drawMapEntities.
    _glMapEntities(dyn, atlas, push, pxScale, iconScale) {
        const s = this.state;
        const entities = s.mapEntities;
        if (!entities || entities.length === 0) return;
        const f = s.entityFilters;
        const size = ITEM_MARKER_SIZE * iconScale * pxScale;
        const fontPx = Math.round(10 * iconScale * pxScale);
        const font = `bold ${fontPx}px -apple-system, BlinkMacSystemFont, sans-serif`;

        if (f.teleporter && s.teleportArrows.length > 0) {
            const col = parseColor(hexToRgba(TELEPORT_COLOR, 0.55));
            const halfWidth = Math.max(0.5, 0.75 * iconScale) * pxScale;
            for (const a of s.teleportArrows) {
                this._glWorldArrow(push, a.sx, a.sy, a.sz,
                    a.dx - a.sx, a.dy - a.sy, a.dz - a.sz, col, halfWidth, pxScale);
            }
        }

        const sorted = entities
            .map(e => ({ e, depth: this.toCanvasNew(e.x, e.y, e.z).depth }))
            .sort((a, b) => a.depth - b.depth);
        for (const { e } of sorted) {
            if (!f[this.entityCategory(e)]) continue;
            const style = e.type === 'item' ? LEARN_ITEM_STYLES[e.kind] : STRUCTURAL_STYLES[e.type];
            if (!style) continue;
            if (style.circle) {
                push('points', {
                    x: e.x, y: e.y, z: e.z,
                    size, shape: 0, color: parseColor(style.fill),
                });
                if (style.outline) {
                    push('points', {
                        x: e.x, y: e.y, z: e.z,
                        size, shape: 2, param: 1 - (3 * pxScale) / size,
                        color: parseColor(style.outline),
                    });
                }
            } else {
                push('points', {
                    x: e.x, y: e.y, z: e.z,
                    size, shape: 3, color: parseColor(style.fill),
                });
                if (style.outline) {
                    push('points', {
                        x: e.x, y: e.y, z: e.z,
                        size, shape: 4, param: (3 * pxScale) / size,
                        color: parseColor(style.outline),
                    });
                }
            }
            if (style.label) {
                const sp = this._glLabelSprite(atlas, style.label, font,
                    style.textColor || style.outline || '#fff', e.x, e.y, e.z, 0, 1);
                if (sp) push('sprites', sp);
            }
        }
    }

    // _collectTrails: the trail window as GL line segments and marker
    // sprites — the same windowing, death/spawn gaps and teleport-dash rules
    // as the 2D drawTracks, expressed as data. Segments are world-space, so
    // trails will foreshorten correctly under any future camera.
    _collectTrails(dyn, pxScale) {
        const s = this.state;
        const time = s.currentTime;
        const trailDuration = s.trailDuration;

        for (const [name, points] of Object.entries(s.fullTrails)) {
            if (!s.enabledPlayers[name]) continue;
            if (points.length < 2) continue;

            // If current time is before trail start, pull start back so the
            // trail grows from here (same rule as the 2D path).
            if (time < (s.trailStartTimes[name] || 0)) {
                s.trailStartTimes[name] = time;
            }

            const endIdx = trailIndexAtTime(points, time);
            if (endIdx < 1) continue;
            const trailStart = Math.max(time - trailDuration, s.trailStartTimes[name] || 0);
            let startIdx = trailIndexAtTime(points, trailStart);
            if (startIdx < 0) startIdx = 0;
            if (endIdx - startIdx < 1) continue;

            const teamHex = this.teamColors[points[0].teamIdx] || this.teamColors[0];
            const solid = parseColor(hexToRgba(teamHex, 0.4));
            const dashCol = parseColor(hexToRgba(teamHex, 0.2));
            const markCol = parseColor(hexToRgba(teamHex, 0.8));
            const halfWidth = 1.5 * pxScale;              // lineWidth 3
            const dash = [4 * pxScale, 6 * pxScale];
            const spawnDot = { size: 6 * pxScale, shape: 0 };
            const deathX = { size: 12 * pxScale, shape: 1 };

            let afterDeath = false;
            let prev = points[startIdx];
            const mark = (pt, kind) => dyn.points.push({
                x: pt.wx, y: pt.wy, z: pt.wz,
                size: kind.size, shape: kind.shape, color: markCol,
            });
            if (prev.spawn) mark(prev, spawnDot);

            for (let i = startIdx + 1; i <= endIdx; i++) {
                const pt = points[i];
                if (pt.spawn) {
                    // Spawn: new segment start (gap from the death before it).
                    afterDeath = false;
                    mark(pt, spawnDot);
                    prev = pt;
                    continue;
                }
                if (pt.death) {
                    dyn.lines.push({
                        sx: prev.wx, sy: prev.wy, sz: prev.wz,
                        ex: pt.wx, ey: pt.wy, ez: pt.wz,
                        halfWidth, color: solid,
                    });
                    mark(pt, deathX);
                    afterDeath = true;
                    prev = pt;
                    continue;
                }
                if (afterDeath) {
                    // Between death and spawn — don't draw.
                    prev = pt;
                    continue;
                }
                dyn.lines.push({
                    sx: prev.wx, sy: prev.wy, sz: prev.wz,
                    ex: pt.wx, ey: pt.wy, ez: pt.wz,
                    halfWidth,
                    color: pt.tp ? dashCol : solid,
                    dash: pt.tp ? dash : null,
                });
                prev = pt;
            }
        }
    }

    // _collectVisLines: the LOS/PVS sightlines as GL segments — endpoints at
    // eye height, coloured mutual/one-way exactly like the 2D drawVisLines.
    _collectVisLines(dyn, byPair, style, pxScale) {
        const playerData = this.state._framePlayerData;
        if (!byPair || !playerData) return;
        const tMs = this.state.currentTime * 1000;
        const names = Object.keys(playerData);
        const halfWidth = (style.width / 2) * pxScale;
        for (let i = 0; i < names.length; i++) {
            const a = names[i], pa = playerData[a];
            if (!pa || typeof pa.x !== 'number') continue;
            for (let j = i + 1; j < names.length; j++) {
                const b = names[j], pb = playerData[b];
                if (!pb || typeof pb.x !== 'number') continue;
                const aSeesB = losCovers(byPair[a] && byPair[a][b], tMs);
                const bSeesA = losCovers(byPair[b] && byPair[b][a], tMs);
                if (!aSeesB && !bSeesA) continue;
                dyn.lines.push({
                    sx: pa.x, sy: pa.y, sz: pa.z + 22,
                    ex: pb.x, ey: pb.y, ez: pb.z + 22,
                    halfWidth,
                    color: parseColor((aSeesB && bSeesA) ? style.mutual
                        : aSeesB ? style.first : style.second),
                });
            }
        }
    }

    // drawWorld: the whole scene, through the WebGL backend — floors (or
    // the loc-blob fills on maps with no triangle geometry), liquids,
    // movers, overlays, weapon fire, labels and actors. Returns false when
    // WebGL is unavailable, in which case the caller shows the notice.
    drawWorld(ctx) {
        const s = this.state;
        const groups = s.locationGroups || [];
        const backdropTris = s.mapGeometry && s.mapGeometry.backdropTris;
        if (groups.length === 0 && (!backdropTris || backdropTris.length < 9)) return true;
        return this.drawWorldGL(ctx, this.floorModel());
    }

    // ─── Actors: players, items, entities ───────────────────────────────────






    // buildTeleportArrows pairs each entrance (teleportSrc.target) with its exit
    // (teleportDst.targetName), storing world-coord endpoints for the arrows.
    buildTeleportArrows() {
        this.state.teleportArrows = [];
        const dstByName = {};
        for (const e of this.state.mapEntities) {
            if (e.type === 'teleportDst' && e.targetName) dstByName[e.targetName] = e;
        }
        for (const e of this.state.mapEntities) {
            if (e.type !== 'teleportSrc' || !e.target) continue;
            const dst = dstByName[e.target];
            if (!dst) continue;
            this.state.teleportArrows.push({ sx: e.x, sy: e.y, sz: e.z, dx: dst.x, dy: dst.y, dz: dst.z });
        }
    }

    entityCategory(e) {
        if (e.type === 'item') return ITEM_KIND_CATEGORY[e.kind] || 'item';
        if (e.type === 'teleportSrc' || e.type === 'teleportDst') return 'teleporter';
        return e.type; // 'spawn' | 'button' | 'door'
    }



    getActiveBadges(data) {
        const badges = [];
        for (const def of BADGE_DEFS) {
            let active = false, color = def.color, letter = def.letter;
            switch (def.key) {
                case 'q':   active = !!data.q; break;
                case 'rl':  active = !!data.rl; break;
                case 'lg':  active = !!data.lg; break;
                case 'sng':
                    if (data.sng) { active = true; letter = 'N'; }
                    else if (data.ssg) { active = true; letter = 'S'; }
                    break;
                case 'mh':  active = data.h > 100; break;
                case 'arm':
                    if (data.at) {
                        active = true;
                        color = ARMOR_COLORS[data.at] || 'rgb(180, 180, 180)';
                        letter = data.at.toUpperCase();
                    }
                    break;
                case 'pe':  active = !!data.pe; break;
                case 'r':   active = !!data.r; break;
            }
            if (active) badges.push({ angle: def.angle, letter, color });
        }
        return badges;
    }

    // isItemUp returns true if the item is available to be picked up at the
    // given time — i.e., we're inside an "available" phase. Handles the MH
    // pending-respawn case (phase with TakenAt set but RespawnAt==0 is
    // still held).
    //
    // `time` is in seconds (mapState.currentTime). Schema v8: item.phases[]
    // .availableFrom / .takenAt / .respawnAt are int32 ms — promote `time`
    // to ms once here so all comparisons happen in the phase's native unit.
    isItemUp(item, time) {
        const phases = item.phases;
        if (!phases || phases.length === 0) return true;
        const timeMs = time * 1000;
        for (let i = 0; i < phases.length; i++) {
            const p = phases[i];
            if (p.availableFrom > timeMs) break;
            const takenAt = p.takenAt || 0;
            if (takenAt === 0) return true; // phase open, not yet taken (this phase is current → up)
            if (timeMs < takenAt) return true; // available window
            // taken at takenAt; respawnAt may be 0 (MH pending) or a future/past value
            const respawnAt = p.respawnAt || 0;
            if (respawnAt > 0 && timeMs >= respawnAt) {
                // Respawned; if this is the last phase or the next phase
                // opens at respawnAt, let the loop continue.
                continue;
            }
            return false;
        }
        return true;
    }

    // itemStatus returns a small object describing status at the given time:
    //   { up: bool, secsToRespawn: number|null, pending: bool }
    // secsToRespawn is the wait time in seconds (null when up).
    // pending is true for MH in its rot window (TakenAt set, RespawnAt==0).
    //
    // `time` is in seconds. Schema v8: phase fields are ms — promote `time`
    // to ms for comparisons, then convert the result back to seconds before
    // returning.
    itemStatus(item, time) {
        const phases = item.phases;
        if (!phases || phases.length === 0) {
            return { up: true, secsToRespawn: null, pending: false };
        }
        const timeMs = time * 1000;
        // Find the phase whose window contains `time` (availableFrom <= timeMs < nextAvailableFrom).
        let activePhase = null;
        for (let i = 0; i < phases.length; i++) {
            const p = phases[i];
            const next = phases[i + 1];
            if (p.availableFrom <= timeMs && (!next || next.availableFrom > timeMs)) {
                activePhase = p;
                break;
            }
        }
        if (!activePhase) {
            // Before the first phase opens — treat as up.
            return { up: true, secsToRespawn: null, pending: false };
        }
        const takenAt = activePhase.takenAt || 0;
        if (takenAt === 0 || timeMs < takenAt) {
            return { up: true, secsToRespawn: null, pending: false };
        }
        const respawnAt = activePhase.respawnAt || 0;
        if (respawnAt === 0) {
            // Held with pending respawn (MH during rot).
            return { up: false, secsToRespawn: null, pending: true };
        }
        if (timeMs >= respawnAt) {
            return { up: true, secsToRespawn: null, pending: false };
        }
        return { up: false, secsToRespawn: (respawnAt - timeMs) * 0.001, pending: false };
    }

    // ─── Overlays: trails, sightlines, occupancy, region control ───────────

    // Prefer the server-resolved loc name (3D nearest, matches ezQuake
    // exactly). High-res buckets carry an integer index `li` into
    // state.locTable; older 1s buckets carry the resolved name in
    // `data.location`. Falls back to the 2D nearest-neighbor only when
    // neither field is present (e.g. demos with no .loc file). The 2D
    // fallback is harmless in that case because there is no stacked-loc
    // disambiguation to do without a loc file in the first place.
    resolvePlayerLoc(data) {
        if (data) {
            if (data.li && this.state.locTable) {
                return this.state.locTable[data.li] || '';
            }
            if (data.location) return data.location;
        }
        return findNearestLocation(data ? data.x : 0, data ? data.y : 0, this.state.locations);
    }

    // Compute, per loc-group occupied by at least one living player this
    // bucket, the set of team indices present in it. Keyed by normalized loc
    // name. Uses the server-resolved 3D-nearest loc (matches ezQuake) via
    // resolvePlayerLoc; each player's team index comes from the canonical
    // playerSymbols mapping.
    computeOccupiedGroupTeams(playerData) {
        const occupied = new Map(); // normalized loc name -> Set<teamIdx>
        if (!playerData) return occupied;
        const symbols = this.state.playerSymbols || {};
        for (const [name, data] of Object.entries(playerData)) {
            if (!data) continue;
            if (data.d || (data.h !== undefined && data.h <= 0)) continue;
            if (data.x === 0 && data.y === 0) continue;
            const locName = this.resolvePlayerLoc(data);
            if (!locName) continue;
            const key = normalizeLocationName(locName);
            let teams = occupied.get(key);
            if (!teams) { teams = new Set(); occupied.set(key, teams); }
            teams.add(symbols[name] ? symbols[name].teamIdx : 0);
        }
        return occupied;
    }

    regionActiveTint(teams) {
        if (!teams || teams.size !== 1) return REGION_TINT_CONTESTED;
        const teamIdx = teams.values().next().value;
        return hexToRgba(this.teamColors[teamIdx] || this.teamColors[0], REGION_TINT_ALPHA);
    }

    // computeOccupiedOverlay: the occupied-region highlight as data — per
    // occupied group with geometry: the group, its team tint, and its name
    // (for the outline and bold-label passes). Shared by both backends.
    computeOccupiedOverlay(playerData) {
        const groupsByName = this.state.locationGroupByName;
        if (!groupsByName) return null;
        const occupied = this.computeOccupiedGroupTeams(playerData);
        if (occupied.size === 0) return null;
        const out = { names: [], groups: [] };
        for (const [name, teams] of occupied) {
            const group = groupsByName[name];
            if (!group) continue;
            out.names.push(name);
            if (group.tris && group.tris.length >= 9) {
                out.groups.push({ group, tint: this.regionActiveTint(teams) });
            }
        }
        return out;
    }



    // computeControlFills: the region-control overlay as data — one entry
    // per group with geometry, in draw order. Shared by both backends.
    computeControlFills(controlStates) {
        const regions = this.state.controlRegions;
        if (!regions || !controlStates) return null;

        // Build the set of regions that are occupied (any non-empty state).
        // Used by the stacking rule: a region is boosted when no region above it
        // in its stack is currently occupied.
        const occupied = new Set();
        for (const [name, state] of Object.entries(controlStates)) {
            if (state !== 'empty') occupied.add(name);
        }

        // Index regions by name for the boost lookup.
        const regionByName = {};
        for (const r of regions) regionByName[r.name] = r;

        const fills = [];
        for (const [regionName, state] of Object.entries(controlStates)) {
            const groups = this.state.regionToGroups[regionName];
            if (!groups || groups.length === 0) continue;

            let baseAlpha, hex;
            switch (state) {
                case 'teamAControl':     baseAlpha = 0.24; hex = this.teamColors[0]; break;
                case 'teamAWeakControl': baseAlpha = 0.14; hex = this.teamColors[0]; break;
                case 'teamBControl':     baseAlpha = 0.24; hex = this.teamColors[1]; break;
                case 'teamBWeakControl': baseAlpha = 0.14; hex = this.teamColors[1]; break;
                case 'contested':        baseAlpha = 0.14; hex = '#ffffff'; break;
                case 'weakContested':    baseAlpha = 0.07; hex = '#ffffff'; break;
                default: continue; // empty
            }

            const r = regionByName[regionName];
            let boost = 1.0;
            if (r && r._above && r._above.length > 0) {
                const anyAboveOccupied = r._above.some(ra => occupied.has(ra.name));
                if (!anyAboveOccupied) boost = REGION_OPACITY_BOOST;
            }
            const finalAlpha = Math.min(0.5, baseAlpha * boost);
            const color = hexToRgba(hex, finalAlpha);

            for (const group of groups) {
                if (!group.tris || group.tris.length < 9) continue;
                // Region focus: tint outside the focus neighborhood fades with
                // the base fills so the focused area keeps visual priority.
                const tint = this.focusTier(group.name) === 'far'
                    ? scaleRgbaAlpha(color, 0.3)
                    : color;
                fills.push({ group, tint });
            }
        }
        return fills;
    }





    // ─── Hit testing ────────────────────────────────────────────────────────

    // pickLocGroupAt: which named loc region is under the canvas point. Tests
    // every projected floor triangle; among hits the one nearest the camera
    // wins, so clicking a spot where two floors stack resolves to the visible
    // (upper, under the current rotation) one. One-off per click — no caching.
    pickLocGroupAt(cx, cy) {
        let bestName = null;
        let bestDepth = -Infinity;
        for (const group of this.state.locationGroups || []) {
            const tris = group.tris;
            if (!tris || tris.length < 9) continue;
            for (let i = 0; i + 8 < tris.length; i += 9) {
                const a = this.toCanvasNew(tris[i],     tris[i + 1], tris[i + 2]);
                const b = this.toCanvasNew(tris[i + 3], tris[i + 4], tris[i + 5]);
                const c = this.toCanvasNew(tris[i + 6], tris[i + 7], tris[i + 8]);
                if (!pointInTriangle(cx, cy, a, b, c)) continue;
                const depth = Math.max(a.depth, b.depth, c.depth);
                if (depth > bestDepth) {
                    bestDepth = depth;
                    bestName = group.name;
                }
            }
        }
        return bestName;
    }

    // hitTestPlayerSymbol: the player whose drawn symbol is nearest the canvas
    // point, within the follow hit radius. Reads the same frame the draw pass
    // rendered from; positions are refined through the native-rate streams
    // exactly like the draw pass.
    hitTestPlayerSymbol(cx, cy, time) {
        const frame = this.frameAt(time);
        const bucketPlayers = frame && frame.p;
        if (!bucketPlayers) return null;
        let best = null;
        let bestD2 = FOLLOW_HIT_RADIUS_PX * FOLLOW_HIT_RADIUS_PX;
        for (const [name, data] of Object.entries(bucketPlayers)) {
            // Hit-test against the stream-sourced position the symbol is drawn at.
            const sp = this.streamPosAt(name, time * 1000);
            const x = sp ? sp.x : data.x, y = sp ? sp.y : data.y, z = sp ? sp.z : data.z;
            if (x === 0 && y === 0) continue;
            const pos = this.toCanvas(x, y, z);
            const dx = pos.x - cx;
            const dy = pos.y - cy;
            const d2 = dx * dx + dy * dy;
            if (d2 <= bestD2) {
                bestD2 = d2;
                best = name;
            }
        }
        return best;
    }

    // floorZUnder: highest floor surface at world (x, y) with z <= zMax, or null
    // when no loaded floor triangle covers that point. Scans the named groups +
    // backdrop with a cheap bbox reject; z on the face is interpolated
    // barycentrically so sloped floors anchor correctly. Walls are not floors
    // and are never scanned.
    floorZUnder(x, y, zMax) {
        const geom = this.state.mapGeometry;
        if (!geom) return null;
        let best = null;
        const scan = (tris) => {
            if (!tris) return;
            for (let i = 0; i + 8 < tris.length; i += 9) {
                const ax = tris[i],     ay = tris[i + 1];
                const bx = tris[i + 3], by = tris[i + 4];
                const cx = tris[i + 6], cy = tris[i + 7];
                if (x < ax && x < bx && x < cx) continue;
                if (x > ax && x > bx && x > cx) continue;
                if (y < ay && y < by && y < cy) continue;
                if (y > ay && y > by && y > cy) continue;
                const denom = (by - cy) * (ax - cx) + (cx - bx) * (ay - cy);
                if (denom === 0) continue;
                const w1 = ((by - cy) * (x - cx) + (cx - bx) * (y - cy)) / denom;
                if (w1 < 0 || w1 > 1) continue;
                const w2 = ((cy - ay) * (x - cx) + (ax - cx) * (y - cy)) / denom;
                if (w2 < 0 || w1 + w2 > 1) continue;
                const z = w1 * tris[i + 2] + w2 * tris[i + 5]
                        + (1 - w1 - w2) * tris[i + 8];
                if (z <= zMax && (best === null || z > best)) best = z;
            }
        };
        scan(geom.backdropTris);
        for (const group of this.state.locationGroups || []) scan(group.tris);
        return best;
    }

    // playerFloorZ: memoised floorZUnder per player — a paused timeline or a
    // rotation drag re-renders with unchanged positions, so the triangle scan
    // runs only when the player actually moved.
    playerFloorZ(name, x, y, z) {
        let cache = this.state._floorZCache;
        if (!cache) cache = this.state._floorZCache = new Map();
        const e = cache.get(name);
        if (e && e.x === x && e.y === y && e.z === z) return e.floorZ;
        const floorZ = this.floorZUnder(x, y, z - PLAYER_ORIGIN_ABOVE_FLOOR + FLOOR_SNAP_TOLERANCE);
        cache.set(name, { x, y, z, floorZ });
        return floorZ;
    }

    // streamPosAt returns {x, y, z, h} from a player's native-rate position
    // track at tMs (match-relative ms): the last sample at or before tMs,
    // clamped to the first. h is the PositionTrack.H floor-height (or null when
    // the column is absent / NoFloor). Binary search — tracks are dense.
    streamPosAt(name, tMs) {
        const pos = this.state.posStreams && this.state.posStreams[name];
        if (!pos || !pos.t || pos.t.length === 0) return null;
        const t = pos.t;
        const n = t.length;
        // Clamp times before the first sample to it (dense, strictly increasing
        // tracks, so this matches the previous tMs<=t[0] guard exactly).
        let idx = lowerBoundIndex(t, tMs, (a, i) => a[i]);
        if (idx < 0) idx = 0;
        let h = null;
        if (pos.h && pos.h.length === n && pos.h[idx] !== MAP_NO_FLOOR) h = pos.h[idx];
        const out = { x: pos.x[idx], y: pos.y[idx], z: pos.z[idx], h };
        // View direction (raw angle16) and velocity (u/s) ride the same stream
        // when present (schema v31–v32); the map's optional arrows read them.
        if (pos.vya && pos.vya.length === n) {
            out.vya = pos.vya[idx];
            out.vp = (pos.vp && pos.vp.length === n) ? pos.vp[idx] : 0;
        }
        if (pos.vx && pos.vx.length === n) {
            out.vx = pos.vx[idx];
            out.vy = (pos.vy && pos.vy.length === n) ? pos.vy[idx] : 0;
            out.vz = (pos.vz && pos.vz.length === n) ? pos.vz[idx] : 0;
        }
        return out;
    }

    // augmentPlayerData overlays stream-sourced position (x/y/z) and the
    // per-sample floor-height (fh) onto the bucket-reconstructed player map,
    // without mutating the cached bucket. State fields (health/armor/weapons)
    // are carried through from the bucket unchanged. Players with no position
    // stream keep their bucket position.
    augmentPlayerData(bucketPlayers, tMs) {
        if (!bucketPlayers) return bucketPlayers;
        const out = {};
        for (const name in bucketPlayers) {
            const d = bucketPlayers[name];
            const sp = this.streamPosAt(name, tMs);
            if (sp) {
                out[name] = Object.assign({}, d, {
                    x: sp.x, y: sp.y, z: sp.z, fh: sp.h,
                    vya: sp.vya, vp: sp.vp, vx: sp.vx, vy: sp.vy, vz: sp.vz,
                });
            } else {
                out[name] = d;
            }
        }
        return out;
    }

    // ─── Frame composition ──────────────────────────────────────────────────

    // setFrames installs (or clears) the columnar bucket view the frame
    // lookups read from. Whole-view replacement is the contract: a windowed
    // source hands in a view covering what it has, and the component treats
    // "no frames yet" as a first-class state rather than an error.
    setFrames(view) {
        const s = this.state;
        s.frames = view || null;
        s._frameRecon = null;
        s.lastRenderedBucket = null;
        s.renderDirty = true;
    }

    // frameAt: the reconstructed row-shape frame ({t, idx, p, td}) whose
    // bucket span contains `time` (seconds), or null before frames arrive.
    // Memoised on (view, index): repeated calls at the same time return the
    // same object, so render's `bucket === lastRenderedBucket` redraw-skip
    // and the host's legend/region/hit-test callers all share one
    // reconstruction per frame.
    frameAt(time) {
        const s = this.state;
        const view = s.frames;
        if (!view || !view.count) return null;
        const i = bucketIndexAtTime(view, time);
        if (i < 0) return null;
        const c = s._frameRecon;
        if (c && c.view === view && c.i === i) return c.frame;
        const frame = {
            t: bucketTimeSec(view, i),
            idx: i,
            p: reconstructBucketPlayers(view, i),
            td: reconstructBucketTeams(view, i),
        };
        s._frameRecon = { view, i, frame };
        return frame;
    }

    // regionControlAt: region name → control state at `time`, decoded from
    // the per-region bucket-state strings (which share the frame grid), or
    // null when region control is absent or the frames haven't arrived.
    regionControlAt(time) {
        const s = this.state;
        if (!s.controlRegions || !s.bucketStates) return null;
        const idx = bucketIndexAtTime(s.frames, time);
        if (idx < 0) return null;
        const states = {};
        for (const region of s.controlRegions) {
            const str = s.bucketStates[region.name];
            if (typeof str !== 'string' || idx >= str.length) continue;
            states[region.name] = decodeRegionStateChar(str[idx]);
        }
        return states;
    }

    // setGeometry installs (or re-installs, after an edit / regeneration) a
    // geometry object as the map's floor/wall source. Normalizes legacy
    // versions, splits out the unnamed backdrop, indexes the submodel meshes
    // for the mover renderer, drops every geometry-derived cache, and
    // rebuilds the loc groups so their tris attach to the new geometry.
    setGeometry(geom) {
        const s = this.state;
        normalizeMapGeometry(geom);
        // The unnamed backdrop bucket (name === "") is drawn as a neutral
        // underlay by the world layer; cache its triangle list separately so
        // it isn't confused with loc groups keyed by name.
        const backdrop = geom.locs.find(l => l && l.name === '');
        geom.backdropTris = backdrop && Array.isArray(backdrop.tris) && backdrop.tris.length >= 9
            ? backdrop.tris : null;
        s.mapGeometry = geom;
        // Submodel meshes (corpus v4) keyed by id for the mover renderer.
        s.submodelMeshes = null;
        if (Array.isArray(geom.submodels)) {
            const sm = {};
            for (const sub of geom.submodels) {
                if (sub && Number.isInteger(sub.id) && Array.isArray(sub.tris) && sub.tris.length >= 9) {
                    sm[sub.id] = sub.tris;
                }
            }
            if (Object.keys(sm).length > 0) s.submodelMeshes = sm;
        }
        // Geometry-derived caches are stale now.
        s._floorZCache = null;
        s._floorModel = null;
        s._floorCanvasKey = null;
        s._moverFaces = null;
        this.rebuildLocationGroups();
    }

    // rebuildTrails derives the per-player trail tracks (world-space points
    // with death/spawn/teleport marks) and the standalone death-marker list
    // from the installed frame view. Trails all start disabled — the host's
    // trail controls enable them.
    rebuildTrails() {
        const s = this.state;
        s.fullTrails = {};
        // Sorted-by-time list of death frames in world space, used by render
        // to draw a fading "X" at the death location for a couple of seconds.
        s.deathEvents = [];
        const view = s.frames;
        if (!view || !view.players) return;

        // Teleport threshold: no player covers more than 2500 u/s of ground
        // legitimately, so a bigger per-bucket step is a teleporter (or a
        // respawn, which the death/spawn marks already break the trail on).
        const MAX_MOVE_PER_BUCKET = 2500 * ((view.windowMs || 50) / 1000);
        // "Meaningful movement" threshold — 2 canvas pixels at the base
        // fit-to-canvas scale, translated to world units so the filter is
        // applied in world space.
        const MIN_MOVE_WORLD = this.camera.scale > 0 ? (2 / this.camera.scale) : 0;

        // Walk each player's dense columns over their active span. Dead
        // buckets (alive=0) are skipped, which breaks the trail across
        // death→respawn just as the old row shape did by omitting the player
        // from those buckets.
        for (const name in view.players) {
            const cp = view.players[name];
            const symbolInfo = s.playerSymbols[name];
            if (!symbolInfo) continue;
            const xs = cp.x, ys = cp.y, zs = cp.z, ds = cp.d, sps = cp.sp;
            if (!xs || !ys) continue;

            let lastWorld = null;
            for (let rel = 0; rel < cp.n; rel++) {
                if (!cp.alive[rel]) continue;
                const x = xs[rel], y = ys[rel];
                const z = zs ? zs[rel] : 0;
                if (x === 0 && y === 0) continue;

                const i = cp.first + rel;
                const t = bucketTimeSec(view, i);
                const isDeath = ds ? !!ds[rel] : false;
                const isSpawn = sps ? !!sps[rel] : false;

                if (!s.fullTrails[name]) s.fullTrails[name] = [];
                const track = s.fullTrails[name];
                const last = track[track.length - 1];

                // Death frames also get added to the standalone deathEvents
                // list so render can find them without scanning every player
                // trail. teamIdx is captured so the X is painted in the dead
                // player's own team color rather than a generic red.
                if (isDeath) {
                    s.deathEvents.push({ t, wx: x, wy: y, wz: z, teamIdx: symbolInfo.teamIdx });
                }

                // Always include death/spawn markers regardless of distance.
                if (!isDeath && !isSpawn) {
                    if (last && Math.abs(last.wx - x) <= MIN_MOVE_WORLD && Math.abs(last.wy - y) <= MIN_MOVE_WORLD) {
                        lastWorld = { x, y };
                        continue;
                    }
                }

                // Teleport detection in world units (scale-independent)
                const isTeleport = !isDeath && !isSpawn && lastWorld && (Math.abs(x - lastWorld.x) > MAX_MOVE_PER_BUCKET || Math.abs(y - lastWorld.y) > MAX_MOVE_PER_BUCKET);

                lastWorld = { x, y };
                track.push({ wx: x, wy: y, wz: z, t, teamIdx: symbolInfo.teamIdx, tp: isTeleport, death: isDeath, spawn: isSpawn });
            }
        }

        // deathEvents was filled per-player; render expects it time-ordered.
        s.deathEvents.sort((a, b) => a.t - b.t);

        // All players start disabled (the host's All button / per-player
        // checkboxes enable them).
        s.enabledPlayers = {};
        s.trailStartTimes = {};
        for (const name of Object.keys(s.fullTrails)) {
            s.enabledPlayers[name] = false;
            s.trailStartTimes[name] = 0;
        }
    }

    // rebuildLocationGroups (re)derives the named loc regions from the loc
    // points plus whatever geometry is loaded, and refreshes the
    // normalized-name lookup the per-frame occupancy highlighting keys into.
    rebuildLocationGroups() {
        const s = this.state;
        const { groups, byName } = processLocationGroups(s.locations, s.mapGeometry);
        s.locationGroups = groups;
        s.locationGroupByName = byName;
        return groups;
    }

    // render composes one frame at `time` (seconds) from the installed frame
    // feed (setFrames). Rendering with no frames yet draws the world with
    // nobody on it — the partially-loaded-timeline state.
    render(time) {
        const s = this.state;
        const ctx = s.ctx;
        const canvas = s.canvas;

        if (!ctx || !canvas) return;

        // The frame's identity doubles as the redraw key (frameAt memoises
        // per bucket index).
        const bucket = this.frameAt(time);

        // Skip redraw if same data bucket and nothing else changed — but NOT
        // during playback: positions come from the native-rate streams, so the
        // map must repaint every frame to animate at native rate (the old
        // bucket-gated repaint froze the view between 50 ms buckets, which read
        // as ~2 fps in slow motion). When paused/idle the skip still elides
        // redundant redraws.
        if (bucket === s.lastRenderedBucket && !s.renderDirty && !s.isPlaying) return;
        s.lastRenderedBucket = bucket;
        s.renderDirty = false;

        // Player positions (and the floor-height fh for the anchor stem) come
        // from the native-rate streams; state badges stay on the bucket. Built
        // once here from a non-mutating overlay on the cached bucket.
        const playerData = bucket ? this.augmentPlayerData(bucket.p, time * 1000) : null;
        // Stash for drawMovers (runs inside the world layer, which has no
        // playerData of its own) to highlight movers a player is riding.
        s._framePlayerData = playerData;

        // Normalize to CSS pixel coordinates. The canvas backing store is sized
        // to cssDims * devicePixelRatio for sharp rendering on HiDPI displays;
        // setTransform(dpr,...) makes every subsequent draw interpret its
        // coordinates in CSS px while rasterising at physical resolution.
        const dpr = s.dpr || 1;
        const cssW = s.canvasCssW || canvas.width;
        const cssH = s.canvasCssH || canvas.height;
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

        // Follow-player: pin the camera on the tracked player this frame by
        // adjusting panX/panY so their symbol lands at canvas center.
        if (s.followPlayer && playerData) {
            const fp = playerData[s.followPlayer];
            if (fp && !(fp.x === 0 && fp.y === 0)) {
                this.camera.panX = 0;
                this.camera.panY = 0;
                const pos = this.toCanvas(fp.x, fp.y, fp.z);
                this.camera.panX = cssW / 2 - pos.x;
                this.camera.panY = cssH / 2 - pos.y;
            }
        }

        // Clear
        ctx.fillStyle = '#0a0a15';
        ctx.fillRect(0, 0, cssW, cssH);

        // Process location groups once (cached on the state)
        if (!s.locationGroups && s.locations.length > 0) {
            this.rebuildLocationGroups();
        }

        // Draw the location underlay (backdrop + per-loc regions + outlines +
        // labels). Fresh each frame so it follows pan / zoom precisely and stays
        // crisp at any zoom level.
        // drawWorld renders the entire scene through the WebGL backend —
        // world, overlays, trails, actors, labels. False means WebGL is
        // unavailable (no context, or lost): show the notice instead of a
        // silently empty map.
        if (!this.drawWorld(ctx)) {
            ctx.fillStyle = '#8892b0';
            ctx.font = '14px monospace';
            ctx.textAlign = 'center';
            ctx.textBaseline = 'middle';
            ctx.fillText('WebGL2 is required for the map view', cssW / 2, cssH / 2);
        }
    }

    // ─── Events ─────────────────────────────────────────────────────────────

    // on registers a host listener. Two events today: 'follow' (the followed
    // player changed — null when cleared) and 'camera' (the orbit angles
    // changed). The host mirrors these into its own chrome (a select box, a
    // 3D-toggle highlight); the component never reaches out to find one.
    on(name, fn) {
        (this._listeners[name] ||= []).push(fn);
        return this;
    }

    _emit(name, arg) {
        const l = this._listeners[name];
        if (l) for (const fn of l) fn(arg);
    }

    // ─── Camera, focus, follow ──────────────────────────────────────────────

    // setCamera: normalize + snap + clamp the orbit angles, notify the host,
    // and redraw. The single entry point for every rotation source (button,
    // drag).
    setCamera(yaw, pitch) {
        setAngles(this.camera, yaw, pitch);
        this._emit('camera', { yaw: this.camera.yaw, pitch: this.camera.pitch });
        this.state.renderDirty = true;
        this.render(this.state.currentTime);
    }

    // setFocusGroup: focus a named loc region (or clear with null). The
    // region and its XY-neighbors render solid and saturated while everything
    // else fades, so a zoomed-in fight area stays readable. Focus also
    // becomes the orbit-drag pivot (currentOrbitPivot).
    setFocusGroup(name) {
        const s = this.state;
        s.focusGroupName = null;
        s.focusNeighbors = null;
        const focus = name ? s.locationGroupByName?.[name] : null;
        if (focus && focus.tris && focus.tris.length >= 9) {
            s.focusGroupName = name;
            const fb = groupWorldBBox(focus);
            const nb = new Set();
            for (const g of s.locationGroups || []) {
                if (g === focus || !g.tris || g.tris.length < 9) continue;
                const gb = groupWorldBBox(g);
                const gapX = Math.max(gb.minX - fb.maxX, fb.minX - gb.maxX);
                const gapY = Math.max(gb.minY - fb.maxY, fb.minY - gb.maxY);
                if (Math.max(gapX, gapY) <= FOCUS_NEIGHBOR_MARGIN) nb.add(g.name);
            }
            s.focusNeighbors = nb;
        }
        s.renderDirty = true;
        this.render(s.currentTime);
    }

    setFollowPlayer(name) {
        const s = this.state;
        s.followPlayer = name || null;
        if (s.followPlayer) {
            // Entering follow mode clears any previous manual pan so the
            // camera lock is relative to a fit-to-canvas baseline. Zoom is
            // preserved.
            this.camera.panX = 0;
            this.camera.panY = 0;
        }
        this._emit('follow', s.followPlayer);
        s.renderDirty = true;
        this.render(s.currentTime);
    }

    // currentOrbitPivot: where an orbit drag should pivot. Follow mode pivots
    // on the tracked player; a focused region pivots on its centroid (at its
    // real floor height, so pitch changes don't swing a high floor across the
    // screen); otherwise the world point currently at canvas center — so
    // "pan/zoom to a place, then rotate" orbits where you're looking.
    currentOrbitPivot() {
        const s = this.state;
        const w = this.camera;
        if (s.followPlayer) {
            // Match the stream-sourced symbol position so the orbit pivots
            // exactly on the drawn symbol.
            const sp = this.streamPosAt(s.followPlayer, s.currentTime * 1000);
            if (sp && !(sp.x === 0 && sp.y === 0)) {
                return { x: sp.x, y: sp.y, z: sp.z || 0 };
            }
            const frame = this.frameAt(s.currentTime);
            const fp = frame && frame.p ? frame.p[s.followPlayer] : null;
            if (fp && !(fp.x === 0 && fp.y === 0)) {
                return { x: fp.x, y: fp.y, z: fp.z || 0 };
            }
        }
        if (s.focusGroupName && s.locationGroupByName) {
            const g = s.locationGroupByName[s.focusGroupName];
            if (g) return { x: g.centroid.x, y: g.centroid.y, z: g.centroid.z };
        }
        const c = toWorld(w, (s.canvasCssW || 0) / 2, (s.canvasCssH || 0) / 2);
        return { x: c.x, y: c.y, z: w.zMid };
    }

    // resetView: back to the fit-to-canvas baseline — pan/zoom cleared,
    // follow and focus dropped, orbit pivot restored to the map center /
    // mid height, default isometric angles.
    resetView() {
        const s = this.state;
        const w = this.camera;
        w.panX = 0;
        w.panY = 0;
        w.zoomK = 1;
        if (s.followPlayer) {
            s.followPlayer = null;
            this._emit('follow', null);
        }
        if (s.focusGroupName) this.setFocusGroup(null);
        // Restore the default orbit pivot (map center / mid height) — orbit
        // drags may have re-centered it.
        this.refit();
        w.zMid = w.zMidDefault || 0;
        // Back to the default isometric view (also notifies the host and
        // redraws).
        this.setCamera(DEFAULT_YAW, DEFAULT_PITCH);
    }

    // ─── Pointer interaction ────────────────────────────────────────────────
    //
    // The host owns the DOM events and forwards them here in canvas-space CSS
    // pixels; the component owns the gesture semantics. Pan: 'pan' drag.
    // Rotate: 'orbit' drag (absolute deltas from the drag-start angles, so
    // the cardinal yaw snap can be dragged through). A press that never
    // exceeds the click threshold dispatches as a click on release: player
    // symbols toggle follow, loc regions toggle focus.

    pointerDown(x, y, mode = 'pan', button = 0) {
        const d = this._drag;
        d.active = true;
        d.button = button;
        d.mode = mode === 'orbit' ? 'orbit' : 'pan';
        d.startX = d.lastX = x;
        d.startY = d.lastY = y;
        d.moved = false;
        if (d.mode === 'orbit') {
            // Re-center the orbit on what the user is looking at (followed
            // player > focused region > view center) and capture the start
            // angles.
            const pv = this.currentOrbitPivot();
            setOrbitCenter(this.camera, pv.x, pv.y, pv.z);
            d.yaw0 = this.camera.yaw;
            d.pitch0 = this.camera.pitch;
        }
    }

    pointerMove(x, y) {
        const d = this._drag;
        if (!d.active) return;
        const s = this.state;
        const dx = x - d.lastX;
        const dy = y - d.lastY;
        d.lastX = x;
        d.lastY = y;
        if (!d.moved) {
            const totalDx = x - d.startX;
            const totalDy = y - d.startY;
            if (Math.abs(totalDx) > CLICK_MAX_MOTION_PX ||
                Math.abs(totalDy) > CLICK_MAX_MOTION_PX) {
                d.moved = true;
                // Starting a pan drops follow-mode so the user isn't fighting
                // the camera. Orbiting keeps it — rotation composes fine with
                // the per-frame follow re-center.
                if (d.mode === 'pan' && s.followPlayer) {
                    s.followPlayer = null;
                    this._emit('follow', null);
                }
                if (s.canvas) s.canvas.style.cursor = 'grabbing';
            }
        }
        if (d.moved) {
            if (d.mode === 'orbit') {
                // Horizontal drag spins, vertical drag tilts (up = tilt
                // further toward horizontal, down = back toward top-down).
                // Absolute from the drag-start angles, not incremental, so
                // the yaw snap in setCamera can't capture the drag.
                this.setCamera(d.yaw0 + (x - d.startX) * ORBIT_YAW_PER_PX,
                               d.pitch0 + (y - d.startY) * ORBIT_PITCH_PER_PX);
            } else {
                this.camera.panX += dx;
                this.camera.panY += dy;
                s.renderDirty = true;
                this.render(s.currentTime);
            }
        }
    }

    pointerUp(x, y) {
        const d = this._drag;
        if (!d.active) return;
        const wasClick = !d.moved && d.button === 0;
        d.active = false;
        d.button = -1;
        if (this.state.canvas) this.state.canvas.style.cursor = '';
        if (wasClick) this._dispatchClick(x, y);
    }

    _dispatchClick(cx, cy) {
        const s = this.state;
        const hit = this.hitTestPlayerSymbol(cx, cy, s.currentTime);
        if (hit) {
            this.setFollowPlayer(s.followPlayer === hit ? null : hit);
            return;
        }
        const region = this.pickLocGroupAt(cx, cy);
        if (region && region !== s.focusGroupName) {
            this.setFocusGroup(region);
        } else if (s.focusGroupName) {
            this.setFocusGroup(null);
        }
    }

    // wheelZoom: exponential zoom anchored in *view space* — the (u, v) under
    // the cursor is found by inverting only the linear screen map (rotation
    // plays no part), so this is exact at any pitch, including a fully
    // horizontal camera where a world-plane inverse would be singular.
    wheelZoom(x, y, deltaY) {
        const w = this.camera;
        const s = this.state;
        const vb = toView(w, x, y);
        let newZoom = w.zoomK * Math.exp(-deltaY * 0.0015);
        if (newZoom < ZOOM_MIN) newZoom = ZOOM_MIN;
        if (newZoom > ZOOM_MAX) newZoom = ZOOM_MAX;
        if (newZoom === w.zoomK) return;
        w.zoomK = newZoom;
        // Re-solve pan so the same (u, v) lands back under the cursor.
        // Follow-mode intentionally survives zoom — render's follow step
        // will re-center on the tracked player using the new zoom level, so
        // zoom becomes "zoom in on the player" rather than dropping follow.
        const sx = w.scale * w.zoomK;
        w.panX = x - w.offsetX - (vb.u - w.minX) * sx;
        w.panY = y - w.canvasH + w.offsetY + (vb.v - w.minY) * sx;
        s.renderDirty = true;
        this.render(s.currentTime);
    }
}
