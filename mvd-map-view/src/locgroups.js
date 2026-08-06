// Loc regions: grouping the analyzer's per-loc points into named regions,
// attaching BSP-derived triangle geometry to them, and the derived shapes the
// renderer draws — outline, bounding box, and the stacking relation that lets
// an upper deck's tint sit over a lower one.

import { normalizeLocationName } from './locs.js';
import { getLocationColor } from './color.js';
import { floorBoundaryWalls } from './geometry.js';

// processLocationGroups: fold the per-loc point list into one group per
// normalized name, with a centroid (z rides along so labels project to the
// group's real floor height) and, when BSP geometry is loaded, that loc's
// triangle list.
//
// Geometry keys must match the analyzer's own normalisation —
// NormalizeLocationName (Go) <-> normalizeLocationName (JS). Geometry entries
// with an empty name are the unnamed backdrop bucket (faces that matched no
// loc); the caller draws those separately as a neutral underlay.
//
// Returns { groups, byName } — the array the renderer iterates and the lookup
// the per-frame occupancy highlighting keys into.
export function processLocationGroups(locations, geometry) {
    const groups = {};

    for (const loc of locations) {
        const normalizedName = normalizeLocationName(loc.name);
        if (!groups[normalizedName]) {
            groups[normalizedName] = {
                name: normalizedName,
                points: [],
                centroid: { x: 0, y: 0 },
                color: getLocationColor(normalizedName),
            };
        }
        groups[normalizedName].points.push({ x: loc.x, y: loc.y, z: loc.z });
    }

    for (const group of Object.values(groups)) {
        let sumX = 0, sumY = 0, sumZ = 0;
        for (const p of group.points) {
            sumX += p.x;
            sumY += p.y;
            sumZ += p.z || 0;
        }
        group.centroid = {
            x: sumX / group.points.length,
            y: sumY / group.points.length,
            z: sumZ / group.points.length,
        };
    }

    if (geometry && Array.isArray(geometry.locs)) {
        const geomByName = {};
        for (const l of geometry.locs) {
            if (l.name === '') continue;
            geomByName[l.name] = l;
        }
        for (const group of Object.values(groups)) {
            const g = geomByName[group.name];
            group.tris = g && Array.isArray(g.tris) && g.tris.length >= 9 ? g.tris : null;
        }
    }

    return { groups: Object.values(groups), byName: groups };
}

// computeRegionOutline: the group's boundary edge list — edges used by exactly
// one of its triangles — each with an XY normal pointing away from the region
// interior. Memoized on the group (invalidated by rebuilding groups, which is
// what a geometry reload does).
export function computeRegionOutline(group) {
    if (group.outline !== undefined) return group.outline;
    const tris = group.tris;
    if (!tris || tris.length < 9) {
        group.outline = null;
        group.outlineNormals = null;
        return null;
    }
    const edgeInfo = new Map();
    const keyFor = (x1, y1, z1, x2, y2, z2) => {
        // Canonical order so (a,b) and (b,a) hash equally.
        if (x1 < x2 || (x1 === x2 && (y1 < y2 || (y1 === y2 && z1 <= z2)))) {
            return x1 + ',' + y1 + ',' + z1 + '|' + x2 + ',' + y2 + ',' + z2;
        }
        return x2 + ',' + y2 + ',' + z2 + '|' + x1 + ',' + y1 + ',' + z1;
    };
    const bump = (key, tcx, tcy) => {
        const info = edgeInfo.get(key);
        if (info) info.count++;
        else edgeInfo.set(key, { count: 1, tcx, tcy });
    };
    for (let i = 0; i + 8 < tris.length; i += 9) {
        const ax = tris[i],     ay = tris[i + 1], az = tris[i + 2];
        const bx = tris[i + 3], by = tris[i + 4], bz = tris[i + 5];
        const cx = tris[i + 6], cy = tris[i + 7], cz = tris[i + 8];
        // XY centroid of the owning triangle marks the interior side of
        // each of its edges.
        const tcx = (ax + bx + cx) / 3;
        const tcy = (ay + by + cy) / 3;
        bump(keyFor(ax, ay, az, bx, by, bz), tcx, tcy);
        bump(keyFor(bx, by, bz, cx, cy, cz), tcx, tcy);
        bump(keyFor(cx, cy, cz, ax, ay, az), tcx, tcy);
    }
    const outline = [];
    const normals = [];
    for (const [key, info] of edgeInfo) {
        if (info.count !== 1) continue;
        const [p1, p2] = key.split('|');
        const [x1, y1, z1] = p1.split(',').map(Number);
        const [x2, y2, z2] = p2.split(',').map(Number);
        outline.push(x1, y1, z1, x2, y2, z2);
        // Edge perpendicular in XY, signed to point away from the interior.
        let nx = y2 - y1;
        let ny = x1 - x2;
        const mx = (x1 + x2) / 2;
        const my = (y1 + y2) / 2;
        if (nx * (info.tcx - mx) + ny * (info.tcy - my) > 0) {
            nx = -nx;
            ny = -ny;
        }
        normals.push(nx, ny);
    }
    group.outline = outline;
    group.outlineNormals = normals;
    return outline;
}

// groupWorldBBox: the group's XY bounding box over its triangles, memoized.
export function groupWorldBBox(group) {
    if (group._wbbox) return group._wbbox;
    const tris = group.tris;
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (let i = 0; i + 8 < tris.length; i += 9) {
        for (let v = 0; v < 9; v += 3) {
            const x = tris[i + v], y = tris[i + v + 1];
            if (x < minX) minX = x;
            if (x > maxX) maxX = x;
            if (y < minY) minY = y;
            if (y > maxY) maxY = y;
        }
    }
    group._wbbox = { minX, maxX, minY, maxY };
    return group._wbbox;
}

// Two control regions stack when one sits at least this far above the other
// (roughly one step height) and their XY boxes overlap by at least this
// fraction of the lower one's area.
export const REGION_STACK_Z_EPS = 32;
export const REGION_STACK_OVERLAP_FRAC = 0.25;

// computeRegionStacking: precompute per-region bbox, median z, and the list of
// regions stacked above it. The overlay uses `_above` to decide whether a
// region's tint should read through an empty upper deck.
export function computeRegionStacking(regions) {
    for (const r of regions) {
        let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
        const zs = [];
        for (const pt of r.points) {
            if (pt.x < minX) minX = pt.x;
            if (pt.x > maxX) maxX = pt.x;
            if (pt.y < minY) minY = pt.y;
            if (pt.y > maxY) maxY = pt.y;
            zs.push(pt.z ?? 0);
        }
        r._bbox = { minX, maxX, minY, maxY };
        zs.sort((a, b) => a - b);
        r._z = zs.length > 0 ? zs[zs.length >> 1] : 0;
        r._bboxArea = Math.max(1, (maxX - minX) * (maxY - minY));
    }
    for (const r of regions) {
        const above = [];
        for (const r2 of regions) {
            if (r2 === r) continue;
            if (r2._z <= r._z + REGION_STACK_Z_EPS) continue;
            const ox = Math.max(0, Math.min(r._bbox.maxX, r2._bbox.maxX) - Math.max(r._bbox.minX, r2._bbox.minX));
            const oy = Math.max(0, Math.min(r._bbox.maxY, r2._bbox.maxY) - Math.max(r._bbox.minY, r2._bbox.minY));
            if ((ox * oy) / r._bboxArea >= REGION_STACK_OVERLAP_FRAC) {
                above.push(r2);
            }
        }
        r._above = above;
    }
}

// ─── Floor model ────────────────────────────────────────────────────────────

// Floor-model tones. Region tops use the neutral backdrop tone; the box sides
// use a darker one so they read as sides. No Lambert/normal shading anywhere —
// every surface is a single flat colour, so from overhead the floor reads dead
// flat. Near-opaque so a higher floor cleanly covers a lower one (no
// translucent stacking, which was the apparent "shading").
export const BACKDROP_FLOOR_RGB = [70, 80, 110];
export const FLOOR_BOX_SIDE_RGB = [44, 50, 72];
export const FLOOR_TOP_ALPHA = 0.95;

// buildFloorModel builds the per-triangle render list for the one clean view:
// flat region tops + the backdrop + the box sides, each a single flat tone,
// with a centroid for the painter sort. Returns null when the map has no
// triangle geometry at all (callers fall back to the flat translucent fills /
// loc blobs). Caching is the caller's business — the returned model carries
// the geom and groups it was built from so a stale one is easy to spot.
export function buildFloorModel(geom, groups) {
    groups = groups || [];
    const backdropTris = geom && geom.backdropTris;
    const haveBackdrop = backdropTris && backdropTris.length >= 9;
    if (!haveBackdrop && groups.length === 0) return null;

    const entries = [];
    const push = (tris, rgb, name) => {
        if (!tris || tris.length < 9) return;
        const fill = `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${FLOOR_TOP_ALPHA})`;
        const fillFaded = `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${FLOOR_TOP_ALPHA * 0.18})`;
        for (let i = 0; i + 8 < tris.length; i += 9) {
            entries.push({
                tris, off: i,
                cx: (tris[i] + tris[i + 3] + tris[i + 6]) / 3,
                cy: (tris[i + 1] + tris[i + 4] + tris[i + 7]) / 3,
                cz: (tris[i + 2] + tris[i + 5] + tris[i + 8]) / 3,
                name, fill, fillFaded, depth: 0,
            });
        }
    };

    if (haveBackdrop) push(backdropTris, BACKDROP_FLOOR_RGB, null);
    // Region tops default to the neutral backdrop tone. Colouring every loc by
    // its own hue was mostly visual noise; a region only takes on its loc
    // colour when a player is in it, via the live occupancy fill pass. The
    // name is still carried so that overlay can find the tris.
    for (const g of groups) {
        if (!g.tris || g.tris.length < 9) continue;
        push(g.tris, BACKDROP_FLOOR_RGB, g.name);
    }
    const sides = floorBoundaryWalls(backdropTris, groups);
    if (sides.length >= 9) push(sides, FLOOR_BOX_SIDE_RGB, null);

    if (entries.length === 0) return null;
    return { geom, groups, entries, sortedFor: null };
}
