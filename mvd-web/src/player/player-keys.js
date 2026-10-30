// player-keys.js — hub's demo player keys (hub.quakeworld.nu
// public/assets/fte/config_v10.cfg), for the analyzer's playback and the
// light player alike: Space tracks the next player, Ctrl on its own plays /
// pauses, ← / → step a second back / on and Shift ← / → ten. Tab (the
// scoreboard) and ` (the console) are the engine's own binds
// (mvd-fte-player/config.cfg). Keys typed into a field are left alone.
//
// Ctrl plays / pauses when it is released with no other key pressed nor
// pointer button (`cancel`) meanwhile, so Ctrl+C or Ctrl+click never
// toggles.

/**
 * @typedef {object} PlayerKeyActions
 * @property {() => void} toggle    play / pause
 * @property {() => void} next      track the next player
 * @property {(by: number) => void} seek  move the playhead by seconds
 * @property {() => boolean} [enabled]  whether the keys apply now
 */

/** @param {EventTarget | null} t */
const typing = (t) => !!(/** @type {Element} */ (t)?.closest?.('input, textarea, select, [contenteditable]'));

/**
 * @param {PlayerKeyActions} actions
 * @returns {{ down: (e: KeyboardEvent) => void, up: (e: KeyboardEvent) => void, cancel: () => void }}
 */
export function createPlayerKeys(actions) {
    let ctrlAlone = false;
    const on = () => !actions.enabled || actions.enabled();
    return {
        down(e) {
            if (e.key === 'Control') {
                // A held key repeats: the first press decides.
                if (!e.repeat) ctrlAlone = !e.shiftKey && !e.altKey && !e.metaKey;
                return;
            }
            ctrlAlone = false;
            if (!on() || e.ctrlKey || e.metaKey || e.altKey || typing(e.target)) return;
            if (e.key === ' ') {
                // Space on a button presses it.
                if (/** @type {Element} */ (e.target)?.closest?.('button, a')) return;
                e.preventDefault();
                if (!e.repeat) actions.next();
            } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
                e.preventDefault();
                actions.seek((e.key === 'ArrowLeft' ? -1 : 1) * (e.shiftKey ? 10 : 1));
            }
        },
        up(e) {
            if (e.key !== 'Control') return;
            const alone = ctrlAlone;
            ctrlAlone = false;
            if (alone && on() && !typing(e.target)) actions.toggle();
        },
        cancel() {
            ctrlAlone = false;
        },
    };
}

/**
 * The name after `current` in `names`, wrapping; the first when `current`
 * is not among them (autotrack, or nobody followed).
 * @param {string[]} names
 * @param {string | null} current
 * @returns {string | null}
 */
export function nextName(names, current) {
    if (!names.length) return null;
    const i = current == null ? -1 : names.indexOf(current);
    return names[(i + 1) % names.length];
}
