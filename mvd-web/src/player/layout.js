// layout.js — the Player tab's pane column: which panes are open, in what
// order, each one's height and collapsed state, the column's width and
// whether it is shown. A per-viewer convenience kept in localStorage.

import { store } from '../state/store.js';

const KEY = 'mvd.playerPanes.v2';
const OLD_KEY = 'mvd.playerPanes.v1';   // an order array, the PoC's format

export const PANE_IDS = ['map', 'stack', 'moments', 'feed'];
const DEFAULT = {
    order: ['map', 'stack', 'moments', 'feed'],
    collapsed: {},
    heights: {},          // pane id → body height in px; unset = natural
    width: 420,
    hidden: false,
};
export const MIN_WIDTH = 260;
export const MAX_WIDTH = 900;

function sanitize(v) {
    const out = { ...DEFAULT, ...(v && typeof v === 'object' ? v : {}) };
    const order = Array.isArray(out.order) ? out.order : DEFAULT.order;
    out.order = order.filter((id, i) => PANE_IDS.includes(id) && order.indexOf(id) === i);
    out.collapsed = { ...(out.collapsed || {}) };
    out.heights = { ...(out.heights || {}) };
    out.width = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Number(out.width) || DEFAULT.width));
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
export function movePane(id, toIndex) {
    updateLayout((l) => {
        const from = l.order.indexOf(id);
        if (from < 0 || toIndex < 0 || toIndex >= l.order.length || from === toIndex) return;
        l.order.splice(from, 1);
        l.order.splice(toIndex, 0, id);
    });
}
