import test from 'node:test';
import assert from 'node:assert/strict';

import { lowerBoundIndex, trailIndexAtTime } from '../src/util.js';
import { hexToRgba, scaleRgbaAlpha, getLocationColor } from '../src/color.js';
import { normalizeLocationName, findNearestLocation } from '../src/locs.js';
import { decodeRegionStateChar } from '../src/regions.js';

const at = (a, i) => a[i];

test('lowerBoundIndex finds the last entry at or before t', () => {
    const a = [0, 10, 20, 30];
    assert.equal(lowerBoundIndex(a, 0, at), 0);
    assert.equal(lowerBoundIndex(a, 15, at), 1);
    assert.equal(lowerBoundIndex(a, 30, at), 3);
    assert.equal(lowerBoundIndex(a, 9999, at), 3);
});

test('lowerBoundIndex returns -1 when every entry is later than t', () => {
    assert.equal(lowerBoundIndex([10, 20], 9, at), -1);
    assert.equal(lowerBoundIndex([], 0, at), -1);
});

test('trailIndexAtTime searches a point list by its t field', () => {
    const pts = [{ t: 0 }, { t: 5 }, { t: 9 }];
    assert.equal(trailIndexAtTime(pts, 6), 1);
    assert.equal(trailIndexAtTime(pts, -1), -1);
});

test('hexToRgba splits a #rrggbb string', () => {
    assert.equal(hexToRgba('#ff5050', 0.5), 'rgba(255, 80, 80, 0.5)');
});

test('scaleRgbaAlpha multiplies alpha, honouring the cap and the 1.0 ceiling', () => {
    assert.equal(scaleRgbaAlpha('rgba(1,2,3,0.2)', 2), 'rgba(1,2,3,0.4)');
    assert.equal(scaleRgbaAlpha('rgba(1,2,3,0.2)', 10), 'rgba(1,2,3,1)');
    assert.equal(scaleRgbaAlpha('rgba(1,2,3,0.4)', 2, 0.5), 'rgba(1,2,3,0.5)');
});

test('scaleRgbaAlpha leaves a non-rgba string alone', () => {
    assert.equal(scaleRgbaAlpha('#ff0000', 2), '#ff0000');
});

test('normalizeLocationName upper-cases item keywords and lower-cases the rest', () => {
    assert.equal(normalizeLocationName('  RA Entry '), 'RA.entry');
    assert.equal(normalizeLocationName('ya-box'), 'YA.box');
    assert.equal(normalizeLocationName('Bridge Low'), 'bridge.low');
    // Same loc written three ways must land on one name — the whole point of
    // having a single canonical normalizer.
    const forms = ['ra.tunnel', 'RA Tunnel', 'Ra-TUNNEL'];
    assert.equal(new Set(forms.map(normalizeLocationName)).size, 1);
});

test('getLocationColor keys off item words in the name', () => {
    assert.notDeepEqual(getLocationColor('RA.entry'), getLocationColor('bridge.low'));
    assert.deepEqual(getLocationColor('quad.room'), getLocationColor('QUAD.hide'));
});

test('findNearestLocation picks the closest centre in XY', () => {
    const locs = [{ name: 'a', x: 0, y: 0 }, { name: 'b', x: 100, y: 0 }];
    assert.equal(findNearestLocation(10, 0, locs), 'a');
    assert.equal(findNearestLocation(90, 5, locs), 'b');
    assert.equal(findNearestLocation(0, 0, []), '');
});

test('decodeRegionStateChar maps the analyzer codes and defaults to empty', () => {
    assert.equal(decodeRegionStateChar('A'), 'teamAControl');
    assert.equal(decodeRegionStateChar('b'), 'teamBWeakControl');
    assert.equal(decodeRegionStateChar('c'), 'weakContested');
    assert.equal(decodeRegionStateChar('?'), 'empty');
});
