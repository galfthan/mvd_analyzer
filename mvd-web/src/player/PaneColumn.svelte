<script>
    // The pane column beside the player: panes in the user's order (drag a
    // header onto another pane, or ↑ ↓), each collapsible and resizable,
    // the column itself resizable from its left edge and hideable.
    import Pane from './Pane.svelte';
    import MapPane from './panes/MapPane.svelte';
    import StackPane from './panes/StackPane.svelte';
    import MomentsPane from './panes/MomentsPane.svelte';
    import FeedPane from './panes/FeedPane.svelte';
    import { paneLayout, updateLayout, PANE_IDS, MIN_WIDTH, MAX_WIDTH } from './layout.js';

    const PANES = {
        map: { title: 'Map', component: MapPane },
        stack: { title: 'Health / armor', component: StackPane },
        moments: { title: 'Moments', component: MomentsPane },
        feed: { title: 'Frag feed', component: FeedPane },
    };

    let order = $derived($paneLayout.order);
    let closed = $derived(PANE_IDS.filter((id) => !order.includes(id)));

    function add(e) {
        const id = e.currentTarget.value;
        e.currentTarget.value = '';
        if (id) updateLayout((l) => { l.order.push(id); });
    }

    function widthStart(e) {
        e.preventDefault();
        const startX = e.clientX;
        const startW = $paneLayout.width;
        const move = (ev) => {
            const w = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, startW + startX - ev.clientX));
            updateLayout((l) => { l.width = w; });
        };
        const up = () => {
            window.removeEventListener('pointermove', move);
            window.removeEventListener('pointerup', up);
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', up);
    }
</script>

{#if $paneLayout.hidden}
    <button class="panes-show player-pov-btn" title="Show the panes" onclick={() => updateLayout((l) => { l.hidden = false; })}>◂ Panes</button>
{:else}
    <div class="player-panes">
        <div class="width-handle" role="separator" aria-orientation="vertical" title="Drag to resize the column"
            onpointerdown={widthStart}></div>
        <div class="column-head">
            <select title="Add a pane to this column" onchange={add} disabled={!closed.length}>
                <option value="">+ Add pane…</option>
                {#each closed as id (id)}
                    <option value={id}>{PANES[id].title}</option>
                {/each}
            </select>
            <button class="player-pov-btn" title="Hide the panes" onclick={() => updateLayout((l) => { l.hidden = true; })}>Hide ▸</button>
        </div>
        {#each order as id, i (id)}
            {@const P = PANES[id].component}
            <Pane {id} title={PANES[id].title} index={i} count={order.length}>
                <P />
            </Pane>
        {/each}
    </div>
{/if}

<style>
    .player-panes {
        position: relative;
    }
    .width-handle {
        position: absolute;
        left: -7px;
        top: 0;
        bottom: 0;
        width: 6px;
        cursor: ew-resize;
    }
    .width-handle:hover {
        background: rgba(0, 217, 255, 0.25);
    }
    .column-head {
        display: flex;
        gap: 6px;
        justify-content: space-between;
    }
    .column-head select {
        background: var(--bg-panel);
        color: var(--fg-default);
        border: 1px solid transparent;
        border-radius: 3px;
        padding: 4px 6px;
        font: inherit;
    }
    .panes-show {
        align-self: start;
    }
</style>
