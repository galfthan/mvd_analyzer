<script>
    // One pane in the column: a header (drag to reorder, click the title to
    // collapse, ↑ ↓ ×) and a body whose height the bottom handle resizes.
    import { paneLayout, updateLayout, movePane } from './layout.js';

    let { id, title, index, count, children } = $props();

    let collapsed = $derived(!!$paneLayout.collapsed[id]);
    let height = $derived($paneLayout.heights[id] ?? null);
    let dragOver = $state(false);
    let body = $state();

    function toggle() {
        updateLayout((l) => { l.collapsed[id] = !l.collapsed[id]; });
    }

    function close() {
        updateLayout((l) => { l.order = l.order.filter((x) => x !== id); });
    }

    function dragStart(e) {
        e.dataTransfer.setData('text/x-mvd-pane', id);
        e.dataTransfer.effectAllowed = 'move';
    }

    function dragOverPane(e) {
        if (!e.dataTransfer.types.includes('text/x-mvd-pane')) return;
        e.preventDefault();
        dragOver = true;
    }

    function drop(e) {
        dragOver = false;
        const from = e.dataTransfer.getData('text/x-mvd-pane');
        if (!from || from === id) return;
        e.preventDefault();
        movePane(from, index);
    }

    // Bottom handle: drag to set the body height; double-click resets it to
    // the pane's natural height.
    function resizeStart(e) {
        e.preventDefault();
        const startY = e.clientY;
        const startH = body.getBoundingClientRect().height;
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
        <button class="chev" title={collapsed ? 'Expand' : 'Collapse'} onclick={toggle}>{collapsed ? '▸' : '▾'}</button>
        <span class="title" role="button" tabindex="-1" onclick={toggle} onkeydown={() => {}}>{title}</span>
        <button title="Move up" disabled={index === 0} onclick={() => movePane(id, index - 1)}>↑</button>
        <button title="Move down" disabled={index === count - 1} onclick={() => movePane(id, index + 1)}>↓</button>
        <button title="Close pane" onclick={close}>×</button>
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
        height: 6px;
        cursor: ns-resize;
        border-top: 1px solid transparent;
    }
    .pane-resize:hover {
        border-top-color: var(--accent-cyan);
    }
    .chev {
        width: 1.2em;
    }
    button:disabled {
        opacity: 0.3;
        cursor: default;
    }
</style>
