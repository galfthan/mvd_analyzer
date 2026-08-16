import test from 'node:test';
import assert from 'node:assert/strict';

import {
    drawPlayerSymbolAt, drawBadgesAroundCenter, PLAYER_SYMBOL_BASE_SIZE,
} from '../src/draw.js';

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

test('drawPlayerSymbolAt scales every stroke from the base size', () => {
    const ctx = recorder();
    drawPlayerSymbolAt(ctx, 'N', '#ff5050', 20, 20, PLAYER_SYMBOL_BASE_SIZE * 2);
    const arcs = ctx.calls.filter(c => c[0] === 'arc');
    assert.equal(arcs.length, 1);
    assert.equal(arcs[0][3], 26, 'radius 13 at 2x scale');
    const lw = ctx.calls.find(c => c[0] === 'set:lineWidth');
    assert.equal(lw[1], 4, 'ring stroke 2 at 2x scale');
    const text = ctx.calls.find(c => c[0] === 'fillText');
    assert.deepEqual(text.slice(1), ['N', 20, 20]);
});

test('drawBadgesAroundCenter places badges on the orbit angles', () => {
    const ctx = recorder();
    drawBadgesAroundCenter(ctx, [
        { angle: 0, letter: 'Q', color: '#00f' },
        { angle: 90, letter: 'R', color: '#f00' },
    ], 100, 100, 10, 4);
    const arcs = ctx.calls.filter(c => c[0] === 'arc');
    assert.equal(arcs.length, 2);
    assert.ok(Math.abs(arcs[0][1] - 100) < 1e-9 && Math.abs(arcs[0][2] - 90) < 1e-9,
        'angle 0 sits straight above the centre');
    assert.ok(Math.abs(arcs[1][1] - 110) < 1e-9 && Math.abs(arcs[1][2] - 100) < 1e-9,
        'angle 90 sits to the right');
});
