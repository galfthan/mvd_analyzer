<script>
    // The unified playback bar above the Timeline, Chat, Map, Key Moments and
    // Player tabs (TABS_WITH_TIMELINE), laid out like a video player's: a
    // thin scrubber across the top — the played part, a round playhead,
    // minute marks, the selected segment, and the time under the pointer
    // floating above it — and under it play / pause and pills for the
    // speed, the time, the selection and "+ clip". src/lib/playback-bar.js
    // has its arithmetic.
    //
    // On the scrubber a press near the playhead grabs it (drag to scrub);
    // anywhere else a press that moves selects a range — the shared segment
    // (src/state/timeline.js), the Timeline tab's view window too, set on
    // release (the bar previews it meanwhile) — and one that does not move
    // clears the segment and seeks there. A double-click clears it. A new
    // selection seeks to its start, and playback stops at its end (a
    // selection plays like a clip; play there starts it over). Where a
    // POV is at hand (src/player/selection-clip.js) a "+ clip" pill adds it to
    // the playlist.
    //
    // Play / pause and the speed run the page's own playback
    // (src/state/playback.js) or, while the Player engine is the clock (the
    // Player tab), the engine. Space toggles play / pause when no field or
    // button has the keys.
    //
    // It follows the clock on every tick (a few style writes and a text). A
    // tab's fullscreen (src/app/fullscreen.js) moves the root element into
    // the tab's panel and back, so it is always rendered and hidden by style
    // only; there it ends with the button out of fullscreen.
    import { onMount } from 'svelte';
    import { fade } from 'svelte/transition';
    import Play from '@lucide/svelte/icons/play';
    import Pause from '@lucide/svelte/icons/pause';
    import Minimize from '@lucide/svelte/icons/minimize';
    import { fullscreenTab, toggleFullscreen } from '../app/fullscreen.js';
    import {
        clock, demo, activeTab, isWatchTab, segment, playback, setPlaybackSpeed, togglePlayback, TABS_WITH_TIMELINE,
    } from '../state/index.js';
    import Plus from '@lucide/svelte/icons/plus';
    import X from '@lucide/svelte/icons/x';
    import { engine } from '../player/index.js';
    import { selectionPov, addSelectionClip } from '../player/selection-clip.js';
    import { createPlayerKeys } from '../player/player-keys.js';
    import { watchNext } from '../player/follow.js';
    import { formatDuration } from '../lib/result.js';
    import { matchDurationSec } from '../lib/timeline.js';
    import {
        barAxisTicks, barPct, barTime, dragSpan, isDrag, nearKnob, overlayBox, segmentLabel, formatTenths, transport, replayFrom,
    } from '../lib/playback-bar.js';

    /** @type {HTMLDivElement} */
    let bar;
    let result = $state.raw(/** @type {import('../lib/result.js').Result | null} */ (null));
    let tab = $state('search');
    let duration = $state(0);
    let time = $state('0:00');
    let pct = $state(0);
    // `engine` is not read while `drives` is false.
    let tp = $state.raw(transport({ drives: false, engine: /** @type {any} */ ({}), playing: false, speed: 1 }));

    // The segment as shown: the store's, or a drag's preview.
    let shownSeg = $state.raw(/** @type {{ start: number, end: number } | null} */ (null));
    // The pointer over the scrubber: its x (px into the bar) and time.
    let hover = $state.raw(/** @type {{ x: number, t: number, at: number } | null} */ (null));
    const READOUT_HALF = 30;   // half the hover readout's width, px
    let overKnob = $state(false);

    /** @type {null | { mode: 'knob' | 'press' | 'select', x0: number, t0: number }} */
    let drag = null;
    let grabbing = $state(false);
    let selecting = $state(false);

    const shown = $derived(!!result && TABS_WITH_TIMELINE.includes(tab));
    const marks = $derived(barAxisTicks(duration).slice(1, -1));
    const box = $derived(shownSeg ? overlayBox(shownSeg, duration) : null);
    const label = $derived(segmentLabel(shownSeg));
    const total = $derived(formatDuration(duration));

    /** @param {number} t */
    function follow(t) {
        const p = barPct(t, duration);
        if (p !== null) pct = p;
        time = formatTenths(t);
    }

    function refresh() {
        const p = playback.get();
        tp = transport({ drives: engine.drives(), engine: engine.status.get(), playing: p.playing, speed: p.speed });
    }

    function togglePlay() {
        // The Player tab with a Player pane plays the engine; until it is up
        // there is nothing to play (the page's own loop would fight it).
        if (engine.loading()) return;
        if (!tp.playing) {
            const from = replayFrom(segment.get(), clock.t, tp.speed);
            if (from != null) clock.seek(from);
        }
        if (engine.drives()) engine.togglePlay();
        else togglePlayback();
    }

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function pickSpeed(e) {
        const v = Number(e.currentTarget.value);
        if (engine.drives()) engine.setRate(v);
        else setPlaybackSpeed(v);
        e.currentTarget.blur();
    }

    /** @param {PointerEvent} e */
    const timeAt = (e) => barTime(e.clientX, bar.getBoundingClientRect(), duration);

    /** @param {PointerEvent} e */
    function track(e) {
        const r = bar.getBoundingClientRect();
        const t = barTime(e.clientX, r, duration);
        // The readout stays whole within the bar near its ends.
        hover = t === null ? null : { x: e.clientX - r.left, t, at: Math.min(Math.max(e.clientX - r.left, READOUT_HALF), r.width - READOUT_HALF) };
        overKnob = nearKnob(e.clientX, r, clock.t, duration);
    }

    /** @param {PointerEvent} e */
    function down(e) {
        if (e.button !== 0) return;
        const t = timeAt(e);
        if (t === null) return;
        bar.setPointerCapture(e.pointerId);
        const knob = nearKnob(e.clientX, bar.getBoundingClientRect(), clock.t, duration);
        drag = { mode: knob ? 'knob' : 'press', x0: e.clientX, t0: t };
        grabbing = knob;
        e.preventDefault();
    }

    /** @param {PointerEvent} e */
    function move(e) {
        track(e);
        if (!drag) return;
        const t = timeAt(e);
        if (t === null) return;
        if (drag.mode === 'knob') {
            clock.seek(t);
        } else if (drag.mode === 'select' || isDrag(drag.x0, e.clientX)) {
            drag.mode = 'select';
            selecting = true;
            shownSeg = dragSpan(drag.t0, t);
        }
    }

    /** @param {PointerEvent} e */
    function up(e) {
        if (!drag) return;
        const { mode, t0 } = drag;
        drag = null;
        grabbing = false;
        selecting = false;
        const t = timeAt(e);
        if (mode === 'press') {
            segment.set(null);
            clock.seek(t0);
        } else if (mode === 'select' && t !== null) {
            const span = dragSpan(t0, t);
            segment.set(span);
            clock.seek(span.start);
        }
    }

    function cancel() {
        drag = null;
        grabbing = false;
        selecting = false;
        shownSeg = segment.get();
    }

    // The player keys (src/player/player-keys.js): Space plays / pauses,
    // ← / → step, ↑ / ↓ the previous / next player on the Map and Player
    // tabs (elsewhere they scroll).
    const key = createPlayerKeys({
        enabled: () => shown,
        toggle: togglePlay,
        get step() {
            return isWatchTab(activeTab.get()) ? watchNext : null;
        },
        seek: (by) => clock.seek(Math.max(0, clock.t + by)),
    });

    // A click or Space on the game view is this play / pause too.
    onMount(() => {
        engine.screenToggle = togglePlay;
        return () => {
            engine.screenToggle = () => engine.togglePlay();
        };
    });

    onMount(() => {
        const offs = [
            demo.subscribe((h) => {
                result = h?.result ?? null;
                duration = result ? matchDurationSec(result) : 0;
                follow(clock.t);
            }),
            activeTab.subscribe((t) => {
                tab = t;
                refresh();
            }),
            clock.subscribe(({ t }) => follow(t)),
            segment.subscribe((seg) => {
                if (!drag) shownSeg = seg;
            }),
            playback.subscribe(refresh),
            engine.status.subscribe(refresh),
        ];
        return () => offs.forEach((off) => off());
    });
</script>

<svelte:window onkeydown={key} />

<div id="unified-timeline" class="panel pb" style:display={shown ? null : 'none'}>
    <!-- svelte-ignore a11y_no_static_element_interactions: a pointer scrubber; play / pause and the speed are the controls -->
    <div class="pb-scrub" class:knob-hover={overKnob} class:grabbing bind:this={bar}
        onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={cancel}
        onpointerleave={() => { if (!drag) { hover = null; overKnob = false; } }}
        ondblclick={() => segment.set(null)}>
        <div class="pb-track">
            <div class="pb-played" style:width="{pct}%"></div>
            {#each marks as m, i (i)}<span class="pb-mark" style:left="{m.pct}%"></span>{/each}
            {#if box}<div class="pb-sel" style:left={box.left} style:width={box.width}></div>{/if}
        </div>
        <div class="pb-knob" style:left="{pct}%"></div>
        {#if hover}
            <div class="pb-hover-line" style:left="{hover.x}px"></div>
            <div class="pb-readout" style:left="{hover.at}px" transition:fade={{ duration: 100 }}>{formatTenths(hover.t)}</div>
        {/if}
    </div>
    <div class="pb-row">
        <button class="pb-play" title={tp.playing ? 'Pause (space)' : 'Play (space)'} aria-label={tp.playing ? 'Pause' : 'Play'} onclick={togglePlay}>
            {#if tp.playing}<Pause size={16} fill="currentColor" />{:else}<Play size={16} fill="currentColor" />{/if}
        </button>
        <select class="pb-pill pb-speed" title="Playback speed" value={tp.speed} onchange={pickSpeed}>
            {#each tp.speeds as s (s.value)}
                <option value={s.value} disabled={s.disabled}>{s.label}</option>
            {/each}
        </select>
        <span class="pb-pill pb-time"><span class="now">{time}</span> / {total}</span>
        {#if label}
            <span class="pb-pill pb-seg" title="The selected segment: playback stops at its end (double-click the bar to clear)" transition:fade={{ duration: 120 }}>
                {label}
                <button class="pb-x" title="Clear the selection" aria-label="Clear the selection" onclick={() => segment.set(null)}><X size={12} /></button>
            </span>
            {#if $selectionPov && !selecting}
                <!-- The selection from the current POV, as a clip -->
                <button class="pb-pill pb-clip" title="Add the selection to the playlist, watched from {$selectionPov.who || 'Auto'} (key: c; i / o set its start / end at the playhead)"
                    onclick={addSelectionClip} transition:fade={{ duration: 120 }}>
                    <Plus size={12} /> clip · {$selectionPov.who || 'Auto'}
                </button>
            {/if}
        {/if}
        {#if $fullscreenTab}
            <button class="pb-exit-fs" title="Leave fullscreen (Esc or f)" aria-label="Leave fullscreen" onclick={() => toggleFullscreen()}>
                <Minimize size={16} /> Exit
            </button>
        {/if}
    </div>
</div>

<style>
    .pb {
        display: flex;
        flex-direction: column;
        gap: 2px;
        margin-bottom: 10px;
        padding: 2px 12px 5px;
    }
    .pb-row {
        display: flex;
        align-items: center;
        gap: 8px;
        height: 26px;
    }
    .pb-play {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 28px;
        height: 26px;
        padding: 0;
        background: none;
        border: none;
        border-radius: 4px;
        color: var(--accent-cyan);
        cursor: pointer;
    }
    .pb-play:hover {
        background: rgba(0, 217, 255, 0.15);
    }
    .pb-pill {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        height: 22px;
        padding: 0 10px;
        border-radius: 11px;
        border: none;
        background: rgba(255, 255, 255, 0.07);
        color: #bcd;
        font-size: 12px;
        white-space: nowrap;
        box-sizing: border-box;
    }
    .pb-speed {
        padding: 0 4px 0 8px;
        cursor: pointer;
        color-scheme: dark;
    }
    .pb-time {
        font-family: monospace;
        color: #889;
    }
    .pb-time .now {
        color: var(--accent-cyan);
    }
    .pb-seg {
        font-family: monospace;
        color: #ffd250;
        background: rgba(255, 210, 80, 0.12);
        padding-right: 4px;
    }
    .pb-x {
        display: inline-flex;
        padding: 1px;
        background: none;
        border: none;
        border-radius: 50%;
        color: inherit;
        cursor: pointer;
    }
    .pb-x:hover {
        background: rgba(255, 210, 80, 0.25);
    }
    .pb-clip {
        border: 1px solid #ffd250;
        background: #2a2410;
        color: #ffd250;
        cursor: pointer;
    }
    .pb-clip:hover {
        background: #ffd250;
        color: #1a1505;
    }
    .pb-scrub {
        position: relative;
        height: 18px;
        cursor: pointer;
        touch-action: none;
        user-select: none;
    }
    .pb-scrub.knob-hover {
        cursor: grab;
    }
    .pb-scrub.grabbing {
        cursor: grabbing;
    }
    .pb-track {
        position: absolute;
        left: 0;
        right: 0;
        top: 50%;
        height: 4px;
        transform: translateY(-50%);
        background: #1f2e4d;
        border-radius: 2px;
        transition: height 0.12s ease;
    }
    .pb-scrub:hover .pb-track,
    .pb-scrub.grabbing .pb-track {
        height: 7px;
    }
    .pb-played {
        position: absolute;
        inset: 0 auto 0 0;
        background: rgba(0, 217, 255, 0.55);
        border-radius: 2px;
    }
    .pb-mark {
        position: absolute;
        top: 0;
        bottom: 0;
        width: 1px;
        background: rgba(255, 255, 255, 0.12);
    }
    .pb-sel {
        position: absolute;
        top: -3px;
        bottom: -3px;
        background: rgba(255, 210, 80, 0.28);
        border-left: 2px solid #ffd250;
        border-right: 2px solid #ffd250;
        border-radius: 2px;
        box-sizing: border-box;
    }
    .pb-knob {
        position: absolute;
        top: 50%;
        width: 12px;
        height: 12px;
        border-radius: 50%;
        background: var(--accent-cyan);
        box-shadow: 0 0 6px rgba(0, 217, 255, 0.6);
        transform: translate(-50%, -50%);
        transition: width 0.12s ease, height 0.12s ease;
        pointer-events: none;
    }
    .pb-scrub.knob-hover .pb-knob,
    .pb-scrub.grabbing .pb-knob {
        width: 16px;
        height: 16px;
    }
    .pb-hover-line {
        position: absolute;
        top: 3px;
        bottom: 3px;
        width: 1px;
        background: rgba(255, 255, 255, 0.35);
        pointer-events: none;
    }
    .pb-readout {
        position: absolute;
        bottom: calc(100% + 2px);
        transform: translateX(-50%);
        padding: 1px 6px;
        border-radius: 3px;
        background: #0b1222;
        border: 1px solid rgba(0, 217, 255, 0.35);
        color: #dde;
        font-family: monospace;
        font-size: 11px;
        white-space: nowrap;
        pointer-events: none;
        z-index: 30;
    }
    .pb-exit-fs {
        margin-left: auto;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        height: 24px;
        padding: 0 8px;
        background: none;
        border: 1px solid rgba(0, 217, 255, 0.3);
        border-radius: 3px;
        color: #7a8ba8;
        font-size: 0.85rem;
        cursor: pointer;
    }
    .pb-exit-fs:hover {
        color: var(--accent-cyan);
        border-color: var(--accent-cyan);
    }
</style>
