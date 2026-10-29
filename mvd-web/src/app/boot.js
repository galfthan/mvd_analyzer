// boot.js — the page's start-up, run by src/main.js once the shell is
// mounted: the URL follows the clock, segment, learn mode and tab; the
// initial tab; the analyzer's readiness on the overlay; a deep-linked demo
// (?gameId=, or the legacy ?hub=) and the URL's search filters.

import { clock } from '../state/clock.js';
import { segment } from '../state/timeline.js';
import { hubInput } from '../state/shell.js';
import { learnMode } from '../tabs/map/map-model.js';
import { preset } from '../player/layout.js';
import { SEARCH_FIELDS } from '../data/hub.js';
import { filtersFromParams } from '../lib/search.js';
import { setSearchForm, runSearch } from '../tabs/search/search-model.js';
import { mvdData } from './source.js';
import { onAnalyzerReady, onAnalyzerFailed } from './overlay.js';
import { updateUrlState, onClockTick } from './url.js';
import { switchTab, setupTabs } from './tabs.js';
import { installFullscreen } from './fullscreen.js';
import { urlTab } from '../lib/url-state.js';
import { loadDeepLink } from './load.js';

export function boot() {
    // Shared state (src/state/) the URL keeps (?t=, ?seg=, ?learn=). The
    // stores call their subscriber at once; that first call is not a change.
    let subscribed = false;
    clock.subscribe((tick) => { if (subscribed) onClockTick(tick); });
    segment.subscribe(() => { if (subscribed) updateUrlState(); });
    learnMode.subscribe(() => { if (subscribed) updateUrlState(); });
    preset.subscribe(() => { if (subscribed) updateUrlState(); });
    subscribed = true;
    setupTabs();
    installFullscreen();

    const params = new URLSearchParams(location.search);
    const hubId = params.get('gameId') || params.get('hub');
    // An unknown ?tab= counts as none.
    const requestedTab = urlTab(params.get('tab'));

    // Pick the initial active tab. When deep-linking to a demo we want
    // the destination tab to be active even before the demo finishes
    // loading, so the wasm-loading overlay covers the right pane and
    // the user never glimpses the Search panel mid-flight.
    if (hubId) {
        switchTab(requestedTab || 'summary');
    } else if (requestedTab) {
        switchTab(requestedTab);
    } // else: leave the default (Search) active.

    mvdData.ready.then(onAnalyzerReady, onAnalyzerFailed);

    // Auto-load demo if URL has ?gameId= (canonical) or ?hub= (legacy) param.
    // The load waits for the analyzer itself.
    if (hubId) {
        hubInput.set(hubId);
        loadDeepLink();
    }

    // Auto-populate search filters from the URL. Only auto-RUN the
    // query when there's no demo also being deep-loaded — when the
    // user shares e.g. ?gameId=…&player=nexus, the player filter is
    // incidental URL state from however they originally found the
    // demo, not a request to browse the search results.
    const { filters, any } = filtersFromParams(params, SEARCH_FIELDS);
    if (any) {
        setSearchForm(filters);
        if (!hubId) {
            if (!requestedTab) switchTab('search');
            runSearch();
        }
    }
}
