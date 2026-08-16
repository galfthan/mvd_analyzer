// Map-render benchmark support: a deterministic scripted camera path and
// the summary statistics for a recorded run.
//
// The host drives the actual benchmark (it owns requestAnimationFrame and
// the playback clock — this package touches neither): each animation frame
// it asks benchCameraAt(elapsed) where the camera should be, renders, and
// records the frame's timings; at the end summarizeBench turns the samples
// into a compact, comparable summary. Everything here is pure math, so two
// runs over the same demo differ only by machine/build performance — which
// is exactly what makes the numbers comparable across runs.

import { DEFAULT_YAW, DEFAULT_PITCH, PITCH_MAX, PITCH_MIN } from './camera.js';

// Total scripted duration (seconds). Long enough to average out GC and
// respawn spikes, short enough to re-run casually after a change.
export const BENCH_DURATION_S = 18;

// The camera path, exercising what users actually do with the map:
// - yaw spins two full turns over the run (continuous rotation is the case
//   the GL rewrite exists for — every world VBO re-projects per frame);
// - pitch oscillates between ~29° and ~81° (crossing the angle-keyed liquid
//   re-sorts and the tilted-view stems/labels);
// - zoom holds at 1 for the first third (whole-map view), then swells to 4×
//   and back (icon rescale, label rebakes, fill-rate at high magnification).
export function benchCameraAt(tSec) {
    const t = Math.min(Math.max(tSec, 0), BENCH_DURATION_S);
    const yaw = DEFAULT_YAW + (4 * Math.PI * t) / BENCH_DURATION_S;
    let pitch = DEFAULT_PITCH + 0.45 * Math.sin((2 * Math.PI * t) / 9);
    if (pitch > PITCH_MAX) pitch = PITCH_MAX;
    if (pitch < PITCH_MIN) pitch = PITCH_MIN;
    let zoomK = 1;
    if (t > 6) {
        const u = (t - 6) / (BENCH_DURATION_S - 6);
        const hump = Math.sin(Math.PI * u);
        zoomK = 1 + 3 * hump * hump;
    }
    return { yaw, pitch, zoomK, done: tSec >= BENCH_DURATION_S };
}

// percentile: nearest-rank percentile of an ascending-sorted numeric array.
export function percentile(sorted, p) {
    if (sorted.length === 0) return 0;
    const idx = Math.min(sorted.length - 1,
        Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
    return sorted[idx];
}

const round1 = (v) => Math.round(v * 10) / 10;
const round2 = (v) => Math.round(v * 100) / 100;

function dist(samples, field) {
    const vals = [];
    for (const s of samples) {
        const v = s[field];
        if (typeof v === 'number' && !Number.isNaN(v)) vals.push(v);
    }
    if (vals.length === 0) return null;
    vals.sort((a, b) => a - b);
    return {
        p50: round2(percentile(vals, 50)),
        p95: round2(percentile(vals, 95)),
        max: round2(vals[vals.length - 1]),
    };
}

// summarizeBench: fold the per-frame samples ({dtMs, buildMs, submitMs,
// blitMs, gpuMs?, draws, ...}) into the run summary. dtMs is the interval
// between presented frames — its distribution is the user-felt smoothness;
// avgFps is frames over wall time. longFrames counts frames slower than
// 33.4 ms (two 60 Hz vsync periods — a visible hitch).
export function summarizeBench(samples) {
    const frames = samples.length;
    if (frames === 0) return { frames: 0 };
    let wallMs = 0;
    let longFrames = 0;
    let drawsSum = 0, drawsMax = 0;
    for (const s of samples) {
        wallMs += s.dtMs || 0;
        if ((s.dtMs || 0) > 33.4) longFrames++;
        const d = s.draws || 0;
        drawsSum += d;
        if (d > drawsMax) drawsMax = d;
    }
    return {
        frames,
        seconds: round2(wallMs / 1000),
        avgFps: wallMs > 0 ? round1((frames * 1000) / wallMs) : 0,
        longFrames,
        frameMs: dist(samples, 'dtMs'),
        buildMs: dist(samples, 'buildMs'),
        submitMs: dist(samples, 'submitMs'),
        blitMs: dist(samples, 'blitMs'),
        gpuMs: dist(samples, 'gpuMs'),
        draws: { avg: Math.round(drawsSum / frames), max: drawsMax },
    };
}
