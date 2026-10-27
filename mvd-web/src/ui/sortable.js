// sortable.js — makeSortable(table): click a header to sort the table's
// <tbody> rows by that column (rules in src/lib/sortable.js). It reorders
// the row nodes in place, so it is for tables whose rows are built by hand
// (app.js, and the ported tabs that build their rows as markup) — never
// for rows a Svelte {#each} owns. app.js reaches it as MvdLib.makeSortable.

import { nextSortDir, compareSortText } from '../lib/sortable.js';

const sortText = (cell) => cell?.dataset.sortValue ?? cell?.textContent.trim() ?? '';

export function makeSortable(table) {
    const theadRows = table.querySelectorAll('thead tr');
    const allHeaders = table.querySelectorAll('thead th');

    // Build a column index map: for each th, compute which td column it maps to.
    // Handles rowspan and colspan in multi-row headers.
    const grid = []; // grid[row][col] = th element or null (occupied by span)
    theadRows.forEach((tr, rowIdx) => {
        if (!grid[rowIdx]) grid[rowIdx] = [];
        let colPos = 0;
        tr.querySelectorAll('th').forEach(th => {
            // Skip columns already occupied by rowspan from previous rows
            while (grid[rowIdx][colPos]) colPos++;
            const colspan = parseInt(th.getAttribute('colspan')) || 1;
            const rowspan = parseInt(th.getAttribute('rowspan')) || 1;
            // Store mapping on the element
            th._sortColIdx = colPos;
            th._sortColspan = colspan;
            // Mark grid cells as occupied
            for (let r = 0; r < rowspan; r++) {
                if (!grid[rowIdx + r]) grid[rowIdx + r] = [];
                for (let c = 0; c < colspan; c++) {
                    grid[rowIdx + r][colPos + c] = true;
                }
            }
            colPos += colspan;
        });
    });

    allHeaders.forEach(th => {
        // Skip colspan > 1 headers (group headers, not sortable)
        if (th._sortColspan > 1) return;
        // Bind once per element. Dynamically rebuilt tables (e.g. the loc /
        // region heatmaps) call makeSortable again after replacing their <th>
        // nodes; fresh nodes lack the flag and bind, while the load-time
        // querySelectorAll('.stats-table') pass won't double-bind existing ones
        // (a second listener would cancel every toggle).
        if (th._sortBound) return;
        th._sortBound = true;

        const colIdx = th._sortColIdx;
        th.classList.add('sortable');
        th.addEventListener('click', () => {
            const tbody = table.querySelector('tbody');
            if (!tbody) return;
            const rows = Array.from(tbody.querySelectorAll('tr'));

            // Toggle direction: a first click sniffs the column (numeric
            // descending, text ascending), further clicks flip.
            const current = th.classList.contains('sort-asc') ? 'asc'
                : th.classList.contains('sort-desc') ? 'desc' : null;
            const dir = nextSortDir(current, rows.map(r => sortText(r.cells[colIdx])));

            // Reset all headers in this table
            allHeaders.forEach(h => h.classList.remove('sort-asc', 'sort-desc'));
            th.classList.add(dir === 'asc' ? 'sort-asc' : 'sort-desc');

            rows.sort((a, b) => compareSortText(sortText(a.cells[colIdx]), sortText(b.cells[colIdx]), dir));

            rows.forEach(row => tbody.appendChild(row));
        });
    });
}
