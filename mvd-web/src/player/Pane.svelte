<script>
    // One pane in a column: a header (drag to reorder or onto the other
    // column, click the title to collapse, ↑ ↓, move across, ×) and a body
    // whose height the bottom handle resizes.
    import GripVertical from '@lucide/svelte/icons/grip-vertical';
    import ChevronDown from '@lucide/svelte/icons/chevron-down';
    import ChevronRight from '@lucide/svelte/icons/chevron-right';
    import ArrowUp from '@lucide/svelte/icons/arrow-up';
    import ArrowDown from '@lucide/svelte/icons/arrow-down';
    import X from '@lucide/svelte/icons/x';
    import PanelRight from '@lucide/svelte/icons/panel-right';
    import PanelBottom from '@lucide/svelte/icons/panel-bottom';
    import { paneLayout, updateLayout, movePane } from './layout.js';

    /** @type {{ id: string, title: string, column: import('./layout.js').Column, index: number, count: number, children: import('svelte').Snippet }} */
    let { id, title, column, index, count, children } = $props();
    let other = $derived(/** @type {import('./layout.js').Column} */ (column === 'order' ? 'below' : 'order'));

    let collapsed = $derived(!!$paneLayout.collapsed[id]);
    let height = $derived($paneLayout.heights[id] ?? null);
    let dragOver = $state(false);
    /** @type {HTMLElement | undefined} */
    let body = $state();

    function toggle() {
        updateLayout((l) => { l.collapsed[id] = !l.collapsed[id]; });
    }

    function close() {
        updateLayout((l) => { l[column] = l[column].filter((x) => x !== id); });
    }

    // A drag event carries dataTransfer.
    /** @param {DragEvent} e */
    function dragStart(e) {
        const dt = /** @type {DataTransfer} */ (e.dataTransfer);
        dt.setData('text/x-mvd-pane', id);
        dt.effectAllowed = 'move';
    }

    /** @param {DragEvent} e */
    function dragOverPane(e) {
        if (!/** @type {DataTransfer} */ (e.dataTransfer).types.includes('text/x-mvd-pane')) return;
        e.preventDefault();
        dragOver = true;
    }

    /** @param {DragEvent} e */
    function drop(e) {
        dragOver = false;
        const from = /** @type {DataTransfer} */ (e.dataTransfer).getData('text/x-mvd-pane');
        if (!from || from === id) return;
        e.preventDefault();
        movePane(from, column, index);
    }

    // Bottom handle: drag to set the body height; double-click resets it to
    // the pane's natural height.
    /** @param {PointerEvent} e */
    function resizeStart(e) {
        e.preventDefault();
        const startY = e.clientY;
        // The handle renders with the body it resizes.
        const startH = /** @type {HTMLElement} */ (body).getBoundingClientRect().height;
        /** @param {PointerEvent} ev */
        const move = (ev) => {
            const h = Math.max(60, Math.round(startH + ev.clientY - startY));
            updateLayout((l) => { l.heights[id] = h; });
        };
        const up = () => {
            window.removeEventListener('pointermove', move);
            window.removeEventListener('pointerup', up);
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', up);
    }

    function resizeReset() {
        updateLayout((l) => { delete l.heights[id]; });
    }
</script>

<div class="player-pane" class:drag-over={dragOver} data-pane={id} role="group" aria-label={title}
    ondragover={dragOverPane} ondragleave={() => (dragOver = false)} ondrop={drop}>
    <div class="player-pane-head" draggable="true" ondragstart={dragStart} role="toolbar" tabindex="-1">
        <span class="grip" title="Drag to reorder"><GripVertical size={14} /></span>
        <button class="chev" title={collapsed ? 'Expand' : 'Collapse'} onclick={toggle}>
            {#if collapsed}<ChevronRight size={14} />{:else}<ChevronDown size={14} />{/if}
        </button>
        <span class="title" role="button" tabindex="-1" onclick={toggle} onkeydown={() => {}}>{title}</span>
        <span class="acts">
            <button title="Move up" disabled={index === 0} onclick={() => movePane(id, column, index - 1)}><ArrowUp size={14} /></button>
            <button title="Move down" disabled={index === count - 1} onclick={() => movePane(id, column, index + 1)}><ArrowDown size={14} /></button>
            <button title={column === 'order' ? 'Move under the player' : 'Move beside the player'} onclick={() => movePane(id, other, $paneLayout[other].length)}>
                {#if column === 'order'}<PanelBottom size={14} />{:else}<PanelRight size={14} />{/if}
            </button>
            <button title="Close pane" onclick={close}><X size={14} /></button>
        </span>
    </div>
    {#if !collapsed}
        <div class="player-pane-body" bind:this={body} class:sized={height != null}
            style:height={height != null ? `${height}px` : null}>
            {@render children()}
        </div>
        <div class="pane-resize" role="separator" aria-orientation="horizontal"
            title="Drag to resize · double-click: natural height"
            onpointerdown={resizeStart} ondblclick={resizeReset}></div>
    {/if}
</div>

<style>
    .player-pane-body.sized {
        overflow: auto;
    }
    .pane-resize {
        height: 5px;
        cursor: ns-resize;
        border-top: 1px solid transparent;
    }
    .pane-resize:hover {
        border-top-color: var(--accent-cyan);
    }
    .grip {
        display: inline-flex;
        color: #667;
        cursor: grab;
    }
    .acts {
        display: inline-flex;
        gap: 2px;
        opacity: 0.35;
        transition: opacity 0.15s;
    }
    .player-pane-head:hover .acts,
    .acts:focus-within {
        opacity: 1;
    }
    button:disabled {
        opacity: 0.3;
        cursor: default;
    }
</style>
