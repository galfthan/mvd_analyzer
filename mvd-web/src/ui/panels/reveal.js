// reveal.js — what a tab does when one of its panels comes back into view
// (expanded or reopened, src/ui/panels): a canvas drawn while its panel was
// hidden may need painting again. A ported tab registers its own redraw
// here; a tab still in app.js falls back to app.js's renderTabContent.

import { rerenderTab } from '../../legacy.js';

const handlers = new Map();

// onPanelReveal(tab, fn) registers the tab's redraw; returns the unregister.
export function onPanelReveal(tab, fn) {
    handlers.set(tab, fn);
    return () => {
        if (handlers.get(tab) === fn) handlers.delete(tab);
    };
}

export function panelRevealed(tab) {
    const fn = handlers.get(tab);
    if (fn) fn();
    else rerenderTab(tab);
}
