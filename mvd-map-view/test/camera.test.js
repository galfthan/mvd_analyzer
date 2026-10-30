import test from 'node:test';
import assert from 'node:assert/strict';

import {
    PITCH_MAX, PITCH_MIN, YAW_SNAP, DEFAULT_PITCH, DEFAULT_YAW,
    newCamera, is3D, refreshTrig, setAngles, fit, project, toView, toWorld,
    setOrbitCenter,
} from '../src/camera.js';

const BOUNDS = { minX: -1000, maxX: 1000, minY: -500, maxY: 500 };

function fitted(cssW = 800, cssH = 600) {
    const w = newCamera();
    fit(w, BOUNDS, cssW, cssH);
    refreshTrig(w);
    return w;
}

const p = (w, x, y, z) => project(w, x, y, z, { x: 0, y: 0, depth: 0 });

test('a fresh camera is top-down and not 3D', () => {
    const w = newCamera();
    assert.equal(w.pitch, PITCH_MAX);
    assert.equal(is3D(w), false);
});

test('is3D trips on either a tilt or a spin', () => {
    const w = newCamera();
    setAngles(w, 0, PITCH_MAX - 0.2);
    assert.equal(is3D(w), true);
    setAngles(w, 0.5, PITCH_MAX);
    assert.equal(is3D(w), true);
});

test('fit centres the world in the canvas and preserves pan/zoom/angles', () => {
    const w = fitted(800, 600);
    // 2000x1000 world into 800x600: the width binds, scale = 0.4.
    assert.equal(w.scale, 0.4);
    assert.equal(w.offsetX, 0);
    assert.equal(w.offsetY, (600 - 1000 * 0.4) / 2);
    assert.equal(w.cx, 0);
    assert.equal(w.cy, 0);

    w.panX = 17; w.panY = -3; w.zoomK = 2.5;
    setAngles(w, 0.3, 0.9);
    fit(w, BOUNDS, 400, 300);
    assert.equal(w.panX, 17);
    assert.equal(w.panY, -3);
    assert.equal(w.zoomK, 2.5);
    assert.equal(w.pitch, 0.9);
});

test('at top-down the projection is the plain 2D transform and depth is z', () => {
    const w = fitted();
    const a = p(w, 0, 0, 0);
    assert.equal(a.x, w.offsetX + (0 - w.minX) * w.scale);
    assert.equal(a.y, w.canvasH - w.offsetY - (0 - w.minY) * w.scale);
    // depth degenerates to height above the orbit centre, so the pre-3D
    // z-sort semantics carry over unchanged.
    assert.ok(p(w, 0, 0, 100).depth > p(w, 0, 0, 0).depth);
    // Screen position must not depend on z at top-down.
    assert.equal(p(w, 50, 50, 999).x, p(w, 50, 50, 0).x);
});

test('yaw spins the map about the orbit centre', () => {
    const w = fitted();
    setAngles(w, Math.PI / 2, PITCH_MAX);
    const east = p(w, 500, 0, 0);
    const north = p(w, 0, 500, 0);
    // A quarter turn maps +x onto where +y used to draw.
    assert.ok(Math.abs(east.y - p(fitted(), 0, 500, 0).y) < 1e-9 ||
              Math.abs(east.x - p(fitted(), 0, 500, 0).x) < 1e-9,
              'the +x point lands on an axis the unrotated +y point used');
    assert.ok(Number.isFinite(north.x) && Number.isFinite(north.y));
});

test('setAngles snaps yaw to the cardinals only inside the margin', () => {
    const w = newCamera();
    setAngles(w, YAW_SNAP * 0.4, PITCH_MAX);
    assert.equal(w.yaw, 0, 'inside the margin snaps to 0');
    setAngles(w, Math.PI / 2 + YAW_SNAP * 0.4, PITCH_MAX);
    assert.ok(Math.abs(w.yaw - Math.PI / 2) < 1e-12);
    const off = YAW_SNAP * 3;
    setAngles(w, off, PITCH_MAX);
    assert.ok(Math.abs(w.yaw - off) < 1e-12, 'outside the margin is left alone');
});

test('setAngles normalizes yaw into (-π, π] and clamps pitch', () => {
    const w = newCamera();
    setAngles(w, 5 * Math.PI, 1);
    assert.ok(w.yaw > -Math.PI - 1e-9 && w.yaw <= Math.PI + 1e-9, `yaw ${w.yaw}`);
    setAngles(w, 0, 99);
    assert.equal(w.pitch, PITCH_MAX);
    setAngles(w, 0, -99);
    assert.equal(w.pitch, PITCH_MIN);
});

test('setAngles refreshes the cached trig', () => {
    const w = newCamera();
    setAngles(w, DEFAULT_YAW, DEFAULT_PITCH);
    assert.ok(Math.abs(w.sinYaw - Math.sin(DEFAULT_YAW)) < 1e-12);
    assert.ok(Math.abs(w.cosPitch - Math.cos(DEFAULT_PITCH)) < 1e-12);
});

test('toWorld inverts project on the orbit plane, at every camera angle', () => {
    for (const [yaw, pitch] of [[0, PITCH_MAX], [DEFAULT_YAW, DEFAULT_PITCH], [-2.1, 0.35]]) {
        const w = fitted();
        setAngles(w, yaw, pitch);
        w.panX = 25; w.panY = -40; w.zoomK = 1.7;
        const world = { x: 321, y: -654 };
        const scr = p(w, world.x, world.y, w.zMid);
        const back = toWorld(w, scr.x, scr.y);
        assert.ok(Math.abs(back.x - world.x) < 1e-6, `x ${back.x} vs ${world.x} @${yaw},${pitch}`);
        assert.ok(Math.abs(back.y - world.y) < 1e-6, `y ${back.y} vs ${world.y} @${yaw},${pitch}`);
    }
});

test('toWorld stays finite at a horizontal camera instead of blowing up', () => {
    const w = fitted();
    setAngles(w, 0.4, PITCH_MIN); // edge-on plane: the true inverse diverges
    const back = toWorld(w, 100, 100);
    assert.ok(Number.isFinite(back.x) && Number.isFinite(back.y));
});

test('toView inverts the linear screen part only', () => {
    const w = fitted();
    w.panX = 13; w.zoomK = 3;
    const { u, v } = toView(w, 200, 150);
    const sx = w.scale * w.zoomK;
    assert.ok(Math.abs((w.offsetX + (u - w.minX) * sx + w.panX) - 200) < 1e-9);
    assert.ok(Math.abs((w.canvasH - w.offsetY - (v - w.minY) * sx + w.panY) - 150) < 1e-9);
});

test('setOrbitCenter moves the pivot without moving the picture', () => {
    const w = fitted();
    setAngles(w, DEFAULT_YAW, DEFAULT_PITCH);
    const probes = [[0, 0, 0], [400, -250, 64], [-900, 480, -32]];
    const before = probes.map(([x, y, z]) => p(w, x, y, z));
    setOrbitCenter(w, 400, -250, 64);
    const after = probes.map(([x, y, z]) => p(w, x, y, z));
    for (let i = 0; i < probes.length; i++) {
        assert.ok(Math.abs(before[i].x - after[i].x) < 1e-6, `probe ${i} x moved`);
        assert.ok(Math.abs(before[i].y - after[i].y) < 1e-6, `probe ${i} y moved`);
    }
    assert.equal(w.cx, 400);
    assert.equal(w.zMid, 64);
});
