import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { decodeBucketPack } from '../src/lib/bucketpack.js';
import { reconstructBucketPlayers, reconstructBucketTeams } from '../../mvd-map-view/src/frames.js';

// Both files are written by the Go encoder from one built view
// (mvd-analytics/view/columnar_pack_test.go, packFixtureView): the .bin is
// its bucket pack and the .json its ColumnarBuckets JSON. The pack must
// decode to what the JSON parses to.
const read = (/** @type {string} */ name) => readFileSync(new URL(`./fixtures/${name}`, import.meta.url));
const pack = () => {
    const b = read('bucket-pack.bin');
    return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
};
const json = () => JSON.parse(read('bucket-pack.json').toString('utf8'));

// sameView compares a decoded pack with the parsed JSON: the same keys
// everywhere and the same values, float32 columns to the JSON's rounding.
// loose extends that tolerance to every number — for frames, where the
// accessors have copied float32 elements out into plain numbers.
/** @param {any} got @param {any} want @param {string} path @param {boolean} [loose] */
function sameView(got, want, path, loose = false) {
    if (ArrayBuffer.isView(got) || Array.isArray(got)) {
        assert.ok(Array.isArray(want), `${path}: JSON has no array`);
        assert.equal(got.length, want.length, `${path}: length`);
        const tol = loose || got instanceof Float32Array ? 5e-4 : 0;
        for (let i = 0; i < want.length; i++) {
            if (tol) assert.ok(Math.abs(got[i] - want[i]) <= tol, `${path}[${i}]: ${got[i]} vs ${want[i]}`);
            else assert.equal(got[i], want[i], `${path}[${i}]`);
        }
        return;
    }
    if (got && typeof got === 'object') {
        assert.deepEqual(Object.keys(got).sort(), Object.keys(want).sort(), `${path}: keys`);
        for (const k in want) sameView(got[k], want[k], `${path}.${k}`, loose);
        return;
    }
    if (loose && typeof want === 'number') assert.ok(Math.abs(got - want) <= 5e-4, `${path}: ${got} vs ${want}`);
    else assert.equal(got, want, path);
}

test('the Go-written pack decodes to its view\'s JSON', () => {
    const v = decodeBucketPack(pack());
    sameView(v, json(), 'view');
    const p = v.players['bär!'];
    assert.ok(p.x instanceof Float32Array && p.h instanceof Int16Array && p.alive instanceof Uint8Array);
    assert.ok(p.a instanceof Float64Array && v.teams.red.th instanceof Int32Array);
    assert.ok(Math.abs(p.x[0] - 0.3) < 1e-6, 'floats arrive unrounded');
    assert.deepEqual(p.at.slice(0, 3), ['', '', 'ya']);
});

test('the frame accessors read the pack as they read the JSON', () => {
    const v = decodeBucketPack(pack());
    const j = json();
    for (let i = 0; i < j.count; i++) {
        sameView(reconstructBucketPlayers(v, i), reconstructBucketPlayers(j, i), `players@${i}`, true);
        sameView(reconstructBucketTeams(v, i), reconstructBucketTeams(j, i), `teams@${i}`, true);
    }
});

test('a newer pack is refused, not misread', () => {
    const buf = pack();
    new DataView(buf).setUint32(4, 2, true);
    assert.throws(() => decodeBucketPack(buf), /bucket pack: unsupported \(MVDB v2/);
});
