<script>
    // A tab's panel bar, above its panels: reopen a closed panel, show
    // everything (reopen and expand all), or go back to the default layout.
    // Empty — and hidden — while the tab is in its default layout.
    import Eye from '@lucide/svelte/icons/eye';
    import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
    import { panelLayouts, updatePanels, showAll, resetPanels, isDefault } from './layout.js';

    let { tab, titles } = $props();

    let layout = $derived($panelLayouts[tab]);
    let closed = $derived(layout.order.filter((id) => layout.closed[id]));
    let hiddenCount = $derived(closed.length + Object.keys(layout.collapsed).length);

    function add(e) {
        const id = e.currentTarget.value;
        e.currentTarget.value = '';
        if (id) updatePanels(tab, (l) => { delete l.closed[id]; delete l.collapsed[id]; });
    }
</script>

{#if !isDefault(tab, layout)}
    <div class="lp-bar">
        {#if closed.length}
            <select class="pt-select" title="Reopen a closed panel" onchange={add}>
                <option value="">Add panel…</option>
                {#each closed as id (id)}
                    <option value={id}>{titles[id]}</option>
                {/each}
            </select>
        {/if}
        {#if hiddenCount}
            <button class="pt-btn" title="Reopen every closed panel and expand every collapsed one" onclick={() => showAll(tab)}>
                <Eye size={14} /> Show all
            </button>
        {/if}
        <button class="pt-btn icon" title="Default layout: every panel open, in the original order" onclick={() => resetPanels(tab)}>
            <RotateCcw size={14} />
        </button>
    </div>
{/if}
