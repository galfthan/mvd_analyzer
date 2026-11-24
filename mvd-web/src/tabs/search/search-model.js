// search-model.js — the Search tab's state: the filter form, the status
// line, the result rows and their paging, and the last search that ran
// (the URL keeps its filters). SearchTab.svelte renders it; the boot
// (src/app/boot.js) fills the form from a URL's filters and runs it — with
// none, the empty form's search: the newest games.

import { store } from '../../state/store.js';
import { searchGames, fetchMatch, fetchGame, fetchQwlSeries } from '../../data/hub.js';
import { EMPTY_FILTERS, searchFilters, formValues, nextPaging, hasMore } from '../../lib/search.js';
import { searchMatches, cutShort, missingMaps, mergeGames, siblingFilters } from '../../lib/matches.js';
import { mergeSeries } from '../../lib/qwl.js';
import { updateUrlState } from '../../app/url.js';

// The form's fields as typed (strings; the mode '' is "Any").
export const searchForm = store({ ...EMPTY_FILTERS });
export const searchStatus = store({ text: '', cls: 'status' });
export const searchBusy = store(false);
// The list under the form: `rows` (hub game rows), `empty` (the search
// found nothing — a message instead of rows), `pager` (the footer shows).
export const searchResults = store({ rows: /** @type {import('../../data/hub.js').HubGame[]} */ ([]), empty: false, pager: false });
// The filters the shown rows belong to, how many are shown, and the
// hub-reported total (null = unknown).
export const searchPaging = store(/** @type {import('../../lib/search.js').Paging} */ ({ filters: null, shown: 0, total: null }));
// The filters of the last search that answered, for the URL.
export const lastExecutedSearch = store(/** @type {import('../../lib/search.js').Filters | null} */ (null));

// Whether the list groups the found games into matches (src/lib/matches.js).
// On unless the viewer turned it off; their choice is kept in the browser.
const GROUPED_KEY = 'mvd.search.grouped';
export const searchGrouped = store(readGrouped());
// Games the search did not return that belong to a listed match, fetched
// for the grouped list (completeMatches).
export const searchExtraGames = store(/** @type {import('../../data/hub.js').HubGame[]} */ ([]));
// The QWLeague series the listed games are maps of (src/lib/qwl.js): they
// group those games exactly and label them. Empty without an API that
// knows them.
export const searchSeries = store(/** @type {import('../../lib/qwl.js').QwlSeries[]} */ ([]));

function readGrouped() {
    try {
        return localStorage.getItem(GROUPED_KEY) !== '0';
    } catch {
        return true;
    }
}

/** @param {boolean} on */
export function setSearchGrouped(on) {
    searchGrouped.set(on);
    try {
        localStorage.setItem(GROUPED_KEY, on ? '1' : '0');
    } catch {}
    if (on) completeMatches();
}

// The lookups already made for the search shown: a match's other maps by
// its filters (siblingFilters; two listed matches between the same sides on
// one day are one lookup), a QWLeague series' missing map by its demo's
// sha. `queue` is what is still to ask — a game whose match to fetch, or
// such a sha — shared by the few workers running; `generation` drops the
// answers of an earlier search.
/** @type {Set<string>} */
let asked = new Set();
/** @type {Array<import('../../data/hub.js').HubGame | string>} */
let queue = [];
let workers = 0;
let generation = 0;
const COMPLETE_CONCURRENCY = 4;

// completeMatches fetches the maps the search cut off the listed matches
// (src/lib/matches.js cutShort), a few at a time; the list regroups as they
// arrive.
function completeMatches() {
    const filters = searchPaging.get().filters;
    if (!searchGrouped.get() || !filters) return;
    const matches = searchMatches(searchResults.get().rows, searchExtraGames.get(), searchSeries.get());
    for (const m of matches) {
        for (const sha of missingMaps(m)) {
            if (asked.has(sha)) continue;
            asked.add(sha);
            queue.push(sha);
        }
    }
    for (const m of cutShort(matches, filters, hasMore(searchPaging.get()))) {
        const lookup = siblingFilters(m.games[0]);
        const key = JSON.stringify(lookup);
        if (!lookup || asked.has(key)) continue;
        asked.add(key);
        queue.push(m.games[0]);
    }
    for (; workers < COMPLETE_CONCURRENCY && queue.length; workers++) {
        completeQueued().finally(() => { workers--; });
    }
}

async function completeQueued() {
    for (let next = queue.shift(); next; next = queue.shift()) {
        const gen = generation;
        try {
            const games = typeof next === 'string' ? [await fetchGame(`sha:${next}`)] : (await fetchMatch(next)).games;
            if (gen === generation) searchExtraGames.set(mergeGames(searchExtraGames.get(), games));
        } catch (err) {
            // The match stays as the search returned it.
            console.warn('match lookup failed:', /** @type {Error} */ (err).message);
        }
    }
}

// setSearchForm writes `filters` (only the fields given) into the form, as
// the inputs hold them (src/lib/search.js formValues).
/** @param {Partial<Record<string, unknown>>} filters */
export function setSearchForm(filters) {
    searchForm.set({ ...searchForm.get(), ...formValues(filters) });
}

// runSearch runs the form's search, or (loadMore) the next page of the
// shown one.
export async function runSearch(loadMore = false) {
    const prev = searchPaging.get();
    const filters = loadMore && prev.filters ? prev.filters : searchFilters(searchForm.get());
    const offset = loadMore ? prev.shown : 0;

    searchStatus.set({ text: 'Searching…', cls: 'status loading' });
    searchBusy.set(true);

    try {
        const { games, total } = await searchGames(filters, offset);
        searchStatus.set({ text: '', cls: 'status' });
        const paging = nextPaging(searchPaging.get(), filters, offset, games.length, total);
        const shown = searchResults.get();
        if (!loadMore) {
            generation++;
            asked = new Set();
            queue = [];
            searchExtraGames.set([]);
            searchSeries.set([]);
        }
        searchPaging.set(paging);
        searchResults.set(loadMore
            ? { rows: [...shown.rows, ...games], empty: shown.empty, pager: paging.shown > 0 }
            : { rows: games, empty: games.length === 0, pager: paging.shown > 0 });
        lastExecutedSearch.set(filters);
        updateUrlState();
        // The page's QWLeague series first, so their games are grouped by
        // series and not looked up as an ordinary match's.
        const gen = generation;
        const series = await fetchQwlSeries(games);
        if (gen !== generation) return;
        if (series.length) searchSeries.set(mergeSeries(searchSeries.get(), series));
        completeMatches();
    } catch (err) {
        searchStatus.set({ text: 'Error: ' + /** @type {Error} */ (err).message, cls: 'status error' });
        if (!loadMore) searchResults.set({ rows: [], empty: false, pager: false });
    } finally {
        searchBusy.set(false);
    }
}
