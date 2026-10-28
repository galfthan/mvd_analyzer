<script>
    // A column of panes: beside the player (`order`) or under it (`below`),
    // in the user's order (drag a header onto another pane or this column's
    // head, or ↑ ↓), each collapsible and resizable. The side column's width
    // and hiding are PlayerTab's split; hidden, it renders nothing, so its
    // map stops drawing.
    import PanelRightClose from '@lucide/svelte/icons/panel-right-close';
    import Pane from './Pane.svelte';
    import ClipFinder from './ClipFinder.svelte';
    import Playlist from './Playlist.svelte';
    import MapPane from './panes/MapPane.svelte';
    import StackPane from './panes/StackPane.svelte';
    import MomentsPane from './panes/MomentsPane.svelte';
    import FeedPane from './panes/FeedPane.svelte';
    import PlayersPane from './panes/PlayersPane.svelte';
    import GraphPane from './panes/GraphPane.svelte';
    import { paneLayout, updateLayout, movePane, columnOf, PANE_IDS } from './layout.js';
    import { playlist } from './playlist-store.js';

    /** @type {{ column: import('./layout.js').Column }} */
    let { column } = $props();

    /** @type {Record<string, { title: string, component: import('svelte').Component<any>, props?: Record<string, unknown> }>} */
    const PANES = {
        finder: { title: 'Clip finder', component: ClipFinder },
        playlist: { title: 'Playlist', component: Playlist },
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

    let ids = $derived($paneLayout[column]);
    let closed = $derived(PANE_IDS.filter((id) => !columnOf($paneLayout, id)));
    let dragOver = $state(false);

    /** @param {string} id */
    const titleOf = (id) => (id === 'playlist' && $playlist.clips.length ? `Playlist (${$playlist.clips.length})` : PANES[id].title);

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function add(e) {
        const id = e.currentTarget.value;
        e.currentTarget.value = '';
        if (id) updateLayout((l) => { l[column].push(id); });
    }

    // Dropped on the head, a pane joins this column at the end.
    /** @param {DragEvent} e */
    function dragOverHead(e) {
        if (!/** @type {DataTransfer} */ (e.dataTransfer).types.includes('text/x-mvd-pane')) return;
        e.preventDefault();
        dragOver = true;
    }

    /** @param {DragEvent} e */
    function drop(e) {
        dragOver = false;
        const id = /** @type {DataTransfer} */ (e.dataTransfer).getData('text/x-mvd-pane');
        if (!id) return;
        e.preventDefault();
        movePane(id, column, ids.length);
    }
</script>

{#if column === 'below' || !$paneLayout.hidden}
    <div class="player-panes" class:below={column === 'below'}>
        <div class="column-head" class:drag-over={dragOver} role="group" aria-label="Pane column"
            ondragover={dragOverHead} ondragleave={() => (dragOver = false)} ondrop={drop}>
            {#if closed.length}
                <select class="pt-select" title="Add a pane to this column" onchange={add}>
                    <option value="">Add pane…</option>
                    {#each closed as id (id)}
                        <option value={id}>{PANES[id].title}</option>
                    {/each}
                </select>
            {/if}
            {#if column === 'order'}
                <button class="pt-btn icon" title="Hide the panes" onclick={() => updateLayout((l) => { l.hidden = true; })}>
                    <PanelRightClose size={16} />
                </button>
            {/if}
        </div>
        {#each ids as id, i (id)}
            {@const P = PANES[id].component}
            <Pane {id} {column} title={titleOf(id)} index={i} count={ids.length}>
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
        min-height: 24px;
        border: 1px dashed transparent;
        border-radius: 4px;
    }
    .column-head.drag-over {
        border-color: var(--accent-cyan);
    }
    .below .column-head {
        order: 1;
    }
</style>
