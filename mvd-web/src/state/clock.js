// clock.js — the page's one match clock, in match-relative seconds.
//
// Every view that follows playback (timeline cursor, map, chat, panes)
// subscribes; whoever moves time calls seek(). `from` names the mover so a
// subscriber can ignore its own echo (the player does not seek itself
// back to where it just reported being). The value is clamped to
// [0, duration]; a seek to the same time still notifies (a redraw request).
//
// This is the hot path (~60 Hz while playing): subscribers that draw
// should only mark themselves dirty and paint in requestAnimationFrame.

import { store } from './store.js';

/** @typedef {{ t: number, from: string | null }} ClockValue */

export function createClock() {
    const s = store(/** @type {ClockValue} */ ({ t: 0, from: null }));
    let duration = Infinity;
    return {
        subscribe: s.subscribe,
        get t() {
            return s.get().t;
        },
        get duration() {
            return duration;
        },
        /** @param {number} d */
        setDuration(d) {
            duration = d > 0 ? d : Infinity;
        },
        /**
         * @param {number} t
         * @param {string | null} [from]
         */
        seek(t, from = null) {
            s.set({ t: Math.max(0, Math.min(t, duration)), from });
        },
    };
}

export const clock = createClock();
