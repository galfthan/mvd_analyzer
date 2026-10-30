<script>
    // The tab list.
    import { TABS, activeTab, selectTab } from '../state/ui.js';
</script>

<nav class="sidebar" aria-label="Primary">
    {#each TABS as tab (tab.id)}
        <button class="sidebar-btn" class:active={$activeTab === tab.id} data-tab={tab.id}
            title={tab.title} onclick={() => selectTab(tab.id)}>
            <span class="sidebar-btn-label">{tab.label}</span>
        </button>
    {/each}
</nav>
