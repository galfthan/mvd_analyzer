// legacy.js — the ONE place the Svelte side reaches into app.js (a classic
// script whose top-level functions are window properties). Everything here
// moves into src/ as the tab that owns it is ported; until then a new
// component imports these instead of touching window itself.

// loadDemo(input) loads and renders a demo (app.js: status line, timing,
// displayResults). Throws on failure.
export function loadDemo(input) {
    return window.loadDemo(input);
}

// The Timeline tab's per-player drill-down helpers, used by the Player
// tab's Health / armor pane.
export function timelinePlayersByTeam() {
    return window.timelinePlayersByTeam();
}

export function prepPlayerHAData(cp, start, end) {
    return window.prepPlayerHAData(cp, start, end);
}

export function renderMiniStack(canvasId, opts) {
    return window.renderMiniStack(canvasId, opts);
}

// The Map tab's MvdMap: the Player tab's map pane reads its loaded data
// (geometry, locs, frames, items…) — app.js's initMapView and the async
// loaders fill it.
export function mapTabView() {
    return window.legacyMapView || null;
}

// rerenderTab(tab) redraws a tab's canvases (app.js renderTabContent): a
// panel expanded or reopened (src/ui/panels) may hold a canvas drawn at
// zero width while it was hidden.
export function rerenderTab(tab) {
    return window.renderTabContent?.(tab);
}
