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

import { newCamera, project } from './camera.js';
import { moverPoseAt } from './geometry.js';
import { buildFloorModel } from './locgroups.js';
import { scaleRgbaAlpha } from './color.js';
import {
    drawTriangleListFill, drawRegionOutline, renderSolidEntries,
    drawMoverMesh, drawLiquidVolume,
} from './draw.js';

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
        // Scratch projection point, reused by toCanvas. Bound projection
        // callbacks so draw primitives can be handed a plain function.
        this._pt = { x: 0, y: 0, depth: 0 };
        this._toCanvas = (x, y, z) => this.toCanvas(x, y, z);
        this._toCanvasNew = (x, y, z) => this.toCanvasNew(x, y, z);
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
}
