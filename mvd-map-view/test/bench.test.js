import test from 'node:test';
import assert from 'node:assert/strict';

import {
    BENCH_DURATION_S, benchCameraAt, percentile, summarizeBench,
} from '../src/bench.js';
import { PITCH_MAX, PITCH_MIN, DEFAULT_YAW } from '../src/camera.js';

test('benchCameraAt is deterministic and spans the advertised duration', () => {
    const a = benchCameraAt(7.3);
    const b = benchCameraAt(7.3);
    assert.deepEqual(a, b, 'same input → same pose (comparable runs)');
    assert.equal(benchCameraAt(0).done, false);
    assert.equal(benchCameraAt(BENCH_DURATION_S).done, true);
    assert.equal(benchCameraAt(BENCH_DURATION_S + 5).done, true);
});

test('benchCameraAt covers rotation, tilt and zoom within camera limits', () => {
    let minPitch = Infinity, maxPitch = -Infinity, maxZoom = 0;
    for (let t = 0; t <= BENCH_DURATION_S; t += 0.05) {
        const p = benchCameraAt(t);
        assert.ok(p.pitch >= PITCH_MIN && p.pitch <= PITCH_MAX,
            `pitch stays in camera range at t=${t}`);
        assert.ok(p.zoomK >= 1, 'never zooms out past the fit baseline');
        minPitch = Math.min(minPitch, p.pitch);
        maxPitch = Math.max(maxPitch, p.pitch);
        maxZoom = Math.max(maxZoom, p.zoomK);
    }
    // Two full yaw turns over the run.
    assert.ok(Math.abs(benchCameraAt(BENCH_DURATION_S).yaw
        - (DEFAULT_YAW + 4 * Math.PI)) < 1e-9);
    assert.ok(maxPitch - minPitch > 0.5, 'pitch actually sweeps');
    assert.ok(maxZoom > 3.5, 'zoom reaches its peak');
    // Endpoints return to the launch pose so the run ends where it began.
    assert.ok(Math.abs(benchCameraAt(0).zoomK - 1) < 1e-9);
    assert.ok(Math.abs(benchCameraAt(BENCH_DURATION_S).zoomK - 1) < 1e-6);
});

test('percentile uses nearest-rank on the sorted values', () => {
    const v = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    assert.equal(percentile(v, 50), 5);
    assert.equal(percentile(v, 95), 10);
    assert.equal(percentile(v, 100), 10);
    assert.equal(percentile([42], 50), 42);
    assert.equal(percentile([], 50), 0);
});

test('summarizeBench folds samples into the comparable summary', () => {
    const samples = [];
    for (let i = 0; i < 100; i++) {
        samples.push({
            dtMs: i === 99 ? 50 : 10,   // one hitch frame
            buildMs: 2, submitMs: 3, blitMs: 1,
            draws: 80 + (i % 2),
            gpuMs: i < 10 ? undefined : 4,  // resolves a few frames in
        });
    }
    const s = summarizeBench(samples);
    assert.equal(s.frames, 100);
    assert.equal(s.longFrames, 1);
    assert.equal(s.frameMs.p50, 10);
    assert.equal(s.frameMs.max, 50);
    assert.equal(s.buildMs.p50, 2);
    assert.equal(s.gpuMs.p50, 4, 'missing gpu samples are skipped, not zeroed');
    assert.equal(s.draws.max, 81);
    assert.ok(Math.abs(s.avgFps - (100 * 1000) / (99 * 10 + 50)) < 0.1);
    assert.deepEqual(summarizeBench([]), { frames: 0 });
});
