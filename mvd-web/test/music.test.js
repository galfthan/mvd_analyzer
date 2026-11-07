import test from 'node:test';
import assert from 'node:assert/strict';

import { createMusic, DEFAULT_VOLUME } from '../src/play/music.js';

// fakeAudio records what the player does with its element. Like a real
// one, a file just set has no metadata (readyState 0) — and a seek then is
// lost — until load() says it has.
function fakeAudio() {
    /** @type {Array<() => void>} */
    let onMeta = [];
    let src = '';
    return {
        get src() { return src; },
        set src(v) { src = v; this.readyState = 0; },
        readyState: 0, paused: true, volume: 1, loop: false, preload: '',
        _t: 0,
        get currentTime() { return this._t; },
        set currentTime(v) { if (this.readyState >= 1) this._t = v; },
        plays: 0,
        play() { this.paused = false; this.plays++; return Promise.resolve(); },
        pause() { this.paused = true; },
        addEventListener(/** @type {string} */ ev, /** @type {() => void} */ fn) { if (ev === 'loadedmetadata') onMeta.push(fn); },
        load() {
            this.readyState = 1;
            const fns = onMeta;
            onMeta = [];
            for (const fn of fns) fn();
        },
    };
}
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const A = { source: 'incompetech', id: 'a', url: 'https://incompetech.com/a.mp3', title: 'A', artist: 'K', license: 'CC BY 4.0', licenseUrl: '', page: '', durationSec: 60 };
const B = { ...A, id: 'b', url: 'https://incompetech.com/b.mp3', title: 'B' };

test('a cue plays its track, looping; the same track carries on; another fades over', async () => {
    const audio = fakeAudio();
    const m = createMusic({ audio: /** @type {any} */ (audio), fadeMs: 5 });
    assert.equal(audio.loop, true);
    m.cue(A);
    assert.equal(audio.src, A.url);
    assert.equal(audio.plays, 1, 'at once: the cue comes from the click that starts the run');
    assert.deepEqual(m.state.get(), { track: A, sounding: true, on: true, scale: 1 });
    await wait(30);
    assert.equal(audio.volume, DEFAULT_VOLUME, 'faded in to the default loudness');
    m.cue(A);
    assert.equal(audio.plays, 1, 'the same track is not restarted');
    m.cue(B);
    assert.equal(audio.src, A.url, 'the one before fades out first');
    await wait(40);
    assert.equal(audio.src, B.url);
    assert.ok(!audio.paused && audio.volume === DEFAULT_VOLUME);
});

test('a cue sets its loudness, the viewer scales it, and a jump picks the track up mid-way', async () => {
    const audio = fakeAudio();
    const m = createMusic({ audio: /** @type {any} */ (audio), fadeMs: 5, scale: 0.5 });
    m.cue({ ...A, volume: 0.6 }, 75);
    assert.equal(audio.currentTime, 0, 'no metadata yet: the seek waits');
    audio.load();
    assert.equal(audio.currentTime, 15, 'a 60 s track, 75 s into its section: looped round to 15 s');
    await wait(30);
    assert.ok(Math.abs(audio.volume - 0.3) < 1e-9, 'the cue\'s 0.6 × the viewer\'s 0.5');
    m.setScale(1);
    await wait(30);
    assert.ok(Math.abs(audio.volume - 0.6) < 1e-9);
    m.cue({ ...A, volume: 0.4 });
    await wait(30);
    assert.ok(Math.abs(audio.volume - 0.4) < 1e-9, 'the same track at another cue: its loudness, no restart');
    assert.equal(audio.plays, 1);
    m.cue({ ...A, volume: 0.4 }, 10);
    assert.equal(audio.currentTime, 10, 'a jump within the same track seeks it');
});

test('pause, the toggle and the end fade the music out; silence is a cue too', async () => {
    const audio = fakeAudio();
    const m = createMusic({ audio: /** @type {any} */ (audio), fadeMs: 5 });
    m.cue(A);
    await wait(20);
    m.setPaused(true);
    assert.equal(m.state.get().sounding, false);
    await wait(30);
    assert.ok(audio.paused && audio.volume === 0);
    m.setPaused(false);
    assert.ok(!audio.paused && audio.src === A.url, 'resumes the same track');
    m.toggle();
    await wait(30);
    assert.ok(audio.paused);
    assert.deepEqual(m.state.get(), { track: A, sounding: false, on: false, scale: 1 }, 'off: the track still credited');
    m.toggle();
    await wait(20);
    m.cue(null);
    await wait(30);
    assert.ok(audio.paused);
    assert.equal(m.state.get().track, null);
    m.cue(B);
    m.stop();
    await wait(30);
    assert.ok(audio.paused);
    assert.deepEqual(m.state.get(), { track: null, sounding: false, on: true, scale: 1 });
});

test('a jump to another track seeks it once its file has loaded, after the fade', async () => {
    const audio = fakeAudio();
    const m = createMusic({ audio: /** @type {any} */ (audio), fadeMs: 5 });
    m.cue(A);
    audio.load();
    await wait(20);
    m.cue(B, 42);
    await wait(30);
    assert.equal(audio.src, B.url);
    audio.load();
    assert.equal(audio.currentTime, 42);
});
