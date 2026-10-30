// page-driver.js — clips on the Map tab, without the engine. The runner
// (runner.js) drives this as it drives the engine — jump, follow the
// clip's player, play, slow motion, a tick per move — over the page's own
// playback (src/state/playback.js), the map following the clip's player
// (follow.js mapFollow) where the engine would take their POV.

import { clock, demo, segment, playback, startPlayback, stopPlayback, slowPlayback } from '../state/index.js';
import { demoOffsetSec, nameOfUserId } from '../lib/result.js';
import { mapFollow } from './follow.js';

/** @typedef {import('../lib/playlist.js').Track} Track */

export function createPageDriver() {
    const offset = () => demoOffsetSec(demo.get()?.result);

    // Playback stops itself at the run's segment end (the clip's `to`, or
    // the match end before it): the stop's own seek is the tick that ends
    // the clip. Crossing `to` while playing is no test here — frames can
    // be seconds apart, which the runner takes for a seek.
    let stopping = false;
    let wasPlaying = false;
    playback.subscribe((p) => {
        if (wasPlaying && !p.playing) stopping = true;
        wasPlaying = p.playing;
    });

    /** @type {Set<(demoT: number) => void>} */
    const ticks = new Set();
    clock.subscribe(({ t }) => {
        for (const fn of ticks) fn(t + offset());
        stopping = false;
    });

    return {
        offset,
        ready: () => true,
        seeking: () => false,
        isPlaying: () => playback.get().playing,
        hasEnded() {
            if (playback.get().playing) return false;
            const seg = segment.get();
            return clock.t >= clock.duration || (stopping && !!seg && clock.t >= Math.min(seg.end, clock.duration));
        },
        /** @param {(demoT: number) => void} fn */
        onTick(fn) {
            ticks.add(fn);
            return () => ticks.delete(fn);
        },
        /** @param {number} demoT */
        jumpToDemoTime(demoT) {
            clock.seek(Math.max(0, demoT - offset()));
        },
        /** @param {Track} userid  'auto' follows nobody */
        track(userid) {
            const name = userid === 'auto' ? null : nameOfUserId(demo.get()?.result, userid);
            if (name !== mapFollow.get()) mapFollow.set(name);
        },
        play: startPlayback,
        pause: () => stopPlayback(),
        /** @param {number | null} speed */
        slowTo(speed) {
            slowPlayback(speed);
        },
    };
}
