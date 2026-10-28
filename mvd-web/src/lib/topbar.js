// topbar.js — the top bar's demo line ("A vs B · score · map · date") and
// the page title for a loaded demo. The load (src/app/load.js) computes it
// once the canonical team order is set; src/shell/Topbar.svelte shows it.

import { escapeHtml } from './html.js';
import { playerStatsRows, teamFragTotals } from './player-stats.js';
import { isMultiPlayerIndividual } from './result.js';
import { matchWallClock } from './wall-clock.js';

// topbarView(result, order) → { html, title }. `order` is the teams store's
// canonical order.
export function topbarView(result, order) {
    const demoInfo = result?.demoInfo;
    // Display: the canonical shortname (dm2, e1m2) — how QW players name
    // maps. `match.mapTitle` is available for surfaces that want the long
    // level title; the topbar is not one of them. Never a key.
    const map = demoInfo?.map || result?.match?.map || '';
    // Same precedence as the Summary "Date" cell (matchDateHtml in
    // src/lib/wall-clock.js): KTX's own stamp where the demoinfo block
    // exists, the graded wall-clock anchor where it does not.
    const wallClock = demoInfo?.date ? null : matchWallClock(result);
    const date = demoInfo?.date || '';

    let teams = order.length >= 2 ? [...order] : [];
    if (teams.length < 2 && demoInfo?.teams) teams = [...demoInfo.teams];
    if (teams.length < 2 && result?.match?.teams) teams = result.match.teams.map(t => t.name);

    // playerStats first so the topbar score agrees with the scoreboard on
    // the Summary tab: its score.frags is the corrected net score, while
    // KTX's stats.frags resets after a reconnect.
    let teamScores = {};
    const psRows = playerStatsRows(result);
    if (psRows.length) {
        teamScores = teamFragTotals(psRows);
    } else if (demoInfo?.players) {
        teamScores = teamFragTotals(demoInfo.players);
    } else if (result?.match?.teams) {
        for (const t of result.match.teams) teamScores[t.name] = t.frags || 0;
    }

    const parts = [];
    // An individual match with more than two players is not a matchup, and
    // "toast vs SMOK" over a field of eight reads as one. Name the leader and
    // the size of the field instead; a two-player one (a duel, or a 1v1 FFA)
    // still renders as "A vs B" below, which is what it is.
    const individualField = isMultiPlayerIndividual(result);
    if (individualField) {
        const a = teams[0];
        parts.push(
            `<span class="topbar-team-a">${escapeHtml(a)}</span>` +
            (a in teamScores ? ` <span class="topbar-score">${teamScores[a]}</span>` : '')
        );
        parts.push(`<span class="topbar-field">${teams.length} players</span>`);
    } else if (teams.length >= 2) {
        const a = teams[0];
        const b = teams[1];
        parts.push(
            `<span class="topbar-team-a">${escapeHtml(a)}</span>` +
            ` <span class="topbar-vs">vs</span> ` +
            `<span class="topbar-team-b">${escapeHtml(b)}</span>`
        );
        if (a in teamScores || b in teamScores) {
            parts.push(
                `<span class="topbar-score">` +
                `${teamScores[a] ?? 0} - ${teamScores[b] ?? 0}` +
                `</span>`
            );
        }
    }
    if (map) parts.push(`<span class="topbar-map">${escapeHtml(map)}</span>`);
    if (date) parts.push(`<span class="topbar-date">${escapeHtml(date)}</span>`);
    else if (wallClock) parts.push(`<span class="topbar-date">${wallClock.html}</span>`);

    const titleParts = [];
    if (individualField) titleParts.push(teams[0]);
    else if (teams.length >= 2) titleParts.push(`${teams[0]} ${teams[1]}`);
    if (map) titleParts.push(map);
    return {
        html: parts.join('<span class="topbar-sep">·</span>'),
        title: titleParts.length ? `MVD | ${titleParts.join(' ')}` : 'MVD Analyzer',
    };
}
