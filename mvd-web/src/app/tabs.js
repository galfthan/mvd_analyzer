// tabs.js — tab switching. The sidebar (src/shell/Sidebar.svelte) and the
// load set the activeTab store; this shows the tab's .tab-content and
// writes the URL. Subscribed after the shell mounted (src/app/boot.js), so
// it runs after the tabs' own subscribers — they wait a microtask for the
// tab to be visible. Setting the same tab again notifies too, which is how
// a tab re-runs its show path after late data.
import { activeTab, selectTab, isWatchTab } from '../state/ui.js';
import { resolveTabName } from '../lib/url-state.js';
import { updateUrlState } from './url.js';

// switchTab takes a URL tab name too (the locs-regions alias); an unknown
// name changes nothing.
/** @param {string} name */
export function switchTab(name) {
    selectTab(resolveTabName(name));
}

export function activeTabName() {
    return activeTab.get();
}

export function setupTabs() {
    let subscribed = false;
    activeTab.subscribe((tabName) => {
        if (subscribed) onTabActivated(tabName);
    });
    subscribed = true;
}

/** @param {string} tabName */
function onTabActivated(tabName) {
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    // Every tab in TABS renders its #tab-<id> (src/shell/App.svelte); the
    // Map and Player tabs share #tab-player.
    /** @type {HTMLElement} */ (document.getElementById(`tab-${isWatchTab(tabName) ? 'player' : tabName}`)).classList.add('active');
    updateUrlState();
}
