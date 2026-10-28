<script>
    // The last frags up to the playhead, newest first; those within the last
    // RECENT_S seconds are lit. Click one to watch it from the killer's view,
    // a few seconds before.
    import { clock, demo, teams, colorOf } from '../../state/index.js';
    import { formatDuration } from '../../lib/result.js';
    import { engine } from '../index.js';
    import { onFrame } from './tick.js';

    const ROWS = 12;
    const RECENT_S = 4;
    const LEAD_S = 4;

    let frags = $derived($demo?.result?.frags?.frags || []);
    let tMs = $state(0);

    $effect(() => onFrame((t) => { tMs = t * 1000; }));

    // Derived from the frags and the time together, so a new demo's shorter
    // list can never meet the previous demo's index.
    let rows = $derived.by(() => {
        // Last frag at or before the playhead (frags are time-ordered).
        let lo = 0, hi = frags.length;
        while (lo < hi) {
            const mid = (lo + hi) >> 1;
            if (frags[mid].time <= tMs) lo = mid + 1; else hi = mid;
        }
        const out = [];
        for (let k = lo - 1; k >= 0 && out.length < ROWS; k--) {
            out.push({ f: frags[k], lit: tMs - frags[k].time <= RECENT_S * 1000 });
        }
        return out;
    });

    /** @param {string} name */
    const color = (name) => colorOf($teams, name, $teams.perPlayer);

    /** @param {Record<string, any>} f  a result.frags.frags row */
    function watch(f) {
        clock.seek(Math.max(0, f.time / 1000 - LEAD_S));
        engine.trackName((!f.isSuicide && f.killer) || f.victim);
    }
</script>

<ol class="pp-feed">
    {#each rows as { f, lit } (f)}
        <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
        <li class:recent={lit} title="Watch it from the killer's view" role="button" tabindex="0"
            onclick={() => watch(f)} onkeydown={(e) => e.key === 'Enter' && watch(f)}>
            <span class="t">{formatDuration(f.time / 1000)}</span>
            {#if !f.isSuicide && f.killer}<span style:color={color(f.killer)}>{f.killer}</span>{/if}
            <span class="w">{f.cause || f.weapon || ''}</span>
            <span style:color={color(f.victim)}>{f.victim}</span>
        </li>
    {/each}
</ol>
