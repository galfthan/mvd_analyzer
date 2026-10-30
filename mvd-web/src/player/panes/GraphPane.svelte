<script>
    // One of the Timeline tab's graphs as a pane, over the selected segment
    // (the whole match without one — the default), a window that rolls
    // with the playhead (1, 3 or 10 minutes, three quarters behind it) or
    // the whole match: the score, team weapons, team health / armor,
    // per-player weapons, powerups and region control. The series and the
    // renderers are the Timeline tab's (src/lib/timeline.js,
    // src/ui/timeline-canvas.js). A line marks the playhead; click seeks.
    // Given a height (the pane's resize handle) the graph grows to fill it;
    // its natural size is the least it takes.
    import { clock, demo, teams, regionControl, segment } from '../../state/index.js';
    import {
        timelineEvents, matchDurationSec, paneWindow, PANE_WINDOWS, SELECTION_WINDOW, indicatorPct, rosterTeamOf, playersByTeam,
        DIVERGING_GRAPH_LAYOUT, RC_ROW_H, RC_AXIS_H,
        prepScoreData, prepWeaponsData, computeBackpackDrops, prepHealthArmorData, prepPowerupRowsData,
        prepRegionControlData, prepPlayerWeaponSpans, playerDropMarks, WEAPON_SPAN_COLORS, teamStrongColor,
    } from '../../lib/timeline.js';
    import { regionSides } from '../../lib/regions.js';
    import { renderDivergingGraph, renderSpansTimeline } from '../../ui/timeline-canvas.js';
    import { paneLayout, paneOpt, setPaneOpt } from '../layout.js';
    import { onFrame } from './tick.js';

    /** @typedef {'score' | 'team-weapons' | 'team-ha' | 'weapons-pp' | 'powerups' | 'regions'} Kind */

    /** @type {{ kind: Kind }} */
    let { kind } = $props();

    let spans = $derived(kind === 'weapons-pp' || kind === 'powerups' || kind === 'regions');

    let width = $derived(paneOpt($paneLayout, kind, 'window', SELECTION_WINDOW));

    /** @type {Record<string, any> | null} the columnar bucket view */
    let view = $state.raw(null);
    $effect(() => {
        const handle = $demo;
        view = handle?.result?.timelineAnalysis?.bucketView || null;
        handle?.ready('buckets').then(() => {
            if ($demo === handle) view = handle.result.timelineAnalysis?.bucketView || null;
        }, () => {});
    });
    let ev = $derived(timelineEvents($demo?.result || null));

    /** @type {HTMLCanvasElement} */
    let canvas;
    /** @type {HTMLDivElement} */
    let head;

    // The height the pane gives the graph when the viewer sized it (the
    // pane body less the head), else null: natural size.
    function availH() {
        const body = canvas?.closest('.player-pane-body');
        if (!body || !body.classList.contains('sized')) return null;
        return body.clientHeight - head.offsetHeight - 6;
    }
    /** @type {HTMLDivElement | undefined} */
    let labels = $state();
    let range = $state(/** @type {[number, number]} */ ([0, 0]));
    let cursor = $state(/** @type {number | null} */ (null));
    let hint = $state('');
    let legend = $state.raw(/** @type {Array<{ text: string, color: string }>} */ ([]));
    let lastKey = '';
    // The canvas width and the sized height, read on resize only: reading
    // them per frame forces a layout after the frame's DOM writes.
    let boxW = 0;
    /** @type {number | null} */
    let avail = null;
    function measure() {
        boxW = canvas?.parentElement?.clientWidth || 0;
        avail = availH();
        lastKey = '';
    }

    // draw renders when the window or the data moved — by a canvas pixel
    // at least, so a rolling window does not redraw every frame; the
    // playhead line is an element over the canvas, so a paused whole-match
    // graph only moves it.
    /** @param {number} t */
    function draw(t) {
        const result = $demo?.result;
        const { order, colors, perPlayer } = $teams;
        const [start, end] = paneWindow(t, width, matchDurationSec(result), segment.get());
        range = [start, end];
        cursor = indicatorPct(t, start, end);
        const px = (end - start) / Math.max(1, boxW);
        const key = `${Math.floor(start / px)}|${Math.floor(end / px)}|${boxW}|${avail}`;
        if (key === lastKey) return;
        lastKey = key;
        hint = '';
        if (!result || order.length < 2) { hint = 'Needs a demo with two sides or more.'; return; }
        const twoSided = !perPlayer;
        if (kind === 'score') {
            if (!twoSided) { hint = 'A field of players has no two sides to score.'; return; }
            const { points, max } = prepScoreData(ev.fragEvents, start, end, order, colors);
            legend = [{ text: `${order[0]} leading ↑`, color: colors[0] }, { text: `${order[1]} leading ↓`, color: colors[1] }, { text: `±${max}`, color: '#888' }];
            renderDivergingGraph(canvas, { startTime: start, endTime: end, dataPoints: points, maxValue: max, height: grown(avail) });
        } else if (kind === 'team-weapons' || kind === 'team-ha') {
            if (!twoSided) { hint = 'A field of players has no two sides — try Weapons per player.'; return; }
            if (!view) { hint = 'Loading…'; lastKey = ''; return; }
            const { points, max } = kind === 'team-weapons' ? prepWeaponsData(view, start, end, order) : prepHealthArmorData(view, start, end, order);
            const dropMarks = kind === 'team-weapons' ? computeBackpackDrops(ev.backpacks, start, end, order) : undefined;
            legend = [{ text: `${order[0]} ↑`, color: colors[0] }, { text: `${order[1]} ↓`, color: colors[1] }, { text: `max ${max}`, color: '#888' }];
            renderDivergingGraph(canvas, { startTime: start, endTime: end, dataPoints: points, maxValue: max, dropMarks, height: grown(avail) });
        } else if (kind === 'weapons-pp') {
            const v = view;
            if (!v) { hint = 'Loading…'; lastKey = ''; return; }
            /** @type {Array<{ name: string, color: string, spans: import('../../lib/timeline.js').StateSpan[] }>} */
            const rows = [];
            /** @type {string[]} */
            const names = [];
            playersByTeam(v, order, rosterTeamOf(result), perPlayer).forEach((group, ti) => {
                for (const name of group) {
                    rows.push({ name, color: colors[ti] || '#ccc', spans: prepPlayerWeaponSpans(v, v.players[name], start, end) });
                    names.push(name);
                }
            });
            legend = [{ text: 'RL', color: WEAPON_SPAN_COLORS.rl }, { text: 'LG', color: WEAPON_SPAN_COLORS.lg }, { text: 'both', color: WEAPON_SPAN_COLORS.rllg }];
            renderSpansTimeline(canvas, labels, {
                startTime: start, endTime: end, rows, stateColors: WEAPON_SPAN_COLORS,
                dropMarks: playerDropMarks(ev.backpacks, names, start, end), rowH: rowH(avail, rows.length),
            });
        } else if (kind === 'powerups') {
            if (!twoSided) { hint = 'A field of players has no two sides to colour.'; return; }
            const data = prepPowerupRowsData(ev.powerupEvents, start, end, order);
            if (!data || !data.rows.some((r) => r.spans.length > 0)) { hint = 'No powerup runs in this window.'; return; }
            const ca = teamStrongColor(colors[0]);
            const cb = teamStrongColor(colors[1]);
            legend = [{ text: data.teamA, color: ca }, { text: data.teamB, color: cb }];
            renderSpansTimeline(canvas, labels, {
                startTime: start, endTime: end, rows: data.rows, rowH: rowH(avail, data.rows.length),
                stateColors: { teamA: ca, teamB: cb, other: 'rgba(180, 180, 180, 0.85)' },
            });
        } else {
            const rc = regionControl.get();
            const data = twoSided && rc?.editable && view ? prepRegionControlData(rc, view, start, end, order) : null;
            if (!data) { hint = 'No region control for this demo.'; return; }
            const [ia, ib] = regionSides(order, data);
            const ca = teamStrongColor(colors[ia]);
            const cb = teamStrongColor(colors[ib]);
            legend = [{ text: data.teamA, color: ca }, { text: 'contested', color: '#fff' }, { text: data.teamB, color: cb }];
            renderSpansTimeline(canvas, labels, {
                startTime: start, endTime: end, rows: data.rows, rowH: rowH(avail, data.rows.length),
                stateColors: { teamAControl: ca, contested: 'rgb(255, 255, 255)', teamBControl: cb },
            });
        }
    }

    // The diverging graphs' height and the span rows' in a sized pane:
    // never below their natural size.
    /** @param {number | null} avail */
    const grown = (avail) => (avail == null ? undefined : Math.max(DIVERGING_GRAPH_LAYOUT.H, avail));
    /** @param {number | null} avail @param {number} n */
    const rowH = (avail, n) => (avail == null || !n ? undefined : Math.max(RC_ROW_H, Math.floor((avail - RC_AXIS_H) / n)));

    // A new demo, roster, region set, window or selection redraws even when
    // the span stayed put.
    $effect(() => {
        $demo; $teams; $regionControl; view; width; $segment;
        measure();
        draw(clock.t);
    });

    $effect(() => {
        const off = onFrame((t) => draw(t));
        const ro = new ResizeObserver(() => { measure(); draw(clock.t); });
        ro.observe(/** @type {HTMLElement} */ (canvas.parentElement));
        const body = canvas.closest('.player-pane-body');
        if (body) ro.observe(body);
        return () => { off(); ro.disconnect(); };
    });

    /** @param {MouseEvent} e */
    function seek(e) {
        const r = canvas.getBoundingClientRect();
        if (!r.width) return;
        const [start, end] = range;
        clock.seek(start + ((e.clientX - r.left) / r.width) * (end - start));
    }
</script>

<div class="gp-head" bind:this={head}>
    <span class="gp-legend">
        {#each legend as l (l.text)}<span style:color={l.color}>{l.text}</span>{/each}
    </span>
    <select class="pt-select" title="How much of the match the graph shows" value={width}
        onchange={(e) => setPaneOpt(kind, 'window', Number(e.currentTarget.value))}>
        {#each PANE_WINDOWS as [w, label] (w)}<option value={w}>{label}</option>{/each}
    </select>
</div>
<div class="gp-body" class:spans={spans} style:display={hint ? 'none' : null}>
    {#if spans}<div class="gp-labels" bind:this={labels}></div>{/if}
    <div class="gp-plot">
        <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
        <canvas bind:this={canvas} onclick={seek} title="Click to seek"></canvas>
        {#if cursor != null}<div class="gp-cursor" style:left="{cursor}%"></div>{/if}
    </div>
</div>
{#if hint}<div class="player-clip-hint">{hint}</div>{/if}

<style>
    .gp-head {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 6px;
        margin-bottom: 2px;
    }
    .gp-legend {
        display: inline-flex;
        flex-wrap: wrap;
        gap: 8px;
        font-size: 11px;
    }
    .gp-head .pt-select {
        height: 22px;
        font-size: 11px;
    }
    .gp-body.spans {
        display: flex;
        align-items: flex-start;
    }
    .gp-labels {
        flex: none;
        font-size: 11px;
    }
    .gp-plot {
        position: relative;
        flex: 1 1 auto;
        min-width: 0;
    }
    canvas {
        display: block;
        cursor: pointer;
    }
    .gp-cursor {
        position: absolute;
        top: 0;
        bottom: 0;
        width: 1px;
        background: rgba(255, 255, 255, 0.85);
        pointer-events: none;
    }
</style>
