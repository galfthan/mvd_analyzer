// follow.js — whom the tab watches. With the engine up that is its POV
// (engine.trackName); without it — the Map preset — the map panes follow
// `mapFollow` instead. A click on a player in a pane goes through watch().

import { store } from '../state/store.js';
import { demo } from '../state/index.js';
import { engine } from './index.js';

export const mapFollow = store(/** @type {string | null} */ (null));
// A new demo follows nobody (its players are others, or the same names in
// another game).
demo.subscribe(() => { if (mapFollow.get() !== null) mapFollow.set(null); });

/** @param {string} name */
export function watch(name) {
    if (engine.drives()) engine.trackName(name);
    else mapFollow.set(mapFollow.get() === name ? null : name);
}

// watched: the player the tab watches now — the engine's POV, else the
// map's follow target.
export function watched() {
    return engine.drives() ? engine.povName() : mapFollow.get();
}
