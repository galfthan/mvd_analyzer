<script>
    // The page shell: top bar, sidebar, and every tab in the scrolling
    // pane (src/app/tabs.js shows the active one), with the unified
    // playback bar above it. The Search tab comes last, where the legacy
    // markup it replaced was put.
    import Topbar from './Topbar.svelte';
    import Sidebar from './Sidebar.svelte';
    import PlayerTab from '../player/PlayerTab.svelte';
    import SummaryTab from '../tabs/summary/SummaryTab.svelte';
    import ChatTab from '../tabs/chat/ChatTab.svelte';
    import PackDropsTab from '../tabs/packdrops/PackDropsTab.svelte';
    import PickupsTab from '../tabs/pickups/PickupsTab.svelte';
    import KeyMomentsTab from '../tabs/keymoments/KeyMomentsTab.svelte';
    import AimStatsTab from '../tabs/aimstats/AimStatsTab.svelte';
    import LocGraphTab from '../tabs/locgraph/LocGraphTab.svelte';
    import TimelineTab from '../tabs/timeline/TimelineTab.svelte';
    import SearchTab from '../tabs/search/SearchTab.svelte';
    import PlaybackBar from '../ui/PlaybackBar.svelte';
    import Toast from '../player/Toast.svelte';
</script>

<Topbar />
<div class="layout">
    <Sidebar />
    <main class="main">
        <PlaybackBar />
        <div class="main-scroll">
            <SummaryTab />
            <ChatTab />
            <PackDropsTab />
            <PickupsTab />
            <KeyMomentsTab />
            <AimStatsTab />
            <LocGraphTab />
            <TimelineTab />
            <PlayerTab />
            <SearchTab />
        </div>
    </main>
</div>
<Toast />
