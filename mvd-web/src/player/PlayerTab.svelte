<script>
    // The Map / Player tab ("Watch"): a Map | Player switch between its
    // preset views (layout.js: each preset a saved layout; Reset restores
    // the one in use), then two columns of panes split by PaneForge — the main one
    // (layout `below`: the Player pane and the playlist to start with) and
    // the side one (`order`; drag the divider, or focus it and use the arrow
    // keys; dragging the column shut hides it). The engine itself is not in
    // a pane: PlayerLayer keeps its iframe in one fixed layer over the Player
    // pane's placeholder, since a moved iframe reloads (player-slot.js).
    // src/app/tabs.js toggles the `active` class like on every other
    // .tab-content.
    import { PaneGroup, Pane, PaneResizer } from 'paneforge';
    import PlayerLayer from './PlayerLayer.svelte';
    import PaneColumn from './PaneColumn.svelte';
    import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
    import { paneLayout, updateLayout, preset, setPreset, resetPreset, MIN_SPLIT, MAX_SPLIT } from './layout.js';
    import { fullscreenTab } from '../app/fullscreen.js';

    // PaneForge takes the sizes once; the layout store follows its changes.
    const initial = paneLayout.get();
    /** @type {Pane | undefined} */
    let column = $state();

    // The column's hidden flag and PaneForge's collapsed state move together,
    // whichever side changes first (the hide / show buttons, or a drag).
    // Shown again, it takes the stored width back.
    $effect(() => {
        if (!column) return;
        if ($paneLayout.hidden && !column.isCollapsed()) column.collapse();
        else if (!$paneLayout.hidden && column.isCollapsed()) {
            // expand() reports its own (minimum) size first; read ours before.
            const split = paneLayout.get().split;
            column.expand();
            column.resize(split);
        }
    });

    // Another preset (or a reset) brings its own split.
    $effect(() => {
        $preset;
        $paneLayout;
        if (!column || $paneLayout.hidden || column.isCollapsed()) return;
        if (Math.abs(column.getSize() - $paneLayout.split) > 0.5) column.resize($paneLayout.split);
    });

    /** @param {boolean} hidden */
    function setHidden(hidden) {
        if (paneLayout.get().hidden !== hidden) updateLayout((l) => { l.hidden = hidden; });
    }

    // The width is stored when a drag ends, not while it runs: a drag that
    // shuts the column passes through its minimum on the way, and showing
    // it again should not bring that back. Keyboard steps store at once.
    let dragging = false;
    /** @type {number[] | null} */
    let lastSizes = null;

    /** @param {number[]} sizes */
    function storeSplit(sizes) {
        const split = Math.round(sizes[1] * 10) / 10;
        if (split >= MIN_SPLIT && split !== paneLayout.get().split) updateLayout((l) => { l.split = split; });
    }

    /** @param {number[]} sizes */
    function layoutChange(sizes) {
        lastSizes = sizes;
        if (!dragging) storeSplit(sizes);
    }

    /** @param {boolean} on */
    function draggingChange(on) {
        dragging = on;
        if (!on && lastSizes) storeSplit(lastSizes);
    }
</script>

<div id="tab-player" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel player-panel" class:player-panel--fullscreen={$fullscreenTab === 'player'}>
        <div class="watch-head">
            <span class="pt-group" role="group" aria-label="View">
                <button class="pt-btn" class:active={$preset === 'map'} aria-pressed={$preset === 'map'}
                    title="Mainly the map: the map large, players, items and regions beside it — no demo player to load" onclick={() => setPreset('map')}>Map</button>
                <button class="pt-btn" class:active={$preset === 'player'} aria-pressed={$preset === 'player'}
                    title="Mainly the demo player: the game view and the playlist, the map beside them" onclick={() => setPreset('player')}>Player</button>
            </span>
            <button class="pt-btn icon" title="Put this view's panes back as they were" onclick={() => resetPreset()}><RotateCcw size={14} /></button>
        </div>
        <PaneGroup direction="horizontal" class="player-layout" onLayoutChange={layoutChange}>
            <Pane order={1} minSize={100 - MAX_SPLIT}>
                <PaneColumn column="below" />
            </Pane>
            <PaneResizer class="player-split" onDraggingChange={draggingChange} title="Drag to resize the panes · drag shut to hide them" />
            <Pane order={2} bind:this={column} defaultSize={initial.hidden ? 0 : initial.split}
                minSize={MIN_SPLIT} maxSize={MAX_SPLIT} collapsible collapsedSize={0}
                onCollapse={() => setHidden(true)} onExpand={() => setHidden(false)}>
                <PaneColumn column="order" />
            </Pane>
        </PaneGroup>
        <PlayerLayer />
    </div>
</div>

<style>
    .watch-head {
        display: flex;
        align-items: center;
        gap: 6px;
    }
</style>
