// music.js — a playlist's music on the light player (play.html): the track
// a cue names (src/lib/playlist.js cueAt) plays under the clips, from the
// run's first click — a user gesture, so the browser lets it sound — to its
// end. One <audio> element: a new track fades the one before out, then in;
// the same track carries on across cues. A track shorter than its section
// loops. The viewer's play / pause pauses it too (a title card does not —
// the music plays over it), and the toggle turns it off and on.
//
// `state` is what the page shows: the track playing (its credit), whether
// it sounds, whether the viewer has it on.

import { store } from '../state/store.js';

const VOLUME = 0.8;

/**
 * @typedef {import('../lib/playlist.js').Music} Music
 * @typedef {{ track: Music | null, sounding: boolean, on: boolean }} MusicState
 */

/** @param {{ audio?: HTMLAudioElement, fadeMs?: number }} [opts]  the element and fade length (tests pass a fake and a short one) */
export function createMusic({ audio = new Audio(), fadeMs = 800 } = {}) {
    audio.loop = true;
    audio.preload = 'auto';
    audio.volume = 0;
    /** @type {Music | null} */
    let track = null;     // the track wanted
    let paused = false;   // by the viewer
    let on = true;
    /** @type {ReturnType<typeof setInterval> | null} */
    let ramp = null;
    let seq = 0;          // drops a fade overtaken by another change

    const state = store(/** @type {MusicState} */ ({ track: null, sounding: false, on: true }));
    const publish = () => state.set({ track, sounding: !!track && on && !paused, on });

    // fadeTo ramps the volume to v, then calls done (unless overtaken).
    /**
     * @param {number} v
     * @param {() => void} [done]
     */
    function fadeTo(v, done) {
        if (ramp) clearInterval(ramp);
        const from = audio.volume;
        const t0 = performance.now();
        const mine = ++seq;
        ramp = setInterval(() => {
            const k = Math.min(1, (performance.now() - t0) / fadeMs);
            audio.volume = from + (v - from) * k;
            if (k < 1) return;
            if (ramp) clearInterval(ramp);
            ramp = null;
            if (mine === seq && done) done();
        }, Math.min(30, fadeMs));
    }

    // apply brings the element to what is wanted: the track's file, playing
    // or faded and paused.
    function apply() {
        publish();
        const sound = !!track && on && !paused;
        if (!sound) {
            fadeTo(0, () => audio.pause());
            return;
        }
        const url = /** @type {Music} */ (track).url;
        if (audio.src === url) return start();
        const swap = () => {
            audio.src = url;
            start();
        };
        // Another track: one that sounds fades out first.
        if (audio.src && !audio.paused) fadeTo(0, swap);
        else swap();
    }

    function start() {
        audio.play()?.catch((/** @type {unknown} */ err) => console.warn('music:', err));
        fadeTo(VOLUME);
    }

    return {
        state,
        // cue(track | null): the music wanted from here (the runner, on
        // every step to an item).
        /** @param {Music | null} next */
        cue(next) {
            if ((next?.url ?? null) === (track?.url ?? null)) return;
            track = next;
            apply();
        },
        // The viewer's play / pause.
        /** @param {boolean} p */
        setPaused(p) {
            if (p === paused) return;
            paused = p;
            if (track) apply();
        },
        toggle() {
            on = !on;
            apply();
        },
        // The run ended: fade out, nothing wanted.
        stop() {
            track = null;
            paused = false;
            apply();
        },
    };
}
