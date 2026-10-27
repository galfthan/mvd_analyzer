<script>
    // The Player tab: the engine with its POV and clip bars and the playlist
    // on the left, the pane column on the right. app.js still toggles the
    // `active` class like on every other .tab-content (onTabActivated).
    import PlayerFrame from './PlayerFrame.svelte';
    import PovBar from './PovBar.svelte';
    import ClipBar from './ClipBar.svelte';
    import Playlist from './Playlist.svelte';
    import PaneColumn from './PaneColumn.svelte';
    import { paneLayout } from './layout.js';

    let columns = $derived($paneLayout.hidden ? 'minmax(0, 1fr) auto' : `minmax(0, 1fr) ${$paneLayout.width}px`);
</script>

<div id="tab-player" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel player-panel">
        <div class="player-layout" style:--player-columns={columns}>
            <div class="player-main">
                <PlayerFrame />
                <PovBar />
                <ClipBar />
                <Playlist />
            </div>
            <PaneColumn />
        </div>
    </div>
</div>

<style>
    @media (min-width: 1101px) {
        .player-layout {
            grid-template-columns: var(--player-columns);
        }
    }
</style>
