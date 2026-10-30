import test from 'node:test';
import assert from 'node:assert/strict';

import {
    processLocationGroups, computeRegionOutline, groupWorldBBox,
    computeRegionStacking, buildFloorModel,
    REGION_STACK_Z_EPS, FLOOR_TOP_ALPHA,
} from '../src/locgroups.js';

const LOCS = [
    { name: 'RA entry', x: 0,   y: 0,   z: 0 },
    { name: 'ra.entry', x: 100, y: 0,   z: 64 },   // same loc, different spelling
    { name: 'bridge',   x: 500, y: 500, z: 128 },
];

test('processLocationGroups folds spellings of one loc into a single group', () => {
    const { groups, byName } = processLocationGroups(LOCS, null);
    assert.equal(groups.length, 2);
    assert.ok(byName['RA.entry'], 'keyed by the canonical name');
    assert.equal(byName['RA.entry'].points.length, 2);
});

test('processLocationGroups averages the centroid including z', () => {
    const { byName } = processLocationGroups(LOCS, null);
    assert.deepEqual(byName['RA.entry'].centroid, { x: 50, y: 0, z: 32 });
});

test('processLocationGroups attaches geometry by canonical name, skipping the backdrop', () => {
    const tris = new Array(9).fill(0);
    const geom = { locs: [
        { name: 'RA.entry', tris },
        { name: '', tris },              // unnamed backdrop bucket
        { name: 'bridge', tris: [1, 2] }, // too short to be a triangle
    ] };
    const { byName } = processLocationGroups(LOCS, geom);
    assert.equal(byName['RA.entry'].tris, tris);
    assert.equal(byName['bridge'].tris, null);
});

// Two triangles forming a square; the shared diagonal is interior.
const SQUARE = [
    0, 0, 0, 10, 0, 0, 10, 10, 0,
    0, 0, 0, 10, 10, 0, 0, 10, 0,
];

test('computeRegionOutline keeps only boundary edges and memoizes', () => {
    const group = { tris: SQUARE };
    const outline = computeRegionOutline(group);
    assert.equal(outline.length, 4 * 6, 'four edges, two xyz endpoints each');
    assert.equal(group.outlineNormals.length, 4 * 2);
    assert.equal(computeRegionOutline(group), outline, 'second call is cached');
});

test('computeRegionOutline is null for a group with no usable geometry', () => {
    assert.equal(computeRegionOutline({ tris: null }), null);
    assert.equal(computeRegionOutline({ tris: [1, 2, 3] }), null);
});

test('computeRegionOutline normals point away from the region interior', () => {
    const group = { tris: SQUARE };
    const outline = computeRegionOutline(group);
    const n = group.outlineNormals;
    const interior = { x: 5, y: 5 };
    for (let e = 0; e < outline.length / 6; e++) {
        const mx = (outline[e * 6] + outline[e * 6 + 3]) / 2;
        const my = (outline[e * 6 + 1] + outline[e * 6 + 4]) / 2;
        const dot = n[e * 2] * (interior.x - mx) + n[e * 2 + 1] * (interior.y - my);
        assert.ok(dot <= 0, `edge ${e} normal points inward`);
    }
});

test('groupWorldBBox bounds the triangles and memoizes', () => {
    const group = { tris: SQUARE };
    assert.deepEqual(groupWorldBBox(group), { minX: 0, maxX: 10, minY: 0, maxY: 10 });
    assert.equal(groupWorldBBox(group), group._wbbox);
});

const box = (x0, y0, z) => ({
    points: [
        { x: x0, y: y0, z }, { x: x0 + 100, y: y0, z },
        { x: x0 + 100, y: y0 + 100, z }, { x: x0, y: y0 + 100, z },
    ],
});

test('computeRegionStacking links a region to the ones overlapping above it', () => {
    const lower = box(0, 0, 0);
    const upper = box(10, 10, REGION_STACK_Z_EPS + 100);   // overlaps, clearly higher
    const beside = box(500, 500, REGION_STACK_Z_EPS + 100); // higher but no overlap
    const regions = [lower, upper, beside];
    computeRegionStacking(regions);
    assert.deepEqual(lower._above, [upper]);
    assert.deepEqual(upper._above, []);
    assert.deepEqual(beside._above, []);
});

test('computeRegionStacking ignores a region only a step above', () => {
    const lower = box(0, 0, 0);
    const barely = box(0, 0, REGION_STACK_Z_EPS - 1);
    computeRegionStacking([lower, barely]);
    assert.deepEqual(lower._above, []);
});

test('computeRegionStacking takes the median z, not the mean', () => {
    const r = { points: [{ x: 0, y: 0, z: 0 }, { x: 1, y: 1, z: 10 }, { x: 2, y: 2, z: 10000 }] };
    computeRegionStacking([r]);
    assert.equal(r._z, 10);
});

test('buildFloorModel emits one entry per triangle plus extruded box sides', () => {
    const geom = { backdropTris: SQUARE };
    const model = buildFloorModel(geom, []);
    // 2 backdrop triangles + 4 boundary edges x 2 triangles of side wall.
    assert.equal(model.entries.length, 2 + 8);
    assert.equal(model.geom, geom);
    assert.equal(model.sortedFor, null);
    for (const e of model.entries) {
        assert.ok(e.fill.includes(String(FLOOR_TOP_ALPHA)) || e.fill.startsWith('rgba('));
        assert.ok(Number.isFinite(e.cx) && Number.isFinite(e.cy) && Number.isFinite(e.cz));
    }
});

test('buildFloorModel carries the loc name on region tops and null on the shell', () => {
    const model = buildFloorModel({ backdropTris: SQUARE }, [{ name: 'RA.entry', tris: SQUARE }]);
    const named = model.entries.filter(e => e.name === 'RA.entry');
    assert.equal(named.length, 2, 'the region contributes its own two triangles');
    assert.ok(model.entries.some(e => e.name === null), 'backdrop and sides are unnamed');
});

test('buildFloorModel is null when there is no geometry at all', () => {
    assert.equal(buildFloorModel(null, []), null);
    assert.equal(buildFloorModel({ backdropTris: [1, 2, 3] }, []), null);
});
