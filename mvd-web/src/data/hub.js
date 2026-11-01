// hub.js — the hub.quakeworld.nu client: game lookup and search (through
// mvd-api, src/data/api.js, which holds the hub's catalog credentials) and
// demo download (the hub's public CDN). No DOM; `fetchImpl` is injectable
// for tests.

import { apiJSON } from './api.js';

/**
 * A hub game row (the v1_games table, with the columns mvd-api's
 * /v1/games/search?roster=1 and /v1/games/{id} return). Open: the hub owns
 * its shape.
 * @typedef {Record<string, any>} HubGame
 * @typedef {typeof fetch} FetchImpl
 * @typedef {Partial<Record<'player' | 'team' | 'map' | 'mode' | 'tag' | 'from' | 'to', string>>} SearchFilters
 */

export const SEARCH_FIELDS = ['player', 'team', 'map', 'mode', 'tag', 'from', 'to'];

// Page size for the search tab. The hub itself pages at 1000 (PostgREST
// max-rows); 100 keeps a single response light (~90 KB) while "Load more"
// walks offset pages until every match is shown.
const SEARCH_PAGE_SIZE = 100;

/**
 * @param {string} input
 * @returns {number}
 */
export function parseGameId(input) {
    input = input.trim();
    const asNum = parseInt(input, 10);
    if (!isNaN(asNum) && String(asNum) === input) return asNum;
    try {
        const url = new URL(input);
        const gid = url.searchParams.get('gameId');
        if (gid) return parseInt(gid, 10);
    } catch {}
    const match = input.match(/gameId=(\d+)/);
    if (match) return parseInt(match[1], 10);
    throw new Error('Could not parse game ID from: ' + input);
}

/**
 * @param {number | string} gameId
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<HubGame>}
 */
export async function fetchGame(gameId, fetchImpl = fetch) {
    return apiJSON(`games/${encodeURIComponent(String(gameId))}`, {}, fetchImpl);
}

// searchGames: one page of the newest matches. mvd-api's search filters as
// the hub does (player full-text, team contains, map / mode exact, tag
// substring, dates inclusive of the end day); its `players` and `teams`
// are comma-separated lists, AND'd.
/**
 * @param {SearchFilters} filters
 * @param {number} [offset]
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<{ games: HubGame[], total: number | null }>}
 */
export async function searchGames(filters, offset = 0, fetchImpl = fetch) {
    const q = new URLSearchParams({ limit: String(SEARCH_PAGE_SIZE), roster: '1' });
    if (offset > 0) q.set('offset', String(offset));
    if (filters.player) q.set('players', filters.player);
    if (filters.team) q.set('teams', filters.team);
    if (filters.map) q.set('map', filters.map);
    if (filters.mode) q.set('mode', filters.mode);
    if (filters.tag) q.set('matchtag', filters.tag);
    if (filters.from) q.set('from', filters.from);
    if (filters.to) q.set('to', filters.to);
    const body = await apiJSON(`games/search?${q}`, {}, fetchImpl);
    return { games: body.games || [], total: typeof body.total === 'number' ? body.total : null };
}

// downloadDemo tries the hub CDN by sha256, then the server's own URL.
/**
 * @param {HubGame} game
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<Uint8Array<ArrayBuffer>>}
 */
export async function downloadDemo(game, fetchImpl = fetch) {
    const sha = game.demo_sha256;
    if (sha && sha.length >= 3) {
        const cdnUrl = `https://d.quake.world/${sha.slice(0, 3)}/${sha}.mvd.gz`;
        try {
            const resp = await fetchImpl(cdnUrl);
            if (resp.ok) return new Uint8Array(await resp.arrayBuffer());
        } catch {}
    }
    if (game.demo_source_url) {
        const resp = await fetchImpl(game.demo_source_url);
        if (resp.ok) return new Uint8Array(await resp.arrayBuffer());
        throw new Error('Failed to download demo from server');
    }
    throw new Error('No download URL available for this game');
}

/** @param {HubGame} game */
export function demoFilename(game) {
    const teams = (game.teams || []).map(/** @param {{ name?: string }} t */ t => (t.name || '').replace(/[^a-zA-Z0-9_-]/g, '_'));
    const teamsStr = teams.join('_vs_') || 'unknown';
    const mapName = (game.map || 'unknown').replace(/[^a-zA-Z0-9_-]/g, '_');
    const ts = new Date(game.timestamp).toISOString().replace(/[-:T]/g, '').slice(0, 13);
    return `${game.mode || 'unknown'}_${teamsStr}[${mapName}]${ts}.mvd.gz`;
}
