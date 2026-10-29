// layout.js — the Player tab's panes, in two columns: `below`, the main
// one (the Player pane's by default; the name is from before the player
// was a pane, when it sat under it), and `order`, the side one. Which panes are open in each, in what
// order, each one's height, collapsed state and options (the map's
// overlays, a graph's window), the side column's share of the tab's width
// (a percentage, PaneForge's unit) and whether it is shown. A per-viewer
// convenience kept in localStorage.

import { store } from '../state/store.js';

const KEY = 'mvd.playerPanes.v3';
const V2_KEY = 'mvd.playerPanes.v2';    // before the player was a pane
const OLD_KEY = 'mvd.playerPanes.v1';   // an order array, the PoC's format

/**
 * @typedef {object} PaneLayout
 * @property {string[]} order                 the side column's pane ids, top to bottom
 * @property {string[]} below                 the main column's, top to bottom
 * @property {Record<string, boolean>} collapsed
 * @property {Record<string, number>} heights  pane id → body height in px
 * @property {Record<string, Record<string, unknown>>} opts  pane id → its options
 * @property {number} split                   the column's % of the width
 * @property {boolean} hidden
 */

// Every pane there is; the columns start with a few (the clip finder is
// not one of them), the rest are one "Add pane…" away. A pane is open in at most one column.
export const PANE_IDS = [
    'player', 'finder', 'playlist',
    'map', 'players', 'items', 'region-status', 'entities', 'stack', 'moments', 'feed',
    'score', 'team-weapons', 'team-ha', 'weapons-pp', 'powerups', 'regions',
];
/** @typedef {'order' | 'below'} Column */
/** @type {PaneLayout} */
const DEFAULT = {
    order: ['map', 'players', 'team-weapons'],
    below: ['player', 'playlist'],
    collapsed: {},
    heights: {},          // pane id → body height in px; unset = natural
    opts: {},
    split: 30,            // the column's % of the width, when shown
    hidden: false,
};
export const MIN_SPLIT = 18;
export const MAX_SPLIT = 60;

/**
 * @param {unknown} v  a stored layout, untrusted
 * @returns {PaneLayout}
 */
function sanitize(v) {
    /** @type {PaneLayout & { width?: unknown }} */
    const out = { ...DEFAULT, ...(v && typeof v === 'object' ? v : {}) };
    // A layout stored before the finder and the playlist were panes has no
    // `below`: it gets the default one.
    const order = Array.isArray(out.order) ? out.order : DEFAULT.order;
    const below = Array.isArray(out.below) ? out.below : DEFAULT.below;
    const seen = new Set();
    /** @param {unknown[]} ids */
    const clean = (ids) => /** @type {string[]} */ (ids.filter((id) => typeof id === 'string' && PANE_IDS.includes(id) && !seen.has(id) && seen.add(id)));
    out.below = clean(below);
    out.order = clean(order);
    out.collapsed = { ...(out.collapsed || {}) };
    out.heights = { ...(out.heights || {}) };
    out.opts = Object.fromEntries(Object.entries(out.opts && typeof out.opts === 'object' ? out.opts : {})
        .filter(([id, o]) => PANE_IDS.includes(id) && o && typeof o === 'object'));
    out.split = Math.min(MAX_SPLIT, Math.max(MIN_SPLIT, Number(out.split) || DEFAULT.split));
    delete out.width;     // v2's px width, before the split
    out.hidden = !!out.hidden;
    return out;
}

function load() {
    try {
        const v = JSON.parse(localStorage.getItem(KEY) || 'null');
        if (v) return sanitize(v);
        // A layout from before the player was a pane gets it on top of the
        // main column, where the player was.
        const v2 = JSON.parse(localStorage.getItem(V2_KEY) || 'null');
        if (v2) {
            const l = sanitize(v2);
            if (!columnOf(l, 'player')) l.below.unshift('player');
            return l;
        }
        const old = JSON.parse(localStorage.getItem(OLD_KEY) || 'null');
        if (Array.isArray(old)) return sanitize({ order: old });
    } catch { /* storage blocked or corrupt: defaults */ }
    return sanitize(null);
}

export const paneLayout = store(load());

/** @param {(layout: PaneLayout) => void} fn */
export function updateLayout(fn) {
    const next = sanitize(structuredClone(paneLayout.get()));
    fn(next);
    const clean = sanitize(next);
    try {
        localStorage.setItem(KEY, JSON.stringify(clean));
    } catch { /* per-viewer convenience */ }
    paneLayout.set(clean);
}

/**
 * @param {PaneLayout} layout
 * @param {string} id
 * @returns {Column | null}
 */
export function columnOf(layout, id) {
    return layout.order.includes(id) ? 'order' : layout.below.includes(id) ? 'below' : null;
}

// movePane puts a pane at `toIndex` of a column, from wherever it is: the
// dragged pane takes the target's slot, so dropping on the pane below moves
// it down; an index past the end appends.
/**
 * @param {string} id
 * @param {Column} column
 * @param {number} toIndex
 */
export function movePane(id, column, toIndex) {
    updateLayout((l) => {
        const from = columnOf(l, id);
        if (!from || toIndex < 0) return;
        if (from === column && (toIndex >= l[column].length || l[column].indexOf(id) === toIndex)) return;
        l[from] = l[from].filter((x) => x !== id);
        l[column].splice(Math.min(toIndex, l[column].length), 0, id);
    });
}

// showPane opens a pane, expanded, where it is or else in the main column.
/** @param {string} id */
export function showPane(id) {
    updateLayout((l) => {
        if (!columnOf(l, id)) l.below.push(id);
        delete l.collapsed[id];
    });
}

// paneOpt reads one of a pane's options, `fallback` when unset or of
// another type (the stored layout is untrusted).
/**
 * @template T
 * @param {PaneLayout} layout
 * @param {string} id
 * @param {string} key
 * @param {T} fallback
 * @returns {T}
 */
export function paneOpt(layout, id, key, fallback) {
    const v = layout.opts[id]?.[key];
    return typeof v === typeof fallback ? /** @type {T} */ (v) : fallback;
}

/**
 * @param {string} id
 * @param {string} key
 * @param {unknown} value
 */
export function setPaneOpt(id, key, value) {
    updateLayout((l) => { l.opts[id] = { ...(l.opts[id] || {}), [key]: value }; });
}
