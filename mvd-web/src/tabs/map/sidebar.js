// sidebar.js — the Map tab's two hand-built sidebar panels: the legend (one
// sortable table per team, rows built once per demo, cells refreshed on the
// clock) and the region status (one row per region with the players in it
// as composited symbol icons). Built by hand rather than by Svelte because
// makeSortable sorts the legend by moving its row nodes, and the region
// icons are cached canvases. The cell contents are src/lib/map.js.

import { drawPlayerSymbolAt, drawBadgesAroundCenter, PLAYER_SYMBOL_BASE_SIZE } from '../../../../mvd-map-view/src/index.js';
import { makeSortable } from '../../ui/sortable.js';
import { fragsAt } from '../../lib/timeline.js';
import {
    legendTeams, legendRowHtml, LEGEND_HEAD, teamFragsAt, stackCell, weaponsText, regionStatusRows,
} from '../../lib/map.js';

/**
 * @typedef {import('../../../../mvd-map-view/src/index.js').MvdMap} MvdMap
 * @typedef {import('../../lib/map.js').PlayerSymbols} PlayerSymbols
 * @typedef {import('../../lib/map.js').PlayerSymbol} PlayerSymbol
 * @typedef {import('../../lib/map.js').PlayerFrame} PlayerFrame
 */

// buildLegend fills `el` with a titled table per team.
/**
 * @param {HTMLElement} el
 * @param {string[]} teams
 * @param {PlayerSymbols} symbols
 * @param {string[]} colors
 */
export function buildLegend(el, teams, symbols, colors) {
    el.innerHTML = '';
    for (const block of legendTeams(teams, symbols, colors)) {
        const title = document.createElement('h4');
        title.style.color = block.color;
        title.id = `map-legend-team-title-${block.ti}`;
        title.textContent = `${block.team} — 0 frags`;
        el.appendChild(title);
        const table = document.createElement('table');
        table.className = 'team-status-table';
        table.innerHTML = LEGEND_HEAD;
        const tbody = document.createElement('tbody');
        tbody.className = 'map-legend-tbody';
        for (const p of block.players) {
            const tr = document.createElement('tr');
            tr.dataset.player = p.name;
            tr.innerHTML = legendRowHtml(p.name, p.symbol, block.color);
            tbody.appendChild(tr);
        }
        table.appendChild(tbody);
        el.appendChild(table);
    }
    /** @type {NodeListOf<HTMLTableElement>} */ (el.querySelectorAll('.team-status-table')).forEach(makeSortable);
}

// syncLegend fills the legend at `time`: team frag totals, each player's
// loc, stack and weapons.
/**
 * @param {HTMLElement} el
 * @param {MvdMap} view
 * @param {import('../../lib/result.js').Result} result
 * @param {number} time
 */
export function syncLegend(el, view, result, time) {
    const s = view.state;
    const frame = view.frameAt(time);
    const pd = frame ? frame.p : null;
    const frags = fragsAt(result, time);
    s.teams.forEach((/** @type {string} */ team, /** @type {number} */ ti) => {
        const t = el.querySelector(`#map-legend-team-title-${ti}`);
        if (t) t.textContent = `${team} — ${teamFragsAt(team, s.playerSymbols, frags)} frags`;
    });
    // Every legend cell carries data-player (legendRowHtml).
    for (const cell of /** @type {NodeListOf<HTMLElement>} */ (el.querySelectorAll('.map-legend-loc'))) {
        const d = pd?.[/** @type {string} */ (cell.dataset.player)];
        cell.textContent = d && !(d.x === 0 && d.y === 0) ? (view.resolvePlayerLoc(d) || '') : '';
    }
    for (const cell of /** @type {NodeListOf<HTMLElement>} */ (el.querySelectorAll('.map-legend-stack'))) {
        const c = stackCell(pd?.[/** @type {string} */ (cell.dataset.player)]);
        cell.dataset.sortValue = c.sort;
        if (c.html === null) cell.textContent = '-';
        else cell.innerHTML = c.html;
    }
    for (const cell of /** @type {NodeListOf<HTMLElement>} */ (el.querySelectorAll('.map-legend-wpn'))) {
        cell.textContent = weaponsText(pd?.[/** @type {string} */ (cell.dataset.player)]);
    }
}

// syncRegionStatus rebuilds the region rows at `time`. `iconCache` caches
// the composited player icons per (name, letter, colour, badges) for the
// demo: a player is in one region per frame, so a cached node is appended
// once.
/**
 * @param {HTMLElement | null | undefined} el
 * @param {MvdMap} view
 * @param {number} time
 * @param {string[]} colors
 * @param {Map<string, HTMLCanvasElement>} iconCache
 */
export function syncRegionStatus(el, view, time, colors, iconCache) {
    const s = view.state;
    if (!el || !s.controlRegions || s.controlRegions.length === 0) return;
    const states = view.regionControlAt(time);
    const frame = states ? view.frameAt(time) : null;
    const rows = !states ? [] : regionStatusRows({
        regions: s.controlRegions, states, playerData: frame ? frame.p : null,
        locOf: (/** @type {PlayerFrame} */ d) => view.resolvePlayerLoc(d), locToRegion: s.locToRegion,
        symbols: s.playerSymbols, teams: s.teams || [], colors, sides: s.controlSides,
    });
    // It runs every frame: rebuild only when a row changed (the icons are
    // cached per look, so the same canvas means the same badge set).
    const icons = rows.map((r) => r.players.map((p) => playerIcon(view, p, colors, iconCache)));
    const sig = rows.flatMap((r, i) => [r.name, r.label, r.color, icons[i].length, ...icons[i]]);
    const last = shownRows.get(el);
    if (last && last.length === sig.length && last.every((v, i) => v === sig[i])) return;
    shownRows.set(el, sig);
    el.innerHTML = '';
    rows.forEach((r, ri) => {
        const row = document.createElement('div');
        row.className = 'region-status-row';
        const name = document.createElement('span');
        name.className = 'region-status-name';
        name.textContent = r.name;
        row.appendChild(name);
        const state = document.createElement('span');
        state.className = 'region-status-state';
        state.style.color = r.color;
        state.textContent = r.label;
        row.appendChild(state);
        const players = document.createElement('span');
        players.className = 'region-status-players';
        if (r.players.length === 0) players.textContent = '-';
        r.players.forEach((p, pi) => {
            const icon = icons[ri][pi];
            icon.title = p.name;
            players.appendChild(icon);
        });
        row.appendChild(players);
        el.appendChild(row);
    });
}

// What each status element shows now (syncRegionStatus's signature).
/** @type {WeakMap<HTMLElement, unknown[]>} */
const shownRows = new WeakMap();

// A player's symbol with their RL / LG / powerup badges around it.
/**
 * @param {MvdMap} view
 * @param {{ name: string, data: PlayerFrame, sym: PlayerSymbol | undefined, teamIdx: number }} p
 * @param {string[]} colors
 * @param {Map<string, HTMLCanvasElement>} cache
 * @returns {HTMLCanvasElement}
 */
function playerIcon(view, p, colors, cache) {
    const letter = p.sym?.symbol || p.name.charAt(0).toUpperCase();
    const color = colors[p.sym?.teamIdx ?? p.teamIdx] || colors[0];
    const badges = view.getActiveBadges(p.data);
    const key = `${p.name}|${letter}|${color}|${badges.join(',')}`;
    let canvas = cache.get(key);
    if (canvas) return canvas;
    const dpr = window.devicePixelRatio || 1;
    const size = 40;
    canvas = document.createElement('canvas');
    canvas.width = Math.round(size * dpr);
    canvas.height = Math.round(size * dpr);
    canvas.style.width = size + 'px';
    canvas.style.height = size + 'px';
    canvas.className = 'region-player-icon';
    const ctx = /** @type {CanvasRenderingContext2D} */ (canvas.getContext('2d'));
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    drawPlayerSymbolAt(ctx, letter, color, size / 2, size / 2, PLAYER_SYMBOL_BASE_SIZE);
    if (badges.length > 0) drawBadgesAroundCenter(ctx, badges, size / 2, size / 2, 14, 5);
    cache.set(key, canvas);
    return canvas;
}
