// follow.js — whom the tab watches. With the engine up that is its POV
// (engine.trackName); without it — the Map preset — the map panes follow
// `mapFollow` instead. A click on a player in a pane goes through watch().

import { store } from '../state/store.js';
import { demo, teams, teamIndexOf } from '../state/index.js';
import { engine } from './index.js';
import { nextName } from './player-keys.js';

export const mapFollow = store(/** @type {string | null} */ (null));
// A new demo follows nobody (its players are others, or the same names in
// another game).
demo.subscribe(() => { if (mapFollow.get() !== null) mapFollow.set(null); });

/** @param {string} name */
export function watch(name) {
    if (engine.drives()) engine.trackName(name);
    else mapFollow.set(mapFollow.get() === name ? null : name);
}

// followName: watch `name` — no toggling off (a feed row, ↑ / ↓).
/** @param {string} name */
export function followName(name) {
    if (engine.drives()) engine.trackName(name);
    else mapFollow.set(name);
}

// watched: the player the tab watches now — the engine's POV, else the
// map's follow target.
export function watched() {
    return engine.drives() ? engine.povName() : mapFollow.get();
}

// watchOrder: the demo's players as the POV bar lists them — by the
// canonical team order, then by name.
/**
 * @param {import('../data/wasm-source.js').DemoHandle | null} handle
 * @param {import('../state/teams.js').TeamsState} t
 * @returns {string[]}
 */
export function watchOrder(handle, t) {
    const ids = handle?.result?.timelineAnalysis?.playerUserIDs || {};
    const idx = (/** @type {string} */ n) => teamIndexOf(t, n, t.perPlayer);
    return Object.keys(ids).sort((a, b) => (idx(a) - idx(b)) || a.localeCompare(b));
}

// watchNext: watch the player after (dir 1) or before (-1) the one
// watched now, in that order (↓ / ↑, src/player/player-keys.js).
/** @param {1 | -1} dir */
export function watchNext(dir) {
    const name = nextName(watchOrder(demo.get(), teams.get()), watched(), dir);
    if (name) followName(name);
}
