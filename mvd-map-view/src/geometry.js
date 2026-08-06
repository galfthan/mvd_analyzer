// Pure geometry helpers for the map renderer: triangle-soup handling, the
// floor-slab boundary extrusion, and mover pose lookup. Nothing here touches
// the DOM, a canvas, or renderer state — every function is a value in,
// value out.

import { lowerBoundIndex } from './util.js';

// Floors are this many units tall. Both views extrude the floor's outer
// boundary (perimeter + step risers, via floorBoundaryEdges) down by this
// much into box sides (floorBoundaryWalls), baked into the depth-sorted
// floor model so the floor reads as one solid slab.
export const FLOOR_SLAB_DEPTH = 10;

// normalizeMapGeometry: upgrade a fetched geometry JSON to the version-2
// shape in place. Version 2 (mapgen ≥ schema bump) emits 9 floats per
// triangle (x,y,z per vertex); version 1 files emitted 6 (XY only). Old
// files — e.g. a stale browser cache — are expanded by flattening every
// vertex to the region's median z, which reproduces the v1 look exactly
// at top-down and gives a usable (if flat-per-region) 3D view.
export function normalizeMapGeometry(geom) {
    if (geom.version >= 2) return;
    for (const l of geom.locs) {
        if (!l || !Array.isArray(l.tris)) continue;
        const t6 = l.tris;
        const z = l.z || 0;
        const t9 = new Array((t6.length / 6) * 9);
        let j = 0;
        for (let i = 0; i + 5 < t6.length; i += 6) {
            t9[j++] = t6[i];     t9[j++] = t6[i + 1]; t9[j++] = z;
            t9[j++] = t6[i + 2]; t9[j++] = t6[i + 3]; t9[j++] = z;
            t9[j++] = t6[i + 4]; t9[j++] = t6[i + 5]; t9[j++] = z;
        }
        l.tris = t9;
    }
    geom.version = 2;
}

export function pointInTriangle(px, py, a, b, c) {
    const d1 = (px - b.x) * (a.y - b.y) - (a.x - b.x) * (py - b.y);
    const d2 = (px - c.x) * (b.y - c.y) - (b.x - c.x) * (py - c.y);
    const d3 = (px - a.x) * (c.y - a.y) - (c.x - a.x) * (py - a.y);
    const hasNeg = (d1 < 0) || (d2 < 0) || (d3 < 0);
    const hasPos = (d1 > 0) || (d2 > 0) || (d3 > 0);
    return !(hasNeg && hasPos);
}

// Compute the 2nd / 98th percentile of z across all map locations. These
// endpoints are used to scale player-symbol size by "height on the map": a
// player at the lo end renders at base size, one at the hi end 25% larger.
// Percentiles (not min / max) so a single out-of-bounds loc doesn't squash
// the useful range.
export function computeMapZRange(locations) {
    if (!locations || locations.length === 0) return { lo: 0, hi: 0 };
    const zs = [];
    for (const loc of locations) zs.push(loc.z || 0);
    zs.sort((a, b) => a - b);
    const n = zs.length;
    const lo = zs[Math.floor(n * 0.02)];
    const hi = zs[Math.min(n - 1, Math.floor(n * 0.98))];
    return { lo, hi };
}

// floorBoundaryEdges finds the triangle-soup's boundary edges — those used by
// exactly one triangle — and gives each an outward horizontal normal. Vertices
// are keyed at 1/8-unit precision (the wire's own quantum) so coincident
// vertices from separate loc regions weld instead of leaving a seam.
export function floorBoundaryEdges(backdropTris, groups) {
    const edges = new Map();
    const k = (x, y, z) => Math.round(x * 8) + ',' + Math.round(y * 8) + ',' + Math.round(z * 8);
    const add = (tris) => {
        if (!tris || tris.length < 9) return;
        for (let i = 0; i + 8 < tris.length; i += 9) {
            for (let e = 0; e < 3; e++) {
                const o1 = i + e * 3, o2 = i + ((e + 1) % 3) * 3, o3 = i + ((e + 2) % 3) * 3;
                const ka = k(tris[o1], tris[o1 + 1], tris[o1 + 2]);
                const kb = k(tris[o2], tris[o2 + 1], tris[o2 + 2]);
                const key = ka < kb ? ka + '|' + kb : kb + '|' + ka;
                const info = edges.get(key);
                if (info) { info.count++; continue; }
                edges.set(key, {
                    count: 1,
                    ax: tris[o1], ay: tris[o1 + 1], az: tris[o1 + 2],
                    bx: tris[o2], by: tris[o2 + 1], bz: tris[o2 + 2],
                    ox: tris[o3], oy: tris[o3 + 1], // opposite vertex → outward dir
                });
            }
        }
    };
    add(backdropTris);
    for (const g of groups) add(g.tris);
    const out = [];
    for (const info of edges.values()) {
        if (info.count !== 1) continue; // interior edge → no box side
        const { ax, ay, az, bx, by, bz, ox, oy } = info;
        // Horizontal normal ⟂ the edge, flipped to point away from the
        // triangle's opposite vertex (i.e. out of the floor).
        let nx = -(by - ay), ny = bx - ax;
        const mx = (ax + bx) / 2, my = (ay + by) / 2;
        if (nx * (ox - mx) + ny * (oy - my) > 0) { nx = -nx; ny = -ny; }
        const nl = Math.hypot(nx, ny) || 1;
        out.push({ ax, ay, az, bx, by, bz, nx: nx / nl, ny: ny / nl });
    }
    return out;
}

// floorBoundaryWalls extrudes the boundary edges into the side-wall triangles
// that turn the floor into one solid box FLOOR_SLAB_DEPTH units tall — used by
// the floor model, which depth-sorts opaque triangles (no per-edge culling).
export function floorBoundaryWalls(backdropTris, groups) {
    const walls = [];
    for (const e of floorBoundaryEdges(backdropTris, groups)) {
        const { ax, ay, az, bx, by, bz } = e;
        const az2 = az - FLOOR_SLAB_DEPTH, bz2 = bz - FLOOR_SLAB_DEPTH;
        walls.push(ax, ay, az, bx, by, bz, bx, by, bz2);
        walls.push(ax, ay, az, bx, by, bz2, ax, ay, az2);
    }
    return walls;
}

// moverPoseAt returns the mover's {x, y, z, vis} at tMs (match-relative
// milliseconds): the last recorded sample at or before tMs, clamped to the
// first sample for earlier times. Binary search — tracks can be long for a
// lift that ran the whole match.
export function moverPoseAt(m, tMs) {
    const t = m.t;
    const n = t ? t.length : 0;
    if (n === 0) return null;
    // Clamp times before the first sample to it (strictly increasing tracks,
    // so this matches the previous tMs<=t[0] guard exactly).
    let idx = lowerBoundIndex(t, tMs, (a, i) => a[i]);
    if (idx < 0) idx = 0;
    return { x: m.x[idx], y: m.y[idx], z: m.z[idx], vis: m.vis[idx] };
}
