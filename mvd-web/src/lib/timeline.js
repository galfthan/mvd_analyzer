// timeline.js — the Timeline tab without a DOM: the event streams it plots
// (in seconds), the series behind each graph (score, weapons, health /
// armor, powerups, region control and the per-player drill-downs), the
// shared view range, the team-status rows and their markup, and the hover
// read-outs. The tab (src/tabs/timeline/) and the canvas renderers
// (src/ui/timeline-canvas.js) draw them. The Map tab's legend
// (src/tabs/map/sidebar.js) reads the running frag counts and the hub watch
// link from here.
//
// Times are match-relative SECONDS throughout: the Result ships int32 ms
// (schema v8), converted once at intake (timelineEvents).

import {
    bucketTimeSec, bucketIndexAtOrAfter, playerValAt, playerAliveAt, teamSnapshot,
} from '../../../mvd-map-view/src/frames.js';
import { decodeRegionStateChar } from '../../../mvd-map-view/src/regions.js';
import { lowerBoundIndex } from '../../../mvd-map-view/src/util.js';
import { escapeHtml } from './html.js';
import { hubReplayUrl, hubLinkHtml } from './hub-url.js';
import { formatDuration, demoOffsetSec } from './result.js';
import { playerStatsRows } from './player-stats.js';

/**
 * @typedef {import('./result.js').Result} Result
 * @typedef {Record<string, any>} BucketView    timelineAnalysis.bucketView (ColumnarBuckets, columnar.go)
 * @typedef {Record<string, any>} BucketPlayer  one of its view.players columns
 * @typedef {{ time: number, [field: string]: any }} TimedEvent  a Result event, its time in seconds
 * @typedef {{ time: number, cumulative: Record<string, number> }} FragCount
 * @typedef {object} TimelineEvents
 * @property {TimedEvent[]} fragEvents
 * @property {TimedEvent[]} deathEvents
 * @property {TimedEvent[]} killEvents
 * @property {TimedEvent[]} backpacks
 * @property {TimedEvent[]} powerupEvents
 * @property {FragCount[]} fragCounts
 * @typedef {{ h: number, color: string }} StackSeg
 * @typedef {{ t: number, dt: number, up: StackSeg[], down?: StackSeg[] }} StackPoint
 * @typedef {{ points: StackPoint[], max: number }} DivergingSeries
 * @typedef {{ start: number, end: number, state: string, event?: TimedEvent }} StateSpan
 * @typedef {{ name: string, spans: StateSpan[] }} SpanRow
 * @typedef {{ rows: SpanRow[], teamA: string, teamB: string }} SpanRows
 * @typedef {{ time: number, color: string, isTop: boolean, drop: TimedEvent }} DropMark
 * @typedef {{ key: string, dir: number }} TeamStatusSort
 */

// The bucket view's interval (50 ms): one data point spans this long.
export const HR_DUR = 0.05;

export const GRAPH_COLORS = {
    RL:     'rgba(255, 107, 107, 0.9)',
    LG:     'rgba(0, 217, 255, 0.9)',
    RLLG:   'rgba(156, 39, 176, 0.9)',
    QUAD:   'rgba(0, 150, 255, 0.9)',
    PENT:   'rgba(255, 0, 0, 0.9)',
    RING:   'rgba(255, 235, 59, 0.9)',
    HEALTH: 'rgba(0, 200, 83, 0.9)',
    RA:     'rgba(255, 50, 50, 0.9)',
    YA:     'rgba(255, 200, 0, 0.9)',
    GA:     'rgba(0, 180, 0, 0.6)',
};

// Plot background — a shade apart from the panel so the graph area reads as
// one; the same for every graph.
export const PLOT_BG_COLOR = '#16213e';

/**
 * @param {string} hex
 * @returns {[number, number, number]}
 */
export function hexToRgb(hex) {
    return [parseInt(hex.slice(1, 3), 16), parseInt(hex.slice(3, 5), 16), parseInt(hex.slice(5, 7), 16)];
}

// The strong (30 % darker) team colour the powerup and region-control spans
// are filled with.
/** @param {string} hex */
export function teamStrongColor(hex) {
    const [r, g, b] = hexToRgb(hex);
    return `rgb(${Math.round(r * 0.7)}, ${Math.round(g * 0.7)}, ${Math.round(b * 0.7)})`;
}

// A team hex darkened toward black: the behind-half of a per-player +/- row
// reads as a muted mirror of the team-coloured ahead-half.
/** @param {string} hex */
export function dimColor(hex) {
    const [r, g, b] = hexToRgb(hex);
    return `rgb(${Math.round(r * 0.55)},${Math.round(g * 0.55)},${Math.round(b * 0.55)})`;
}

// ─── Event intake ───────────────────────────────────────────────────────────

/** @type {WeakMap<Result, TimelineEvents>} */
const eventCache = new WeakMap();

// timelineEvents: the Result's timeline event streams in seconds, built once
// per Result. fragEvents is sorted by time here so the score series and the
// running frag counts don't each re-sort it on every render; fragCounts is
// [{ time, cumulative: { player: frags } }], one entry per frag event.
/**
 * @param {Result | null | undefined} result
 * @returns {TimelineEvents}
 */
export function timelineEvents(result) {
    if (!result) return emptyEvents();
    let ev = eventCache.get(result);
    if (ev) return ev;
    const ta = result.timelineAnalysis;
    ev = {
        fragEvents: (ta?.fragEvents || []).map(/** @param {TimedEvent} f */ f => ({ ...f, time: f.time * 0.001 }))
            .sort(/** @param {TimedEvent} a @param {TimedEvent} b */ (a, b) => a.time - b.time),
        // Every death, for the frags / deaths drill-down.
        deathEvents: (ta?.deathEvents || []).map(/** @param {TimedEvent} d */ d => ({ ...d, time: d.time * 0.001 })),
        // Enemy kills, killer-keyed, for the same.
        killEvents: (ta?.killEvents || []).map(/** @param {TimedEvent} k */ k => ({ ...k, time: k.time * 0.001 })),
        // RL / LG drops (KTX hint or reconstructed — see d.source).
        backpacks: (result.backpacks || []).map(/** @param {TimedEvent} d */ d => ({ ...d, time: d.time * 0.001 })),
        // One record per powerup run: player, team, frags, duration.
        powerupEvents: (ta?.powerupEvents || []).map(/** @param {TimedEvent} p */ p => ({
            ...p,
            time: p.time * 0.001,
            endTime: (p.endTime || 0) * 0.001,
            duration: (p.duration || 0) * 0.001,
        })),
        fragCounts: [],
    };
    /** @type {Record<string, number>} */
    const running = {};
    for (const fe of ev.fragEvents) {
        running[fe.player] = (running[fe.player] || 0) + (fe.delta || 1);
        ev.fragCounts.push({ time: fe.time, cumulative: { ...running } });
    }
    eventCache.set(result, ev);
    return ev;
}

/** @returns {TimelineEvents} */
function emptyEvents() {
    return { fragEvents: [], deathEvents: [], killEvents: [], backpacks: [], powerupEvents: [], fragCounts: [] };
}

// fragsAt: { player: frags } at `time` — the Timeline's team status and the
// Map legend.
/**
 * @param {Result | null | undefined} result
 * @param {number} time
 * @returns {Record<string, number>}
 */
export function fragsAt(result, time) {
    const counts = timelineEvents(result).fragCounts;
    const idx = lowerBoundIndex(counts, time, /** @param {FragCount[]} a @param {number} i */ (a, i) => a[i].time);
    return idx < 0 ? {} : counts[idx].cumulative;
}

// The match length in seconds (the page clock's duration).
/** @param {Result | null | undefined} result */
export function matchDurationSec(result) {
    return (result?.match?.duration || 600000) * 0.001;
}

// ─── View range ─────────────────────────────────────────────────────────────
//
// Every graph shows the same window: the selected segment (MvdState.segment,
// src/state/timeline.js), or the whole match when there is none. The unified
// bar's range select and a graph's Ctrl+wheel zoom / drag pan both set it.

const MIN_VIEW_SPAN = 2; // seconds — don't zoom past this

/**
 * @param {{ start: number, end: number } | null | undefined} segment
 * @param {number} duration
 * @returns {[number, number]}
 */
export function viewRange(segment, duration) {
    return segment ? [segment.start, segment.end] : [0, duration || 0];
}

// clampSegment: the segment a requested [start, end] window becomes — at
// least MIN_VIEW_SPAN wide, slid back inside [0, duration] without
// shrinking, and null when it covers the whole match. undefined when there
// is no match to clamp against (the request is ignored).
/**
 * @param {number} start
 * @param {number} end
 * @param {number} duration
 * @returns {{ start: number, end: number } | null | undefined}
 */
export function clampSegment(start, end, duration) {
    if (!(duration > 0)) return undefined;
    if (end - start < MIN_VIEW_SPAN) {
        const mid = (start + end) / 2;
        start = mid - MIN_VIEW_SPAN / 2;
        end = mid + MIN_VIEW_SPAN / 2;
    }
    if (start < 0) { end -= start; start = 0; }
    if (end > duration) { start -= (end - duration); end = duration; }
    start = Math.max(0, start);
    end = Math.min(duration, end);
    if (start <= 0 && end >= duration) return null;
    return { start, end };
}

// The current-time line's left offset (percent of the plot width) for the
// window, or null when the window is empty.
/**
 * @param {number} t
 * @param {number} start
 * @param {number} end
 */
export function indicatorPct(t, start, end) {
    const range = end - start;
    if (range <= 0) return null;
    return Math.max(0, Math.min(100, ((t - start) / range) * 100));
}

// rollingWindow: the span a Player-tab graph pane shows around the
// playhead — `width` seconds, three quarters behind it, clamped into the
// match; width 0 (or one longer than the match) is the whole match.
/**
 * @param {number} t
 * @param {number} width
 * @param {number} duration
 * @returns {[number, number]}
 */
export function rollingWindow(t, width, duration) {
    if (!(width > 0) || width >= duration) return [0, duration];
    const start = Math.min(Math.max(0, t - width * 0.75), duration - width);
    return [start, start + width];
}

// ─── Axis ticks ─────────────────────────────────────────────────────────────

const NICE_TICK_INTERVALS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600];

/**
 * @param {number} duration
 * @param {number} maxTicks
 */
export function pickTickInterval(duration, maxTicks) {
    const target = duration / maxTicks;
    for (const iv of NICE_TICK_INTERVALS) {
        if (iv >= target) return iv;
    }
    return NICE_TICK_INTERVALS[NICE_TICK_INTERVALS.length - 1];
}

// axisTicks: the labelled ticks of a [start, end] axis `widthCss` px wide —
// [{ t, frac, label }]. `maxTicks` caps the density (the on-canvas axes use
// 12, the per-player shared axis 10); `slack` admits a tick a hair past the
// end.
/**
 * @param {number} start
 * @param {number} end
 * @param {number} widthCss
 * @param {number} maxTicks
 * @param {number} [slack]
 * @returns {Array<{ t: number, frac: number, label: string }>}
 */
export function axisTicks(start, end, widthCss, maxTicks, slack = 0) {
    const duration = end - start;
    if (duration <= 0) return [];
    const targetTicks = Math.max(4, Math.min(maxTicks, Math.floor(widthCss / 100)));
    const interval = pickTickInterval(duration, targetTicks);
    const out = [];
    const first = Math.ceil(start / interval) * interval;
    for (let t = first; t <= end + slack; t += interval) {
        out.push({ t, frac: (t - start) / duration, label: formatDuration(t) });
    }
    return out;
}

// ─── Scanline sampling ──────────────────────────────────────────────────────

// Output-driven sampler shared by the diverging and mini renderers. Returns
// a closure that, given a query time, walks a monotonic cursor over the
// sorted `points` (step function / hold-last) and returns the active point —
// or null before the first point, or past the last point's declared dt.
// Walking the OUTPUT (one call per pixel column) makes "every column is
// drawn" structural and gives a draw cost that scales with canvas width, not
// bucket count — empty buckets leave no unpainted stripes through the bars.
/**
 * @template {{ t: number, dt?: number }} P
 * @param {P[]} points
 * @returns {(tQuery: number) => P | null}
 */
export function makeScanlineSampler(points) {
    let cursor = -1;
    return (tQuery) => {
        while (cursor + 1 < points.length && points[cursor + 1].t <= tQuery) cursor++;
        if (cursor < 0) return null;
        const pt = points[cursor];
        // Last point: cap at its declared dt so we don't paint past the end
        // of the data when the view extends slightly beyond it.
        if (cursor === points.length - 1) {
            const endT = pt.t + (pt.dt || 0);
            if (endT > 0 && tQuery > endT) return null;
        }
        return pt;
    };
}

// ─── Series: team graphs ────────────────────────────────────────────────────
//
// Each returns { points, max } for the diverging renderer: points are
// [{ t, dt, up: [{ h, color }], down: [{ h, color }] }] — team A (teams[0])
// stacks up, team B (teams[1]) down.

/**
 * @param {BucketView | null | undefined} view
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @returns {DivergingSeries}
 */
export function prepWeaponsData(view, startTime, endTime, teams) {
    if (!view || !view.count) return { points: [], max: 4 };
    /** @type {StackPoint[]} */
    const points = [];
    let maxVal = 4;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        const tdA = /** @type {Record<string, any>} */ (teamSnapshot(view, teams[0], i));
        const tdB = /** @type {Record<string, any>} */ (teamSnapshot(view, teams[1], i));
        const upT = (tdA.rl || 0) + (tdA.lg || 0) + (tdA.rllg || 0);
        const dnT = (tdB.rl || 0) + (tdB.lg || 0) + (tdB.rllg || 0);
        maxVal = Math.max(maxVal, upT, dnT);
        points.push({
            t: bt, dt: HR_DUR,
            up: [
                { h: tdA.rllg || 0, color: GRAPH_COLORS.RLLG },
                { h: tdA.lg || 0, color: GRAPH_COLORS.LG },
                { h: tdA.rl || 0, color: GRAPH_COLORS.RL },
            ],
            down: [
                { h: tdB.rllg || 0, color: GRAPH_COLORS.RLLG },
                { h: tdB.lg || 0, color: GRAPH_COLORS.LG },
                { h: tdB.rl || 0, color: GRAPH_COLORS.RL },
            ],
        });
    }
    return { points, max: maxVal };
}

/** @param {string} weapon */
function dropColor(weapon) {
    return weapon === 'rl' ? GRAPH_COLORS.RL : weapon === 'lg' ? GRAPH_COLORS.LG : null;
}

// computeBackpackDrops: the weapon graph's drop dots — every RL / LG pack
// dropped in the window, isTop for team A, carrying the drop itself for the
// hover read-out. A drop by neither side (a spectator, a mid-substitution
// player) has no strip to sit in and is skipped.
/**
 * @param {TimedEvent[] | null | undefined} backpacks
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @returns {DropMark[]}
 */
export function computeBackpackDrops(backpacks, startTime, endTime, teams) {
    if (!backpacks || backpacks.length === 0) return [];
    const teamA = teams[0], teamB = teams[1];
    /** @type {DropMark[]} */
    const out = [];
    for (const d of backpacks) {
        if (d.time < startTime || d.time > endTime) continue;
        let isTop;
        if (d.team === teamA) isTop = true;
        else if (d.team === teamB) isTop = false;
        else continue;
        const color = dropColor(d.weapon);
        if (!color) continue;
        out.push({ time: d.time, color, isTop, drop: d });
    }
    return out;
}

/**
 * @param {BucketView | null | undefined} view
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @returns {DivergingSeries}
 */
export function prepHealthArmorData(view, startTime, endTime, teams) {
    if (!view || !view.count) return { points: [], max: 400 };
    /** @type {StackPoint[]} */
    const points = [];
    let maxVal = 400;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        const tdA = /** @type {Record<string, any>} */ (teamSnapshot(view, teams[0], i));
        const tdB = /** @type {Record<string, any>} */ (teamSnapshot(view, teams[1], i));
        maxVal = Math.max(maxVal, (tdA.th || 0) + (tdA.ta || 0), (tdB.th || 0) + (tdB.ta || 0));
        points.push({ t: bt, dt: HR_DUR, up: buildHASegments(tdA), down: buildHASegments(tdB) });
    }
    return { points, max: maxVal };
}

// A team's health, then its armor split by the armor types its members wear.
/**
 * @param {Record<string, any>} td  a teamSnapshot
 * @returns {StackSeg[]}
 */
export function buildHASegments(td) {
    /** @type {StackSeg[]} */
    const segs = [];
    if ((td.th || 0) > 0) segs.push({ h: td.th, color: GRAPH_COLORS.HEALTH });
    const armor = td.ta || 0;
    const abt = td.abt || {};
    const ra = abt.ra || 0, ya = abt.ya || 0, ga = abt.ga || 0;
    const total = ra + ya + ga;
    if (total > 0 && armor > 0) {
        if (ga > 0) segs.push({ h: (ga / total) * armor, color: GRAPH_COLORS.GA });
        if (ya > 0) segs.push({ h: (ya / total) * armor, color: GRAPH_COLORS.YA });
        if (ra > 0) segs.push({ h: (ra / total) * armor, color: GRAPH_COLORS.RA });
    } else if (armor > 0) {
        segs.push({ h: armor, color: GRAPH_COLORS.YA });
    }
    return segs;
}

// The running score: team A's frags minus team B's, carried from match start
// so the window's left edge shows the score at that point; sampled ~400 times
// across the window. `colors` are the two teams' colours.
/**
 * @param {TimedEvent[]} fragEvents
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @param {string[]} colors
 * @returns {DivergingSeries}
 */
export function prepScoreData(fragEvents, startTime, endTime, teams, colors) {
    if (teams.length < 2) return { points: [], max: 10 };
    let score = 0;
    for (const f of fragEvents) {
        if (f.time >= startTime) break;
        if (f.team === teams[0]) score += (f.delta || 1);
        else if (f.team === teams[1]) score -= (f.delta || 1);
    }
    const scoreAt = [{ time: startTime, score }];
    let s = score;
    for (const f of fragEvents) {
        if (f.time < startTime) continue;
        if (f.time > endTime) break;
        if (f.team === teams[0]) s += (f.delta || 1);
        else if (f.team === teams[1]) s -= (f.delta || 1);
        scoreAt.push({ time: f.time, score: s });
    }
    scoreAt.push({ time: endTime, score: s });

    const duration = endTime - startTime;
    const sampleRate = Math.max(0.5, duration / 400);
    const [rA, gA, bA] = hexToRgb(colors[0]);
    const [rB, gB, bB] = hexToRgb(colors[1]);
    const cA = `rgba(${rA},${gA},${bA},0.8)`;
    const cB = `rgba(${rB},${gB},${bB},0.8)`;
    let maxVal = 10;
    /** @type {StackPoint[]} */
    const points = [];
    let si = 0;
    for (let t = startTime; t < endTime; t += sampleRate) {
        while (si + 1 < scoreAt.length && scoreAt[si + 1].time <= t) si++;
        const v = scoreAt[si].score;
        maxVal = Math.max(maxVal, Math.abs(v));
        points.push({
            t, dt: sampleRate,
            up: v > 0 ? [{ h: v, color: cA }] : [],
            down: v < 0 ? [{ h: -v, color: cB }] : [],
        });
    }
    return { points, max: maxVal };
}

// ─── Series: span timelines (powerups, region control) ──────────────────────
//
// Rows of [{ start, end, state }] spans for the span renderer.

const POWERUP_TYPES = [
    { key: 'quad', name: 'Quad' },
    { key: 'pent', name: 'Pent' },
    { key: 'ring', name: 'Ring' },
];

// One row per powerup type, each run a span coloured by which of the two
// teams held it ('other' — a mid-game team change, a spectator — grey). A
// run is clipped to the window; the span keeps the run itself for the hover
// read-out, which shows its full duration. null without runs or two teams.
/**
 * @param {TimedEvent[] | null | undefined} powerupEvents
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @returns {SpanRows | null}
 */
export function prepPowerupRowsData(powerupEvents, startTime, endTime, teams) {
    if (!powerupEvents || powerupEvents.length === 0) return null;
    const teamA = teams[0], teamB = teams[1];
    if (!teamA || !teamB) return null;
    /** @type {Map<string, SpanRow>} */
    const rowByKey = new Map();
    const rows = POWERUP_TYPES.map(pu => {
        /** @type {SpanRow} */
        const r = { name: pu.name, spans: [] };
        rowByKey.set(pu.key, r);
        return r;
    });
    for (const ev of powerupEvents) {
        const row = rowByKey.get(ev.powerupType);
        if (!row) continue;
        const start = Math.max(startTime, ev.time);
        const end = Math.min(endTime, ev.endTime);
        if (end <= start) continue;
        let state;
        if (ev.team === teamA) state = 'teamA';
        else if (ev.team === teamB) state = 'teamB';
        else state = 'other';
        row.spans.push({ start, end, state, event: ev });
    }
    return { rows, teamA, teamB };
}

// One row per control region, spans of the state Go assigned each bucket
// (rc.bucketStates[region] — one character per 50 ms bucket on the bucket
// view's grid, so a string index IS the bucket index). `rc` is the region
// set in effect: { regions, bucketStates, teamA, teamB }; Go's team labels
// name the A / B states when it shipped them. null without regions, states
// or a bucket view.
/**
 * @param {{ regions?: Array<{ name: string }>, bucketStates?: Record<string, string> | null, teamA?: string | null, teamB?: string | null } | null | undefined} rc
 * @param {BucketView | null | undefined} view
 * @param {number} startTime
 * @param {number} endTime
 * @param {string[]} teams
 * @returns {SpanRows | null}
 */
export function prepRegionControlData(rc, view, startTime, endTime, teams) {
    const regions = rc?.regions;
    const bucketStates = rc?.bucketStates;
    if (!regions || regions.length === 0 || !bucketStates) return null;
    if (!view || !view.count) return null;
    const teamA = rc.teamA || teams[0];
    const teamB = rc.teamB || teams[1];
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    /** @type {SpanRow[]} */
    const rows = [];
    for (const r of regions) {
        const s = bucketStates[r.name];
        if (typeof s !== 'string') continue;
        /** @type {StateSpan[]} */
        const spans = [];
        /** @type {string | null} */
        let curState = null;
        let curStart = startTime;
        for (let i = idx0; i < view.count; i++) {
            const bt = bucketTimeSec(view, i);
            if (bt > endTime) break;
            const state = decodeRegionStateChar(i < s.length ? s[i] : '_');
            if (state !== curState) {
                if (curState) spans.push({ start: curStart, end: bt, state: curState });
                curState = state;
                curStart = bt;
            }
        }
        if (curState) spans.push({ start: curStart, end: endTime, state: curState });
        rows.push({ name: r.name, spans });
    }
    return { rows, teamA, teamB };
}

// ─── Per-player drill-downs ─────────────────────────────────────────────────
//
// Score, Weapons and Team Health/Armor each expand to a per-player view, all
// from the bucket view (view.players[name]) plus the kill / death / drop
// streams — no extra data fetch.

// rosterTeamOf: a player's team as the Result's roster names it — the KTX
// demoinfo block where the demo has one, else playerStats (the canonical
// roster, present on every demo) — the source the Map's player symbols use.
/**
 * @param {Result | null | undefined} result
 * @returns {Map<string, string>}
 */
export function rosterTeamOf(result) {
    /** @type {Map<string, string>} */
    const teamOf = new Map();
    const demoPlayers = result?.demoInfo?.players || [];
    const rows = demoPlayers.length ? demoPlayers : playerStatsRows(result);
    for (const p of rows) {
        if (p?.name != null && p.team && !teamOf.has(p.name)) teamOf.set(p.name, p.team);
    }
    return teamOf;
}

// playersByTeam groups the bucket view's players by side, in the canonical
// team order, so a group's index is its colour index. Two groups on a
// two-sided match (players of neither team — spectators, mid-game joiners —
// are dropped); in the individual layout (`individual`) one group per entry
// of `teams`, the frag-sorted player list, a player's side being their own
// name. Names sort within a group.
/**
 * @param {BucketView | null | undefined} view
 * @param {string[]} teams
 * @param {Map<string, string>} teamOf
 * @param {boolean} [individual]
 * @returns {string[][]}
 */
export function playersByTeam(view, teams, teamOf, individual) {
    const groupCount = individual ? Math.max(2, teams.length) : 2;
    const out = Array.from({ length: groupCount }, () => /** @type {string[]} */ ([]));
    if (teams.length < 2 || !view || !view.players) return out;
    for (const name of Object.keys(view.players)) {
        // A player of neither team has no teamOf entry: indexOf(undefined) is -1.
        const ti = teams.indexOf(individual ? name : /** @type {string} */ (teamOf.get(name)));
        if (ti < 0 || ti >= out.length) continue;
        out[ti].push(name);
    }
    for (const group of out) group.sort();
    return out;
}

// One player's health, then armor in its type's colour.
/**
 * @param {number} h
 * @param {number} a
 * @param {string} at
 * @returns {StackSeg[]}
 */
function buildPlayerHASegments(h, a, at) {
    /** @type {StackSeg[]} */
    const segs = [];
    if (h > 0) segs.push({ h, color: GRAPH_COLORS.HEALTH });
    if (a > 0) {
        const color = at === 'ra' ? GRAPH_COLORS.RA
                    : at === 'ga' ? GRAPH_COLORS.GA
                    : GRAPH_COLORS.YA; // ya or unknown
        segs.push({ h: a, color });
    }
    return segs;
}

// A player's health / armor stack per bucket; an empty stack where they are
// dead or absent, so the chart shows a gap there.
/**
 * @param {BucketView | null | undefined} view
 * @param {BucketPlayer | null | undefined} cp
 * @param {number} startTime
 * @param {number} endTime
 * @returns {StackPoint[]}
 */
export function prepPlayerHAData(view, cp, startTime, endTime) {
    /** @type {StackPoint[]} */
    const points = [];
    if (!view || !view.count || !cp) return points;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        const h = playerValAt(cp, 'h', i) || 0;
        const a = playerValAt(cp, 'a', i) || 0;
        const at = playerValAt(cp, 'at', i) || '';
        points.push({ t: bt, dt: HR_DUR, up: buildPlayerHASegments(h, a, at) });
    }
    return points;
}

// The tallest stack over a set of per-player series, never under 300, so the
// health / armor minis share one comparable scale.
/** @param {StackPoint[][]} seriesList */
export function sharedStackMax(seriesList) {
    let maxVal = 300;
    for (const pts of seriesList) {
        for (const p of pts) {
            let s = 0;
            for (const seg of p.up) s += seg.h;
            if (s > maxVal) maxVal = s;
        }
    }
    return maxVal;
}

// A player's running plus/minus — enemy kills (killEvents, the canonical
// frag log: suicides and teamkills excluded) minus deaths (deathEvents,
// every death once) — sampled like the score, up in `upColor` when ahead,
// down in `downColor` when behind; plus the window-end totals. The
// full-match endpoint is byPlayer.kills − byPlayer.deaths, which reconciles
// with the kills-based efficiency on each row
// (ktx/src/statsTables.c calculateEfficiency).
/**
 * @param {TimedEvent[]} killEvents
 * @param {TimedEvent[]} deathEvents
 * @param {string} name
 * @param {number} startTime
 * @param {number} endTime
 * @param {string} upColor
 * @param {string} downColor
 */
export function prepPlayerFragDeathData(killEvents, deathEvents, name, startTime, endTime, upColor, downColor) {
    let kills = 0, deaths = 0;
    for (const k of killEvents) {
        if (k.time >= startTime) break;
        if (k.player === name) kills += 1;
    }
    for (const d of deathEvents) {
        if (d.time >= startTime) break;
        if (d.player === name) deaths += 1;
    }
    const evs = [];
    for (const k of killEvents) {
        if (k.time < startTime) continue;
        if (k.time > endTime) break;
        if (k.player === name) evs.push({ time: k.time, dn: 1 });
    }
    for (const d of deathEvents) {
        if (d.time < startTime) continue;
        if (d.time > endTime) break;
        if (d.player === name) evs.push({ time: d.time, dn: -1 });
    }
    evs.sort((a, b) => a.time - b.time);

    const stepAt = [{ time: startTime, net: kills - deaths }];
    let ck = kills, cd = deaths;
    for (const e of evs) {
        if (e.dn > 0) ck += 1; else cd += 1;
        stepAt.push({ time: e.time, net: ck - cd });
    }
    stepAt.push({ time: endTime, net: ck - cd });

    const duration = endTime - startTime;
    const sampleRate = Math.max(0.5, duration / 400);
    let maxVal = 1;
    /** @type {StackPoint[]} */
    const points = [];
    let si = 0;
    for (let t = startTime; t < endTime; t += sampleRate) {
        while (si + 1 < stepAt.length && stepAt[si + 1].time <= t) si++;
        const v = stepAt[si].net;
        maxVal = Math.max(maxVal, Math.abs(v));
        points.push({
            t, dt: sampleRate,
            up: v > 0 ? [{ h: v, color: upColor }] : [],
            down: v < 0 ? [{ h: -v, color: downColor }] : [],
        });
    }
    return { points, max: maxVal, kills: ck, deaths: cd };
}

// Each player's kills / deaths and efficiency AT time t (not whole-match
// totals): { name: { text, title } } for the per-player frags labels.
/**
 * @param {TimedEvent[]} killEvents
 * @param {TimedEvent[]} deathEvents
 * @param {string[]} names
 * @param {number} t
 * @returns {Record<string, { text: string, title: string }>}
 */
export function fragDeathLabels(killEvents, deathEvents, names, t) {
    /** @type {Record<string, number>} */
    const kc = {};
    /** @type {Record<string, number>} */
    const dc = {};
    for (const e of killEvents) if (e.time <= t) kc[e.player] = (kc[e.player] || 0) + 1;
    for (const e of deathEvents) if (e.time <= t) dc[e.player] = (dc[e.player] || 0) + 1;
    /** @type {Record<string, { text: string, title: string }>} */
    const out = {};
    for (const name of names) {
        const k = kc[name] || 0, d = dc[name] || 0;
        const eff = (k + d) > 0 ? Math.round((k / (k + d)) * 100) : 0;
        out[name] = {
            text: `${k}/${d} · ${eff}%`,
            title: `${k} kills / ${d} deaths · ${eff}% efficiency at ${formatDuration(t)}`,
        };
    }
    return out;
}

// One player's weapon state over the window as spans: 'rl', 'lg' or 'rllg'
// while alive and holding them, nothing otherwise.
/**
 * @param {BucketView | null | undefined} view
 * @param {BucketPlayer | null | undefined} cp
 * @param {number} startTime
 * @param {number} endTime
 * @returns {StateSpan[]}
 */
export function prepPlayerWeaponSpans(view, cp, startTime, endTime) {
    /** @type {StateSpan[]} */
    const spans = [];
    if (!view || !view.count || !cp) return spans;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    /** @type {string | null} */
    let curState = null, curStart = startTime;
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        /** @type {string | null} */
        let state = null;
        if (playerAliveAt(cp, i)) {
            const rl = !!playerValAt(cp, 'rl', i);
            const lg = !!playerValAt(cp, 'lg', i);
            if (rl && lg) state = 'rllg';
            else if (rl) state = 'rl';
            else if (lg) state = 'lg';
        }
        if (state !== curState) {
            if (curState) spans.push({ start: curStart, end: bt, state: curState });
            curState = state;
            curStart = bt;
        }
    }
    if (curState) spans.push({ start: curStart, end: endTime, state: curState });
    return spans;
}

// The per-player weapons rows' drop dots: [{ row, time, color }], a row
// being the dropper's index in `rowPlayers`.
/**
 * @param {TimedEvent[] | null | undefined} backpacks
 * @param {string[]} rowPlayers
 * @param {number} startTime
 * @param {number} endTime
 * @returns {Array<{ row: number, time: number, color: string }>}
 */
export function playerDropMarks(backpacks, rowPlayers, startTime, endTime) {
    /** @type {Array<{ row: number, time: number, color: string }>} */
    const marks = [];
    for (const d of (backpacks || [])) {
        if (d.time < startTime || d.time > endTime) continue;
        const row = rowPlayers.indexOf(d.player);
        if (row < 0) continue;
        const color = dropColor(d.weapon);
        if (!color) continue;
        marks.push({ row, time: d.time, color });
    }
    return marks;
}

export const WEAPON_SPAN_COLORS = { rl: GRAPH_COLORS.RL, lg: GRAPH_COLORS.LG, rllg: GRAPH_COLORS.RLLG };

// ─── Team status ────────────────────────────────────────────────────────────

// The roster tables' sort. The default is player name ascending: the rosters
// are rebuilt on every playhead tick, so a frags-descending order made rows
// swap places as the match progressed — churn under the reader's eyes while
// scrubbing. Reset per demo.
/** @type {TeamStatusSort} */
export const TEAM_STATUS_SORT_DEFAULT = { key: 'name', dir: 1 };

// A click on a sortable header: the active column flips, a new one starts
// names A→Z and numbers biggest-first.
/**
 * @param {TeamStatusSort} sort
 * @param {string} key
 * @returns {TeamStatusSort}
 */
export function nextTeamStatusSort(sort, key) {
    if (sort.key === key) return { key, dir: -sort.dir };
    return { key, dir: key === 'name' ? 1 : -1 };
}

// dir: 1 ascending, -1 descending. Numeric keys tie-break on name (always
// ascending) so equal-frag rows keep a stable, readable order.
/**
 * @param {TeamStatusSort} sort
 * @returns {(a: TeamStatusRow, b: TeamStatusRow) => number}
 */
export function compareTeamStatusRows(sort) {
    return (a, b) => {
        const k = sort.key;
        if (k === 'name') return a.name.localeCompare(b.name) * sort.dir;
        const cmp = (/** @type {any} */ (a)[k] || 0) - (/** @type {any} */ (b)[k] || 0);
        return cmp ? cmp * sort.dir : a.name.localeCompare(b.name);
    };
}

// teamStatusRows: one team's rows at a frame — every known member, the dead
// and respawning shown with '-' stats as on the Map legend. Members are the
// demoinfo roster's, the roster the Map's symbols use (rosterTeamOf), and
// anyone in the frame on that team. `frame` is the row-shape player map at
// the time (reconstructBucketPlayers), `frags` the running counts.
/**
 * @typedef {object} TeamStatusRow
 * @property {string} name
 * @property {boolean} dead
 * @property {number} health
 * @property {number} armor
 * @property {string} armorType
 * @property {boolean} hasRL
 * @property {boolean} hasLG
 * @property {boolean} hasQuad
 * @property {boolean} hasPent
 * @property {boolean} hasRing
 * @property {number} frags
 */

/**
 * @param {Result | null | undefined} result
 * @param {Map<string, string>} teamOf
 * @param {Record<string, Record<string, any>>} frame  reconstructBucketPlayers' row-shape players
 * @param {Record<string, number>} frags
 * @param {string} team
 * @param {TeamStatusSort} sort
 * @returns {TeamStatusRow[]}
 */
export function teamStatusRows(result, teamOf, frame, frags, team, sort) {
    /** @type {Set<string>} */
    const names = new Set();
    for (const dp of (result?.demoInfo?.players || [])) {
        if (dp.team === team) names.add(dp.name);
    }
    for (const [name, t] of teamOf) {
        if (t === team) names.add(name);
    }
    for (const [name, data] of Object.entries(frame)) {
        if ((data.team || teamOf.get(name)) === team) names.add(name);
    }
    /** @type {TeamStatusRow[]} */
    const players = [];
    for (const name of names) {
        const data = frame[name];
        const isDead = !data || (data.d ?? data.dead) || (data.h ?? data.health ?? 0) <= 0;
        players.push({
            name,
            dead: !!isDead,
            health: isDead ? 0 : (data.h ?? data.health ?? 0),
            armor: isDead ? 0 : (data.a ?? data.armor ?? 0),
            armorType: isDead ? '' : (data.at ?? data.armorType ?? ''),
            hasRL: isDead ? false : (data.rl ?? data.hasRL ?? false),
            hasLG: isDead ? false : (data.lg ?? data.hasLG ?? false),
            hasQuad: isDead ? false : (data.q ?? data.hasQuad ?? false),
            hasPent: isDead ? false : (data.pe ?? false),
            hasRing: isDead ? false : (data.r ?? data.hasRing ?? false),
            frags: frags[name] || 0,
        });
    }
    players.sort(compareTeamStatusRows(sort));
    return players;
}

// The "hub" link of a roster row: the hub replay at `time`, tracking the
// player. Our times are match-relative; hub's are demo-relative (countdown
// included), hence `demoOffset` (seconds). '' without a hub game or userid.
/**
 * @param {string} playerName
 * @param {number} time
 * @param {{ gameId?: number } | null | undefined} hubInfo
 * @param {Record<string, number>} playerUserIDs
 * @param {number} [demoOffset]
 */
export function hubWatchLinkHtml(playerName, time, hubInfo, playerUserIDs, demoOffset) {
    if (!hubInfo || !hubInfo.gameId) return '';
    const trackId = playerUserIDs[playerName];
    if (!trackId) return '';
    const from = Math.floor(time + (demoOffset || 0));
    const url = hubReplayUrl({ gameId: hubInfo.gameId, from, track: trackId });
    return hubLinkHtml(url, true);
}

// teamStatusHtml: one side's roster table at `time` — the team name and frag
// total in the team colour, a row per player, a totals row. The sortable
// headers carry their key (the tab's delegated click reads it) and the same
// arrow classes makeSortable sets.
/**
 * @param {Result | null | undefined} result
 * @param {TeamStatusRow[]} players
 * @param {string} team
 * @param {string} color
 * @param {TeamStatusSort} sort
 * @param {number} time
 */
export function teamStatusHtml(result, players, team, color, sort, time) {
    const teamFrags = players.reduce((s, p) => s + p.frags, 0);
    const teamHealth = players.reduce((s, p) => s + (p.health || 0), 0);
    const teamArmor = players.reduce((s, p) => s + (p.armor || 0), 0);
    const hubInfo = result?.hubInfo;
    const playerUserIDs = result?.timelineAnalysis?.playerUserIDs || {};
    const demoOffset = demoOffsetSec(result);

    let html = `<h4 style="color: ${color}">${escapeHtml(team)} — ${teamFrags} frags</h4>`;
    html += `<table class="team-status-table">`;
    /** @param {string} key @param {string} label */
    const sortTh = (key, label) => {
        const active = sort.key === key;
        const cls = `sortable${active ? (sort.dir === 1 ? ' sort-asc' : ' sort-desc') : ''}`;
        return `<th class="${cls}" data-key="${key}">${label}</th>`;
    };
    html += `<tr>${sortTh('name', 'Player')}${sortTh('frags', 'Frags')}` +
        `${sortTh('health', 'Health')}${sortTh('armor', 'Armor')}` +
        `<th>Weapons</th><th>View</th></tr>`;

    for (const p of players) {
        const hubLink = hubWatchLinkHtml(p.name, time, hubInfo, playerUserIDs, demoOffset);
        if (p.dead) {
            html += `<tr><td>${escapeHtml(p.name)}</td><td>${p.frags}</td><td>-</td><td>-</td><td>-</td><td>${hubLink}</td></tr>`;
            continue;
        }
        const hp = p.health || 0;
        const arm = p.armor || 0;
        const at = p.armorType || '';
        const armorClass = at ? `armor-${at}` : '';
        const armorStr = arm > 0 ? `<span class="${armorClass}">${arm} ${at.toUpperCase()}</span>` : '0';
        const weps = [];
        if (p.hasRL && p.hasLG) weps.push('RL+LG');
        else if (p.hasRL) weps.push('RL');
        else if (p.hasLG) weps.push('LG');
        if (p.hasQuad) weps.push('Quad');
        if (p.hasPent) weps.push('Pent');
        if (p.hasRing) weps.push('Ring');
        html += `<tr><td>${escapeHtml(p.name)}</td><td>${p.frags}</td><td>${hp}</td><td>${armorStr}</td>` +
            `<td>${weps.join(', ') || '-'}</td><td>${hubLink}</td></tr>`;
    }
    html += `<tr class="totals-row"><td>Total</td><td>${teamFrags}</td><td>${teamHealth}</td><td>${teamArmor}</td><td></td><td></td></tr>`;
    html += `</table>`;
    return html;
}

// ─── Hover read-outs ────────────────────────────────────────────────────────

// The diverging graphs' layout in CSS px: the renderer scales it by the
// device pixel ratio, the drop-dot hit test reads it as is (cursor
// coordinates are CSS px) — one source so the two cannot drift apart.
export const DIVERGING_GRAPH_LAYOUT = {
    H: 200,           // total canvas height
    AXIS_H: 20,       // bottom x-axis strip
    PAD: 4,           // inner padding
    DROP_STRIP_H: 8,  // reserved drop-dot strip at top/bottom of plot
};

// The span timelines' row and axis heights (CSS px); the powerup hit test
// assumes RC_ROW_H rows.
export const RC_ROW_H = 20;
export const RC_AXIS_H = 20;

// hitDropMark: the weapon-graph drop dot nearest the cursor (mx, my in
// canvas CSS px) within reach, or null. `hit` is what the last render drew:
// { startTime, endTime, W (CSS px), dropMarks }.
/**
 * @param {{ startTime: number, endTime: number, W: number, dropMarks: DropMark[] }} hit
 * @param {number} mx
 * @param {number} my
 * @returns {DropMark | null}
 */
export function hitDropMark(hit, mx, my) {
    const HIT_R = 6;   // hit radius (dot radius is 3)
    const HIT_DY = 8;  // vertical tolerance — generous so hovering near works
    const { H, AXIS_H, PAD, DROP_STRIP_H } = DIVERGING_GRAPH_LAYOUT;
    const graphH = H - AXIS_H;
    const topY = PAD + DROP_STRIP_H / 2;
    const bottomY = graphH - PAD - DROP_STRIP_H / 2;
    if (!hit.W || !hit.dropMarks.length) return null;
    const duration = hit.endTime - hit.startTime;
    if (duration <= 0) return null;
    let best = null;
    let bestDx = HIT_R + 1;
    for (const m of hit.dropMarks) {
        const x = ((m.time - hit.startTime) / duration) * hit.W;
        const y = m.isTop ? topY : bottomY;
        const dx = Math.abs(mx - x);
        const dy = Math.abs(my - y);
        if (dy <= HIT_DY && dx <= bestDx) {
            bestDx = dx;
            best = m;
        }
    }
    return best;
}

/** @param {DropMark} mark */
export function dropTooltipHtml(mark) {
    const d = mark.drop;
    const weapon = (d.weapon || '').toUpperCase();
    const locLine = d.loc ? `<div>Loc: ${escapeHtml(d.loc)}</div>` : '';
    return `<div><strong>${escapeHtml(d.player || '?')}</strong> dropped <strong>${weapon}</strong></div>
${locLine}<div>Time: ${formatDuration(d.time)}</div>`;
}

// hitPowerupSpan: the powerup run under the cursor — the row by y (the axis
// strip below the rows is ignored), the first span containing x — or null.
// `hit` is { startTime, endTime, W, rows } from the last render.
/**
 * @param {{ startTime: number, endTime: number, W: number, rows: SpanRow[] }} hit
 * @param {number} mx
 * @param {number} my
 * @returns {{ row: SpanRow, sp: StateSpan } | null}
 */
export function hitPowerupSpan(hit, mx, my) {
    if (!hit.W || !hit.rows.length) return null;
    const duration = hit.endTime - hit.startTime;
    if (duration <= 0) return null;
    const rowIdx = Math.floor(my / RC_ROW_H);
    if (rowIdx < 0 || rowIdx >= hit.rows.length) return null;
    const row = hit.rows[rowIdx];
    let found = null;
    for (const sp of row.spans) {
        const x1 = ((sp.start - hit.startTime) / duration) * hit.W;
        const x2 = ((sp.end - hit.startTime) / duration) * hit.W;
        if (mx >= x1 && mx <= x2) { found = sp; break; }
    }
    if (!found || !found.event) return null;
    return { row, sp: found };
}

/** @param {{ row: SpanRow, sp: StateSpan }} hit */
export function powerupTooltipHtml({ row, sp }) {
    // hitPowerupSpan only answers spans that carry their run.
    const ev = /** @type {TimedEvent} */ (sp.event);
    const player = escapeHtml(ev.playerName || 'Unknown');
    const team = ev.team ? `<div>Team: ${escapeHtml(ev.team)}</div>` : '';
    const dur = (ev.duration != null) ? `${Math.round(ev.duration)}s` : '?';
    return `<div><strong>${escapeHtml(row.name)}</strong> · <strong>${player}</strong></div>
${team}<div>Frags: ${ev.frags || 0}</div>
<div>Duration: ${dur}</div>`;
}
