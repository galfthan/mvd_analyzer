<script>
    // The engine's fixed layer (player-slot.js): the iframe and the clip
    // card, positioned over the Player pane's placeholder once per frame
    // while the tab shows (a style write only when the box changed) and
    // clipped to what of the placeholder its scrolling ancestors show. No
    // placeholder: hidden — the engine runs on, it is the tab's clock.
    // While the pointer is down elsewhere (a pane drag, a resize, the split)
    // and during a pane drag-and-drop the layer lets events through, so the
    // iframe does not swallow them. The wheel over the picture scrolls the
    // column under it, as it would over any pane (a fixed layer's iframe
    // hands unused wheel events to the viewport, which does not scroll).
    import { onMount } from 'svelte';
    import PlayerFrame from './PlayerFrame.svelte';
    import { activeTab } from '../state/index.js';
    import { engine } from './index.js';
    import { playerSlot, slotBox, clipAncestors } from './player-slot.js';
    import { paneLayout } from './layout.js';

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

        // The engine's iframe gets a wheel listener once its page is in
        // (same origin); a new demo is a new iframe.
        /** @type {HTMLIFrameElement | null} */
        let hooked = null;
        function hookWheel() {
            const f = engine.iframe;
            if (!f || f === hooked || f.contentDocument?.readyState !== 'complete') return;
            hooked = f;
            f.contentWindow?.addEventListener('wheel', (/** @type {WheelEvent} */ e) => {
                const px = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaMode === 2 ? e.deltaY * 400 : e.deltaY;
                const scroller = clips.find((c) => c.scrollHeight > c.clientHeight + 1
                    && /auto|scroll/.test(getComputedStyle(c).overflowY));
                scroller?.scrollBy({ top: px });
            }, { passive: true });
        }

        function frame() {
            raf = requestAnimationFrame(frame);
            hookWheel();
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
        // A pane height set or reset makes the pane body a scroller or not.
        const offLayout = paneLayout.subscribe(() => { clipsOf = null; });
        const offTab = activeTab.subscribe((tab) => {
            cancelAnimationFrame(raf);
            raf = 0;
            clipsOf = null;
            if (tab === 'player') raf = requestAnimationFrame(frame);
        });
        const refresh = () => { clipsOf = null; };
        window.addEventListener('resize', refresh);
        document.addEventListener('fullscreenchange', refresh);

        // A drag-and-drop cancels its pointer right after dragstart: only its
        // own end releases it.
        let dnd = false;
        /** @param {PointerEvent} e */
        const down = (e) => { if (!layer.contains(/** @type {Node} */ (e.target))) passThrough = true; };
        const release = () => { if (!dnd) passThrough = false; };
        const dragEnd = () => { dnd = false; passThrough = false; };
        document.addEventListener('pointerdown', down, true);
        document.addEventListener('pointerup', release, true);
        document.addEventListener('pointercancel', release, true);
        const dragging = () => { dnd = true; passThrough = true; };
        document.addEventListener('dragstart', dragging, true);
        document.addEventListener('dragend', dragEnd, true);
        document.addEventListener('drop', dragEnd, true);
        // A context menu can take the pointerup with it.
        document.addEventListener('contextmenu', release, true);
        return () => {
            cancelAnimationFrame(raf);
            offSlot();
            offLayout();
            offTab();
            window.removeEventListener('resize', refresh);
            document.removeEventListener('fullscreenchange', refresh);
            document.removeEventListener('pointerdown', down, true);
            document.removeEventListener('pointerup', release, true);
            document.removeEventListener('pointercancel', release, true);
            document.removeEventListener('dragstart', dragging, true);
            document.removeEventListener('dragend', dragEnd, true);
            document.removeEventListener('drop', dragEnd, true);
            document.removeEventListener('contextmenu', release, true);
        };
    });
</script>

<div class="player-layer" class:pass-through={passThrough} bind:this={layer} style="visibility:hidden">
    <PlayerFrame {engine} />
</div>
