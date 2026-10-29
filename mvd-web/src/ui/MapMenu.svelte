<script>
    // The map's right-click menu (the map panes of the Map and Player tabs): a list of items (checks, radios, actions, separators) at a
    // point of its container, kept inside it. A click on an item runs it
    // and leaves the menu open (so several overlays can be toggled in a
    // row) unless the item closes it; a click elsewhere or Escape closes.
    // Each host builds its own items from its own state, and opens the menu
    // on a right-click that did not drag (an orbit that went nowhere).

    /** @typedef {import('./map-menu.js').MenuItem} MenuItem */

    /** @type {{ at: { x: number, y: number } | null, items: MenuItem[], keep?: string }} */
    let { at = $bindable(), items, keep = '' } = $props();

    /** @type {HTMLDivElement | undefined} */
    let el = $state();

    // Inside the container: the menu's own size against its parent's.
    let pos = $derived.by(() => {
        if (!at) return null;
        const parent = el?.parentElement;
        const w = el?.offsetWidth || 200;
        const h = el?.offsetHeight || 280;
        const pw = parent?.clientWidth || Infinity;
        const ph = parent?.clientHeight || Infinity;
        return { x: Math.max(4, Math.min(at.x, pw - w - 4)), y: Math.max(4, Math.min(at.y, ph - h - 4)) };
    });

    $effect(() => {
        if (!at) return;
        // `keep`: a selector for the host's own button that toggles the menu.
        const close = (/** @type {Event} */ e) => {
            if (e instanceof KeyboardEvent) {
                if (e.key === 'Escape') at = null;
                return;
            }
            const t = /** @type {Element} */ (e.target);
            if (!el?.contains(t) && !(keep && t.closest?.(keep))) at = null;
        };
        window.addEventListener('pointerdown', close, true);
        window.addEventListener('keydown', close);
        return () => {
            window.removeEventListener('pointerdown', close, true);
            window.removeEventListener('keydown', close);
        };
    });
</script>

{#if at && pos}
    <div class="map-menu" role="menu" bind:this={el} style:left="{pos.x}px" style:top="{pos.y}px">
        {#each items as it, i (i)}
            {#if it.kind === 'sep'}
                <hr>
            {:else if it.kind === 'action'}
                <button role="menuitem" title={it.title} onclick={() => { it.run(); if (it.close) at = null; }}><span class="tick"></span>{it.label}</button>
            {:else}
                <button role={it.kind === 'check' ? 'menuitemcheckbox' : 'menuitemradio'} aria-checked={it.on} title={it.title}
                    onclick={() => it.run()}><span class="tick">{it.on ? (it.kind === 'check' ? '✓' : '•') : ''}</span>{it.label}</button>
            {/if}
        {/each}
    </div>
{/if}

<style>
    .map-menu {
        position: absolute;
        z-index: 20;
        min-width: 190px;
        display: flex;
        flex-direction: column;
        padding: 4px 0;
        background: #10162a;
        border: 1px solid var(--bg-panel);
        border-radius: 4px;
        box-shadow: 0 6px 18px rgba(0, 0, 0, 0.5);
        font-size: 12px;
    }
    .map-menu button {
        display: flex;
        align-items: center;
        gap: 4px;
        padding: 4px 10px 4px 6px;
        background: none;
        border: 0;
        color: var(--fg-default);
        font: inherit;
        text-align: left;
        cursor: pointer;
    }
    .map-menu button:hover {
        background: #34507a;
    }
    .tick {
        width: 14px;
        color: var(--accent-cyan);
    }
    hr {
        width: 100%;
        margin: 3px 0;
        border: 0;
        border-top: 1px solid var(--bg-panel);
    }
</style>
