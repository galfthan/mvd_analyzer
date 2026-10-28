<script>
    // The Player tab: the engine with its POV and clip bars, the clip finder and the playlist
    // on the left, the pane column on the right, split by PaneForge (drag
    // the divider, or focus it and use the arrow keys; dragging the column
    // shut hides it). src/app/tabs.js toggles the `active` class like on every
    // other .tab-content.
    import { PaneGroup, Pane, PaneResizer } from 'paneforge';
    import PlayerFrame from './PlayerFrame.svelte';
    import PovBar from './PovBar.svelte';
    import ClipBar from './ClipBar.svelte';
    import Playlist from './Playlist.svelte';
    import ClipFinder from './ClipFinder.svelte';
    import PaneColumn from './PaneColumn.svelte';
    import { paneLayout, updateLayout, MIN_SPLIT, MAX_SPLIT } from './layout.js';

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
    <div class="panel player-panel">
        <PaneGroup direction="horizontal" class="player-layout" onLayoutChange={layoutChange}>
            <Pane order={1} minSize={100 - MAX_SPLIT}>
                <div class="player-main">
                    <PlayerFrame />
                    <PovBar />
                    <ClipBar />
                    <ClipFinder />
                    <Playlist />
                </div>
            </Pane>
            <PaneResizer class="player-split" onDraggingChange={draggingChange} title="Drag to resize the panes · drag shut to hide them" />
            <Pane order={2} bind:this={column} defaultSize={initial.hidden ? 0 : initial.split}
                minSize={MIN_SPLIT} maxSize={MAX_SPLIT} collapsible collapsedSize={0}
                onCollapse={() => setHidden(true)} onExpand={() => setHidden(false)}>
                <PaneColumn />
            </Pane>
        </PaneGroup>
    </div>
</div>
