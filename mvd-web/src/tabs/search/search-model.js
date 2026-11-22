// search-model.js — the Search tab's state: the filter form, the status
// line, the result rows and their paging, and the last search that ran
// (the URL keeps its filters). SearchTab.svelte renders it; the boot
// (src/app/boot.js) fills the form from a URL's filters and runs it, or
// lists the recent games (runRecentSearch) when the URL has none.

import { store } from '../../state/store.js';
import { searchGames, fetchMatch, MATCH_SEARCH_LIMIT } from '../../data/hub.js';
import {
    EMPTY_FILTERS, SEARCH_MODE_LABELS, RECENT_MODES, RECENT_HOURS, searchFilters, formValues, nextPaging, hasMore,
    recentFrom, recentGames,
} from '../../lib/search.js';
import { searchMatches, cutShort, mergeGames } from '../../lib/matches.js';
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

// The matches already asked for whole, by the id of the game asked with,
// for the search shown; `generation` drops the answers of an earlier one.
/** @type {Set<string>} */
let asked = new Set();
let generation = 0;
const COMPLETE_CONCURRENCY = 4;

// completeMatches fetches the maps the search cut off the listed matches
// (src/lib/matches.js cutShort), a few at a time; the list regroups as they
// arrive.
async function completeMatches() {
    const filters = searchPaging.get().filters;
    if (!searchGrouped.get() || !filters) return;
    const gen = generation;
    const matches = searchMatches(searchResults.get().rows, searchExtraGames.get());
    const todo = cutShort(matches, filters, hasMore(searchPaging.get()))
        .filter(m => !m.games.some(g => asked.has(String(g.id))));
    for (const m of todo) asked.add(String(m.games[0].id));
    const worker = async () => {
        for (let m = todo.shift(); m && gen === generation; m = todo.shift()) {
            try {
                const whole = await fetchMatch(m.games[0]);
                if (gen !== generation) return;
                searchExtraGames.set(mergeGames(searchExtraGames.get(), whole.games));
            } catch (err) {
                // The match stays as the search returned it.
                console.warn('match lookup failed:', /** @type {Error} */ (err).message);
            }
        }
    };
    await Promise.all(Array.from({ length: COMPLETE_CONCURRENCY }, worker));
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
            searchExtraGames.set([]);
        }
        searchPaging.set(paging);
        searchResults.set(loadMore
            ? { rows: [...shown.rows, ...games], empty: shown.empty, pager: paging.shown > 0 }
            : { rows: games, empty: games.length === 0, pager: paging.shown > 0 });
        lastExecutedSearch.set(filters);
        updateUrlState();
        completeMatches();
    } catch (err) {
        searchStatus.set({ text: 'Error: ' + /** @type {Error} */ (err).message, cls: 'status error' });
        if (!loadMore) searchResults.set({ rows: [], empty: false, pager: false });
    } finally {
        searchBusy.set(false);
    }
}

// runRecentSearch fills the list a bare page opens with: the games of the
// main modes from the last day (src/lib/search.js recentGames), one search
// a mode since the search takes one. It is not a search of the form's — the
// form stays empty, the URL clean, and there is no next page — and a form
// search that answered meanwhile keeps the list.
export async function runRecentSearch() {
    const now = Date.now();
    const what = `${RECENT_MODES.map(m => SEARCH_MODE_LABELS[m]).join(', ')} games of the last ${RECENT_HOURS} hours`;
    searchStatus.set({ text: 'Loading the latest games…', cls: 'status loading' });
    try {
        const lists = await Promise.all(RECENT_MODES.map(mode =>
            searchGames({ mode, from: recentFrom(now) }, 0, fetch, MATCH_SEARCH_LIMIT)));
        if (lastExecutedSearch.get() || searchBusy.get()) return;
        const { rows, extra } = recentGames(lists.map(l => l.games), now);
        generation++;
        asked = new Set();
        searchExtraGames.set(extra);
        // No filters: nothing to page through or to complete — the older
        // games fetched alongside already hold the cut-off maps.
        searchPaging.set({ filters: null, shown: rows.length, total: rows.length });
        searchResults.set({ rows, empty: false, pager: rows.length > 0 });
        searchStatus.set({ text: rows.length ? `The ${what}.` : `No ${what}.`, cls: 'status' });
    } catch (err) {
        if (lastExecutedSearch.get() || searchBusy.get()) return;
        searchStatus.set({ text: 'Error: ' + /** @type {Error} */ (err).message, cls: 'status error' });
    }
}
