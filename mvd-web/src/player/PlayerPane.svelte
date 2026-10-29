<script>
    // The Player pane: a placeholder the engine's layer sits over
    // (player-slot.js — the iframe itself never moves) and the POV bar.
    import PovBar from './PovBar.svelte';
    import { playerSlot } from './player-slot.js';

    /** @param {HTMLElement} el */
    function slot(el) {
        playerSlot.set(el);
        return { destroy: () => { if (playerSlot.get() === el) playerSlot.set(null); } };
    }
</script>

<div class="player-slot" use:slot></div>
<PovBar />
