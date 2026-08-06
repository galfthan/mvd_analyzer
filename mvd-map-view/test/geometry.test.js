import test from 'node:test';
import assert from 'node:assert/strict';

import {
    FLOOR_SLAB_DEPTH,
    normalizeMapGeometry,
    pointInTriangle,
    computeMapZRange,
    floorBoundaryEdges,
    floorBoundaryWalls,
    moverPoseAt,
} from '../src/geometry.js';

test('normalizeMapGeometry expands v1 (6 floats/tri) to v2 (9) at the region z', () => {
    const geom = { version: 1, locs: [{ name: 'ra', z: 64, tris: [0, 0, 10, 0, 10, 10] }] };
    normalizeMapGeometry(geom);
    assert.equal(geom.version, 2);
    assert.deepEqual(geom.locs[0].tris, [0, 0, 64, 10, 0, 64, 10, 10, 64]);
});

test('normalizeMapGeometry leaves v2 untouched', () => {
    const tris = [0, 0, 1, 2, 3, 4, 5, 6, 7];
    const geom = { version: 2, locs: [{ name: 'ya', tris }] };
    normalizeMapGeometry(geom);
    assert.equal(geom.locs[0].tris, tris); // same reference, not rebuilt
});

test('normalizeMapGeometry tolerates a loc with no triangles', () => {
    const geom = { version: 1, locs: [{ name: 'empty' }, null] };
    normalizeMapGeometry(geom);
    assert.equal(geom.version, 2);
});

test('pointInTriangle: inside, outside, and on an edge', () => {
    const a = { x: 0, y: 0 }, b = { x: 10, y: 0 }, c = { x: 0, y: 10 };
    assert.equal(pointInTriangle(1, 1, a, b, c), true);
    assert.equal(pointInTriangle(9, 9, a, b, c), false);
    assert.equal(pointInTriangle(5, 0, a, b, c), true, 'edge counts as inside');
});

test('computeMapZRange uses 2nd/98th percentiles, not min/max', () => {
    const locs = [];
    for (let i = 0; i < 100; i++) locs.push({ z: i });
    locs.push({ z: -10000 }); // one out-of-bounds loc must not squash the range
    const { lo, hi } = computeMapZRange(locs);
    assert.ok(lo > -10000, `lo ${lo} should ignore the outlier`);
    assert.ok(hi >= 96 && hi <= 99, `hi ${hi} should sit near the top`);
});

test('computeMapZRange on no locations is a zero range', () => {
    assert.deepEqual(computeMapZRange([]), { lo: 0, hi: 0 });
    assert.deepEqual(computeMapZRange(null), { lo: 0, hi: 0 });
});

// Two triangles sharing the diagonal of a unit-ish square: the shared edge is
// interior (used twice) and must not produce a wall; the four outer edges must.
const SQUARE = [
    0, 0, 0, 10, 0, 0, 10, 10, 0,
    0, 0, 0, 10, 10, 0, 0, 10, 0,
];

test('floorBoundaryEdges drops interior edges and keeps the perimeter', () => {
    const edges = floorBoundaryEdges(SQUARE, []);
    assert.equal(edges.length, 4);
    for (const e of edges) {
        assert.ok(Math.abs(Math.hypot(e.nx, e.ny) - 1) < 1e-6, 'normal is unit length');
    }
});

test('floorBoundaryEdges welds coincident vertices across separate groups', () => {
    // Same square, split into one triangle in the backdrop and one in a group.
    const back = SQUARE.slice(0, 9);
    const group = [{ tris: SQUARE.slice(9) }];
    assert.equal(floorBoundaryEdges(back, group).length, 4);
});

test('floorBoundaryWalls extrudes each boundary edge into two triangles', () => {
    const walls = floorBoundaryWalls(SQUARE, []);
    assert.equal(walls.length, 4 * 2 * 9, 'four edges, two tris each, 9 floats per tri');
    // Every wall reaches exactly FLOOR_SLAB_DEPTH below the floor.
    const zs = [];
    for (let i = 2; i < walls.length; i += 3) zs.push(walls[i]);
    assert.equal(Math.min(...zs), -FLOOR_SLAB_DEPTH);
    assert.equal(Math.max(...zs), 0);
});

test('floorBoundaryEdges ignores degenerate triangle lists', () => {
    assert.deepEqual(floorBoundaryEdges(null, []), []);
    assert.deepEqual(floorBoundaryEdges([1, 2, 3], []), []);
});

const MOVER = { t: [0, 1000, 5000], x: [1, 2, 3], y: [10, 20, 30], z: [0, 64, 128], vis: [true, true, false] };

test('moverPoseAt carries the last sample forward and clamps before the first', () => {
    assert.deepEqual(moverPoseAt(MOVER, -1), { x: 1, y: 10, z: 0, vis: true });
    assert.deepEqual(moverPoseAt(MOVER, 1000), { x: 2, y: 20, z: 64, vis: true });
    assert.deepEqual(moverPoseAt(MOVER, 4999), { x: 2, y: 20, z: 64, vis: true });
    assert.deepEqual(moverPoseAt(MOVER, 99999), { x: 3, y: 30, z: 128, vis: false });
});

test('moverPoseAt on an empty track is null', () => {
    assert.equal(moverPoseAt({ t: [] }, 0), null);
    assert.equal(moverPoseAt({}, 0), null);
});
