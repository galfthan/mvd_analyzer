import test from 'node:test';
import assert from 'node:assert/strict';

import {
    bucketTimeSec, bucketIndexAtTime, bucketIndexAtOrAfter,
    playerValAt, playerAliveAt, reconstructBucketPlayers,
    teamSnapshot, reconstructBucketTeams,
} from '../src/frames.js';

const VIEW = { windowMs: 50, start: 1000, count: 4 };

test('the time axis is implicit arithmetic over (start, windowMs)', () => {
    assert.equal(bucketTimeSec(VIEW, 0), 1);
    assert.equal(bucketTimeSec(VIEW, 3), 1.15);
});

test('bucketIndexAtTime floors into the containing span and clamps', () => {
    assert.equal(bucketIndexAtTime(VIEW, 1.0), 0);
    assert.equal(bucketIndexAtTime(VIEW, 1.049), 0, 'half-open span');
    assert.equal(bucketIndexAtTime(VIEW, 1.05), 1);
    assert.equal(bucketIndexAtTime(VIEW, 0), 0, 'clamped below');
    assert.equal(bucketIndexAtTime(VIEW, 99), 3, 'clamped above');
    assert.equal(bucketIndexAtTime(null, 1), -1);
    assert.equal(bucketIndexAtTime({ count: 0 }, 1), -1);
});

test('bucketIndexAtOrAfter ceils and clamps to [0, count]', () => {
    assert.equal(bucketIndexAtOrAfter(VIEW, 1.01), 1);
    assert.equal(bucketIndexAtOrAfter(VIEW, 1.05), 1, 'exact boundary stays');
    assert.equal(bucketIndexAtOrAfter(VIEW, 0), 0);
    assert.equal(bucketIndexAtOrAfter(VIEW, 99), 4, 'may point one past the end');
});

test('playerValAt gates on span, liveness and validFrom', () => {
    const p = { first: 2, n: 2, alive: [1, 0], h: [100, 50], validFrom: { h: 3 } };
    assert.equal(playerValAt(p, 'h', 1), undefined, 'before the span');
    assert.equal(playerValAt(p, 'h', 2), undefined, 'alive but before validFrom');
    assert.equal(playerValAt(p, 'h', 3), undefined, 'dead bucket');
    assert.equal(playerValAt({ ...p, validFrom: {} }, 'h', 2), 100);
    assert.equal(playerValAt(p, 'nosuch', 2), undefined, 'absent column');
    assert.equal(playerAliveAt(p, 2), true);
    assert.equal(playerAliveAt(p, 3), false);
});

test('reconstructBucketPlayers mirrors the Go columnarToRow oracle', () => {
    const view = {
        ...VIEW,
        players: {
            up:   { first: 0, n: 1, alive: [1], x: [10], y: [20], h: [100], rl: [1], q: [0], at: ['ra'] },
            down: { first: 0, n: 1, alive: [0], x: [1], y: [1] },
        },
    };
    const p = reconstructBucketPlayers(view, 0);
    assert.deepEqual(p.up, { x: 10, y: 20, h: 100, rl: true, q: false, at: 'ra' });
    assert.equal(p.down, undefined);
});

test('team snapshots expand counters and the armor breakdown per bucket', () => {
    const view = {
        ...VIEW,
        teams: { red: { rl: [1, 2], abt: { ra: [0, 3] } } },
    };
    assert.deepEqual(teamSnapshot(view, 'red', 1), { rl: 2, abt: { ra: 3 } });
    assert.deepEqual(teamSnapshot(view, 'blue', 1), {}, 'absent team');
    assert.deepEqual(reconstructBucketTeams(view, 0), { red: { rl: 1, abt: { ra: 0 } } });
    assert.equal(reconstructBucketTeams({ ...VIEW }, 0), undefined, 'no teams block');
});
