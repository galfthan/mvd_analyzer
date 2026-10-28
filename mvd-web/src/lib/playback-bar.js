// playback-bar.js — the unified playback bar's DOM-free parts
// (src/ui/PlaybackBar.svelte): the axis, the cursor position, a pointer x as
// a time, what a drag across the bar selects and the speed buttons' state.
// Times are match seconds.

import { formatDuration } from './result.js';

// A drag shorter than this is a click: it clears the segment and seeks.
export const CLICK_MAX_S = 2;

// The speed buttons, in bar order, and their labels when not running.
export const SPEED_BUTTONS = [
    { id: 'tl-rev', speed: -1, label: '-1x' },
    { id: 'tl-slow', speed: 0.2, label: '0.2x' },
    { id: 'tl-play-pause', speed: 1, label: '1x' },
    { id: 'tl-5x', speed: 5, label: '5x' },
];

// barAxisTicks: 4–10 evenly spaced labels, about one a minute.
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
export function barPct(t, duration) {
    if (!(duration > 0)) return null;
    return Math.max(0, Math.min(100, (t / duration) * 100));
}

// barTime: a pointer's clientX over the bar (its rect) as a time, null when
// the bar has no width.
export function barTime(clientX, rect, duration) {
    if (!(rect.width > 0)) return null;
    return Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)) * duration;
}

// dragSpan: the span between the drag's start and `t`, ordered.
export function dragSpan(t0, t) {
    return { start: Math.min(t0, t), end: Math.max(t0, t) };
}

export const isClick = (span) => span.end - span.start <= CLICK_MAX_S;

// overlayBox: the selection overlay's left / width (percent), null without a
// duration.
export function overlayBox(seg, duration) {
    if (!(duration > 0)) return null;
    const a = (seg.start / duration) * 100;
    return { left: `${a}%`, width: `${(seg.end / duration) * 100 - a}%` };
}

export function segmentLabel(seg) {
    return seg ? `${formatDuration(seg.start)} – ${formatDuration(seg.end)}` : '';
}

// speedButtons: each button's text, highlight and disabled state. While the
// Player engine is the clock (`drives`) its status rules and it cannot play
// backwards; otherwise the page's own playback (src/state/playback.js) does.
// The running speed shows a pause glyph.
export function speedButtons({ drives, engine, playing, speed }) {
    const on = drives ? engine.playing : playing;
    const current = drives ? engine.speed / 100 : speed;
    return SPEED_BUTTONS.map((b) => {
        const active = on && current === b.speed;
        return { ...b, active, text: active ? '⏸' : b.label, disabled: drives && b.speed < 0 };
    });
}
