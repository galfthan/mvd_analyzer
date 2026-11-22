<script>
    // The wordmark and what is loaded: the demo line (src/lib/topbar.js,
    // computed by the load), or — for a demo that is one map of a match
    // (src/state/match.js) — the match's score and its maps as tabs
    // (MatchTabs.svelte). The build version and the links are at the
    // sidebar's foot (Sidebar.svelte).
    import { topbarDemo } from '../state/shell.js';
    import { currentMatch } from '../state/match.js';
    import { demo } from '../state/demo.js';
    import MatchTabs from './MatchTabs.svelte';

    const match = $derived($currentMatch?.sides ? $currentMatch : null);
    const shownId = $derived(String($demo?.game?.id ?? ''));
    const index = $derived(match ? match.games.findIndex(g => String(g.id) === shownId) : -1);
</script>

<header class="topbar">
    <a class="topbar-brand" href="./" title="Reset and reload">MVD Analyzer</a>
    {#if match && index >= 0}
        <MatchTabs {match} {index} />
    {:else}
        <div class="topbar-demo-info" id="topbar-demo-info">{@html $topbarDemo}</div>
    {/if}
</header>
