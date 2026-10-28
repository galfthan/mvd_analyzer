// url-state.js — what the page keeps in its URL (src/app/url.js writes and
// reads it): ?gameId= the hub demo, ?tab=, ?t= the clock, ?seg= the selected
// segment, ?learn=1 the map's learn mode, and the Search tab's filters.
// The legacy ?hub= is read as ?gameId= and never written.

import { isTab } from '../state/ui.js';

// Tab URL aliases. The loc tab's internal id stayed "loc-graph", but the tab
// is labelled "Locs & Regions" and the URL prefers the matching
// "locs-regions" slug. Old "loc-graph" links still resolve; new URLs are
// written as "locs-regions".
export const TAB_URL_TO_INTERNAL = { 'locs-regions': 'loc-graph' };
export const TAB_INTERNAL_TO_URL = { 'loc-graph': 'locs-regions' };

export function resolveTabName(name) {
    return TAB_URL_TO_INTERNAL[name] || name;
}

// urlTab: the tab a URL's ?tab= names (alias resolved), null when it names
// none — an unknown ?tab= is treated as no ?tab= at all.
export function urlTab(name) {
    const id = resolveTabName(name || '');
    return isTab(id) ? id : null;
}

// urlQuery → the query string (no '?', '' when nothing is worth keeping).
//   demo: null before a demo, else { gameId, activeTab, learn, t, seg }
//   search: the last executed search's filters, or null; fields: their keys
export function urlQuery(demo, search, fields) {
    const params = new URLSearchParams();
    if (demo) {
        if (demo.gameId) params.set('gameId', demo.gameId);
        if (demo.activeTab && demo.activeTab !== 'summary') {
            params.set('tab', TAB_INTERNAL_TO_URL[demo.activeTab] || demo.activeTab);
        }
        if (demo.learn) params.set('learn', '1');
        if (demo.t > 0) params.set('t', Math.round(demo.t));
        if (demo.seg) params.set('seg', `${Math.round(demo.seg.start)}-${Math.round(demo.seg.end)}`);
    }
    if (search) {
        for (const f of fields) {
            const v = search[f];
            if (v) params.set(f, v);
        }
    }
    return params.toString();
}

// parseSeg reads ?seg=start-end (seconds), null when it is not two numbers.
export function parseSeg(seg) {
    if (!seg) return null;
    const [start, end] = seg.split('-').map(Number);
    return !isNaN(start) && !isNaN(end) ? { start, end } : null;
}

// parseTime reads ?t= (seconds), null when absent or not a number.
export function parseTime(t) {
    if (!t) return null;
    const time = Number(t);
    return isNaN(time) ? null : time;
}
