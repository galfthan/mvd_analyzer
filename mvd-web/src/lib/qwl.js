// qwl.js — QWLeague (qwleague.com) on the hub's games. A game played on one
// of QWLeague's servers is known by its hub row alone (the hostname); what
// series it is a map of, the format and — for a tournament — the event and
// the clans come from mvd-api's GET /v1/qwleague/series (src/data/hub.js
// fetchQwlSeries), which answers by the demo's sha256. DOM-free.

/**
 * @typedef {import('../data/hub.js').HubGame} HubGame
 * @typedef {object} QwlMap
 * @property {string} map
 * @property {number} matchId
 * @property {string} [sha]      the map's demo on the hub; absent for a map not played or not on the hub
 * @property {[number, number]} [frags]  team 1's and team 2's
 * @property {number} [winnerTeam]
 * @typedef {object} QwlSeries   one series, as mvd-api returns it (its openapi QWLeagueSeries)
 * @property {number} id
 * @property {string} url
 * @property {string} mode
 * @property {'ladder' | 'tournament' | 'prac' | 'quickplay' | 'custom'} kind
 * @property {number} [bestOf]
 * @property {string} [format]
 * @property {[number, number]} [score]  maps won by team 1 and team 2
 * @property {number} [winnerTeam]
 * @property {boolean} [forfeit]
 * @property {string} endedAt
 * @property {QwlMap[]} maps
 * @property {string[]} [unplayed]
 * @property {{ name: string, slug?: string, division?: string, stage?: string, round?: number }} [tournament]
 * @property {Array<{ name: string, tag?: string }>} [teams]
 */

// The filter value the hub search takes for "played on QWLeague's servers"
// (a substring of a row's server_hostname).
export const QWL_SERVER = 'qwleague.com';

// isQwlGame: a hub row of a game played on a QWLeague server.
/** @param {HubGame} game */
export function isQwlGame(game) {
    const host = String(game?.server_hostname || '').toLowerCase().split(':')[0];
    return host === QWL_SERVER || host.endsWith('.' + QWL_SERVER);
}

// seriesBySha: each demo sha of the series' maps → its series.
/** @param {QwlSeries[]} series */
export function seriesBySha(series) {
    /** @type {Map<string, QwlSeries>} */
    const out = new Map();
    for (const s of series) for (const m of s.maps) if (m.sha) out.set(m.sha, s);
    return out;
}

// mergeSeries: two lists of series as one, each (by its first map) once;
// the later copy wins, a series read again having gained a map.
/**
 * @param {QwlSeries[]} a
 * @param {QwlSeries[]} b
 */
export function mergeSeries(a, b) {
    /** @type {Map<number, QwlSeries>} */
    const out = new Map();
    for (const s of [...a, ...b]) out.set(s.maps[0]?.matchId ?? s.id, s);
    return [...out.values()];
}

/** @type {Record<string, string>} */
const KIND_LABELS = { ladder: 'ladder', prac: 'prac', quickplay: 'Quick Play', custom: 'custom' };

// seriesLabel: what a series was, in a few words after the "QWL" badge:
// "ladder · Bo3", "4on4 · Season 1 · Division 2 · group, round 1 · Bo3".
/** @param {QwlSeries} s */
export function seriesLabel(s) {
    const parts = [];
    if (s.kind === 'tournament' && s.tournament) {
        const t = s.tournament;
        parts.push(t.name);
        if (t.division) parts.push(t.division);
        if (t.stage) parts.push(t.round ? `${t.stage}, round ${t.round}` : t.stage);
    } else {
        parts.push(KIND_LABELS[s.kind] || s.kind);
    }
    if (s.bestOf) parts.push(s.format === 'fixed' ? `${s.bestOf} maps` : `Bo${s.bestOf}`);
    if (s.forfeit) parts.push('forfeit');
    return parts.join(' · ');
}
