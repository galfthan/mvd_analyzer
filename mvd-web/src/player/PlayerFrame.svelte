<script>
    import { engine } from './index.js';

    // The engine appends its iframe here (src/player/engine.js).
    /** @param {HTMLElement} el */
    function host(el) {
        engine.attachHost(el);
    }
</script>

<div class="player-frame-host" use:host></div>
