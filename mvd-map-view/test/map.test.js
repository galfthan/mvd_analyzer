import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';

import { MvdMap, newState } from '../src/map.js';

test('a fresh map has its own state and camera', () => {
    const a = new MvdMap();
    const b = new MvdMap();
    assert.notEqual(a.state, b.state, 'state is per-instance, not shared module scope');
    assert.notEqual(a.camera, b.camera);
    a.state.currentTime = 500;
    assert.equal(b.state.currentTime, 0);
});

test('nested state defaults are not shared between instances', () => {
    const a = new MvdMap();
    const b = new MvdMap();
    a.state.entityFilters.spawn = true;
    a.state.bounds.minX = -99;
    assert.equal(b.state.entityFilters.spawn, false);
    assert.equal(b.state.bounds.minX, 0);
});

test('attach binds a canvas and its 2d context', () => {
    const ctx = {};
    const canvas = { getContext: (kind) => (kind === '2d' ? ctx : null) };
    const map = new MvdMap(canvas);
    assert.equal(map.state.canvas, canvas);
    assert.equal(map.state.ctx, ctx);
});

test('constructing without a canvas leaves the surface unbound', () => {
    const map = new MvdMap();
    assert.equal(map.state.canvas, null);
    assert.equal(map.state.ctx, null);
});

test('the clock starts at zero and paused', () => {
    const s = newState();
    assert.equal(s.currentTime, 0);
    assert.equal(s.isPlaying, false);
    assert.equal(s.playbackSpeed, 1);
});

// The loader invariant (see README): the MCP Apps viewer runs behind a CSP
// with no network origins and inside a host-sized iframe, so a fetch or a
// document lookup anywhere in this package breaks it — and breaks it only
// there, invisibly to every other test. Pin it mechanically.
test('no module in the package loads anything or touches the DOM', () => {
    const dir = new URL('../src/', import.meta.url);
    const banned = [
        /\bfetch\s*\(/, /\bXMLHttpRequest\b/, /\bimport\s*\(/,
        /\bdocument\s*\./, /\bwindow\s*\./, /\blocation\s*\./,
        /\blocalStorage\b/, /\bnavigator\s*\./,
    ];
    for (const file of readdirSync(dir).filter(f => f.endsWith('.js'))) {
        const src = readFileSync(new URL(file, dir), 'utf8')
            // Strip comments — the rule is about code, and the comments
            // discuss exactly these words.
            .replace(/\/\*[\s\S]*?\*\//g, '')
            .split('\n').filter(l => !l.trim().startsWith('//')).join('\n');
        for (const re of banned) {
            assert.equal(re.test(src), false, `${file} violates the loader invariant: ${re}`);
        }
    }
});

test('the package uses no webfonts — only generic families', () => {
    const dir = new URL('../src/', import.meta.url);
    for (const file of readdirSync(dir).filter(f => f.endsWith('.js'))) {
        const src = readFileSync(new URL(file, dir), 'utf8');
        for (const m of src.matchAll(/font\s*=\s*[`'"]([^`'"]*)[`'"]/g)) {
            assert.match(m[1], /(monospace|sans-serif|serif)\s*$/,
                `${file} names a specific font family: ${m[1]}`);
        }
    }
});

// ─── World-layer behaviour ──────────────────────────────────────────────────

test('focusTier classifies the focused loc, its neighbours and the rest', () => {
    const map = new MvdMap();
    assert.equal(map.focusTier('RA.entry'), null, 'no focus → no tier');
    map.state.focusGroupName = 'RA.entry';
    map.state.focusNeighbors = new Set(['RA.tunnel']);
    assert.equal(map.focusTier('RA.entry'), 'focus');
    assert.equal(map.focusTier('RA.tunnel'), 'near');
    assert.equal(map.focusTier('bridge'), 'far');
});

test('farFadePredicate is null unless something is focused', () => {
    const map = new MvdMap();
    assert.equal(map.farFadePredicate(), null);
    map.state.focusGroupName = 'RA.entry';
    const p = map.farFadePredicate();
    assert.equal(p('bridge'), true);
    assert.equal(p('RA.entry'), false);
});

test('tierFill brightens the focus, fades the far, and leaves the base alone', () => {
    const map = new MvdMap();
    const group = { color: { fill: 'rgba(10,20,30,0.1)' } };
    assert.equal(map.tierFill(group, null), 'rgba(10,20,30,0.1)');
    const focus = parseFloat(map.tierFill(group, 'focus').split(',')[3]);
    const near  = parseFloat(map.tierFill(group, 'near').split(',')[3]);
    const far   = parseFloat(map.tierFill(group, 'far').split(',')[3]);
    assert.ok(focus > near && near > 0.1 && far < 0.1, `${focus} ${near} ${far}`);
    assert.ok(focus <= 0.5, 'focus alpha is capped so a region never goes opaque');
});

test('iconScale ramps with zoom and caps at 1.5', () => {
    const map = new MvdMap();
    assert.equal(map.iconScale(), 1);
    map.camera.zoomK = 0.5;
    assert.equal(map.iconScale(), 1, 'zooming out never shrinks labels');
    map.camera.zoomK = 2;
    assert.ok(map.iconScale() > 1 && map.iconScale() < 1.5);
    map.camera.zoomK = 100;
    assert.equal(map.iconScale(), 1.5);
});

test('floorModel rebuilds only when geometry or groups change', () => {
    const map = new MvdMap();
    const tris = [0, 0, 0, 10, 0, 0, 10, 10, 0];
    map.state.mapGeometry = { backdropTris: tris };
    map.state.locationGroups = [];
    const first = map.floorModel();
    assert.ok(first);
    assert.equal(map.floorModel(), first, 'cached across calls');
    map.state.locationGroups = [{ name: 'RA.entry', tris }];
    assert.notEqual(map.floorModel(), first, 'new groups invalidate it');
    map.state.mapGeometry = { backdropTris: tris };   // different object
    assert.notEqual(map.floorModel(), first, 'new geometry invalidates it');
});

test('mover face normals and bboxes are computed once per submodel', () => {
    const map = new MvdMap();
    map.state.submodelMeshes = { 7: [0, 0, 0, 10, 0, 0, 10, 10, 4] };
    const faces = map.moverMeshFaces(7);
    assert.equal(faces.length, 1);
    assert.equal(map.moverMeshFaces(7), faces, 'cached by id');
    const bb = map.moverLocalBBox(7);
    assert.deepEqual(bb, { minX: 0, maxX: 10, minY: 0, maxY: 10, minZ: 0, maxZ: 4 });
    assert.equal(map.moverLocalBBox(7), bb);
});

test('playerOnMover needs both the footprint and the height window', () => {
    const map = new MvdMap();
    map.state.submodelMeshes = { 1: [0, 0, 0, 100, 0, 0, 100, 100, 0] };
    const pose = { x: 0, y: 0, z: 0 };
    const on = (p) => map.playerOnMover(pose, 1, [p]);
    assert.equal(on({ x: 50, y: 50, z: 10 }), true, 'standing on it');
    assert.equal(on({ x: 500, y: 50, z: 10 }), false, 'beside it');
    assert.equal(on({ x: 50, y: 50, z: 500 }), false, 'far above it');
    assert.equal(on({ x: 50, y: 50, z: -500 }), false, 'far below it');
});

test('livingPlayersAtFrame drops the dead and the unpositioned', () => {
    const map = new MvdMap();
    map.state._framePlayerData = {
        alive:   { x: 1, y: 2, z: 3, h: 100 },
        dead:    { x: 1, y: 2, z: 3, d: true },
        zeroHp:  { x: 1, y: 2, z: 3, h: 0 },
        noPos:   { x: 0, y: 0, z: 0, h: 100 },
        missing: null,
    };
    const living = map.livingPlayersAtFrame();
    assert.equal(living.length, 1);
    assert.equal(living[0].h, 100);
});

test('livingPlayersAtFrame is empty before a frame has been composed', () => {
    assert.deepEqual(new MvdMap().livingPlayersAtFrame(), []);
});

test('the world bake takes its scratch canvas from the target canvas document', () => {
    // Not `document.createElement` — the package must not touch the ambient
    // document (loader invariant), and in an MCP iframe the canvas may belong
    // to a document this code never sees a global for.
    let asked = 0;
    const canvas = {
        getContext: () => ({}),
        ownerDocument: { createElement: (t) => { asked++; return { tag: t }; } },
    };
    const map = new MvdMap(canvas);
    assert.deepEqual(map.scratchCanvas(), { tag: 'canvas' });
    assert.equal(asked, 1);
});
