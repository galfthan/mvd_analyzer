// loc-heatmap.js — the Locs & Regions tab's two matrices, the Loc Heatmap
// and the Region Control table, as data and as table markup.
//
// Both share one renderer (heatmapTableHtml): a sortable .stats-table — rows
// on the y-axis, one column per series, each cell viridis-shaded by a
// precomputed intensity with the % printed in it. Using a real table (vs. a
// canvas) gives crisp text and free column sorting via makeSortable.
//
//   - Loc heatmap: rows = locs (busiest first); columns are the team
//     aggregates (every member's time combined) then one per player grouped by
//     team. Intensity = each column's share of its time in the loc, normalised
//     PER COLUMN to its own busiest loc (sqrt-curved).
//   - Region control: rows = regions; columns are the seven control states.
//     Intensity = each state's share, normalised PER ROW to the region's
//     busiest control state (Empty excluded — see buildRegionHeatmap).
//
// Normalisation is baked into each cell's intensity by the build* functions, so
// the renderer is policy-free. Team identity rides on the canonical team order
// and colours (src/state/teams.js, CLAUDE.md "Team colors") as a coloured
// underline on the relevant column headers. Data comes from result.locGraph +
// result.demoInfo (locs) and the region-control model (src/state/regions.js)
// — no extra analyzer pass.
//
// ── Shared matrix model ──────────────────────────────────────────────────────
//
// Both build* functions return the same shape, rendered by heatmapTableHtml:
//
//   {
//     rows:    [{ name, cells: [{ i, p }, ...] }]    // i: 0..1 intensity, p: %|null
//     columns: [{ label, full, team, teamIdx, ... }] // teamIdx<0 = no underline
//     teamCols,                                       // separator before col teamCols
//     cellTitle(col, row, ci) -> string               // <td> title text
//   }

import { escapeHtml } from './html.js';
import { heatColorRGB } from './heat-color.js';
import { metricWeightsOf } from './locgraph.js';

// Readable text color over an [r,g,b] fill — dark ink on light cells, white
// on dark ones (Rec. 601 luma).
export function contrastInk(rgb) {
    const lum = 0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2];
    return lum > 150 ? '#10131f' : '#f0f0f0';
}

// Canonical team order: team colours are indexed by position in the
// canonical (frag-sorted) order, so the header underlines match the rest of
// the app. Fall back to demoInfo order before that is set.
export function canonicalTeams(result, order) {
    if (order && order.length) return [...order];
    const dt = result.demoInfo && result.demoInfo.teams;
    return (dt && dt.length) ? [...dt] : [''];
}

// Short display label for a long player name; the full name is kept on the
// column header's title attribute. (QuakeWorld's in-game short name comes from
// the client-side `cl_fakename` cvar, which is only ever injected as a text
// prefix on say_team messages — never carried in userinfo — so there is no
// per-player short name in the demo stream to read.)
export function shortName(name, n = 9) {
    name = String(name || '');
    return name.length > n ? name.slice(0, n - 1) + '…' : name;
}

// Build the loc matrix for `metric` ('all' | 'armed' | 'quad' …): leading
// team-aggregate columns then one per player grouped by team; rows are locs
// (busiest first by the metric). Intensity is each column's share normalised to
// its own busiest loc (sqrt-curved to lift small shares). Returns null when
// there's nothing to show (e.g. nobody ever held a quad). `teamOrder` is the
// canonical order; `duel` drops the team columns.
export function buildLocHeatmap(result, metric, teamOrder, duel) {
    const graph = result && result.locGraph;
    if (!graph || !graph.locs || graph.locs.length === 0) return null;
    const players = (result.demoInfo && result.demoInfo.players) || [];
    const teams = canonicalTeams(result, teamOrder);
    const teamIdxOf = (team) => {
        const i = teams.indexOf(team);
        return i >= 0 ? i : teams.length; // unknown teams share the trailing color
    };

    // Weights for the chosen metric (node itself for 'all', the armed/quad
    // sub-object otherwise) drive every time read below.
    const W = (n) => metricWeightsOf(n, metric);

    const playerTotal = new Map();
    for (const node of graph.locs) {
        for (const [p, t] of Object.entries(W(node).byPlayer || {})) {
            playerTotal.set(p, (playerTotal.get(p) || 0) + t);
        }
    }

    const baseLocs = graph.locs
        .filter(n => W(n).byPlayer && Object.keys(W(n).byPlayer).length > 0)
        .slice()
        .sort((a, b) => (W(b).total || 0) - (W(a).total || 0));
    if (baseLocs.length === 0) return null;

    const hasTeams = !duel && teams.length >= 2 && !(teams.length === 1 && teams[0] === '');

    // Columns carry `members` so a cell value is uniformly sum(byPlayer[m]);
    // `kind` ('team' | 'player') drives the title wording. `label` is the short
    // header text, `full` the untruncated name kept on the header title.
    const columns = [];
    if (hasTeams) {
        for (const t of teams) {
            const members = players
                .filter(p => (p.team || '') === t && (playerTotal.get(p.name) || 0) > 0)
                .map(p => p.name);
            const total = members.reduce((s, p) => s + (playerTotal.get(p) || 0), 0);
            if (total > 0) columns.push({ kind: 'team', label: shortName(t), full: t, team: t, teamIdx: teamIdxOf(t), members, total });
        }
    }
    const teamCols = columns.length;

    const teamOfPlayer = new Map();
    for (const p of players) if (p.name) teamOfPlayer.set(p.name, p.team || '');
    const seen = new Set();
    for (const team of teams) {
        for (const p of players) {
            if (!p.name || seen.has(p.name)) continue;
            if ((p.team || '') !== team) continue;
            const total = playerTotal.get(p.name) || 0;
            if (total <= 0) continue;
            columns.push({ kind: 'player', label: shortName(p.name), full: p.name, team, teamIdx: teamIdxOf(team), members: [p.name], total });
            seen.add(p.name);
        }
    }
    for (const [p, total] of playerTotal) {
        if (seen.has(p) || total <= 0) continue;
        const team = teamOfPlayer.get(p) || '';
        columns.push({ kind: 'player', label: shortName(p), full: p, team, teamIdx: teamIdxOf(team), members: [p], total });
        seen.add(p);
    }
    if (columns.length === 0) return null;

    // Per-column max share → full intensity at each column's busiest loc.
    const secOf = (n, c) => { const bp = W(n).byPlayer || {}; return c.members.reduce((s, p) => s + (bp[p] || 0), 0); };
    const colMaxFrac = columns.map(c => {
        let m = 0;
        for (const n of baseLocs) {
            const f = c.total > 0 ? secOf(n, c) / c.total : 0;
            if (f > m) m = f;
        }
        return m || 1;
    });

    let rows = baseLocs.map(n => ({
        name: n.name,
        secs: columns.map(c => secOf(n, c)),
        cells: columns.map((c, ci) => {
            const sec = secOf(n, c);
            const share = c.total > 0 ? sec / c.total : 0;
            const norm = colMaxFrac[ci] > 0 ? share / colMaxFrac[ci] : 0;
            return { i: sec > 0 ? Math.sqrt(norm) : 0, p: sec > 0 ? share * 100 : null };
        }),
    }));
    rows = rows.filter(r => r.secs.some(v => v > 0));
    if (rows.length === 0) return null;

    return {
        rows, columns, teamCols,
        cellTitle: (col, row, ci) => {
            // row.secs holds int32-ms weights (schema v57); show seconds.
            const sec = (row.secs[ci] || 0) / 1000;
            const pct = row.cells[ci].p != null ? row.cells[ci].p : 0;
            const suffix = col.kind === 'team' ? 'of team time' : 'of their time';
            const who = col.full + (col.kind === 'player' && col.team && col.team !== col.full ? ` (${col.team})` : '');
            return `${who} · ${row.name}: ${sec.toFixed(1)}s · ${pct.toFixed(1)}% ${suffix}`;
        },
    };
}

// Build the region-control matrix: rows are regions, columns are the seven
// control states. Colour is normalised per-row to the region's busiest control
// state (Empty excluded — it is filler, not a control state, and would swamp
// the scale), so a row uses the full colormap; the printed % stays absolute.
// `stats` is Go's per-region stats; the team names ride at the parent level
// of Go's shape, not on each region, so they come in separately, with
// `sides`, their canonical (colour) indices (lib/regions.js regionSides).
export function buildRegionHeatmap(regions, stats, teamAName, teamBName, sides) {
    if (!regions || !regions.length || !stats) return null;
    // Only the regions Go returned stats for count (a stats object with none
    // of them is nothing to show).
    if (!regions.some((r) => stats[r.name])) return null;
    const teamA = teamAName || 'Team A';
    const teamB = teamBName || 'Team B';

    // Columns: teamA control/weak (side A's colour underline) |
    // contested/cont.weak/empty (neutral) | teamB weak/control (side B's).
    // `label` is the header text, `full` the title; the team-named columns
    // carry the team identity.
    const columns = [
        { label: teamA,        full: `${teamA} control`,      key: 'teamAControl',     team: teamA, teamIdx: sides[0] },
        { label: `${teamA} wk`, full: `${teamA} weak control`, key: 'teamAWeakControl', team: teamA, teamIdx: sides[0] },
        { label: 'Cont',       full: 'Contested',             key: 'contested',        team: '',    teamIdx: -1 },
        { label: 'Cont wk',    full: 'Contested (weak)',      key: 'weakContested',    team: '',    teamIdx: -1 },
        { label: 'Empty',      full: 'Empty (no players)',    key: 'empty',            team: '',    teamIdx: -1, noHeat: true },
        { label: `${teamB} wk`, full: `${teamB} weak control`, key: 'teamBWeakControl', team: teamB, teamIdx: sides[1] },
        { label: teamB,        full: `${teamB} control`,      key: 'teamBControl',     team: teamB, teamIdx: sides[1] },
    ];

    const rows = [];
    for (const region of regions) {
        const s = stats[region.name];
        if (!s) continue;
        const rowMax = Math.max(
            s.teamAControl, s.teamAWeakControl, s.contested,
            s.weakContested, s.teamBWeakControl, s.teamBControl) || 1;
        rows.push({
            name: region.name,
            cells: columns.map(c => {
                const v = s[c.key] || 0;
                const i = c.noHeat ? 0 : (rowMax > 0 ? v / rowMax : 0);
                return { i, p: v };
            }),
        });
    }

    return {
        rows, columns, teamCols: 0,
        cellTitle: (col, row, ci) => `${row.name} · ${col.full}: ${row.cells[ci].p}% of match`,
    };
}

// The markup of a matrix: `head` — the header row's cells (row-axis label +
// one team-underlined <th> per column) — and `rows`, each one <tr>'s cells.
// Each cell is viridis-shaded by its intensity with the % printed and a
// data-sort-value, so makeSortable can sort any column. `teamColors` is the
// canonical colour list (colors[i] belongs to the canonical order[i]).
export function heatmapTableHtml(data, firstColLabel, teamColors) {
    const { columns, teamCols } = data;

    const heads = [`<th>${escapeHtml(firstColLabel)}</th>`];
    columns.forEach((col, ci) => {
        const cls = ['heatmap-col'];
        if (col.kind === 'team') cls.push('heatmap-col-team');
        if (teamCols && ci === teamCols) cls.push('heatmap-col-sep');
        const color = col.teamIdx >= 0 ? teamColors[col.teamIdx % teamColors.length] : '';
        const style = color ? ` style="border-bottom: 3px solid ${color}"` : '';
        heads.push(`<th class="${cls.join(' ')}"${style} title="${escapeHtml(col.full || col.label)}">${escapeHtml(col.label)}</th>`);
    });

    const rows = data.rows.map((row) => {
        const tds = [`<td class="heatmap-rowname"><strong>${escapeHtml(row.name)}</strong></td>`];
        row.cells.forEach((cell, ci) => {
            const cls = ['heatmap-cell'];
            if (teamCols && ci === teamCols) cls.push('heatmap-col-sep');
            if (cell.p == null) { // column not present in this row (e.g. player never here)
                tds.push(`<td class="${cls.join(' ')}" data-sort-value="-1"></td>`);
                return;
            }
            const rgb = cell.i > 0 ? heatColorRGB(cell.i) : null;
            const style = rgb ? ` style="background: rgb(${rgb[0]}, ${rgb[1]}, ${rgb[2]}); color: ${contrastInk(rgb)}"` : '';
            const title = data.cellTitle ? data.cellTitle(columns[ci], row, ci) : '';
            const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
            // Cells default to "<p>%"; pass data.cellText to show another unit
            // (e.g. a raw count) while keeping cell.p as the sort key.
            const text = data.cellText ? data.cellText(cell) : `${Math.round(cell.p)}%`;
            tds.push(`<td class="${cls.join(' ')}"${style} data-sort-value="${cell.p}"${titleAttr}>${text}</td>`);
        });
        return tds.join('');
    });

    return { head: heads.join(''), rows };
}
