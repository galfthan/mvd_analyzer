<script>
    // The pane column beside the player: panes in the user's order (drag a
    // header onto another pane, or ↑ ↓), each collapsible and resizable.
    // The column's width and hiding are PlayerTab's split; hidden, it
    // renders nothing, so its map stops drawing.
    import PanelRightClose from '@lucide/svelte/icons/panel-right-close';
    import Pane from './Pane.svelte';
    import MapPane from './panes/MapPane.svelte';
    import StackPane from './panes/StackPane.svelte';
    import MomentsPane from './panes/MomentsPane.svelte';
    import FeedPane from './panes/FeedPane.svelte';
    import PlayersPane from './panes/PlayersPane.svelte';
    import GraphPane from './panes/GraphPane.svelte';
    import { paneLayout, updateLayout, PANE_IDS } from './layout.js';

    /** @type {Record<string, { title: string, component: import('svelte').Component<any>, props?: Record<string, unknown> }>} */
    const PANES = {
        map: { title: 'Map', component: MapPane },
        players: { title: 'Players', component: PlayersPane },
        stack: { title: 'Health / armor strips', component: StackPane },
        moments: { title: 'Moments', component: MomentsPane },
        feed: { title: 'Frag feed', component: FeedPane },
        score: { title: 'Score', component: GraphPane, props: { kind: 'score' } },
        'team-weapons': { title: 'Team weapons', component: GraphPane, props: { kind: 'team-weapons' } },
        'team-ha': { title: 'Team health / armor', component: GraphPane, props: { kind: 'team-ha' } },
        'weapons-pp': { title: 'Weapons per player', component: GraphPane, props: { kind: 'weapons-pp' } },
        powerups: { title: 'Powerups', component: GraphPane, props: { kind: 'powerups' } },
        regions: { title: 'Region control', component: GraphPane, props: { kind: 'regions' } },
    };

    let order = $derived($paneLayout.order);
    let closed = $derived(PANE_IDS.filter((id) => !order.includes(id)));

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function add(e) {
        const id = e.currentTarget.value;
        e.currentTarget.value = '';
        if (id) updateLayout((l) => { l.order.push(id); });
    }
</script>

{#if !$paneLayout.hidden}
    <div class="player-panes">
        <div class="column-head">
            {#if closed.length}
                <select class="pt-select" title="Add a pane to this column" onchange={add}>
                    <option value="">Add pane…</option>
                    {#each closed as id (id)}
                        <option value={id}>{PANES[id].title}</option>
                    {/each}
                </select>
            {/if}
            <button class="pt-btn icon" title="Hide the panes" onclick={() => updateLayout((l) => { l.hidden = true; })}>
                <PanelRightClose size={16} />
            </button>
        </div>
        {#each order as id, i (id)}
            {@const P = PANES[id].component}
            <Pane {id} title={PANES[id].title} index={i} count={order.length}>
                <P {...PANES[id].props} />
            </Pane>
        {/each}
    </div>
{/if}

<style>
    .column-head {
        display: flex;
        gap: 6px;
        justify-content: flex-end;
    }
</style>
