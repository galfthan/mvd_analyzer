<script>
    // The engine's fixed layer (player-slot.js): the iframe and the clip
    // card, positioned over the Player pane's placeholder once per frame
    // while the tab shows (a style write only when the box changed) and
    // clipped to what of the placeholder its scrolling ancestors show. No
    // placeholder: hidden — the engine runs on, it is the tab's clock.
    // While the pointer is down elsewhere (a pane drag, a resize, the split)
    // and during a pane drag-and-drop the layer lets events through, so the
    // iframe does not swallow them.
    import { onMount } from 'svelte';
    import PlayerFrame from './PlayerFrame.svelte';
    import { activeTab } from '../state/index.js';
    import { playerSlot, slotBox, clipAncestors } from './player-slot.js';

    /** @type {HTMLDivElement} */
    let layer;
    let passThrough = $state(false);

    onMount(() => {
        let raf = 0;
        let last = '';
        /** @type {HTMLElement[]} */
        let clips = [];
        /** @type {HTMLElement | null} */
        let clipsOf = null;

        function frame() {
            raf = requestAnimationFrame(frame);
            const slot = playerSlot.get();
            let style = 'visibility:hidden';
            if (slot && slot.isConnected) {
                if (clipsOf !== slot) {
                    clips = clipAncestors(slot);
                    clipsOf = slot;
                }
                const { box, inset, visible } = slotBox(slot.getBoundingClientRect(), clips.map((c) => c.getBoundingClientRect()));
                if (visible) {
                    style = `left:${box.left}px;top:${box.top}px;width:${box.width}px;height:${box.height}px;`
                        + `clip-path:inset(${inset.top}px ${inset.right}px ${inset.bottom}px ${inset.left}px)`;
                }
            }
            if (style !== last) {
                layer.style.cssText = style;
                last = style;
            }
        }

        // Layouts change (a column moved, fullscreen) without the slot
        // changing: look the clipping ancestors up again.
        const offSlot = playerSlot.subscribe(() => { clipsOf = null; });
        const offTab = activeTab.subscribe((tab) => {
            cancelAnimationFrame(raf);
            raf = 0;
            clipsOf = null;
            if (tab === 'player') raf = requestAnimationFrame(frame);
        });
        const refresh = () => { clipsOf = null; };
        window.addEventListener('resize', refresh);
        document.addEventListener('fullscreenchange', refresh);

        /** @param {PointerEvent} e */
        const down = (e) => { if (!layer.contains(/** @type {Node} */ (e.target))) passThrough = true; };
        const release = () => { passThrough = false; };
        document.addEventListener('pointerdown', down, true);
        document.addEventListener('pointerup', release, true);
        document.addEventListener('pointercancel', release, true);
        const dragging = () => { passThrough = true; };
        document.addEventListener('dragstart', dragging, true);
        document.addEventListener('dragend', release, true);
        document.addEventListener('drop', release, true);
        return () => {
            cancelAnimationFrame(raf);
            offSlot();
            offTab();
            window.removeEventListener('resize', refresh);
            document.removeEventListener('fullscreenchange', refresh);
            document.removeEventListener('pointerdown', down, true);
            document.removeEventListener('pointerup', release, true);
            document.removeEventListener('pointercancel', release, true);
            document.removeEventListener('dragstart', dragging, true);
            document.removeEventListener('dragend', release, true);
            document.removeEventListener('drop', release, true);
        };
    });
</script>

<div class="player-layer" class:pass-through={passThrough} bind:this={layer} style="visibility:hidden">
    <PlayerFrame />
</div>
