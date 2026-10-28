// result.js — small readers over a pipeline Result (schema:
// mvd-analytics/RESULT_SCHEMA.md) shared by the load orchestration
// (src/app/) and the tabs.

import { playerStatsRows } from './player-stats.js';

/**
 * The pipeline Result as parsed JSON. Its fields are left open on purpose:
 * the shape is owned by the Go side (RESULT_SCHEMA.md, pinned by the golden
 * tests), and a hand-kept copy of it here would drift. What the checker
 * still sees is that a Result, not some other value, is passed around.
 * @typedef {Record<string, any>} Result
 */

// mapFileKey mirrors Go's Result.EffectiveMap(): `demoInfo.map`, then the
// serverinfo `map` key (on every pre-2020 demo, so the absence of the KTX
// block never reads as "no map"), then `match.map` — the canonical SHORT
// name. `match.mapTitle` is display-only text ("Castle of the Damned") and
// must never appear here.
/** @param {Result | null | undefined} result */
export function mapFileKey(result) {
    const raw = result?.demoInfo?.map || result?.metadata?.serverInfo?.map
        || result?.match?.map || '';
    let base = String(raw).toLowerCase();
    const slash = base.lastIndexOf('/');
    if (slash >= 0) base = base.slice(slash + 1);
    return base.endsWith('.bsp') ? base.slice(0, -4) : base;
}

// demoOffsetSec: demo-relative time = match time + this (seconds). The
// engine, hub links and playlist clips run on demo time, the page on match
// time (streams.global.demoOffset, ms).
/** @param {Result | null | undefined} result */
export function demoOffsetSec(result) {
    return (result?.streams?.global?.demoOffset || 0) * 0.001;
}

/** @param {number} seconds */
export function formatDuration(seconds) {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
}

// The userid FTE tracks a player by, from the pipeline's name → userid map.
/**
 * @param {Result | null | undefined} result
 * @param {string} name
 * @returns {number | null}
 */
export function userIdOf(result, name) {
    return result?.timelineAnalysis?.playerUserIDs?.[name] ?? null;
}

/**
 * @param {Result | null | undefined} result
 * @param {number | string} userid
 * @returns {string | null}
 */
export function nameOfUserId(result, userid) {
    const ids = result?.timelineAnalysis?.playerUserIDs || {};
    for (const [name, id] of Object.entries(ids)) if (id === Number(userid)) return name;
    return null;
}

// The frontend asks TWO different questions about teams, and they have two
// different answers on the same demo. Keep them apart:
//
//   isIndividualLayout — "is every player their own side here?" It decides
//     LAYOUT: which panels exist, which colour palette is handed out, whether
//     the scoreboard is per player. It is a property of the RESULT, so it
//     reads the layout the Go side actually applied: match.sources.teams ===
//     'individual' (rebuildIndividualMatch's own stamp).
//
//   isTeamBasedMode — "was the teamplay ruleset in force?" It decides team
//     SEMANTICS: whether a team kill or a team-damage figure is a thing that
//     can happen. It reads match.gameMode.teamBased, the resolved descriptor.
//
// They disagree on a 1v1 played on a teamplay server (a CTF server's duel,
// archive 2a2ed2e9ca…): the layout is individual — two participants, one side
// each — while the ruleset genuinely was teamplay. Deriving either from the
// other is what put a meaningless Team filter on an FFA aim tab and, in the
// other direction, would put team colours on a duel.
/** @param {Result | null | undefined} result */
export function isIndividualLayout(result) {
    return result?.match?.sources?.teams === 'individual';
}

// isTeamBasedMode reports the resolved ruleset: teamplay in force, so a team
// tag names a side.
/** @param {Result | null | undefined} result */
export function isTeamBasedMode(result) {
    return result?.match?.gameMode?.teamBased === true;
}

// hasTeammates reports whether any player in this demo has a team-mate: the
// ruleset was teamplay AND the match is not laid out one side per player.
// Everything that renders a same-team quantity (team kills, team damage, the
// aim tab's Team victim filter) is empty without it.
/** @param {Result | null | undefined} result */
export function hasTeammates(result) {
    return isTeamBasedMode(result) && !isIndividualLayout(result);
}

// isDuel reports a 1v1. applyDuelModeUI (src/app/load.js) toggles the "Per Team"
// aggregation panels and the standalone "Teams" summary off on one;
// everything else (the per-player scoreboard, weapon stats, item pickups)
// still renders normally, and the loc graph drops its team filters.
//
// Detection: the Go `roster` core node stamps every participant team field
// as their own name for duels (and only for exactly-2-player matches — see
// newRoster in analyzer/roster.go), so we can detect duel mode reliably by
// checking whether the two players each have `team === name`. This avoids
// depending on the metadata mode string, which can be "duel" / "1on1" /
// "LGC" / "Hoony" / missing entirely depending on the server flavour.
//
// Read off playerStats rather than demoInfo: the roster stamp is what is
// being tested, playerStats carries it on every demo, and a pre-KTX-block
// duel used to fall through here and render the team panels for a 1v1.
/** @param {Result | null | undefined} result */
export function isDuel(result) {
    const players = playerStatsRows(result);
    if (!players.length) return false;
    return players.length === 2 && players.every(p => p.team === p.name);
}

// isMultiPlayerIndividual reports whether this demo is laid out one side per
// player with MORE than two of them — a field rather than a matchup. Every
// two-sided surface reads it: the "Team A ↑ / Team B ↓" timeline graphs, the
// Chat tab's per-side say columns, region control, and the colour palette
// (per-player rather than the four-entry team one — see PLAYER_PALETTE and
// CLAUDE.md's team-colour rule). None of them has a pair of sides to name
// here: the top two players are not a matchup and everyone else does not
// appear at all. A two-participant match (a duel, or a 1v1 FFA) IS the
// two-sided view and keeps every one of them. The test is "not a duel"
// rather than "more than two": isDuel needs exactly two playerStats rows,
// so an individual layout with fewer (one player alone on a server) takes
// the field layout too — it has no pair of sides to show either.
/** @param {Result | null | undefined} result */
export function isMultiPlayerIndividual(result) {
    return isIndividualLayout(result) && !isDuel(result);
}
