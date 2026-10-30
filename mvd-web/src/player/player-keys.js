// player-keys.js — the demo player's keys, for the analyzer's playback and
// the light player alike: Space plays / pauses, ↑ / ↓ track the previous /
// next player, ← / → step a second back / on and Shift ← / → ten, t shows /
// hides the team info and (where given) f toggles fullscreen. Tab (the
// scoreboard) and ` (the console) are the engine's own binds
// (mvd-fte-player/config.cfg), as on hub. Keys typed into a field are left
// alone.

/**
 * @typedef {object} PlayerKeyActions
 * @property {() => void} toggle    play / pause
 * @property {((dir: 1 | -1) => void) | null} [step]  track the next / previous
 *   player; null where there is nobody to watch (↑ / ↓ then scroll the page)
 * @property {(by: number) => void} seek  move the playhead by seconds
 * @property {() => void} [teamInfo]  show / hide the engine's team info
 * @property {() => void} [fullscreen]  fullscreen on / off (the light
 *   player; the analyzer's `f` is src/app/fullscreen.js's)
 * @property {() => boolean} [enabled]  whether the keys apply now
 */

/** @param {EventTarget | null} t */
const typing = (t) => !!(/** @type {Element} */ (t)?.closest?.('input, textarea, select, [contenteditable]'));

/**
 * @param {PlayerKeyActions} actions
 * @returns {(e: KeyboardEvent) => void} the keydown handler
 */
export function createPlayerKeys(actions) {
    return (e) => {
        if (actions.enabled && !actions.enabled()) return;
        // Taken already (a focused pane divider resizes with the arrows).
        if (e.defaultPrevented) return;
        if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target)) return;
        if (e.key === ' ') {
            // Space on a button reached by keyboard presses it; one that
            // only kept the focus after a click (Chrome) does not.
            if (/** @type {Element} */ (e.target)?.closest?.('button, a')?.matches(':focus-visible')) return;
            e.preventDefault();
            if (!e.repeat) actions.toggle();
        } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
            e.preventDefault();
            actions.seek((e.key === 'ArrowLeft' ? -1 : 1) * (e.shiftKey ? 10 : 1));
        } else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && actions.step && !e.shiftKey) {
            e.preventDefault();
            actions.step(e.key === 'ArrowUp' ? -1 : 1);
        } else if (e.key === 't' && actions.teamInfo && !e.repeat) {
            e.preventDefault();
            actions.teamInfo();
        } else if (e.key === 'f' && actions.fullscreen && !e.repeat) {
            e.preventDefault();
            actions.fullscreen();
        }
    };
}

/**
 * The name `dir` places after `current` in `names`, wrapping; from the
 * start (or the end, going back) when `current` is not among them
 * (autotrack, or nobody followed).
 * @param {string[]} names
 * @param {string | null} current
 * @param {1 | -1} [dir]
 * @returns {string | null}
 */
export function nextName(names, current, dir = 1) {
    const n = names.length;
    if (!n) return null;
    const i = current == null ? -1 : names.indexOf(current);
    if (i < 0) return dir > 0 ? names[0] : names[n - 1];
    return names[(i + dir + n) % n];
}
