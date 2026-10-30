<script>
    // One row per player, grouped and coloured in the canonical team order:
    // a strip of the health/armor stack (the Timeline tab's per-player
    // renderer) over the pane's window — the selected segment (the whole
    // match without one, the default), a window rolling with the playhead
    // or the whole match, as the graph panes — the playhead marked, and the
    // values at it. Strips redraw when the window moves (a line over them
    // follows the playhead); given a height (the pane's resize handle) the
    // rows grow to share it. Click a name to watch that player (follow.js).
    import { clock, demo, teams, colorOf, segment } from '../../state/index.js';
    import { bucketIndexAtTime, playerValAt } from '../../../../mvd-map-view/src/index.js';
    import {
        playersByTeam, rosterTeamOf, prepPlayerHAData, matchDurationSec, paneWindow, indicatorPct, PANE_WINDOWS, SELECTION_WINDOW,
    } from '../../lib/timeline.js';
    import { renderMiniStack } from '../../ui/timeline-canvas.js';
    import { paneLayout, paneOpt, setPaneOpt } from '../layout.js';
    import { watch, watched } from '../follow.js';
    import { onFrame } from './tick.js';

    const ROW_H = 22;
    // The strip's fixed scale: a mega (250) under 200 red armor tops out at
    // 450; most of a match sits under 300, so 300 keeps rows comparable and
    // readable, and the rare taller stack clips at the top.
    const MAX = 300;

    let uid = 0;
    // The 50 ms bucket view arrives after the rest of the result. Raw: a
    // deep proxy over the whole columnar view makes every per-frame read go
    // through a Proxy trap (and holds a second copy of the structure).
    /** @type {Record<string, any> | null} the columnar bucket view */
    let view = $state.raw(null);
    $effect(() => {
        const handle = $demo;
        view = handle?.result?.timelineAnalysis?.bucketView || null;
        handle?.ready('buckets').then(() => {
            if ($demo === handle) view = handle.result.timelineAnalysis?.bucketView || null;
        }, () => {});
    });

    let width = $derived(paneOpt($paneLayout, 'stack', 'window', SELECTION_WINDOW));

    let rows = $derived.by(() => {
        const t = $teams;
        if (!view) return [];
        return playersByTeam(view, t.order, rosterTeamOf($demo?.result), t.perPlayer).flat()
            .map((name) => ({ name, cid: `pp-stack-${uid++}` }));
    });

    /** @type {Record<string, { h: number, a: number, at: string } | null>} */
    let vals = $state({});
    /** @type {string | null} */
    let pov = $state(null);
    let cursor = $state(/** @type {number | null} */ (null));

    /** @type {HTMLDivElement} */
    let head;
    /** @type {HTMLDivElement} */
    let list;

    // The row height when the viewer sized the pane, never below ROW_H:
    // the body's inner height less the head (with its margin) shared by the
    // rows, each a strip plus its 2 px of padding.
    function rowHeight() {
        const body = list?.closest('.player-pane-body');
        if (!body || !body.classList.contains('sized') || !rows.length) return ROW_H;
        const bs = getComputedStyle(body);
        const inner = body.clientHeight - parseFloat(bs.paddingTop) - parseFloat(bs.paddingBottom);
        const headH = head.offsetHeight + parseFloat(getComputedStyle(head).marginBottom);
        return Math.max(ROW_H, Math.floor((inner - headH) / rows.length) - 2);
    }

    let lastKey = '';
    // The strips' width and row height, read on resize only: reading them
    // per frame forces a layout after the frame's DOM writes.
    let boxW = 0;
    let rowH = ROW_H;
    function measure() {
        boxW = list?.clientWidth || 0;
        rowH = rowHeight();
        lastKey = '';
    }

    // The strips redraw when the window moved by a pixel at least (a
    // rolling window would otherwise redraw them every frame).
    /** @param {number} t */
    function draw(t) {
        const v = view;
        if (!v || !v.count) return;
        const [start, end] = paneWindow(t, width, matchDurationSec($demo?.result), segment.get());
        cursor = indicatorPct(t, start, end);
        const h = rowH;
        const px = (end - start) / Math.max(1, boxW);
        const key = `${Math.floor(start / px)}|${Math.floor(end / px)}|${h}|${boxW}`;
        if (key !== lastKey) {
            lastKey = key;
            for (const { name, cid } of rows) {
                renderMiniStack(cid, {
                    startTime: start, endTime: end,
                    points: prepPlayerHAData(v, v.players[name], start, end),
                    maxValue: MAX, height: h,
                });
            }
        }
        const i = bucketIndexAtTime(v, t);
        /** @type {typeof vals} */
        const out = {};
        for (const { name } of rows) {
            const cp = v.players[name];
            const hv = cp && i >= 0 ? playerValAt(cp, 'h', i) : undefined;
            out[name] = hv == null ? null : { h: hv, a: playerValAt(cp, 'a', i) || 0, at: playerValAt(cp, 'at', i) || '' };
        }
        vals = out;
        pov = watched();
    }

    // New rows, data, window or selection redraw the strips.
    $effect(() => {
        rows; view; width; $segment;
        measure();
        return onFrame(draw);
    });

    $effect(() => {
        // A resize with the clock still: no frame comes, so draw now.
        const ro = new ResizeObserver(() => { measure(); draw(clock.t); });
        ro.observe(list);
        const body = list.closest('.player-pane-body');
        if (body) ro.observe(body);
        return () => ro.disconnect();
    });
</script>

<div class="sp-head" bind:this={head}>
    <select class="pt-select" title="How much of the match the strips show" value={width}
        onchange={(e) => setPaneOpt('stack', 'window', Number(e.currentTarget.value))}>
        {#each PANE_WINDOWS as [w, label] (w)}<option value={w}>{label}</option>{/each}
    </select>
</div>
<div bind:this={list}>
    {#each rows as { name, cid } (cid)}
        {@const v = vals[name]}
        <div class="pp-stack-row" class:pov={name === pov}>
            <span class="name" style:color={colorOf($teams, name, $teams.perPlayer)} title="{name} — click to watch"
                role="button" tabindex="-1" onclick={() => watch(name)} onkeydown={() => {}}>{name}</span>
            <div class="pp-stack-wrap">
                <canvas id={cid}></canvas>
                {#if cursor != null}<div class="sp-cursor" style:left="{cursor}%"></div>{/if}
            </div>
            {#if v}
                <span class="vals">{v.h}/<span class={v.at ? `armor-${v.at}` : ''}>{v.a}</span></span>
            {:else}
                <span class="vals dead">—</span>
            {/if}
        </div>
    {:else}
        <div class="player-clip-hint">No per-player streams in this demo.</div>
    {/each}
</div>

<style>
    .sp-head {
        display: flex;
        justify-content: flex-end;
        margin-bottom: 2px;
    }
    .sp-head .pt-select {
        height: 22px;
        font-size: 11px;
    }
    .sp-cursor {
        position: absolute;
        top: 0;
        bottom: 0;
        width: 1px;
        background: rgba(255, 255, 255, 0.8);
        pointer-events: none;
    }
</style>
