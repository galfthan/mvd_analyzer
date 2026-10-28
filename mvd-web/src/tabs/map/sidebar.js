// sidebar.js — the Map tab's two hand-built sidebar panels: the legend (one
// sortable table per team, rows built once per demo, cells refreshed on the
// clock) and the region status (one row per region with the players in it
// as composited symbol icons). Built by hand rather than by Svelte because
// makeSortable sorts the legend by moving its row nodes, and the region
// icons are cached canvases. The cell contents are src/lib/map.js.

import { drawPlayerSymbolAt, drawBadgesAroundCenter, PLAYER_SYMBOL_BASE_SIZE } from '../../../../mvd-map-view/src/index.js';
import { makeSortable } from '../../ui/sortable.js';
import { demoOffsetSec } from '../../lib/result.js';
import { fragsAt, hubWatchLinkHtml } from '../../lib/timeline.js';
import {
    legendTeams, legendRowHtml, LEGEND_HEAD, teamFragsAt, stackCell, weaponsText, regionStatusRows,
} from '../../lib/map.js';

// buildLegend fills `el` with a titled table per team.
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
    el.querySelectorAll('.team-status-table').forEach(makeSortable);
}

// syncLegend fills the legend at `time`: team frag totals, each player's
// loc, stack, weapons and hub watch link.
export function syncLegend(el, view, result, time) {
    const s = view.state;
    const frame = view.frameAt(time);
    const pd = frame ? frame.p : null;
    const frags = fragsAt(result, time);
    s.teams.forEach((team, ti) => {
        const t = document.getElementById(`map-legend-team-title-${ti}`);
        if (t) t.textContent = `${team} — ${teamFragsAt(team, s.playerSymbols, frags)} frags`;
    });
    for (const cell of el.querySelectorAll('.map-legend-loc')) {
        const d = pd?.[cell.dataset.player];
        cell.textContent = d && !(d.x === 0 && d.y === 0) ? (view.resolvePlayerLoc(d) || '') : '';
    }
    for (const cell of el.querySelectorAll('.map-legend-stack')) {
        const c = stackCell(pd?.[cell.dataset.player]);
        cell.dataset.sortValue = c.sort;
        if (c.html === null) cell.textContent = '-';
        else cell.innerHTML = c.html;
    }
    for (const cell of el.querySelectorAll('.map-legend-wpn')) {
        cell.textContent = weaponsText(pd?.[cell.dataset.player]);
    }
    const hubInfo = result?.hubInfo;
    const ids = result?.timelineAnalysis?.playerUserIDs || {};
    const offset = demoOffsetSec(result);
    for (const cell of el.querySelectorAll('.map-legend-hub')) {
        cell.innerHTML = hubWatchLinkHtml(cell.dataset.player, time, hubInfo, ids, offset);
    }
}

// syncRegionStatus rebuilds the region rows at `time`. `icons` caches the
// composited player icons per (name, letter, colour, badges) for the demo:
// a player is in one region per frame, so a cached node is appended once.
export function syncRegionStatus(el, view, time, colors, icons) {
    const s = view.state;
    if (!el || !s.controlRegions || s.controlRegions.length === 0) return;
    el.innerHTML = '';
    const states = view.regionControlAt(time);
    if (!states) return;
    const frame = view.frameAt(time);
    const rows = regionStatusRows({
        regions: s.controlRegions, states, playerData: frame ? frame.p : null,
        locOf: (d) => view.resolvePlayerLoc(d), locToRegion: s.locToRegion,
        symbols: s.playerSymbols, teams: s.teams || [], colors, sides: s.controlSides,
    });
    for (const r of rows) {
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
        for (const p of r.players) {
            const icon = playerIcon(view, p, colors, icons);
            icon.title = p.name;
            players.appendChild(icon);
        }
        row.appendChild(players);
        el.appendChild(row);
    }
}

// A player's symbol with their RL / LG / powerup badges around it.
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
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    drawPlayerSymbolAt(ctx, letter, color, size / 2, size / 2, PLAYER_SYMBOL_BASE_SIZE);
    if (badges.length > 0) drawBadgesAroundCenter(ctx, badges, size / 2, size / 2, 14, 5);
    cache.set(key, canvas);
    return canvas;
}
