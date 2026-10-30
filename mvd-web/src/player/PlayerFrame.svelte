<script>
    import ClipCard from './ClipCard.svelte';
    import PlaylistOverlay from './PlaylistOverlay.svelte';

    // The engine appends its iframe here (src/player/engine.js); the clip
    // card and the playlist's title cards and texts sit over it. The engine is a prop so the light player page
    // (src/play/) can host its own without the analyzer's modules.
    /** @type {{ engine: import('./engine.js').PlayerEngine }} */
    let { engine } = $props();

    /** @param {HTMLElement} el */
    function host(el) {
        engine.attachHost(el);
    }
</script>

<div class="player-frame-host" use:host><ClipCard {engine} /><PlaylistOverlay {engine} /></div>
