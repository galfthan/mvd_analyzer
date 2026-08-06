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
