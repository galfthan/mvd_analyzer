// ui.js — which tab is showing. The sidebar and the load set it;
// src/app/tabs.js follows it (showing the tab, the URL) and the tabs read it.
import { store } from './store.js';

export const TABS = [
    { id: 'search', label: 'Search', title: 'Search demos' },
    { id: 'summary', label: 'Summary', title: 'Summary' },
    { id: 'timeline', label: 'Timeline', title: 'Timeline' },
    { id: 'chat', label: 'Chat', title: 'Chat' },
    { id: 'player', label: 'Player', title: 'Player — watch the demo' },
    { id: 'map', label: 'Map', title: 'Map' },
    { id: 'loc-graph', label: 'Locs & Regions', title: 'Locs & Regions' },
    { id: 'keymoments', label: 'Key Moments', title: 'Key Moments' },
    { id: 'packdrops', label: 'Pack Drops', title: 'Pack Drops' },
    { id: 'pickups', label: 'Pickups', title: 'Pickups' },
    { id: 'aimstats', label: 'Aim Stats', title: 'Aim Stats (experimental)' },
];

// The Map and Player tabs are one tab, two views: both show #tab-player
// (src/player/PlayerTab.svelte), each with its own pane layout
// (src/player/layout.js presets).
export const WATCH_TABS = ['map', 'player'];
/** @param {string} id */
export const isWatchTab = (id) => WATCH_TABS.includes(id);

// Setting the same tab again notifies too: each tab re-runs its show path,
// which is how it repaints after late data.
export const activeTab = store('search');

/** @param {string} id */
export function isTab(id) {
    return TABS.some((t) => t.id === id);
}

/** @param {string} id */
export function selectTab(id) {
    if (isTab(id)) activeTab.set(id);
}
