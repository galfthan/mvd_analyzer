import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { decodePositionPack, attachPositions } from '../src/lib/postrack.js';

// Written by the Go encoder (mvd-analytics/result/postrack_wire_test.go,
// fixtureStreams — the values below are its), so this pins the JS reader
// to the Go writer.
const fixture = () => {
    const b = readFileSync(new URL('./fixtures/position-pack.bin', import.meta.url));
    return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
};

const f32 = (/** @type {number[]} */ a) => Array.from(new Float32Array(a));

test('the Go-written pack decodes to its tracks, bit for bit', () => {
    const [bar, xy, spec] = decodePositionPack(fixture());
    assert.equal(bar.name, 'bär!');
    const p = bar.pos;
    assert.ok(p);
    assert.deepEqual(Array.from(p.t), [0, 13, -7]);
    assert.deepEqual(Array.from(p.x), [1.125, -2.5, -0]);
    assert.ok(Object.is(p.x[2], -0));
    assert.deepEqual(Array.from(p.y), [100, 200.25, -300]);
    assert.deepEqual(Array.from(p.z), [24.03125, 0, -1e9]);
    assert.deepEqual(Array.from(p.li), [3, -1, 7]);
    assert.deepEqual(Array.from(p.h), f32([0, -1e9, 31.999]));
    assert.deepEqual(Array.from(p.lq), [0, 9, -1]);
    assert.deepEqual(Array.from(p.vp), [-32768, 0, 32767]);
    assert.deepEqual(Array.from(p.vya), [16384, -16384, 1]);
    assert.deepEqual(Array.from(p.vx), f32([320, -58.333332, 0.5]));
    assert.deepEqual(Array.from(p.vy), [1, 2, 3]);
    assert.deepEqual(Array.from(p.vz), [-270, 0, 270]);
    assert.ok(p.x instanceof Float32Array && p.t instanceof Int32Array && p.lq instanceof Int8Array);

    // Only the always-present columns on a minimal track, as the JSON's
    // omitempty had it.
    assert.equal(xy.name, 'xy');
    assert.deepEqual(Object.keys(xy.pos || {}), ['t', 'x', 'y', 'z']);
    assert.deepEqual(spec, { name: 'spec', pos: null });
});

test('the columns view the pack in place', () => {
    const buf = fixture();
    const [bar] = decodePositionPack(buf);
    assert.equal(bar.pos?.x.buffer, buf);
});

test('attachPositions puts each track where the JSON had it', () => {
    const result = { streams: { players: [{ name: 'bär!' }, { name: 'xy' }, { name: 'spec' }] } };
    attachPositions(result, fixture());
    assert.deepEqual(Array.from(result.streams.players[1].pos.x), [1]);
    assert.ok(!('pos' in result.streams.players[2]));
});

test('a foreign or newer pack is refused', () => {
    const buf = fixture();
    new DataView(buf).setUint32(4, 2, true);
    assert.throws(() => decodePositionPack(buf), /unsupported \(MVDP v2/);
    assert.throws(() => decodePositionPack(new ArrayBuffer(12)), /unsupported/);
});
