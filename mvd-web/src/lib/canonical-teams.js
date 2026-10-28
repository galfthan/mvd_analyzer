// canonical-teams.js — the match's canonical team order, chosen once per
// demo by the load (src/app/load.js) and handed to the teams store
// (src/state/teams.js setCanonicalTeams). CLAUDE.md "Team colors": this is
// the ONE canonical team → colour source — colors[i] belongs to order[i],
// winner at 0 — and it must never be derived from demoInfo.teams order or
// any per-feature ordering.

import { playerStatsRows, playerStatsTeamRows, sortByFragsDesc, fragsOfRow, teamFragTotals } from './player-stats.js';
import { isIndividualLayout, isMultiPlayerIndividual } from './result.js';

// canonicalTeamOrder → { order, perPlayer }: sorted by total frags, highest
// first. playerStats is the frag source (its score.frags is the corrected
// net score and it exists on demos with no KTX block at all), with
// demoInfo / match retained only to enumerate the team NAMES. In an
// individual layout every player is their own side, so the frag-sorted
// PLAYER list is the order — and since every row's `team` IS its name,
// every `order.indexOf(row.team)` colour lookup resolves to the player's
// rank without a second mapping. `perPlayer` (the player palette) only for
// more than two: a duel keeps the team palette.
export function canonicalTeamOrder(result) {
    const demoInfo = result.demoInfo;
    const individual = isIndividualLayout(result);
    const psTeams = playerStatsTeamRows(result);
    let teams = [];
    if (individual) {
        teams = sortByFragsDesc(playerStatsRows(result)).map(p => p.name);
        if (!teams.length && result.match?.players) {
            teams = [...result.match.players]
                .sort((a, b) => (b.frags || 0) - (a.frags || 0))
                .map(p => p.name);
        }
    } else if (psTeams.length) {
        teams = psTeams.map(t => t.name);
    } else if (demoInfo?.teams) {
        teams = [...demoInfo.teams];
    } else if (result.match?.teams) {
        teams = result.match.teams.map(t => t.name);
    }
    if (!individual && teams.length >= 2) {
        const rows = playerStatsRows(result);
        const teamFrags = psTeams.length
            ? Object.fromEntries(psTeams.map(t => [t.name, fragsOfRow(t)]))
            : teamFragTotals(rows.length ? rows : demoInfo?.players);
        teams.sort((a, b) => (teamFrags[b] || 0) - (teamFrags[a] || 0));
    }
    return { order: teams, perPlayer: isMultiPlayerIndividual(result) };
}

// fallbackTeamOrder: the demo's own team list when canonicalTeamOrder found
// none (an empty order) — demoInfo.teams for a team layout, else
// match.teams — or null when it has neither.
export function fallbackTeamOrder(result) {
    if (!isIndividualLayout(result) && result.demoInfo?.teams) {
        return { order: [...result.demoInfo.teams], perPlayer: false };
    }
    if (result.match?.teams) {
        return { order: result.match.teams.map(t => t.name), perPlayer: isMultiPlayerIndividual(result) };
    }
    return null;
}

// canonicalPlayers: the [{ name, team }] rows the teams store maps a player
// to their side with.
export function canonicalPlayers(result) {
    return [...(result?.match?.players || []), ...(result?.demoInfo?.players || [])];
}
