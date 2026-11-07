// music.js — a playlist's music on the light player (play.html): the track
// a cue names (src/lib/playlist.js cueAt) plays under the clips, from the
// run's first click — a user gesture, so the browser lets it sound — to its
// end. One <audio> element: a new track fades the one before out, then in;
// the same track carries on across cues. A track shorter than its section
// loops. A jump (the runner's start, at a clip picked) picks the music up
// where playing through would have it (cueTimeAt). The viewer's play /
// pause pauses it too (a title card does not — the music plays over it),
// the toggle turns it off and on, and the viewer's scale (a slider)
// multiplies the cue's own loudness.
//
// Loudness: a cue's `volume`, else DEFAULT_VOLUME — under the game sound,
// which the page turns down to 60 % meanwhile.
//
// `state` is what the page shows: the track playing (its credit), whether
// it sounds, whether the viewer has it on.

import { store } from '../state/store.js';

export const DEFAULT_VOLUME = 0.3;

/**
 * @typedef {import('../lib/playlist.js').Cue} Cue
 * @typedef {{ track: Cue | null, sounding: boolean, on: boolean, scale: number }} MusicState
 */

/** @param {{ audio?: HTMLAudioElement, fadeMs?: number }} [opts]  the element and fade length (tests pass a fake and a short one) */
/** @param {{ audio?: HTMLAudioElement, fadeMs?: number, scale?: number }} [opts] */
export function createMusic({ audio = new Audio(), fadeMs = 800, scale = 1 } = {}) {
    audio.loop = true;
    audio.preload = 'auto';
    audio.volume = 0;
    /** @type {Cue | null} */
    let track = null;     // the track wanted
    /** @type {number | null} */
    let seekTo = null;    // seconds into it, for a jump
    let paused = false;   // by the viewer
    let on = true;
    /** @type {ReturnType<typeof setInterval> | null} */
    let ramp = null;
    let seq = 0;          // drops a fade overtaken by another change

    const state = store(/** @type {MusicState} */ ({ track: null, sounding: false, on: true, scale }));
    const publish = () => state.set({ track, sounding: !!track && on && !paused, on, scale });
    const loudness = () => Math.min(1, (track?.volume ?? DEFAULT_VOLUME) * scale);

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
        const url = /** @type {Cue} */ (track).url;
        if (audio.src === url) {
            seek();
            return start();
        }
        const swap = () => {
            audio.src = url;
            seek();
            start();
        };
        // Another track: one that sounds fades out first.
        if (audio.src && !audio.paused) fadeTo(0, swap);
        else swap();
    }

    function start() {
        audio.play()?.catch((/** @type {unknown} */ err) => console.warn('music:', err));
        fadeTo(loudness());
    }

    // seek: a jump's position, wrapped round a looping track. Set before
    // the file has loaded, it is where playback starts.
    function seek() {
        if (seekTo == null) return;
        const len = track?.durationSec || 0;
        audio.currentTime = len > 0 ? seekTo % len : seekTo;
        seekTo = null;
    }

    return {
        state,
        // cue(track | null, at?): the music wanted from here (the runner,
        // on every step to an item). `at`: a jump — seconds into the
        // section, where the track picks up, the same track included.
        /**
         * @param {Cue | null} next
         * @param {number} [at]
         */
        cue(next, at) {
            const same = (next?.url ?? null) === (track?.url ?? null);
            if (same && at == null) {
                // The same track, maybe at another cue's loudness.
                if (next && next.volume !== track?.volume) {
                    track = next;
                    if (on && !paused) fadeTo(loudness());
                    publish();
                }
                return;
            }
            track = next;
            seekTo = at ?? null;
            apply();
        },
        // The viewer's loudness scale (the slider), 0..2.
        /** @param {number} f */
        setScale(f) {
            scale = Math.max(0, Math.min(2, f));
            if (track && on && !paused) fadeTo(loudness());
            publish();
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
