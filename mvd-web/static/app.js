// MVD Analyzer Dashboard — Pure client-side via WASM

// ─── Theme & Constants ──────────────────────────────────────────────────────
//
// Single source of truth for colours, magic numbers and shared layout values.
// Anything that used to be a literal sprinkled across the file lives here so
// it can be tweaked in one place — including the values that are duplicated
// in styles.css as :root custom properties (kept in sync by hand: see the
// matching --team-a / --armor-* / --accent-cyan declarations in styles.css).
// Team colours live in src/state/teams.js (MvdState) — the one team → colour
// mapping, chosen by team NAME (see CLAUDE.md "Team colors"). TEAM_COLORS and
// timelineState.teams are app.js's mirror of it, refreshed by onTeamsChanged:
// TEAM_COLORS[i] is the colour of timelineState.teams[i], the winner-first
// canonical order every call site indexes by.
let TEAM_COLORS = [];

// setCanonicalTeams sets the match's canonical team order (once per demo, from
// displayResults). In an individual layout with more than two players
// (`perPlayer`) every player is their own side and is coloured from the
// player palette.
function setCanonicalTeams(teams, perPlayer = false) {
    const players = [...(currentResult?.match?.players || []), ...(currentResult?.demoInfo?.players || [])];
    MvdState.setCanonicalTeams(teams, perPlayer, players);
}

function onTeamsChanged(state) {
    timelineState.teams = state.order;
    TEAM_COLORS = state.colors;
    if (mapView) mapView.teamColors = TEAM_COLORS;
    const rootStyle = document.documentElement.style;
    ['--team-a', '--team-b', '--team-c', '--team-d'].forEach((v, i) =>
        rootStyle.setProperty(v, TEAM_COLORS[i]));
}
const PLAYER_SYMBOLS = ['*', 'x', '+', 'o', '◆', '▲', '●', '■'];

const PLAYBACK_FPS_MS   = 16;   // map playback throttle (~60 fps = 16 ms/frame)

let currentResult = null;

// ─── Data source ────────────────────────────────────────────────────────────

// Demos load through the data layer (src/data/, published as window.mvdData
// and window.MvdHub by src/main.js). Module scripts run after this file's top
// level, so only functions may touch them. `currentDemo` is the DemoHandle
// behind `currentResult`: its view queries, region recompute and line of
// sight answer for that demo only.
let currentDemo = null;
let analyzerFailed = false;

// ─── Load-timing instrumentation ────────────────────────────────────────────
// Structured per-stage timing for the demo load path, printed to the console
// (see mvd-web/README.md). `mvdTiming` holds one-time facts (wasm load);
// `loadTiming` is a per-demo accumulator built up across network → WASM →
// render and flushed by finishLoadTiming(). Also stashed on window.__mvdTimings.
let mvdTiming = { wasmLoadMs: null };
let loadTiming = null;

function startLoadTiming() {
    loadTiming = { t0: performance.now(), net: {}, render: [], worker: null, parseMs: 0 };
}
function timeRender(name, fn) {
    if (!loadTiming) return fn();
    const t = performance.now();
    try {
        return fn();
    } finally {
        loadTiming.render.push({ stage: name, ms: +(performance.now() - t).toFixed(2) });
    }
}
function finishLoadTiming() {
    if (!loadTiming) return;
    const lt = loadTiming;
    loadTiming = null; // close the window before any async render fires
    const total = performance.now() - lt.t0;
    const summary = {
        wasmLoadMs: mvdTiming.wasmLoadMs,
        network: lt.net,
        worker: lt.worker,
        parseMs: +lt.parseMs.toFixed(1),
        render: lt.render,
        downloadToUiMs: +total.toFixed(1),
    };
    window.__mvdTimings = summary;
    try {
        console.groupCollapsed(`[mvd-timing] load breakdown — ${total.toFixed(0)} ms download→UI`);
        if (mvdTiming.wasmLoadMs != null) {
            console.log(`wasm load (one-time): ${mvdTiming.wasmLoadMs.toFixed(1)} ms`);
        }
        console.log('network (ms):', lt.net);
        if (lt.worker) {
            const w = lt.worker;
            const locMs = (w.locFetch || []).reduce((s, f) => s + f.ms, 0);
            const bspMs = (w.bspFetch || []).reduce((s, f) => s + f.ms, 0);
            console.log(
                `WASM analyze: ${w.wasmAnalyzeMs.toFixed(1)} ms ` +
                `(incl. loc fetch ${locMs.toFixed(1)} ms, bsp fetch ${bspMs.toFixed(1)} ms — ` +
                `subtract from finalize:timelineAnalysis for pure loc compute)`
            );
            console.table((w.goPhases || []).map(p => ({ phase: p.name, ms: +p.ms.toFixed(2) })));
        }
        console.log(`result JSON.parse (main thread): ${lt.parseMs.toFixed(1)} ms`);
        console.table(lt.render);
        console.groupEnd();
    } catch (e) {
        console.warn('[mvd-timing] log failed:', e);
    }
}

// Hide the wasm-loading overlay. When auto-loading a demo from a URL
// (?gameId=...), keep it up through the demo download/analyse so the
// user never sees a half-populated Search/Summary tab in the
// background — displayResults() calls hideLoadingOverlay() once the
// pipeline has finished.
function hideLoadingOverlay() {
    const overlay = document.getElementById('wasm-loading');
    if (overlay) overlay.style.display = 'none';
}

// Version handshake: `make build` stamps <meta name="build"> in index.html
// with the git hash, and the wasm reports the hash it was built from. Both
// deploy as one unit, so a mismatch means the browser is running mixed
// cached versions (a deploy landed between fetching the page and the wasm,
// or a cache served a stale asset). Reload once — assets are served
// Cache-Control: no-cache, so the reload revalidates everything — and if
// the mismatch survives the reload (deploy still propagating), just flag it
// in the version tag instead of looping.
function checkBuildCoherence(v) {
    const meta = document.querySelector('meta[name="build"]');
    const pageBuild = meta ? meta.content : '';
    const wasmBuild = v && v.hash ? v.hash : '';
    if (!pageBuild || pageBuild === 'dev') return;
    if (!wasmBuild || wasmBuild === 'dev' || wasmBuild === 'unknown') return;
    if (pageBuild === wasmBuild) return;

    const guard = 'mvd-build-reload';
    const attempted = (() => { try { return sessionStorage.getItem(guard); } catch { return null; } })();
    const key = `${pageBuild}:${wasmBuild}`;
    if (attempted !== key) {
        try { sessionStorage.setItem(guard, key); } catch {}
        location.reload();
        return;
    }
    const tag = document.getElementById('version-tag');
    if (tag) {
        tag.appendChild(document.createTextNode(' — new version available, reload the page'));
    }
}

function setLoadingOverlayMessage(text) {
    const overlay = document.getElementById('wasm-loading');
    if (!overlay) return;
    // The subtitle is the second flex child (the small status line).
    const subtitle = overlay.children[1];
    if (subtitle) subtitle.textContent = text;
}

// onAnalyzerReady runs once the wasm is up (mvdData.ready): records its
// load time, drops the overlay unless a deep-linked demo is about to load,
// and fills the version tag.
function onAnalyzerReady(info) {
    if (typeof info.wasmLoadMs === 'number') mvdTiming.wasmLoadMs = info.wasmLoadMs;
    const params = new URLSearchParams(location.search);
    const willAutoLoadDemo = !!(params.get('gameId') || params.get('hub'));
    if (willAutoLoadDemo) {
        // Demo download is about to start; keep the overlay up and
        // show the next phase.
        setLoadingOverlayMessage('Loading demo…');
    } else {
        hideLoadingOverlay();
    }
    const v = info.version;
    const tag = document.getElementById('version-tag');
    if (tag && v) {
        tag.textContent = `${v.tag} (`;
        const a = document.createElement('a');
        a.href = `https://github.com/galfthan/mvd_analyzer/commit/${encodeURIComponent(v.hash)}`;
        a.target = '_blank';
        a.rel = 'noopener';
        a.textContent = v.hash;
        tag.appendChild(a);
        tag.appendChild(document.createTextNode(`) — ${v.date}`));
    }
    checkBuildCoherence(v);
}

// onBucketsReady runs when the current demo's deferred 50 ms bucket view and
// region states have landed on its Result (DemoHandle.ready('buckets')), and
// RE-RUNS the bucket-dependent initialisers (region control, the bucket view
// the map draws, the map). They already ran once in displayResults with the
// bucket-derived fields empty (blank map trails / region overlay);
// re-running them now fills those in. Finally re-render the active tab so a
// Map view the user already opened repaints. (The Timeline tab,
// src/tabs/timeline/, redraws itself when the buckets land.)
function onBucketsReady(data) {
    const ta = currentResult.timelineAnalysis;

    // Re-run the inits, now that bucket-derived data is present. Order
    // matches displayResults: region control feeds the map.
    if (ta && ta.regionControl) initRegionControlData(currentResult);
    if (ta || currentResult.messages?.messages) initTimeline(currentResult);
    if (ta) initMapView(currentResult);

    // Re-render whichever tab is active so the Map paints with the new
    // data (re-selecting re-runs the tab's render path; harmless on Summary).
    switchTab(activeTabName());

    if (typeof data.bucketsMs === 'number') {
        const deferred = (data.bucketsMs + (data.regionMs || 0)).toFixed(0);
        console.log(`[mvd-timing] Timeline/Map ready — ${deferred} ms of bucket work ran off the critical path`);
    }
}

// ─── Setup ──────────────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
    // Shared state (src/state/) → app.js's mirrors. Both stores call their
    // subscriber at once; the clock's first call (t = 0, nothing loaded) is
    // not a tick.
    MvdState.teams.subscribe(onTeamsChanged);
    MvdState.regionControl.subscribe(onRegionControlChanged);
    let clockSubscribed = false;
    MvdState.clock.subscribe((tick) => { if (clockSubscribed) onClockTick(tick); });
    MvdState.segment.subscribe((seg) => { if (clockSubscribed) onSegmentChanged(seg); });
    clockSubscribed = true;
    setupFileUpload();
    setupTabs();
    MvdPlayer.status.subscribe(() => updatePlaybackButtons());
    setupSearch();

    const params = new URLSearchParams(location.search);
    const hubId = params.get('gameId') || params.get('hub');
    const requestedTab = params.get('tab');

    // Pick the initial active tab. When deep-linking to a demo we want
    // the destination tab to be active even before the demo finishes
    // loading, so the wasm-loading overlay covers the right pane and
    // the user never glimpses the Search panel mid-flight.
    if (hubId) {
        switchTab(requestedTab || 'summary');
    } else if (requestedTab) {
        switchTab(requestedTab);
    } // else: leave the HTML default (Search) active.

    mvdData.ready.then(onAnalyzerReady, (err) => {
        analyzerFailed = true;
        setLoadingOverlayMessage(err.message);
    });

    // Auto-load demo if URL has ?gameId= (canonical) or ?hub= (legacy) param.
    // The load waits for the analyzer itself.
    if (hubId) {
        document.getElementById('hub-input').value = hubId;
        loadFromHub();
    }

    // Auto-populate search filters from the URL. Only auto-RUN the
    // query when there's no demo also being deep-loaded — when the
    // user shares e.g. ?gameId=…&player=nexus, the player filter is
    // incidental URL state from however they originally found the
    // demo, not a request to browse the search results.
    const urlFilters = {};
    let hasSearch = false;
    for (const f of MvdHub.SEARCH_FIELDS) {
        const v = params.get(f);
        if (v) { urlFilters[f] = v; hasSearch = true; }
    }
    if (hasSearch) {
        writeSearchFiltersToForm(urlFilters);
        if (!hubId) {
            if (!requestedTab) switchTab('search');
            runSearch();
        }
    }
});

// ─── Current Time ──────────────────────────────────────────────────────────

// setCurrentTime moves the page's one clock (MvdState.clock, src/state/
// clock.js); onClockTick then syncs every legacy view.
function setCurrentTime(time) {
    MvdState.clock.seek(time);
}

// onClockTick is app.js's clock subscriber. The map tab's playback loop
// ('playback') does its own lighter per-frame sync, and a demo reset
// ('reset') happens while the views are half torn down — both skip it.
// The Player engine ('engine') ticks once per display frame with the Map
// tab hidden (onTabActivated redraws it): move the cursor every
// frame, the rest at the playback loop's 200 ms full-sync rate. (The
// Timeline tab, src/tabs/timeline/, follows the clock itself.)
let _engineSyncTimer = null;
function onClockTick({ t, from }) {
    if (from === 'reset') {
        clearTimeout(_engineSyncTimer);
        _engineSyncTimer = null;
    }
    if (from === 'playback' || from === 'reset') return;
    mapState.currentTime = t;
    mapState.renderDirty = true;
    if (from === 'engine') {
        updateUnifiedCursor();
        updateUnifiedTimeDisplay();
        if (!_engineSyncTimer) {
            _engineSyncTimer = setTimeout(() => {
                _engineSyncTimer = null;
                updateMapLegend();
                updateRegionStatus();
                updateItemsPanelStatus(mapState.currentTime);
                updateUrlState();
            }, 200);
        }
        return;
    }
    updateUnifiedCursor();
    updateUnifiedTimeDisplay();
    updateMapLegend();
    updateRegionStatus();
    updateItemsPanelStatus(mapState.currentTime);
    renderMap(mapState.currentTime);
    updateUrlState();
}

// ─── URL State Sharing ─────────────────────────────────────────────────────

let _urlStateTimer = null;
let lastExecutedSearch = null;
function updateUrlState() {
    if (_urlStateTimer) return;
    _urlStateTimer = setTimeout(() => {
        _urlStateTimer = null;
        const params = new URLSearchParams();

        if (currentResult) {
            if (currentResult.hubInfo?.gameId) {
                params.set('gameId', currentResult.hubInfo.gameId);
            }

            const activeTab = activeTabName();
            if (activeTab && activeTab !== 'summary') {
                params.set('tab', TAB_INTERNAL_TO_URL[activeTab] || activeTab);
            }

            if (mapState.learnMode) {
                params.set('learn', '1');
            }

            if (mapState.currentTime > 0) {
                params.set('t', Math.round(mapState.currentTime));
            }

            const seg = MvdState.segment.get();
            if (seg) {
                params.set('seg', `${Math.round(seg.start)}-${Math.round(seg.end)}`);
            }
        }

        if (lastExecutedSearch) {
            for (const f of MvdHub.SEARCH_FIELDS) {
                const v = lastExecutedSearch[f];
                if (v) params.set(f, v);
            }
        }

        const qs = params.toString();
        if (qs) {
            history.replaceState(null, '', `?${qs}`);
        } else if (currentResult || lastExecutedSearch) {
            // We've actually loaded something but happen to have no
            // params worth keeping (e.g. fresh load on Summary at t=0
            // with no segment). Clear the URL.
            history.replaceState(null, '', location.pathname);
        }
        // else: nothing yet — leave the URL alone. There may still be
        // an in-flight ?gameId=… deep-link auto-load whose params we
        // would otherwise wipe before WASM finishes booting and
        // applyUrlState() can read them.
    }, 500);
}

function applyUrlState() {
    const params = new URLSearchParams(location.search);

    const seg = params.get('seg');
    if (seg) {
        const [start, end] = seg.split('-').map(Number);
        if (!isNaN(start) && !isNaN(end)) MvdState.segment.set({ start, end });
    }

    const t = params.get('t');
    if (t) {
        const time = Number(t);
        if (!isNaN(time)) {
            setCurrentTime(time);
        }
    }

    const tab = params.get('tab');
    if (tab) switchTab(tab); // resolves the locs-regions / loc-graph alias

    // Deep-link into the map's "learn" study view (only if this map has a
    // static entity corpus to show).
    if (params.get('learn') === '1' && mapState.mapEntities && mapState.mapEntities.length > 0) {
        setLearnMode(true);
    }

    updateUrlState();
}

function setupFileUpload() {
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');

    fileInput.addEventListener('change', (e) => {
        if (e.target.files.length > 0) {
            uploadFile(e.target.files[0]);
        }
    });

    dropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropZone.classList.add('dragover');
    });

    dropZone.addEventListener('dragleave', () => {
        dropZone.classList.remove('dragover');
    });

    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropZone.classList.remove('dragover');
        if (e.dataTransfer.files.length > 0) {
            uploadFile(e.dataTransfer.files[0]);
        }
    });
}

const TABS_WITH_TIMELINE = ['timeline', 'chat', 'map', 'keymoments', 'player'];

// Tab URL aliases. The loc tab's internal data-tab stayed "loc-graph", but
// the tab is now labelled "Locs & Regions" and the URL prefers the matching
// "locs-regions" slug. Old "loc-graph" links still resolve; new URLs are
// written as "locs-regions" (see updateUrlState).
const TAB_URL_TO_INTERNAL = { 'locs-regions': 'loc-graph' };
const TAB_INTERNAL_TO_URL = { 'loc-graph': 'locs-regions' };
function resolveTabName(name) { return TAB_URL_TO_INTERNAL[name] || name; }

function switchTab(name) {
    MvdState.selectTab(resolveTabName(name));
}

function activeTabName() {
    return MvdState.activeTab.get();
}

// The sidebar (src/shell/Sidebar.svelte) sets MvdState.activeTab; app.js
// follows it here. Setting the same tab again re-runs the tab's render path.
function setupTabs() {
    let subscribed = false;
    MvdState.activeTab.subscribe((tabName) => {
        if (subscribed) onTabActivated(tabName);
    });
    subscribed = true;
}

function onTabActivated(tabName) {
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    document.getElementById(`tab-${tabName}`).classList.add('active');

    // Show/hide the unified-timeline. It now lives outside the
    // scroll container as a sibling row of .main-scroll, so a
    // simple display toggle is all that's needed — no sticky,
    // no compositor games.
    const tl = document.getElementById('unified-timeline');
    if (tl && currentResult) {
        tl.style.display = TABS_WITH_TIMELINE.includes(tabName) ? '' : 'none';
    }

    // Stop playback when switching to tabs without timeline
    if (!TABS_WITH_TIMELINE.includes(tabName) && mapState.isPlaying) {
        stopPlayback();
    }

    // The Player tab's clock is the engine's: the map's own playback loop
    // must not run underneath it. (The engine follows the tab itself.)
    if (tabName === 'player' && mapState.isPlaying) stopPlayback();
    updatePlaybackButtons();

    // Sync views on tab switch.
    //
    // Canvases sized from container.clientWidth render empty
    // when first drawn while the tab was hidden (clientWidth
    // === 0 under display:none). Force a synchronous reflow
    // of the now-active tab content so the display:none →
    // block transition commits before we measure or draw.
    //
    // Even after that, Firefox specifically composites the
    // newly-revealed tab subtree through a layer snapshot for
    // a brief window — canvas writes during that window stay
    // invisible until the user does anything else. The two
    // backup re-renders (next frame + 120 ms) catch it
    // without us having to know exactly when the snapshot
    // releases. (Restructuring the layout to drop position:
    // sticky on the unified-timeline did not eliminate this
    // — the snapshot trigger appears to be the tab-content
    // display change itself, not the sticky element.)
    const tabContentEl = document.getElementById(`tab-${tabName}`);
    if (tabContentEl) void tabContentEl.offsetHeight;

    const renderForTab = () => renderTabContent(tabName);
    renderForTab();
    requestAnimationFrame(renderForTab);
    setTimeout(renderForTab, 120);

    updateUrlState();
}

// renderTabContent redraws a tab's canvases and size-dependent views: on
// activation (above), and when a panel of it is expanded or reopened
// (src/ui/panels) — a canvas drawn while hidden is drawn at zero width.
// Only the Map is left here; the ported tabs redraw themselves.
function renderTabContent(tabName) {
    if (tabName === 'map') {
        mapState.renderDirty = true;
        mapState.lastRenderedBucket = null;
        renderMap(mapState.currentTime);
    }
}

// ─── File Upload (via WASM Worker) ──────────────────────────────────────────

// The load-status line after a successful analyse. A demo the marker calls
// match-less analysed fine, so it is not an error (the red `status error` is
// reserved for a load or parse that failed) — but "Analysis complete!" over a
// Summary tab with no scoreboard reads as a bug, so it says what happened and
// points at the panel that explains it.
function setAnalysisStatus(status, result) {
    if (result.noMatch) {
        status.textContent = 'Analysed — this recording holds no match (see Summary).';
        status.className = 'status';
        return;
    }
    status.textContent = 'Analysis complete!';
    status.className = 'status success';
}

async function uploadFile(file) {
    const status = document.getElementById('upload-status');
    status.textContent = 'Analyzing...';
    status.className = 'status loading';
    try {
        await loadDemo({ bytes: new Uint8Array(await file.arrayBuffer()), name: file.name });
    } catch (error) {
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
    }
}

// ─── Hub Loading ────────────────────────────────────────────────────────────

async function loadFromHub() {
    const input = document.getElementById('hub-input').value.trim();
    if (!input) {
        alert('Please enter a game ID or URL');
        return;
    }

    const status = document.getElementById('upload-status');
    const btn = document.getElementById('hub-load-btn');
    status.textContent = 'Fetching from QuakeWorld Hub...';
    status.className = 'status loading';
    btn.disabled = true;

    try {
        await loadDemo({ gameId: MvdHub.parseGameId(input) });
    } catch (error) {
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
        // If we were holding the loading overlay up for a deep-link auto-load,
        // drop it so the user sees the error message instead of a stuck
        // spinner — unless the analyzer itself failed, which the overlay says.
        if (!analyzerFailed) hideLoadingOverlay();
    } finally {
        btn.disabled = false;
    }
}

const LOAD_STAGE_TEXT = {
    info: 'Fetching game info...',
    download: 'Downloading demo...',
    analyze: 'Analyzing...',
};

// loadDemo loads one demo through the data source (`input` is what
// WasmSource.load takes: a file's bytes, a hub row, or a hub game id) and
// renders it. Shared by the file drop, the hub box, search clicks and
// playlist runs; throws on failure for the caller to report.
async function loadDemo(input) {
    const status = document.getElementById('upload-status');
    status.className = 'status loading';
    const hubId = input.game?.id ?? input.gameId;
    if (hubId != null) document.getElementById('hub-input').value = hubId;
    // The timing window opens once the analyzer is up, so a deep link's
    // total does not include the wasm load (logged on its own).
    await mvdData.ready;
    startLoadTiming();
    const handle = await mvdData.load(input, {
        onStage: (stage) => { status.textContent = LOAD_STAGE_TEXT[stage]; },
    });
    if (loadTiming) {
        Object.assign(loadTiming.net, handle.timings.net);
        loadTiming.worker = handle.timings.worker;
        loadTiming.parseMs = handle.timings.parseMs;
    }

    setAnalysisStatus(status, handle.result);
    currentDemo = handle;
    currentResult = handle.result;
    MvdState.demo.set(handle);
    if (handle.game) {
        currentResult.hubInfo = {
            gameId: handle.game.id,
            viewerUrl: `https://hub.quakeworld.nu/games/?gameId=${handle.game.id}`,
            players: handle.game.players
        };
    }
    handle.ready('buckets').then(
        (info) => { if (handle === currentDemo) onBucketsReady(info); },
        (err) => console.warn('deferred buckets failed:', err.message));

    displayResults(currentResult);
}

// ─── Demo Search Panel ──────────────────────────────────────────────────────

function readSearchFilters() {
    return {
        player: document.getElementById('search-player').value.trim(),
        team:   document.getElementById('search-team').value.trim(),
        map:    document.getElementById('search-map').value.trim(),
        mode:   document.getElementById('search-mode').value,
        tag:    document.getElementById('search-tag').value.trim(),
        from:   document.getElementById('search-from').value,
        to:     document.getElementById('search-to').value,
    };
}

function writeSearchFiltersToForm(filters) {
    if (filters.player !== undefined) document.getElementById('search-player').value = filters.player;
    if (filters.team   !== undefined) document.getElementById('search-team').value   = filters.team;
    if (filters.map    !== undefined) document.getElementById('search-map').value    = filters.map;
    if (filters.mode   !== undefined) document.getElementById('search-mode').value   = filters.mode;
    if (filters.tag    !== undefined) document.getElementById('search-tag').value    = filters.tag;
    if (filters.from   !== undefined) document.getElementById('search-from').value   = filters.from;
    if (filters.to     !== undefined) document.getElementById('search-to').value     = filters.to;
}

function setupSearch() {
    // The mode facet list lives in ONE place — SEARCH_MODE_LABELS, which also
    // labels the result rows. index.html ships only the "Any" option and this
    // fills the rest, so a facet cannot be offered in the dropdown and render
    // as a raw token in the rows below it.
    const modeSel = document.getElementById('search-mode');
    if (modeSel) {
        for (const [value, label] of Object.entries(SEARCH_MODE_LABELS)) {
            const opt = document.createElement('option');
            opt.value = value;
            opt.textContent = label;
            modeSel.appendChild(opt);
        }
    }
    document.getElementById('search-form').addEventListener('submit', (e) => {
        e.preventDefault();
        runSearch();
    });
}

// Paging state for the search tab: the filters the shown results belong to,
// how many rows are rendered, and the hub-reported total (null = unknown).
let searchPaging = { filters: null, shown: 0, total: null };

async function runSearch(loadMore = false) {
    const filters = loadMore && searchPaging.filters ? searchPaging.filters : readSearchFilters();
    const offset = loadMore ? searchPaging.shown : 0;
    const status = document.getElementById('search-status');
    const submit = document.getElementById('search-submit');
    const resultsEl = document.getElementById('search-results');

    status.textContent = 'Searching…';
    status.className = 'status loading';
    submit.disabled = true;

    try {
        const { games, total } = await MvdHub.searchGames(filters, offset);
        status.textContent = '';
        status.className = 'status';
        renderSearchResults(games, loadMore);
        searchPaging = {
            filters,
            shown: offset + games.length,
            total: total !== null ? total : searchPaging.total,
        };
        renderSearchPager();
        lastExecutedSearch = filters;
        updateUrlState();
    } catch (err) {
        status.textContent = 'Error: ' + err.message;
        status.className = 'status error';
        if (!loadMore) resultsEl.innerHTML = '';
    } finally {
        submit.disabled = false;
    }
}

// Footer under the result list: "N of M matches" plus a Load-more button
// while the hub reports more pages. Rebuilt after every page.
function renderSearchPager() {
    const el = document.getElementById('search-results');
    const old = el.querySelector('.search-pager');
    if (old) old.remove();
    const { shown, total } = searchPaging;
    if (!shown) return;

    const pager = document.createElement('div');
    pager.className = 'search-pager';
    const label = document.createElement('span');
    label.className = 'search-pager-count';
    label.textContent = total !== null ? `${shown} of ${total} matches` : `${shown} matches`;
    pager.appendChild(label);
    if (total !== null && shown < total) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'search-submit search-pager-more';
        btn.textContent = 'Load more';
        btn.addEventListener('click', () => runSearch(true));
        pager.appendChild(btn);
    }
    el.appendChild(pager);
}

const SEARCH_MODE_LABELS = {
    '1on1': '1v1',
    '2on2': '2v2',
    '4on4': '4v4',
    'ffa':  'FFA',
    'ctf':  'CTF',
};

// Which of those hub facets name a mode with no teams. This is the HUB's
// vocabulary on a HUB search row — not a Result — so it cannot read
// match.gameMode: the row is a listing from hub.quakeworld.nu carrying its
// own `mode` string, and no demo has been analysed at that point. It lives
// here beside the facet table so the pair stays in step (it used to be an
// inline `mode !== '1on1' && mode !== 'ffa'` at the one call site).
const SEARCH_INDIVIDUAL_MODES = new Set(['1on1', 'ffa']);

// The shared helpers below live in src/lib/ (window.MvdLib, published by
// src/main.js); the wrappers keep app.js's call sites unchanged.
function escapeHtml(s) {
    return MvdLib.escapeHtml(s);
}

// src/lib/player-stats.js: either row shape (playerStats / demoInfo).
function fragsOfRow(p) {
    return MvdLib.fragsOfRow(p);
}

// src/lib/player-stats.js: { team: total frags }, teamless under emptyTeamKey.
function teamFragTotals(players, emptyTeamKey = '') {
    return MvdLib.teamFragTotals(players, emptyTeamKey);
}

// src/lib/player-stats.js: stable, frag-sorted — the canonical colour order.
function sortByFragsDesc(players) {
    return MvdLib.sortByFragsDesc(players);
}

// ─── playerStats accessors ─────────────────────────────────────────────────
//
// result.playerStats is the canonical per-player section (schema v61); see
// src/lib/player-stats.js.

function playerStatsRows(result) {
    return MvdLib.playerStatsRows(result);
}

function playerStatsTeamRows(result) {
    return MvdLib.playerStatsTeamRows(result);
}

// The map's FILE key — the basename the shipped geometry (maps/e1m2.json)
// and a saved region config are named by; see src/lib/result.js.
function mapFileKey(result) {
    return MvdLib.mapFileKey(result);
}

// One possession cell (src/lib/player-stats.js): '-' when never held, the
// raw ms as data-sort-value, the share and window in the tooltip.
function holdCell(stat, win) {
    return MvdLib.holdCell(stat, win);
}

// The graded match-start anchor ({ html, text } or null) for the topbar date
// — src/lib/wall-clock.js, shared with the Summary Date cell.
function matchWallClock(result) {
    return MvdLib.matchWallClock(result);
}

function updateTopbarDemoInfo(result) {
    const el = document.getElementById('topbar-demo-info');
    if (!el) return;

    const demoInfo = result?.demoInfo;
    // Display: the canonical shortname (dm2, e1m2) — how QW players name
    // maps. `match.mapTitle` is available for surfaces that want the long
    // level title; the topbar is not one of them. Never a key.
    const map = demoInfo?.map || result?.match?.map || '';
    // Same precedence as the Summary "Date" cell (matchDateHtml in
    // src/lib/wall-clock.js): KTX's own stamp where the demoinfo block
    // exists, the graded wall-clock anchor where it does not.
    const wallClock = demoInfo?.date ? null : matchWallClock(result);
    const date = demoInfo?.date || '';

    let teams = (timelineState.teams && timelineState.teams.length >= 2)
        ? [...timelineState.teams]
        : [];
    if (teams.length < 2 && demoInfo?.teams) teams = [...demoInfo.teams];
    if (teams.length < 2 && result?.match?.teams) teams = result.match.teams.map(t => t.name);

    // playerStats first so the topbar score agrees with the scoreboard on
    // the Summary tab: its score.frags is the corrected net score, while
    // KTX's stats.frags resets after a reconnect.
    let teamScores = {};
    const psRows = playerStatsRows(result);
    if (psRows.length) {
        teamScores = teamFragTotals(psRows);
    } else if (demoInfo?.players) {
        teamScores = teamFragTotals(demoInfo.players);
    } else if (result?.match?.teams) {
        for (const t of result.match.teams) teamScores[t.name] = t.frags || 0;
    }

    const parts = [];
    // An individual match with more than two players is not a matchup, and
    // "toast vs SMOK" over a field of eight reads as one. Name the leader and
    // the size of the field instead; a two-player one (a duel, or a 1v1 FFA)
    // still renders as "A vs B" below, which is what it is.
    const individualField = isMultiPlayerIndividual(result);
    if (individualField) {
        const a = teams[0];
        parts.push(
            `<span class="topbar-team-a">${escapeHtml(a)}</span>` +
            (a in teamScores ? ` <span class="topbar-score">${teamScores[a]}</span>` : '')
        );
        parts.push(`<span class="topbar-field">${teams.length} players</span>`);
    } else if (teams.length >= 2) {
        const a = teams[0];
        const b = teams[1];
        parts.push(
            `<span class="topbar-team-a">${escapeHtml(a)}</span>` +
            ` <span class="topbar-vs">vs</span> ` +
            `<span class="topbar-team-b">${escapeHtml(b)}</span>`
        );
        if (a in teamScores || b in teamScores) {
            parts.push(
                `<span class="topbar-score">` +
                `${teamScores[a] ?? 0} - ${teamScores[b] ?? 0}` +
                `</span>`
            );
        }
    }
    if (map) parts.push(`<span class="topbar-map">${escapeHtml(map)}</span>`);
    if (date) parts.push(`<span class="topbar-date">${escapeHtml(date)}</span>`);
    else if (wallClock) parts.push(`<span class="topbar-date">${wallClock.html}</span>`);

    el.innerHTML = parts.join('<span class="topbar-sep">·</span>');

    const titleParts = [];
    if (individualField) titleParts.push(teams[0]);
    else if (teams.length >= 2) titleParts.push(`${teams[0]} ${teams[1]}`);
    if (map) titleParts.push(map);
    document.title = titleParts.length
        ? `MVD | ${titleParts.join(' ')}`
        : 'MVD Analyzer';
}

function renderSearchResults(games, append = false) {
    const el = document.getElementById('search-results');
    if (!append) el.innerHTML = '';
    if ((!games || games.length === 0) && !append) {
        el.innerHTML = '<div class="search-empty">No demos match these filters.</div>';
        return;
    }
    for (const game of games) {
        const row = document.createElement('button');
        row.type = 'button';
        row.className = 'search-result';
        row.dataset.gameId = game.id;

        const modeText = SEARCH_MODE_LABELS[game.mode] || game.mode || '-';
        const mapText  = game.map || '-';
        const dateText = (game.timestamp || '').slice(0, 10);

        // Pick contestants: top-2 teams for team modes, top-2 players otherwise.
        const isTeamMode = !!game.mode && !SEARCH_INDIVIDUAL_MODES.has(game.mode);
        let contestants = '—';
        if (isTeamMode && Array.isArray(game.teams) && game.teams.length >= 2) {
            const t = [...game.teams].sort((a, b) => (b.frags || 0) - (a.frags || 0));
            contestants = `${t[0].name} ${t[0].frags} : ${t[1].frags} ${t[1].name}`;
        } else if (Array.isArray(game.players) && game.players.length >= 2) {
            const p = [...game.players].sort((a, b) => (b.frags || 0) - (a.frags || 0));
            contestants = `${p[0].name} ${p[0].frags} : ${p[1].frags} ${p[1].name}`;
        } else if (Array.isArray(game.players) && game.players.length === 1) {
            const p = game.players[0];
            contestants = `${p.name} ${p.frags || 0}`;
        }

        const tagHtml = game.matchtag
            ? `<span class="search-result-tag">${escapeHtml(game.matchtag)}</span>`
            : '<span class="search-result-tag"></span>';

        row.innerHTML =
            `<span class="search-result-mode">${escapeHtml(modeText)}</span>` +
            `<span class="search-result-map">${escapeHtml(mapText)}</span>` +
            `<span class="search-result-contest">${escapeHtml(contestants)}</span>` +
            tagHtml +
            `<span class="search-result-date">${escapeHtml(dateText)}</span>`;

        row.addEventListener('click', () => loadGameFromSearch(game));
        el.appendChild(row);
    }
}

async function loadGameFromSearch(game) {
    try {
        await loadDemo({ game });
        // displayResults() switches to the Summary tab as part of finishing a load.
    } catch (error) {
        const status = document.getElementById('upload-status');
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
    }
}

function displayResults(result) {
    // Wipe every panel, table, canvas, and JS-side state object that
    // could carry data from a previous demo. Critical when swapping
    // between demos with different shapes (e.g. 4on4 → FFA where
    // teams/regions/etc. are absent and would otherwise survive).
    resetUIToCleanState();

    document.body.classList.remove('no-demo');

    // Land the user on the Summary tab after a fresh load, unless the URL
    // explicitly asks for a different tab (deep links like ?tab=map should
    // win — applyUrlState() is called later and will switch tabs again).
    if (!new URLSearchParams(location.search).get('tab')) {
        switchTab('summary');
    }

    const demoInfo = result.demoInfo;

    // Before anything that reads `streams`: the marker decides whether the
    // panels below describe a match at all; the body class hides the
    // stream-fed Summary panels (src/tabs/summary/ renders the marker).
    document.body.classList.toggle('no-match', !!result.noMatch);

    // Duel-mode styling: the Go-side `roster` core node has already
    // stamped every team reference as the player's name for 1v1 demos
    // (born-correct). Now collapse the redundant "Per Team" panels and the
    // Teams summary box in the UI so the viewer only sees the per-player
    // tables. Detected by checking whether every player's team equals
    // their own name (a property only true after the Go-side rewrite).
    applyDuelModeUI(result);

    // Set team order first (sorted by total frags, highest first) so every
    // panel below — and the Summary tab (src/tabs/summary/), which builds
    // from the teams store — sees this demo's order, not the previous demo's. CLAUDE.md: this is the ONE canonical
    // team→colour source — TEAM_COLORS indexed by position in
    // timelineState.teams, winner at 0, with the palette entry at each index
    // assigned by team name (assignTeamColors) — and it must never be derived
    // from demoInfo.teams order or any per-feature ordering. playerStats is
    // the frag source (its score.frags is the corrected net score and it
    // exists on demos with no KTX block at all), with demoInfo/match retained
    // only to enumerate the team NAMES.
    {
        const individual = isIndividualLayout(result);
        const psTeams = playerStatsTeamRows(result);
        let teams = [];
        if (individual) {
            // One side per player. playerStats carries the corrected net
            // score, so the frag-sorted PLAYER list is the canonical order —
            // and since every row's `team` IS its name, every existing
            // `teamOrder.indexOf(row.team)` colour lookup resolves to the
            // player's rank without a second mapping.
            teams = sortByFragsDesc(playerStatsRows(result)).map(p => p.name);
            if (!teams.length && result.match?.players) {
                teams = [...result.match.players]
                    .sort((a, b) => (b.frags || 0) - (a.frags || 0))
                    .map(p => p.name);
            }
        } else if (psTeams.length) {
            teams = psTeams.map(t => t.name);
        } else if (demoInfo?.teams) {
            teams = [...demoInfo.teams];
        } else if (result.match?.teams) {
            teams = result.match.teams.map(t => t.name);
        }
        if (!individual && teams.length >= 2) {
            const rows = playerStatsRows(result);
            const teamFrags = psTeams.length
                ? Object.fromEntries(psTeams.map(t => [t.name, fragsOfRow(t)]))
                : teamFragTotals(rows.length ? rows : demoInfo?.players);
            teams.sort((a, b) => (teamFrags[b] || 0) - (teamFrags[a] || 0));
        }
        // Duels keep the team palette (isMultiPlayerIndividual is false at
        // two participants), so a 1v1 colours exactly as it did before
        // individual modes existed.
        setCanonicalTeams(teams, isMultiPlayerIndividual(result));
    }

    updateTopbarDemoInfo(result);

    // Region control data (needed by the map). Cheap on the main thread;
    // the multi-second cost was the worker's bucket build, which now runs
    // after the result is delivered. These run here with the bucket-derived
    // fields still empty (map trails / region overlay blank) and
    // onBucketsReady() re-runs them once the 'buckets' message lands.
    // initTimeline populates the unified timeline strip, so it must run now.
    if (result.timelineAnalysis?.regionControl) {
        initRegionControlData(result);
    }

    if (result.timelineAnalysis || result.messages?.messages) {
        timeRender('initTimeline', () => initTimeline(result));
    }

    // Map View
    if (result.timelineAnalysis) {
        timeRender('initMapView', () => initMapView(result));
    }

    // Make all static tables sortable
    document.querySelectorAll('.stats-table').forEach(makeSortable);

    // Apply URL state (tab, time, segment) if present
    applyUrlState();

    // Reveal the now-populated UI (overlay may already be hidden if the
    // demo was loaded interactively rather than via ?gameId=).
    hideLoadingOverlay();

    // Flush the consolidated load-timing breakdown to the console.
    finishLoadTiming();
}

// ─── Sortable Tables ──────────────────────────────────────────────────────

// src/ui/sortable.js: click a header to sort the rows (binds once per <th>).
function makeSortable(table) {
    return MvdLib.makeSortable(table);
}

// isDuel: a 1v1 — src/lib/result.js (applyDuelModeUI toggles the "Per Team"
// aggregation panels and the standalone "Teams" summary off on one).
function isDuel(result) {
    return MvdLib.isDuel(result);
}

// The two team questions — LAYOUT (every player their own side?) and
// SEMANTICS (teamplay ruleset in force?) — and why they must stay apart:
// src/lib/result.js.
function isIndividualLayout(result) {
    return MvdLib.isIndividualLayout(result);
}

function isTeamBasedMode(result) {
    return MvdLib.isTeamBasedMode(result);
}

// isMultiPlayerIndividual reports whether this demo is laid out one side per
// player with MORE than two of them — a field rather than a matchup. Every
// two-sided surface reads it: the "Team A ↑ / Team B ↓" timeline graphs, the
// Chat tab's per-side say columns, region control, and the colour palette
// (per-player rather than the four-entry team one — see PLAYER_PALETTE and
// CLAUDE.md's team-colour rule). None of them has a pair of sides to name
// here: the top two players are not a matchup and everyone else does not
// appear at all. A two-participant match (a duel, or a 1v1 FFA) IS the
// two-sided view and keeps every one of them. The test is "not a duel"
// rather than "more than two": isDuel needs exactly two playerStats rows,
// so an individual layout with fewer (one player alone on a server) takes
// the field layout too — it has no pair of sides to show either.
//
// It was four copies of this expression under four names. They answered
// different questions in the prose and the same one in the code, and a
// consumer reading one of them could not tell which.
function isMultiPlayerIndividual(result) {
    return isIndividualLayout(result) && !isDuel(result);
}

// setIndividualFieldLayout puts the `individual-field` class on <body>: the
// CSS switches every two-sided surface to its per-player form off it — the
// Timeline's A-vs-B panels (src/tabs/timeline/ moves its per-player views
// into panels of their own off the teams store's perPlayer, the same test)
// and the Chat tab's second say column (src/tabs/chat/).
function setIndividualFieldLayout(on) {
    document.body.classList.toggle('individual-field', on);
}

function applyDuelModeUI(result) {
    // Toggle a class on <body> so CSS can drive the hiding. Using a
    // class (instead of inline style writes) means the UI can re-render
    // cleanly on demo reload without leaking stale display:none values
    // onto unrelated elements.
    document.body.classList.toggle('duel-mode', isDuel(result));
    // FFA and friends: no teams anywhere, so every team surface goes — the
    // Teams box, the per-team aggregates, the Team / TK / TDmg scoreboard
    // columns (structurally 0 when nobody has a teammate), and the team
    // colour legends. Region control needs a binary side layout; its panels
    // are hidden by initRegionControl, NOT by a missing-data gate — the
    // result ships `timelineAnalysis.regionControl.regions` for every map.
    document.body.classList.toggle('individual-mode', isIndividualLayout(result));
    // FFA has no teams at all, so playerStats omits the section and the
    // three Summary "Per Team" tables rendered as headers over nothing —
    // duel mode was the only thing hiding them, and it does not fire at
    // five players. Drive it off the rows themselves, which covers every
    // demo class with nothing to aggregate.
    document.body.classList.toggle('no-team-rows', playerStatsTeamRows(result).length === 0);
    // Timeline and Chat: the A-vs-B graphs and the two say columns both
    // describe a matchup an FFA does not have.
    setIndividualFieldLayout(isMultiPlayerIndividual(result));
}

// ─── Team helpers ──────────────────────────────────────────────────────────

function getTeamOrder(sortedPlayers) {
    // Canonical order set early in displayResults(), sorted by total frags;
    // else the input's own (already frag-sorted) — src/lib/player-stats.js.
    return MvdLib.teamOrder(timelineState.teams, sortedPlayers);
}


function formatDuration(seconds) {
    return MvdLib.formatDuration(seconds);
}

// The match timeline as app.js's map and unified bar use it. The selected
// segment is shared (MvdState.segment); the Timeline tab
// (src/tabs/timeline/) reads the rest from the Result itself.
let timelineState = {
    bucketView: null,  // column-major ColumnarBuckets (50ms) from getDefaultBuckets
    duration: 0,       // match length, seconds
    demoOffset: 0,     // demo time = match time + this, seconds
    teams: [],         // the canonical team order (teams store mirror)
    dragging: false,   // a range select on the unified bar in progress
    dragStartTime: 0,  // where it started
    dragPreview: null, // { start, end } the bar shows while dragging
};

// Reset all timeline state for loading a new demo
// resetUIToCleanState wipes every panel, table, canvas, and JS-side
// state object that could carry data from a previous demo. Called at
// the top of displayResults() so each load starts from a blank slate.
// Without this, conditionally-populated panels (teams, scoreboards,
// timeline graphs, map view, …) survive a swap from e.g. a 4on4 demo
// to an FFA demo that simply lacks those fields.
function resetUIToCleanState() {
    resetTimelineState();

    document.title = 'MVD Analyzer';

    const setHTML = (id, v) => {
        const el = document.getElementById(id);
        if (el) el.innerHTML = v;
    };
    const hide = id => {
        const el = document.getElementById(id);
        if (el) el.style.display = 'none';
    };

    // The Summary tab (src/tabs/summary/) rebuilds itself from the demo
    // store; nothing of it is cleared here.

    // Topbar demo info
    setHTML('topbar-demo-info', '');

    // The Timeline tab (src/tabs/timeline/) clears itself on the demo store.
    // Two-sided default for the timeline AND the chat columns (CSS);
    // applyDuelModeUI re-applies the individual field layout below if this
    // demo wants it.
    setIndividualFieldLayout(false);
    hide('unified-timeline');

    // Map view
    const mapCanvas = document.getElementById('map-canvas');
    if (mapCanvas && mapCanvas.getContext) {
        mapCanvas.getContext('2d').clearRect(0, 0, mapCanvas.width, mapCanvas.height);
    }
    setHTML('map-legend', '');
    setHTML('map-items-body', '');
    hide('map-items-panel');
    hide('map-no-data');
    setHTML('region-status-body', '');
    hide('region-status-panel');
    // The Locs & Regions tab (src/tabs/locgraph/) clears its region editor
    // and matrix on this, and its loc graph on the demo store.
    MvdState.regionControl.set(null);

    // Body classes — duel-mode / individual-mode / no-team-rows / no-match
    // collapse some panels via CSS; clear them so a teamplay demo loaded after
    // a duel, an FFA or a match-less recording doesn't inherit the layout.
    document.body.classList.remove('duel-mode');
    document.body.classList.remove('individual-mode');
    document.body.classList.remove('no-team-rows');
    document.body.classList.remove('no-match');

    // JS-side state objects that hold per-demo data
    mapState.locations = [];
    mapState.locationGroups = null;
    mapState.mapGeometry = null;
    mapState.bounds = { minX: 0, maxX: 0, minY: 0, maxY: 0 };
    mapState.currentTime = 0;
    MvdState.clock.seek(0, 'reset');
    mapState.isPlaying = false;
    mapState.playbackSpeed = 1;
    mapState.animationFrameId = null;
    mapState.lastRenderTime = 0;
    mapState.fullTrails = {};
    mapState.trailStartTimes = {};
    mapState.enabledPlayers = {};
    mapState.teams = [];
    mapState.playerSymbols = {};
    mapState.lastRenderedBucket = null;
    mapState.renderDirty = false;
    mapState.followPlayer = null;
    mapState._regionIconCache = null; // composited region-status icons (per demo)
    if ('controlRegions' in mapState) mapState.controlRegions = null;
    if ('locToRegion' in mapState) mapState.locToRegion = {};
    if ('dropEvents' in mapState) mapState.dropEvents = [];
    if ('zRange' in mapState) mapState.zRange = null;
    if ('locTable' in mapState) mapState.locTable = [''];
}

function resetTimelineState() {
    if (mapState.isPlaying) stopPlayback();

    // Map view data is rebuilt by initMapView, which is now deferred until
    // the 50ms buckets arrive (see onBucketsReady). Clear it here so a
    // fast demo-swap can't render the previous demo's map during the window
    // before the new demo's buckets finish building.
    mapState.locations = [];
    mapState.locationGroups = null;
    mapState.mapGeometry = null;
    mapState.fullTrails = {};
    mapState.dropEvents = [];
    mapState.bucketStates = null;
    mapState.lastRenderedBucket = null;
    mapState.renderDirty = true;

    timelineState.bucketView = null;
    mapView.setFrames(null);
    timelineState.duration = 0;
    MvdState.clock.setDuration(0);
    timelineState.demoOffset = 0;
    MvdState.setCanonicalTeams([]);
    MvdState.segment.set(null);
    timelineState.dragging = false;
    timelineState.dragPreview = null;

    // Clear the timeline axis
    const axis = document.getElementById('tl-axis');
    if (axis) axis.innerHTML = '';
}

// initTimeline sets up the match timeline app.js keeps — the bucket view the
// map draws, the duration and demo offset, the unified bar — on load and
// again when the deferred buckets land.
function initTimeline(result) {
    const timeline = result.timelineAnalysis;
    const demoInfo = result.demoInfo;

    // Teams already set (frag-sorted) in displayResults; only set if missing
    if (!timelineState.teams || timelineState.teams.length === 0) {
        const individual = isIndividualLayout(result);
        if (!individual && demoInfo?.teams) {
            setCanonicalTeams([...demoInfo.teams], false);
        } else if (result.match?.teams) {
            setCanonicalTeams(result.match.teams.map(t => t.name), isMultiPlayerIndividual(result));
        }
    }

    // Schema v7: bucketed data is not a parse-time field; the WASM worker
    // calls getDefaultBuckets after analyzeMVD and onBucketsReady
    // stashes the resulting column-major ColumnarBuckets object on
    // timeline.bucketView.
    timelineState.bucketView = timeline?.bucketView || null;
    mapView.setFrames(timelineState.bucketView);
    // Schema v8: raw time fields are int32 ms; the page works in seconds
    // (mapState.currentTime, formatDuration, hub URL `from`/`to`), so convert
    // here at intake. Schema v23 moved the timing anchor to streams.global.
    timelineState.demoOffset = MvdLib.demoOffsetSec(result);
    timelineState.duration = MvdLib.matchDurationSec(result);
    MvdState.clock.setDuration(timelineState.duration);

    // Note: the shared playhead (mapState.currentTime) is initialised to 0 by
    // resetUIToCleanState at the start of each demo load. It is deliberately
    // NOT reset here — initTimeline is re-run by onBucketsReady seconds after
    // load, and a reset there would clobber a ?t= deep link or any early
    // scrubbing the user did during the deferred-bucket window.

    setupUnifiedTimeline();

    // Show the unified timeline on applicable tabs
    const activeTab = activeTabName();
    const tl = document.getElementById('unified-timeline');
    if (tl) tl.style.display = TABS_WITH_TIMELINE.includes(activeTab) ? '' : 'none';

    updateUnifiedCursor();
}

// ─── Unified Timeline Widget ──────────────────────────────────────────────
//
// The playback bar above the Timeline, Chat, Map, Key Moments and Player
// tabs: playback buttons, the time, a scrub caret and a range select. The
// range it selects is the shared segment (MvdState.segment, src/state/
// timeline.js) — the Timeline tab's view window, which that tab's own zoom
// and pan also set. While a drag is in progress the bar shows its own
// preview (timelineState.dragPreview); the segment is set on release.

let unifiedTimelineInitialized = false;

function setupUnifiedTimeline() {
    if (unifiedTimelineInitialized) {
        updateUnifiedCursor();
        updateUnifiedTimeDisplay();
        renderUnifiedAxis();
        return;
    }

    const bar = document.getElementById('tl-bar');
    const caret = document.getElementById('tl-caret');

    renderUnifiedAxis();

    // --- Caret drag: sets current time ---
    let caretDragging = false;

    caret.addEventListener('mousedown', (e) => {
        caretDragging = true;
        e.preventDefault();
        e.stopPropagation();
    });

    document.addEventListener('mousemove', (e) => {
        if (caretDragging) {
            const time = tlBarClickToTime(e);
            if (time === null) return;
            setCurrentTime(time);
            return;
        }

        if (!timelineState.dragging) return;
        const time = tlBarClickToTime(e);
        if (time === null) return;

        const start = Math.min(timelineState.dragStartTime, time);
        const end = Math.max(timelineState.dragStartTime, time);

        if (end - start > 2) {
            timelineState.dragPreview = { start, end };
            updateSelectionOverlay(timelineState.dragPreview);
            updateSegmentLabel(timelineState.dragPreview);
        }
    });

    document.addEventListener('mouseup', (e) => {
        if (caretDragging) {
            caretDragging = false;
            updateUrlState();
            return;
        }

        if (!timelineState.dragging) return;
        timelineState.dragging = false;
        timelineState.dragPreview = null;

        const time = tlBarClickToTime(e);
        if (time === null) return;

        const start = Math.min(timelineState.dragStartTime, time);
        const end = Math.max(timelineState.dragStartTime, time);

        if (end - start <= 2) {
            // A click: no segment, and the time moves there.
            MvdState.segment.set(null);
            setCurrentTime(time);
        } else {
            MvdState.segment.set({ start, end });
        }
    });

    bar.addEventListener('mousedown', (e) => {
        const time = tlBarClickToTime(e);
        if (time === null) return;

        timelineState.dragging = true;
        timelineState.dragStartTime = time;
        timelineState.dragPreview = null;
        updateSelectionOverlay(null);
        updateSegmentLabel(null);

        e.preventDefault();
    });

    bar.addEventListener('dblclick', () => {
        MvdState.segment.set(null);
    });

    // --- Playback controls ---
    document.getElementById('tl-rev').addEventListener('click', () => startPlaybackAtSpeed(-1));
    document.getElementById('tl-slow').addEventListener('click', () => startPlaybackAtSpeed(0.2));
    document.getElementById('tl-play-pause').addEventListener('click', () => startPlaybackAtSpeed(1));
    document.getElementById('tl-5x').addEventListener('click', () => startPlaybackAtSpeed(5));

    unifiedTimelineInitialized = true;
}

// onSegmentChanged mirrors the shared segment onto the bar and the URL.
function onSegmentChanged(seg) {
    updateSelectionOverlay(seg);
    updateSegmentLabel(seg);
    updateUrlState();
}

function tlBarClickToTime(e) {
    const bar = document.getElementById('tl-bar');
    if (!bar) return null;
    const rect = bar.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const width = rect.width;
    if (width <= 0) return null;
    const frac = Math.max(0, Math.min(1, x / width));
    return frac * timelineState.duration;
}

// The bar's selection overlay and label for a segment (the shared one, or
// the drag preview); null hides them.
function updateSelectionOverlay(seg) {
    const overlay = document.getElementById('tl-selection-overlay');
    if (!overlay) return;

    if (!seg) {
        overlay.style.display = 'none';
        return;
    }

    const duration = timelineState.duration;
    if (duration <= 0) return;

    const startPct = (seg.start / duration) * 100;
    const endPct = (seg.end / duration) * 100;

    overlay.style.display = 'block';
    overlay.style.left = `${startPct}%`;
    overlay.style.width = `${endPct - startPct}%`;
}

function updateSegmentLabel(seg) {
    const label = document.getElementById('tl-segment-label');
    if (!label) return;

    if (!seg) {
        label.textContent = '';
        return;
    }

    label.textContent = `${formatDuration(seg.start)} – ${formatDuration(seg.end)}`;
}

function updateUnifiedCursor() {
    const cursor = document.getElementById('tl-cursor');
    const caret = document.getElementById('tl-caret');

    const duration = timelineState.duration;
    if (duration <= 0) return;

    const pct = Math.max(0, Math.min(100, (mapState.currentTime / duration) * 100));
    if (cursor) cursor.style.left = `${pct}%`;
    if (caret) caret.style.left = `${pct}%`;
}

function updateUnifiedTimeDisplay() {
    const display = document.getElementById('tl-current-time');
    if (!display) return;
    display.textContent = formatDuration(Math.max(0, mapState.currentTime));
}

function renderUnifiedAxis() {
    const container = document.getElementById('tl-axis');
    if (!container) return;
    container.innerHTML = '';

    const duration = timelineState.duration;
    if (duration <= 0) return;

    const tickCount = Math.min(10, Math.max(4, Math.floor(duration / 60)));
    for (let i = 0; i <= tickCount; i++) {
        const time = (duration / tickCount) * i;
        const pct = (i / tickCount) * 100;
        const span = document.createElement('span');
        span.textContent = formatDuration(time);
        span.style.left = `${pct}%`;
        container.appendChild(span);
    }
}

// ─── Player Tab ─────────────────────────────────────────────────────────────
//
// The Player tab is Svelte (src/player/). app.js reaches it only through
// window.MvdPlayer (the engine): while the engine is the page clock, the
// timeline's speed buttons drive it, and their state follows its status.

function playerDrives() {
    return MvdPlayer.drives();
}

// =============================================================================
// Map Visualization
// =============================================================================

// ─── Region focus mode ──────────────────────────────────────────────────────
//
// Clicking a loc region (on empty floor, not a player symbol) focuses it:
// the region and its XY-neighbors render solid and saturated while
// everything else fades to a faint outline, so a zoomed-in fight area stays
// readable. Click the same region, empty space, Escape, or Reset view to
// clear. Focus also becomes the orbit-drag pivot (currentOrbitPivot).

function setFocusGroup(name) {
    mapView.setFocusGroup(name);
}

// (pickLocGroupAt, computeOccupiedGroupTeams, regionActiveTint and the two
// region overlays now live on MvdMap in mvd-map-view/src/map.js.)

// Map View State
// Renderer state, owned by mvd-map-view's MvdMap. app.js keeps the two
// aliases it has always used — mapState for the state bag, _wtc for the
// camera — so the render path is unchanged while the draw layers move across
// group by group. Both are null until the module bootstrap runs (see
// onMapViewReady below), which is before DOMContentLoaded and therefore
// before anything can read them.
let mapView = null;
let mapState = null;

// (PLAYER_SYMBOLS, BADGE_DEFS and ARMOR_COLORS now live with the rest of
// the theme constants at the top of this file.)

function markMapDirty() {
    mapState.renderDirty = true;
}

// applyMapGeometry: install (or re-install, after an edit / regeneration)
// a geometry object as the map's floor/wall source. The state half —
// normalization, backdrop split, submodel indexing, cache invalidation,
// group rebuild — is MvdMap.setGeometry; this wrapper only refreshes the
// region->group references (the control overlay must not keep pointing at
// the previous, stale-tris group objects: re-applying the regions in effect
// rebuilds them, see onRegionControlChanged) and repaints.
function applyMapGeometry(geom) {
    mapView.setGeometry(geom);
    if (MvdState.regionControl.get()) {
        MvdState.reapplyRegions(currentDemo); // also calls renderMap
    } else {
        markMapDirty();
        renderMap(mapState.currentTime);
    }
}

function initMapView(result) {
    if (!result.timelineAnalysis) return;

    mapState.canvas = document.getElementById('map-canvas');
    if (!mapState.canvas) return;
    mapState.ctx = mapState.canvas.getContext('2d');

    // Get location data from timeline analysis
    const timeline = result.timelineAnalysis;
    mapState.locations = timeline.locationData || [];
    // Interned loc-name table — index 0 is the empty/no-loc sentinel.
    // High-res player records carry an integer Li indexing into this; the
    // mapView.resolvePlayerLoc helper hides the indirection from call sites.
    mapState.locTable = (timeline && timeline.locTable) ? timeline.locTable : [''];
    mapState.locationGroups = null; // Clear cached groups for new demo
    mapState.mapGeometry = null;    // Reset BSP-derived geometry for new demo
    mapState.focusGroupName = null; // Region focus doesn't survive demo switch
    mapState.focusNeighbors = null;
    mapState._floorModel = null;    // floor-model + anchor caches are per-geometry
    mapState._floorCanvasKey = null;
    mapState._floorZCache = null;

    // Mover pose timelines (result.streams.movers, schema v32) — animated
    // lifts/doors/plats. Meshes (mapState.submodelMeshes) arrive with the
    // async corpus geometry; both are required to draw a mover, so either
    // being absent is a graceful no-op.
    mapState.movers = (result.streams && Array.isArray(result.streams.movers))
        ? result.streams.movers : [];
    mapState.submodelMeshes = null;
    mapState._moverFaces = null;

    // Per-player native-rate position tracks (result.streams.players[].pos)
    // keyed by canonical name. The map animates symbol positions from these
    // and reads the per-sample floor-height H for the floor-anchor stem
    // (state badges still come from the bucket view). Keyed by the same
    // disambiguated name the bucket view uses.
    mapState.posStreams = {};
    if (result.streams && Array.isArray(result.streams.players)) {
        for (const p of result.streams.players) {
            if (p && p.pos && Array.isArray(p.pos.t) && p.pos.t.length > 0) {
                mapState.posStreams[p.name] = p.pos;
            }
        }
    }

    // Spatial weapon-fire streams (schema v40): rocket/grenade flights
    // (projectiles) and LG bolts (beams), as parallel columns. Present only
    // when the analysis built them (WASM map build); absent → graceful no-op.
    mapState.projectiles = (result.streams && result.streams.projectiles) || null;
    mapState.beams = (result.streams && result.streams.beams) || null;
    mapState.nails = (result.streams && result.streams.nails) || null;

    // Line of sight is computed lazily (it is the heaviest position-derived
    // pass), so it is NOT in the parsed result. The map's LOS overlay requests
    // it from the worker on first toggle; reset the cache here.
    mapState.losByPair = {};
    mapState.pvsByPair = {};
    mapState.losComputed = false;
    mapState.losPending = false;

    // Static map-entity corpus (result.mapEntities) for the "learn map" view.
    // Item spawners for the map's live markers. The renderer reads these from
    // its own state rather than the app's result object — it has no notion of
    // a Result, and in the MCP viewer there isn't one to reach for.
    mapState.items = (result.items && Array.isArray(result.items.items)) ? result.items.items : null;

    mapState.mapEntities = (result.mapEntities && Array.isArray(result.mapEntities.entities))
        ? result.mapEntities.entities : [];
    mapView.buildTeleportArrows();
    // Fresh demo always starts in live mode; only offer learn mode when the
    // map has a static entity corpus.
    mapState.learnMode = false;
    const entPanel = document.getElementById('map-entities-panel');
    if (entPanel) entPanel.style.display = 'none';
    const learnBtn = document.getElementById('map-learn-toggle');
    if (learnBtn) {
        learnBtn.style.display = mapState.mapEntities.length > 0 ? '' : 'none';
        learnBtn.classList.remove('active');
    }

    // Fire-and-forget: try to load pre-generated BSP-derived map geometry.
    // If present, switch from convex-hull blobs to real floor polygons.
    // If absent (404 or fetch error), the existing hull path remains as fallback.
    const mapBasename = mapFileKey(result);
    mapState.mapBasename = mapBasename;
    if (mapBasename) {
        const tGeom = performance.now();
        fetch(`maps/${mapBasename}.json`)
            .then(r => r.ok ? r.json() : null)
            .then(geom => {
                console.log(`[mvd-timing] map geometry fetch (async): ${(performance.now() - tGeom).toFixed(1)} ms`);
                if (!geom || !Array.isArray(geom.locs) || geom.locs.length === 0) return;
                applyMapGeometry(geom);
            })
            .catch(() => {});
    }

    // Show/hide no-data message
    const noDataMsg = document.getElementById('map-no-data');
    if (noDataMsg) {
        noDataMsg.style.display = mapState.locations.length === 0 ? 'block' : 'none';
    }

    // Calculate bounds from locations and player positions
    calculateMapBounds(result);

    // Size canvas and recompute transform. A fresh demo load resets user
    // pan/zoom and the camera to the default tilted 3D view (floors at
    // different heights separate at a glance; the 3D toggle drops to top-down).
    _wtc.panX = 0;
    _wtc.panY = 0;
    _wtc.zoomK = 1;
    _wtc.yaw = MapView.DEFAULT_YAW;
    _wtc.pitch = MapView.DEFAULT_PITCH;
    MapView.refreshTrig(_wtc);
    const btn3d = document.getElementById('map-3d-toggle');
    if (btn3d) btn3d.classList.toggle('active', MapView.is3D(_wtc));
    mapState.followPlayer = null;
    resizeMapCanvas();

    // Use the canonical frag-sorted team order set in displayResults
    if (timelineState.teams && timelineState.teams.length >= 2) {
        mapState.teams = [...timelineState.teams];
    } else if (result.demoInfo?.teams) {
        mapState.teams = result.demoInfo.teams;
    } else if (result.match?.teams) {
        mapState.teams = result.match.teams.map(t => t.name);
    } else {
        mapState.teams = [];
    }

    // Assign symbols to players
    assignPlayerSymbols(result);

    // Set up trail controls + map pan/zoom interaction (only once)
    if (!mapState.initialized) {
        setupMapTrailControls();
        installMapInteraction(mapState.canvas);
        mapState.initialized = true;
    }

    // Pre-compute full trails from high-res bucket data
    precomputeFullTrails();

    // Backpack drops — mirrors mapState.deathEvents in shape so renderMap
    // can fade them on the same DEATH_X_DURATION timeline. Only RL/LG drops
    // exist in result.backpacks today (see qwanalytics/result/backpacks.go).
    // Schema v57: d.time is int32 ms; convert to seconds because the renderMap
    // fade compares against mapState.currentTime (seconds) and DEATH_X_DURATION
    // (seconds).
    mapState.dropEvents = (result.backpacks || []).map(d => ({
        t:      d.time * 0.001,
        wx:     d.origin?.[0] || 0,
        wy:     d.origin?.[1] || 0,
        wz:     d.origin?.[2] || 0,
        weapon: d.weapon,
    }));

    // Cache the map's z percentile range — drives per-player z-based size
    // scaling in renderMap (players higher up on the map render up to 25%
    // larger than those on the lowest level).
    mapState.zRange = MapView.computeMapZRange(mapState.locations);

    // Orbit-camera reference height: tilting pivots about the map's vertical
    // middle so high and low floors fan out symmetrically around the screen
    // position they had at top-down. The default is remembered so Reset view
    // can undo orbit-drag re-centering.
    _wtc.zMid = (mapState.zRange.lo + mapState.zRange.hi) / 2;
    _wtc.zMidDefault = _wtc.zMid;

    // Populate the Follow-player dropdown with current players.
    rebuildFollowSelect();

    // Build item list panel (armors, weapons, MH, powerups with live
    // up/down status — present for KTX demos, auto-hidden otherwise).
    renderItemsPanel();

    // Reset trail checkboxes
    document.querySelectorAll('.map-player-trail-cb').forEach(cb => { cb.checked = false; });

    // On first load resetUIToCleanState has set currentTime to 0; on the
    // deferred re-init (onBucketsReady) it holds the user's scrub
    // position or a ?t= deep link, which must survive — so currentTime is
    // deliberately NOT reset to 0 here.

    // Initialize region control data
    initRegionControl(result);

    renderMap(mapState.currentTime);
}

// Early init of region control data (before the map init)
function initRegionControlData(result) {
    const rc = result.timelineAnalysis?.regionControl;
    if (!rc || !rc.regions || rc.regions.length === 0) return;

    // Ensure locations are available
    if (!mapState.locations || mapState.locations.length === 0) {
        mapState.locations = result.timelineAnalysis?.locationData || [];
    }

    // Set control regions and locToRegion from backend definitions
    mapState.controlRegions = rc.regions;
    mapState.locToRegion = {};
    for (const region of rc.regions) {
        for (const pt of region.points) {
            mapState.locToRegion[pt.name] = region.name;
        }
    }
    MapView.computeRegionStacking(mapState.controlRegions);

    // v7: bucketStates and stats are populated by the deferred buckets (DemoHandle._applyBuckets) —
    // the worker calls recomputeRegionControl with the default regions
    // after analyzeMVD and the result is stashed back onto the parsed
    // result before displayResults runs. Same code shape as v6.
    mapState.bucketStates      = rc.bucketStates || null;
    mapState.regionControlStats = rc.stats || null;
    mapState.teamA              = rc.teamA || null;
    mapState.teamB              = rc.teamB || null;
}

// initRegionControl puts the demo's own regions in effect
// (src/state/regions.js — the Locs & Regions tab builds its editor and
// control matrix from them) and shows or hides the Map's region status.
function initRegionControl(result) {
    const rc = result.timelineAnalysis?.regionControl;
    const statusPanel = document.getElementById('region-status-panel');
    // Region control is a two-SIDES display: the map tint, the legend and the
    // stats table are all "team A vs team B" (view/region_control.go builds
    // buckets only for exactly two labels). An individual layout with more
    // than two players has no such pair — the Go side ships `regions` for it
    // all the same, because the region GEOMETRY is a property of the map, and
    // the editor rendered from it had an Apply button whose only effect was a
    // console warning. A two-participant match (a duel, a 1v1 FFA) does have
    // two sides and keeps the panels.
    if (!rc || !rc.regions || rc.regions.length === 0 || isMultiPlayerIndividual(result)) {
        if (statusPanel) statusPanel.style.display = 'none';
        mapState.controlRegions = null;
        MvdState.resetRegionControl(result, false);
        return;
    }

    // Ensure location groups are processed
    if (!mapState.locationGroups && mapState.locations.length > 0) {
        mapView.rebuildLocationGroups();
    }

    // The editor and matrix render from the Go-supplied regions and stats
    // (the same ones initRegionControlData stashed on mapState). User edits
    // come back through onRegionControlChanged.
    MvdState.resetRegionControl(result, true);
    mapState.renderDirty = true;

    if (statusPanel) statusPanel.style.display = '';
}

// onRegionControlChanged mirrors an edit on the Locs & Regions tab
// (src/state/regions.js) onto the Map's state, in the two steps it lands in:
// the resolved regions, then Go's recomputed control states — after which
// the map redraws (the Timeline's Region Control panel reads the model
// itself). The demo's own regions ('loaded') were written by
// initRegionControlData already.
function onRegionControlChanged(rc) {
    if (!rc || rc.stage === 'loaded') return;
    mapState.controlRegions = rc.regions;
    if (rc.stage === 'resolved') {
        MapView.computeRegionStacking(rc.regions);
        mapState.locToRegion = MvdLib.locToRegionOf(rc.regions);
        // Region → loc groups, for the control overlay's colouring.
        mapState.regionToGroups = MvdLib.regionToGroupsOf(rc.regions, mapState.locationGroups);
        return;
    }
    mapState.bucketStates       = rc.bucketStates;
    mapState.regionControlStats = rc.stats;
    mapState.teamA              = rc.teamA || mapState.teamA;
    mapState.teamB              = rc.teamB || mapState.teamB;

    // Force map redraw
    mapState.renderDirty = true;
    renderMap(mapState.currentTime);
}

// Look up region control state at a given time — MvdMap.regionControlAt
// indexes the Go-supplied bucketStates strings on the frame grid.
function getRegionControlAtTime(time) {
    return mapView.regionControlAt(time);
}

function calculateMapBounds(result) {
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;

    // From locations
    for (const loc of mapState.locations) {
        minX = Math.min(minX, loc.x);
        maxX = Math.max(maxX, loc.x);
        minY = Math.min(minY, loc.y);
        maxY = Math.max(maxY, loc.y);
    }

    // From the bucket view — position bounds. Walk each player's dense x/y
    // columns over their active span; padding/dead slots read (0,0) and are
    // skipped (matching the old row shape that omitted them).
    const view = timelineState.bucketView;
    if (view && view.players) {
        for (const name in view.players) {
            const cp = view.players[name];
            const xs = cp.x, ys = cp.y;
            if (!xs || !ys) continue;
            for (let rel = 0; rel < cp.n; rel++) {
                if (!cp.alive[rel]) continue;
                const x = xs[rel], y = ys[rel];
                if (x !== 0 || y !== 0) {
                    if (x < minX) minX = x;
                    if (x > maxX) maxX = x;
                    if (y < minY) minY = y;
                    if (y > maxY) maxY = y;
                }
            }
        }
    }

    // Handle case where no data found
    if (minX === Infinity) {
        minX = -1000; maxX = 1000;
        minY = -1000; maxY = 1000;
    }

    // Add padding (10%)
    const padX = (maxX - minX) * 0.1;
    const padY = (maxY - minY) * 0.1;

    mapState.bounds = {
        minX: minX - padX,
        maxX: maxX + padX,
        minY: minY - padY,
        maxY: maxY + padY
    };
    mapView.refit();
}

// Orbit-camera state, allocated by mvd-map-view (newCamera) — the
// projection, the angle clamping and the fit-to-canvas math all live there;
// see mvd-map-view/src/camera.js for what each field means. panX/panY/zoomK
// carry user pan and zoom on top of the fit base and survive a refit
// (canvas resize, geometry reload), so the user's view is not thrown away.
// Call mapView.refit() when bounds or the canvas change.
//
// Null until the module bootstrap in index.html hands the namespace over,
// which happens after this script's top level but before DOMContentLoaded —
// so every reader (all of them inside functions) sees a live camera.
let _wtc = null;

window.onMapViewReady = (MapView) => {
    mapView = new MapView.MvdMap();
    mapState = mapView.state;
    _wtc = mapView.camera;
    // Mirror component-side changes into the page chrome. The elements are
    // looked up at event time — these listeners attach before DOMContentLoaded.
    // The Player tab's map pane reads this map's loaded data (src/legacy.js).
    window.legacyMapView = mapView;
    mapView.on('follow', () => syncFollowSelectUI());
    mapView.on('camera', () => {
        const btn = document.getElementById('map-3d-toggle');
        if (btn) btn.classList.toggle('active', MapView.is3D(_wtc));
    });
};

// setMapCamera: the single entry point for every rotation source (button,
// harness poke). The 3D-toggle highlight follows via the 'camera' event
// listener installed in onMapViewReady.
function setMapCamera(yaw, pitch) {
    mapView.setCamera(yaw, pitch);
}

// Canvas width used for non-fullscreen rendering. Fullscreen reads the container bbox instead.
const MAP_CANVAS_BASE_WIDTH = 850;

// resizeMapCanvas measures the surface the map should fill — the fullscreen
// panel when active, a fixed width shaped by the world aspect otherwise —
// and pushes the result into the component. All measurement stays here:
// MvdMap.resize is push-only (the MCP host owns its iframe's dimensions).
function resizeMapCanvas() {
    const canvas = mapState.canvas;
    if (!canvas) return;
    const worldW = mapState.bounds ? (mapState.bounds.maxX - mapState.bounds.minX) : 0;
    const worldH = mapState.bounds ? (mapState.bounds.maxY - mapState.bounds.minY) : 0;
    const fs = !!(document.fullscreenElement &&
                  document.fullscreenElement.classList &&
                  document.fullscreenElement.classList.contains('map-panel'));
    let cssW, cssH;
    if (fs && canvas.parentElement) {
        const rect = canvas.parentElement.getBoundingClientRect();
        cssW = Math.max(300, Math.floor(rect.width));
        cssH = Math.max(200, Math.floor(rect.height));
    } else {
        cssW = MAP_CANVAS_BASE_WIDTH;
        cssH = worldW > 0
            ? Math.round(Math.max(400, Math.min(850, cssW * (worldH / worldW))))
            : 700;
    }
    mapView.resize(cssW, cssH, window.devicePixelRatio || 1);
}

function resetMapView() {
    mapView.resetView();
}

// (The projection wrappers — worldToCanvas and friends — are gone: every
// consumer now lives on MvdMap and projects through the component's camera.)

function assignPlayerSymbols(result) {
    // Roster source: the KTX demoinfo block where the demo has one, else
    // playerStats — the canonical roster, built from the streams and present
    // on every demo. Both shapes carry `name` and `team`, which is all this
    // needs. Reading demoInfo alone left the symbol map EMPTY on a pre-KTX
    // demo, and an empty symbol map is what the draw loop, the legend, the
    // trail dropdown and the sidebar roster all key off — so a demo with
    // full position streams rendered a map with nobody on it.
    const players = result.demoInfo?.players?.length
        ? result.demoInfo.players
        : playerStatsRows(result);

    mapState.playerSymbols = {};

    // Group players by team
    const teamPlayers = {};
    for (const team of mapState.teams) {
        teamPlayers[team] = [];
    }

    for (const player of players) {
        if (player.team && teamPlayers[player.team]) {
            teamPlayers[player.team].push(player.name);
        }
    }

    // Assign unique first-letter symbols and pre-render to offscreen canvases
    const usedLetters = new Set();
    const allPlayers = [];
    for (let teamIdx = 0; teamIdx < mapState.teams.length; teamIdx++) {
        const team = mapState.teams[teamIdx];
        for (const name of (teamPlayers[team] || [])) {
            allPlayers.push({ name, team, teamIdx });
        }
    }

    // Assign unique letter per player: first unused letter from their name
    for (const player of allPlayers) {
        let letter = '?';
        for (const ch of player.name) {
            const upper = ch.toUpperCase();
            if (upper >= 'A' && upper <= 'Z' && !usedLetters.has(upper)) {
                letter = upper;
                usedLetters.add(upper);
                break;
            }
        }
        if (letter === '?') letter = player.name[0]?.toUpperCase() || '?';

        mapState.playerSymbols[player.name] = {
            symbol: letter,
            team: player.team,
            teamIdx: player.teamIdx,
        };
    }

    // Build legend and refresh the trail-players dropdown now that the
    // player roster is known for this demo.
    buildMapLegend();
    buildTrailPlayersPanel();
    updateTrailButtonStates(); // reflect the initial selection (None on load)
}

// Base size (px) of a player symbol at iconScale=1. The letter circle
// radius / outline width / letter font size all scale proportionally from
// this when we draw for a different iconScale.

function buildMapLegend() {
    const legend = document.getElementById('map-legend');
    if (!legend) return;

    legend.innerHTML = '';

    for (let teamIdx = 0; teamIdx < mapState.teams.length; teamIdx++) {
        const team = mapState.teams[teamIdx];
        const teamHex = TEAM_COLORS[teamIdx] || TEAM_COLORS[0];

        const title = document.createElement('h4');
        title.style.color = teamHex;
        title.id = `map-legend-team-title-${teamIdx}`;
        title.textContent = `${team} — 0 frags`;
        legend.appendChild(title);

        const table = document.createElement('table');
        table.className = 'team-status-table';
        table.innerHTML = `<thead><tr><th></th><th>Player</th><th>Loc</th><th title="Health + armor (sorted by H+A)">Stack</th><th>Wpn</th><th>View</th></tr></thead>`;
        const tbody = document.createElement('tbody');
        tbody.className = 'map-legend-tbody';

        // Name order inside the team — playerSymbols is in roster order,
        // which is frag-ish and demo-dependent. The column headers stay
        // click-sortable for anything else.
        const teamPlayers = Object.entries(mapState.playerSymbols)
            .sort((a, b) => a[0].localeCompare(b[0]));

        for (const [name, info] of teamPlayers) {
            if (info.team === team) {
                const tr = document.createElement('tr');
                tr.dataset.player = name;
                const escapedName = escapeHtml(name);
                tr.innerHTML = `
                    <td><span class="map-legend-symbol" style="color: ${teamHex}">${info.symbol}</span></td>
                    <td>${escapedName}</td>
                    <td class="map-legend-loc" data-player="${escapedName}">-</td>
                    <td class="map-legend-stack" data-player="${escapedName}" data-sort-value="0">-</td>
                    <td class="map-legend-wpn" data-player="${escapedName}">-</td>
                    <td class="map-legend-hub" data-player="${escapedName}"></td>
                `;
                tbody.appendChild(tr);
            }
        }

        table.appendChild(tbody);
        legend.appendChild(table);
    }

    // Make tables sortable. The sort indicator is now inline text (see
    // th.sortable in styles.css), so enabling sort here no longer shifts
    // the column headers out of alignment with the body cells.
    legend.querySelectorAll('.team-status-table').forEach(makeSortable);
}

// Build / refresh the Trails → Players dropdown in the top bar. One checkbox
// per player, wired to the same mapState.enabledPlayers / trailStartTimes
// state the legend previously mutated.
function buildTrailPlayersPanel() {
    const panel = document.getElementById('map-trails-players');
    if (!panel) return;
    panel.innerHTML = '';
    const names = Object.keys(mapState.playerSymbols).sort((a, b) => a.localeCompare(b));
    for (const name of names) {
        const info = mapState.playerSymbols[name];
        const teamIdx = info?.teamIdx ?? 0;
        const teamHex = TEAM_COLORS[teamIdx] || TEAM_COLORS[0];
        const label = document.createElement('label');
        const cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.className = 'map-player-trail-cb';
        cb.dataset.player = name;
        cb.checked = !!mapState.enabledPlayers[name];
        cb.addEventListener('change', () => {
            mapState.enabledPlayers[name] = cb.checked;
            if (cb.checked) {
                mapState.trailStartTimes[name] = mapState.currentTime;
            }
            updateTrailButtonStates();
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
        const nameSpan = document.createElement('span');
        nameSpan.className = 'map-trails-player-name';
        nameSpan.style.color = teamHex;
        nameSpan.textContent = name;
        label.appendChild(cb);
        label.appendChild(nameSpan);
        panel.appendChild(label);
    }
}

function updateMapLegend() {
    const legend = document.getElementById('map-legend');
    if (!legend) return;

    const time = mapState.currentTime;
    const bucket = findBucketAtTime(time);
    const playerData = bucket ? (bucket.p) : null;
    const hubInfo = currentResult?.hubInfo;
    const playerUserIDs = currentResult?.timelineAnalysis?.playerUserIDs || {};
    const fragCounts = MvdLib.fragsAt(currentResult, time);

    // Update team titles with frag counts
    for (let ti = 0; ti < mapState.teams.length; ti++) {
        const titleEl = document.getElementById(`map-legend-team-title-${ti}`);
        if (!titleEl) continue;
        const team = mapState.teams[ti];
        let teamFrags = 0;
        for (const [name, info] of Object.entries(mapState.playerSymbols)) {
            if (info.team === team) teamFrags += fragCounts[name] || 0;
        }
        titleEl.textContent = `${team} — ${teamFrags} frags`;
    }

    // Update per-player cells
    const locCells = legend.querySelectorAll('.map-legend-loc');
    for (const cell of locCells) {
        const name = cell.dataset.player;
        const data = playerData?.[name];
        if (data && !(data.x === 0 && data.y === 0)) {
            cell.textContent = mapView.resolvePlayerLoc(data) || '';
        } else {
            cell.textContent = '';
        }
    }

    const stackCells = legend.querySelectorAll('.map-legend-stack');
    for (const cell of stackCells) {
        const name = cell.dataset.player;
        const data = playerData?.[name];
        if (!data) {
            cell.textContent = '-';
            cell.dataset.sortValue = '0';
            continue;
        }
        const h = data.h ?? data.health ?? 0;
        const a = data.a ?? data.armor ?? 0;
        const at = data.at ?? data.armorType ?? '';
        cell.dataset.sortValue = String(h + a);
        if (h <= 0) {
            cell.textContent = '-';
        } else if (a > 0 && at) {
            cell.innerHTML = `<span class="stack-h">${h}</span> <span class="armor-${at}">${a} ${at.toUpperCase()}</span>`;
        } else if (a > 0) {
            cell.innerHTML = `<span class="stack-h">${h}</span> <span>${a}</span>`;
        } else {
            cell.innerHTML = `<span class="stack-h">${h}</span>`;
        }
    }

    const wpnCells = legend.querySelectorAll('.map-legend-wpn');
    for (const cell of wpnCells) {
        const name = cell.dataset.player;
        const data = playerData?.[name];
        if (data) {
            const wpns = [];
            if (data.rl ?? data.hasRL) wpns.push('RL');
            if (data.lg ?? data.hasLG) wpns.push('LG');
            cell.textContent = wpns.length > 0 ? wpns.join(' ') : '-';
        } else {
            cell.textContent = '-';
        }
    }

    const hubCells = legend.querySelectorAll('.map-legend-hub');
    for (const cell of hubCells) {
        const name = cell.dataset.player;
        cell.innerHTML = MvdLib.hubWatchLinkHtml(name, time, hubInfo, playerUserIDs, timelineState.demoOffset);
    }
}

function updateRegionStatus() {
    const container = document.getElementById('region-status-body');
    if (!container || !mapState.controlRegions || mapState.controlRegions.length === 0) return;
    container.innerHTML = '';

    const time = mapState.currentTime;
    const controlStates = getRegionControlAtTime(time);
    if (!controlStates) return;

    const bucket = findBucketAtTime(time);
    const playerData = bucket ? (bucket.p) : null;
    const teams = mapState.teams || [];

    // Build per-region player lists
    const regionPlayers = {};
    for (const r of mapState.controlRegions) {
        regionPlayers[r.name] = [];
    }

    if (playerData) {
        for (const [name, data] of Object.entries(playerData)) {
            if (data.d || (data.h !== undefined && data.h <= 0)) continue;
            if (data.x === 0 && data.y === 0) continue;

            const nearest = mapView.resolvePlayerLoc(data);
            if (!nearest) continue;
            const regionName = mapState.locToRegion?.[nearest];
            if (!regionName || !regionPlayers[regionName]) continue;

            const sym = mapState.playerSymbols[name];
            regionPlayers[regionName].push({
                name, data, sym,
                teamIdx: sym ? sym.teamIdx : -1,
                hasRL: data.rl || false,
                hasLG: data.lg || false,
            });
        }
    }

    // Build HTML
    let html = '';
    for (const region of mapState.controlRegions) {
        const state = controlStates[region.name] || 'empty';
        const players = regionPlayers[region.name] || [];

        // Status label and color
        let statusLabel, statusColor;
        switch (state) {
            case 'teamAControl':
                statusLabel = teams[0] || 'A';
                statusColor = TEAM_COLORS[0];
                break;
            case 'teamAWeakControl':
                statusLabel = (teams[0] || 'A') + ' (weak)';
                statusColor = TEAM_COLORS[0];
                break;
            case 'teamBControl':
                statusLabel = teams[1] || 'B';
                statusColor = TEAM_COLORS[1];
                break;
            case 'teamBWeakControl':
                statusLabel = (teams[1] || 'B') + ' (weak)';
                statusColor = TEAM_COLORS[1];
                break;
            case 'contested':
                statusLabel = 'Contested';
                statusColor = '#ffffff';
                break;
            case 'weakContested':
                statusLabel = 'Contested (weak)';
                statusColor = '#bbbbbb';
                break;
            default:
                statusLabel = 'Empty';
                statusColor = '#555';
                break;
        }

        // Build row
        const row = document.createElement('div');
        row.className = 'region-status-row';

        const nameSpan = document.createElement('span');
        nameSpan.className = 'region-status-name';
        nameSpan.textContent = region.name;
        row.appendChild(nameSpan);

        const stateSpan = document.createElement('span');
        stateSpan.className = 'region-status-state';
        stateSpan.style.color = statusColor;
        stateSpan.textContent = statusLabel;
        row.appendChild(stateSpan);

        const playersSpan = document.createElement('span');
        playersSpan.className = 'region-status-players';

        // Sort: team A first, then team B
        players.sort((a, b) => a.teamIdx - b.teamIdx);
        if (players.length === 0) {
            playersSpan.textContent = '-';
        } else {
            for (const p of players) {
                const icon = buildPlayerRegionIcon(p);
                icon.title = p.name;
                playersSpan.appendChild(icon);
            }
        }
        row.appendChild(playersSpan);
        container.appendChild(row);
    }
}

// Build a composited canvas icon: player circle+letter with RL/LG weapon icons
// in corners. The icon is a pure function of the player's identity (letter +
// team colour) and current badge set, so it's cached per (name, badge-set):
// updateRegionStatus runs on every setCurrentTime and every 200 ms during
// playback, and without the cache each tick allocated + repainted a fresh
// canvas per player. A player occupies exactly one region per frame, so a
// cached node is appended at most once per render — no double-attach. Keyed by
// name so distinct players never share a node; the cache is cleared per demo.
function buildPlayerRegionIcon(player) {
    const sym = player.sym;
    const letter = sym?.symbol || player.name.charAt(0).toUpperCase();
    const teamColor = TEAM_COLORS[sym?.teamIdx ?? player.teamIdx] || TEAM_COLORS[0];
    const badges = mapView.getActiveBadges(player.data);

    const cache = mapState._regionIconCache || (mapState._regionIconCache = new Map());
    const key = `${player.name}|${letter}|${teamColor}|${badges.join(',')}`;
    let canvas = cache.get(key);
    if (canvas) return canvas;

    const dpr = window.devicePixelRatio || 1;
    const size = 40;
    canvas = document.createElement('canvas');
    canvas.width = Math.round(size * dpr);
    canvas.height = Math.round(size * dpr);
    canvas.style.width = size + 'px';
    canvas.style.height = size + 'px';
    canvas.className = 'region-player-icon';
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    // Draw player symbol centered — fresh-drawn so it's crisp at DPR.
    MapView.drawPlayerSymbolAt(ctx, letter, teamColor, size / 2, size / 2, MapView.PLAYER_SYMBOL_BASE_SIZE);

    // Draw status badges around player symbol
    if (badges.length > 0) {
        MapView.drawBadgesAroundCenter(ctx, badges, size / 2, size / 2, 14, 5);
    }

    cache.set(key, canvas);
    return canvas;
}

// ─── Floor-model geometry + shading helpers ─────────────────────────────────
//
// The floor model (buildFloorModel) extrudes the floor's outer boundary into
// box sides and painter-sorts every triangle by projected camera depth into
// an offscreen canvas keyed by the full camera state, so steady playback just
// blits — only rotation / pan / zoom / focus changes re-render. The helpers
// below find that boundary (floorBoundaryEdges / floorBoundaryWalls) and draw
// the sorted entries. The world layers themselves now live in
// mvd-map-view's MvdMap; what remains here is the per-frame composition.


// Mover (lift/door/plat/train) rendering. A mover reads as a moving piece of
// floor: a single flat silhouette (near hull only), no shading, at the same
// opacity as the floor tops (the old 0.5 alpha left it ghostly next to the
// near-opaque floor). A touch lighter than the backdrop floor so the moving
// piece stays legible at rest; when a player is riding it the mover takes the
// brighter MOVER_FILL_ACTIVE tone so it stands out like an occupied region.

// A player counts as "riding" a posed mover when their XY lands within its
// footprint and their z sits within a player-height window of its top surface.
// Colours for the weapon-fire overlays.
// A beam flashes for this half-window (ms) around its instant.

// Liquid volumes (water/slime/lava) from corpus v4. Rendered as a shaded,
// depth-sorted translucent solid: each face is Lambert-shaded (so the top
// surface reads brighter than the descending sides) and painted back to
// front, so the body reads as a 3D volume with visible depth rather than a
// flat silhouette. Static geometry — drawn live on top of the floor model.

// precomputeFullTrails: derive the trail tracks and death markers from the
// installed frame view — MvdMap.rebuildTrails reads its own frames, player
// symbols and camera scale.
function precomputeFullTrails() {
    mapView.rebuildTrails();
}

// renderMap: the component owns the whole composition, including the frame
// lookup (the columnar view is pushed in via mapView.setFrames). The global
// wrapper survives because ~30 call sites and the parity harness invoke it
// by name.
function renderMap(time) {
    mapView.render(time);
    mapPerfSample();
}

// ─── Map render perf HUD + benchmark ────────────────────────────────────────
//
// The component measures (MvdMap.lastFrameStats: CPU build / GL submit /
// blit split, GlWorld draw counters, optional GPU timer); the host owns the
// presentation. The FPS toggle shows a live overlay; the Bench button plays
// the demo under a fixed 18 s camera script (MapView.benchCameraAt) and
// reports fps + frame-time percentiles, compared against earlier runs of
// the same demo kept in localStorage.

const mapPerf = {
    hudEnabled: false,
    benchActive: false,
    samples: [],     // recent MvdMap.lastFrameStats, newest last
    hudTimer: null,
};

const MAP_BENCH_HISTORY_KEY = 'mvd-map-bench-history';

// mapPerfSample: called after every renderMap. A frame that hit the
// redraw-skip leaves lastFrameStats untouched — the unchanged timestamp
// dedupes it, so the HUD's fps counts *presented* map frames.
function mapPerfSample() {
    if ((!mapPerf.hudEnabled && !mapPerf.benchActive) || !mapView) return;
    const fs = mapView.lastFrameStats;
    if (!fs) return;
    const list = mapPerf.samples;
    if (list.length > 0 && list[list.length - 1].t === fs.t) return;
    list.push(fs);
    if (list.length > 600) list.splice(0, list.length - 300);
}

function updateMapPerfHud() {
    const hud = document.getElementById('map-perf-hud');
    if (!hud || mapPerf.benchActive) return;
    const now = performance.now();
    const recent = mapPerf.samples.filter(s => now - s.t < 1000);
    if (recent.length === 0) {
        hud.textContent = 'map idle — play or drag to sample';
        return;
    }
    const avg = (field) => {
        let sum = 0, n = 0;
        for (const s of recent) {
            if (typeof s[field] === 'number') { sum += s[field]; n++; }
        }
        return n > 0 ? sum / n : null;
    };
    const ms1 = (v) => v === null ? '—' : v.toFixed(1);
    const last = recent[recent.length - 1];
    const gpu = avg('gpuMs');
    hud.textContent =
        `${recent.length} fps\n` +
        `build ${ms1(avg('buildMs'))}  submit ${ms1(avg('submitMs'))}` +
        `  blit ${ms1(avg('blitMs'))}  gpu ${ms1(gpu)} ms\n` +
        `draws ${last.draws}  tris ${last.worldTris}  pts ${last.points}` +
        `  segs ${last.lineSegs}  spr ${last.sprites}\n` +
        `stream ${(last.streamBytes / 1024).toFixed(0)} KiB/frame`;
}

function toggleMapPerfHud() {
    const btn = document.getElementById('map-perf');
    const hud = document.getElementById('map-perf-hud');
    mapPerf.hudEnabled = !mapPerf.hudEnabled;
    if (btn) btn.classList.toggle('active', mapPerf.hudEnabled);
    if (hud) hud.style.display = mapPerf.hudEnabled ? '' : 'none';
    if (mapView) mapView.perfCapture = mapPerf.hudEnabled || mapPerf.benchActive;
    if (mapPerf.hudEnabled) {
        mapPerf.samples = [];
        updateMapPerfHud();
        mapPerf.hudTimer = setInterval(updateMapPerfHud, 250);
        // Seed a sample so the HUD is live before the user touches anything.
        mapState.renderDirty = true;
        renderMap(mapState.currentTime);
    } else if (mapPerf.hudTimer) {
        clearInterval(mapPerf.hudTimer);
        mapPerf.hudTimer = null;
    }
}

// mapBenchDemoKey: identifies "the same demo" across sessions for run
// comparisons. The result JSON carries no filename, so map + canonical
// teams + rounded duration is the fingerprint.
function mapBenchDemoKey() {
    const map = currentResult?.demoInfo?.map || currentResult?.match?.map || '?';
    const teams = (timelineState.teams || []).join('+');
    return `${map}|${teams}|${Math.round(timelineState.duration || 0)}s`;
}

function loadMapBenchHistory() {
    try {
        const raw = localStorage.getItem(MAP_BENCH_HISTORY_KEY);
        const list = raw ? JSON.parse(raw) : [];
        return Array.isArray(list) ? list : [];
    } catch (e) {
        return [];
    }
}

function saveMapBenchHistory(list) {
    try {
        localStorage.setItem(MAP_BENCH_HISTORY_KEY, JSON.stringify(list.slice(-30)));
    } catch (e) { /* storage unavailable — the console log still has the run */ }
}

function runMapBenchmark() {
    if (!mapView || !mapState || mapPerf.benchActive || !currentResult) return;
    if (mapState.isPlaying) stopPlayback();
    const cam = mapView.camera;
    const saved = {
        yaw: cam.yaw, pitch: cam.pitch, zoomK: cam.zoomK,
        panX: cam.panX, panY: cam.panY,
        cx: cam.cx, cy: cam.cy, zMid: cam.zMid,
        time: mapState.currentTime,
        follow: mapState.followPlayer,
        focus: mapState.focusGroupName,
    };
    if (saved.follow) mapView.setFollowPlayer(null);
    if (saved.focus) mapView.setFocusGroup(null);
    // Deterministic launch pose: fit-to-canvas pivot, no pan, scripted
    // camera; the demo plays at 1× from a fixed early timestamp.
    cam.panX = 0;
    cam.panY = 0;
    mapView.refit();
    cam.zMid = cam.zMidDefault || 0;
    const t0Demo = Math.min(30, (timelineState.duration || 600) * 0.25);
    mapPerf.benchActive = true;
    mapView.perfCapture = true;
    const benchBtn = document.getElementById('map-bench');
    if (benchBtn) benchBtn.classList.add('active');
    const hud = document.getElementById('map-perf-hud');
    if (hud) {
        hud.style.display = '';
        hud.textContent = 'benchmarking… (18 s)';
    }
    const samples = [];
    const start = performance.now();
    let lastTick = start;
    const step = () => {
        if (!mapPerf.benchActive) return;   // demo unloaded mid-run
        const now = performance.now();
        const elapsed = (now - start) / 1000;
        const pose = MapView.benchCameraAt(elapsed);
        if (pose.done) {
            finishMapBenchmark(saved, samples);
            return;
        }
        // Set the angles directly, NOT through setAngles: its ±2° cardinal
        // yaw snap freezes a continuous sweep at every 90° crossing (~0.1 s
        // each at this sweep rate), which reads as a periodic judder in the
        // benchmark. The script's pitch is already clamped; yaw needs no
        // normalisation for rendering.
        cam.yaw = pose.yaw;
        cam.pitch = pose.pitch;
        MapView.refreshTrig(cam);
        cam.zoomK = pose.zoomK;
        cam.panX = 0;
        cam.panY = 0;
        mapState.currentTime = t0Demo + elapsed;
        mapState.renderDirty = true;
        renderMap(mapState.currentTime);
        const fs = mapView.lastFrameStats;
        if (fs) samples.push(Object.assign({ dtMs: now - lastTick }, fs));
        lastTick = now;
        requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
}

function finishMapBenchmark(saved, samples) {
    const summary = MapView.summarizeBench(samples);
    const noFrames = summary.frames === 0;
    const canvas = mapState.canvas;
    const record = Object.assign({
        demo: mapBenchDemoKey(),
        px: canvas ? `${canvas.width}x${canvas.height}` : '?',
        date: new Date().toISOString(),
    }, summary);
    let prior = [];
    if (!noFrames) {
        // No GL frames presented (context lost / no geometry) → don't record
        // a meaningless run.
        console.log('[map-bench] ' + JSON.stringify(record));
        const history = loadMapBenchHistory();
        prior = history.filter(r => r.demo === record.demo && r.px === record.px);
        saveMapBenchHistory(history.concat([record]));
    }

    // Restore the user's view and clock.
    const cam = mapView.camera;
    mapPerf.benchActive = false;
    mapView.perfCapture = mapPerf.hudEnabled;
    const benchBtn = document.getElementById('map-bench');
    if (benchBtn) benchBtn.classList.remove('active');
    cam.cx = saved.cx;
    cam.cy = saved.cy;
    cam.zMid = saved.zMid;
    cam.zoomK = saved.zoomK;
    cam.panX = saved.panX;
    cam.panY = saved.panY;
    if (saved.focus) mapView.setFocusGroup(saved.focus);
    if (saved.follow) mapView.setFollowPlayer(saved.follow);
    mapView.setCamera(saved.yaw, saved.pitch);
    setCurrentTime(saved.time);

    // Results overlay: this run, plus the newest earlier run of the same
    // demo at the same surface size when there is one.
    const hud = document.getElementById('map-perf-hud');
    if (hud) {
        hud.style.display = '';
        if (noFrames) {
            hud.textContent = 'bench: no GL frames rendered';
            return;
        }
        const f = summary.frameMs || {};
        let text =
            `bench ${record.demo}\n` +
            `${summary.avgFps} fps avg  ${summary.frames} frames  ` +
            `${summary.longFrames} hitches\n` +
            `frame ms p50 ${f.p50} p95 ${f.p95} max ${f.max}\n` +
            `build p50 ${summary.buildMs?.p50}  submit p50 ${summary.submitMs?.p50}` +
            (summary.gpuMs ? `  gpu p50 ${summary.gpuMs.p50}` : '') + '\n' +
            `draws avg ${summary.draws.avg}`;
        const prev = prior[prior.length - 1];
        if (prev) {
            const d = (record.avgFps - prev.avgFps).toFixed(1);
            text += `\nvs ${prev.date.slice(0, 10)}: ${prev.avgFps} fps` +
                ` (${d >= 0 ? '+' : ''}${d})`;
        }
        text += '\nfull record in the console';
        hud.textContent = text;
        if (!mapPerf.hudEnabled) {
            // Leave the results up for a while, then hide (the FPS toggle
            // owns the overlay otherwise).
            setTimeout(() => {
                if (!mapPerf.hudEnabled && !mapPerf.benchActive) {
                    hud.style.display = 'none';
                }
            }, 30000);
        }
    }
}

// ─── Map Items (armor / weapon / MH / powerup overlays) ────────────────────
//
// Draws a small square per tracked item on the map. Armors render as
// solid-filled coloured squares (RA red, YA yellow, GA green). Weapons,
// MH, and powerups render as black squares with a coloured outline +
// short text label that reuses the timeline colour palette so users
// pattern-match weapons across views. Items currently taken are dimmed.

// Kinds surfaced in the sidebar Items panel. Armors, MH, the two
// "fight-over" weapons (RL, LG), and powerups — the core resources
// players actively contest. Other weapons / ammo / small health are
// still rendered on the map but omitted from the scrolling list.
const PANEL_ITEM_KINDS = new Set(['ra', 'ya', 'ga', 'mh', 'rl', 'lg', 'quad', 'pent', 'ring']);

// ─── Player → floor anchor stems (3D views) ─────────────────────────────────
//
// When the camera is tilted, each player symbol gets a thin vertical stem
// down to the floor surface beneath it, ending in a small ground dot. The
// stem visually anchors the symbol to its floor (a tilted projection
// otherwise leaves symbols floating ambiguously between decks), and its
// length doubles as an air-height cue for jumps and falls.

// ─── Learn-map entity overlay ───────────────────────────────────────────────
//
// Static study view (mapState.learnMode): draws result.mapEntities — the
// map's designed item spawns, spawnpoints, teleporters and buttons — instead
// of players, with per-category toggles (mapState.entityFilters) and arrows
// linking each teleport entrance to its exit. Reuses worldToCanvas so markers
// land exactly where players do.

// setLearnMode swaps the map between live playback and the static
// entity-study view, swapping the sidebar panels accordingly.
function setLearnMode(on) {
    if (on === mapState.learnMode) return;
    mapState.learnMode = on;
    const swapIds = ['map-legend', 'map-items-panel', 'region-status-panel'];
    const entPanel = document.getElementById('map-entities-panel');
    if (on) {
        mapState._preLearnDisplay = {};
        for (const id of swapIds) {
            const el = document.getElementById(id);
            if (el) { mapState._preLearnDisplay[id] = el.style.display; el.style.display = 'none'; }
        }
        if (entPanel) entPanel.style.display = '';
    } else {
        const prev = mapState._preLearnDisplay || {};
        for (const id of swapIds) {
            const el = document.getElementById(id);
            if (el) el.style.display = prev[id] || '';
        }
        if (entPanel) entPanel.style.display = 'none';
    }
    const btn = document.getElementById('map-learn-toggle');
    if (btn) btn.classList.toggle('active', on); // highlight only; label stays "Learn map"
    const tableWrap = document.getElementById('map-entities-table-wrap');
    if (tableWrap) tableWrap.style.display = on ? '' : 'none';
    if (on) buildEntityTable();
    mapState.renderDirty = true;
    renderMap(mapState.currentTime);
    updateUrlState(); // reflect ?learn=1 for deep-linking
}

// Cleartext class labels for the entity table.
const ENTITY_CLASS_LABEL = {
    weapon: 'Weapon', armor: 'Armor', health: 'Health', ammo: 'Ammo',
    powerup: 'Powerup', teleporter: 'Teleporter', spawn: 'Spawn',
    button: 'Button', door: 'Door',
};

// buildEntityTable fills the below-map table with every visible entity
// (respecting the category filters). Teleporters are collapsed to one row
// per entrance→exit pair: Loc is the exit, Source is the entrance.
function buildEntityTable() {
    const tbody = document.getElementById('map-entities-table-body');
    if (!tbody) return;
    const f = mapState.entityFilters;
    const ents = mapState.mapEntities || [];
    const rows = [];

    for (const e of ents) {
        if (e.type === 'teleportSrc' || e.type === 'teleportDst') continue;
        const cat = mapView.entityCategory(e);
        if (!f[cat]) continue;
        rows.push({
            cls: ENTITY_CLASS_LABEL[cat] || cat,
            type: e.type === 'item' ? (e.kind || '') : '',
            name: e.name || '', loc: e.loc || '', source: '',
        });
    }

    if (f.teleporter) {
        const dstByName = {};
        for (const e of ents) {
            if (e.type === 'teleportDst' && e.targetName) dstByName[e.targetName] = e;
        }
        const pairedDst = new Set();
        for (const e of ents) {
            if (e.type !== 'teleportSrc') continue;
            const dst = e.target ? dstByName[e.target] : null;
            if (dst) pairedDst.add(dst);
            // Loc = entrance (where the trigger sits); Source = exit it leads to.
            rows.push({
                cls: 'Teleporter', type: '', name: e.name || '',
                loc: e.loc || '', source: dst ? (dst.loc || '') : '',
            });
        }
        for (const e of ents) { // exits with no matching entrance
            if (e.type === 'teleportDst' && !pairedDst.has(e)) {
                rows.push({ cls: 'Teleporter', type: '', name: e.name || '', loc: '', source: e.loc || '' });
            }
        }
    }

    rows.sort((a, b) =>
        a.cls.localeCompare(b.cls) || a.type.localeCompare(b.type) || a.name.localeCompare(b.name));

    tbody.innerHTML = rows.map(r =>
        `<tr><td>${escapeHtml(r.cls)}</td><td>${escapeHtml(r.type)}</td>` +
        `<td>${escapeHtml(r.name)}</td><td>${escapeHtml(r.loc)}</td>` +
        `<td>${escapeHtml(r.source)}</td></tr>`
    ).join('');
}

// ─── Map Items Panel (sidebar list) ────────────────────────────────────────
//
// Live-updating table of every tracked item with status ("up" / "X.Xs" /
// "held") and region. Shown only when result.items is populated (KTX
// demos); hidden for non-KTX sources that produce no item events.

// Cache the sorted-by-name item list and the <tr>/<td> refs so each
// setCurrentTime tick only updates text, not layout.
const _itemsPanelState = {
    lastResult: null,
    rows: [],       // [{ item, tr, statusTd }]
};

// buildItemSwatch returns a <span> that visually mirrors the on-map
// marker for a given item kind: solid-colour armor squares with a
// black label, or black squares with a coloured outline + matching
// label for weapons / MH / powerups.
function buildItemSwatch(style) {
    const sq = document.createElement('span');
    sq.className = 'item-swatch';
    sq.style.background = style.fill;
    if (style.outline) {
        sq.style.border = `1.5px solid ${style.outline}`;
        sq.style.boxSizing = 'border-box';
    }
    if (style.label) {
        sq.textContent = style.label;
        sq.style.color = style.textColor || style.outline || '#fff';
    }
    return sq;
}

function renderItemsPanel() {
    const panel = document.getElementById('map-items-panel');
    const body = document.getElementById('map-items-body');
    if (!panel || !body) return;

    const items = currentResult?.items?.items;
    if (!items || items.length === 0) {
        panel.style.display = 'none';
        _itemsPanelState.lastResult = null;
        _itemsPanelState.rows = [];
        return;
    }

    // Rebuild rows when the underlying result changes.
    if (_itemsPanelState.lastResult !== currentResult) {
        body.innerHTML = '';
        _itemsPanelState.rows = [];
        // Display order: armors first, then MH, then RL/LG, then
        // powerups. Kinds outside PANEL_ITEM_KINDS are filtered out so
        // the sidebar stays focused on the items players contest.
        const KIND_ORDER = { ra: 0, ya: 1, ga: 2, mh: 3, rl: 4, lg: 5, quad: 6, pent: 7, ring: 8 };
        const sorted = items
            .filter(it => PANEL_ITEM_KINDS.has(it.kind) && MapView.ITEM_MARKER_STYLES[it.kind])
            .sort((a, b) => {
                const ka = KIND_ORDER[a.kind] ?? 99;
                const kb = KIND_ORDER[b.kind] ?? 99;
                if (ka !== kb) return ka - kb;
                return a.name.localeCompare(b.name);
            });
        for (const item of sorted) {
            const style = MapView.ITEM_MARKER_STYLES[item.kind];
            const tr = document.createElement('tr');
            const swatch = document.createElement('td');
            swatch.className = 'item-swatch-cell';
            swatch.appendChild(buildItemSwatch(style));
            const loc = document.createElement('td');
            loc.className = 'item-loc';
            loc.textContent = item.loc || '';
            const status = document.createElement('td');
            status.className = 'item-status';
            tr.appendChild(swatch);
            tr.appendChild(loc);
            tr.appendChild(status);
            body.appendChild(tr);
            _itemsPanelState.rows.push({ item, tr, statusTd: status });
        }
        _itemsPanelState.lastResult = currentResult;
    }

    panel.style.display = '';
    updateItemsPanelStatus(mapState.currentTime);
}

function updateItemsPanelStatus(time) {
    for (const row of _itemsPanelState.rows) {
        const s = mapView.itemStatus(row.item, time);
        row.tr.classList.toggle('taken', !s.up);
        if (s.up) {
            row.statusTd.textContent = 'up';
            row.statusTd.className = 'item-status up';
        } else if (s.pending) {
            row.statusTd.textContent = 'held';
            row.statusTd.className = 'item-status pending';
        } else {
            row.statusTd.textContent = s.secsToRespawn.toFixed(1) + 's';
            row.statusTd.className = 'item-status respawn';
        }
    }
}

// ensureLosComputed runs the lazy line-of-sight pass once via the worker, then
// builds mapState.losByPair / pvsByPair and re-renders. One pass fills both LOS
// and PVS (the reply carries los and pvs per player), so the LOS and PVS
// overlays share this latch. The button shows a pending state while the
// (potentially multi-second on a 4on4) raycast pass runs in the worker — the
// main thread stays responsive. On a BSP-less map the reply has no intervals;
// losComputed still latches so we don't re-ask.
async function ensureLosComputed(losBtn) {
    mapState.losPending = true;
    if (losBtn) losBtn.classList.add('pending');
    try {
        const json = await currentDemo.lineOfSight();
        const players = JSON.parse(json);
        if (players && players.error) throw new Error(players.error);
        mapState.losByPair = MapView.buildVisByPair(players, 'los');
        mapState.pvsByPair = MapView.buildVisByPair(players, 'pvs');
        mapState.losComputed = true;
    } catch (err) {
        console.warn('computeLineOfSight:', err.message);
        // Leave losComputed false so a later toggle can retry.
    } finally {
        mapState.losPending = false;
        if (losBtn) losBtn.classList.remove('pending');
        mapState.renderDirty = true;
        renderMap(mapState.currentTime);
    }
}

// (buildVisByPair, losCovers, the LOS/PVS line overlays and drawTracks now
// live in mvd-map-view — see MvdMap.drawLosLines / drawTracks.)

// findBucketAtTime: the reconstructed row-shape frame at `time`, memoised by
// the component (see MvdMap.frameAt). The legend and region panels share the
// exact object the map rendered from, one reconstruction per frame.
function findBucketAtTime(time) {
    return mapView.frameAt(time);
}

// updateTrailButtonStates: reflect the current trail selection in the control
// buttons — All highlighted when every player's trail is on, None when none
// are, and the "Players" dropdown summary when a custom subset is chosen. Call
// after any change to mapState.enabledPlayers.
function updateTrailButtonStates() {
    const names = Object.keys(mapState.fullTrails || {});
    const total = names.length;
    let on = 0;
    for (const n of names) if (mapState.enabledPlayers[n]) on++;
    const allBtn = document.getElementById('map-trails-all');
    const noneBtn = document.getElementById('map-trails-none');
    const summary = document.querySelector('#map-trails-dropdown > summary');
    if (allBtn)  allBtn.classList.toggle('active', total > 0 && on === total);
    if (noneBtn) noneBtn.classList.toggle('active', on === 0);
    if (summary) summary.classList.toggle('active', on > 0 && on < total);
}

function setupMapTrailControls() {
    const allBtn = document.getElementById('map-trails-all');
    if (allBtn) {
        allBtn.addEventListener('click', () => {
            for (const name of Object.keys(mapState.fullTrails)) {
                // Only reset start time for newly-enabled players
                if (!mapState.enabledPlayers[name]) {
                    mapState.trailStartTimes[name] = mapState.currentTime;
                }
                mapState.enabledPlayers[name] = true;
            }
            // Sync legend checkboxes
            document.querySelectorAll('.map-player-trail-cb').forEach(cb => { cb.checked = true; });
            updateTrailButtonStates();
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const noneBtn = document.getElementById('map-trails-none');
    if (noneBtn) {
        noneBtn.addEventListener('click', () => {
            for (const name of Object.keys(mapState.fullTrails)) {
                mapState.enabledPlayers[name] = false;
            }
            document.querySelectorAll('.map-player-trail-cb').forEach(cb => { cb.checked = false; });
            updateTrailButtonStates();
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const resetTracksBtn = document.getElementById('map-reset-tracks');
    if (resetTracksBtn) {
        resetTracksBtn.addEventListener('click', () => {
            for (const name of Object.keys(mapState.fullTrails)) {
                mapState.trailStartTimes[name] = mapState.currentTime;
            }
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const durationSelect = document.getElementById('map-trail-duration');
    if (durationSelect) {
        durationSelect.addEventListener('change', (e) => {
            mapState.trailDuration = parseInt(e.target.value, 10);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const followSel = document.getElementById('map-follow');
    if (followSel) {
        followSel.addEventListener('change', (e) => {
            setFollowPlayer(e.target.value || null);
        });
    }

    const btn3d = document.getElementById('map-3d-toggle');
    if (btn3d) {
        btn3d.addEventListener('click', () => {
            // Toggle between top-down (2D, yaw 0) and the default isometric
            // view (yaw 45° / 55° tilt). Free rotation is available via
            // right-drag / Ctrl+drag.
            setMapCamera(MapView.is3D(_wtc) ? 0 : MapView.DEFAULT_YAW,
                         MapView.is3D(_wtc) ? MapView.PITCH_MAX : MapView.DEFAULT_PITCH);
        });
    }

    const viewArrowsBtn = document.getElementById('map-view-arrows');
    if (viewArrowsBtn) {
        viewArrowsBtn.addEventListener('click', () => {
            mapState.showViewArrows = !mapState.showViewArrows;
            viewArrowsBtn.classList.toggle('active', mapState.showViewArrows);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const velArrowsBtn = document.getElementById('map-vel-arrows');
    if (velArrowsBtn) {
        velArrowsBtn.addEventListener('click', () => {
            mapState.showVelArrows = !mapState.showVelArrows;
            velArrowsBtn.classList.toggle('active', mapState.showVelArrows);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    }

    const losBtn = document.getElementById('map-los');
    if (losBtn) {
        losBtn.addEventListener('click', () => {
            mapState.showLos = !mapState.showLos;
            losBtn.classList.toggle('active', mapState.showLos);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
            // Compute LOS lazily the first time it is switched on.
            if (mapState.showLos && !mapState.losComputed && !mapState.losPending) {
                ensureLosComputed(losBtn);
            }
        });
    }

    const pvsBtn = document.getElementById('map-pvs');
    if (pvsBtn) {
        pvsBtn.addEventListener('click', () => {
            mapState.showPvs = !mapState.showPvs;
            pvsBtn.classList.toggle('active', mapState.showPvs);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
            // PVS rides on the same lazy pass as LOS; compute on first need.
            if (mapState.showPvs && !mapState.losComputed && !mapState.losPending) {
                ensureLosComputed(pvsBtn);
            }
        });
    }

    // Effect toggles (GL renderer): depth fog, floor shading, realistic
    // occlusion. Each just flips the component state and repaints.
    const fxToggle = (id, apply) => {
        const btn = document.getElementById(id);
        if (!btn) return;
        btn.addEventListener('click', () => {
            const on = apply();
            btn.classList.toggle('active', on);
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    };
    fxToggle('map-fog', () => (mapState.fog = mapState.fog > 0 ? 0 : 1) > 0);
    fxToggle('map-light', () => (mapState.worldLight = mapState.worldLight > 0 ? 0 : 0.6) > 0);
    fxToggle('map-occlude', () => (mapState.occludeActors = !mapState.occludeActors));

    const perfBtn = document.getElementById('map-perf');
    if (perfBtn) {
        perfBtn.addEventListener('click', () => toggleMapPerfHud());
    }
    const benchBtn = document.getElementById('map-bench');
    if (benchBtn) {
        benchBtn.addEventListener('click', () => runMapBenchmark());
    }

    const resetViewBtn = document.getElementById('map-reset-view');
    if (resetViewBtn) {
        resetViewBtn.addEventListener('click', () => { resetMapView(); });
    }

    const fsBtn = document.getElementById('map-fullscreen');
    if (fsBtn) {
        fsBtn.addEventListener('click', () => { toggleMapFullscreen(); });
    }

    const learnBtn = document.getElementById('map-learn-toggle');
    if (learnBtn) {
        learnBtn.addEventListener('click', () => setLearnMode(!mapState.learnMode));
    }
    document.querySelectorAll('.map-entity-cb').forEach(cb => {
        cb.addEventListener('change', (e) => {
            mapState.entityFilters[e.target.dataset.cat] = e.target.checked;
            if (mapState.learnMode) buildEntityTable();
            mapState.renderDirty = true;
            renderMap(mapState.currentTime);
        });
    });

    // React to fullscreen changes regardless of who triggered them (button,
    // Escape key, browser UI). Only one listener is needed for the page.
    if (!setupMapTrailControls.__fsListenerAttached) {
        document.addEventListener('fullscreenchange', onMapFullscreenChange);
        window.addEventListener('resize', onMapWindowResize);
        // Escape clears region focus (no-op otherwise, so it doesn't fight
        // other Escape behaviours like exiting fullscreen).
        document.addEventListener('keydown', (ev) => {
            if (ev.key === 'Escape' && mapState.focusGroupName) {
                setFocusGroup(null);
            }
        });
        setupMapTrailControls.__fsListenerAttached = true;
    }
}

// installMapInteraction wires the DOM events to the component's pointer
// state machine. The gesture semantics (pan / orbit / click-to-follow /
// wheel zoom anchored on the cursor) live on MvdMap; this glue only turns
// browser events into canvas-space CSS-pixel calls. mousemove/mouseup attach
// to window so a drag keeps tracking outside the canvas.
function installMapInteraction(canvas) {
    if (!canvas || canvas.__mapInteractionInstalled) return;
    canvas.__mapInteractionInstalled = true;

    function canvasPointFromEvent(ev) {
        // CSS pixel coords relative to the canvas origin — matches what
        // the renderer uses now that setTransform(dpr) handles the CSS →
        // physical scaling for drawing.
        const rect = canvas.getBoundingClientRect();
        return {
            x: ev.clientX - rect.left,
            y: ev.clientY - rect.top,
        };
    }

    canvas.addEventListener('mousedown', (ev) => {
        if (ev.button !== 0 && ev.button !== 2) return;
        const p = canvasPointFromEvent(ev);
        const mode = (ev.button === 2 || ev.ctrlKey || ev.metaKey) ? 'orbit' : 'pan';
        mapView.pointerDown(p.x, p.y, mode, ev.button);
        ev.preventDefault();
    });

    // Right-drag is the orbit gesture — keep the browser context menu off
    // the canvas so it doesn't swallow the mouseup.
    canvas.addEventListener('contextmenu', (ev) => ev.preventDefault());

    window.addEventListener('mousemove', (ev) => {
        const p = canvasPointFromEvent(ev);
        mapView.pointerMove(p.x, p.y);
    });

    window.addEventListener('mouseup', (ev) => {
        const p = canvasPointFromEvent(ev);
        mapView.pointerUp(p.x, p.y);
    });

    canvas.addEventListener('wheel', (ev) => {
        ev.preventDefault();
        const p = canvasPointFromEvent(ev);
        mapView.wheelZoom(p.x, p.y, ev.deltaY);
    }, { passive: false });

    canvas.addEventListener('dblclick', (ev) => {
        ev.preventDefault();
        resetMapView();
    });

    canvas.style.cursor = 'grab';
}

// ─── Follow-player ────────────────────────────────────────────────────────

function setFollowPlayer(name) {
    mapView.setFollowPlayer(name);
}

function syncFollowSelectUI() {
    const sel = document.getElementById('map-follow');
    if (!sel) return;
    sel.value = mapState.followPlayer || '';
}

function rebuildFollowSelect() {
    const sel = document.getElementById('map-follow');
    if (!sel) return;
    const prev = mapState.followPlayer || '';
    sel.innerHTML = '';
    const off = document.createElement('option');
    off.value = '';
    off.textContent = 'Off';
    sel.appendChild(off);
    const names = Object.keys(mapState.fullTrails).sort((a, b) => a.localeCompare(b));
    for (const n of names) {
        const opt = document.createElement('option');
        opt.value = n;
        opt.textContent = n;
        sel.appendChild(opt);
    }
    if (prev && !names.includes(prev)) {
        mapState.followPlayer = null;
    }
    sel.value = mapState.followPlayer || '';
}

// ─── Fullscreen ───────────────────────────────────────────────────────────

function toggleMapFullscreen() {
    const panel = document.querySelector('#tab-map .map-panel');
    if (!panel) return;
    if (document.fullscreenElement === panel) {
        document.exitFullscreen().catch(() => {});
    } else {
        const req = panel.requestFullscreen?.bind(panel);
        if (req) req().catch(() => {});
    }
}

// Remembers the unified-timeline's original parent / sibling slot so we can
// put it back after leaving fullscreen. Populated the first time we relocate.
let _fsTimelineHome = null;

function onMapFullscreenChange() {
    const panel = document.querySelector('#tab-map .map-panel');
    if (!panel) return;
    const nowFs = document.fullscreenElement === panel;
    panel.classList.toggle('map-panel--fullscreen', nowFs);
    mapState.fullscreen = nowFs;
    const btn = document.getElementById('map-fullscreen');
    if (btn) btn.classList.toggle('active', nowFs); // highlight only; label stays "Fullscreen"

    // Relocate the shared timeline (playback buttons + scrubber) into the
    // fullscreen map panel so it stays usable. On exit, put it back.
    const tl = document.getElementById('unified-timeline');
    if (tl) {
        if (nowFs) {
            if (!_fsTimelineHome) {
                _fsTimelineHome = { parent: tl.parentNode, next: tl.nextSibling };
            }
            // Pin to the top of the fullscreen panel so the visual order is
            // timeline → canvas → controls (controls live at the bottom of
            // .map-panel via the HTML layout).
            panel.insertBefore(tl, panel.firstChild);
        } else if (_fsTimelineHome && _fsTimelineHome.parent) {
            _fsTimelineHome.parent.insertBefore(tl, _fsTimelineHome.next);
        }
    }

    // Canvas backing store must match the new container size; re-render.
    resizeMapCanvas();
    mapState.renderDirty = true;
    renderMap(mapState.currentTime);
}

let _mapResizeRafId = null;
function onMapWindowResize() {
    // Only active (debounced to next frame) while in fullscreen; the
    // non-fullscreen canvas is fixed-size so the resize listener is a no-op.
    if (!mapState.fullscreen) return;
    if (_mapResizeRafId !== null) return;
    _mapResizeRafId = requestAnimationFrame(() => {
        _mapResizeRafId = null;
        resizeMapCanvas();
        mapState.renderDirty = true;
        renderMap(mapState.currentTime);
    });
}

// ─── Playback Engine ──────────────────────────────────────────────────────

const PLAYBACK_BUTTON_LABELS = {
    'tl-rev': '-1x',
    'tl-slow': '0.2x',
    'tl-play-pause': '1x',
    'tl-5x': '5x'
};

function updatePlaybackButtons() {
    const buttons = {
        'tl-rev': -1,
        'tl-slow': 0.2,
        'tl-play-pause': 1,
        'tl-5x': 5
    };
    const drives = playerDrives();
    const engine = MvdPlayer.status.get();
    const playing = drives ? engine.playing : mapState.isPlaying;
    const current = drives ? engine.speed / 100 : mapState.playbackSpeed;
    for (const [id, speed] of Object.entries(buttons)) {
        const btn = document.getElementById(id);
        if (!btn) continue;
        // FTE cannot play a demo backwards.
        btn.disabled = drives && speed < 0;
        if (playing && current === speed) {
            btn.classList.add('active');
            btn.textContent = '⏸';
        } else {
            btn.classList.remove('active');
            btn.textContent = PLAYBACK_BUTTON_LABELS[id];
        }
    }
}

function startPlaybackAtSpeed(speed) {
    if (playerDrives()) {
        MvdPlayer.setSpeed(speed);
        return;
    }
    if (mapState.isPlaying && mapState.playbackSpeed === speed) {
        // Toggle off — pause
        stopPlayback();
        return;
    }

    mapState.playbackSpeed = speed;
    if (!mapState.isPlaying) {
        mapState.isPlaying = true;
        mapState.lastRenderTime = performance.now();
        animatePlayback();
    }
    updatePlaybackButtons();
}

function stopPlayback() {
    mapState.isPlaying = false;
    if (mapState.animationFrameId) {
        cancelAnimationFrame(mapState.animationFrameId);
        mapState.animationFrameId = null;
    }
    updatePlaybackButtons();
    setCurrentTime(mapState.currentTime);
}

let _lastFullSyncTime = 0;

function animatePlayback() {
    if (!mapState.isPlaying) {
        mapState.animationFrameId = null;
        return;
    }

    mapState.animationFrameId = requestAnimationFrame(animatePlayback);

    const now = performance.now();
    const elapsed = (now - mapState.lastRenderTime) / 1000;

    // Throttle map redraws to PLAYBACK_FPS_MS (~60 fps).
    if (elapsed < PLAYBACK_FPS_MS / 1000) return;

    mapState.currentTime += elapsed * mapState.playbackSpeed;
    mapState.lastRenderTime = now;

    const duration = timelineState.duration || 600;

    // Forward past end: wrap to 0
    if (mapState.currentTime > duration) {
        mapState.currentTime = 0;
        mapState.renderDirty = true;
    }

    // Reverse past start: stop at 0
    if (mapState.currentTime < 0) {
        mapState.currentTime = 0;
        stopPlayback();
        return;
    }

    // Lightweight sync every frame
    MvdState.clock.seek(mapState.currentTime, 'playback');
    updateUnifiedCursor();
    updateUnifiedTimeDisplay();
    renderMap(mapState.currentTime);

    // Full sync every 200ms
    if (now - _lastFullSyncTime > 200) {
        _lastFullSyncTime = now;
        mapState.renderDirty = true;
        updateMapLegend();
        updateRegionStatus();
        updateItemsPanelStatus(mapState.currentTime);
    }
}
