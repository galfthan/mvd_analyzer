// playback-bar.js — the unified playback bar's DOM-free parts
// (src/ui/PlaybackBar.svelte): the minute marks, the playhead's position, a
// pointer x as a time, whether a press grabs the playhead or starts a
// selection, what a drag selects and the transport's state (play / pause
// and the speed). Times are match seconds.

import { formatDuration } from './result.js';

/** @typedef {{ start: number, end: number }} Span  match seconds */

// A press that moves less than this (px) is a click: it clears the segment
// and seeks. Pixels, not match time, so it feels the same on every demo.
export const DRAG_MIN_PX = 4;
// A press this close (px) to the playhead grabs it instead of selecting.
export const KNOB_GRAB_PX = 10;

// The speeds the dropdown offers (× 1). -1 plays backwards — the page's
// own playback only; the Player engine cannot.
export const SPEEDS = [-1, 0.2, 0.5, 1, 2, 5];

// barAxisTicks: 4–10 evenly spaced marks, about one a minute.
/**
 * @param {number} duration
 * @returns {Array<{ pct: number, label: string }>}
 */
export function barAxisTicks(duration) {
    if (!(duration > 0)) return [];
    const n = Math.min(10, Math.max(4, Math.floor(duration / 60)));
    const out = [];
    for (let i = 0; i <= n; i++) {
        out.push({ pct: (i / n) * 100, label: formatDuration((duration / n) * i) });
    }
    return out;
}

// barPct: the cursor's position (percent), null without a duration.
/**
 * @param {number} t
 * @param {number} duration
 */
export function barPct(t, duration) {
    if (!(duration > 0)) return null;
    return Math.max(0, Math.min(100, (t / duration) * 100));
}

// barTime: a pointer's clientX over the bar (its rect) as a time, null when
// the bar has no width.
/**
 * @param {number} clientX
 * @param {{ left: number, width: number }} rect
 * @param {number} duration
 */
export function barTime(clientX, rect, duration) {
    if (!(rect.width > 0)) return null;
    return Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)) * duration;
}

// dragSpan: the span between the drag's start and `t`, ordered.
/**
 * @param {number} t0
 * @param {number} t
 * @returns {Span}
 */
export function dragSpan(t0, t) {
    return { start: Math.min(t0, t), end: Math.max(t0, t) };
}

/**
 * @param {number} x0  where the press started (clientX)
 * @param {number} x
 */
export const isDrag = (x0, x) => Math.abs(x - x0) >= DRAG_MIN_PX;

// nearKnob: a pointer this close to the playhead at `t` grabs it.
/**
 * @param {number} clientX
 * @param {{ left: number, width: number }} rect
 * @param {number} t
 * @param {number} duration
 */
export function nearKnob(clientX, rect, t, duration) {
    const pct = barPct(t, duration);
    if (pct === null || !(rect.width > 0)) return false;
    return Math.abs(clientX - (rect.left + (pct / 100) * rect.width)) <= KNOB_GRAB_PX;
}

// overlayBox: the selection overlay's left / width (percent), null without a
// duration.
/**
 * @param {Span} seg
 * @param {number} duration
 */
export function overlayBox(seg, duration) {
    if (!(duration > 0)) return null;
    const a = (seg.start / duration) * 100;
    return { left: `${a}%`, width: `${(seg.end / duration) * 100 - a}%` };
}

/** @param {Span | null | undefined} seg */
export function segmentLabel(seg) {
    return seg ? `${formatDuration(seg.start)} – ${formatDuration(seg.end)}` : '';
}

// transport: whether it plays, the speed it runs (or will run) at, and the
// speeds on offer. While the Player engine is the clock (`drives`) its
// status rules — its `rate` is the speed play runs at, kept while paused —
// and it cannot play backwards; otherwise the page's own playback
// (src/state/playback.js) does.
/**
 * @param {{ drives: boolean, engine: { playing: boolean, rate: number }, playing: boolean, speed: number }} state
 */
export function transport({ drives, engine, playing, speed }) {
    return {
        playing: drives ? engine.playing : playing,
        speed: drives ? engine.rate / 100 : speed,
        speeds: SPEEDS.map((v) => ({ value: v, label: `${v}×`, disabled: drives && v < 0 })),
    };
}

// crossedEnd: playback ran from inside the segment to its end or past it —
// where it stops (a selection plays like a clip). Callers pass playback
// steps only (a seek is not one): a long step — a frame hitch, a hidden
// browser tab — still stops.
/**
 * @param {Span | null | undefined} seg
 * @param {number | null} prev  the clock before this step
 * @param {number} t
 */
export function crossedEnd(seg, prev, t) {
    if (!seg || prev == null) return false;
    return prev >= seg.start && prev < seg.end && t >= seg.end;
}

// replayFrom: play pressed forward while paused at the segment's end (where
// playback stopped) starts over at its start; null plays on from where it
// is — anywhere else, or backwards.
/**
 * @param {Span | null | undefined} seg
 * @param {number} t
 * @param {number} speed
 */
export function replayFrom(seg, t, speed) {
    return seg && speed > 0 && Math.abs(t - seg.end) <= 0.5 ? seg.start : null;
}

// The i / o keys: the selection's start or end at the playhead, keeping
// the other edge when it is still on its side (else `len` seconds away).
const MARK_LEN_S = 10;
/**
 * @param {Span | null | undefined} seg
 * @param {number} t
 * @param {number} duration
 * @returns {Span}
 */
export function markIn(seg, t, duration) {
    return { start: t, end: seg && seg.end > t ? seg.end : Math.min(duration, t + MARK_LEN_S) };
}
/**
 * @param {Span | null | undefined} seg
 * @param {number} t
 * @returns {Span}
 */
export function markOut(seg, t) {
    return { start: seg && seg.start < t ? seg.start : Math.max(0, t - MARK_LEN_S), end: t };
}
