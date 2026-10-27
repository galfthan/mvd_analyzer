<script>
    // The page shell. Tabs not yet ported are app.js's markup, kept in
    // index.html's #legacy-dom and adopted into place here before app.js's
    // DOMContentLoaded setup looks them up by id (src/main.js flushes the
    // mount synchronously).
    import Topbar from './Topbar.svelte';
    import Sidebar from './Sidebar.svelte';
    import PlayerTab from '../player/PlayerTab.svelte';
    import ChatTab from '../tabs/chat/ChatTab.svelte';
    import PackDropsTab from '../tabs/packdrops/PackDropsTab.svelte';
    import Toast from '../player/Toast.svelte';
    import { adopt } from './adopt.js';
</script>

<Topbar />
<div class="layout">
    <Sidebar />
    <main class="main" use:adopt={{ selector: '#legacy-dom > #unified-timeline', first: true }}>
        <div class="main-scroll" use:adopt={{ selector: '#legacy-dom > .tab-content' }}>
            <ChatTab />
            <PackDropsTab />
            <PlayerTab />
        </div>
    </main>
</div>
<Toast />
