<script>
    // The controls in a panel's top-right corner: drag the grip onto another
    // panel to reorder, collapse to the heading, move up / down, close.
    // Mounted into the panel element itself by setupPanels (index.js).
    import GripVertical from '@lucide/svelte/icons/grip-vertical';
    import ChevronDown from '@lucide/svelte/icons/chevron-down';
    import ChevronRight from '@lucide/svelte/icons/chevron-right';
    import ArrowUp from '@lucide/svelte/icons/arrow-up';
    import ArrowDown from '@lucide/svelte/icons/arrow-down';
    import X from '@lucide/svelte/icons/x';
    import { panelLayouts, updatePanels } from './layout.js';

    let { tab, id, title, step } = $props();

    let collapsed = $derived(!!$panelLayouts[tab].collapsed[id]);

    function toggle() {
        updatePanels(tab, (l) => { l.collapsed[id] = !l.collapsed[id]; });
    }

    function close() {
        updatePanels(tab, (l) => { l.closed[id] = true; });
    }

    function dragStart(e) {
        e.dataTransfer.setData('text/x-mvd-panel', `${tab}:${id}`);
        e.dataTransfer.effectAllowed = 'move';
        const panel = e.currentTarget.closest('[data-panel]');
        if (panel) e.dataTransfer.setDragImage(panel, 20, 20);
    }
</script>

<div class="lp-controls" role="toolbar" aria-label="{title} panel">
    <span class="lp-grip" draggable="true" ondragstart={dragStart} title="Drag onto another panel to reorder"
        role="button" tabindex="-1"><GripVertical size={14} /></span>
    <button title={collapsed ? 'Expand' : 'Collapse to the heading'} onclick={toggle}>
        {#if collapsed}<ChevronRight size={14} />{:else}<ChevronDown size={14} />{/if}
    </button>
    <button title="Move up" onclick={() => step(-1)}><ArrowUp size={14} /></button>
    <button title="Move down" onclick={() => step(1)}><ArrowDown size={14} /></button>
    <button title="Close panel (Add panel… or Show all brings it back)" onclick={close}><X size={14} /></button>
</div>
