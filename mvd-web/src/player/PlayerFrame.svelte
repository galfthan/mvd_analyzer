<script>
    import { engine } from './index.js';
    import ClipCard from './ClipCard.svelte';

    // The engine appends its iframe here (src/player/engine.js); the clip
    // card sits over it.
    /** @param {HTMLElement} el */
    function host(el) {
        engine.attachHost(el);
    }
</script>

<div class="player-frame-host" use:host><ClipCard /></div>
