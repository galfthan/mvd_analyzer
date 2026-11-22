// matches.js — a MATCH: the maps two sides played against each other in one
// sitting, grouped from hub game rows (src/data/hub.js). The hub lists single
// games and knows no such thing, so the page derives it: consecutive games of
// one mode between the same two sides — the same team names with enough of
// the same players, or the same two players in a duel — with a short break
// between them. DOM-free; the Search tab (the grouped list) and the map tabs
// above a loaded demo (src/shell/MatchTabs.svelte) both read it.

/**
 * @typedef {import('../data/hub.js').HubGame} HubGame
 * @typedef {{ key: string, name: string, players: Set<string>, frags: number }} Side
 * @typedef {{ name: string, wins: number }} MatchSide
 * @typedef {object} Match
 * @property {number | string} id       its first game's id
 * @property {string} mode
 * @property {HubGame[]} games          in the order played
 * @property {[MatchSide, MatchSide] | null} sides  the leader first, each with the maps it won (a drawn map is neither's); null for a game that has no two sides
 */

// A hub row's timestamp is the demo's START and the row has no duration, so
// the break between two maps is measured from the mode's usual timelimit:
// two starts at most NOMINAL + MAX_BREAK minutes apart follow each other.
// (Measured on 9000 hub games, 2026-11: same-roster 4on4 maps start 20–35 min
// apart, 2on2 and 1on1 10–25.) An overtime eats into the break.
/** @type {Record<string, number>} */
const NOMINAL_MINUTES = { '1on1': 10, '2on2': 10, '4on4': 20, 'ctf': 20 };
const DEFAULT_NOMINAL_MINUTES = 20;
const MAX_BREAK_MINUTES = 15;
// How many of a team's players must be the same on the next map.
const MIN_SHARED_PLAYERS = 3;

/** @param {string | undefined} s */
const fold = (s) => (s || '').trim().toLowerCase();

/** @param {HubGame} game */
const startMs = (game) => Date.parse(game.timestamp);

// gameSides: the two sides of a game, null when it does not have exactly two
// (an FFA, a three-team game, a row without a roster). A duel's sides are
// its players; a team mode's are the teams its players name.
/**
 * @param {HubGame} game
 * @returns {[Side, Side] | null}
 */
export function gameSides(game) {
    const players = (Array.isArray(game.players) ? game.players : []).filter((/** @type {any} */ p) => p && !p.is_bot);
    /** @type {Map<string, Side>} */
    const sides = new Map();
    const duel = game.mode === '1on1';
    for (const p of players) {
        const name = duel ? p.name : p.team;
        const key = fold(name);
        if (!key) return null;
        let side = sides.get(key);
        if (!side) sides.set(key, side = { key, name, players: new Set(), frags: 0 });
        side.players.add(fold(p.name));
        side.frags += p.frags || 0;
    }
    if (sides.size !== 2) return null;
    // The hub's own team totals where it has them (a player who left
    // mid-game is in the team's frags and not in the roster).
    for (const t of Array.isArray(game.teams) ? game.teams : []) {
        const side = sides.get(fold(t.name));
        if (side && typeof t.frags === 'number') side.frags = t.frags;
    }
    const [a, b] = [...sides.values()].sort((x, y) => (x.key < y.key ? -1 : 1));
    return [a, b];
}

// sameContest: the same two sides — by name, and each with enough of the
// same players (all of them where a side is smaller than MIN_SHARED_PLAYERS).
/**
 * @param {[Side, Side]} a
 * @param {[Side, Side]} b
 */
function sameContest(a, b) {
    for (let i = 0; i < 2; i++) {
        if (a[i].key !== b[i].key) return false;
        const need = Math.min(MIN_SHARED_PLAYERS, a[i].players.size, b[i].players.size);
        let shared = 0;
        for (const p of a[i].players) if (b[i].players.has(p)) shared++;
        if (shared < need) return false;
    }
    return true;
}

/** @param {string} mode */
function maxStartGapMs(mode) {
    return ((NOMINAL_MINUTES[mode] ?? DEFAULT_NOMINAL_MINUTES) + MAX_BREAK_MINUTES) * 60000;
}

// groupMatches: `games` (any order, each id once) as matches, the newest
// match first. A game without two sides is a match of its own.
/**
 * @param {HubGame[]} games
 * @returns {Match[]}
 */
export function groupMatches(games) {
    const sorted = [...games].sort((a, b) => startMs(a) - startMs(b));
    /** @type {Array<{ games: HubGame[], sides: [Side, Side] | null }>} */
    const chains = [];
    // The chains each (mode, side names) pair has played: two pickups named
    // red v blue run at once on two servers, each its own match.
    /** @type {Map<string, Array<{ games: HubGame[], sides: [Side, Side] }>>} */
    const open = new Map();
    for (const game of sorted) {
        const sides = gameSides(game);
        if (!sides) {
            chains.push({ games: [game], sides: null });
            continue;
        }
        const key = `${game.mode}\n${sides[0].key}\n${sides[1].key}`;
        let named = open.get(key);
        if (!named) open.set(key, named = []);
        // The latest chain this game follows: soon enough, the same rosters.
        const chain = [...named].reverse().find(c =>
            startMs(game) - startMs(c.games[c.games.length - 1]) <= maxStartGapMs(game.mode) && sameContest(c.sides, sides));
        if (chain) {
            chain.games.push(game);
            // The roster the next map is compared with is the last map's.
            chain.sides = sides;
            continue;
        }
        const fresh = { games: [game], sides };
        chains.push(fresh);
        named.push(fresh);
    }
    return chains.map(matchOfChain).sort((a, b) => startMs(b.games[0]) - startMs(a.games[0]));
}

/**
 * @param {{ games: HubGame[], sides: [Side, Side] | null }} chain
 * @returns {Match}
 */
function matchOfChain({ games, sides }) {
    const first = games[0];
    if (!sides) return { id: first.id, mode: first.mode, games, sides: null };
    /** @type {[MatchSide, MatchSide]} */
    const score = [{ name: sides[0].name, wins: 0 }, { name: sides[1].name, wins: 0 }];
    for (const game of games) {
        const w = mapWinner(game);
        if (w !== null) score[fold(w) === sides[0].key ? 0 : 1].wins++;
    }
    // The leader first; a level score keeps the name order.
    if (score[1].wins > score[0].wins) score.reverse();
    return { id: first.id, mode: first.mode, games, sides: score };
}

// mapWinner: the name of the side that won a game, null for a draw or a
// game without two sides.
/** @param {HubGame} game */
export function mapWinner(game) {
    const sides = gameSides(game);
    if (!sides || sides[0].frags === sides[1].frags) return null;
    return sides[0].frags > sides[1].frags ? sides[0].name : sides[1].name;
}

// mapScore: a game's frags for the named side and for the other one.
/**
 * @param {HubGame} game
 * @param {string} sideName
 * @returns {{ own: number, other: number } | null}
 */
export function mapScore(game, sideName) {
    const sides = gameSides(game);
    if (!sides) return null;
    const i = sides[0].key === fold(sideName) ? 0 : 1;
    return { own: sides[i].frags, other: sides[1 - i].frags };
}

// matchWith: the match in `matches` that holds the game, or undefined.
/**
 * @param {Match[]} matches
 * @param {number | string} gameId
 */
export function matchWith(matches, gameId) {
    return matches.find(m => m.games.some(g => String(g.id) === String(gameId)));
}

// siblingFilters: the search (src/data/hub.js searchGames) that finds the
// other maps of a game's match — its mode, both sides by name, and the days
// around it. null when the game has no two sides, or a side's name cannot
// go in the search's comma-separated list.
/**
 * @param {HubGame} game
 * @returns {import('../data/hub.js').SearchFilters | null}
 */
export function siblingFilters(game) {
    const sides = gameSides(game);
    const t = startMs(game);
    if (!sides || isNaN(t)) return null;
    const names = [sides[0].name, sides[1].name];
    if (names.some(n => n.includes(','))) return null;
    const day = (/** @type {number} */ ms) => new Date(ms).toISOString().slice(0, 10);
    const span = 6 * 3600000;
    return {
        mode: game.mode,
        [game.mode === '1on1' ? 'player' : 'team']: names.join(','),
        from: day(t - span),
        to: day(t + span),
    };
}

// mergeGames: two lists of hub rows as one, each id once (the first wins).
/**
 * @param {HubGame[]} a
 * @param {HubGame[]} b
 */
export function mergeGames(a, b) {
    const seen = new Set(a.map(g => String(g.id)));
    return [...a, ...b.filter(g => !seen.has(String(g.id)) && seen.add(String(g.id)))];
}

// matchRow: a match's tag and day for the search list.
/** @param {Match} match */
export function matchRow(match) {
    const last = match.games[match.games.length - 1];
    return {
        tag: match.games.find(g => g.matchtag)?.matchtag || '',
        date: (last.timestamp || '').slice(0, 10),
    };
}

// searchMatches: the Search tab's grouped list — the matches of the found
// games (`rows`), each with the maps of it the search did not return
// (`extra`, fetched since: a map filter or a page's end cuts a match short).
// A match none of whose games the search found is not listed.
/**
 * @param {HubGame[]} rows
 * @param {HubGame[]} extra
 * @returns {Match[]}
 */
export function searchMatches(rows, extra) {
    const found = new Set(rows.map(g => String(g.id)));
    return groupMatches(mergeGames(rows, extra)).filter(m => m.games.some(g => found.has(String(g.id))));
}

// cutShort: the listed matches the search may have returned only part of,
// each to be asked for whole (fetchMatch). A map filter drops the other
// maps of every match; a date bound those of a match that starts or ends
// within a map's reach of it; and the list's end, when it has more pages,
// may cut the oldest one. A game without two sides has no other maps.
/**
 * @param {Match[]} matches  newest first (searchMatches)
 * @param {{ map?: string, from?: string, to?: string }} filters  the dates as UTC days, `to` inclusive
 * @param {boolean} more  the search has rows not yet shown
 * @returns {Match[]}
 */
export function cutShort(matches, filters, more) {
    const from = filters.from ? Date.parse(filters.from) : NaN;
    const to = filters.to ? Date.parse(filters.to) + 86400000 : NaN;
    const last = matches[matches.length - 1];
    return matches.filter(m => {
        if (!m.sides) return false;
        if (filters.map || (more && m === last)) return true;
        const reach = maxStartGapMs(m.mode);
        return startMs(m.games[0]) - from <= reach || to - startMs(m.games[m.games.length - 1]) <= reach;
    });
}
