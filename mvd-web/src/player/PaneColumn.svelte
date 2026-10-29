<script>
    // A column of panes: the side column (`order`) or the main one (`below`
    // — the name is from before the player was a pane),
    // in the user's order (drag a header onto another pane or onto this
    // column's "Add pane…" row at its foot, or ↑ ↓), each collapsible and
    // resizable. The side column's width and hiding (the POV bar's toggle)
    // are PlayerTab's split; hidden, it renders nothing, so its map stops
    // drawing.
    import Pane from './Pane.svelte';
    import PlayerPane from './PlayerPane.svelte';
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
        player: { title: 'Player', component: PlayerPane },
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

    // Dropped on the foot, a pane joins this column at the end.
    /** @param {DragEvent} e */
    function dragOverFoot(e) {
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
        {#each ids as id, i (id)}
            {@const P = PANES[id].component}
            <Pane {id} {column} title={titleOf(id)} index={i} count={ids.length}>
                <P {...PANES[id].props} />
            </Pane>
        {/each}
        <div class="column-add" class:drag-over={dragOver} role="group" aria-label="Add a pane to this column"
            ondragover={dragOverFoot} ondragleave={() => (dragOver = false)} ondrop={drop}>
            {#if closed.length}
                <select class="pt-select" title="Add a pane to the end of this column" onchange={add}>
                    <option value="">+ Add pane…</option>
                    {#each closed as id (id)}
                        <option value={id}>{PANES[id].title}</option>
                    {/each}
                </select>
            {/if}
        </div>
    </div>
{/if}

<style>
    .column-add {
        display: flex;
        justify-content: center;
        min-height: 28px;
        border: 1px dashed transparent;
        border-radius: 4px;
    }
    .column-add.drag-over {
        border-color: var(--accent-cyan);
    }
    .column-add .pt-select {
        opacity: 0.6;
        transition: opacity 0.15s;
    }
    .column-add .pt-select:hover,
    .column-add .pt-select:focus {
        opacity: 1;
    }
</style>
