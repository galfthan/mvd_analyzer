// layout.js — the viewer's arrangement of each tab's panels: their order,
// which are collapsed to their heading and which are closed. A per-viewer
// convenience kept in localStorage (one key for every tab), like the Player
// tab's pane column (src/player/layout.js).

import { store } from '../../state/store.js';
import { PANEL_TABS } from './config.js';

const KEY = 'mvd.panels.v1';

const defaultOrder = (tab) => PANEL_TABS[tab].map(([id]) => id);

// One tab's layout, cleaned: the order names every known panel once (a
// panel added to the page since the layout was saved joins at its default
// place), and the flags name only known panels.
function sanitizeTab(tab, v) {
    const known = defaultOrder(tab);
    const saved = Array.isArray(v?.order) ? v.order.filter((id, i, a) => known.includes(id) && a.indexOf(id) === i) : [];
    const order = [...saved];
    known.forEach((id, i) => {
        if (order.includes(id)) return;
        // After the nearest earlier default neighbour that is placed.
        const prev = known.slice(0, i).reverse().find((p) => order.includes(p));
        order.splice(prev ? order.indexOf(prev) + 1 : 0, 0, id);
    });
    const flags = (o) => Object.fromEntries(Object.entries(o && typeof o === 'object' ? o : {})
        .filter(([id, on]) => known.includes(id) && on).map(([id]) => [id, true]));
    return { order, collapsed: flags(v?.collapsed), closed: flags(v?.closed) };
}

function sanitize(v) {
    const out = {};
    for (const tab of Object.keys(PANEL_TABS)) out[tab] = sanitizeTab(tab, v?.[tab]);
    return out;
}

function load() {
    try {
        return sanitize(JSON.parse(localStorage.getItem(KEY) || 'null'));
    } catch { /* storage blocked or corrupt: defaults */ }
    return sanitize(null);
}

export const panelLayouts = store(load());

// updatePanels(tab, fn): fn edits a copy of the tab's layout.
export function updatePanels(tab, fn) {
    const all = structuredClone(panelLayouts.get());
    fn(all[tab]);
    const clean = sanitize(all);
    try {
        localStorage.setItem(KEY, JSON.stringify(clean));
    } catch { /* per-viewer convenience */ }
    panelLayouts.set(clean);
}

// movePanel: the moved panel takes the target's slot (as in the Player
// tab's pane column), so dropping on the panel below moves it down.
export function movePanel(tab, id, targetId) {
    updatePanels(tab, (l) => {
        const from = l.order.indexOf(id);
        const to = l.order.indexOf(targetId);
        if (from < 0 || to < 0 || from === to) return;
        l.order.splice(from, 1);
        l.order.splice(to, 0, id);
    });
}

export const isDefault = (tab, l) =>
    !Object.keys(l.collapsed).length && !Object.keys(l.closed).length &&
    l.order.join() === defaultOrder(tab).join();

export function showAll(tab) {
    updatePanels(tab, (l) => { l.collapsed = {}; l.closed = {}; });
}

export function resetPanels(tab) {
    updatePanels(tab, (l) => {
        l.order = defaultOrder(tab);
        l.collapsed = {};
        l.closed = {};
    });
}
