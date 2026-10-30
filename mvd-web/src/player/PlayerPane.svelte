<script>
    // The Player pane: a placeholder the engine's layer sits over
    // (player-slot.js — the iframe itself never moves), the POV bar, and the
    // player menu (player-menu.js: the keys and switches), opened from the
    // POV bar's keyboard button or a right-click on the game view.
    import { onMount } from 'svelte';
    import PovBar from './PovBar.svelte';
    import MapMenu from '../ui/MapMenu.svelte';
    import { playerSlot } from './player-slot.js';
    import { playerMenu, playerMenuItems } from './player-menu.js';
    import { watchNext } from './follow.js';
    import { engine } from './index.js';
    import { clock } from '../state/index.js';

    /** @param {HTMLElement} el */
    function slot(el) {
        playerSlot.set(el);
        return { destroy: () => { if (playerSlot.get() === el) playerSlot.set(null); } };
    }

    const status = engine.status;
    let items = $derived(playerMenuItems({
        engine,
        status: $status,
        toggle: () => engine.screenToggle(),
        step: watchNext,
        seek: (by) => clock.seek(Math.max(0, clock.t + by)),
    }));

    onMount(() => {
        // The game view's pixels are the slot's: the layer sits on it.
        engine.screenMenu = (x, y) => playerMenu.set({ x, y });
        engine.screenDismiss = () => {
            if (!playerMenu.get()) return false;
            playerMenu.set(null);
            return true;
        };
        return () => {
            engine.screenMenu = null;
            engine.screenDismiss = () => false;
            playerMenu.set(null);
        };
    });
</script>

<!-- The menu is placed in the pane body (position: relative); the slot
     is its top-left, so game-view pixels place it there. -->
<div class="player-slot" use:slot></div>
<PovBar />
<MapMenu bind:at={$playerMenu} {items} keep=".player-menu-btn" />
