// layout.js — the Map / Player tab's panes, in two columns: `below`, the
// main one (the name is from before the player was a pane, when it sat
// under it), and `order`, the side one. Which panes are open in each, in
// what order, each one's height, collapsed state and options (the map's
// overlays, a graph's window), the side column's share of the tab's width
// (a percentage, PaneForge's unit) and whether it is shown.
//
// The Map and Player tabs are this one tab in two preset views — `map`
// (mainly the map, no engine) and `player` (mainly the demo player) — each
// its own layout: rearranging edits the view in use, resetPreset() restores
// it; the tab shown picks the view (`paneLayout` is always the one in use).
// A per-viewer convenience kept in localStorage.

import { store } from '../state/store.js';
import { activeTab } from '../state/ui.js';

const KEY = 'mvd.playerPanes.v4';        // { layouts: { map, player } }
const V3_KEY = 'mvd.playerPanes.v3';     // one layout, before the presets
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
const PLAYER_DEFAULT = {
    order: ['map', 'players', 'moments', 'team-weapons'],
    below: ['player', 'playlist'],
    collapsed: {},
    heights: {},          // pane id → body height in px; unset = natural
    opts: {},
    split: 30,            // the column's % of the width, when shown
    hidden: false,
};
/** @type {PaneLayout} */
const MAP_DEFAULT = {
    ...PLAYER_DEFAULT,
    order: ['players', 'moments', 'items', 'region-status'],
    below: ['map'],
    split: 34,
};

/** @typedef {'map' | 'player'} Preset */
/** @type {Record<Preset, PaneLayout>} */
const PRESET_DEFAULTS = { map: MAP_DEFAULT, player: PLAYER_DEFAULT };
const PRESET_IDS = /** @type {Preset[]} */ (['map', 'player']);
/** @param {unknown} id @returns {id is Preset} */
const isPreset = (id) => PRESET_IDS.includes(/** @type {Preset} */ (id));

export const MIN_SPLIT = 18;
export const MAX_SPLIT = 60;

/**
 * @param {unknown} v  a stored layout, untrusted
 * @param {PaneLayout} [DEFAULT]  the preset's default
 * @returns {PaneLayout}
 */
function sanitize(v, DEFAULT = PLAYER_DEFAULT) {
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

// legacyLayout: a layout stored before the presets (v3, v2 or the PoC's
// v1), null when there is none. It becomes the Player preset.
function legacyLayout() {
    const v = JSON.parse(localStorage.getItem(V3_KEY) || 'null');
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
    return null;
}

// The view in use is not stored: the tab shown picks it (until one shows,
// the Map view's layout stands).
/** @returns {{ preset: Preset, layouts: Record<Preset, PaneLayout> }} */
function load() {
    try {
        const v = JSON.parse(localStorage.getItem(KEY) || 'null');
        if (v && typeof v === 'object') {
            return {
                preset: 'map',
                layouts: { map: sanitize(v.layouts?.map, MAP_DEFAULT), player: sanitize(v.layouts?.player, PLAYER_DEFAULT) },
            };
        }
        return { preset: 'map', layouts: { map: sanitize(null, MAP_DEFAULT), player: legacyLayout() || sanitize(null) } };
    } catch { /* storage blocked or corrupt: defaults */ }
    return { preset: 'map', layouts: { map: sanitize(null, MAP_DEFAULT), player: sanitize(null) } };
}

const all = load();
export const preset = store(all.preset);
export const paneLayout = store(all.layouts[all.preset]);

function save() {
    try {
        localStorage.setItem(KEY, JSON.stringify({ layouts: all.layouts }));
    } catch { /* per-viewer convenience */ }
}

/** @param {(layout: PaneLayout) => void} fn */
export function updateLayout(fn) {
    const base = PRESET_DEFAULTS[all.preset];
    const next = sanitize(structuredClone(paneLayout.get()), base);
    fn(next);
    const clean = sanitize(next, base);
    all.layouts[all.preset] = clean;
    save();
    paneLayout.set(clean);
}

// setPreset shows a preset's layout (as the viewer left it).
/** @param {string} id */
export function setPreset(id) {
    if (!isPreset(id) || id === all.preset) return;
    all.preset = id;
    preset.set(id);
    paneLayout.set(all.layouts[id]);
}

// The tab shown picks the view (setPreset ignores the other tabs).
activeTab.subscribe((tab) => setPreset(tab));

// resetPreset puts the preset in use back to its default layout.
export function resetPreset() {
    all.layouts[all.preset] = sanitize(null, PRESET_DEFAULTS[all.preset]);
    save();
    paneLayout.set(all.layouts[all.preset]);
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

// showPane opens a pane, expanded and shown, where it is or else in the
// main column.
/** @param {string} id */
export function showPane(id) {
    updateLayout((l) => {
        if (!columnOf(l, id)) l.below.push(id);
        delete l.collapsed[id];
        // In a hidden side column it would not show.
        if (columnOf(l, id) === 'order') l.hidden = false;
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
