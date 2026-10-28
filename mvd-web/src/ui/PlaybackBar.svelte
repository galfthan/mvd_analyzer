<script>
    // The unified playback bar above the Timeline, Chat, Map, Key Moments and
    // Player tabs (TABS_WITH_TIMELINE): the speed buttons, the time, a scrub
    // caret and a range select. src/lib/playback-bar.js has its arithmetic.
    //
    // The speed buttons run the page's own playback (src/state/playback.js)
    // or, while the Player engine is the clock (the Player tab), the engine.
    // The range a drag selects is the shared segment (src/state/timeline.js)
    // — the Timeline tab's view window, which that tab's zoom and pan set
    // too; while the drag is in progress the bar shows its own preview and
    // the segment is set on release. A click clears it and seeks there; a
    // double-click clears it.
    //
    // It follows the clock on every tick (two style writes and a text). The
    // Map tab moves the root element into its panel in fullscreen and back,
    // so it is always rendered and hidden by style only.
    import { onMount } from 'svelte';
    import { clock, demo, activeTab, segment, playback, playAtSpeed, TABS_WITH_TIMELINE } from '../state/index.js';
    import { engine } from '../player/index.js';
    import { formatDuration } from '../lib/result.js';
    import { matchDurationSec } from '../lib/timeline.js';
    import {
        barAxisTicks, barPct, barTime, dragSpan, isClick, overlayBox, segmentLabel, speedButtons,
    } from '../lib/playback-bar.js';

    const TITLES = {
        'tl-rev': 'Reverse playback (1x)',
        'tl-slow': 'Play 0.2x (slow motion)',
        'tl-play-pause': 'Play/Pause',
        'tl-5x': 'Play 5x speed',
    };

    let bar;
    let result = $state.raw(null);
    let tab = $state('search');
    let duration = $state(0);
    let time = $state('0:00');
    let left = $state(null);
    let buttons = $state.raw(speedButtons({ drives: false, engine: {}, playing: false, speed: 1 }));

    // The overlay keeps its last left / width while hidden, as the bar always
    // has; it has no style at all until the first segment change.
    let ovDisplay = $state(null);
    let ovLeft = $state(null);
    let ovWidth = $state(null);
    let label = $state('');

    // A range drag (dragStart, the preview it shows) or a caret drag.
    let dragging = false;
    let dragStart = 0;
    let caretDragging = false;

    const shown = $derived(!!result && TABS_WITH_TIMELINE.includes(tab));
    const ticks = $derived(barAxisTicks(duration));

    function showSegment(seg) {
        if (!seg) {
            ovDisplay = 'none';
            label = '';
            return;
        }
        const box = overlayBox(seg, duration);
        if (box) {
            ovDisplay = 'block';
            ovLeft = box.left;
            ovWidth = box.width;
        }
        label = segmentLabel(seg);
    }

    function follow(t) {
        const pct = barPct(t, duration);
        if (pct !== null) left = `${pct}%`;
        time = formatDuration(Math.max(0, t));
    }

    function refreshButtons() {
        const p = playback.get();
        buttons = speedButtons({ drives: engine.drives(), engine: engine.status.get(), playing: p.playing, speed: p.speed });
    }

    function press(speed) {
        if (engine.drives()) engine.setSpeed(speed);
        else playAtSpeed(speed);
    }

    const timeAt = (e) => barTime(e.clientX, bar.getBoundingClientRect(), duration);

    function barDown(e) {
        const t = timeAt(e);
        if (t === null) return;
        dragging = true;
        dragStart = t;
        showSegment(null);
        e.preventDefault();
    }

    function caretDown(e) {
        caretDragging = true;
        e.preventDefault();
        e.stopPropagation();
    }

    function move(e) {
        if (caretDragging) {
            const t = timeAt(e);
            if (t !== null) clock.seek(t);
            return;
        }
        if (!dragging) return;
        const t = timeAt(e);
        if (t === null) return;
        const span = dragSpan(dragStart, t);
        if (!isClick(span)) showSegment(span);
    }

    function up(e) {
        if (caretDragging) {
            caretDragging = false;
            return;
        }
        if (!dragging) return;
        dragging = false;
        const t = timeAt(e);
        if (t === null) return;
        const span = dragSpan(dragStart, t);
        if (isClick(span)) {
            segment.set(null);
            clock.seek(t);
        } else {
            segment.set(span);
        }
    }

    onMount(() => {
        let first = true;
        const offs = [
            demo.subscribe((h) => {
                result = h?.result ?? null;
                duration = result ? matchDurationSec(result) : 0;
                follow(clock.t);
            }),
            activeTab.subscribe((t) => {
                tab = t;
                refreshButtons();
            }),
            clock.subscribe(({ t }) => follow(t)),
            segment.subscribe((seg) => {
                if (first) return;
                if (!dragging) showSegment(seg);
            }),
            playback.subscribe(refreshButtons),
            engine.status.subscribe(refreshButtons),
        ];
        first = false;
        return () => offs.forEach((off) => off());
    });
</script>

<svelte:document onmousemove={move} onmouseup={up} />

<div id="unified-timeline" class="panel timeline-nav-panel" style:display={shown ? null : 'none'}>
    <div class="timeline-nav-row">
        <div class="timeline-playback-controls">
            {#each buttons as b (b.id)}
                <button id={b.id} title={TITLES[b.id]} class:active={b.active} disabled={b.disabled} onclick={() => press(b.speed)}>{b.text}</button>
            {/each}
        </div>
        <span id="tl-current-time" class="timeline-nav-time">{time}</span>
        <div class="timeline-nav-bar-wrapper" id="tl-bar-wrapper">
            <!-- svelte-ignore a11y_no_static_element_interactions: a pointer scrubber; the speed buttons and the caret are the controls -->
            <div class="timeline-nav-bar" id="tl-bar" bind:this={bar} onmousedown={barDown} ondblclick={() => segment.set(null)}>
                <div class="timeline-nav-track" id="tl-track"></div>
                <div class="timeline-selection-overlay" id="tl-selection-overlay"
                    style:display={ovDisplay} style:left={ovLeft} style:width={ovWidth}></div>
                <div class="timeline-nav-cursor" id="tl-cursor" style:left={left}></div>
            </div>
            <!-- svelte-ignore a11y_no_static_element_interactions: dragged with the pointer -->
            <div class="timeline-nav-caret" id="tl-caret" title="Drag to set current time" style:left={left} onmousedown={caretDown}>▲</div>
            <div class="timeline-nav-axis" id="tl-axis">
                {#each ticks as tick, i (i)}<span style:left="{tick.pct}%">{tick.label}</span>{/each}
            </div>
        </div>
        <span id="tl-segment-label" class="timeline-nav-segment">{label}</span>
    </div>
</div>
