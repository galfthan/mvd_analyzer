// teams.js — THE team → colour mapping (see CLAUDE.md "Team colors").
//
// A team's colour is TEAM_PALETTE entry chosen by its NAME
// (assignTeamColors): "red"/"blue" get those colours, everyone else takes
// the remaining entries in name order — so a matchup colours the same in
// every demo whoever won. `order` is the frag-sorted canonical order
// (winner = index 0) and `colors[i]` belongs to `order[i]`; consumers index
// colours by a team's position in `order`, never by any other ordering.
// A field of MORE than two individual players (FFA, race) is coloured per
// player from PLAYER_PALETTE, also by name; a duel keeps the team palette.

import { store } from './store.js';

export const TEAM_PALETTE = ['#ff5050', '#50a0ff', '#4ecdc4', '#ffc107'];

const PLAYER_PALETTE = [
    '#ff5050', '#50a0ff', '#4ecdc4', '#ffc107',
    '#c084fc', '#7ee787', '#ff8fab', '#f97316',
    '#38bdf8', '#a3e635', '#e879f9', '#94a3b8',
];

// Per-match team colours. colors[i] is the colour of order[i]. Two fixed
// entries — "red"/"blue" teams keep those colours — then the rest in name
// sort order. Unused palette entries pad the tail so defensive colors[i]
// lookups past the team count still answer a palette colour.
/**
 * @param {string[]} teams
 * @returns {string[]}
 */
export function assignTeamColors(teams) {
    const slotOf = new Array(teams.length).fill(-1);
    const taken = new Set();
    teams.forEach((t, i) => {
        const n = String(t).toLowerCase();
        const fixed = n === 'red' ? 0 : n === 'blue' ? 1 : -1;
        if (fixed >= 0 && !taken.has(fixed)) { slotOf[i] = fixed; taken.add(fixed); }
    });
    const rest = teams.map((_, i) => i).filter(i => slotOf[i] < 0)
        .sort((a, b) => (teams[a] < teams[b] ? -1 : teams[a] > teams[b] ? 1 : 0));
    let next = 0;
    for (const i of rest) {
        while (taken.has(next)) next++;
        if (next >= TEAM_PALETTE.length) break;
        slotOf[i] = next;
        taken.add(next);
    }
    const colors = teams.map((_, i) => TEAM_PALETTE[slotOf[i]] ?? '#cccccc');
    for (let s = 0; s < TEAM_PALETTE.length; s++) {
        if (!taken.has(s)) colors.push(TEAM_PALETTE[s]);
    }
    return colors;
}

/**
 * @param {string[]} players
 * @returns {string[]}
 */
export function assignPlayerColors(players) {
    const byName = players.map((_, i) => i)
        .sort((a, b) => (players[a] < players[b] ? -1 : players[a] > players[b] ? 1 : 0));
    const colors = new Array(players.length);
    byName.forEach((idx, rank) => {
        colors[idx] = PLAYER_PALETTE[rank % PLAYER_PALETTE.length];
    });
    return colors;
}

/**
 * @typedef {object} TeamsState
 * @property {string[]} order          frag-sorted canonical order, winner first
 * @property {string[]} colors         colors[i] belongs to order[i]
 * @property {boolean} perPlayer       coloured per player (a field of > 2)
 * @property {Map<string, string>} teamOf  player name → team name
 */

export const teams = store(/** @type {TeamsState} */ ({ order: [], colors: [...TEAM_PALETTE], perPlayer: false, teamOf: new Map() }));

// setCanonicalTeams sets the match's canonical order once per demo.
// `players` ([{ name, team }]) maps a player to their side for
// teamIndexOf / colorOf; in an individual layout a player is their own side.
/**
 * @param {string[]} order
 * @param {boolean} [perPlayer]
 * @param {Array<{ name?: string, team?: string }>} [players]
 */
export function setCanonicalTeams(order, perPlayer = false, players = []) {
    const teamOf = new Map();
    for (const p of players) if (p?.name != null && p.team != null && !teamOf.has(p.name)) teamOf.set(p.name, p.team);
    teams.set({
        order: [...order],
        colors: perPlayer ? assignPlayerColors(order) : assignTeamColors(order),
        perPlayer,
        teamOf,
    });
}

// teamIndexOf: a player's index in the canonical order (the colour index),
// -1 when unknown. `individual` is true when every player is their own side.
/**
 * @param {TeamsState} state
 * @param {string} name
 * @param {boolean} [individual]
 */
export function teamIndexOf(state, name, individual) {
    if (individual) return state.order.indexOf(name);
    // An unknown player has no team: indexOf(undefined) is -1.
    return state.order.indexOf(/** @type {string} */ (state.teamOf.get(name)));
}

/**
 * @param {TeamsState} state
 * @param {string} name
 * @param {boolean} [individual]
 */
export function colorOf(state, name, individual) {
    return state.colors[teamIndexOf(state, name, individual)] || '#ccc';
}
