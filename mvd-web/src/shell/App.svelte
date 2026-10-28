<script>
    // The page shell. The tab not yet ported (Search) is app.js's markup,
    // kept in index.html's #legacy-dom and adopted into place here before
    // app.js's DOMContentLoaded setup looks it up by id (src/main.js flushes
    // the mount synchronously). The unified playback bar sits above the
    // scrolling pane.
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
    import MapTab from '../tabs/map/MapTab.svelte';
    import PlaybackBar from '../ui/PlaybackBar.svelte';
    import Toast from '../player/Toast.svelte';
    import { adopt } from './adopt.js';
</script>

<Topbar />
<div class="layout">
    <Sidebar />
    <main class="main">
        <PlaybackBar />
        <div class="main-scroll" use:adopt={{ selector: '#legacy-dom > .tab-content' }}>
            <SummaryTab />
            <ChatTab />
            <PackDropsTab />
            <PickupsTab />
            <KeyMomentsTab />
            <AimStatsTab />
            <LocGraphTab />
            <TimelineTab />
            <PlayerTab />
            <MapTab />
        </div>
    </main>
</div>
<Toast />
