// hub.js — the hub.quakeworld.nu client: game lookup and search, and demo
// download (the hub's public CDN). Lookup and search ask mvd-api
// (src/data/api.js) first; when it cannot answer — unreachable, the key
// refused, throttled, failing, or no /api at all (a standalone `make serve`
// without an API) — they query the hub's catalog directly with its public
// anon key, as the page did before the API, so the page works on its own.
// No DOM; `fetchImpl` is injectable for tests.

import { apiJSON } from './api.js';

const SUPABASE_URL = 'https://ncsphkjfominimxztjip.supabase.co/rest/v1/v1_games';
const SUPABASE_API_KEY = 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6Im5jc3Boa2pmb21pbmlteHp0amlwIiwicm9sZSI6ImFub24iLCJpYXQiOjE2OTY5Mzg1NjMsImV4cCI6MjAxMjUxNDU2M30.NN6hjlEW-qB4Og9hWAVlgvUdwrbBO13s8OkAJuBGVbo';
const HEADERS = {
    'apikey': SUPABASE_API_KEY,
    'Authorization': `Bearer ${SUPABASE_API_KEY}`,
    'accept-profile': 'public',
};
// The columns mvd-api's search and lookup return (hubfetch.SearchSelect).
const SEARCH_SELECT = 'id,timestamp,mode,matchtag,map,teams,players,demo_sha256,demo_source_url,server_hostname';

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
    try {
        return await apiJSON(`games/${encodeURIComponent(String(gameId))}`, {}, fetchImpl);
    } catch (err) {
        if (!(/** @type {{ canFallBack?: boolean }} */ (err).canFallBack)) throw err;
    }
    const resp = await fetchImpl(`${SUPABASE_URL}?select=${encodeURIComponent(SEARCH_SELECT)}&id=eq.${encodeURIComponent(String(gameId))}`, { headers: HEADERS });
    if (!resp.ok) throw new Error(`Hub API error: ${resp.status}`);
    const games = await resp.json();
    if (games.length === 0) throw new Error(`Game ID ${gameId} not found`);
    return games[0];
}

// searchGames: one page of the newest matches. mvd-api's search filters as
// the hub does (player full-text, team contains, map / mode exact, tag
// substring, dates inclusive of the end day); its `players` and `teams`
// are comma-separated lists, AND'd, where the direct query takes each as
// one term.
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
    try {
        const body = await apiJSON(`games/search?${q}`, {}, fetchImpl);
        return { games: body.games || [], total: typeof body.total === 'number' ? body.total : null };
    } catch (err) {
        if (!(/** @type {{ canFallBack?: boolean }} */ (err).canFallBack)) throw err;
    }
    return searchHub(filters, offset, fetchImpl);
}

// searchHub: the same search against the hub's catalog directly.
/**
 * @param {SearchFilters} filters
 * @param {number} offset
 * @param {FetchImpl} fetchImpl
 * @returns {Promise<{ games: HubGame[], total: number | null }>}
 */
async function searchHub(filters, offset, fetchImpl) {
    const parts = [
        `select=${encodeURIComponent(SEARCH_SELECT)}`,
        `order=timestamp.desc`,
        `limit=${SEARCH_PAGE_SIZE}`,
    ];
    if (offset > 0) parts.push(`offset=${offset}`);
    if (filters.player) parts.push(`players_fts=fts.'${encodeURIComponent(filters.player)}'`);
    if (filters.team)   parts.push(`team_names=cs.{${encodeURIComponent(filters.team)}}`);
    if (filters.map)    parts.push(`map=eq.${encodeURIComponent(filters.map)}`);
    if (filters.mode)   parts.push(`mode=eq.${encodeURIComponent(filters.mode)}`);
    if (filters.tag)    parts.push(`matchtag=ilike.%25${encodeURIComponent(filters.tag)}%25`);
    if (filters.from)   parts.push(`timestamp=gte.${encodeURIComponent(filters.from)}`);
    if (filters.to)     parts.push(`timestamp=lte.${encodeURIComponent(filters.to + 'T23:59:59')}`);

    const resp = await fetchImpl(`${SUPABASE_URL}?${parts.join('&')}`, {
        // Ask PostgREST for the exact total (Content-Range: 0-99/1234) so
        // the UI can say "N of M" and know when every match is shown. With
        // a count preference a partial page answers 206 — still resp.ok.
        headers: { ...HEADERS, 'Prefer': 'count=exact' },
    });
    if (!resp.ok) throw new Error(`Hub API error: ${resp.status}`);
    const games = await resp.json();
    let total = null;
    const cr = resp.headers.get('content-range') || '';
    const slash = cr.lastIndexOf('/');
    if (slash >= 0 && cr.slice(slash + 1) !== '*') {
        const n = parseInt(cr.slice(slash + 1), 10);
        if (!isNaN(n)) total = n;
    }
    return { games, total };
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
