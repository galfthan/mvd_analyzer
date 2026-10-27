// result.js — small readers over a pipeline Result (schema:
// mvd-analytics/RESULT_SCHEMA.md) shared by app.js (window.MvdLib) and the
// Svelte side.

import { playerStatsRows } from './player-stats.js';

// mapFileKey mirrors Go's Result.EffectiveMap(): `demoInfo.map`, then the
// serverinfo `map` key (on every pre-2020 demo, so the absence of the KTX
// block never reads as "no map"), then `match.map` — the canonical SHORT
// name. `match.mapTitle` is display-only text ("Castle of the Damned") and
// must never appear here.
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
export function demoOffsetSec(result) {
    return (result?.streams?.global?.demoOffset || 0) * 0.001;
}

export function formatDuration(seconds) {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
}

// The userid FTE tracks a player by, from the pipeline's name → userid map.
export function userIdOf(result, name) {
    return result?.timelineAnalysis?.playerUserIDs?.[name] ?? null;
}

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
export function isIndividualLayout(result) {
    return result?.match?.sources?.teams === 'individual';
}

// isTeamBasedMode reports the resolved ruleset: teamplay in force, so a team
// tag names a side.
export function isTeamBasedMode(result) {
    return result?.match?.gameMode?.teamBased === true;
}

// hasTeammates reports whether any player in this demo has a team-mate: the
// ruleset was teamplay AND the match is not laid out one side per player.
// Everything that renders a same-team quantity (team kills, team damage, the
// aim tab's Team victim filter) is empty without it.
export function hasTeammates(result) {
    return isTeamBasedMode(result) && !isIndividualLayout(result);
}

// isDuel reports a 1v1. app.js's applyDuelModeUI toggles the "Per Team"
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
export function isDuel(result) {
    const players = playerStatsRows(result);
    if (!players.length) return false;
    return players.length === 2 && players.every(p => p.team === p.name);
}
