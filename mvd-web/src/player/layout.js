// layout.js — the Player tab's pane column: which panes are open, in what
// order, each one's height, collapsed state and options (the map's
// overlays, a graph's window), the column's share of the tab's width (a
// percentage, PaneForge's unit) and whether it is shown. A per-viewer
// convenience kept in localStorage.

import { store } from '../state/store.js';

const KEY = 'mvd.playerPanes.v2';
const OLD_KEY = 'mvd.playerPanes.v1';   // an order array, the PoC's format

/**
 * @typedef {object} PaneLayout
 * @property {string[]} order                 open pane ids, top to bottom
 * @property {Record<string, boolean>} collapsed
 * @property {Record<string, number>} heights  pane id → body height in px
 * @property {Record<string, Record<string, unknown>>} opts  pane id → its options
 * @property {number} split                   the column's % of the width
 * @property {boolean} hidden
 */

// Every pane there is; the column starts with a few, the rest are one
// "Add pane…" away.
export const PANE_IDS = [
    'map', 'players', 'stack', 'moments', 'feed',
    'score', 'team-weapons', 'team-ha', 'weapons-pp', 'powerups', 'regions',
];
/** @type {PaneLayout} */
const DEFAULT = {
    order: ['map', 'players', 'moments', 'feed'],
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
    const order = Array.isArray(out.order) ? out.order : DEFAULT.order;
    out.order = order.filter((/** @type {string} */ id, /** @type {number} */ i) => PANE_IDS.includes(id) && order.indexOf(id) === i);
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

// movePane: the dragged pane takes the target's slot, so dropping on the
// pane below moves it down.
/**
 * @param {string} id
 * @param {number} toIndex
 */
export function movePane(id, toIndex) {
    updateLayout((l) => {
        const from = l.order.indexOf(id);
        if (from < 0 || toIndex < 0 || toIndex >= l.order.length || from === toIndex) return;
        l.order.splice(from, 1);
        l.order.splice(toIndex, 0, id);
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
