// heatmap-table.js — fill one of the tab's two matrix tables
// (src/lib/loc-heatmap.js builds the markup) and make it sortable. The rows
// are markup rather than an {#each} because makeSortable sorts by moving row
// nodes; a fresh header row binds afresh, so a rebuilt table's sort starts
// over.

import { heatmapTableHtml } from '../../lib/loc-heatmap.js';
import { makeSortable } from '../../ui/sortable.js';

export function fillHeatmapTable(table, data, firstColLabel, teamColors) {
    const { head, rows } = heatmapTableHtml(data, firstColLabel, teamColors);
    table.tHead.rows[0].innerHTML = head;
    const tbody = table.tBodies[0];
    tbody.innerHTML = '';
    for (const html of rows) {
        const tr = document.createElement('tr');
        tr.innerHTML = html;
        tbody.appendChild(tr);
    }
    makeSortable(table);
}

export function clearHeatmapTable(table, { head = true } = {}) {
    if (head) table.tHead.rows[0].innerHTML = '';
    table.tBodies[0].innerHTML = '';
}
