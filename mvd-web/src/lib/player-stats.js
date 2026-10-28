// player-stats.js — readers over result.playerStats and the per-player
// rows shared by the load (src/app/load.js), the topbar and the tabs.

import { escapeHtml } from './html.js';

/**
 * A result.playerStats.players row (Result JSON, RESULT_SCHEMA PlayerStats).
 * Only the identity fields are named; the stat families stay open.
 * @typedef {{ name: string, team: string, score?: Record<string, any>, accuracy?: import('./accuracy.js').AccFamily, [field: string]: any }} PlayerStatsRow
 * A result.playerStats.teams row: the same families summed per team.
 * @typedef {{ name: string, score?: Record<string, any>, accuracy?: import('./accuracy.js').AccFamily, [field: string]: any }} PlayerStatsTeamRow
 * A row frags can be read off: a playerStats row or a legacy demoInfo
 * player, the two shapes fragsOfRow takes.
 * @typedef {{ team?: string, score?: { frags?: number }, stats?: { frags?: number } }} FragRow
 * A hold-time stat and its window (playerStats possession families).
 * @typedef {{ ms?: number, shareAlive?: number | null, runs?: number, longestMs?: number }} HoldStat
 * @typedef {{ aliveMs?: number, presentMs?: number, matchMs?: number }} HoldWindow
 */

// Frags off a row from either shape: a playerStats row (score.frags — the
// corrected svc_updatefrags net score) or a legacy demoInfo player
// (stats.frags). Both are read during the same render on demos where one
// panel has migrated and another has not, so the accessor takes both
// rather than each call site branching.
/**
 * @param {FragRow | null | undefined} p
 * @returns {number}
 */
export function fragsOfRow(p) {
    return p?.score?.frags ?? p?.stats?.frags ?? 0;
}

// Return a new array of players sorted by frags descending. The sort is
// stable (equal-frag players keep input order); this frag-sorted order is the
// canonical team order (src/state/teams.js), so its semantics must not change.
/**
 * @template {FragRow} P
 * @param {P[]} players
 * @returns {P[]}
 */
export function sortByFragsDesc(players) {
    return [...players].sort((a, b) => fragsOfRow(b) - fragsOfRow(a));
}

// Sum frags per team, returning a { teamName: totalFrags } map. Players
// with no team bucket under `emptyTeamKey` (default '' — the topbar /
// team-order convention; the Teams panel passes 'unknown' so a teamless
// bucket renders with a visible label).
/**
 * @param {FragRow[] | null | undefined} players
 * @param {string} [emptyTeamKey]
 * @returns {Record<string, number>}
 */
export function teamFragTotals(players, emptyTeamKey = '') {
    /** @type {Record<string, number>} */
    const totals = {};
    for (const p of (players || [])) {
        const t = p.team || emptyTeamKey;
        totals[t] = (totals[t] || 0) + fragsOfRow(p);
    }
    return totals;
}

// teamOrder: the canonical order (the teams store's `order`, set early in
// displayResults(), sorted by total frags) when it names two or more sides;
// otherwise the order the teams first appear in `sortedPlayers` (already
// frag-sorted).
/**
 * @param {string[] | null | undefined} canonical
 * @param {Array<{ team?: string }>} sortedPlayers
 * @returns {string[]}
 */
export function teamOrder(canonical, sortedPlayers) {
    if (canonical && canonical.length >= 2) return [...canonical];
    const seen = new Set();
    /** @type {string[]} */
    const order = [];
    for (const p of sortedPlayers) {
        const t = p.team || '';
        if (t && !seen.has(t)) { seen.add(t); order.push(t); }
    }
    return order;
}

// ─── playerStats accessors ─────────────────────────────────────────────────
//
// result.playerStats is the canonical per-player section (schema v61). The
// WASM entry point applies the KTX overlay before marshalling
// (mvd-web/cmd/wasm/main.go withPlayerStatsOverlay), so what reaches the
// browser is already merged — every family carries `src` saying whether the
// numbers came from KTX's demoinfo block or from this pipeline's
// reconstruction. The summary tab reads it and nothing else; the old
// four-source join across match.players / frags.byPlayer / frags.frags /
// demoInfo now lives in Go.

// Rows for the per-player tables. Empty array when the section is absent
// (a parse that produced no player streams — e.g. a race demo, which has
// no match at all), which the callers render as an empty table.
/**
 * @param {import('./result.js').Result | null | undefined} result
 * @returns {PlayerStatsRow[]}
 */
export function playerStatsRows(result) {
    return result?.playerStats?.players || [];
}

// Rows for the per-team tables. Absent on duels and FFA by construction.
/**
 * @param {import('./result.js').Result | null | undefined} result
 * @returns {PlayerStatsTeamRow[]}
 */
export function playerStatsTeamRows(result) {
    return result?.playerStats?.teams || [];
}

// The per-team tables render playerStats.teams verbatim. The sums are done
// ONCE in Go (analyzer aggregateTeamRows / view reaggregateTeams) rather
// than re-derived per panel here: hold figures in particular must be summed
// over the team's own alive time, and averaging per-player shares — which is
// what a JS reduce naturally does — is simply wrong.
// Rows arrive in the section's own order; the canonical order (`canonical`,
// through teamOrder) is still the colour index authority, so a team's stripe
// matches it everywhere else.
/**
 * @template {{ name: string, team?: string }} T
 * @param {T[]} teamRows
 * @param {string[] | null | undefined} canonical
 * @returns {T[]}
 */
export function teamRowsInColourOrder(teamRows, canonical) {
    const order = teamOrder(canonical, teamRows);
    /** @type {Record<string, T>} */
    const byName = {};
    for (const t of teamRows) byName[t.name] = t;
    /** @type {T[]} */
    const out = [];
    for (const name of order) if (byName[name]) out.push(byName[name]);
    // Any team the colour order doesn't know about still renders, after
    // the ones it does — dropping a row would silently lose a whole team.
    for (const t of teamRows) if (!order.includes(t.name)) out.push(t);
    return out;
}

// One possession cell of the Item Pickups table (and the Pickups tab's `s`
// columns): seconds spent holding the item, from OUR hold integral (KTX
// writes no weapon hold time at all, and its armor clock keeps counting
// after the armor is chewed to zero). Returns '-' when the item was never
// held, so a blank cell means "never", never "not measured".
//
// The share of ALIVE time and the run count go into the tooltip together
// with the raw ms and the row's window: a share is only as meaningful as
// its denominator, and a player the streams barely saw can get a presence
// window that is a FLOOR rather than a measurement (possessionExtent in
// analyzer/player_stats.go says so itself) — a confident "100%" over a few
// seconds of an 18-minute match. The seconds cannot express that; the
// numbers behind them can, so they are one hover away.
//
// data-sort-value keeps the raw ms so the column sorts numerically rather
// than on the rendered "12s".
/**
 * @param {HoldStat | null | undefined} stat
 * @param {HoldWindow | null} [win]
 * @returns {string}
 */
export function holdCell(stat, win) {
    if (!stat || !stat.ms) return `<td data-sort-value="0">-</td>`;
    const bits = [`held ${stat.ms} ms`];
    if (stat.shareAlive != null) bits.push(`${(stat.shareAlive * 100).toFixed(0)}% of alive time`);
    bits.push(`${stat.runs || 0} run${stat.runs === 1 ? '' : 's'}`);
    if (stat.longestMs) bits.push(`longest ${Math.round(stat.longestMs / 1000)}s`);
    // Team rows sum alive/present over members while matchMs stays the
    // single match length (aggregateTeamRows), so present > match is
    // expected there — label it so the triple doesn't read as broken.
    if (win) {
        const summed = (win.presentMs || 0) > (win.matchMs || 0) ? ', member-summed' : '';
        bits.push(`window alive ${win.aliveMs || 0} / present ${win.presentMs || 0} / match ${win.matchMs || 0} ms${summed}`);
    }
    const title = escapeHtml(bits.join(' · '));
    return `<td data-sort-value="${stat.ms}" title="${title}">${Math.round(stat.ms / 1000)}s</td>`;
}
