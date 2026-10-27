// result.js — small readers over a pipeline Result (schema:
// mvd-analytics/RESULT_SCHEMA.md) shared by app.js (window.MvdLib) and the
// Svelte side.

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
