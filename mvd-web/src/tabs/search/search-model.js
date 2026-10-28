// search-model.js — the Search tab's state: the filter form, the status
// line, the result rows and their paging, and the last search that ran
// (the URL keeps its filters). SearchTab.svelte renders it; the boot
// (src/app/boot.js) fills the form from a URL's filters and runs it.

import { store } from '../../state/store.js';
import { searchGames } from '../../data/hub.js';
import { EMPTY_FILTERS, searchFilters, formValues, nextPaging } from '../../lib/search.js';
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
        searchPaging.set(paging);
        searchResults.set(loadMore
            ? { rows: [...shown.rows, ...games], empty: shown.empty, pager: paging.shown > 0 }
            : { rows: games, empty: games.length === 0, pager: paging.shown > 0 });
        lastExecutedSearch.set(filters);
        updateUrlState();
    } catch (err) {
        searchStatus.set({ text: 'Error: ' + /** @type {Error} */ (err).message, cls: 'status error' });
        if (!loadMore) searchResults.set({ rows: [], empty: false, pager: false });
    } finally {
        searchBusy.set(false);
    }
}
