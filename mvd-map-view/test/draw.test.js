import test from 'node:test';
import assert from 'node:assert/strict';

import {
    drawTriangleListFill, renderSolidEntries, drawMoverMesh, drawLiquidVolume,
    drawWorldArrow, drawArrow, PLAYER_SYMBOL_BASE_SIZE,
} from '../src/draw.js';
import { newCamera, refreshTrig, setAngles, fit, project, PITCH_MAX } from '../src/camera.js';
import { buildFloorModel } from '../src/locgroups.js';

// A canvas2d stand-in that records the call sequence. The primitives are pure
// emitters — given the same inputs they must issue the same calls — so this
// makes their behaviour (batching, culling, sort order) assertable in node,
// where there is no canvas at all.
function recorder() {
    const calls = [];
    const rec = (name) => (...args) => calls.push([name, ...args]);
    const ctx = {
        calls,
        beginPath: rec('beginPath'), closePath: rec('closePath'),
        moveTo: rec('moveTo'), lineTo: rec('lineTo'), arc: rec('arc'),
        fill: rec('fill'), stroke: rec('stroke'),
        save: rec('save'), restore: rec('restore'),
        fillText: rec('fillText'), strokeText: rec('strokeText'),
        measureText: () => ({ actualBoundingBoxAscent: 20, actualBoundingBoxDescent: 0 }),
    };
    // Style setters are plain properties; record assignments so fill batching
    // is visible in the same stream as the path calls.
    for (const prop of ['fillStyle', 'strokeStyle', 'lineWidth', 'font', 'textAlign',
                        'textBaseline', 'lineJoin', 'lineCap']) {
        let v;
        Object.defineProperty(ctx, prop, {
            get: () => v,
            set: (nv) => { v = nv; calls.push(['set:' + prop, nv]); },
        });
    }
    return ctx;
}

const camera = () => {
    const w = newCamera();
    fit(w, { minX: -100, maxX: 100, minY: -100, maxY: 100 }, 200, 200);
    refreshTrig(w);
    return w;
};
const toCanvas = (w) => (x, y, z) => project(w, x, y, z, { x: 0, y: 0, depth: 0 });
const count = (ctx, name) => ctx.calls.filter(c => c[0] === name).length;

const SQUARE = [
    0, 0, 0, 10, 0, 0, 10, 10, 0,
    0, 0, 0, 10, 10, 0, 0, 10, 0,
];

test('drawTriangleListFill batches every triangle into one filled path', () => {
    const ctx = recorder();
    drawTriangleListFill(ctx, SQUARE, '#abc', toCanvas(camera()));
    assert.equal(count(ctx, 'beginPath'), 1, 'one path for the whole list');
    assert.equal(count(ctx, 'fill'), 1, 'one fill for the whole list');
    assert.equal(count(ctx, 'closePath'), 2, 'one sub-path per triangle');
    assert.equal(count(ctx, 'moveTo'), 2);
    assert.equal(count(ctx, 'lineTo'), 4);
});

test('drawTriangleListFill no-ops on an empty or degenerate list', () => {
    const ctx = recorder();
    drawTriangleListFill(ctx, null, '#abc', toCanvas(camera()));
    drawTriangleListFill(ctx, [1, 2, 3], '#abc', toCanvas(camera()));
    assert.equal(ctx.calls.length, 0);
});

test('renderSolidEntries sorts back-to-front and re-sorts only on angle change', () => {
    const w = camera();
    setAngles(w, 0.5, 0.7);
    const model = buildFloorModel({ backdropTris: SQUARE }, []);
    renderSolidEntries(recorder(), model, w, toCanvas(w), null);
    const key = model.sortedFor;
    assert.ok(key, 'sort is memoized by camera angle');
    for (let i = 1; i < model.entries.length; i++) {
        assert.ok(model.entries[i - 1].depth <= model.entries[i].depth, 'painter order');
    }
    // Panning must not invalidate the sort; rotating must.
    w.panX += 100;
    renderSolidEntries(recorder(), model, w, toCanvas(w), null);
    assert.equal(model.sortedFor, key);
    setAngles(w, 1.5, 0.7);
    renderSolidEntries(recorder(), model, w, toCanvas(w), null);
    assert.notEqual(model.sortedFor, key);
});

test('renderSolidEntries opens one path per fill colour, not per triangle', () => {
    const w = camera();
    const model = buildFloorModel({ backdropTris: SQUARE }, []);
    const ctx = recorder();
    renderSolidEntries(ctx, model, w, toCanvas(w), null);
    const fills = new Set(model.entries.map(e => e.fill));
    assert.equal(count(ctx, 'beginPath'), fills.size, 'one path per distinct fill');
    // Each batch is filled and then stroked in its own colour to seal the
    // anti-aliasing seams between adjacent triangles.
    assert.equal(count(ctx, 'fill'), fills.size);
    assert.equal(count(ctx, 'stroke'), fills.size);
});

test('renderSolidEntries uses the faded tone exactly for locs the predicate rejects', () => {
    const w = camera();
    const model = buildFloorModel({ backdropTris: SQUARE }, [{ name: 'RA.entry', tris: SQUARE }]);
    const ctx = recorder();
    renderSolidEntries(ctx, model, w, toCanvas(w), (name) => name === 'RA.entry');
    const faded = new Set(model.entries.filter(e => e.name === 'RA.entry').map(e => e.fillFaded));
    const used = ctx.calls.filter(c => c[0] === 'set:fillStyle').map(c => c[1]);
    for (const f of faded) assert.ok(used.includes(f), 'faded tone was selected');
});

// A two-triangle mesh with opposing normals: exactly one faces the camera.
const MOVER_FACES = [
    { off: 0, nx: 0, ny: -1, nz: 0 },
    { off: 9, nx: 0, ny: 1, nz: 0 },
];
const MOVER_MESH = [...SQUARE];

test('drawMoverMesh culls back faces against the view direction', () => {
    const w = camera();
    setAngles(w, 0, PITCH_MAX - 0.3);
    const ctx = recorder();
    drawMoverMesh(ctx, MOVER_MESH, MOVER_FACES, { x: 0, y: 0, z: 0 }, '#fff', w, toCanvas(w));
    assert.equal(count(ctx, 'closePath'), 1, 'only the front-facing triangle is drawn');
    // Spin 180° and the other face is the visible one — still exactly one.
    setAngles(w, Math.PI, PITCH_MAX - 0.3);
    const ctx2 = recorder();
    drawMoverMesh(ctx2, MOVER_MESH, MOVER_FACES, { x: 0, y: 0, z: 0 }, '#fff', w, toCanvas(w));
    assert.equal(count(ctx2, 'closePath'), 1);
});

test('drawMoverMesh offsets the mesh by the pose', () => {
    const w = camera();
    setAngles(w, 0, PITCH_MAX - 0.3);
    const at = (pose) => {
        const ctx = recorder();
        drawMoverMesh(ctx, MOVER_MESH, MOVER_FACES, pose, '#fff', w, toCanvas(w));
        return ctx.calls.find(c => c[0] === 'moveTo');
    };
    const a = at({ x: 0, y: 0, z: 0 });
    const b = at({ x: 64, y: 0, z: 0 });
    assert.notEqual(a[1], b[1], 'a moved lift draws somewhere else');
});

test('drawLiquidVolume draws every face, back to front', () => {
    const w = camera();
    setAngles(w, 0.3, 0.8);
    const tris = [
        ...SQUARE,                                     // z = 0
        0, 0, 90, 10, 0, 90, 10, 10, 90,               // higher, so nearer the camera
    ];
    const ctx = recorder();
    drawLiquidVolume(ctx, tris, [10, 20, 30], 0.15, [0, 0, 1], w, toCanvas(w));
    assert.equal(count(ctx, 'fill'), 3, 'one fill per face — translucency needs separate draws');
    // The high face must be filled last so it composites over the low ones.
    const ys = ctx.calls.filter(c => c[0] === 'moveTo').map(c => c[2]);
    assert.ok(ys[ys.length - 1] < ys[0], 'nearest (highest on screen) face is drawn last');
});

test('drawWorldArrow projects both endpoints and skips sub-pixel arrows', () => {
    const w = camera();
    const toNew = toCanvas(w);
    const ctx = recorder();
    drawWorldArrow(ctx, 0, 0, 0, 50, 0, 0, '#fff', 2, toNew);
    assert.equal(count(ctx, 'stroke'), 1);
    assert.equal(count(ctx, 'fill'), 1, 'the head is a filled triangle');

    const tiny = recorder();
    drawWorldArrow(tiny, 0, 0, 0, 0.001, 0, 0, '#fff', 2, toNew);
    assert.equal(tiny.calls.filter(c => c[0] === 'stroke').length, 0, 'degenerate arrow is skipped');
});

test('drawArrow emits a shaft and a filled head', () => {
    const ctx = recorder();
    drawArrow(ctx, 0, 0, 100, 0, 8);
    assert.equal(count(ctx, 'stroke'), 1);
    assert.equal(count(ctx, 'fill'), 1);
    assert.equal(count(ctx, 'beginPath'), 2);
});

test('the player symbol base size is the value every stroke scales from', () => {
    // A wrong constant here rescales every player symbol on the map, which is
    // the sort of thing a screenshot diff catches and a unit test should pin.
    assert.equal(PLAYER_SYMBOL_BASE_SIZE, 32);
});
