<script>
    // Region status: the Map tab's panel as a pane — one row per control
    // region with its state (whose, contested) and the players in it as
    // their map symbols, at the playhead (src/tabs/map/sidebar.js over the
    // map model's data). Only where region control applies (two sides).
    import { onMount } from 'svelte';
    import { teams, regionControl } from '../../state/index.js';
    import { view, mapData } from '../../tabs/map/map-model.js';
    import { syncRegionStatus } from '../../tabs/map/sidebar.js';
    import { onFrame } from './tick.js';

    /** @type {HTMLDivElement} */
    let el;
    let shown = $derived(!!$regionControl?.editable);

    onMount(() => {
        // The composited player icons, per demo.
        /** @type {Map<string, HTMLCanvasElement>} */
        let icons = new Map();
        const offs = [
            mapData.subscribe(() => { icons = new Map(); }),
            onFrame((t) => syncRegionStatus(el, view, t, teams.get().colors, icons)),
        ];
        return () => offs.forEach((off) => off());
    });
</script>

<div class="region-status-pane" bind:this={el} style:display={shown ? null : 'none'}></div>
{#if !shown}<div class="player-clip-hint">No region control in this demo (it needs two sides).</div>{/if}
