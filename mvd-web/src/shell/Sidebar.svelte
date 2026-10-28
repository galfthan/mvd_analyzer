<script>
    // The tab list; a tab that can go fullscreen (src/app/fullscreen.js)
    // shows the icon for it on its row while it is the active tab.
    import Maximize from '@lucide/svelte/icons/maximize';
    import { TABS, activeTab, selectTab } from '../state/ui.js';
    import { FULLSCREEN_PANELS, toggleFullscreen } from '../app/fullscreen.js';
</script>

<nav class="sidebar" aria-label="Primary">
    {#each TABS as tab (tab.id)}
        <div class="sidebar-row">
            <button class="sidebar-btn" class:active={$activeTab === tab.id} data-tab={tab.id}
                title={tab.title} onclick={() => selectTab(tab.id)}>
                <span class="sidebar-btn-label">{tab.label}</span>
            </button>
            {#if $activeTab === tab.id && Object.hasOwn(FULLSCREEN_PANELS, tab.id)}
                <button class="sidebar-fs" title="Fullscreen this tab (f) — Esc leaves" aria-label="Fullscreen {tab.label}"
                    onclick={() => toggleFullscreen(tab.id)}><Maximize size={15} /></button>
            {/if}
        </div>
    {/each}
</nav>

<style>
    .sidebar-row {
        position: relative;
    }
    .sidebar-fs {
        position: absolute;
        right: 6px;
        top: 50%;
        transform: translateY(-50%);
        display: inline-flex;
        padding: 4px;
        background: transparent;
        border: 1px solid transparent;
        border-radius: 4px;
        color: var(--accent-cyan);
        cursor: pointer;
    }
    .sidebar-fs:hover {
        border-color: var(--accent-cyan);
        background: rgba(0, 217, 255, 0.1);
    }
    /* The tab list as a row (styles.css): the icon follows its tab. */
    @media (max-width: 800px) {
        .sidebar-row {
            display: flex;
            align-items: center;
            flex-shrink: 0;
        }
        .sidebar-fs {
            position: static;
            transform: none;
        }
    }
</style>
