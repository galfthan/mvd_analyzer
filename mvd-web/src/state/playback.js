// playback.js — the page's own playback: a loop that moves the clock at a
// speed (SPEEDS in src/lib/playback-bar.js) once per display frame, tagged
// 'playback', so every view that follows the clock moves with it (the Map
// tab draws each of those frames; the Timeline and Chat follow at their own
// pace). The unified bar (src/ui/PlaybackBar.svelte) starts and stops it
// and sets its speed. While the Player tab
// shows with its engine up, the engine is the clock instead and this loop
// does not run (setPlaybackGate).
//
// Playing into the selected segment's end stops there (crossedEnd).
// Forward past the end wraps to the start; reverse stops at 0. A tab without
// the bar, the Player tab, and a new demo stop it. Stopping seeks the clock
// to where it stopped (an untagged seek), which is what makes the views
// that only follow playback every so often catch up.

import { store } from './store.js';
import { clock } from './clock.js';
import { activeTab } from './ui.js';
import { demo } from './demo.js';
import { segment } from './timeline.js';
import { crossedEnd } from '../lib/playback-bar.js';

// The tabs the bar shows on.
export const TABS_WITH_TIMELINE = ['timeline', 'chat', 'map', 'keymoments', 'player'];

// A frame closer than this to the last one is skipped (~60 fps).
const FRAME_MS = 16;

export const playback = store({ playing: false, speed: 1 });

// The Player engine, while it is the clock, keeps this loop off
// (src/player/index.js sets the gate: engine.drives).
let gate = () => false;
/** @param {() => boolean} fn */
export function setPlaybackGate(fn) {
    gate = fn;
}

/** @type {number | null} */
let raf = null;
let last = 0;

// setPlaybackSpeed changes the speed; playing, at once, paused, for the
// next start.
/** @param {number} speed */
export function setPlaybackSpeed(speed) {
    playback.set({ playing: playback.get().playing, speed });
}

export function startPlayback() {
    const { playing, speed } = playback.get();
    if (playing || gate()) return;
    playback.set({ playing: true, speed });
    last = performance.now();
    raf = requestAnimationFrame(step);
}

export function togglePlayback() {
    if (playback.get().playing) stopPlayback();
    else startPlayback();
}

/** @param {number} [at] */
export function stopPlayback(at = clock.t) {
    if (!playback.get().playing) return;
    if (raf) cancelAnimationFrame(raf);
    raf = null;
    playback.set({ playing: false, speed: playback.get().speed });
    clock.seek(at);
}

function step() {
    if (gate()) {
        stopPlayback();
        return;
    }
    raf = requestAnimationFrame(step);
    const now = performance.now();
    const elapsed = (now - last) / 1000;
    if (elapsed < FRAME_MS / 1000) return;
    last = now;
    const duration = Number.isFinite(clock.duration) ? clock.duration : 600;
    const prev = clock.t;
    let t = prev + elapsed * playback.get().speed;
    const seg = segment.get();
    if (seg && crossedEnd(seg, prev, t)) {
        stopPlayback(seg.end);
        return;
    }
    if (t > duration) t = 0;
    if (t < 0) {
        stopPlayback(0);
        return;
    }
    clock.seek(t, 'playback');
}

activeTab.subscribe((tab) => {
    if (!TABS_WITH_TIMELINE.includes(tab)) stopPlayback();
});

demo.subscribe(() => {
    stopPlayback();
    playback.set({ playing: false, speed: 1 });
});
