// playback.js — the page's own playback: a loop that moves the clock at a
// speed (-1, 0.2, 1 or 5 ×) once per display frame, tagged 'playback', so
// every view that follows the clock moves with it (the Map tab draws each of
// those frames; the Timeline and Chat follow at their own pace). The unified
// bar (src/ui/PlaybackBar.svelte) starts and stops it. While the Player tab
// shows, its engine is the clock instead and this loop does not run.
//
// Forward past the end wraps to the start; reverse stops at 0. A tab without
// the bar, the Player tab, and a new demo stop it. Stopping seeks the clock
// to where it stopped (an untagged seek), which is what makes the views
// that only follow playback every so often catch up.

import { store } from './store.js';
import { clock } from './clock.js';
import { activeTab } from './ui.js';
import { demo } from './demo.js';

// The tabs the bar shows on.
export const TABS_WITH_TIMELINE = ['timeline', 'chat', 'map', 'keymoments', 'player'];

// A frame closer than this to the last one is skipped (~60 fps).
const FRAME_MS = 16;

export const playback = store({ playing: false, speed: 1 });

/** @type {number | null} */
let raf = null;
let last = 0;

// playAtSpeed starts playback at `speed`, or changes the speed; pressing the
// running speed again pauses.
/** @param {number} speed */
export function playAtSpeed(speed) {
    const { playing, speed: cur } = playback.get();
    if (playing && cur === speed) {
        stopPlayback();
        return;
    }
    playback.set({ playing: true, speed });
    if (!playing) {
        last = performance.now();
        raf = requestAnimationFrame(step);
    }
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
    raf = requestAnimationFrame(step);
    const now = performance.now();
    const elapsed = (now - last) / 1000;
    if (elapsed < FRAME_MS / 1000) return;
    last = now;
    const duration = Number.isFinite(clock.duration) ? clock.duration : 600;
    let t = clock.t + elapsed * playback.get().speed;
    if (t > duration) t = 0;
    if (t < 0) {
        stopPlayback(0);
        return;
    }
    clock.seek(t, 'playback');
}

activeTab.subscribe((tab) => {
    if (!TABS_WITH_TIMELINE.includes(tab) || tab === 'player') stopPlayback();
});

demo.subscribe(() => {
    stopPlayback();
    playback.set({ playing: false, speed: 1 });
});
