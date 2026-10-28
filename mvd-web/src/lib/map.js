// map.js — the Map tab's DOM-free parts (src/tabs/map/): what the map view
// (mvd-map-view's MvdMap) is fed from a Result — the team order it draws, the
// player symbols, the world bounds, the backpack drops, the position
// streams — and what the sidebar shows next to it: the legend cells, the
// region status rows, the items list, the learn-mode entity table, the trail
// and follow controls, the canvas size and the perf / benchmark read-outs.
// Times are seconds (the page clock), as the map view's are.

import { playerStatsRows } from './player-stats.js';
import { escapeHtml } from './html.js';

// ─── What the map draws ─────────────────────────────────────────────────────

// mapTeams: the canonical frag-sorted order (the teams store) when it names
// two sides or more, else the demo's own team list.
export function mapTeams(order, result) {
    if (order && order.length >= 2) return [...order];
    if (result?.demoInfo?.teams) return result.demoInfo.teams;
    if (result?.match?.teams) return result.match.teams.map(t => t.name);
    return [];
}

// assignPlayerSymbols: { name: { symbol, team, teamIdx } } — each player's
// first letter not yet taken (A–Z, in team then roster order), teamIdx their
// side's index in `teams` (the colour index). The roster is the KTX demoinfo
// block where the demo has one, else playerStats — present on every demo, so
// a pre-KTX demo with position streams still has people on its map.
export function assignPlayerSymbols(result, teams) {
    const players = result?.demoInfo?.players?.length
        ? result.demoInfo.players
        : playerStatsRows(result);
    const teamPlayers = {};
    for (const team of teams) teamPlayers[team] = [];
    for (const player of players) {
        if (player.team && teamPlayers[player.team]) teamPlayers[player.team].push(player.name);
    }
    const all = [];
    teams.forEach((team, teamIdx) => {
        for (const name of (teamPlayers[team] || [])) all.push({ name, team, teamIdx });
    });
    const used = new Set();
    const symbols = {};
    for (const p of all) {
        let letter = '?';
        for (const ch of p.name) {
            const upper = ch.toUpperCase();
            if (upper >= 'A' && upper <= 'Z' && !used.has(upper)) {
                letter = upper;
                used.add(upper);
                break;
            }
        }
        if (letter === '?') letter = p.name[0]?.toUpperCase() || '?';
        symbols[p.name] = { symbol: letter, team: p.team, teamIdx: p.teamIdx };
    }
    return symbols;
}

// mapBounds: the world XY box of the loc points and every living player
// position in the bucket view ((0, 0) slots are padding, skipped), padded by
// 10 % — or ±1000 when there is nothing.
export function mapBounds(locations, view) {
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (const loc of locations || []) {
        minX = Math.min(minX, loc.x);
        maxX = Math.max(maxX, loc.x);
        minY = Math.min(minY, loc.y);
        maxY = Math.max(maxY, loc.y);
    }
    if (view && view.players) {
        for (const name in view.players) {
            const cp = view.players[name];
            const xs = cp.x, ys = cp.y;
            if (!xs || !ys) continue;
            for (let rel = 0; rel < cp.n; rel++) {
                if (!cp.alive[rel]) continue;
                const x = xs[rel], y = ys[rel];
                if (x !== 0 || y !== 0) {
                    if (x < minX) minX = x;
                    if (x > maxX) maxX = x;
                    if (y < minY) minY = y;
                    if (y > maxY) maxY = y;
                }
            }
        }
    }
    if (minX === Infinity) {
        minX = -1000; maxX = 1000;
        minY = -1000; maxY = 1000;
    }
    const padX = (maxX - minX) * 0.1;
    const padY = (maxY - minY) * 0.1;
    return { minX: minX - padX, maxX: maxX + padX, minY: minY - padY, maxY: maxY + padY };
}

// dropEventsOf: the backpack drops in the map's death-marker shape, time in
// seconds (Result times are ms).
export function dropEventsOf(result) {
    return (result?.backpacks || []).map(d => ({
        t: d.time * 0.001,
        wx: d.origin?.[0] || 0,
        wy: d.origin?.[1] || 0,
        wz: d.origin?.[2] || 0,
        weapon: d.weapon,
    }));
}

// posStreamsOf: name → native-rate position track, for the players that have
// samples.
export function posStreamsOf(result) {
    const out = {};
    const players = result?.streams?.players;
    if (Array.isArray(players)) {
        for (const p of players) {
            if (p && p.pos && Array.isArray(p.pos.t) && p.pos.t.length > 0) out[p.name] = p.pos;
        }
    }
    return out;
}

// ─── Canvas size ────────────────────────────────────────────────────────────

// The canvas width outside fullscreen.
export const MAP_CANVAS_BASE_WIDTH = 850;

// mapCanvasSize: fullscreen fills the panel's canvas container (`rect`);
// otherwise a fixed width with the height from the world's aspect, capped.
export function mapCanvasSize(fullscreen, rect, bounds) {
    if (fullscreen && rect) {
        return [Math.max(300, Math.floor(rect.width)), Math.max(200, Math.floor(rect.height))];
    }
    const worldW = bounds ? bounds.maxX - bounds.minX : 0;
    const worldH = bounds ? bounds.maxY - bounds.minY : 0;
    const w = MAP_CANVAS_BASE_WIDTH;
    return [w, worldW > 0 ? Math.round(Math.max(400, Math.min(850, w * (worldH / worldW)))) : 700];
}

// ─── Legend ─────────────────────────────────────────────────────────────────

// legendTeams: one block per team in colour order, its players in name
// order (the column headers sort the rest).
export function legendTeams(teams, symbols, colors) {
    const byName = Object.entries(symbols).sort((a, b) => a[0].localeCompare(b[0]));
    return teams.map((team, ti) => ({
        team,
        ti,
        color: colors[ti] || colors[0],
        players: byName.filter(([, info]) => info.team === team).map(([name, info]) => ({ name, symbol: info.symbol })),
    }));
}

// legendRowHtml: one legend row's cells, before the clock fills them.
export function legendRowHtml(name, symbol, color) {
    const n = escapeHtml(name);
    return `
                    <td><span class="map-legend-symbol" style="color: ${color}">${symbol}</span></td>
                    <td>${n}</td>
                    <td class="map-legend-loc" data-player="${n}">-</td>
                    <td class="map-legend-stack" data-player="${n}" data-sort-value="0">-</td>
                    <td class="map-legend-wpn" data-player="${n}">-</td>
                    <td class="map-legend-hub" data-player="${n}"></td>
                `;
}

export const LEGEND_HEAD = '<thead><tr><th></th><th>Player</th><th>Loc</th><th title="Health + armor (sorted by H+A)">Stack</th><th>Wpn</th><th>View</th></tr></thead>';

// teamFragsAt: a team's frags from the running per-player counts.
export function teamFragsAt(team, symbols, fragCounts) {
    let n = 0;
    for (const [name, info] of Object.entries(symbols)) {
        if (info.team === team) n += fragCounts[name] || 0;
    }
    return n;
}

// stackCell: the Stack column — health, then armour with its type; the sort
// value is health + armour. `html` null means the text is '-'.
export function stackCell(data) {
    if (!data) return { sort: '0', html: null };
    const h = data.h ?? data.health ?? 0;
    const a = data.a ?? data.armor ?? 0;
    const at = data.at ?? data.armorType ?? '';
    const sort = String(h + a);
    if (h <= 0) return { sort, html: null };
    if (a > 0 && at) return { sort, html: `<span class="stack-h">${h}</span> <span class="armor-${at}">${a} ${at.toUpperCase()}</span>` };
    if (a > 0) return { sort, html: `<span class="stack-h">${h}</span> <span>${a}</span>` };
    return { sort, html: `<span class="stack-h">${h}</span>` };
}

// weaponsText: the Wpn column.
export function weaponsText(data) {
    if (!data) return '-';
    const w = [];
    if (data.rl ?? data.hasRL) w.push('RL');
    if (data.lg ?? data.hasLG) w.push('LG');
    return w.length > 0 ? w.join(' ') : '-';
}

// ─── Region status ──────────────────────────────────────────────────────────

// regionStateLabel: a control state's label and colour (team A / B are the
// first two of the canonical order).
export function regionStateLabel(state, teams, colors) {
    switch (state) {
        case 'teamAControl': return [teams[0] || 'A', colors[0]];
        case 'teamAWeakControl': return [(teams[0] || 'A') + ' (weak)', colors[0]];
        case 'teamBControl': return [teams[1] || 'B', colors[1]];
        case 'teamBWeakControl': return [(teams[1] || 'B') + ' (weak)', colors[1]];
        case 'contested': return ['Contested', '#ffffff'];
        case 'weakContested': return ['Contested (weak)', '#bbbbbb'];
        default: return ['Empty', '#555'];
    }
}

// regionStatusRows: per region its state and the living players in it
// (team A first). `locOf(data)` resolves a player's loc name.
export function regionStatusRows({ regions, states, playerData, locOf, locToRegion, symbols, teams, colors }) {
    const inRegion = {};
    for (const r of regions) inRegion[r.name] = [];
    if (playerData) {
        for (const [name, data] of Object.entries(playerData)) {
            if (data.d || (data.h !== undefined && data.h <= 0)) continue;
            if (data.x === 0 && data.y === 0) continue;
            const loc = locOf(data);
            if (!loc) continue;
            const region = locToRegion?.[loc];
            if (!region || !inRegion[region]) continue;
            const sym = symbols[name];
            inRegion[region].push({ name, data, sym, teamIdx: sym ? sym.teamIdx : -1 });
        }
    }
    return regions.map((r) => {
        const [label, color] = regionStateLabel(states[r.name] || 'empty', teams, colors);
        const players = inRegion[r.name] || [];
        players.sort((a, b) => a.teamIdx - b.teamIdx);
        return { name: r.name, label, color, players };
    });
}

// ─── Items list ─────────────────────────────────────────────────────────────

// The contested items the sidebar lists (armours, MH, RL / LG, powerups), in
// display order; the map draws the rest too.
const ITEM_KIND_ORDER = { ra: 0, ya: 1, ga: 2, mh: 3, rl: 4, lg: 5, quad: 6, pent: 7, ring: 8 };

export function panelItems(items, styles) {
    return (items || [])
        .filter(it => it.kind in ITEM_KIND_ORDER && styles[it.kind])
        .sort((a, b) => (ITEM_KIND_ORDER[a.kind] - ITEM_KIND_ORDER[b.kind]) || a.name.localeCompare(b.name));
}

// itemStatusCell: 'up', 'held' (taken, respawn not started) or the seconds
// to respawn.
export function itemStatusCell(s) {
    if (s.up) return { text: 'up', cls: 'item-status up' };
    if (s.pending) return { text: 'held', cls: 'item-status pending' };
    return { text: s.secsToRespawn.toFixed(1) + 's', cls: 'item-status respawn' };
}

// ─── Learn mode ─────────────────────────────────────────────────────────────

const ENTITY_CLASS_LABEL = {
    weapon: 'Weapon', armor: 'Armor', health: 'Health', ammo: 'Ammo',
    powerup: 'Powerup', teleporter: 'Teleporter', spawn: 'Spawn',
    button: 'Button', door: 'Door',
};

// entityRows: the learn-mode table — every entity of a shown category, the
// teleporters as one row per entrance (Loc) → exit (Destination), exits with
// no entrance on their own. `categoryOf(e)` is the map view's.
export function entityRows(ents, filters, categoryOf) {
    const rows = [];
    for (const e of ents) {
        if (e.type === 'teleportSrc' || e.type === 'teleportDst') continue;
        const cat = categoryOf(e);
        if (!filters[cat]) continue;
        rows.push({
            cls: ENTITY_CLASS_LABEL[cat] || cat,
            type: e.type === 'item' ? (e.kind || '') : '',
            name: e.name || '', loc: e.loc || '', source: '',
        });
    }
    if (filters.teleporter) {
        const dstByName = {};
        for (const e of ents) {
            if (e.type === 'teleportDst' && e.targetName) dstByName[e.targetName] = e;
        }
        const paired = new Set();
        for (const e of ents) {
            if (e.type !== 'teleportSrc') continue;
            const dst = e.target ? dstByName[e.target] : null;
            if (dst) paired.add(dst);
            rows.push({ cls: 'Teleporter', type: '', name: e.name || '', loc: e.loc || '', source: dst ? (dst.loc || '') : '' });
        }
        for (const e of ents) {
            if (e.type === 'teleportDst' && !paired.has(e)) {
                rows.push({ cls: 'Teleporter', type: '', name: e.name || '', loc: '', source: e.loc || '' });
            }
        }
    }
    rows.sort((a, b) => a.cls.localeCompare(b.cls) || a.type.localeCompare(b.type) || a.name.localeCompare(b.name));
    return rows;
}

export function entityRowsHtml(rows) {
    return rows.map(r =>
        `<tr><td>${escapeHtml(r.cls)}</td><td>${escapeHtml(r.type)}</td>` +
        `<td>${escapeHtml(r.name)}</td><td>${escapeHtml(r.loc)}</td>` +
        `<td>${escapeHtml(r.source)}</td></tr>`
    ).join('');
}

// ─── Trails and follow ──────────────────────────────────────────────────────

// trailButtons: All lit when every trail is on, None when none is, the
// Players dropdown when a subset is.
export function trailButtons(names, enabled) {
    let on = 0;
    for (const n of names) if (enabled[n]) on++;
    const total = names.length;
    return { all: total > 0 && on === total, none: on === 0, some: on > 0 && on < total };
}

export const byName = (a, b) => a.localeCompare(b);

// ─── Perf HUD and benchmark ─────────────────────────────────────────────────

// perfHudText: the last second of frame samples (MvdMap.lastFrameStats).
export function perfHudText(samples, now) {
    const recent = samples.filter(s => now - s.t < 1000);
    if (recent.length === 0) return 'map idle — play or drag to sample';
    const avg = (field) => {
        let sum = 0, n = 0;
        for (const s of recent) {
            if (typeof s[field] === 'number') { sum += s[field]; n++; }
        }
        return n > 0 ? sum / n : null;
    };
    const ms1 = (v) => v === null ? '—' : v.toFixed(1);
    const last = recent[recent.length - 1];
    return `${recent.length} fps\n` +
        `build ${ms1(avg('buildMs'))}  submit ${ms1(avg('submitMs'))}` +
        `  blit ${ms1(avg('blitMs'))}  gpu ${ms1(avg('gpuMs'))} ms\n` +
        `draws ${last.draws}  tris ${last.worldTris}  pts ${last.points}` +
        `  segs ${last.lineSegs}  spr ${last.sprites}\n` +
        `stream ${(last.streamBytes / 1024).toFixed(0)} KiB/frame`;
}

// benchDemoKey: "the same demo" across sessions — the Result has no file
// name, so map + teams + rounded duration.
export function benchDemoKey(result, teams, duration) {
    const map = result?.demoInfo?.map || result?.match?.map || '?';
    return `${map}|${(teams || []).join('+')}|${Math.round(duration || 0)}s`;
}

// benchText: a run's read-out, against the newest earlier run at the same
// surface size when there is one.
export function benchText(record, summary, prev) {
    const f = summary.frameMs || {};
    let text =
        `bench ${record.demo}\n` +
        `${summary.avgFps} fps avg  ${summary.frames} frames  ` +
        `${summary.longFrames} hitches\n` +
        `frame ms p50 ${f.p50} p95 ${f.p95} max ${f.max}\n` +
        `build p50 ${summary.buildMs?.p50}  submit p50 ${summary.submitMs?.p50}` +
        (summary.gpuMs ? `  gpu p50 ${summary.gpuMs.p50}` : '') + '\n' +
        `draws avg ${summary.draws.avg}`;
    if (prev) {
        const d = (record.avgFps - prev.avgFps).toFixed(1);
        text += `\nvs ${prev.date.slice(0, 10)}: ${prev.avgFps} fps (${d >= 0 ? '+' : ''}${d})`;
    }
    return text + '\nfull record in the console';
}
