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

import { newCamera, project, is3D } from './camera.js';
import { moverPoseAt, pointInTriangle } from './geometry.js';
import { buildFloorModel } from './locgroups.js';
import { scaleRgbaAlpha, hexToRgba } from './color.js';
import {
    drawTriangleListFill, drawRegionOutline, fillRegion, renderSolidEntries,
    drawMoverMesh, drawLiquidVolume, drawPlayerSymbolAt, drawBadgesAroundCenter,
    drawWorldArrow, drawArrow, PLAYER_SYMBOL_BASE_SIZE,
} from './draw.js';
import { lowerBoundIndex, trailIndexAtTime } from './util.js';
import { normalizeLocationName, findNearestLocation } from './locs.js';

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
        // either of them up.
        canvas: null,
        ctx: null,

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
        // Scratch projection point, reused by toCanvas. Bound projection
        // callbacks so draw primitives can be handed a plain function.
        this._pt = { x: 0, y: 0, depth: 0 };
        this._toCanvas = (x, y, z) => this.toCanvas(x, y, z);
        this._toCanvasNew = (x, y, z) => this.toCanvasNew(x, y, z);
        if (canvas) this.attach(canvas);
        this.options = opts;
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

    drawMovers(ctx) {
        const s = this.state;
        const movers = s.movers;
        const meshes = s.submodelMeshes;
        if (!movers || movers.length === 0 || !meshes) return;
        const tMs = s.currentTime * 1000;
        const players = this.livingPlayersAtFrame();
        for (const m of movers) {
            const mesh = meshes[m.sub];
            if (!mesh || mesh.length < 9) continue;
            const pose = moverPoseAt(m, tMs);
            if (!pose || !pose.vis) continue;
            const active = players.length > 0 && this.playerOnMover(pose, m.sub, players);
            drawMoverMesh(ctx, mesh, this.moverMeshFaces(m.sub), pose,
                          active ? MOVER_FILL_ACTIVE : MOVER_FILL,
                          this.camera, this._toCanvas);
        }
    }

    // ─── Weapon-fire overlays ───────────────────────────────────────────────

    // drawFlightDots: each flight (rocket/grenade/nail) live at the current
    // time, interpolated along its spawn→despawn segment — linear, so exact
    // for the straight-flying rocket and approximate for grenades and nails.
    drawFlightDots(ctx, pr, radius, colorOf) {
        if (!pr || !Array.isArray(pr.s) || pr.s.length === 0) return;
        const tMs = this.state.currentTime * 1000;
        ctx.save();
        for (let i = 0; i < pr.s.length; i++) {
            const t0 = pr.s[i], t1 = pr.e[i];
            if (tMs < t0 || tMs > t1) continue;
            const f = t1 > t0 ? (tMs - t0) / (t1 - t0) : 0;
            const x = pr.sx[i] + (pr.ex[i] - pr.sx[i]) * f;
            const y = pr.sy[i] + (pr.ey[i] - pr.sy[i]) * f;
            const z = pr.sz[i] + (pr.ez[i] - pr.sz[i]) * f;
            const p = this.toCanvas(x, y, z);
            ctx.fillStyle = colorOf(pr.w[i]);
            ctx.beginPath();
            ctx.arc(p.x, p.y, radius, 0, Math.PI * 2);
            ctx.fill();
        }
        ctx.restore();
    }

    drawProjectiles(ctx) {
        const s = this.state;
        this.drawFlightDots(ctx, s.projectiles, 3, (w) => PROJECTILE_COLORS[w] || '#ffffff');
        this.drawFlightDots(ctx, s.nails, 1.5, () => NAIL_COLOR);
    }

    // drawBeams: each LG bolt active near the current time, as a short-lived
    // line from muzzle to impact.
    drawBeams(ctx) {
        const bm = this.state.beams;
        if (!bm || !Array.isArray(bm.t) || bm.t.length === 0) return;
        const tMs = this.state.currentTime * 1000;
        ctx.save();
        ctx.strokeStyle = BEAM_COLOR;
        ctx.lineWidth = 1.5;
        for (let i = 0; i < bm.t.length; i++) {
            if (Math.abs(bm.t[i] - tMs) > BEAM_FLASH_MS) continue;
            const a = this.toCanvas(bm.sx[i], bm.sy[i], bm.sz[i]);
            const ax = a.x, ay = a.y;
            const b = this.toCanvas(bm.ex[i], bm.ey[i], bm.ez[i]);
            ctx.beginPath();
            ctx.moveTo(ax, ay);
            ctx.lineTo(b.x, b.y);
            ctx.stroke();
        }
        ctx.restore();
    }

    // ─── World layers ───────────────────────────────────────────────────────

    drawLiquids(ctx) {
        const liquids = this.state.mapGeometry && this.state.mapGeometry.liquids;
        if (!Array.isArray(liquids)) return;
        for (const lq of liquids) {
            if (!lq || !Array.isArray(lq.tris) || lq.tris.length < 9) continue;
            drawLiquidVolume(ctx, lq.tris, LIQUID_BASE[lq.kind] || LIQUID_BASE.water,
                             LIQUID_ALPHA, SOLID_LIGHT, this.camera, this._toCanvas);
        }
    }

    // scratchCanvas: an offscreen surface for the world bake, taken from the
    // document that owns the target canvas — NOT the ambient `document`, which
    // this package must not touch (see the loader invariant). The host always
    // hands us a real canvas element, in mvd-web and in an MCP iframe alike.
    scratchCanvas() {
        return this.state.canvas.ownerDocument.createElement('canvas');
    }

    // drawCachedWorld: blit the depth-sorted floor model, re-rendering the
    // offscreen bake only when the camera, the canvas or the focus changed.
    // Steady playback at a fixed camera is therefore one drawImage.
    drawCachedWorld(ctx, se, cacheField, keyField, bakeLiquids) {
        if (!se) return;
        const s = this.state;
        const w = this.camera;
        const canvas = s.canvas;
        const dpr = s.dpr || 1;
        const liquids = s.mapGeometry && s.mapGeometry.liquids;
        const key = [
            w.yaw, w.pitch, w.zoomK, w.panX, w.panY,
            w.scale, w.offsetX, w.offsetY,
            w.cx, w.cy, w.zMid,
            s.focusGroupName, canvas.width, canvas.height, dpr,
            se.entries.length,
            bakeLiquids && Array.isArray(liquids) ? liquids.length : 0,
        ].join('|');
        let cache = s[cacheField];
        if (!cache) cache = s[cacheField] = this.scratchCanvas();
        if (s[keyField] !== key) {
            cache.width = canvas.width;   // also clears
            cache.height = canvas.height;
            const cctx = cache.getContext('2d');
            cctx.setTransform(dpr, 0, 0, dpr, 0, 0);
            renderSolidEntries(cctx, se, w, this._toCanvas, this.farFadePredicate());
            // Liquids are static geometry, so they bake into the cache too — a
            // translucent pass on top of the opaque world (large volumes tint
            // whatever is behind them).
            if (bakeLiquids) this.drawLiquids(cctx);
            s[keyField] = key;
        }
        ctx.save();
        ctx.setTransform(1, 0, 0, 1, 0, 0);
        ctx.drawImage(cache, 0, 0);
        ctx.restore();
    }

    // drawWorld: the static layers under the actors — floors, liquids, movers,
    // weapon-fire overlays, region outlines and loc labels.
    drawWorld(ctx) {
        const s = this.state;
        const groups = s.locationGroups || [];
        const backdropTris = s.mapGeometry && s.mapGeometry.backdropTris;
        if (groups.length === 0 && (!backdropTris || backdropTris.length < 9)) return;

        const focused = !!s.focusGroupName;
        const floorModel = this.floorModel();

        if (floorModel) {
            // One clean view: flat, near-opaque, depth-sorted region tops + box
            // sides. A higher floor covers a lower one (no translucent
            // stacking), the sides read as solid thickness, and from overhead
            // it is dead flat. Cached to an offscreen bitmap (per camera).
            this.drawCachedWorld(ctx, floorModel, '_floorCanvas', '_floorCanvasKey', false);
            // Liquids: translucent volumes above the floor, drawn live.
            this.drawLiquids(ctx);
        } else {
            // No triangle geometry (loc-blob maps): flat translucent fills.
            if (backdropTris && backdropTris.length >= 9) {
                drawTriangleListFill(ctx, backdropTris,
                    focused ? 'rgba(70, 80, 110, 0.14)' : 'rgba(70, 80, 110, 0.35)',
                    this._toCanvas);
            }
            for (const group of groups) {
                if (group.tris && group.tris.length >= 9) {
                    drawTriangleListFill(ctx, group.tris,
                        this.tierFill(group, this.focusTier(group.name)), this._toCanvas);
                }
            }
            this.drawLiquids(ctx);
        }

        // Movers (lifts/doors/plats) posed at the current time — above the
        // region fills and below the outlines and labels.
        this.drawMovers(ctx);

        // Weapon-fire overlays at the current time. No-op unless the spatial
        // streams were built.
        this.drawProjectiles(ctx);
        this.drawBeams(ctx);

        // Thin outlines around each traced region, after all fills so they sit
        // on top and stay visible regardless of adjacent tinting. These need
        // the allocating projection: an edge holds both endpoints at once.
        for (const group of groups) {
            if (!group.tris || group.tris.length < 9) continue;
            const tier = this.focusTier(group.name);
            // Idle baseline stays quiet so the floor reads as one calm
            // surface; the occupied overlay brightens the active region on top.
            let stroke = 'rgba(180, 180, 180, 0.22)';
            let width = 1;
            if (tier === 'focus')    { stroke = 'rgba(255, 255, 255, 0.85)'; width = 1.5; }
            else if (tier === 'far') { stroke = 'rgba(180, 180, 180, 0.1)'; }
            drawRegionOutline(ctx, group, this._toCanvasNew, stroke, width);
        }

        const labelPx = Math.round(12 * this.iconScale());
        ctx.font = `${labelPx}px monospace`;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        for (const group of groups) {
            const tier = this.focusTier(group.name);
            const pos = this.toCanvasNew(group.centroid.x, group.centroid.y, group.centroid.z);
            ctx.fillStyle = tier === 'far'
                ? scaleRgbaAlpha(group.color.text, 0.35)
                : group.color.text;
            ctx.fillText(group.name, pos.x, pos.y);
        }
    }

    // ─── Actors: players, items, entities ───────────────────────────────────

    // Combined z-sorted items-and-players pass. Building a single list lets
    // the draw order mix items and players correctly — two players on
    // different decks occlude in z order, an item clearly above a player
    // draws on top, and the common case of a player standing on a pickup
    // draws the player on top.
    drawItemsAndPlayersZSorted(ctx, time, playerData) {
        const iconScale = this.iconScale();
        const zRange = this.state.zRange || { lo: 0, hi: 0 };
        // Height-based symbol size scaling is a 2D-only cue: once the camera is
        // tilted, height is directly visible, and size differences would read as
        // distance instead.
        const zSpan = is3D(this.camera) ? 0 : (zRange.hi - zRange.lo);

        // Sort key is projected camera depth (closeness), which degenerates to
        // plain z at top-down — preserving the old sort exactly — and stays
        // correct under any rotation. The item bias is applied along the same
        // axis (z contributes sinPitch to depth) so the "player standing on a
        // pickup wins the tie" rule keeps working when tilted.
        const drawables = [];
        const items = this.state.items;
        if (items && items.length > 0) {
            for (const item of items) {
                const style = ITEM_MARKER_STYLES[item.kind];
                if (!style) continue;
                const pos = this.toCanvasNew(item.x, item.y, item.z);
                drawables.push({
                    kind: 'i',
                    sortDepth: pos.depth - ITEM_Z_TOP_THRESHOLD * this.camera.sinPitch,
                    pos, item, style
                });
            }
        }
        if (playerData) {
            for (const [name, data] of Object.entries(playerData)) {
                if (data.x === 0 && data.y === 0) continue;
                const symbolInfo = this.state.playerSymbols[name];
                if (!symbolInfo) continue;
                const pos = this.toCanvasNew(data.x, data.y, data.z);
                drawables.push({
                    kind: 'p',
                    sortDepth: pos.depth,
                    pos, name, data, symbolInfo
                });
            }
        }
        if (drawables.length === 0) return;

        drawables.sort((a, b) => a.sortDepth - b.sortDepth);

        const itemSize = ITEM_MARKER_SIZE * iconScale;
        const itemHalf = itemSize / 2;
        const itemFontPx = Math.round(10 * iconScale);
        const tilted = is3D(this.camera);

        ctx.save();
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';

        for (const d of drawables) {
            if (d.kind === 'i') {
                this.drawSingleMapItem(ctx, time, d.item, d.style,
                                  itemSize, itemHalf, itemFontPx, d.pos);
            } else {
                // Floor anchor stem under the symbol — tilted views only (at
                // top-down it projects to a point).
                if (tilted) {
                    this.drawPlayerFloorStem(ctx, d.name, d.data, d.symbolInfo, d.pos);
                }
                // Optional view/velocity arrows (work at any tilt — xy shows even
                // top-down). Drawn under the symbol so the letter stays legible.
                if (this.state.showViewArrows || this.state.showVelArrows) {
                    this.drawPlayerArrows(ctx, d.data, d.symbolInfo);
                }
                this.drawSinglePlayer(ctx, d.data, d.symbolInfo,
                                 iconScale, zRange, zSpan, d.pos);
            }
        }

        ctx.globalAlpha = 1.0;
        ctx.restore();
    }

    drawSinglePlayer(ctx, data, symbolInfo, iconScale, zRange, zSpan, pos) {
        // Per-player z-based size scale: players near the top of the map
        // (98th percentile z) render 25% larger than those near the bottom
        // (2nd percentile), linearly interpolated. Applied on top of the
        // zoom-driven iconScale.
        let zScale = 1;
        if (zSpan > 0) {
            let t = ((data.z || 0) - zRange.lo) / zSpan;
            if (t < 0) t = 0;
            if (t > 1) t = 1;
            zScale = 1 + 0.25 * t;
        }
        const totalScale = iconScale * zScale;
        const symSize = PLAYER_SYMBOL_BASE_SIZE * totalScale;
        const orbitRadius = 14 * totalScale;
        const badgeRadius = 5 * totalScale;

        const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
        drawPlayerSymbolAt(ctx, symbolInfo.symbol, teamHex, pos.x, pos.y, symSize);

        const badges = this.getActiveBadges(data);
        if (badges.length > 0) {
            drawBadgesAroundCenter(ctx, badges, pos.x, pos.y, orbitRadius, badgeRadius);
        }
    }

    drawSingleMapItem(ctx, time, item, style, size, half, fontPx, pos) {
        const up = this.isItemUp(item, time);
        ctx.globalAlpha = up ? 1.0 : ITEM_DIM_ALPHA;

        const x = Math.round(pos.x - half);
        const y = Math.round(pos.y - half);

        ctx.fillStyle = style.fill;
        ctx.fillRect(x, y, size, size);

        if (style.outline) {
            ctx.strokeStyle = style.outline;
            ctx.lineWidth = 1.5;
            ctx.strokeRect(x + 0.5, y + 0.5, size - 1, size - 1);
        }

        if (style.label) {
            ctx.font = `bold ${fontPx}px -apple-system, BlinkMacSystemFont, sans-serif`;
            ctx.fillStyle = style.textColor || style.outline || '#fff';
            ctx.fillText(style.label, pos.x, pos.y + 1);
        }
        ctx.globalAlpha = 1.0;
    }

    drawMapEntities(ctx) {
        const entities = this.state.mapEntities;
        if (!entities || entities.length === 0) return;
        const f = this.state.entityFilters;
        const iconScale = this.iconScale();
        const size = ITEM_MARKER_SIZE * iconScale;
        const half = size / 2;
        const fontPx = Math.round(10 * iconScale);

        // Connection arrows first, beneath the markers.
        if (f.teleporter && this.state.teleportArrows.length > 0) {
            ctx.save();
            ctx.strokeStyle = TELEPORT_COLOR;
            ctx.fillStyle = TELEPORT_COLOR;
            ctx.globalAlpha = 0.55;
            ctx.lineWidth = Math.max(1, 1.5 * iconScale);
            for (const a of this.state.teleportArrows) {
                const s = this.toCanvasNew(a.sx, a.sy, a.sz);
                const d = this.toCanvasNew(a.dx, a.dy, a.dz);
                drawArrow(ctx, s.x, s.y, d.x, d.y, 8 * iconScale);
            }
            ctx.restore();
        }

        ctx.save();
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        // Farther-from-camera first so nearer markers draw on top (degenerates
        // to lower-decks-first at top-down).
        const sorted = entities
            .map(e => ({ e, pos: this.toCanvasNew(e.x, e.y, e.z) }))
            .sort((a, b) => a.pos.depth - b.pos.depth);
        for (const { e, pos } of sorted) {
            if (!f[this.entityCategory(e)]) continue;
            const style = e.type === 'item' ? LEARN_ITEM_STYLES[e.kind] : STRUCTURAL_STYLES[e.type];
            if (style) this.drawEntityMarker(ctx, e, style, size, half, fontPx, pos);
        }
        ctx.restore();
    }

    drawEntityMarker(ctx, e, style, size, half, fontPx, pos) {
        if (style.circle) {
            ctx.beginPath();
            ctx.arc(pos.x, pos.y, half, 0, Math.PI * 2);
            ctx.fillStyle = style.fill;
            ctx.fill();
            if (style.outline) { ctx.strokeStyle = style.outline; ctx.lineWidth = 1.5; ctx.stroke(); }
        } else {
            const x = Math.round(pos.x - half);
            const y = Math.round(pos.y - half);
            ctx.fillStyle = style.fill;
            ctx.fillRect(x, y, size, size);
            if (style.outline) {
                ctx.strokeStyle = style.outline;
                ctx.lineWidth = 1.5;
                ctx.strokeRect(x + 0.5, y + 0.5, size - 1, size - 1);
            }
        }
        if (style.label) {
            ctx.font = `bold ${fontPx}px -apple-system, BlinkMacSystemFont, sans-serif`;
            ctx.fillStyle = style.textColor || style.outline || '#fff';
            ctx.fillText(style.label, pos.x, pos.y + 1);
        }
    }

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

    // drawPlayerArrows draws the enabled arrows for one player. Velocity uses the
    // team colour (it's that player's motion); view uses a neutral light tone so
    // the two are distinguishable when both are on.
    drawPlayerArrows(ctx, data, symbolInfo) {
        const ox = data.x, oy = data.y, oz = data.z;
        if (this.state.showVelArrows && typeof data.vx === 'number') {
            const speed = Math.hypot(data.vx, data.vy, data.vz);
            if (speed > VEL_ARROW_MIN_SPEED) {
                const s = 1 / VEL_UNITS_PER_MAP_UNIT;
                const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
                drawWorldArrow(ctx, ox, oy, oz, data.vx * s, data.vy * s, data.vz * s,
                                       hexToRgba(teamHex, 0.9), 3.5, this._toCanvasNew);
            }
        }
        if (this.state.showViewArrows && typeof data.vya === 'number') {
            const yaw = data.vya * ANGLE16_TO_RAD;
            const pitch = (data.vp || 0) * ANGLE16_TO_RAD;
            const cp = Math.cos(pitch);
            // Quake forward vector: +pitch looks down, so z = -sin(pitch).
            drawWorldArrow(ctx, ox, oy, oz,
                                   cp * Math.cos(yaw) * VIEW_ARROW_LEN,
                                   cp * Math.sin(yaw) * VIEW_ARROW_LEN,
                                   -Math.sin(pitch) * VIEW_ARROW_LEN,
                                   VIEW_ARROW_COLOR, 3, this._toCanvasNew);
        }
    }

    drawPlayerFloorStem(ctx, name, data, symbolInfo, pos) {
        const z = data.z || 0;
        // Prefer the per-sample computed floor height H (data.fh): the floor
        // surface is z - 24 - H (H is measured from the bottom of the player's
        // bounding box, which sits 24 below the origin). H is accurate on lifts
        // (the floor pass stands players on movers) and makes the stem a direct
        // visual readout of H. Fall back to scanning the static floor geometry
        // when H is unavailable (no BSP) or NoFloor (over a void).
        let bottomZ;
        if (typeof data.fh === 'number') {
            bottomZ = z - PLAYER_ORIGIN_ABOVE_FLOOR - data.fh;
        } else {
            const floorZ = this.playerFloorZ(name, data.x, data.y, z);
            bottomZ = floorZ !== null ? floorZ : z - PLAYER_ORIGIN_ABOVE_FLOOR;
        }
        const bot = this.toCanvasNew(data.x, data.y, bottomZ);
        const teamHex = this.teamColors[symbolInfo.teamIdx] || this.teamColors[0];
        ctx.strokeStyle = hexToRgba(teamHex, 0.55);
        ctx.lineWidth = 3;
        ctx.beginPath();
        ctx.moveTo(pos.x, pos.y);
        ctx.lineTo(bot.x, bot.y);
        ctx.stroke();
        ctx.fillStyle = hexToRgba(teamHex, 0.7);
        ctx.beginPath();
        ctx.arc(bot.x, bot.y, 2.5, 0, Math.PI * 2);
        ctx.fill();
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

    // Highlight loc regions that contain at least one player. Drawn on top of
    // the prerendered background and the team-control tint, so the player's
    // current region is always identifiable at a glance.
    drawOccupiedRegionsOverlay(ctx, playerData) {
        const groupsByName = this.state.locationGroupByName;
        if (!groupsByName) return;
        const occupied = this.computeOccupiedGroupTeams(playerData);
        if (occupied.size === 0) return;

        // Colour-fill pass: tint each occupied region by the team(s) present so
        // the active area stands out against the otherwise-neutral floor. The
        // floor is drawn neutral by default (the floor model),
        // so a tint only ever appears here, under a player. One translucent path
        // per region (single fill → no internal triangle seams).
        for (const [name, teams] of occupied) {
            const group = groupsByName[name];
            if (!group || !group.tris || group.tris.length < 9) continue;
            drawTriangleListFill(ctx, group.tris, this.regionActiveTint(teams), this._toCanvas);
        }

        // Brighter outline pass.
        for (const name of occupied.keys()) {
            const group = groupsByName[name];
            if (!group || !group.tris || group.tris.length < 9) continue;
            drawRegionOutline(ctx, group, this._toCanvasNew, 'rgba(220, 220, 220, 0.7)', 1);
        }

        // Bold label pass — draw over the dimmer prerendered label so it pops.
        const boldPx = Math.round(12 * this.iconScale());
        ctx.font = `bold ${boldPx}px monospace`;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        for (const name of occupied.keys()) {
            const group = groupsByName[name];
            if (!group) continue;
            const pos = this.toCanvasNew(group.centroid.x, group.centroid.y, group.centroid.z);
            // Soft shadow so the label stays legible against any underlying tint.
            ctx.fillStyle = 'rgba(0, 0, 0, 0.65)';
            ctx.fillText(group.name, pos.x + 1, pos.y + 1);
            ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
            ctx.fillText(group.name, pos.x, pos.y);
        }
    }

    // Draw control overlay for regions based on current control state
    drawRegionControlOverlay(ctx, controlStates) {
        const regions = this.state.controlRegions;
        if (!regions) return;

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
                // Region focus: tint outside the focus neighborhood fades with
                // the base fills so the focused area keeps visual priority.
                const tint = this.focusTier(group.name) === 'far'
                    ? scaleRgbaAlpha(color, 0.3)
                    : color;
                fillRegion(ctx, group, tint, this._toCanvasNew);
            }
        }
    }

    // drawVisLines draws a line between every player pair whose byPair intervals
    // currently cover the playhead, styled per `style` (width + mutual/one-way
    // colours). Endpoints sit at eye height (origin + 22) for visual honesty.
    // White = mutual; red/blue = one-way.
    drawVisLines(ctx, byPair, time, playerData, style) {
        if (!byPair || !playerData) return;
        const tMs = time * 1000;
        const names = Object.keys(playerData);
        ctx.save();
        ctx.lineWidth = style.width;
        for (let i = 0; i < names.length; i++) {
            const a = names[i], pa = playerData[a];
            if (!pa || typeof pa.x !== 'number') continue;
            for (let j = i + 1; j < names.length; j++) {
                const b = names[j], pb = playerData[b];
                if (!pb || typeof pb.x !== 'number') continue;
                const aSeesB = losCovers(byPair[a] && byPair[a][b], tMs);
                const bSeesA = losCovers(byPair[b] && byPair[b][a], tMs);
                if (!aSeesB && !bSeesA) continue;
                const ea = this.toCanvasNew(pa.x, pa.y, pa.z + 22);
                const eb = this.toCanvasNew(pb.x, pb.y, pb.z + 22);
                ctx.strokeStyle = (aSeesB && bSeesA) ? style.mutual
                    : aSeesB ? style.first : style.second;
                ctx.beginPath();
                ctx.moveTo(ea.x, ea.y);
                ctx.lineTo(eb.x, eb.y);
                ctx.stroke();
            }
        }
        ctx.restore();
    }

    // drawLosLines renders the PVS overlay (thin, faint — potential visibility)
    // underneath the LOS overlay (solid — actual sightline), each gated by its
    // own toggle. PVS first so LOS lines draw on top. No-op unless data is
    // present (BSP-backed maps only).
    drawLosLines(ctx, time, playerData) {
        const s = this.state;
        if (s.showPvs) this.drawVisLines(ctx, s.pvsByPair, time, playerData, PVS_STYLE);
        if (s.showLos) this.drawVisLines(ctx, s.losByPair, time, playerData, LOS_STYLE);
    }

    drawTracks(ctx, time) {
        const s = this.state;
        const trailDuration = s.trailDuration;

        for (const [name, points] of Object.entries(s.fullTrails)) {
            if (!s.enabledPlayers[name]) continue;
            if (points.length < 2) continue;

            // If current time is before trail start, pull start back so trail grows from here
            if (time < (s.trailStartTimes[name] || 0)) {
                s.trailStartTimes[name] = time;
            }

            // Find the end index: last point at or before current time
            const endIdx = trailIndexAtTime(points, time);
            if (endIdx < 1) continue;

            // Find start: trail window starts at max(time - trailDuration, trailStartTime)
            const trailStart = Math.max(time - trailDuration, s.trailStartTimes[name] || 0);
            let startIdx = trailIndexAtTime(points, trailStart);
            if (startIdx < 0) startIdx = 0;

            if (endIdx - startIdx < 1) continue;

            // Pre-convert the visible window of world-space points into canvas
            // pixels at the current pan / zoom so the inner draw loop stays
            // allocation-free and the scratch projection point isn't clobbered
            // between consecutive reads.
            const cpts = new Array(endIdx - startIdx + 1);
            for (let i = startIdx; i <= endIdx; i++) {
                const pt = points[i];
                const c = this.toCanvasNew(pt.wx, pt.wy, pt.wz);
                cpts[i - startIdx] = { x: c.x, y: c.y, spawn: pt.spawn, death: pt.death, tp: pt.tp };
            }

            const teamHex = this.teamColors[points[0].teamIdx] || this.teamColors[0];
            const solidColor = hexToRgba(teamHex, 0.4);
            const dashColor = hexToRgba(teamHex, 0.2);
            const markerColor = hexToRgba(teamHex, 0.8);

            // Collect death/spawn markers to draw after lines
            const markers = [];

            let inDash = false;
            let afterDeath = false; // suppress line from death to next spawn
            ctx.lineWidth = 3;
            ctx.strokeStyle = solidColor;
            ctx.setLineDash([]);
            ctx.beginPath();
            ctx.moveTo(cpts[0].x, cpts[0].y);

            if (cpts[0].spawn) markers.push({ x: cpts[0].x, y: cpts[0].y, type: 'spawn' });

            for (let i = 1; i < cpts.length; i++) {
                const pt = cpts[i];

                if (pt.spawn) {
                    // Spawn: start a new line segment (gap from death)
                    ctx.stroke();
                    ctx.beginPath();
                    ctx.setLineDash([]);
                    ctx.strokeStyle = solidColor;
                    inDash = false;
                    afterDeath = false;
                    ctx.moveTo(pt.x, pt.y);
                    markers.push({ x: pt.x, y: pt.y, type: 'spawn' });
                    continue;
                }

                if (pt.death) {
                    // Death: draw line to death point, then mark it
                    ctx.lineTo(pt.x, pt.y);
                    ctx.stroke();
                    ctx.beginPath();
                    afterDeath = true;
                    markers.push({ x: pt.x, y: pt.y, type: 'death' });
                    continue;
                }

                if (afterDeath) {
                    // Between death and spawn — don't draw
                    ctx.moveTo(pt.x, pt.y);
                    continue;
                }

                const needDash = !!pt.tp;
                if (needDash !== inDash) {
                    ctx.stroke();
                    ctx.beginPath();
                    const prev = cpts[i - 1];
                    ctx.moveTo(prev.x, prev.y);
                    if (needDash) {
                        ctx.setLineDash([4, 6]);
                        ctx.strokeStyle = dashColor;
                    } else {
                        ctx.setLineDash([]);
                        ctx.strokeStyle = solidColor;
                    }
                    inDash = needDash;
                }
                ctx.lineTo(pt.x, pt.y);
            }
            ctx.stroke();
            ctx.setLineDash([]);

            // Draw death (✕) and spawn (●) markers on top
            ctx.fillStyle = markerColor;
            ctx.strokeStyle = markerColor;
            ctx.lineWidth = 2;
            for (const m of markers) {
                if (m.type === 'death') {
                    // Draw ✕
                    const sz = 5;
                    ctx.beginPath();
                    ctx.moveTo(m.x - sz, m.y - sz);
                    ctx.lineTo(m.x + sz, m.y + sz);
                    ctx.moveTo(m.x + sz, m.y - sz);
                    ctx.lineTo(m.x - sz, m.y + sz);
                    ctx.stroke();
                } else {
                    // Draw ●
                    ctx.beginPath();
                    ctx.arc(m.x, m.y, 3, 0, Math.PI * 2);
                    ctx.fill();
                }
            }
        }
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
    // point, within the follow hit radius. `bucketPlayers` is the frame's
    // player map — the host supplies it because the frame source (bucket
    // reconstruction) still lives host-side; positions are refined through the
    // native-rate streams exactly like the draw pass.
    hitTestPlayerSymbol(cx, cy, time, bucketPlayers) {
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
}
