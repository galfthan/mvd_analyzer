import test from 'node:test';
import assert from 'node:assert/strict';

import {
    parseColor, makeWorldTransform, entryDepth,
    buildEntryVertices, sortedIndices, buildLiquidFaces,
    isSoftwareRenderer, GlWorld, DEPTH_SCALE,
} from '../src/glworld.js';
import { newCamera, refreshTrig, fit, project } from '../src/camera.js';

test('parseColor premultiplies and memoises', () => {
    assert.deepEqual(parseColor('rgba(255, 0, 0, 0.5)'), [0.5, 0, 0, 0.5]);
    assert.deepEqual(parseColor('rgb(0, 255, 0)'), [0, 1, 0, 1]);
    assert.equal(parseColor('rgba(255, 0, 0, 0.5)'), parseColor('rgba(255, 0, 0, 0.5)'),
        'same string → same array instance');
    assert.deepEqual(parseColor('#nonsense'), [0, 0, 0, 1], 'unparsable → opaque black');
});

// The GL vertex shader must land every vertex exactly where project() puts
// it. Fold a fully exercised camera (rotation, tilt, zoom, pan, off-centre
// fit) and compare the row-vector transform against project() over a grid.
test('makeWorldTransform reproduces project() at any camera pose', () => {
    const w = newCamera();
    fit(w, { minX: -512, maxX: 1024, minY: -256, maxY: 768 }, 850, 700);
    w.yaw = 0.83;
    w.pitch = 0.61;
    w.zoomK = 2.3;
    w.panX = -37.5;
    w.panY = 12.25;
    w.zMid = 96;
    refreshTrig(w);
    const t = makeWorldTransform(w, 850, 700);
    const pt = { x: 0, y: 0, depth: 0 };
    for (const [x, y, z] of [[0, 0, 0], [-512, 768, 320], [1024, -256, -128], [333, 421, 47]]) {
        const p = project(w, x, y, z, pt);
        const clipX = t.rx[0] * x + t.rx[1] * y + t.rx[2] * z + t.rx[3];
        const clipY = t.ry[0] * x + t.ry[1] * y + t.ry[2] * z + t.ry[3];
        // Clip → CSS px, the inverse of what the viewport applies.
        const sx = (clipX + 1) / 2 * 850;
        const sy = (1 - clipY) / 2 * 700;
        assert.ok(Math.abs(sx - p.x) < 1e-6, `x: ${sx} vs ${p.x}`);
        assert.ok(Math.abs(sy - p.y) < 1e-6, `y: ${sy} vs ${p.y}`);
        assert.ok(Math.abs(entryDepth(w, x, y, z) - p.depth) < 1e-9, 'depth matches too');
        // The clip-z row: nearer (bigger closeness) must produce SMALLER
        // clip z so it wins the LESS depth test.
        const clipZ = t.rz[0] * x + t.rz[1] * y + t.rz[2] * z + t.rz[3];
        assert.ok(Math.abs(clipZ - (-p.depth * DEPTH_SCALE)) < 1e-9, 'clip z is scaled negated closeness');
    }
});

test('forceOpaque unpremultiplies and snaps alpha to one', () => {
    const tris = [0, 0, 0, 1, 0, 0, 1, 1, 0];
    const entries = [{ tris, off: 0, cx: 0, cy: 0, cz: 0, fill: 'rgba(100, 200, 50, 0.95)' }];
    const { verts } = buildEntryVertices(entries, (e) => e.fill, true);
    assert.ok(Math.abs(verts[3] - 100 / 255) < 1e-6, 'red back at full strength');
    assert.equal(verts[6], 1, 'alpha snapped to 1');
});

test('buildEntryVertices interleaves positions with premultiplied colours', () => {
    const tris = [0, 0, 0, 10, 0, 0, 10, 10, 4];
    const entries = [{ tris, off: 0, cx: 20 / 3, cy: 10 / 3, cz: 4 / 3, fill: 'rgba(100, 200, 50, 0.5)' }];
    const { verts, centroids } = buildEntryVertices(entries, (e) => e.fill);
    assert.equal(verts.length, 21);
    assert.deepEqual([...verts.slice(0, 3)], [0, 0, 0]);
    assert.ok(Math.abs(verts[3] - (100 / 255) * 0.5) < 1e-6, 'premultiplied red');
    assert.ok(Math.abs(verts[6] - 0.5) < 1e-6, 'alpha');
    assert.deepEqual([...verts.slice(14, 17)], [10, 10, 4], 'third vertex position');
    assert.ok(Math.abs(centroids[0] - 20 / 3) < 1e-5);
});

test('sortedIndices orders entries back to front for the camera', () => {
    const w = newCamera();   // top-down: depth = z - zMid, so lower z first
    refreshTrig(w);
    // Three entries at z = 50, 10, 90.
    const centroids = new Float32Array([0, 0, 50, 0, 0, 10, 0, 0, 90]);
    const idx = sortedIndices(w, centroids);
    // Entry 1 (z=10) first, then 0 (z=50), then 2 (z=90).
    assert.deepEqual([...idx], [3, 4, 5, 0, 1, 2, 6, 7, 8]);
});

test('buildLiquidFaces quantises the shade exactly like drawLiquidVolume', () => {
    const light = [0, 0, 1];
    // A flat top face (normal +z): |n·l| = 1 → shade 1.0.
    const flat = { kind: 'water', tris: [0, 0, 0, 10, 0, 0, 10, 10, 0] };
    // A vertical wall (normal in xy): |n·l| = 0 → shade 0.5.
    const wall = { kind: 'lava', tris: [0, 0, 0, 10, 0, 0, 10, 0, 10] };
    const faces = buildLiquidFaces([flat, wall, null, { kind: 'slime', tris: [1] }],
        { water: [64, 128, 255], lava: [255, 120, 40] }, 0.15, light);
    assert.equal(faces.length, 2, 'degenerate/null volumes dropped');
    assert.equal(faces[0].fill, 'rgba(64, 128, 255, 0.15)');
    assert.equal(faces[1].fill, `rgba(${Math.round(255 * 0.5)}, ${Math.round(120 * 0.5)}, ${Math.round(40 * 0.5)}, 0.15)`);
});

test('a software rasteriser is refused unless explicitly allowed', () => {
    assert.equal(isSoftwareRenderer('Google SwiftShader'), true);
    assert.equal(isSoftwareRenderer('llvmpipe (LLVM 17.0.6, 256 bits)'), true);
    assert.equal(isSoftwareRenderer('ANGLE (NVIDIA GeForce RTX 4070)'), false);
    assert.equal(isSoftwareRenderer(''), false);
    // create() consults the renderer string before building the program, so
    // a stub context with only the identity APIs is enough to prove the gate.
    const softGl = {
        getExtension: (name) => name === 'WEBGL_debug_renderer_info'
            ? { UNMASKED_RENDERER_WEBGL: 0x9246 } : null,
        getParameter: () => 'Google SwiftShader',
    };
    const canvas = { getContext: (kind) => (kind === 'webgl2' ? softGl : null) };
    assert.equal(GlWorld.create(canvas), null, 'software GL → 2D fallback');
    assert.equal(GlWorld.create(canvas, { allowSoftware: true }) === null, true,
        'allowSoftware proceeds past the gate (and then fails on the stub program build)');
});
