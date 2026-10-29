// keymoments.js — the Key Moments tab's DOM-free parts: the stored lists
// (powerup runs, frag streaks, demo markers, the highlight catalogue) read
// off the Result at seconds, the powerup-run filter, the two view queries'
// options, the highlight rankings, and every table's row markup.
//
// Rows are markup strings (the tab hands them to the shared makeSortable,
// which sorts by moving row nodes). Every row builder takes the same `ctx`:
//   { gameId, userIDs, demoOffset }
// — the hub game id (absent for a local file: no Hub links), the pipeline's
// name → userid map, and streams.global.demoOffset in seconds (hub from/to
// are demo-relative; the tables show match time).

import { escapeHtml } from './html.js';
import { hubReplayUrl, hubLinkHtml } from './hub-url.js';
import { formatDuration } from './result.js';

/**
 * @typedef {import('./result.js').Result} Result
 * A row of raw Result / view-reply JSON (a powerup run, a highlight, a top
 * kill...), open like the Result itself, plus what the readers here add.
 * @typedef {Record<string, any>} Row
 * The row builders' shared context (see above).
 * @typedef {{ gameId?: number | string | null, userIDs: Record<string, number>, demoOffset: number }} RowCtx
 * @typedef {{ minDur: number, minFrags: number }} RunFilter
 * @typedef {'topdmg' | 'toprl' | 'toplg'} TopKey
 * @typedef {object} HighlightTable
 * @property {string} id
 * @property {string} defaultSort
 * @property {(result: Result | null | undefined) => Row[]} rows
 * @property {(row: Row, ctx: RowCtx) => string} rowHtml
 */

// ─── Powerup-run filter ─────────────────────────────────────────────────────
//
// A run is listed only when it lasted at least `minDur` seconds AND scored
// at least `minFrags` frags — the reading of "min length 5s and min frags 3"
// as a conjunction, which hides low-value quad cycles by default (a quad
// spawns every 60 s, so 0-2-frag runs are routine noise; pent/ring runs
// clearing 3 frags are the ones worth a highlight list too). Both thresholds
// are editable down to 0 in the panel and the Result keeps every run either
// way, so this is a UI control rather than a pipeline filter (CLAUDE.md
// "surface, don't filter"). Back to the defaults on every demo load.
export const KEYMOMENTS_FILTER_DEFAULTS = { minDur: 5, minFrags: 3 };

// The value an input's text settles on.
/**
 * @param {string} raw
 * @param {keyof RunFilter} key
 */
export function cleanFilterValue(raw, key) {
    // Number.isFinite, not isNaN: "1e309" parses to Infinity, which would
    // silently filter every run while the input displays blank.
    const v = parseFloat(raw);
    let clean = Number.isFinite(v) ? Math.max(0, v) : 0;
    if (key === 'minFrags') clean = Math.ceil(clean);
    return clean;
}

// Schema v57: powerupEvents / fragStreaks carry `time` (int32 ms) plus the
// descriptive endTime / duration (also ms). Converted to seconds at intake so
// formatDuration, the clock seek and the hub URL `from`/`to` all see seconds.
/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
export function powerupRuns(result) {
    return (result?.timelineAnalysis?.powerupEvents || []).map(/** @param {Row} ev */ ev => ({
        ...ev,
        time: ev.time * 0.001,
        endTime: (ev.endTime || 0) * 0.001,
        duration: (ev.duration || 0) * 0.001,
    }));
}

// duration is in seconds by here, matching the "min s" input. Compare the
// ROUNDED seconds — the column displays Math.round(duration), and a 4.6s run
// rendered as "5s" must not be hidden by a 5s threshold.
/**
 * @param {Row[]} runs
 * @param {RunFilter} filter
 */
export function filterPowerupRuns(runs, filter) {
    return runs.filter(ev =>
        Math.round(ev.duration) >= filter.minDur && (ev.frags || 0) >= filter.minFrags);
}

/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
export function fragStreaks(result) {
    return (result?.timelineAnalysis?.fragStreaks || []).map(/** @param {Row} s */ s => ({
        ...s,
        time: s.time * 0.001,
        endTime: (s.endTime || 0) * 0.001,
        duration: (s.duration || 0) * 0.001,
    }));
}

// User-inserted `/demomark` bookmarks. Rare (0-10 per demo) and routinely
// absent. `time` is int32 ms and may be negative for markers dropped during
// warmup (before match start).
/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
export function demoMarkers(result) {
    return (result?.timelineAnalysis?.demoMarkers || []).map(/** @param {Row} m */ m => ({ ...m, time: m.time * 0.001 }));
}

/** @param {string} type */
function powerupLabel(type) {
    switch (type) {
        case 'quad': return 'Quad';
        case 'pent': return 'Pent';
        case 'ring': return 'Ring';
        default: return type;
    }
}

// formatDuration assumes a non-negative time (Math.floor on a negative value
// underflows both fields, e.g. -30.5 → "-1:-31"). Demo markers can carry a
// negative match-relative time when a player marks during warmup, so format
// the magnitude and prefix a "-".
/** @param {number} seconds */
export function formatMarkerTime(seconds) {
    if (seconds < 0) return `-${formatDuration(-seconds)}`;
    return formatDuration(seconds);
}

// ─── Stored tables: powerup runs, frag streaks, demo markers ───────────────

/**
 * @param {Row} event
 * @param {RowCtx} ctx
 */
export function powerupRunRowHtml(event, ctx) {
    // A slot number is NOT a userid — falling back to one tracks whichever
    // player happens to hold that userid, and a rare wrong link is the kind
    // nobody reports. No userid, no link (same rule as hubWatchLinkHtml and
    // the pack-drop anchors).
    let watchCell = '-';
    if (ctx.gameId && event.playerUserID) {
        const fromTime = Math.max(0, Math.floor(event.time + ctx.demoOffset) - 10);
        const toTime = Math.floor(event.endTime + ctx.demoOffset) + 5;
        watchCell = hubLinkHtml(hubReplayUrl({ gameId: ctx.gameId, from: fromTime, to: toTime, track: event.playerUserID }));
    }
    return `
            <td class="time-cell time-link">${formatDuration(event.time)}</td>
            <td class="powerup-cell ${event.powerupType}">${powerupLabel(event.powerupType)}</td>
            <td>${escapeHtml(event.playerName || 'Unknown')}</td>
            <td>${escapeHtml(event.team || '-')}</td>
            <td>${event.frags || 0}</td>
            <td>${Math.round(event.duration)}s</td>
            <td>${watchCell}</td>
        `;
}

/**
 * @param {Row} streak
 * @param {RowCtx} ctx
 */
export function streakRowHtml(streak, ctx) {
    let watchCell = '-';
    if (ctx.gameId) {
        const fromTime = Math.max(0, Math.floor(streak.time + ctx.demoOffset));
        const toTime = Math.floor(streak.endTime + ctx.demoOffset) + 3;
        watchCell = hubLinkHtml(hubReplayUrl({ gameId: ctx.gameId, from: fromTime, to: toTime, track: streak.playerUserID || 0 }));
    }
    const mainWep = streak.ewep ? streak.ewep.toUpperCase() : '-';
    return `
                <td class="time-cell time-link">${formatDuration(streak.time)}</td>
                <td>${escapeHtml(streak.playerName || 'Unknown')}</td>
                <td>${escapeHtml(streak.team || '-')}</td>
                <td>${streak.frags}</td>
                <td>${escapeHtml(mainWep)}</td>
                <td>${Math.round(streak.duration)}s</td>
                <td>${watchCell}</td>
            `;
}

/**
 * @param {Row} marker
 * @param {RowCtx} ctx
 */
export function markerRowHtml(marker, ctx) {
    let watchCell = '-';
    if (ctx.gameId) {
        const fromTime = Math.max(0, Math.floor(marker.time + ctx.demoOffset) - 10);
        watchCell = hubLinkHtml(hubReplayUrl({ gameId: ctx.gameId, from: fromTime, to: fromTime + 20, track: marker.playerUserID || 0 }));
    }
    // A spectator's mark has no meaningful team (and its userID is not a
    // useful Hub track target, but the link still frames the right time
    // window).
    const teamCell = marker.spectator ? 'spec' : (marker.team || '-');
    return `
                <td class="time-cell time-link">${formatMarkerTime(marker.time)}</td>
                <td>${escapeHtml(marker.playerName || 'Unknown')}</td>
                <td>${escapeHtml(teamCell)}</td>
                <td>${escapeHtml(marker.label || '-')}</td>
                <td>${watchCell}</td>
            `;
}

// ─── Top damage windows and top kills (view queries) ───────────────────────
//
// These three tables are NOT stored Result fields: they are view-layer
// rankings (view.TopWindows / view.TopKills) computed on demand from the
// analysed demo the worker still holds, so they arrive after the rest of the
// tab has painted. Each table settles on its own — a query the demo cannot
// answer (no damage log, no measurable liveness, no bounded family) leaves
// that table's empty state up and never disturbs its neighbours, exactly as
// the powerup table stays independent of the frag streaks.
//
// dmg is named explicitly on every query: the VIEW default is raw while
// mvd-api's is bounded, and these tables show the same numbers the API
// does. A demo whose bounded family was never reconstructed (BoundedMode
// "skipped:*" — the KTX midair / instagib / dmgfrags modes) therefore
// answers ErrBoundedUnavailable and the table shows its empty state, rather
// than quietly ranking a different damage family under the same heading.

// The window length for the damage-window ranking. 10 s is the "he just went
// off" unit — long enough to hold a whole engagement's damage, short enough
// that the window is one fight rather than a lull plus a fight. Ranked by
// BOUNDED enemy damage (metric=damageGiven) so the table complements the
// per-kill burst tables beside it: same family, same scoreboard semantics;
// ties break on the window's frags (the view's complementary tie-break).
const TOP_RUN_WINDOW_MS = 10000;

// [key, view fn, opts] per table; key is the table's id prefix.
//
// The kill-burst gaps are the end user's per-weapon sweet spots. These go
// out as gapMs — the EXACT per-weapon walk — rather than capturing at the
// default 3000 and narrowing on maxGapMs: that filter exists for REST
// consumers who already hold a capture-3000 response and want a tighter gap
// without a second request. In-process a second request is free. See
// mvd-analytics/view/topkills.go. RL gets the wide list beside the damage
// windows; LG the short one beside the demo markers — the RL burst is the
// headline highlight in team games.
/** @type {Array<[TopKey, string, object]>} */
export const TOP_QUERIES = [
    ['topdmg', 'getTopWindows', { metric: 'damageGiven', windowMs: TOP_RUN_WINDOW_MS, limit: 10, dmg: 'bounded' }],
    ['toprl', 'getTopKills', { weapons: ['rl'], gapMs: 2300, limit: 10, dmg: 'bounded' }],
    ['toplg', 'getTopKills', { weapons: ['lg'], gapMs: 1200, limit: 5, dmg: 'bounded' }],
];

// A windowed Hub clip link for a view row. Same rule as the powerup /
// pack-drop anchors: a slot number is NOT a userid, so a name that
// timelineAnalysis.playerUserIDs does not resolve gets NO link rather than
// one that follows whoever happens to hold that id. Times in, seconds.
/**
 * @param {string} playerName
 * @param {number} fromSec
 * @param {number} toSec
 * @param {RowCtx} ctx
 */
export function topWatchCell(playerName, fromSec, toSec, ctx) {
    if (!ctx.gameId) return '-';
    const trackId = ctx.userIDs[playerName];
    if (!trackId) return '-';
    // Match-relative → demo-relative (Hub time includes countdown/warmup).
    const from = Math.max(0, Math.floor(fromSec + ctx.demoOffset));
    const to = Math.floor(toSec + ctx.demoOffset);
    return hubLinkHtml(hubReplayUrl({ gameId: ctx.gameId, from, to, track: trackId }));
}

// The rows of one view reply (null when the query failed), each with its
// seek time `t` in seconds: windows of a TopWindowsView for topdmg, kills
// of a TopKillsView otherwise.
/**
 * @param {TopKey} key
 * @param {Row | null} v  the view reply
 * @returns {Row[]}
 */
export function topRows(key, v) {
    if (key === 'topdmg') {
        return (v?.windows || []).map(/** @param {Row} w */ w => ({ ...w, t: w.start * 0.001, startSec: w.start * 0.001, endSec: w.end * 0.001 }));
    }
    return (v?.kills || []).map(/** @param {Row} k */ k => ({ ...k, t: k.time * 0.001, timeSec: k.time * 0.001 }));
}

/**
 * @param {TopKey} key
 * @param {Row} row
 * @param {RowCtx} ctx
 */
export function topRowHtml(key, row, ctx) {
    return key === 'topdmg' ? topWindowRowHtml(row, ctx) : topKillRowHtml(row, ctx);
}

/**
 * @param {Row} w
 * @param {RowCtx} ctx
 */
function topWindowRowHtml(w, ctx) {
    // The clip is the window plus a 1 s lead-in — 11 s total, per the
    // product ruling: enough not to open mid-swing, short enough that the
    // clip IS the window. NOTE the hub URL's from/to are DEMO-relative
    // (recording start), while this table displays match-relative time, so
    // the hub scrubber reads demoOffset-1 higher than the Start cell; the
    // content is aligned even though the numbers differ.
    const watchCell = topWatchCell(w.player, w.startSec - 1, w.endSec, ctx);
    // score IS the window's bounded enemy damage (the ranking metric); kills
    // comes from the stats block (IntervalStats is embedded, so its fields
    // are inline on the row — the JSON key is `kills`, not `frags`) and is
    // also what broke any damage tie, per the view's complementary tie-break.
    return `
            <td class="time-cell time-link">${formatDuration(w.startSec)}</td>
            <td>${escapeHtml(w.player || 'Unknown')}</td>
            <td>${escapeHtml(w.team || '-')}</td>
            <td>${w.score ?? 0}</td>
            <td>${w.kills ?? 0}</td>
            <td>${watchCell}</td>
        `;
}

/**
 * @param {Row} k
 * @param {RowCtx} ctx
 */
function topKillRowHtml(k, ctx) {
    // spanMs is the burst's own length (first hit → kill), so the clip
    // covers the whole burst plus a lead-in, not just the killing hit.
    const burstStartSec = k.timeSec - (k.spanMs || 0) * 0.001;
    const watchCell = topWatchCell(k.killer, burstStartSec - 3, k.timeSec + 3, ctx);
    const victimWep = k.victimWep ? k.victimWep.toUpperCase() : '-';
    return `
            <td class="time-cell time-link">${formatDuration(k.timeSec)}</td>
            <td>${escapeHtml(k.killer || 'Unknown')} → ${escapeHtml(k.victim || 'Unknown')}</td>
            <td>${k.damage ?? 0}</td>
            <td>${k.hits ?? 0}</td>
            <td>${((k.spanMs || 0) * 0.001).toFixed(1)} s</td>
            <td>${escapeHtml(victimWep)}</td>
            <td>${k.returnDamage ?? 0}</td>
            <td>${watchCell}</td>
        `;
}

// ─── Highlight catalogue (airgibs / discharges / quadbores / telefrags) ────
//
// result.highlights (schema v76) is the stored catalogue: one row shape for
// every kind — an actor, the victims it touched, kill counters — with each
// participant's state read just before the instant (health/armor/stack,
// weapons, powerups, loc). The tables are plain renderings of those lists;
// the toplist IS the sort, which makeSortable hands to the viewer. Each
// table opens on its default sort column (HIGHLIGHT_TABLES), its rows laid
// out in that order; every row carries `t`, its seek time in seconds.

const AIRGIB_LEAD_S = 2.5;

// The Hub replay link from the actor's userid (the JSON key is `userId` on
// HighlightPlayer): `lead` seconds before the moment (5 s; an airgib, owner's
// call, 2.5 s — the shot itself is the point) to 3 s after.
/**
 * @param {RowCtx} ctx
 * @param {number} timeSec
 * @param {number | undefined} trackId
 * @param {number} [lead]
 */
function highlightWatchCell(ctx, timeSec, trackId, lead = 5) {
    if (!ctx.gameId) return '-';
    const fromTime = Math.max(0, Math.floor(timeSec + ctx.demoOffset - lead));
    const toTime = Math.floor(timeSec + ctx.demoOffset) + 3;
    return hubLinkHtml(hubReplayUrl({ gameId: ctx.gameId, from: fromTime, to: toTime, track: trackId || 0 }));
}

// "health/armor+type" ("118/180ra", "100/0"); "?" when the streams never
// recorded the player (stateSource "").
/** @param {Row | null | undefined} p  a HighlightPlayer */
export function highlightStack(p) {
    if (!p || p.health == null) return '?';
    const armor = p.armor ?? 0;
    const at = armor > 0 && p.armorType ? p.armorType : '';
    const spawn = p.stateSource === 'spawn' ? ' <span class="hl-spawn">(spawn)</span>' : '';
    // Armor value + type carry the timeline's armor colors and caps ("180 RA").
    const armorStr = at
        ? `<span class="armor-${escapeHtml(at)}">${armor} ${escapeHtml(at.toUpperCase())}</span>`
        : `${armor}`;
    return `<span class="hl-stack">${p.health}/${armorStr}</span>${spawn}`;
}

// The tracked inventory and powerups: "RL LG · Quad". Weapons take the
// timeline's weapon colors and caps; powerups render exactly as the powerup
// list does (powerupLabel + .powerup-cell colors), full size — a quad is the
// headline of the row.
/** @param {Row | null | undefined} p  a HighlightPlayer */
export function highlightHad(p) {
    if (!p) return '';
    const weapons = (p.weapons || [])
        .map(/** @param {string} w */ w => `<span class="wep-${escapeHtml(w)}">${escapeHtml(w.toUpperCase())}</span>`).join(' ');
    const pus = (p.powerups || [])
        .map(/** @param {string} x */ x => `<span class="powerup-cell ${escapeHtml(x)}">${escapeHtml(powerupLabel(x))}</span>`).join(' ');
    const weps = weapons ? `<span class="hl-had">${weapons}</span>` : '';
    return `${weps}${weapons && pus ? ' ' : ''}${pus}`;
}

// The shared "what they had" cell: stack + had.
/** @param {Row | null | undefined} p */
function highlightStateCell(p) {
    const had = highlightHad(p);
    return `${highlightStack(p)}${had ? ' ' + had : ''}`;
}

/** @param {string | undefined} rel */
function relationBadge(rel) {
    const r = rel === 'team' || rel === 'self' ? rel : 'enemy';
    return `<span class="hl-badge hl-${r}">${r}</span>`;
}

// One victim as its own line, no relation badge (the column already says
// the side): name, stack, had. Hurt-not-killed victims are dimmed.
/** @param {Row} v */
function victimLine(v) {
    const cls = v.killed || v.survived ? 'hl-vline' : 'hl-vline hl-hurt';
    return `<span class="${cls}"><span class="hl-name">${escapeHtml(v.name || '?')}</span> ${highlightStateCell(v)}</span>`;
}

// One victim: relation badge, name, stack, had, damage taken. Hurt-not-killed
// victims (damage-log only) are dimmed; a deflect's surviving pentagram
// holder gets its own badge.
/** @param {Row} v */
function victimChip(v) {
    const badge = v.survived ? '<span class="hl-badge hl-survived">survived</span>' : relationBadge(v.relation);
    const cls = v.killed || v.survived ? 'hl-chip' : 'hl-chip hl-hurt';
    const dmg = v.damage ? ` <span class="hl-stack">−${v.damage}</span>` : '';
    return `<span class="${cls}">${badge}<span class="hl-name">${escapeHtml(v.name || '?')}</span> ${highlightStateCell(v)}${dmg}</span>`;
}

const KTX_QUAD_MS = 30000; // KTX quad duration; quadHeldMs is measured, the remainder is derived here

/** @type {(list: Row[] | null | undefined) => Row[]} */
const withSec = list => (list || []).map(e => ({ ...e, t: e.time * 0.001 }));
/** @param {Row} e */
const timeCell = e => `<td class="time-cell time-link" data-sort-value="${e.time}">${formatDuration(e.t)}</td>`;
/** @param {Row} e */
const locOf = e => e.actor?.loc || e.victims?.[0]?.loc || '';

// Direct rocket air hits, lethal or not (the Lethal column badges the ones
// that gibbed). Default view is height-above-shooter descending — the
// vertical gap the rocket climbed is what makes a hit look spectacular (the
// analyzer ships the list ordered by floor height). Rows are
// highlights.airgibs (schema v76, the list's only home): HighlightEvent —
// actor / victims[0], the victim row carrying the state.
/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
function airgibRows(result) {
    const data = (result?.highlights?.airgibs || []).map(/** @param {Row} h */ h => ({
        time: h.time, t: h.time * 0.001,
        height: h.height, heightAboveAttacker: h.heightAboveAttacker,
        attacker: h.actor?.name, attackerUserID: h.actor?.userId,
        victim: h.victims?.[0]?.name, lethal: !!h.victims?.[0]?.killed,
        loc: h.victims?.[0]?.loc, state: h.victims?.[0] || null,
    }));
    return data.sort(/** @param {Row} a @param {Row} b */ (a, b) => ((b.heightAboveAttacker ?? 0) - (a.heightAboveAttacker ?? 0)) || (a.time - b.time));
}

// heightAboveAttacker is omitted on the wire for a dead-level 0 (omitempty)
// and when the shooter had no position sample near the hit — the neutral 0
// covers both. Heights are float32 units; display rounds to 1 decimal while
// data-sort-value keeps the raw value for sorting (as every numeric / raw
// column here does, so display strings like "1:23" don't mis-sort).
/** @param {number} v */
const round1 = v => Math.round(v * 10) / 10;
/**
 * @param {Row} a
 * @param {RowCtx} ctx
 */
function airgibRowHtml(a, ctx) {
    const lethalCell = a.lethal ? '<span class="airgib-lethal">gib</span>' : '';
    const aboveShooter = a.heightAboveAttacker ?? 0;
    return `
            <td data-sort-value="${a.height}">${round1(a.height)}</td>
            <td data-sort-value="${aboveShooter}">${round1(aboveShooter)}</td>
            <td>${escapeHtml(a.attacker || 'Unknown')}</td>
            <td>${escapeHtml(a.victim || 'Unknown')}</td>
            <td>${escapeHtml(a.loc || '-')}</td>
            <td data-sort-value="${a.lethal ? 1 : 0}">${lethalCell}</td>
            <td>${a.state ? highlightStateCell(a.state) : ''}</td>
            <td class="time-cell time-link" data-sort-value="${a.time}">${formatDuration(a.t)}</td>
            <td>${highlightWatchCell(ctx, a.t, a.attackerUserID, AIRGIB_LEAD_S)}</td>
        `;
}

// Discharges — ranked by kill VALUE: killed enemies holding RL or LG, +1
// more per killed enemy with quad (armed kills over respawn fodder); ties by
// enemy kills, then bounded enemy damage.
/** @param {Row} e  a highlight */
export function dischargeValue(e) {
    return (e.victims || []).reduce(/** @param {number} n @param {Row} v */ (n, v) => {
        if (!v.killed || v.relation === 'team') return n;
        const armed = (v.weapons || []).some(/** @param {string} w */ w => w === 'rl' || w === 'lg');
        const quad = (v.powerups || []).includes('quad');
        return n + (armed ? 1 : 0) + (quad ? 1 : 0);
    }, 0);
}

/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
function dischargeRows(result) {
    /** @type {Row[]} */
    const rows = withSec(result?.highlights?.discharges).map(e => ({ ...e, value: dischargeValue(e) }));
    return rows.sort((a, b) => (b.value - a.value) || (b.enemyKills - a.enemyKills)
        || ((b.damageEnemy || 0) - (a.damageEnemy || 0)) || ((b.damage || 0) - (a.damage || 0)) || (a.time - b.time));
}

/**
 * @param {Row} e
 * @param {RowCtx} ctx
 */
function dischargeRowHtml(e, ctx) {
    const enemy = e.enemyKills || 0;
    const team = e.teamKills || 0;
    const self = e.actor?.killed ? 1 : 0;
    // KTX scores an enemy kill +1, a teamkill −1, a suicide −1.
    const delta = enemy - team - self;
    const enemyV = (e.victims || []).filter(/** @param {Row} v */ v => v.relation !== 'team').map(victimLine).join('');
    const teamV = (e.victims || []).filter(/** @param {Row} v */ v => v.relation === 'team').map(victimLine).join('');
    return `
                ${timeCell(e)}
                <td data-sort-value="${escapeHtml(e.actor?.name || '')}">${escapeHtml(e.actor?.name || 'Unknown')} ${highlightStateCell(e.actor)}</td>
                <td data-sort-value="${e.cells ?? -1}">${e.cells ?? '?'}</td>
                <td data-sort-value="${e.value}">${e.value || '-'}</td>
                <td data-sort-value="${enemy}">${enemy || '-'}</td>
                <td data-sort-value="${team}">${team || '-'}</td>
                <td data-sort-value="${self}">${self || '-'}</td>
                <td data-sort-value="${delta}">${delta > 0 ? '+' + delta : delta}</td>
                <td data-sort-value="${e.damageEnemy || 0}">${e.damageEnemy || '-'}</td>
                <td data-sort-value="${e.damageTeam || 0}">${e.damageTeam || '-'}</td>
                <td>${enemyV || '-'}</td>
                <td>${teamV || '-'}</td>
                <td>${escapeHtml(locOf(e) || '-')}</td>
                <td>${highlightWatchCell(ctx, e.t, e.actor?.userId)}</td>
            `;
}

// Quadbores — most quad thrown away first.
/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
function quadboreRows(result) {
    /** @type {Row[]} */
    const rows = withSec(result?.highlights?.quadbores).map(e => ({ ...e, quadLeft: Math.max(0, KTX_QUAD_MS - (e.quadHeldMs || 0)) }));
    return rows.sort((a, b) => (b.quadLeft - a.quadLeft) || (a.time - b.time));
}

/**
 * @param {Row} e
 * @param {RowCtx} ctx
 */
function quadboreRowHtml(e, ctx) {
    const victims = (e.victims || []).map(victimChip).join('');
    return `
                ${timeCell(e)}
                <td data-sort-value="${escapeHtml(e.actor?.name || '')}">${escapeHtml(e.actor?.name || 'Unknown')}${victims ? ' <span class="hl-had">took</span> ' + victims : ''}</td>
                <td>${escapeHtml(e.weapon || '?')}</td>
                <td data-sort-value="${e.quadLeft}">${(e.quadLeft / 1000).toFixed(1)} s</td>
                <td data-sort-value="${e.quadFrags || 0}">${e.quadFrags || 0}</td>
                <td data-sort-value="${e.actor?.stack ?? -1}">${highlightStack(e.actor)}</td>
                <td>${highlightHad(e.actor) || '-'}</td>
                <td>${escapeHtml(locOf(e) || '-')}</td>
                <td>${highlightWatchCell(ctx, e.t, e.actor?.userId)}</td>
            `;
}

// Telefrags — most stacked victim first; rows with no killed victim (a
// deflect / spawnicide: the teleporter died) sort last.
/**
 * @param {Result | null | undefined} result
 * @returns {Row[]}
 */
function telefragRows(result) {
    /** @type {Row[]} */
    const rows = withSec(result?.highlights?.telefrags).map(e => {
        const victim = (e.victims || []).find(/** @param {Row} v */ v => v.killed) || null;
        const survivor = (e.victims || []).find(/** @param {Row} v */ v => v.survived) || null;
        return { ...e, victim, survivor, stack: victim?.stack ?? -1 };
    });
    return rows.sort((a, b) => (b.stack - a.stack) || (a.time - b.time));
}

/**
 * @param {Row} e
 * @param {RowCtx} ctx
 */
function telefragRowHtml(e, ctx) {
    const kind = e.teleKind || 'telefrag';
    const subject = e.victim || e.survivor;
    const relation = e.victim ? relationBadge(e.victim.relation)
        : (e.survivor ? '<span class="hl-badge hl-survived">survived</span>' : '-');
    const victimName = e.victim ? e.victim.name : (e.survivor ? e.survivor.name : '-');
    // On a deflect / spawnicide the actor is the one who died; say so.
    const actorCell = `${escapeHtml(e.actor?.name || 'Unknown')}${e.actor?.killed ? ' <span class="hl-badge hl-self">died</span>' : ''}`;
    return `
                ${timeCell(e)}
                <td data-sort-value="${escapeHtml(e.actor?.name || '')}">${actorCell}</td>
                <td data-sort-value="${escapeHtml(victimName)}">${escapeHtml(victimName)}</td>
                <td data-sort-value="${escapeHtml(e.victim?.relation || (e.survivor ? 'survived' : ''))}">${relation}</td>
                <td data-sort-value="${e.stack}">${subject ? highlightStack(subject) : '-'}</td>
                <td>${subject ? (highlightHad(subject) || '-') : '-'}</td>
                <td data-sort-value="${kind}"><span class="hl-badge hl-kind">${kind}</span></td>
                <td>${escapeHtml(locOf(e) || '-')}</td>
                <td>${highlightWatchCell(ctx, e.t, e.actor?.userId)}</td>
            `;
}

// The four highlight tables: id prefix, default sort column, rows (in the
// default order), row markup.
/** @type {HighlightTable[]} */
export const HIGHLIGHT_TABLES = [
    { id: 'airgibs', defaultSort: 'aboveShooter', rows: airgibRows, rowHtml: airgibRowHtml },
    { id: 'discharges', defaultSort: 'value', rows: dischargeRows, rowHtml: dischargeRowHtml },
    { id: 'quadbores', defaultSort: 'quadLeft', rows: quadboreRows, rowHtml: quadboreRowHtml },
    { id: 'telefrags', defaultSort: 'stack', rows: telefragRows, rowHtml: telefragRowHtml },
];
