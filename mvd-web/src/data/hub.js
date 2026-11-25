// hub.js — the hub.quakeworld.nu client: game lookup and search, and demo
// download (the hub's public CDN). Lookup and search ask mvd-api
// (src/data/api.js) first; when it cannot answer — unreachable, the key
// refused, throttled, failing, or no /api at all (a standalone `make serve`
// without an API) — they query the hub's catalog directly with its public
// anon key, as the page did before the API, so the page works on its own.
// No DOM; `fetchImpl` is injectable for tests.

import { apiJSON } from './api.js';
import { siblingFilters, groupMatches, matchWith, mergeGames } from '../lib/matches.js';
import { QWL_SERVER } from '../lib/qwl.js';

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
 * @typedef {Partial<Record<'player' | 'team' | 'map' | 'mode' | 'tag' | 'from' | 'to' | 'qwl', string>>} SearchFilters  `qwl`: '1' = only games played on QWLeague's servers
 */

export const SEARCH_FIELDS = ['player', 'team', 'map', 'mode', 'tag', 'from', 'to', 'qwl'];

// Page size for the search tab. The hub itself pages at 1000 (PostgREST
// max-rows); 100 keeps a single response light (~90 KB) while "Load more"
// walks offset pages until every game is shown.
const SEARCH_PAGE_SIZE = 100;
// Both sides' games of one mode over two or three days: the hub's page
// ceiling, so a pair of generic names ("red" v "blue") is not cut short.
const MATCH_SEARCH_LIMIT = 1000;

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

// searchGames: one page of the newest games. mvd-api's search filters as
// the hub does (player full-text, team contains, map / mode exact, tag
// substring, dates inclusive of the end day); its `players` and `teams`
// are comma-separated lists, AND'd, where the direct query takes each as
// one term. A team goes in lower case: the hub's team filter is an exact
// match on its lower-cased team names ("D2s" finds nothing, "d2s" the
// team), where the player one folds case. A search for QWLeague's games
// alone (`qwl`) filters on the server's hostname, which mvd-api's search
// does not offer: it asks the hub directly.
/**
 * @param {SearchFilters} filters
 * @param {number} [offset]
 * @param {FetchImpl} [fetchImpl]
 * @param {number} [limit]
 * @returns {Promise<{ games: HubGame[], total: number | null }>}
 */
export async function searchGames(filters, offset = 0, fetchImpl = fetch, limit = SEARCH_PAGE_SIZE) {
    const q = new URLSearchParams({ limit: String(limit), roster: '1' });
    if (offset > 0) q.set('offset', String(offset));
    if (filters.player) q.set('players', filters.player);
    if (filters.team) q.set('teams', filters.team.toLowerCase());
    if (filters.map) q.set('map', filters.map);
    if (filters.mode) q.set('mode', filters.mode);
    if (filters.tag) q.set('matchtag', filters.tag);
    if (filters.from) q.set('from', filters.from);
    if (filters.to) q.set('to', filters.to);
    if (filters.qwl) return searchHub(filters, offset, fetchImpl, limit);
    try {
        const body = await apiJSON(`games/search?${q}`, {}, fetchImpl);
        return { games: body.games || [], total: typeof body.total === 'number' ? body.total : null };
    } catch (err) {
        if (!(/** @type {{ canFallBack?: boolean }} */ (err).canFallBack)) throw err;
    }
    return searchHub(filters, offset, fetchImpl, limit);
}

// searchHub: the same search against the hub's catalog directly.
/**
 * @param {SearchFilters} filters
 * @param {number} offset
 * @param {FetchImpl} fetchImpl
 * @param {number} limit
 * @returns {Promise<{ games: HubGame[], total: number | null }>}
 */
async function searchHub(filters, offset, fetchImpl, limit) {
    const parts = [
        `select=${encodeURIComponent(SEARCH_SELECT)}`,
        `order=timestamp.desc`,
        `limit=${limit}`,
    ];
    if (offset > 0) parts.push(`offset=${offset}`);
    if (filters.player) parts.push(`players_fts=fts.'${encodeURIComponent(filters.player)}'`);
    if (filters.team)   parts.push(`team_names=cs.{${encodeURIComponent(filters.team.toLowerCase())}}`);
    if (filters.map)    parts.push(`map=eq.${encodeURIComponent(filters.map)}`);
    if (filters.mode)   parts.push(`mode=eq.${encodeURIComponent(filters.mode)}`);
    if (filters.tag)    parts.push(`matchtag=ilike.%25${encodeURIComponent(filters.tag)}%25`);
    if (filters.from)   parts.push(`timestamp=gte.${encodeURIComponent(filters.from)}`);
    if (filters.to)     parts.push(`timestamp=lte.${encodeURIComponent(filters.to + 'T23:59:59')}`);
    if (filters.qwl)    parts.push(`server_hostname=ilike.%25${QWL_SERVER}%25`);

    const resp = await fetchImpl(`${SUPABASE_URL}?${parts.join('&')}`, {
        // Ask PostgREST for the exact total (Content-Range: 0-99/1234) so
        // the UI can say "N of M" and know when every game is shown. With
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

// fetchMatch: the match (src/lib/matches.js) a game belongs to, with every
// map of it the hub has — one search for the games of both sides around the
// game's day. A game on its own, or one the search cannot find again, is a
// match of one.
/**
 * @param {HubGame} game
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<import('../lib/matches.js').Match>}
 */
export async function fetchMatch(game, fetchImpl = fetch) {
    const filters = siblingFilters(game);
    /** @type {HubGame[]} */
    let around = [];
    if (filters) ({ games: around } = await searchGames(filters, 0, fetchImpl, MATCH_SEARCH_LIMIT));
    // The game itself first: the row in hand wins over the search's copy.
    const match = matchWith(groupMatches(mergeGames([game], around)), game.id);
    return /** @type {import('../lib/matches.js').Match} */ (match);
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
