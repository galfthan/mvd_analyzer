// url.js — URL state sharing (the rules in src/lib/url-state.js). Every
// change worth keeping (tab, clock, segment, learn mode, a search) calls
// updateUrlState(), which writes the URL at most every 500 ms. Only the
// boot's deep-link load (?gameId=) applies the URL's ?seg= / ?t= / ?tab= /
// ?learn= once the demo is in; any later load starts clean.

import { clock } from '../state/clock.js';
import { demo } from '../state/demo.js';
import { segment } from '../state/timeline.js';
import { learnMode, view as mapView } from '../tabs/map/map-model.js';
import { SEARCH_FIELDS } from '../data/hub.js';
import { lastExecutedSearch } from '../tabs/search/search-model.js';
import { urlQuery, parseSeg, parseTime } from '../lib/url-state.js';
import { switchTab, activeTabName } from './tabs.js';
import { preset } from '../player/layout.js';

/** @type {ReturnType<typeof setTimeout> | null} */
let urlStateTimer = null;

export function updateUrlState() {
    if (urlStateTimer) return;
    urlStateTimer = setTimeout(() => {
        urlStateTimer = null;
        const result = demo.get()?.result || null;
        const search = lastExecutedSearch.get();
        const qs = urlQuery(result && {
            gameId: result.hubInfo?.gameId,
            activeTab: activeTabName(),
            preset: preset.get(),
            learn: learnMode.get(),
            t: clock.t,
            seg: segment.get(),
        }, search, SEARCH_FIELDS);
        if (qs) {
            history.replaceState(null, '', `?${qs}`);
        } else if (result || search) {
            // We've actually loaded something but happen to have no
            // params worth keeping (e.g. fresh load on Summary at t=0
            // with no segment). Clear the URL.
            history.replaceState(null, '', location.pathname);
        }
        // else: nothing yet — leave the URL alone. There may still be
        // an in-flight ?gameId=… deep-link auto-load whose params we
        // would otherwise wipe before WASM finishes booting and
        // applyUrlState() can read them.
    }, 500);
}

// applyUrlState puts the deep-linked demo's URL state (segment, time, tab,
// learn mode) in effect once it has loaded. Never for a later load: the URL
// then still describes the previous demo.
export function applyUrlState() {
    const params = new URLSearchParams(location.search);

    const seg = parseSeg(params.get('seg'));
    if (seg) segment.set(seg);

    const t = parseTime(params.get('t'));
    if (t !== null) clock.seek(t);

    const tab = params.get('tab');
    if (tab) switchTab(tab); // resolves the locs-regions / loc-graph alias

    // Deep-link into the map's "learn" study view (only if this map has a
    // static entity corpus to show).
    if (params.get('learn') === '1' && mapView.state.mapEntities.length > 0) {
        learnMode.set(true);
    }

    updateUrlState();
}

// onClockTick keeps the URL's ?t= in step. Playback ticks every frame and a
// stop seeks once more, so it is skipped; the Player engine ('engine') ticks
// every frame too, and is followed every 200 ms. A demo reset ('reset')
// happens mid-load.
/** @type {ReturnType<typeof setTimeout> | null} */
let engineSyncTimer = null;
/** @param {import('../state/clock.js').ClockValue} tick */
export function onClockTick({ from }) {
    if (from === 'reset') {
        clearTimeout(engineSyncTimer ?? undefined);
        engineSyncTimer = null;
    }
    if (from === 'playback' || from === 'reset') return;
    if (from === 'engine') {
        if (!engineSyncTimer) {
            engineSyncTimer = setTimeout(() => {
                engineSyncTimer = null;
                updateUrlState();
            }, 200);
        }
        return;
    }
    updateUrlState();
}
