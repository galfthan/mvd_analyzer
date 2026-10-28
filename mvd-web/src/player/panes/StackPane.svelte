<script>
    // One row per player, grouped and coloured in the canonical team order:
    // a rolling strip of the health/armor stack (the Timeline tab's
    // per-player renderer) from BEFORE s back to AFTER s ahead, the playhead
    // marked, and the values at it. Click a name to watch that player.
    import { demo, teams, colorOf } from '../../state/index.js';
    import { bucketIndexAtTime, playerValAt } from '../../../../mvd-map-view/src/index.js';
    import { playersByTeam, rosterTeamOf, prepPlayerHAData } from '../../lib/timeline.js';
    import { renderMiniStack } from '../../ui/timeline-canvas.js';
    import { engine } from '../index.js';
    import { onFrame } from './tick.js';

    const BEFORE = 45;
    const AFTER = 15;
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

    $effect(() => {
        const r = rows;
        const v = view;
        return onFrame((/** @type {number} */ t) => {
            if (!v || !v.count) return;
            const i = bucketIndexAtTime(v, t);
            /** @type {typeof vals} */
            const out = {};
            for (const { name, cid } of r) {
                const cp = v.players[name];
                renderMiniStack(cid, {
                    startTime: t - BEFORE, endTime: t + AFTER,
                    points: prepPlayerHAData(v, cp, t - BEFORE, t + AFTER),
                    maxValue: MAX, height: ROW_H,
                });
                const canvas = /** @type {HTMLCanvasElement | null} */ (document.getElementById(cid));
                if (canvas) {
                    const ctx = /** @type {CanvasRenderingContext2D} */ (canvas.getContext('2d'));
                    const x = Math.round(canvas.width * BEFORE / (BEFORE + AFTER));
                    ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
                    ctx.fillRect(x, 0, Math.max(1, Math.round(window.devicePixelRatio || 1)), canvas.height);
                }
                const h = cp && i >= 0 ? playerValAt(cp, 'h', i) : undefined;
                out[name] = h == null ? null : { h, a: playerValAt(cp, 'a', i) || 0, at: playerValAt(cp, 'at', i) || '' };
            }
            vals = out;
            pov = engine.povName();
        });
    });
</script>

{#each rows as { name, cid } (cid)}
    {@const v = vals[name]}
    <div class="pp-stack-row" class:pov={name === pov}>
        <span class="name" style:color={colorOf($teams, name, $teams.perPlayer)} title="{name} — click to watch"
            role="button" tabindex="-1" onclick={() => engine.trackName(name)} onkeydown={() => {}}>{name}</span>
        <div class="pp-stack-wrap"><canvas id={cid}></canvas></div>
        {#if v}
            <span class="vals">{v.h}/<span class={v.at ? `armor-${v.at}` : ''}>{v.a}</span></span>
        {:else}
            <span class="vals dead">—</span>
        {/if}
    </div>
{:else}
    <div class="player-clip-hint">No per-player streams in this demo.</div>
{/each}
