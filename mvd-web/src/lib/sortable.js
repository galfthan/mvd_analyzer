// sortable.js — the click-to-sort rules of every sortable table (the DOM
// side is src/ui/sortable.js makeSortable). A cell's sort text is its
// data-sort-value when it has one (e.g. the Stack column shows "70 120 RA"
// but sorts on H+A), its trimmed text otherwise.

// The direction of a first click on a column: numeric columns sort
// descending (biggest first, what a stats reader wants), text columns
// ascending. The sniff uses the same value a sort would, from the first
// row that has one ('' and '-' are "no value").
/** @typedef {'asc' | 'desc'} SortDir */

/**
 * @param {string[]} texts
 * @returns {SortDir}
 */
export function firstSortDir(texts) {
    const probe = texts.find(t => t !== '' && t !== '-');
    return probe !== undefined && !isNaN(parseFloat(probe)) ? 'desc' : 'asc';
}

// The next direction: a column already sorted flips, a fresh one sniffs.
/**
 * @param {SortDir | null} current
 * @param {string[]} texts
 * @returns {SortDir}
 */
export function nextSortDir(current, texts) {
    if (current === 'asc') return 'desc';
    if (current === 'desc') return 'asc';
    return firstSortDir(texts);
}

// compareSortText orders two sort texts. Both numeric — by their leading
// number (handles "42", "3.5%", "12 (30s)", etc.) — else as strings.
/**
 * @param {string} aText
 * @param {string} bText
 * @param {SortDir} dir
 */
export function compareSortText(aText, bText, dir) {
    const aNum = parseFloat(aText);
    const bNum = parseFloat(bText);
    if (!isNaN(aNum) && !isNaN(bNum)) {
        return dir === 'asc' ? aNum - bNum : bNum - aNum;
    }
    return dir === 'asc' ? aText.localeCompare(bText) : bText.localeCompare(aText);
}
