// Panels: the viewer arranges each tab's panels — reorder (drag the grip,
// or ↑ ↓), collapse to the heading, close, and bring back from the tab's
// panel bar ("Add panel…", "Show all", reset). Saved per viewer
// (layout.js).
//
// The panels stay where the tab's markup put them: order is the CSS
// `order` property, collapse and close are classes. Nothing is moved, so
// the tabs and the load's body classes — which hide panels a demo has no
// data for, by inline style or by body class — keep working on the same
// elements; a panel they hide takes its controls with it.

import { mount } from 'svelte';
import { activeTab } from '../../state/index.js';
import { PANEL_TABS } from './config.js';
import { panelLayouts, updatePanels, movePanel } from './layout.js';
import PanelControls from './PanelControls.svelte';
import PanelBar from './PanelBar.svelte';
import { panelRevealed } from './reveal.js';

const DRAG_TYPE = 'text/x-mvd-panel';

export function setupPanels() {
    for (const tab of Object.keys(PANEL_TABS)) setupTab(tab);
}

/** @param {string} tab */
function setupTab(tab) {
    const tabEl = document.getElementById(`tab-${tab}`);
    if (!tabEl) return;
    const titles = Object.fromEntries(PANEL_TABS[tab]);
    /** @type {Record<string, HTMLElement>} */
    const els = {};
    for (const [id, title] of PANEL_TABS[tab]) {
        /** @type {HTMLElement | null} */
        const el = tabEl.querySelector(`:scope > [data-panel="${id}"]`);
        if (!el) continue;
        els[id] = el;
        el.classList.add('lp');
        el.dataset.lpTitle = title;
        const head = el.firstElementChild;
        if (!head || !['H3', 'DETAILS'].includes(head.tagName)) el.classList.add('lp-untitled');
        mount(PanelControls, { target: el, props: { tab, id, title, step: (dir) => step(id, dir) } });
        wireDrop(el, id);
    }
    tabEl.classList.add('panelized');
    mount(PanelBar, { target: tabEl, anchor: tabEl.firstChild ?? undefined, props: { tab, titles } });

    // Up / down step over the panels the viewer can see: closed ones and
    // ones hidden for this demo would make a step look like nothing.
    /**
     * @param {string} id
     * @param {number} dir  -1 up, 1 down
     */
    function step(id, dir) {
        const shown = panelLayouts.get()[tab].order.filter((p) => p === id ||
            (els[p] && !els[p].classList.contains('lp-closed') && getComputedStyle(els[p]).display !== 'none'));
        const target = shown[shown.indexOf(id) + dir];
        if (target) movePanel(tab, id, target);
    }

    /**
     * The drag events' dataTransfer is never null (only a synthetic
     * DragEvent lacks one).
     * @param {HTMLElement} el
     * @param {string} id
     */
    function wireDrop(el, id) {
        el.addEventListener('dragover', (e) => {
            if (!(/** @type {DataTransfer} */ (e.dataTransfer)).types.includes(DRAG_TYPE)) return;
            e.preventDefault();
            el.classList.add('lp-drag-over');
        });
        el.addEventListener('dragleave', (e) => {
            if (!el.contains(/** @type {Node | null} */ (e.relatedTarget))) el.classList.remove('lp-drag-over');
        });
        el.addEventListener('drop', (e) => {
            el.classList.remove('lp-drag-over');
            const [fromTab, from] = ((/** @type {DataTransfer} */ (e.dataTransfer)).getData(DRAG_TYPE) || '').split(':');
            if (fromTab !== tab || !from || from === id) return;
            e.preventDefault();
            movePanel(tab, from, id);
        });
    }

    // Apply the layout. A panel that comes back into view (expanded or
    // reopened) may hold a canvas drawn while it was hidden, at zero width:
    // the tab redraws (reveal.js).
    /** @type {Set<string> | null} */
    let shown = null;
    panelLayouts.subscribe((all) => {
        const l = all[tab];
        const now = new Set();
        l.order.forEach((id, i) => {
            const el = els[id];
            if (!el) return;
            el.style.order = String(i + 1);
            el.classList.toggle('lp-collapsed', !!l.collapsed[id]);
            el.classList.toggle('lp-closed', !!l.closed[id]);
            if (!l.collapsed[id] && !l.closed[id]) now.add(id);
        });
        const prev = shown;
        const revealed = prev && [...now].some((id) => !prev.has(id));
        shown = now;
        if (revealed && activeTab.get() === tab) requestAnimationFrame(() => panelRevealed(tab));
    });
}
