<script>
    // Players: the Map tab's legend as a pane — one sortable table per team
    // (canonical order and colour, running frags in its title), each
    // player's loc, health / armor and weapons at the playhead.
    // Built by src/tabs/map/sidebar.js over the map model's data; cells
    // refresh at most every 150 ms. The watched player's row is lit; click
    // a row to watch them (follow.js).
    import { onMount } from 'svelte';
    import { teams } from '../../state/index.js';
    import { view, mapData } from '../../tabs/map/map-model.js';
    import { buildLegend, syncLegend } from '../../tabs/map/sidebar.js';
    import { mapFollow, watch, watched } from '../follow.js';
    import { engine } from '../index.js';
    import { onFrame } from './tick.js';

    const EVERY_MS = 150;

    /** @type {HTMLDivElement} */
    let el;
    let empty = $state(true);

    onMount(() => {
        /** @type {import('../../lib/result.js').Result | null} */
        let result = null;
        let last = 0;
        /** @type {ReturnType<typeof setTimeout> | undefined} */
        let trailing;

        const sync = (/** @type {number} */ t) => {
            if (!result) return;
            last = performance.now();
            syncLegend(el, view, result, t);
            const who = watched();
            for (const tr of /** @type {NodeListOf<HTMLElement>} */ (el.querySelectorAll('tr[data-player]'))) {
                tr.classList.toggle('watched', tr.dataset.player === who);
            }
        };
        const build = () => {
            const d = mapData.get();
            result = d?.result ?? null;
            empty = !result;
            if (!result) {
                el.innerHTML = '';
                return;
            }
            buildLegend(el, view.state.teams, view.state.playerSymbols, teams.get().colors);
            sync(view.state.currentTime);
        };
        const offs = [
            mapData.subscribe(build),
            teams.subscribe(build),
            mapFollow.subscribe(() => sync(view.state.currentTime)),
            engine.status.subscribe(() => sync(view.state.currentTime)),
            onFrame((t) => {
                clearTimeout(trailing);
                const wait = EVERY_MS - (performance.now() - last);
                if (wait <= 0) sync(t);
                else trailing = setTimeout(() => sync(view.state.currentTime), wait);
            }),
        ];
        return () => {
            clearTimeout(trailing);
            offs.forEach((off) => off());
        };
    });

    /** @param {MouseEvent} e */
    function click(e) {
        const target = /** @type {Element} */ (e.target);
        if (target.closest('a, th')) return;
        const name = /** @type {HTMLElement | null} */ (target.closest('tr[data-player]'))?.dataset.player;
        if (name) watch(name);
    }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions: the rows are a pointer shortcut; the POV bar is the control -->
<div class="map-legend legend-pane" bind:this={el} onclick={click}></div>
{#if empty}<div class="player-clip-hint">No player positions for this demo.</div>{/if}

<style>
    .legend-pane {
        padding: 0;
        background: none;
    }
    .legend-pane :global(tbody tr) {
        cursor: pointer;
    }
    .legend-pane :global(tr.watched) {
        background: rgba(0, 217, 255, 0.1);
    }
</style>
