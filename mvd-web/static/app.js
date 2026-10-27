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

// Derive a strong (darkened) color variant from a hex color for region
// control displays.
function hexToRgb(hex) {
    return [parseInt(hex.slice(1, 3), 16), parseInt(hex.slice(3, 5), 16), parseInt(hex.slice(5, 7), 16)];
}
function teamStrongColor(hex) {
    const [r, g, b] = hexToRgb(hex);
    // Darken by 30% for strong control
    return `rgb(${Math.round(r * 0.7)}, ${Math.round(g * 0.7)}, ${Math.round(b * 0.7)})`;
}

// ─── Table builder ──────────────────────────────────────────────────────────
//
// Clear tbody, loop, build a <tr>, paint a 3px team-coloured border-left,
// set innerHTML, append — the loc / region heatmaps' tbody builder (the
// Summary tab's tables do the same in src/tabs/summary/).
//
// `getTeamIdx` is optional — pass it for tables whose rows should get a
// team colour stripe. The helper looks up the colour from TEAM_COLORS so
// the palette stays in one place.
function renderTableRows(tbodyId, items, buildRow, getTeamIdx) {
    const tbody = document.getElementById(tbodyId);
    if (!tbody) return;
    tbody.innerHTML = '';
    items.forEach((item, i) => {
        const tr = document.createElement('tr');
        if (getTeamIdx) {
            const teamIdx = getTeamIdx(item, i);
            if (teamIdx >= 0 && teamIdx < TEAM_COLORS.length) {
                tr.style.borderLeft = `3px solid ${TEAM_COLORS[teamIdx]}`;
            }
        }
        tr.innerHTML = buildRow(item, i);
        tbody.appendChild(tr);
    });
}

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
// RE-RUNS the bucket-dependent initialisers (region control, timeline, map).
// They already ran once in displayResults with the bucket-derived fields
// empty (blank timeline graph / map trails / region overlay); re-running them
// now fills those in. Finally re-render the active tab so a Timeline/Map view
// the user already opened repaints.
function onBucketsReady(data) {
    const ta = currentResult.timelineAnalysis;

    // Re-run the inits, now that bucket-derived data is present. Order
    // matches displayResults: region control feeds timeline + map.
    if (ta && ta.regionControl) initRegionControlData(currentResult);
    if (ta || currentResult.messages?.messages) displayTimelineAnalysis(currentResult);
    if (ta) initMapView(currentResult);

    // Re-render whichever tab is active so Timeline/Map paint with the new
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
    let clockSubscribed = false;
    MvdState.clock.subscribe((tick) => { if (clockSubscribed) onClockTick(tick); });
    clockSubscribed = true;
    setupFileUpload();
    setupTabs();
    MvdPlayer.status.subscribe(() => updatePlaybackButtons());
    setupSearch();
    setupTeamStatusSort();

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
// frame, the rest at the playback loop's 200 ms full-sync rate.
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
                updateTimeIndicators();
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
    updateTimeIndicators(); // tail-calls updateTeamStatus() when range > 0
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

            if (timelineState.segment) {
                params.set('seg', `${Math.round(timelineState.segment.start)}-${Math.round(timelineState.segment.end)}`);
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
        if (!isNaN(start) && !isNaN(end)) {
            timelineState.segment = { start, end };
            updateSelectionOverlay();
            updateSegmentLabel();
            updateDetailView();
        }
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
function renderTabContent(tabName) {
    if (tabName === 'map') {
        mapState.renderDirty = true;
        mapState.lastRenderedBucket = null;
        renderMap(mapState.currentTime);
    } else if (tabName === 'timeline') {
        if (currentResult) updateDetailView();
        updateTimeIndicators();
    } else if (tabName === 'loc-graph') {
        renderLocGraph();
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

    // Region control data (needed by both timeline and map). Cheap on the
    // main thread; the multi-second cost was the worker's bucket build,
    // which now runs after the result is delivered. These run here with the
    // bucket-derived fields still empty (timeline graph / map trails /
    // region overlay blank) and onBucketsReady() re-runs them once the
    // 'buckets' message lands. displayTimelineAnalysis also populates the
    // timeline strip, so it must run now.
    if (result.timelineAnalysis?.regionControl) {
        initRegionControlData(result);
    }

    // Timeline Analysis (new graphical view)
    if (result.timelineAnalysis || result.messages?.messages) {
        timeRender('displayTimelineAnalysis', () => displayTimelineAnalysis(result));
    }

    // Map View
    if (result.timelineAnalysis) {
        timeRender('initMapView', () => initMapView(result));
    }

    // Loc Graph
    timeRender('initLocGraphView', () => initLocGraphView(result));

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

// applyDuelModeUI toggles the "Per Team" aggregation panels and the
// standalone "Teams" summary off when we're rendering a 1v1 demo.
// Everything else (the per-player scoreboard, weapon stats, item
// pickups) still renders normally.
//
// Detection: the Go `roster` core node stamps every participant team field
// as their own name for duels (and only for exactly-2-player matches — see
// newRoster in analyzer/roster.go), so we can detect duel mode reliably by
// checking whether the two players each have `team === name`. This avoids
// depending on the metadata mode string, which can be "duel" / "1on1" /
// "LGC" / "Hoony" / missing entirely depending on the server flavour.
//
// Read off playerStats rather than demoInfo: the roster stamp is what is
// being tested, playerStats carries it on every demo, and a pre-KTX-block
// duel used to fall through here and render the team panels for a 1v1.
function isDuel(result) {
    const players = playerStatsRows(result);
    if (!players.length) return false;
    return players.length === 2 && players.every(p => p.team === p.name);
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

// individualFieldActive reports the layout currently applied. The render paths
// read the body class rather than re-deriving from the result so the DOM, the
// row grouping and the row heights can never disagree — and displayTimeline-
// Analysis's title write runs again from onBucketsReady long after
// applyDuelModeUI, where the class is what survives.
function individualFieldActive() {
    return document.body.classList.contains('individual-field');
}

// The three per-player drill-downs and where each lives in the two layouts:
// `node` is the content that MOVES, `home` the <details> it sits in under its
// team graph, `host` the body of the panel it is promoted into. The node is
// re-parented, never cloned — it owns live canvases plus the `_sig` / `_cells`
// rebuild cache, and a second copy would render into a detached tree.
const PER_PLAYER_VIEWS = [
    { node: 'frags-per-player',        home: 'frags-per-player-details',   host: 'frags-per-player-host',   panel: 'frags-per-player-panel'   },
    { node: 'weapons-per-player-rows', home: 'weapons-per-player-details', host: 'weapons-per-player-host', panel: 'weapons-per-player-panel' },
    { node: 'ha-per-player',           home: 'ha-per-player-details',      host: 'ha-per-player-host',      panel: 'ha-per-player-panel'      },
];

// setIndividualFieldLayout switches every two-sided surface between its two
// forms, off one body class.
//
// Timeline: hide the A-vs-B graphs and promote the three per-player views to
// panels of their own. Chat (src/tabs/chat/): CSS hides the second say column
// and its header off the class; the tab fills the first from the teams store.
function setIndividualFieldLayout(on) {
    document.body.classList.toggle('individual-field', on);
    for (const v of PER_PLAYER_VIEWS) {
        const panel = document.getElementById(v.panel);
        if (panel) panel.style.display = on ? '' : 'none';
        const node = document.getElementById(v.node);
        const target = document.getElementById(on ? v.host : v.home);
        if (!node || !target || node.parentElement === target) continue;
        target.appendChild(node);
    }
    // The selected-range suffix rides along: its only home is the Weapons
    // heading, which this layout hides.
    const rangeLabel = document.getElementById('time-range-label');
    const rangeHome = document.getElementById(on ? 'weapons-per-player-heading' : 'weapons-timeline-heading');
    if (rangeLabel && rangeHome && rangeLabel.parentElement !== rangeHome) rangeHome.appendChild(rangeLabel);
}

// Row heights for the per-player views. The compact pair is what a collapsed
// drill-down under a team graph uses; promoted to a panel of their own these
// rows carry the tab, with no team graph above them competing for the fold, so
// they get more pixels each.
function ppMiniHeight() {
    return individualFieldActive() ? 60 : 44;
}

function ppSpanRowHeight() {
    return individualFieldActive() ? 28 : RC_ROW_H;
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

// Timeline Analysis State
let timelineState = {
    bucketView: null,      // column-major ColumnarBuckets (50ms) from getDefaultBuckets
    highResDuration: 0.05, // High-res bucket interval
    duration: 0,
    matchStartTime: 0,
    teams: [],
    segment: null, // { start, end } or null for full match - selected time segment
    dragging: false, // Is user dragging to select a segment on overview?
    dragStartTime: 0 // Time at drag start
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

    const setText = (id, v) => {
        const el = document.getElementById(id);
        if (el) el.textContent = v;
    };
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

    // Timeline canvases (DOM order)
    for (const cid of [
        'score-canvas', 'powerup-canvas',
        'detail-graph-canvas', 'health-armor-canvas',
        'region-control-canvas', 'weapons-per-player-canvas',
    ]) {
        const c = document.getElementById(cid);
        if (c && c.getContext) c.getContext('2d').clearRect(0, 0, c.width, c.height);
    }
    hide('powerup-timeline-panel');
    hide('region-control-timeline-panel');
    // Two-sided default for the timeline AND the chat columns (CSS): the
    // per-player views return to their <details> under the team graphs.
    // applyDuelModeUI re-applies the individual field layout below if this
    // demo wants it.
    setIndividualFieldLayout(false);
    hide('unified-timeline');
    setText('time-range-label', '');
    setHTML('team-status-a', '');
    setHTML('team-status-b', '');
    teamStatusSort = { ...TEAM_STATUS_SORT_DEFAULT };

    // Map view
    const mapCanvas = document.getElementById('map-canvas');
    if (mapCanvas && mapCanvas.getContext) {
        mapCanvas.getContext('2d').clearRect(0, 0, mapCanvas.width, mapCanvas.height);
    }
    setHTML('map-legend', '');
    setHTML('map-items-body', '');
    hide('map-items-panel');
    hide('map-no-data');
    setHTML('region-control-body', '');
    hide('region-control-panel');
    setHTML('region-status-body', '');
    hide('region-status-panel');
    setHTML('region-config', '');

    // Loc graph
    if (locGraphState.cy) {
        try { locGraphState.cy.destroy(); } catch (_) {}
        locGraphState.cy = null;
    }
    locGraphState.graph = null;
    locGraphState.result = null;
    setHTML('locgraph-canvas', '');
    hide('locgraph-no-data');
    setHTML('locheatmap-body', '');
    hide('locheatmap-panel');

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
    if ('rcResult' in mapState) mapState.rcResult = null;
    if ('locToRegion' in mapState) mapState.locToRegion = {};
    if ('dropEvents' in mapState) mapState.dropEvents = [];
    if ('zRange' in mapState) mapState.zRange = null;
    if ('locTable' in mapState) mapState.locTable = [''];

    if (typeof weaponGraphHitState !== 'undefined') {
        weaponGraphHitState.W = null;
        weaponGraphHitState.dropMarks = [];
    }
    if (typeof powerupGraphHitState !== 'undefined') {
        powerupGraphHitState.W = null;
        powerupGraphHitState.rows = [];
    }
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
    timelineState.highResDuration = 0.05;
    timelineState.fragEvents = [];
    timelineState.deathEvents = [];
    timelineState.killEvents = [];
    timelineState.duration = 0;
    MvdState.clock.setDuration(0);
    timelineState.matchStartTime = 0;
    timelineState.demoOffset = 0;
    MvdState.setCanonicalTeams([]);
    timelineState.segment = null;
    timelineState.dragging = false;
    precomputedFrags = [];

    // Clear the timeline axis
    const axis = document.getElementById('tl-axis');
    if (axis) axis.innerHTML = '';
    // Clear canvases
    for (const cid of ['detail-graph-canvas', 'health-armor-canvas', 'score-canvas']) {
        const c = document.getElementById(cid);
        if (c && c.getContext) {
            const ctx = c.getContext('2d');
            ctx.clearRect(0, 0, c.width, c.height);
        }
    }
}

function displayTimelineAnalysis(result) {
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
    const teams = timelineState.teams;

    // Schema v7: bucketed data is not a parse-time field; the WASM worker
    // calls getDefaultBuckets after analyzeMVD and onBucketsReady
    // stashes the resulting column-major ColumnarBuckets object on
    // timeline.bucketView. The panels read it via the bucket-view accessors.
    timelineState.bucketView = timeline?.bucketView || null;
    mapView.setFrames(timelineState.bucketView);
    timelineState.highResDuration = 0.05;
    // Schema v8: raw time fields on result/timelineAnalysis flipped from
    // float seconds to int32 ms. The frontend internally still works in
    // seconds (mapState.currentTime, formatDuration,
    // hub URL `from`/`to`, etc. all expect seconds), so convert ms→s once
    // here at intake. Anything copied into timelineState below is in
    // seconds and panels downstream are unchanged.
    // Schema v23 moved the timing anchor from timelineAnalysis to
    // streams.global. matchStartTime is always 0 (the match-relative origin).
    timelineState.matchStartTime = 0;
    timelineState.demoOffset = (result.streams?.global?.demoOffset || 0) * 0.001;
    timelineState.duration = (result.match?.duration || 600000) * 0.001;
    MvdState.clock.setDuration(timelineState.duration);
    // Sorted by time once here so prepScoreData / precomputeFragCounts don't
    // each re-sort a clone on every render (pan-drag re-renders many times/sec).
    timelineState.fragEvents = (timeline?.fragEvents || []).map(f => ({ ...f, time: f.time * 0.001 })).sort((a, b) => a.time - b.time); // Frag events from stat tracking
    timelineState.deathEvents = (timeline?.deathEvents || []).map(d => ({ ...d, time: d.time * 0.001 })); // Per-player deaths (every death) for the frags/deaths drill-down
    timelineState.killEvents = (timeline?.killEvents || []).map(k => ({ ...k, time: k.time * 0.001 })); // Per-player enemy kills (killer-keyed) for the frags/deaths drill-down
    timelineState.backpacks = (result.backpacks || []).map(d => ({ ...d, time: d.time * 0.001 })); // RL/LG drops (KTX hint or reconstructed — see d.source)
    timelineState.powerupEvents = (timeline?.powerupEvents || []).map(ev => ({ // per-run records: player, team, frags, duration
        ...ev,
        time: ev.time * 0.001,
        endTime: (ev.endTime || 0) * 0.001,
        duration: (ev.duration || 0) * 0.001,
    }));

    // Note: the shared playhead (mapState.currentTime) is initialised to 0 by
    // resetUIToCleanState at the start of each demo load. It is deliberately
    // NOT reset here — displayTimelineAnalysis is re-run by onBucketsReady
    // seconds after load, and a reset there would clobber a ?t= deep link or
    // any early scrubbing the user did during the deferred-bucket window.

    // Update legend team names
    if (teams.length >= 2) {
        const setTextIfExists = (id, text) => { const el = document.getElementById(id); if (el) el.textContent = text; };
        setTextIfExists('legend-health-team-a', teams[0] + ' ↑');
        setTextIfExists('legend-health-team-b', teams[1] + ' ↓');
        setTextIfExists('legend-weapons-team-a', teams[0] + ' ↑');
        setTextIfExists('legend-weapons-team-b', teams[1] + ' ↓');
    }

    precomputeFragCounts();
    setupUnifiedTimeline();

    // Show the unified timeline on applicable tabs
    const activeTab = activeTabName();
    const tl = document.getElementById('unified-timeline');
    if (tl) tl.style.display = TABS_WITH_TIMELINE.includes(activeTab) ? '' : 'none';

    updateUnifiedCursor();
    updateDetailView();
    updateTimeIndicators();
    updateTeamStatus();
}

// ─── Columnar bucket-view accessors ──────────────────────────────────────────
//
// The column-major ColumnarBuckets object (see mvd-analytics/view/columnar.go)
// is stored on timelineState.bucketView and pushed into the map component via
// mapView.setFrames. The accessors themselves live in
// mvd-map-view/src/frames.js — one implementation, so the timeline panels and
// the map can never disagree on the row shape. These wrappers keep the
// long-standing local names for the panel code.

function bucketTimeSec(view, i) {
    return MapView.bucketTimeSec(view, i);
}

function bucketIndexAtTime(view, tSec) {
    return MapView.bucketIndexAtTime(view, tSec);
}

function bucketIndexAtOrAfter(view, tSec) {
    return MapView.bucketIndexAtOrAfter(view, tSec);
}

function playerValAt(p, field, i) {
    return MapView.playerValAt(p, field, i);
}

function playerAliveAt(p, i) {
    return MapView.playerAliveAt(p, i);
}

function teamSnapshot(view, team, i) {
    return MapView.teamSnapshot(view, team, i);
}

// ─── Unified Canvas Graph Renderer ──────────────────────────────────────────
//
// All timeline graphs (weapons, health/armor, frags, score) share a single
// canvas-based diverging-bar renderer. Each graph type provides a data
// preparation function that returns an array of data points in a common
// format. The renderer draws them at full resolution on a <canvas>.

const GRAPH_COLORS = {
    RL:     'rgba(255, 107, 107, 0.9)',
    LG:     'rgba(0, 217, 255, 0.9)',
    RLLG:   'rgba(156, 39, 176, 0.9)',
    QUAD:   'rgba(0, 150, 255, 0.9)',
    PENT:   'rgba(255, 0, 0, 0.9)',
    RING:   'rgba(255, 235, 59, 0.9)',
    HEALTH: 'rgba(0, 200, 83, 0.9)',
    RA:     'rgba(255, 50, 50, 0.9)',
    YA:     'rgba(255, 200, 0, 0.9)',
    GA:     'rgba(0, 180, 0, 0.6)',
};

const NICE_TICK_INTERVALS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600];

function pickTickInterval(duration, maxTicks) {
    const target = duration / maxTicks;
    for (const iv of NICE_TICK_INTERVALS) {
        if (iv >= target) return iv;
    }
    return NICE_TICK_INTERVALS[NICE_TICK_INTERVALS.length - 1];
}

// Plot background — slightly different shade than the panel so the
// graph area is visually delineated. Same colour for every detail
// graph so they read as a uniform set.
const PLOT_BG_COLOR = '#16213e';

// Size a canvas for device-pixel rendering and return the drawing
// context plus the dimensions in both CSS and device pixels.
//
// We render directly in device pixels (no ctx.scale) so every fillRect
// edge lands on an integer device pixel by construction. CSS-pixel
// rendering with ctx.scale(dpr, dpr) is fine when dpr is an integer,
// but at fractional dpr (Linux/KDE/GNOME fractional scaling, browser
// zoom != 100 %, some Windows configs) every CSS-pixel fillRect edge
// lands at a non-integer device-pixel offset; the rasteriser then
// splits that edge across two device pixels at ~50 % coverage each,
// producing visible darker boundary pixels (and at one fillRect per
// CSS pixel a regular moiré stripe pattern).
//
// Returns null if the canvas element is missing.
function setupGraphCanvas(canvasId, cssHeight) {
    const canvas = document.getElementById(canvasId);
    if (!canvas || !canvas.getContext) return null;
    const container = canvas.parentElement;
    const Wcss = container.clientWidth;
    const Hcss = cssHeight;
    const dpr = window.devicePixelRatio || 1;
    const W = Math.round(Wcss * dpr);
    const H = Math.round(Hcss * dpr);
    canvas.width = W;
    canvas.height = H;
    canvas.style.width = Wcss + 'px';
    canvas.style.height = Hcss + 'px';
    const ctx = canvas.getContext('2d');
    return { canvas, ctx, Wcss, Hcss, W, H, dpr };
}

// Draw the adaptive x-axis tick line and time labels at the bottom of
// a graph. Tick density keys off Wcss so it doesn't change with dpr.
// All coords are in device pixels (W, graphH, dpr).
function drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime }) {
    const duration = endTime - startTime;
    if (duration <= 0) return;
    const targetTicks = Math.max(4, Math.min(12, Math.floor(Wcss / 100)));
    const interval = pickTickInterval(duration, targetTicks);
    ctx.fillStyle = '#888';
    ctx.font = `${Math.round(10 * dpr)}px monospace`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    ctx.strokeStyle = 'rgba(255,255,255,0.08)';
    const firstTick = Math.ceil(startTime / interval) * interval;
    for (let t = firstTick; t <= endTime; t += interval) {
        const x = ((t - startTime) / duration) * W;
        ctx.beginPath();
        ctx.moveTo(x, graphH);
        ctx.lineTo(x, graphH + 4 * dpr);
        ctx.stroke();
        ctx.fillText(formatDuration(t), x, graphH + 5 * dpr);
    }
}

// Output-driven (scanline) sampler shared by the diverging + mini stacked
// renderers. Returns a closure that, given a query time, walks a monotonic
// cursor over the sorted `points` (step function / hold-last) and returns
// the active point (or null before the first point / past the last point's
// declared dt). Walking the *output* (one call per pixel column) makes
// "every column is drawn" structural and gives a draw cost that scales with
// canvas width, not bucket count — empty buckets no longer leave un-painted
// vertical stripes through the bars.
function makeScanlineSampler(points) {
    let cursor = -1;
    return (tQuery) => {
        while (cursor + 1 < points.length && points[cursor + 1].t <= tQuery) cursor++;
        if (cursor < 0) return null;
        const pt = points[cursor];
        // Last point: cap at its declared dt so we don't paint past the end
        // of the data when the view extends slightly beyond it.
        if (cursor === points.length - 1) {
            const endT = pt.t + (pt.dt || 0);
            if (endT > 0 && tQuery > endT) return null;
        }
        return pt;
    };
}

// Fill one 1px-wide stacked column of segments, snapping each segment
// boundary to a pixel row so stacked segments meet on exact rows instead of
// anti-aliased fractional ones. `from` is the baseline y (device px); `dir`
// is -1 to stack upward, +1 downward; `scale` is the full-height pixel span
// that maps to `maxValue`. Shared by renderDivergingGraph / renderMiniStack /
// renderMiniDiverging.
function fillStackedColumn(ctx, px, segs, from, dir, scale, maxValue) {
    if (!segs) return;
    let yAcc = from;
    let yPrev = Math.round(from);
    for (const seg of segs) {
        if (seg.h > 0) {
            yAcc += dir * ((seg.h / maxValue) * scale);
            const yCur = Math.round(yAcc);
            const segH = (yCur - yPrev) * dir;
            if (segH > 0) {
                ctx.fillStyle = seg.color;
                ctx.fillRect(px, Math.min(yPrev, yCur), 1, segH);
            }
            yPrev = yCur;
        }
    }
}

// Diverging-graph layout, in CSS px. renderDivergingGraph multiplies these
// by dpr for device-pixel drawing; the weapon-graph hit-tester
// (attachWeaponGraphTooltip) consumes them directly since its cursor coords
// are CSS px. One source of truth so the renderer and hit-tester can never
// drift apart.
const DIVERGING_GRAPH_LAYOUT = {
    H: 200,           // total canvas height
    AXIS_H: 20,       // bottom x-axis strip
    PAD: 4,           // inner padding
    DROP_STRIP_H: 8,  // reserved drop-dot strip at top/bottom of plot
};

// Render a diverging bar graph on a canvas.
//   dataPoints: [{t, dt, up: [{h, color}], down: [{h, color}]}]
//   dropMarks:  [{time, color, isTop}] (optional, e.g. RL/LG backpack drops
//               on the weapon graph). Renders as small dots in a reserved
//               strip zone so they never overlap the bars.
function renderDivergingGraph(canvasId, {
    startTime, endTime,
    dataPoints,
    maxValue,
    yAxisId,
    yTopLabel, yBottomLabel,
    dropMarks,
}) {
    const setup = setupGraphCanvas(canvasId, DIVERGING_GRAPH_LAYOUT.H);
    if (!setup) return;
    const { ctx, Wcss, W, H, dpr } = setup;

    // Constants below are in device pixels — multiply the shared CSS-px
    // layout by dpr so the displayed (CSS-px) size of axes / padding / dots
    // / fonts matches what the previous CSS-px-coordinate version drew.
    const AXIS_H = Math.round(DIVERGING_GRAPH_LAYOUT.AXIS_H * dpr);
    const PAD = Math.round(DIVERGING_GRAPH_LAYOUT.PAD * dpr);
    const graphH = H - AXIS_H;
    // Drop-mark strips live in a reserved zone at the top and bottom of
    // the plot area so weapon bars can never grow into them — the
    // weapons bar height scales with max players-per-team, so without
    // this reservation a high-rollout 5v5 snapshot could paint bars
    // straight through the dots. Sized for one row of ~6 px dots.
    const DROP_STRIP_H = Math.round(DIVERGING_GRAPH_LAYOUT.DROP_STRIP_H * dpr);
    const hasDropMarks = !!(dropMarks && dropMarks.length);
    const stripZone = hasDropMarks ? DROP_STRIP_H + Math.round(2 * dpr) : 0;
    const midY = PAD + (graphH - PAD) / 2;
    const barH = midY - PAD - stripZone;
    const duration = endTime - startTime;

    // Plot background — safe again now that the scanline/hold-last
    // renderer below paints every pixel column with the active data
    // point: empty buckets no longer leave bg-coloured vertical stripes
    // through the bars. The bg only shows above/below bars and in the
    // axis strip.
    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, graphH);

    // Grid lines at ±50% (drawn first so bars overlay them)
    ctx.strokeStyle = 'rgba(255,255,255,0.06)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, midY - barH * 0.5); ctx.lineTo(W, midY - barH * 0.5);
    ctx.moveTo(0, midY + barH * 0.5); ctx.lineTo(W, midY + barH * 0.5);
    ctx.stroke();

    if (duration > 0 && dataPoints && dataPoints.length > 0) {
        // Scanline hold-last rendering: sample each pixel column via a
        // monotonic cursor and paint a 1px column. Team A stacks up from
        // the center, team B stacks down. See makeScanlineSampler /
        // fillStackedColumn for the sampling + integer-snap details.
        const sampleAt = makeScanlineSampler(dataPoints);
        for (let px = 0; px < W; px++) {
            const tPx = startTime + (px / W) * duration;
            const pt = sampleAt(tPx);
            if (pt == null) continue;
            fillStackedColumn(ctx, px, pt.up, midY, -1, barH, maxValue);
            fillStackedColumn(ctx, px, pt.down, midY, 1, barH, maxValue);
        }
    }

    // Drop-mark dots — small filled circles in the reserved strip zone.
    // Top strip = team A drops, bottom strip = team B drops; color is
    // weapon-coded by the caller (e.g. RL red, LG cyan).
    if (hasDropMarks && duration > 0) {
        const dotR = 3 * dpr;
        const topY    = PAD + DROP_STRIP_H / 2;
        const bottomY = graphH - PAD - DROP_STRIP_H / 2;
        for (const m of dropMarks) {
            const x = ((m.time - startTime) / duration) * W;
            if (x < -dotR || x > W + dotR) continue;
            const y = m.isTop ? topY : bottomY;
            ctx.fillStyle = m.color;
            ctx.beginPath();
            ctx.arc(x, y, dotR, 0, Math.PI * 2);
            ctx.fill();
        }
    }

    // Zero-y divider — drawn on top of bars so the upper/lower split is
    // always clearly visible. Integer-aligned to avoid anti-aliasing.
    ctx.fillStyle = 'rgba(255, 255, 255, 0.85)';
    ctx.fillRect(0, Math.round(midY), W, Math.max(1, Math.round(dpr)));

    drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime });

    // Y-axis labels
    if (yAxisId) {
        const el = document.getElementById(yAxisId);
        if (el) {
            const top = el.querySelector('.y-top');
            const bot = el.querySelector('.y-bottom');
            if (top) top.textContent = yTopLabel !== undefined ? yTopLabel : maxValue;
            if (bot) bot.textContent = yBottomLabel !== undefined ? yBottomLabel : maxValue;
        }
    }
}

// ─── Data preparation: Weapons ──────────────────────────────────────────────

function prepWeaponsData(startTime, endTime, teams) {
    const view = timelineState.bucketView;
    if (!view || !view.count) return { points: [], max: 4 };
    const hrDur = timelineState.highResDuration || 0.05;
    const points = [];
    let maxVal = 4;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        const tdA = teamSnapshot(view, teams[0], i);
        const tdB = teamSnapshot(view, teams[1], i);
        const upT = (tdA.rl || 0) + (tdA.lg || 0) + (tdA.rllg || 0);
        const dnT = (tdB.rl || 0) + (tdB.lg || 0) + (tdB.rllg || 0);
        maxVal = Math.max(maxVal, upT, dnT);
        points.push({
            t: bt, dt: hrDur,
            up: [
                { h: tdA.rllg || 0, color: GRAPH_COLORS.RLLG },
                { h: tdA.lg || 0, color: GRAPH_COLORS.LG },
                { h: tdA.rl || 0, color: GRAPH_COLORS.RL },
            ],
            down: [
                { h: tdB.rllg || 0, color: GRAPH_COLORS.RLLG },
                { h: tdB.lg || 0, color: GRAPH_COLORS.LG },
                { h: tdB.rl || 0, color: GRAPH_COLORS.RL },
            ],
        });
    }
    return { points, max: maxVal };
}

// computeBackpackDrops returns dot markers for the weapon timeline:
// every RL or LG backpack dropped within the [startTime, endTime]
// window, with isTop=true for team-A drops and false for team-B.
// Reads from timelineState.backpacks (populated from result.backpacks
// in displayTimelineAnalysis). Each mark carries the source `drop`
// entry so hover tooltips can show player, loc, and time.
function computeBackpackDrops(startTime, endTime, teams) {
    const drops = timelineState.backpacks;
    if (!drops || drops.length === 0) return [];
    const teamA = teams[0], teamB = teams[1];
    const out = [];
    for (const d of drops) {
        if (d.time < startTime || d.time > endTime) continue;
        let isTop;
        if (d.team === teamA)      isTop = true;
        else if (d.team === teamB) isTop = false;
        else continue; // unknown team (spectator drop, mid-substitution, etc.)
        const color = d.weapon === 'rl' ? GRAPH_COLORS.RL
                    : d.weapon === 'lg' ? GRAPH_COLORS.LG
                    : null;
        if (!color) continue;
        out.push({ time: d.time, color, isTop, drop: d });
    }
    return out;
}

// Hit-test state for the weapon-graph drop dots — populated by
// updateDetailGraph after each render so the canvas mousemove handler
// can find the dot under the cursor without re-running the data prep.
const weaponGraphHitState = {
    startTime: 0,
    endTime:   0,
    W:         0,
    dropMarks: [],
};

// ─── Data preparation: Health/Armor ─────────────────────────────────────────

function prepHealthArmorData(startTime, endTime, teams) {
    const view = timelineState.bucketView;
    if (!view || !view.count) return { points: [], max: 400 };
    const hrDur = timelineState.highResDuration || 0.05;
    const points = [];
    let maxVal = 400;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        const tdA = teamSnapshot(view, teams[0], i);
        const tdB = teamSnapshot(view, teams[1], i);
        maxVal = Math.max(maxVal, (tdA.th || 0) + (tdA.ta || 0), (tdB.th || 0) + (tdB.ta || 0));
        points.push({
            t: bt, dt: hrDur,
            up: buildHASegments(tdA),
            down: buildHASegments(tdB),
        });
    }
    return { points, max: maxVal };
}

function buildHASegments(td) {
    const segs = [];
    if ((td.th || 0) > 0) segs.push({ h: td.th, color: GRAPH_COLORS.HEALTH });
    const armor = td.ta || 0;
    const abt = td.abt || {};
    const ra = abt.ra || 0, ya = abt.ya || 0, ga = abt.ga || 0;
    const total = ra + ya + ga;
    if (total > 0 && armor > 0) {
        if (ga > 0) segs.push({ h: (ga / total) * armor, color: GRAPH_COLORS.GA });
        if (ya > 0) segs.push({ h: (ya / total) * armor, color: GRAPH_COLORS.YA });
        if (ra > 0) segs.push({ h: (ra / total) * armor, color: GRAPH_COLORS.RA });
    } else if (armor > 0) {
        segs.push({ h: armor, color: GRAPH_COLORS.YA });
    }
    return segs;
}

// ─── Per-player drill-downs (health/armor + weapons) ─────────────────────────
//
// Both the Team Health/Armor and Weapons panels expand to a per-player view.
// All series come from the columnar bucket view already on the client
// (timelineState.bucketView.players[name], read via playerValAt/playerAliveAt)
// plus timelineState.backpacks for weapon-drop dots — no extra data fetch.

// Group the bucket-view players by side, following timelineState.teams order,
// so group index == TEAM_COLORS index. Team membership mirrors updateTeamStatus:
// prefer demoInfo.players, fall back to the map's playerSymbols. Players whose
// team can't be resolved (spectators, mid-game joiners) are dropped.
//
// In the individual layout every player IS a side, so there is one group per
// entry in timelineState.teams (the frag-sorted player list) and a player's
// side is their own name — no roster lookup needed. Before this, the grouping
// hard-coded two sides and resolved everyone to teams[0] / teams[1], so an
// FFA drill-down drew two rows of N. The two-sided panels keep exactly two
// groups, so a team demo is unchanged.
function timelinePlayersByTeam() {
    const teams = timelineState.teams;
    const view = timelineState.bucketView;
    const individual = individualFieldActive();
    const groupCount = individual ? Math.max(2, teams.length) : 2;
    const out = Array.from({ length: groupCount }, () => []);
    if (teams.length < 2 || !view || !view.players) return out;
    const demoPlayers = currentResult?.demoInfo?.players || [];
    const teamOf = (name) => {
        if (individual) return name;
        const dp = demoPlayers.find(p => p.name === name);
        if (dp?.team) return dp.team;
        return mapState.playerSymbols?.[name]?.team;
    };
    for (const name of Object.keys(view.players)) {
        const ti = teams.indexOf(teamOf(name));
        if (ti < 0 || ti >= out.length) continue;
        out[ti].push(name);
    }
    for (const group of out) group.sort();
    return out;
}

// Single-player health+armor stack for the mini chart. Health (green) at the
// bottom, then armor coloured by its type (RA/YA/GA).
function buildPlayerHASegments(h, a, at) {
    const segs = [];
    if (h > 0) segs.push({ h, color: GRAPH_COLORS.HEALTH });
    if (a > 0) {
        const color = at === 'ra' ? GRAPH_COLORS.RA
                    : at === 'ga' ? GRAPH_COLORS.GA
                    : GRAPH_COLORS.YA; // ya or unknown
        segs.push({ h: a, color });
    }
    return segs;
}

function prepPlayerHAData(cp, startTime, endTime) {
    const view = timelineState.bucketView;
    const hrDur = timelineState.highResDuration || 0.05;
    const points = [];
    if (!view || !view.count || !cp) return points;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        // playerValAt returns undefined when the player is dead/absent at this
        // bucket — emit an empty stack so the chart shows a gap there.
        const h = playerValAt(cp, 'h', i) || 0;
        const a = playerValAt(cp, 'a', i) || 0;
        const at = playerValAt(cp, 'at', i) || '';
        points.push({ t: bt, dt: hrDur, up: buildPlayerHASegments(h, a, at) });
    }
    return points;
}

// Compact single-direction stacked renderer for the per-player mini charts.
// Reuses setupGraphCanvas + the scanline hold-last sampling from
// renderDivergingGraph, but stacks upward from the baseline only and skips the
// diverging mirror, zero divider, and x-axis (the team chart above carries it).
function renderMiniStack(canvasId, { startTime, endTime, points, maxValue, height }) {
    const setup = setupGraphCanvas(canvasId, height || 44);
    if (!setup) return;
    const { ctx, W, H, dpr } = setup;
    const PAD = Math.round(2 * dpr);
    const baseY = H - PAD;
    const usableH = H - 2 * PAD;

    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, H);

    const duration = endTime - startTime;
    if (duration <= 0 || !points || points.length === 0 || maxValue <= 0) return;

    const sampleAt = makeScanlineSampler(points);
    for (let px = 0; px < W; px++) {
        const tPx = startTime + (px / W) * duration;
        const pt = sampleAt(tPx);
        if (!pt) continue;
        fillStackedColumn(ctx, px, pt.up, baseY, -1, usableH, maxValue);
    }
}

// Render one mini health/armor chart per player under the Team Health/Armor
// panel. The per-player canvases are built lazily and rebuilt only when the
// roster changes (signature check); on every view change we just re-render.
function renderHealthArmorPerPlayer(startTime, endTime) {
    const container = document.getElementById('ha-per-player');
    if (!container) return;
    const teams = timelineState.teams;
    const view = timelineState.bucketView;
    if (!view || !view.count || teams.length < 2) {
        // Clearing the DOM must invalidate the rebuild cache too: otherwise a
        // later render with the same roster signature skips the rebuild and
        // iterates now-detached _cells, rendering nothing. This bit when a new
        // demo's buckets arrive deferred (the first render runs view-less).
        container.innerHTML = '';
        container._sig = null;
        container._cells = [];
        return;
    }

    const grouped = timelinePlayersByTeam();
    const sig = grouped.map(g => g.join('|')).join('#');
    if (container._sig !== sig) {
        container.innerHTML = '';
        container._cells = [];
        for (let ti = 0; ti < grouped.length; ti++) {
            grouped[ti].forEach((name, idx) => {
                const cid = `ha-pp-${ti}-${idx}`;
                const cell = document.createElement('div');
                cell.className = 'per-player-cell';
                const label = document.createElement('div');
                label.className = 'per-player-label';
                label.textContent = name;
                label.title = name;
                label.style.color = TEAM_COLORS[ti] || '#ccc';
                const wrap = document.createElement('div');
                wrap.className = 'per-player-canvas-wrap';
                const canvas = document.createElement('canvas');
                canvas.id = cid;
                canvas.className = 'timeline-canvas';
                const indicator = document.createElement('div');
                indicator.className = 'current-time-indicator pp-time-indicator';
                wrap.appendChild(canvas);
                wrap.appendChild(indicator);
                cell.appendChild(label);
                cell.appendChild(wrap);
                container.appendChild(cell);
                installGraphPanZoom(cid);
                installIndicatorScrub(indicator, wrap);
                container._cells.push({ name, cid });
            });
        }
        container._sig = sig;
    }

    // Shared max across all players (rounded up) so the minis are comparable.
    let maxVal = 300;
    const prepped = {};
    for (const { name } of container._cells) {
        const pts = prepPlayerHAData(view.players[name], startTime, endTime);
        prepped[name] = pts;
        for (const p of pts) {
            let s = 0;
            for (const seg of p.up) s += seg.h;
            if (s > maxVal) maxVal = s;
        }
    }
    for (const { name, cid } of container._cells) {
        const cv = document.getElementById(cid);
        if (cv) cv.style.width = ''; // let the cell reflow on resize
        renderMiniStack(cid, { startTime, endTime, points: prepped[name] || [], maxValue: maxVal, height: ppMiniHeight() });
    }
    renderSharedTimeAxis(container, startTime, endTime);
}

// ─── Per-player frags / deaths drill-down ───────────────────────────────────
//
// One compact +/- chart per player under the Score Timeline, mirroring the team
// Score Timeline above: the player's running plus/minus = cumulative enemy
// kills minus cumulative deaths. When ahead (more kills than deaths) the area
// rises above the divider in the team colour; when behind it drops below in a
// dimmed team colour. Kills come from killEvents (the canonical frag log,
// suicides/teamkills excluded) and deaths from deathEvents (every death counts
// once), so the full-match endpoint equals byPlayer.kills − byPlayer.deaths and
// reconciles with the kills-based efficiency = kills/(kills+deaths)
// (ktx/src/statsTables.c calculateEfficiency) shown on each row.

// Darken a team hex toward black so the behind-half reads as a muted mirror of
// the team-coloured ahead-half (position above/below the divider already
// separates them; the shade keeps team identity).
function dimColor(hex) {
    const [r, g, b] = hexToRgb(hex);
    return `rgb(${Math.round(r * 0.55)},${Math.round(g * 0.55)},${Math.round(b * 0.55)})`;
}

// Build sampled +/- points for one player plus the window-end kill/death
// totals. Mirrors prepScoreData: carry the running totals from match start so
// the window's left edge shows the cumulative plus/minus, then plot the single
// net value (kills − deaths) as up when positive / down when negative.
function prepPlayerFragDeathData(name, startTime, endTime, upColor, downColor) {
    const ke = timelineState.killEvents || [];
    const de = timelineState.deathEvents || [];

    let kills = 0, deaths = 0;
    for (const k of ke) {
        if (k.time >= startTime) break;
        if (k.player === name) kills += 1;
    }
    for (const d of de) {
        if (d.time >= startTime) break;
        if (d.player === name) deaths += 1;
    }

    // Merge this player's in-window kill and death events by time, then
    // accumulate into a net (kills − deaths) step series.
    const evs = [];
    for (const k of ke) {
        if (k.time < startTime) continue;
        if (k.time > endTime) break;
        if (k.player === name) evs.push({ time: k.time, dn: 1 });
    }
    for (const d of de) {
        if (d.time < startTime) continue;
        if (d.time > endTime) break;
        if (d.player === name) evs.push({ time: d.time, dn: -1 });
    }
    evs.sort((a, b) => a.time - b.time);

    const stepAt = [{ time: startTime, net: kills - deaths }];
    let ck = kills, cd = deaths;
    for (const e of evs) {
        if (e.dn > 0) ck += 1; else cd += 1;
        stepAt.push({ time: e.time, net: ck - cd });
    }
    stepAt.push({ time: endTime, net: ck - cd });

    const duration = endTime - startTime;
    const sampleRate = Math.max(0.5, duration / 400);
    let maxVal = 1;
    const points = [];
    let si = 0;
    for (let t = startTime; t < endTime; t += sampleRate) {
        while (si + 1 < stepAt.length && stepAt[si + 1].time <= t) si++;
        const v = stepAt[si].net;
        maxVal = Math.max(maxVal, Math.abs(v));
        points.push({
            t, dt: sampleRate,
            up: v > 0 ? [{ h: v, color: upColor }] : [],
            down: v < 0 ? [{ h: -v, color: downColor }] : [],
        });
    }
    return { points, max: maxVal, kills: ck, deaths: cd };
}

// Compact diverging renderer for the per-player frags/deaths minis: net frags
// stack up from the center, deaths stack down, sharing maxValue. Mirrors
// renderMiniStack's scanline sampling, mirrored across a faint center divider.
function renderMiniDiverging(canvasId, { startTime, endTime, points, maxValue, height }) {
    const setup = setupGraphCanvas(canvasId, height || 44);
    if (!setup) return;
    const { ctx, W, H, dpr } = setup;
    const PAD = Math.round(2 * dpr);
    const midY = Math.round(H / 2);
    const halfH = (H - 2 * PAD) / 2;

    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, H);

    const duration = endTime - startTime;
    if (duration > 0 && points && points.length && maxValue > 0) {
        const sampleAt = makeScanlineSampler(points);
        for (let px = 0; px < W; px++) {
            const tPx = startTime + (px / W) * duration;
            const pt = sampleAt(tPx);
            if (!pt) continue;
            // Up (net frags, above center); down (deaths, below center).
            fillStackedColumn(ctx, px, pt.up, midY, -1, halfH, maxValue);
            fillStackedColumn(ctx, px, pt.down, midY, 1, halfH, maxValue);
        }
    }

    // Center divider.
    ctx.fillStyle = 'rgba(255, 255, 255, 0.5)';
    ctx.fillRect(0, midY, W, Math.max(1, Math.round(dpr)));
}

// Render one mini +/- chart per player under the Score Timeline panel. Built
// lazily, rebuilt only when the roster changes (signature check). Each row is
// scaled by its OWN |net| max (0 stays centred) so a player whose plus/minus
// swings little still fills the row instead of collapsing to a few pixels next
// to a high-swing team-mate. The labels track the playhead via
// updateFragsPerPlayerStats.
function renderFragsPerPlayer(startTime, endTime) {
    const container = document.getElementById('frags-per-player');
    if (!container) return;
    const teams = timelineState.teams;
    const view = timelineState.bucketView;
    if (!view || !view.count || teams.length < 2) {
        // Clearing the DOM must invalidate the rebuild cache too: otherwise a
        // later render with the same roster signature skips the rebuild and
        // iterates now-detached _cells, rendering nothing. This bit when a new
        // demo's buckets arrive deferred (the first render runs view-less).
        container.innerHTML = '';
        container._sig = null;
        container._cells = [];
        return;
    }

    const grouped = timelinePlayersByTeam();
    const sig = grouped.map(g => g.join('|')).join('#');
    if (container._sig !== sig) {
        container.innerHTML = '';
        container._cells = [];
        for (let ti = 0; ti < grouped.length; ti++) {
            grouped[ti].forEach((name, idx) => {
                const cid = `fd-pp-${ti}-${idx}`;
                const cell = document.createElement('div');
                cell.className = 'per-player-cell';
                const label = document.createElement('div');
                label.className = 'per-player-label fd-label';
                label.style.color = TEAM_COLORS[ti] || '#ccc';
                const nameEl = document.createElement('div');
                nameEl.className = 'fd-name';
                nameEl.textContent = name;
                nameEl.title = name;
                const statEl = document.createElement('div');
                statEl.className = 'fd-stat';
                label.appendChild(nameEl);
                label.appendChild(statEl);
                const wrap = document.createElement('div');
                wrap.className = 'per-player-canvas-wrap';
                const canvas = document.createElement('canvas');
                canvas.id = cid;
                canvas.className = 'timeline-canvas';
                const indicator = document.createElement('div');
                indicator.className = 'current-time-indicator pp-time-indicator';
                wrap.appendChild(canvas);
                wrap.appendChild(indicator);
                cell.appendChild(label);
                cell.appendChild(wrap);
                container.appendChild(cell);
                installGraphPanZoom(cid);
                installIndicatorScrub(indicator, wrap);
                container._cells.push({ name, cid, ti, statEl });
            });
        }
        container._sig = sig;
    }

    for (const { name, ti, cid } of container._cells) {
        const upColor = TEAM_COLORS[ti] || '#ccc';
        const d = prepPlayerFragDeathData(name, startTime, endTime, upColor, dimColor(upColor));
        const cv = document.getElementById(cid);
        if (cv) cv.style.width = '';
        // Per-player max (symmetric about 0): each row uses its full height.
        renderMiniDiverging(cid, { startTime, endTime, points: d.points, maxValue: d.max, height: ppMiniHeight() });
    }
    updateFragsPerPlayerStats();
    renderSharedTimeAxis(container, startTime, endTime);
}

// Update the per-player frags/deaths labels to the cumulative kills / deaths
// and efficiency AT the current playback time (not whole-match totals), so the
// numbers track the playhead as it moves / scrubs. Counts the kill and death
// event streams in one pass each; no-op when the drill-down isn't built.
function updateFragsPerPlayerStats() {
    const container = document.getElementById('frags-per-player');
    if (!container || !container._cells || !container._cells.length) return;
    const t = mapState.currentTime;
    const kc = {}, dc = {};
    for (const e of (timelineState.killEvents || [])) if (e.time <= t) kc[e.player] = (kc[e.player] || 0) + 1;
    for (const e of (timelineState.deathEvents || [])) if (e.time <= t) dc[e.player] = (dc[e.player] || 0) + 1;
    for (const { name, statEl } of container._cells) {
        if (!statEl) continue;
        const k = kc[name] || 0, d = dc[name] || 0;
        const eff = (k + d) > 0 ? Math.round((k / (k + d)) * 100) : 0;
        statEl.textContent = `${k}/${d} · ${eff}%`;
        statEl.title = `${k} kills / ${d} deaths · ${eff}% efficiency at ${formatDuration(t)}`;
    }
}

// Per-player weapons timeline: one combined row per player coloured by whether
// they hold RL / LG / both, with weapon-drop events as dots. Reuses
// renderSpansTimeline (one row per player) with team-coloured labels.
function prepPlayerWeaponSpans(cp, startTime, endTime) {
    const view = timelineState.bucketView;
    const spans = [];
    if (!view || !view.count || !cp) return spans;
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    let curState = null, curStart = startTime;
    for (let i = idx0; i < view.count; i++) {
        const bt = bucketTimeSec(view, i);
        if (bt > endTime) break;
        let state = null;
        if (playerAliveAt(cp, i)) {
            const rl = !!playerValAt(cp, 'rl', i);
            const lg = !!playerValAt(cp, 'lg', i);
            if (rl && lg) state = 'rllg';
            else if (rl) state = 'rl';
            else if (lg) state = 'lg';
        }
        if (state !== curState) {
            if (curState) spans.push({ start: curStart, end: bt, state: curState });
            curState = state;
            curStart = bt;
        }
    }
    if (curState) spans.push({ start: curStart, end: endTime, state: curState });
    return spans;
}

function renderWeaponsPerPlayer(startTime, endTime) {
    const labelsEl = document.getElementById('weapons-per-player-labels');
    if (!labelsEl) return;
    const teams = timelineState.teams;
    const view = timelineState.bucketView;
    if (!view || !view.count || teams.length < 2) { labelsEl.innerHTML = ''; return; }

    const grouped = timelinePlayersByTeam();
    const rows = [];
    const rowPlayers = [];
    for (let ti = 0; ti < grouped.length; ti++) {
        for (const name of grouped[ti]) {
            rows.push({
                name,
                color: TEAM_COLORS[ti] || '#ccc',
                spans: prepPlayerWeaponSpans(view.players[name], startTime, endTime),
            });
            rowPlayers.push(name);
        }
    }
    if (rows.length === 0) { labelsEl.innerHTML = ''; return; }

    const dropMarks = [];
    for (const d of (timelineState.backpacks || [])) {
        if (d.time < startTime || d.time > endTime) continue;
        const row = rowPlayers.indexOf(d.player);
        if (row < 0) continue;
        const color = d.weapon === 'rl' ? GRAPH_COLORS.RL
                    : d.weapon === 'lg' ? GRAPH_COLORS.LG
                    : null;
        if (!color) continue;
        dropMarks.push({ row, time: d.time, color });
    }

    renderSpansTimeline('weapons-per-player-canvas', 'weapons-per-player-labels', {
        startTime, endTime, rows, rowH: ppSpanRowHeight(),
        stateColors: { rl: GRAPH_COLORS.RL, lg: GRAPH_COLORS.LG, rllg: GRAPH_COLORS.RLLG },
        dropMarks,
    });
}

// ─── Data preparation: Score ────────────────────────────────────────────────

function prepScoreData(startTime, endTime, teams) {
    const fragEvents = timelineState.fragEvents || []; // pre-sorted at intake
    if (teams.length < 2) return { points: [], max: 10 };
    let score = 0;
    for (const f of fragEvents) {
        if (f.time >= startTime) break;
        if (f.team === teams[0]) score += (f.delta || 1);
        else if (f.team === teams[1]) score -= (f.delta || 1);
    }
    // Build cumulative score change points
    const scoreAt = [{ time: startTime, score }];
    let s = score;
    for (const f of fragEvents) {
        if (f.time < startTime) continue;
        if (f.time > endTime) break;
        if (f.team === teams[0]) s += (f.delta || 1);
        else if (f.team === teams[1]) s -= (f.delta || 1);
        scoreAt.push({ time: f.time, score: s });
    }
    scoreAt.push({ time: endTime, score: s });

    const duration = endTime - startTime;
    const sampleRate = Math.max(0.5, duration / 400);
    const [rA, gA, bA2] = hexToRgb(TEAM_COLORS[0]);
    const [rB, gB, bB2] = hexToRgb(TEAM_COLORS[1]);
    const cA = `rgba(${rA},${gA},${bA2},0.8)`;
    const cB = `rgba(${rB},${gB},${bB2},0.8)`;
    let maxVal = 10;
    const points = [];
    let si = 0;
    for (let t = startTime; t < endTime; t += sampleRate) {
        while (si + 1 < scoreAt.length && scoreAt[si + 1].time <= t) si++;
        const v = scoreAt[si].score;
        maxVal = Math.max(maxVal, Math.abs(v));
        points.push({
            t, dt: sampleRate,
            up: v > 0 ? [{ h: v, color: cA }] : [],
            down: v < 0 ? [{ h: -v, color: cB }] : [],
        });
    }
    return { points, max: maxVal };
}

// ─── Graph pan/zoom (shared view range) ─────────────────────────────────────
//
// All four diverging graphs and the region-control timeline share
// timelineState.segment as their view range. Ctrl+wheel zooms around the
// cursor; left-click drag pans horizontally. The unified timeline bar's
// range-select still feeds the same state, so both entry points stay in sync.

const MIN_VIEW_SPAN = 2; // seconds — don't zoom past this

function currentViewRange() {
    const duration = timelineState.duration || 0;
    const seg = timelineState.segment;
    return seg ? [seg.start, seg.end] : [0, duration];
}

function setViewRange(start, end) {
    const duration = timelineState.duration || 0;
    if (duration <= 0) return;
    if (end - start < MIN_VIEW_SPAN) {
        const mid = (start + end) / 2;
        start = mid - MIN_VIEW_SPAN / 2;
        end = mid + MIN_VIEW_SPAN / 2;
    }
    // Slide the window back inside [0, duration] without shrinking it.
    if (start < 0) { end -= start; start = 0; }
    if (end > duration) { start -= (end - duration); end = duration; }
    start = Math.max(0, start);
    end = Math.min(duration, end);
    if (start <= 0 && end >= duration) {
        timelineState.segment = null;
    } else {
        timelineState.segment = { start, end };
    }
    updateSelectionOverlay();
    updateSegmentLabel();
    updateDetailView();
    updateUrlState();
}

function graphMouseToTime(canvas, clientX) {
    const rect = canvas.getBoundingClientRect();
    if (rect.width <= 0) return null;
    const [start, end] = currentViewRange();
    return start + ((clientX - rect.left) / rect.width) * (end - start);
}

// One global drag tracker shared by all installed canvases — avoids attaching
// a mousemove listener per canvas. Pan updates are coalesced to one per
// animation frame: a raw mousemove stream would otherwise run
// setViewRange → updateDetailView (five graph preps/renders, each resizing its
// canvas) dozens of times per second.
const graphPanState = { canvas: null, lastX: 0, pendingX: 0, rafId: 0 };
let graphPanGlobalsInstalled = false;

// Apply the accumulated pan since the last processed frame. Runs from the rAF
// scheduled by mousemove, and synchronously from mouseup to flush the final
// cursor position exactly.
function processGraphPan() {
    graphPanState.rafId = 0;
    const c = graphPanState.canvas;
    if (!c) return;
    const rect = c.getBoundingClientRect();
    if (rect.width <= 0) return;
    const [start, end] = currentViewRange();
    const secPerPx = (end - start) / rect.width;
    const dx = graphPanState.pendingX - graphPanState.lastX;
    if (dx === 0) return;
    graphPanState.lastX = graphPanState.pendingX;
    setViewRange(start - dx * secPerPx, end - dx * secPerPx);
    // Pin the playhead to its on-screen position while panning: shift the
    // current time by the amount the window actually moved (after the
    // [0,duration] clamp), so the indicator line stays put and the unified
    // caret + clock + map follow. Not zoomed ⇒ the window can't move ⇒
    // applied === 0 ⇒ the playhead holds.
    const [newStart] = currentViewRange();
    const applied = newStart - start;
    if (applied !== 0) setCurrentTime(mapState.currentTime + applied);
}

function ensureGraphPanGlobals() {
    if (graphPanGlobalsInstalled) return;
    graphPanGlobalsInstalled = true;
    document.addEventListener('mousemove', (e) => {
        if (!graphPanState.canvas) return;
        graphPanState.pendingX = e.clientX;
        if (!graphPanState.rafId) graphPanState.rafId = requestAnimationFrame(processGraphPan);
    });
    document.addEventListener('mouseup', () => {
        if (!graphPanState.canvas) return;
        // Flush any pending pan so the drag ends exactly where the cursor is.
        if (graphPanState.rafId) {
            cancelAnimationFrame(graphPanState.rafId);
            graphPanState.rafId = 0;
            processGraphPan();
        }
        graphPanState.canvas.style.cursor = 'grab';
        graphPanState.canvas = null;
    });
}

function installGraphPanZoom(canvasId) {
    const canvas = document.getElementById(canvasId);
    if (!canvas || canvas._panZoomInstalled) return;
    canvas._panZoomInstalled = true;
    ensureGraphPanGlobals();

    canvas.addEventListener('wheel', (e) => {
        if (!e.ctrlKey && !e.metaKey) return;  // plain wheel = page scroll
        e.preventDefault();                    // stop browser pinch-zoom
        const centerT = graphMouseToTime(canvas, e.clientX);
        if (centerT === null) return;
        // Exponential factor — deltaY > 0 scrolls toward the user → zoom out.
        const factor = Math.exp(e.deltaY * 0.0015);
        const [start, end] = currentViewRange();
        setViewRange(
            centerT - (centerT - start) * factor,
            centerT + (end - centerT) * factor,
        );
    }, { passive: false });

    canvas.addEventListener('mousedown', (e) => {
        if (e.button !== 0) return;
        graphPanState.canvas = canvas;
        graphPanState.lastX = e.clientX;
        graphPanState.pendingX = e.clientX;
        canvas.style.cursor = 'grabbing';
        e.preventDefault();
    });

    canvas.addEventListener('dblclick', () => {
        // Quick reset to full match
        setViewRange(0, timelineState.duration || 0);
    });

    canvas.style.cursor = 'grab';
}

// ─── Current-time scrubbing on the graphs ───────────────────────────────────
//
// Grabbing the current-time line on any timeline graph (team or per-player,
// normal or zoomed) sets the playback time. The indicator div sits above the
// canvas; its parent (the graph-outer / canvas-wrap / spans-outer) holds the
// canvas at full width and so shares the canvas's time→x mapping, which we
// invert through currentViewRange. One global move/up pair, mirroring
// graphPanState.
const graphScrubState = { el: null };
let graphScrubGlobalsInstalled = false;

function ensureGraphScrubGlobals() {
    if (graphScrubGlobalsInstalled) return;
    graphScrubGlobalsInstalled = true;
    document.addEventListener('mousemove', (e) => {
        const el = graphScrubState.el;
        if (!el) return;
        const rect = el.getBoundingClientRect();
        if (rect.width <= 0) return;
        const [start, end] = currentViewRange();
        const frac = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
        setCurrentTime(start + frac * (end - start));
    });
    document.addEventListener('mouseup', () => {
        if (!graphScrubState.el) return;
        graphScrubState.el = null;
        document.body.classList.remove('graph-scrubbing');
    });
}

// Make a current-time indicator line draggable to scrub. measureEl (the
// element whose width maps to the view range) defaults to the indicator's
// parent, which holds the canvas at full width.
function installIndicatorScrub(indicatorEl, measureEl) {
    if (!indicatorEl || indicatorEl._scrubInstalled) return;
    indicatorEl._scrubInstalled = true;
    ensureGraphScrubGlobals();
    indicatorEl.addEventListener('mousedown', (e) => {
        if (e.button !== 0) return;
        graphScrubState.el = measureEl || indicatorEl.parentElement;
        document.body.classList.add('graph-scrubbing');
        e.preventDefault();
        e.stopPropagation();
    });
}

// Render one shared time axis at the bottom of a per-player grid, aligned with
// the graph column (an empty label-width spacer + a tick track spanning the
// canvas column). Rebuilt on every view change since labels depend on the
// window. The team graphs draw their axis on-canvas and the per-player weapons
// spans canvas draws its own; the compact frags/health minis share this single
// axis instead of repeating one per row.
function renderSharedTimeAxis(gridEl, startTime, endTime) {
    if (!gridEl) return;
    let axis = gridEl.querySelector(':scope > .per-player-axis-cell');
    let track;
    if (!axis) {
        axis = document.createElement('div');
        axis.className = 'per-player-cell per-player-axis-cell';
        const spacer = document.createElement('div');
        spacer.className = 'per-player-label';
        track = document.createElement('div');
        track.className = 'per-player-axis-track';
        axis.appendChild(spacer);
        axis.appendChild(track);
    } else {
        track = axis.querySelector('.per-player-axis-track');
    }
    gridEl.appendChild(axis); // keep the axis as the last row
    track.innerHTML = '';
    const duration = endTime - startTime;
    if (duration <= 0) return;
    const wCss = track.clientWidth || 300;
    const targetTicks = Math.max(4, Math.min(10, Math.floor(wCss / 100)));
    const interval = pickTickInterval(duration, targetTicks);
    const firstTick = Math.ceil(startTime / interval) * interval;
    for (let t = firstTick; t <= endTime + 1e-6; t += interval) {
        const span = document.createElement('span');
        span.textContent = formatDuration(t);
        span.style.left = `${((t - startTime) / duration) * 100}%`;
        track.appendChild(span);
    }
}

// ─── Unified Timeline Widget ──────────────────────────────────────────────

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
            timelineState.segment = { start, end };
            updateSelectionOverlay();
            updateSegmentLabel();
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

        const time = tlBarClickToTime(e);
        if (time === null) return;

        const start = Math.min(timelineState.dragStartTime, time);
        const end = Math.max(timelineState.dragStartTime, time);

        if (end - start <= 2) {
            timelineState.segment = null;
            updateSelectionOverlay();
            updateSegmentLabel();
            setCurrentTime(time);
        } else {
            timelineState.segment = { start, end };
            updateSelectionOverlay();
            updateSegmentLabel();
            updateDetailView();
        }
        updateUrlState();
    });

    bar.addEventListener('mousedown', (e) => {
        const time = tlBarClickToTime(e);
        if (time === null) return;

        timelineState.dragging = true;
        timelineState.dragStartTime = time;
        timelineState.segment = null;
        updateSelectionOverlay();
        updateSegmentLabel();

        e.preventDefault();
    });

    bar.addEventListener('dblclick', () => {
        timelineState.segment = null;
        updateSelectionOverlay();
        updateSegmentLabel();
        updateDetailView();
        updateUrlState();
    });

    // --- Playback controls ---
    document.getElementById('tl-rev').addEventListener('click', () => startPlaybackAtSpeed(-1));
    document.getElementById('tl-slow').addEventListener('click', () => startPlaybackAtSpeed(0.2));
    document.getElementById('tl-play-pause').addEventListener('click', () => startPlaybackAtSpeed(1));
    document.getElementById('tl-5x').addEventListener('click', () => startPlaybackAtSpeed(5));

    // --- Pan/zoom on every diverging graph + spans timelines (DOM order) ---
    ['score-canvas', 'powerup-canvas', 'detail-graph-canvas',
     'health-armor-canvas', 'region-control-canvas',
     // Per-player weapons spans canvas — same Ctrl/Cmd+scroll zoom + drag-pan.
     'weapons-per-player-canvas'].forEach(installGraphPanZoom);

    // --- Drag the current-time line on any graph to scrub the clock ---
    ['score-time-indicator', 'powerup-time-indicator', 'detail-time-indicator',
     'health-time-indicator', 'region-time-indicator',
     'weapons-pp-time-indicator'].forEach(id =>
        installIndicatorScrub(document.getElementById(id)));

    // --- Hover tooltip on weapon-graph drop dots ---
    attachWeaponGraphTooltip();

    // --- Hover tooltip on powerup-timeline spans ---
    attachPowerupTimelineTooltip();

    // --- Per-player drill-down toggles: render on first open (and on every
    // re-open) at the current view range. Subsequent view changes re-render
    // via updateDetailGraph / updateHealthArmorGraph while the panel is open.
    // updateTimeIndicators after the first render seats the freshly-created
    // current-time lines at the playhead (they'd otherwise sit at 0% until the
    // next view/time change).
    const haDet = document.getElementById('ha-per-player-details');
    if (haDet && !haDet._toggleWired) {
        haDet._toggleWired = true;
        haDet.addEventListener('toggle', () => {
            if (haDet.open) { renderHealthArmorPerPlayer(...currentViewRange()); updateTimeIndicators(); }
        });
    }
    const wpDet = document.getElementById('weapons-per-player-details');
    if (wpDet && !wpDet._toggleWired) {
        wpDet._toggleWired = true;
        wpDet.addEventListener('toggle', () => {
            if (wpDet.open) { renderWeaponsPerPlayer(...currentViewRange()); updateTimeIndicators(); }
        });
    }
    const fpDet = document.getElementById('frags-per-player-details');
    if (fpDet && !fpDet._toggleWired) {
        fpDet._toggleWired = true;
        fpDet.addEventListener('toggle', () => {
            if (fpDet.open) { renderFragsPerPlayer(...currentViewRange()); updateTimeIndicators(); }
        });
    }

    // --- Window resize: detail-panel canvases are sized in pixels from
    // container.clientWidth, so they don't reflow with the viewport on
    // their own — re-render them when the window resizes. Only the
    // timeline tab has the detail panels; on other tabs the active-tab
    // re-render path handles it on switch. Debounced to a single
    // animation frame per resize burst.
    window.addEventListener('resize', onTimelineWindowResize);

    unifiedTimelineInitialized = true;
}

const TIMELINE_CANVAS_IDS = [
    'score-canvas', 'powerup-canvas', 'detail-graph-canvas',
    'health-armor-canvas', 'region-control-canvas', 'weapons-per-player-canvas',
];

let _timelineResizeRafId = null;
function onTimelineWindowResize() {
    if (!currentResult) return;
    const activeTab = activeTabName();
    if (activeTab !== 'timeline') return;
    if (_timelineResizeRafId !== null) return;
    _timelineResizeRafId = requestAnimationFrame(() => {
        _timelineResizeRafId = null;
        // The renderers set canvas.style.width in pixels, which (combined
        // with the default flex `min-width: auto`) wedges the parent open
        // and prevents shrink-to-fit on window down-size. Clear the inline
        // width before re-measuring so each container reports its true
        // available width via clientWidth.
        for (const id of TIMELINE_CANVAS_IDS) {
            const c = document.getElementById(id);
            if (c) c.style.width = '';
        }
        updateDetailView();
        updateTimeIndicators();
    });
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

function updateSelectionOverlay() {
    const overlay = document.getElementById('tl-selection-overlay');
    if (!overlay) return;

    if (!timelineState.segment) {
        overlay.style.display = 'none';
        return;
    }

    const duration = timelineState.duration;
    if (duration <= 0) return;

    const startPct = (timelineState.segment.start / duration) * 100;
    const endPct = (timelineState.segment.end / duration) * 100;

    overlay.style.display = 'block';
    overlay.style.left = `${startPct}%`;
    overlay.style.width = `${endPct - startPct}%`;
}

function updateSegmentLabel() {
    const label = document.getElementById('tl-segment-label');
    if (!label) return;

    if (!timelineState.segment) {
        label.textContent = '';
        return;
    }

    label.textContent = `${formatDuration(timelineState.segment.start)} – ${formatDuration(timelineState.segment.end)}`;
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

function updateTimeIndicators() {
    updateUnifiedCursor();

    // Detail graphs show either the segment or the full match
    const seg = timelineState.segment;
    const rangeStart = seg ? seg.start : 0;
    const rangeEnd = seg ? seg.end : timelineState.duration;
    const range = rangeEnd - rangeStart;

    const detailIndicators = [
        'score-time-indicator',
        'powerup-time-indicator',
        'detail-time-indicator',
        'health-time-indicator',
        'region-time-indicator'
    ];

    if (range <= 0) return;

    const pct = Math.max(0, Math.min(100, ((mapState.currentTime - rangeStart) / range) * 100));

    for (const id of detailIndicators) {
        const el = document.getElementById(id);
        if (el) {
            // Indicator shares the canvas's time-to-pixel mapping:
            // x = (t - start) / (end - start) * W, spanning the full
            // container width. No inset — the legacy 10 px padding
            // came from a removed axis-strip sibling and causes the
            // cursor to drift away from the bars near the edges.
            el.style.left = `${pct}%`;
        }
    }

    // Per-player drill-down lines share the same view window, so the same
    // pct applies. These exist only while a drill-down is open.
    document.querySelectorAll('.pp-time-indicator').forEach(el => {
        el.style.left = `${pct}%`;
    });

    // Per-player frags/deaths labels read the cumulative score at the playhead.
    updateFragsPerPlayerStats();

    // Update team status table
    updateTeamStatus();
}

function updateDetailView() {
    const duration = timelineState.duration;

    // Use segment if selected, otherwise full match
    const start = timelineState.segment ? timelineState.segment.start : 0;
    const end = timelineState.segment ? timelineState.segment.end : duration;

    // Show range in label
    if (timelineState.segment) {
        document.getElementById('time-range-label').textContent =
            `(${formatDuration(start)} - ${formatDuration(end)})`;
    } else {
        document.getElementById('time-range-label').textContent = '';
    }

    // Update all detail panels (axes are drawn on canvas by the unified
    // renderer). Order mirrors the DOM: score, powerups, weapons,
    // health/armor, then region control.
    updateScoreTimeline(start, end);
    updatePowerupTimeline(start, end);
    updateDetailGraph(start, end);
    updateHealthArmorGraph(start, end);
    updateRegionControlTimeline(start, end);

    // Re-glue the current-time lines to the new window. Without this the
    // indicators keep their last left% after a zoom/pan/select and end up
    // over the wrong time until the next setCurrentTime.
    updateTimeIndicators();
}

function updateDetailGraph(startTime, endTime) {
    const teams = timelineState.teams;
    if (teams.length < 2) return;
    // Individual layout: the A↑/B↓ graph is hidden and the per-player view is
    // a panel of its own, so render that and skip the series prep for a
    // display:none canvas — this runs on every pan/zoom frame.
    if (individualFieldActive()) { renderWeaponsPerPlayer(startTime, endTime); return; }
    const { points, max } = prepWeaponsData(startTime, endTime, teams);
    const dropMarks = computeBackpackDrops(startTime, endTime, teams);
    const legendA = document.getElementById('legend-weapons-team-a');
    const legendB = document.getElementById('legend-weapons-team-b');
    if (legendA) legendA.textContent = `${teams[0]} ↑`;
    if (legendB) legendB.textContent = `${teams[1]} ↓`;
    renderDivergingGraph('detail-graph-canvas', {
        startTime, endTime, dataPoints: points, maxValue: max,
        yAxisId: 'detail-y-axis', dropMarks,
    });
    // Refresh the hit-test cache so the tooltip can find dots after
    // pan/zoom and after segment selection.
    const canvas = document.getElementById('detail-graph-canvas');
    weaponGraphHitState.startTime = startTime;
    weaponGraphHitState.endTime   = endTime;
    weaponGraphHitState.W         = canvas ? canvas.clientWidth : 0;
    weaponGraphHitState.dropMarks = dropMarks;

    if (document.getElementById('weapons-per-player-details')?.open) {
        renderWeaponsPerPlayer(startTime, endTime);
    }
}

// Generic canvas hover-tooltip. Creates one `.canvas-tooltip` div inside the
// canvas's positioned parent, runs `hitTest(mx, my)` (canvas-local CSS px) on
// every mousemove, and — when it returns a truthy hit — fills the tip with
// `renderHtml(hit)` and clamp-positions it near the cursor (kept inside the
// wrapper's right edge). Idempotent per canvas. Shared by the weapon-graph
// drop-dot and powerup-span tooltips; the loc-graph tooltip is intentionally
// NOT built on this (it rides Cytoscape's own node/edge mouseover events and
// renderedPosition, not a raw canvas hit-test).
function attachCanvasTooltip(canvas, hitTest, renderHtml) {
    if (!canvas || canvas._canvasTipAttached) return;
    canvas._canvasTipAttached = true;

    const wrapper = canvas.parentElement; // positioned ancestor
    const tip = document.createElement('div');
    tip.className = 'canvas-tooltip';
    tip.style.display = 'none';
    wrapper.appendChild(tip);

    canvas.addEventListener('mousemove', (e) => {
        const rect = canvas.getBoundingClientRect();
        const mx = e.clientX - rect.left;
        const my = e.clientY - rect.top;
        const hit = hitTest(mx, my);
        if (!hit) { tip.style.display = 'none'; return; }
        tip.innerHTML = renderHtml(hit);
        tip.style.display = 'block';
        // Position offset from cursor; clamp inside the wrapper so the tip
        // doesn't get cut off near the right edge.
        const tipW = tip.offsetWidth || 200;
        const wrapW = wrapper.clientWidth;
        let left = mx + 12;
        if (left + tipW > wrapW) left = mx - tipW - 12;
        tip.style.left = left + 'px';
        tip.style.top  = (my + 12) + 'px';
    });
    canvas.addEventListener('mouseleave', () => { tip.style.display = 'none'; });
}

// Hover tooltip on the weapon-graph canvas: highlights the drop dot under
// the cursor and shows {player, weapon, loc, time}. Layout comes from the
// shared DIVERGING_GRAPH_LAYOUT (same source the renderer scales by dpr).
function attachWeaponGraphTooltip() {
    const canvas = document.getElementById('detail-graph-canvas');
    if (!canvas) return;

    const HIT_R  = 6;   // hit radius (slightly larger than dot radius=3)
    const HIT_DY = 8;   // vertical tolerance — generous so users can hover near
    const { H, AXIS_H, PAD, DROP_STRIP_H } = DIVERGING_GRAPH_LAYOUT;
    const graphH  = H - AXIS_H;
    const topY    = PAD + DROP_STRIP_H / 2;
    const bottomY = graphH - PAD - DROP_STRIP_H / 2;

    const hitTest = (mx, my) => {
        const s = weaponGraphHitState;
        if (!s.W || !s.dropMarks.length) return null;
        const duration = s.endTime - s.startTime;
        if (duration <= 0) return null;
        let best = null;
        let bestDx = HIT_R + 1;
        for (const m of s.dropMarks) {
            const x = ((m.time - s.startTime) / duration) * s.W;
            const y = m.isTop ? topY : bottomY;
            const dx = Math.abs(mx - x);
            const dy = Math.abs(my - y);
            if (dy <= HIT_DY && dx <= bestDx) {
                bestDx = dx;
                best = m;
            }
        }
        return best;
    };

    const renderHtml = (best) => {
        const d = best.drop;
        const weapon = (d.weapon || '').toUpperCase();
        const locLine = d.loc ? `<div>Loc: ${escapeHtml(d.loc)}</div>` : '';
        return `<div><strong>${escapeHtml(d.player || '?')}</strong> dropped <strong>${weapon}</strong></div>
${locLine}<div>Time: ${formatDuration(d.time)}</div>`;
    };

    attachCanvasTooltip(canvas, hitTest, renderHtml);
}

function updateHealthArmorGraph(startTime, endTime) {
    const teams = timelineState.teams;
    if (teams.length < 2) return;
    if (individualFieldActive()) { renderHealthArmorPerPlayer(startTime, endTime); return; }
    const { points, max } = prepHealthArmorData(startTime, endTime, teams);
    const legendA = document.getElementById('legend-health-team-a');
    const legendB = document.getElementById('legend-health-team-b');
    if (legendA) legendA.textContent = `${teams[0]} ↑`;
    if (legendB) legendB.textContent = `${teams[1]} ↓`;
    renderDivergingGraph('health-armor-canvas', {
        startTime, endTime, dataPoints: points, maxValue: max,
        yAxisId: 'health-y-axis',
    });

    if (document.getElementById('ha-per-player-details')?.open) {
        renderHealthArmorPerPlayer(startTime, endTime);
    }
}

function updateScoreTimeline(startTime, endTime) {
    const teams = timelineState.teams;
    if (teams.length < 2) return;
    if (individualFieldActive()) { renderFragsPerPlayer(startTime, endTime); return; }
    const { points, max } = prepScoreData(startTime, endTime, teams);
    const legendA = document.getElementById('legend-score-team-a');
    const legendB = document.getElementById('legend-score-team-b');
    if (legendA) { legendA.textContent = `${teams[0]} leading ↑`; legendA.style.color = TEAM_COLORS[0]; }
    if (legendB) { legendB.textContent = `${teams[1]} leading ↓`; legendB.style.color = TEAM_COLORS[1]; }
    renderDivergingGraph('score-canvas', {
        startTime, endTime, dataPoints: points, maxValue: max,
        yAxisId: 'score-y-axis', yTopLabel: `+${max}`, yBottomLabel: `-${max}`,
    });

    if (document.getElementById('frags-per-player-details')?.open) {
        renderFragsPerPlayer(startTime, endTime);
    }
}

// ─── Region Control Timeline ────────────────────────────────────────────────
//
// One row per control region; each row colors contiguous spans by indexing
// into mapState.bucketStates (the Go-supplied per-bucket state strings from
// view.RegionControl). Only the "strong" states (solo armed control +
// armed-vs-armed contested) paint pixels — weak states render as gaps to
// keep the color story readable.

const RC_ROW_H = 20;
const RC_AXIS_H = 20;

function prepRegionControlData(startTime, endTime, teams) {
    const regions = mapState.controlRegions;
    const bucketStates = mapState.bucketStates;
    if (!regions || regions.length === 0 || !bucketStates) return null;
    const view = timelineState.bucketView;
    if (!view || !view.count) return null;

    // Team labels come from the Go-supplied regionControl when present
    // (matches the bucketStates A/B encoding); fall back to mapState.teams
    // for the rendering layer that wants to show team names.
    const teamA = mapState.teamA || teams[0];
    const teamB = mapState.teamB || teams[1];

    // bucketStates is computed at the same 50ms-from-match-start grid as the
    // bucket view (recomputeRegionControl uses WindowMs:50), so its per-region
    // string indexes 1:1 with the columnar bucket index.
    const idx0 = bucketIndexAtOrAfter(view, startTime);
    const rows = [];
    for (const r of regions) {
        const s = bucketStates[r.name];
        if (typeof s !== 'string') continue;
        const spans = [];
        let curState = null;
        let curStart = startTime;
        for (let i = idx0; i < view.count; i++) {
            const bt = bucketTimeSec(view, i);
            if (bt > endTime) break;
            const c = i < s.length ? s[i] : '_';
            const state = MapView.decodeRegionStateChar(c);
            if (state !== curState) {
                if (curState) spans.push({ start: curStart, end: bt, state: curState });
                curState = state;
                curStart = bt;
            }
        }
        if (curState) spans.push({ start: curStart, end: endTime, state: curState });
        rows.push({ name: r.name, spans });
    }
    return { rows, teamA, teamB };
}

// Generic span-timeline renderer. Each row carries a list of
// {start, end, state} spans; stateColors maps state strings to fill
// colors. Used by both the region-control timeline and the powerup
// timeline so they share one renderer instead of two near-copies.
// dropMarks (optional): [{row, time, color}] — small dots painted in a
// row, e.g. weapon-drop events on the per-player weapons timeline. Callers
// that don't pass it (powerups, region control) render unchanged.
function renderSpansTimeline(canvasId, labelsId, { startTime, endTime, rows, stateColors, dropMarks, rowH }) {
    const labelsEl = document.getElementById(labelsId);
    if (!labelsEl) return;
    // rowH lets a caller stretch the rows (the per-player weapons view does in
    // the individual layout); the region-control and powerup rows take RC_ROW_H,
    // which their hover hit-tests assume.
    const ROW_H_CSS = rowH || RC_ROW_H;
    const setup = setupGraphCanvas(canvasId, rows.length * ROW_H_CSS + RC_AXIS_H);
    if (!setup) return;
    const { ctx, Wcss, W, dpr } = setup;

    const ROW_H = Math.round(ROW_H_CSS * dpr);
    const ROW_PAD = Math.max(1, Math.round(dpr));

    // Label column, one DOM element per row, sized to match the canvas
    // row in CSS pixels (the labels live outside the canvas).
    labelsEl.innerHTML = '';
    for (const r of rows) {
        const lab = document.createElement('div');
        lab.className = 'region-timeline-label';
        lab.style.height = ROW_H_CSS + 'px';
        lab.style.lineHeight = ROW_H_CSS + 'px';
        if (r.color) lab.style.color = r.color;
        lab.textContent = r.name;
        lab.title = r.name;
        labelsEl.appendChild(lab);
    }

    const graphH = rows.length * ROW_H;
    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, graphH);

    const duration = endTime - startTime;
    if (duration <= 0) return;

    rows.forEach((row, idx) => {
        const y = idx * ROW_H;
        for (const span of row.spans) {
            const color = stateColors[span.state];
            if (!color) continue;
            const x1 = Math.round(((span.start - startTime) / duration) * W);
            const x2 = Math.round(((span.end - startTime) / duration) * W);
            const w = x2 - x1;
            if (w <= 0) continue;
            ctx.fillStyle = color;
            ctx.fillRect(x1, y + ROW_PAD, w, ROW_H - 2 * ROW_PAD);
        }
    });

    if (dropMarks && dropMarks.length) {
        const dotR = 3 * dpr;
        for (const m of dropMarks) {
            const x = ((m.time - startTime) / duration) * W;
            if (x < -dotR || x > W + dotR) continue;
            const y = m.row * ROW_H + ROW_H / 2;
            ctx.fillStyle = m.color;
            ctx.beginPath();
            ctx.arc(x, y, dotR, 0, Math.PI * 2);
            ctx.fill();
        }
    }

    drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime });
}

// ─── Powerup Timeline ───────────────────────────────────────────────────────
//
// One row per powerup type (Quad / Pent / Ring); each row colors contiguous
// spans by which team currently holds the powerup. Reuses renderSpansTimeline,
// the same renderer the region-control timeline uses — only the input rows
// and the state→color map differ.
//
// Sourced from result.timelineAnalysis.powerupEvents (one record per run,
// already containing player/team/frags/duration), not from per-bucket
// aggregates: that gives us exact span boundaries plus the metadata the
// hover tooltip needs.

const POWERUP_TYPES = [
    { key: 'quad', name: 'Quad' },
    { key: 'pent', name: 'Pent' },
    { key: 'ring', name: 'Ring' },
];

function prepPowerupRowsData(startTime, endTime, teams) {
    const events = timelineState.powerupEvents;
    if (!events || events.length === 0) return null;
    const teamA = teams[0], teamB = teams[1];
    if (!teamA || !teamB) return null;

    const rowByKey = new Map();
    const rows = POWERUP_TYPES.map(pu => {
        const r = { name: pu.name, spans: [] };
        rowByKey.set(pu.key, r);
        return r;
    });

    for (const ev of events) {
        const row = rowByKey.get(ev.powerupType);
        if (!row) continue;
        // Clip the run to the visible window so the bar doesn't extend
        // beyond the canvas; record the original endpoints in the meta
        // so the tooltip still shows the full duration.
        const start = Math.max(startTime, ev.time);
        const end   = Math.min(endTime,   ev.endTime);
        if (end <= start) continue;
        let state;
        if      (ev.team === teamA) state = 'teamA';
        else if (ev.team === teamB) state = 'teamB';
        else                        state = 'other'; // mid-game team change / spectator
        row.spans.push({ start, end, state, event: ev });
    }
    return { rows, teamA, teamB };
}

// Hit-test state for the powerup-timeline span hover tooltip — populated
// by updatePowerupTimeline after each render so the canvas mousemove
// handler can identify which row + span sits under the cursor.
const powerupGraphHitState = {
    startTime: 0,
    endTime:   0,
    W:         0,
    rows:      [], // [{name, spans}]
};

function updatePowerupTimeline(startTime, endTime) {
    const panel = document.getElementById('powerup-timeline-panel');
    if (!panel) return;
    const teams = timelineState.teams;
    if (teams.length < 2) { panel.style.display = 'none'; return; }
    // The rows are per powerup TYPE coloured by which of two TEAMS holds it,
    // so an individual layout with more than two players paints the top two
    // and greys every other run as 'other'. Hidden rather than recoloured —
    // same call initRegionControl makes for the same reason.
    if (individualFieldActive()) { panel.style.display = 'none'; return; }

    const data = prepPowerupRowsData(startTime, endTime, teams);
    if (!data) { panel.style.display = 'none'; return; }

    // Hide if no powerup activity at all in this window — keeps the
    // panel out of the way for maps without powerups.
    const hasAny = data.rows.some(r => r.spans.length > 0);
    if (!hasAny) { panel.style.display = 'none'; return; }
    panel.style.display = '';

    const teamAEl = document.getElementById('pu-tl-teamA');
    const teamBEl = document.getElementById('pu-tl-teamB');
    if (teamAEl) teamAEl.textContent = data.teamA;
    if (teamBEl) teamBEl.textContent = data.teamB;
    const setLegend = (id, color) => {
        const el = document.getElementById(id);
        if (el) el.style.background = color;
    };
    setLegend('pu-legend-a', teamStrongColor(TEAM_COLORS[0]));
    setLegend('pu-legend-b', teamStrongColor(TEAM_COLORS[1]));

    renderSpansTimeline('powerup-canvas', 'powerup-timeline-labels', {
        startTime, endTime, rows: data.rows,
        stateColors: {
            teamA: teamStrongColor(TEAM_COLORS[0]),
            teamB: teamStrongColor(TEAM_COLORS[1]),
            other: 'rgba(180, 180, 180, 0.85)',
        },
    });

    // Refresh hit-test cache for the hover tooltip.
    const canvas = document.getElementById('powerup-canvas');
    powerupGraphHitState.startTime = startTime;
    powerupGraphHitState.endTime   = endTime;
    powerupGraphHitState.W         = canvas ? canvas.clientWidth : 0;
    powerupGraphHitState.rows      = data.rows;
}

// Mousemove tooltip on the powerup-canvas. Hit-tests against the
// last-rendered row layout (RC_ROW_H per row) and shows the source
// PowerupEvent metadata (player, team, frags, duration).
function attachPowerupTimelineTooltip() {
    const canvas = document.getElementById('powerup-canvas');

    const hitTest = (mx, my) => {
        const s = powerupGraphHitState;
        if (!s.W || !s.rows.length) return null;
        const duration = s.endTime - s.startTime;
        if (duration <= 0) return null;
        // Row index by Y; gracefully ignore the axis strip below the rows.
        const rowIdx = Math.floor(my / RC_ROW_H);
        if (rowIdx < 0 || rowIdx >= s.rows.length) return null;
        const row = s.rows[rowIdx];
        // Find the first span whose [start, end] window contains the cursor x.
        let hit = null;
        for (const sp of row.spans) {
            const x1 = ((sp.start - s.startTime) / duration) * s.W;
            const x2 = ((sp.end   - s.startTime) / duration) * s.W;
            if (mx >= x1 && mx <= x2) { hit = sp; break; }
        }
        if (!hit || !hit.event) return null;
        return { row, sp: hit };
    };

    const renderHtml = ({ row, sp }) => {
        const ev = sp.event;
        const player = escapeHtml(ev.playerName || 'Unknown');
        const team   = ev.team ? `<div>Team: ${escapeHtml(ev.team)}</div>` : '';
        const dur    = (ev.duration != null) ? `${Math.round(ev.duration)}s` : '?';
        return `<div><strong>${escapeHtml(row.name)}</strong> · <strong>${player}</strong></div>
${team}<div>Frags: ${ev.frags || 0}</div>
<div>Duration: ${dur}</div>`;
    };

    attachCanvasTooltip(canvas, hitTest, renderHtml);
}

function updateRegionControlTimeline(startTime, endTime) {
    const panel = document.getElementById('region-control-timeline-panel');
    if (!panel) return;
    const teams = timelineState.teams;
    if (teams.length < 2) { panel.style.display = 'none'; return; }

    const data = prepRegionControlData(startTime, endTime, teams);
    if (!data) { panel.style.display = 'none'; return; }
    panel.style.display = '';

    const teamAEl = document.getElementById('rc-tl-teamA');
    const teamBEl = document.getElementById('rc-tl-teamB');
    if (teamAEl) teamAEl.textContent = data.teamA;
    if (teamBEl) teamBEl.textContent = data.teamB;
    const setLegend = (id, color) => {
        const el = document.getElementById(id);
        if (el) el.style.background = color;
    };
    setLegend('rc-legend-a-ctrl', teamStrongColor(TEAM_COLORS[0]));
    setLegend('rc-legend-b-ctrl', teamStrongColor(TEAM_COLORS[1]));

    renderSpansTimeline('region-control-canvas', 'region-timeline-labels', {
        startTime, endTime, rows: data.rows,
        stateColors: {
            teamAControl: teamStrongColor(TEAM_COLORS[0]),
            contested:    'rgb(255, 255, 255)',
            teamBControl: teamStrongColor(TEAM_COLORS[1]),
        },
    });
}

// ─── Team Status Panel ──────────────────────────────────────────────────────

// Sort state shared by both timeline roster tables. The default is player
// name ascending: the rosters are rebuilt on every playhead tick, so the old
// frags-descending order made rows swap places as the match progressed —
// churn under the reader's eyes while scrubbing. Reset per demo load.
const TEAM_STATUS_SORT_DEFAULT = { key: 'name', dir: 1 };
let teamStatusSort = { ...TEAM_STATUS_SORT_DEFAULT };

// dir: 1 ascending, -1 descending. Numeric keys tie-break on name (always
// ascending) so equal-frag rows keep a stable, readable order.
function compareTeamStatusRows(a, b) {
    const k = teamStatusSort.key;
    if (k === 'name') return a.name.localeCompare(b.name) * teamStatusSort.dir;
    const cmp = (a[k] || 0) - (b[k] || 0);
    return cmp ? cmp * teamStatusSort.dir : a.name.localeCompare(b.name);
}

// One delegated listener per roster container, wired at load. updateTeamStatus
// rewrites the tables' innerHTML on every tick, so per-<th> listeners (what
// makeSortable installs) would be thrown away between clicks.
function setupTeamStatusSort() {
    document.querySelectorAll('.team-status-panel').forEach(panel => {
        panel.addEventListener('click', e => {
            const th = e.target.closest('th[data-key]');
            if (!th || !panel.contains(th)) return;
            const key = th.dataset.key;
            if (teamStatusSort.key === key) {
                teamStatusSort.dir = -teamStatusSort.dir;
            } else {
                // First click: names read A→Z, numbers biggest-first.
                teamStatusSort = { key, dir: key === 'name' ? 1 : -1 };
            }
            updateTeamStatus();
        });
    });
}

function updateTeamStatus() {
    const containerA = document.getElementById('team-status-a');
    const containerB = document.getElementById('team-status-b');
    if (!containerA || !containerB) return;

    // Individual layout: the two-sided status panel is hidden, so don't
    // rebuild its rows on every playhead tick.
    if (individualFieldActive()) {
        containerA.innerHTML = '';
        containerB.innerHTML = '';
        return;
    }

    const teams = timelineState.teams;
    const view = timelineState.bucketView;
    if (!view || !view.count || teams.length < 2) {
        containerA.innerHTML = '';
        containerB.innerHTML = '';
        return;
    }

    // Find high-res bucket at current time
    const time = mapState.currentTime;
    const hrBucket = findBucketAtTime(time);
    const pd = hrBucket ? (hrBucket.p || {}) : {};
    const fragCounts = getFragsAtTime(time);

    for (let ti = 0; ti < 2; ti++) {
        const team = teams[ti];
        const container = ti === 0 ? containerA : containerB;

        // Collect ALL players for this team — show dead/respawning players with '-' stats
        // (matching the map legend behavior)
        const players = [];
        const allPlayerNames = new Set();

        // Get all known players for this team from demoInfo/playerSymbols
        const demoPlayers = currentResult?.demoInfo?.players || [];
        for (const dp of demoPlayers) {
            if (dp.team === team) allPlayerNames.add(dp.name);
        }
        for (const [name, info] of Object.entries(mapState.playerSymbols || {})) {
            if (info.team === team) allPlayerNames.add(name);
        }
        // Also include any players present in bucket data for this team
        for (const [name, data] of Object.entries(pd)) {
            const t = data.team || mapState.playerSymbols?.[name]?.team;
            if (t === team) allPlayerNames.add(name);
        }

        for (const name of allPlayerNames) {
            const data = pd[name];
            const isDead = !data || (data.d ?? data.dead) || (data.h ?? data.health ?? 0) <= 0;
            players.push({
                name,
                dead: isDead,
                health: isDead ? 0 : (data.h ?? data.health ?? 0),
                armor: isDead ? 0 : (data.a ?? data.armor ?? 0),
                armorType: isDead ? '' : (data.at ?? data.armorType ?? ''),
                hasRL: isDead ? false : (data.rl ?? data.hasRL ?? false),
                hasLG: isDead ? false : (data.lg ?? data.hasLG ?? false),
                hasQuad: isDead ? false : (data.q ?? data.hasQuad ?? false),
                hasPent: isDead ? false : (data.pe ?? false),
                hasRing: isDead ? false : (data.r ?? data.hasRing ?? false),
                frags: fragCounts[name] || 0,
            });
        }

        players.sort(compareTeamStatusRows);

        const teamFrags = players.reduce((s, p) => s + p.frags, 0);
        const teamHealth = players.reduce((s, p) => s + (p.health || 0), 0);
        const teamArmor = players.reduce((s, p) => s + (p.armor || 0), 0);

        const hubInfo = currentResult?.hubInfo;
        const playerUserIDs = currentResult?.timelineAnalysis?.playerUserIDs || {};

        // Color the team name + frag header in the team's identity color so
        // the two sides are visually distinct at a glance and match the
        // colors used everywhere else (map legend, score timeline, etc.).
        const teamColor = TEAM_COLORS[ti] || '#ccc';
        let html = `<h4 style="color: ${teamColor}">${escapeHtml(team)} — ${teamFrags} frags</h4>`;
        html += `<table class="team-status-table">`;
        // Sortable headers carry their key; setupTeamStatusSort reads it off
        // the delegated click. The arrow classes are the same ones
        // makeSortable sets, so the indicator styling is shared.
        const sortTh = (key, label) => {
            const active = teamStatusSort.key === key;
            const cls = `sortable${active ? (teamStatusSort.dir === 1 ? ' sort-asc' : ' sort-desc') : ''}`;
            return `<th class="${cls}" data-key="${key}">${label}</th>`;
        };
        html += `<tr>${sortTh('name', 'Player')}${sortTh('frags', 'Frags')}` +
            `${sortTh('health', 'Health')}${sortTh('armor', 'Armor')}` +
            `<th>Weapons</th><th>View</th></tr>`;

        for (const p of players) {
            const hubLink = buildHubWatchLink(p.name, time, hubInfo, playerUserIDs);

            if (p.dead) {
                html += `<tr>`;
                html += `<td>${escapeHtml(p.name)}</td>`;
                html += `<td>${p.frags}</td>`;
                html += `<td>-</td>`;
                html += `<td>-</td>`;
                html += `<td>-</td>`;
                html += `<td>${hubLink}</td>`;
                html += `</tr>`;
            } else {
                const hp = p.health || 0;
                const arm = p.armor || 0;
                const at = p.armorType || '';
                const armorClass = at ? `armor-${at}` : '';
                const armorStr = arm > 0 ? `<span class="${armorClass}">${arm} ${at.toUpperCase()}</span>` : '0';

                const weps = [];
                if (p.hasRL && p.hasLG) weps.push('RL+LG');
                else if (p.hasRL) weps.push('RL');
                else if (p.hasLG) weps.push('LG');
                if (p.hasQuad) weps.push('Quad');
                if (p.hasPent) weps.push('Pent');
                if (p.hasRing) weps.push('Ring');

                html += `<tr>`;
                html += `<td>${escapeHtml(p.name)}</td>`;
                html += `<td>${p.frags}</td>`;
                html += `<td>${hp}</td>`;
                html += `<td>${armorStr}</td>`;
                html += `<td>${weps.join(', ') || '-'}</td>`;
                html += `<td>${hubLink}</td>`;
                html += `</tr>`;
            }
        }

        // Totals row
        html += `<tr class="totals-row">`;
        html += `<td>Total</td>`;
        html += `<td>${teamFrags}</td>`;
        html += `<td>${teamHealth}</td>`;
        html += `<td>${teamArmor}</td>`;
        html += `<td></td>`;
        html += `<td></td>`;
        html += `</tr>`;

        html += `</table>`;
        container.innerHTML = html;
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

// ─── Hub Watch Link Helper ──────────────────────────────────────────────────

// The hub replay URL (src/lib/hub-url.js): from/to are DEMO-relative
// seconds (match time + demoOffset); `to` optional.
function hubReplayUrl(args) {
    return MvdLib.hubReplayUrl(args);
}

function buildHubWatchLink(playerName, time, hubInfo, playerUserIDs) {
    if (!hubInfo || !hubInfo.gameId) return '';
    const trackId = playerUserIDs[playerName];
    if (!trackId) return '';
    // Our times are match-relative (0 = match start). Hub uses demo-relative time
    // (includes countdown/warmup), so add demoOffset to convert.
    const from = Math.floor(time + (timelineState.demoOffset || 0));
    const url = hubReplayUrl({ gameId: hubInfo.gameId, from, track: trackId });
    return `<a href="${url}" target="_blank" class="hub-watch-link" title="Watch in Hub">hub</a>`;
}

// ─── Precomputed Frag Counts ────────────────────────────────────────────────

// Sorted array of { time, cumulative: { player: frags } }
// Built once per demo load; looked up via binary search.
let precomputedFrags = []; // [{ time, cumulative }]

function precomputeFragCounts() {
    const sorted = timelineState.fragEvents || []; // pre-sorted at intake
    precomputedFrags = [];
    if (sorted.length === 0) return;

    const running = {}; // player -> cumulative frags

    for (const fe of sorted) {
        running[fe.player] = (running[fe.player] || 0) + (fe.delta || 1);
        precomputedFrags.push({ time: fe.time, cumulative: { ...running } });
    }
}

function getFragsAtTime(time) {
    const idx = MapView.lowerBoundIndex(precomputedFrags, time, (a, i) => a[i].time);
    return idx < 0 ? {} : precomputedFrags[idx].cumulative;
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
// the previous, stale-tris group objects) and repaints.
function applyMapGeometry(geom) {
    mapView.setGeometry(geom);
    if (mapState.rcResult) {
        applyRegionConfig(); // also calls renderMap
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

// Early init of region control data (before timeline renders, before map init)
function initRegionControlData(result) {
    const rc = result.timelineAnalysis?.regionControl;
    if (!rc || !rc.regions || rc.regions.length === 0) return;

    // Ensure locations are available
    if (!mapState.locations || mapState.locations.length === 0) {
        mapState.locations = result.timelineAnalysis?.locationData || [];
    }

    // Set control regions and locToRegion from backend definitions
    mapState.controlRegions = rc.regions;
    mapState.rcResult = rc;
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

function initRegionControl(result) {
    const rc = result.timelineAnalysis?.regionControl;
    const panel = document.getElementById('region-control-panel');
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
        if (panel) panel.style.display = 'none';
        if (statusPanel) statusPanel.style.display = 'none';
        mapState.controlRegions = null;
        return;
    }

    // Store the original backend result and all locations for recomputation
    mapState.rcResult = rc;

    // Ensure location groups are processed
    if (!mapState.locationGroups && mapState.locations.length > 0) {
        mapView.rebuildLocationGroups();
    }

    // Build region config UI (editable text fields per region)
    buildRegionConfig(rc.regions);

    // Render the table from the Go-supplied stats that
    // initRegionControlData stashed on mapState. User edits route through
    // applyRegionConfig, which calls the WASM bridge to refresh both.
    renderRegionControlFromGo(rc.regions, mapState.regionControlStats, mapState.teamA, mapState.teamB);
    mapState.renderDirty = true;

    if (panel) panel.style.display = '';
    if (statusPanel) statusPanel.style.display = '';
}

function buildRegionConfig(regions) {
    const container = document.getElementById('region-config');
    if (!container) return;
    container.innerHTML = '';

    const rowsWrap = document.createElement('div');
    rowsWrap.className = 'region-config-rows';
    container.appendChild(rowsWrap);

    for (const region of regions) {
        const locNames = [...new Set(region.points.map(p => p.name))].join(', ');
        rowsWrap.appendChild(buildRegionRow(region.name, locNames));
    }

    const actions = document.createElement('div');
    actions.className = 'region-config-actions';
    actions.innerHTML = `
        <button type="button" class="region-add-btn">+ Add region</button>
        <button type="button" class="region-save-btn" title="Download these region definitions as JSON">Save…</button>
        <button type="button" class="region-load-btn" title="Load region definitions from a JSON file">Load…</button>
        <input type="file" class="region-load-input" accept="application/json,.json" hidden>
    `;
    container.appendChild(actions);

    actions.querySelector('.region-add-btn').addEventListener('click', () => {
        const row = buildRegionRow('', '');
        rowsWrap.appendChild(row);
        const nameInput = row.querySelector('.region-name-input');
        if (nameInput) nameInput.focus();
    });
    actions.querySelector('.region-save-btn').addEventListener('click', saveRegionConfig);
    const loadInput = actions.querySelector('.region-load-input');
    actions.querySelector('.region-load-btn').addEventListener('click', () => loadInput.click());
    loadInput.addEventListener('change', (e) => {
        const file = e.target.files && e.target.files[0];
        if (file) loadRegionConfig(file);
        loadInput.value = '';
    });
}

function buildRegionRow(name, locNames) {
    const row = document.createElement('div');
    row.className = 'region-config-row';
    row.innerHTML = `
        <input type="text" class="region-name-input" placeholder="region name" value="${escapeHtml(name)}">
        <input type="text" class="region-locs-input" placeholder="loc1, loc2, …" value="${escapeHtml(locNames)}">
        <button type="button" class="region-remove-btn" title="Remove region">&times;</button>
    `;
    row.querySelector('.region-name-input').addEventListener('change', () => applyRegionConfig());
    row.querySelector('.region-locs-input').addEventListener('change', () => applyRegionConfig());
    row.querySelector('.region-remove-btn').addEventListener('click', () => {
        row.remove();
        applyRegionConfig();
    });
    return row;
}

function readRegionConfigFromUI() {
    // Walk the per-row inputs in DOM order so the user's region order
    // is preserved when serialised. Empty rows (no name or no locs)
    // are dropped silently — they're transient editing state.
    const out = [];
    document.querySelectorAll('#region-config .region-config-row').forEach(row => {
        const name = (row.querySelector('.region-name-input')?.value || '').trim();
        const locsRaw = row.querySelector('.region-locs-input')?.value || '';
        const locs = locsRaw.split(',').map(s => s.trim()).filter(s => s);
        if (!name || locs.length === 0) return;
        out.push({ name, locs });
    });
    return out;
}

async function applyRegionConfig() {
    const rc = mapState.rcResult;
    if (!rc) return;

    // Read current region definitions from the per-row inputs
    const regions = [];
    const seenNames = new Set();
    for (const def of readRegionConfigFromUI()) {
        // Drop dupes — first occurrence wins so editing a row above
        // doesn't get masked by a stale row below sharing the same name.
        if (seenNames.has(def.name)) continue;
        seenNames.add(def.name);

        // Find matching locations from the full loc list. Match is
        // case-insensitive so a hand-edited "ya" still claims the
        // canonical "YA" loc — saves the user having to memorize the
        // exact post-substitution casing.
        const locSet = new Set(def.locs.map(s => s.toLowerCase()));
        const points = [];
        let sumX = 0, sumY = 0;
        for (const loc of mapState.locations) {
            if (locSet.has(loc.name.toLowerCase())) {
                points.push({ x: loc.x, y: loc.y, z: loc.z, name: loc.name });
                sumX += loc.x;
                sumY += loc.y;
            }
        }
        if (points.length > 0) {
            regions.push({
                name: def.name,
                points: points,
                centroidX: sumX / points.length,
                centroidY: sumY / points.length,
            });
        }
    }

    mapState.controlRegions = regions;
    MapView.computeRegionStacking(regions);

    // Build loc-name-to-region lookup
    mapState.locToRegion = {};
    for (const region of regions) {
        for (const pt of region.points) {
            mapState.locToRegion[pt.name] = region.name;
        }
    }

    // Build region-to-location-group mapping for coloring
    mapState.regionToGroups = {};
    if (mapState.locationGroups) {
        for (const group of mapState.locationGroups) {
            for (const region of regions) {
                let matched = false;
                for (const gpt of group.points) {
                    for (const rpt of region.points) {
                        const dx = gpt.x - rpt.x, dy = gpt.y - rpt.y;
                        if (dx * dx + dy * dy < 1) {
                            matched = true;
                            break;
                        }
                    }
                    if (matched) break;
                }
                if (matched) {
                    if (!mapState.regionToGroups[region.name]) {
                        mapState.regionToGroups[region.name] = [];
                    }
                    mapState.regionToGroups[region.name].push(group);
                }
            }
        }
    }

    // Recompute control stats via the WASM bridge (view.RegionControl).
    // Built and shipped together with this JS, so the export is always
    // available.
    const overrideJSON = JSON.stringify({
        regions: regions.map(r => ({
            name: r.name,
            locs: r.points.map(p => p.name),
        })),
    });
    let res;
    try {
        const jsonStr = await currentDemo.recomputeRegions(overrideJSON);
        res = JSON.parse(jsonStr);
    } catch (err) {
        console.warn('recomputeRegionControl:', err.message);
        return;
    }
    if (res.error) {
        console.warn('recomputeRegionControl:', res.error);
        return;
    }
    mapState.bucketStates       = res.bucketStates || null;
    mapState.regionControlStats = res.stats || null;
    mapState.teamA              = res.teamA || mapState.teamA;
    mapState.teamB              = res.teamB || mapState.teamB;
    renderRegionControlFromGo(regions, res.stats, res.teamA, res.teamB);

    // Force map redraw
    mapState.renderDirty = true;
    renderMap(mapState.currentTime);

    // Re-render timeline region control graph
    updateDetailView();
}

// renderRegionControlFromGo decorates Go-supplied per-region stats with
// teamA/teamB strings (the Go shape carries those at the parent level, not on
// each region), stashes them on mapState, and (re)draws the region-control
// matrix — the same canvas UX element as the loc heatmap.
function renderRegionControlFromGo(regions, stats, teamA, teamB) {
    if (!stats) return;
    const decorated = {};
    for (const r of regions) {
        const s = stats[r.name];
        if (!s) continue;
        decorated[r.name] = Object.assign({}, s, { teamA, teamB });
    }
    mapState.controlRegions = regions;
    mapState.controlStats = decorated;
    renderRegionHeatmap();
}

// Map name → safe filename stem for the Save button. Falls back to
// "regions" when no map info is available.
function regionConfigFilename() {
    const base = mapFileKey(currentResult).replace(/[^a-z0-9_-]/g, '');
    return (base || 'regions') + '.json';
}

function saveRegionConfig() {
    const regions = readRegionConfigFromUI();
    const json = JSON.stringify({ regions }, null, 4) + '\n';
    const blob = new Blob([json], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = regionConfigFilename();
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
}

function loadRegionConfig(file) {
    const reader = new FileReader();
    reader.onload = () => {
        let parsed;
        try {
            parsed = JSON.parse(reader.result);
        } catch (err) {
            alert('Failed to parse region JSON: ' + err.message);
            return;
        }
        const regions = Array.isArray(parsed?.regions) ? parsed.regions : null;
        if (!regions) {
            alert('Region JSON must have a top-level "regions" array.');
            return;
        }
        // Rebuild the row UI from the loaded definitions, then re-apply
        // so map render / stats / timeline pick up the change.
        const container = document.getElementById('region-config');
        if (!container) return;
        const rowsWrap = container.querySelector('.region-config-rows');
        if (!rowsWrap) return;
        rowsWrap.innerHTML = '';
        for (const r of regions) {
            const name = String(r.name || '').trim();
            const locs = Array.isArray(r.locs) ? r.locs.map(s => String(s)) : [];
            if (!name) continue;
            rowsWrap.appendChild(buildRegionRow(name, locs.join(', ')));
        }
        applyRegionConfig();
    };
    reader.onerror = () => alert('Failed to read file.');
    reader.readAsText(file);
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
    const fragCounts = typeof getFragsAtTime === 'function' ? getFragsAtTime(time) : {};

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
        cell.innerHTML = buildHubWatchLink(name, time, hubInfo, playerUserIDs);
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

// ─── Loc Graph (Cytoscape) ────────────────────────────────────────────────
//
// Prototype using Cytoscape.js for the loc-graph rendering. Swap-in
// alternative to the earlier hand-rolled canvas version so we can evaluate
// layout quality, interactivity and future extensibility (route analysis,
// player animation on top of nodes).
//
// FIXME: when per-time player positions are added later, this tab will need
// to be added to TABS_WITH_TIMELINE so the unified scrubber appears.

const locGraphState = {
    cy: null,            // Cytoscape instance
    graph: null,         // { locs, edges } from result.locGraph
    result: null,        // Full result — used for filter repopulation on re-init
    initialized: false,
    tooltip: null        // Hover tooltip DOM node
};

// cytoscape-fcose registers itself on the global `cytoscape` object when
// loaded; guard the registration so re-loading the file during dev doesn't
// throw.
function registerCytoscapeExtensions() {
    if (typeof cytoscape === 'undefined') return;
    if (typeof cytoscapeFcose !== 'undefined' && !cytoscape.__fcoseRegistered) {
        cytoscape.use(cytoscapeFcose);
        cytoscape.__fcoseRegistered = true;
    }
}

function initLocGraphView(result) {
    const graph = result && result.locGraph;
    const container = document.getElementById('locgraph-canvas');
    if (!container) return;

    locGraphState.graph = graph || null;
    locGraphState.result = result;

    const noData = document.getElementById('locgraph-no-data');
    if (!graph || !graph.locs || graph.locs.length === 0) {
        if (noData) noData.style.display = 'block';
        if (locGraphState.cy) {
            locGraphState.cy.destroy();
            locGraphState.cy = null;
        }
        renderLocHeatmap();
        return;
    }
    if (noData) noData.style.display = 'none';

    populateLocGraphFilter(result);
    populateLocMetricOptions();

    if (!locGraphState.initialized) {
        setupLocGraphControls();
        locGraphState.initialized = true;
    }

    buildOrRefreshCytoscape();
    renderLocHeatmap();
}

// Show only the loc-analysis metrics this demo actually has data for —
// "With Quad"/"With Pent" disappear on maps/modes where nobody held that
// powerup (e.g. quad in most 1v1s), so the user can't land on an empty
// graph + table. A node carries the conditioned sub-object only when some
// sample met the condition (omitempty), so presence == availability. If the
// current selection becomes unavailable, fall back to "Full time".
function populateLocMetricOptions() {
    const sel = document.getElementById('locgraph-metric');
    if (!sel) return;
    const avail = { all: true, armed: false, unarmed: false, quad: false, pent: false };
    const locs = (locGraphState.graph && locGraphState.graph.locs) || [];
    for (const n of locs) {
        if (n.armed) avail.armed = true;
        if (n.unarmed) avail.unarmed = true;
        if (n.quad) avail.quad = true;
        if (n.pent) avail.pent = true;
        if (avail.armed && avail.unarmed && avail.quad && avail.pent) break;
    }
    let currentOk = false;
    for (const opt of sel.options) {
        const ok = !!avail[opt.value];
        opt.hidden = !ok;
        opt.disabled = !ok;
        if (ok && opt.value === sel.value) currentOk = true;
    }
    if (!currentOk) sel.value = 'all';
}

function populateLocGraphFilter(result) {
    const select = document.getElementById('locgraph-filter');
    if (!select) return;

    const opts = [{ value: 'all', label: 'All' }];
    const teams = (result.demoInfo && result.demoInfo.teams) || [];
    const players = (result.demoInfo && result.demoInfo.players) || [];

    // Hide team options in duel mode (team name == player name for every player).
    if (!isDuel(result)) {
        for (const t of teams) opts.push({ value: 'team:' + t, label: 'Team: ' + t });
    }
    for (const p of players) {
        if (!p.name) continue;
        opts.push({ value: 'player:' + p.name, label: p.name });
    }

    const prev = select.value;
    select.innerHTML = '';
    for (const o of opts) {
        const opt = document.createElement('option');
        opt.value = o.value;
        opt.textContent = o.label;
        if (o.value === prev) opt.selected = true;
        select.appendChild(opt);
    }
    if (!opts.some(o => o.value === prev)) select.value = 'all';
}

function setupLocGraphControls() {
    const on = (id, ev, fn) => {
        const el = document.getElementById(id);
        if (el) el.addEventListener(ev, fn);
    };
    on('locgraph-filter', 'change', buildOrRefreshCytoscape);
    on('locgraph-edge-mode', 'change', buildOrRefreshCytoscape);
    on('locgraph-min-edge', 'change', buildOrRefreshCytoscape);
    on('locgraph-layout', 'change', buildOrRefreshCytoscape);
    // Metric (all / armed / quad) reweights the graph nodes and the heatmap.
    on('locgraph-metric', 'change', () => { buildOrRefreshCytoscape(); renderLocHeatmap(); });
    on('locgraph-show-labels', 'change', applyLocGraphStyle);
    on('locgraph-label-size', 'change', applyLocGraphStyle);
    on('locgraph-relayout', 'click', () => runLocGraphLayout(true));
    on('locgraph-fit', 'click', () => { if (locGraphState.cy) locGraphState.cy.fit(undefined, 30); });
}

function getLocGraphFilter() {
    const sel = document.getElementById('locgraph-filter');
    const val = sel ? sel.value : 'all';
    if (val.startsWith('team:')) return { kind: 'team', key: val.slice(5) };
    if (val.startsWith('player:')) return { kind: 'player', key: val.slice(7) };
    return { kind: 'all', key: '' };
}

// 'all' | 'armed' | 'quad' — which time breakdown to weight by.
function getLocMetric() {
    const sel = document.getElementById('locgraph-metric');
    return sel ? sel.value : 'all';
}

// Resolve a node's weight bundle for the chosen metric. 'all' is the node's
// own Total/ByPlayer/ByTeam; 'armed'/'quad' read the optional sub-objects
// (absent when no sample met the condition).
function metricWeightsOf(node, metric) {
    if (metric === 'armed') return node.armed || EMPTY_WEIGHTS;
    if (metric === 'unarmed') return node.unarmed || EMPTY_WEIGHTS;
    if (metric === 'quad') return node.quad || EMPTY_WEIGHTS;
    if (metric === 'pent') return node.pent || EMPTY_WEIGHTS;
    return node;
}
const EMPTY_WEIGHTS = { total: 0, byPlayer: {}, byTeam: {} };

function nodeWeightFor(node, filter, metric) {
    const w = metricWeightsOf(node, metric);
    if (filter.kind === 'player') return w.byPlayer?.[filter.key] || 0;
    if (filter.kind === 'team') return w.byTeam?.[filter.key] || 0;
    return w.total || 0;
}

// Edges carry the same metric conditioning as nodes, so each metric is a
// self-contained graph (its own nodes + edges).
function metricEdgeWeightsOf(edge, metric) {
    if (metric === 'armed') return edge.armed || EMPTY_WEIGHTS;
    if (metric === 'unarmed') return edge.unarmed || EMPTY_WEIGHTS;
    if (metric === 'quad') return edge.quad || EMPTY_WEIGHTS;
    if (metric === 'pent') return edge.pent || EMPTY_WEIGHTS;
    return edge;
}

function edgeWeightFor(edge, filter, metric) {
    const w = metricEdgeWeightsOf(edge, metric);
    if (filter.kind === 'player') return w.byPlayer?.[filter.key] || 0;
    if (filter.kind === 'team') return w.byTeam?.[filter.key] || 0;
    return w.total || 0;
}

// Build Cytoscape elements from the current graph + filter + edge-mode.
// Nodes/edges carry their filtered weight in data so styles can reference it
// directly instead of re-computing in the mapper.
function buildCytoscapeElements() {
    const { graph } = locGraphState;
    if (!graph) return [];
    const filter = getLocGraphFilter();
    const metric = getLocMetric();
    const edgeModeSel = document.getElementById('locgraph-edge-mode');
    const edgeMode = edgeModeSel ? edgeModeSel.value : 'all';
    const minEdgeSel = document.getElementById('locgraph-min-edge');
    const minEdge = minEdgeSel ? (parseInt(minEdgeSel.value, 10) || 1) : 1;

    let maxNodeWeight = 0;
    for (const n of graph.locs) {
        const w = nodeWeightFor(n, filter, metric);
        if (w > maxNodeWeight) maxNodeWeight = w;
    }
    let maxEdgeWeight = 0;
    for (const e of graph.edges) {
        if (edgeMode === 'normal' && e.kind !== 'normal') continue;
        if (edgeMode === 'teleport' && e.kind !== 'teleport') continue;
        const w = edgeWeightFor(e, filter, metric);
        if (w < minEdge) continue;
        if (w > maxEdgeWeight) maxEdgeWeight = w;
    }

    const elements = [];

    for (const n of graph.locs) {
        const w = nodeWeightFor(n, filter, metric);
        const norm = maxNodeWeight > 0 ? w / maxNodeWeight : 0;
        const mw = metricWeightsOf(n, metric);
        elements.push({
            group: 'nodes',
            data: {
                id: 'n:' + n.name,
                name: n.name,
                weight: w,
                weightNorm: norm,
                total: mw.total || 0,
                byPlayer: mw.byPlayer || {},
                byTeam: mw.byTeam || {},
                // Grey out nodes with zero contribution once a filter or a
                // non-default metric is active, so each conditioned graph
                // reads clearly while map context is preserved.
                dim: w === 0 && (filter.kind !== 'all' || metric !== 'all')
            },
            // Preset/geographic layout reads world coords directly from data.
            // Invert Y so "up" on screen matches "up" in map (QW Y is up).
            position: { x: n.x || 0, y: -(n.y || 0) }
        });
    }

    for (const e of graph.edges) {
        if (edgeMode === 'normal' && e.kind !== 'normal') continue;
        if (edgeMode === 'teleport' && e.kind !== 'teleport') continue;
        const w = edgeWeightFor(e, filter, metric);
        if (w === 0) continue; // Prune edges absent from this filter+metric subgraph
        if (w < minEdge) continue; // UI minimum-edge-count filter
        const norm = maxEdgeWeight > 0 ? w / maxEdgeWeight : 0;
        const ew = metricEdgeWeightsOf(e, metric);
        elements.push({
            group: 'edges',
            data: {
                id: 'e:' + e.from + '->' + e.to,
                source: 'n:' + e.from,
                target: 'n:' + e.to,
                weight: w,
                weightNorm: norm,
                kind: e.kind,
                total: ew.total || 0,
                byPlayer: ew.byPlayer || {},
                byTeam: ew.byTeam || {}
            }
        });
    }

    return elements;
}

function buildLocGraphStyle() {
    const showLabels = document.getElementById('locgraph-show-labels')?.checked ?? true;
    const labelSizeSel = document.getElementById('locgraph-label-size');
    const labelSize = labelSizeSel ? parseInt(labelSizeSel.value, 10) || 14 : 14;
    const filter = getLocGraphFilter();

    // Pick a node fill based on the filter so "Team: red" nodes are tinted
    // with the team colour. In the "all" view, fall back to a neutral blue.
    let nodeFill = '#8fb3ff';
    if (filter.kind === 'team') {
        const teams = timelineState.teams || [];
        const idx = teams.indexOf(filter.key);
        nodeFill = (idx >= 0 && idx < TEAM_COLORS.length) ? TEAM_COLORS[idx] : '#8fb3ff';
    } else if (filter.kind === 'player') {
        nodeFill = '#ffc107';
    }

    return [
        {
            selector: 'node',
            style: {
                'background-color': nodeFill,
                'border-color': 'rgba(0, 217, 255, 0.6)',
                'border-width': 1.5,
                // sqrt so diameter scales with sqrt(time); "eye" area ≈ time.
                'width':  'mapData(weightNorm, 0, 1, 16, 60)',
                'height': 'mapData(weightNorm, 0, 1, 16, 60)',
                'label': showLabels ? 'data(name)' : '',
                'color': '#dfe6f5',
                'font-size': labelSize,
                'font-family': 'Inter, sans-serif',
                'font-weight': 500,
                'text-valign': 'bottom',
                'text-margin-y': Math.max(4, labelSize * 0.3),
                'text-outline-color': '#0a0a15',
                'text-outline-width': Math.max(2, labelSize * 0.18)
            }
        },
        {
            selector: 'node[?dim]',
            style: {
                'background-opacity': 0.25,
                'border-opacity': 0.2,
                'text-opacity': 0.35
            }
        },
        {
            selector: 'edge',
            style: {
                'curve-style': 'bezier',
                'control-point-step-size': 40,
                'width': 'mapData(weightNorm, 0, 1, 1, 7)',
                'line-color': '#8fb3ff',
                'target-arrow-color': '#8fb3ff',
                'target-arrow-shape': 'triangle',
                'arrow-scale': 1.1,
                'opacity': 0.8
            }
        },
        {
            selector: 'edge[kind = "teleport"]',
            style: {
                'line-color': '#00d9ff',
                'target-arrow-color': '#00d9ff',
                'line-style': 'dashed',
                'line-dash-pattern': [8, 4]
            }
        },
        {
            selector: ':selected',
            style: {
                'border-color': '#ffc107',
                'border-width': 3,
                'line-color': '#ffc107',
                'target-arrow-color': '#ffc107'
            }
        },
        {
            selector: '.highlight',
            style: {
                'opacity': 1,
                'z-index': 50
            }
        },
        {
            selector: '.faded',
            style: {
                'opacity': 0.15
            }
        }
    ];
}

// Build the cytoscape instance on first call, otherwise swap in the new
// element set + layout. Keeping the instance alive preserves pan/zoom state
// when the user toggles a filter.
function buildOrRefreshCytoscape() {
    if (!locGraphState.graph) return;
    registerCytoscapeExtensions();
    const container = document.getElementById('locgraph-canvas');
    if (!container || typeof cytoscape === 'undefined') return;

    const elements = buildCytoscapeElements();

    if (!locGraphState.cy) {
        locGraphState.cy = cytoscape({
            container,
            elements,
            style: buildLocGraphStyle(),
            layout: { name: 'preset' },
            wheelSensitivity: 0.2,
            minZoom: 0.1,
            maxZoom: 4
        });
        attachLocGraphInteractions(locGraphState.cy);
        locGraphState.cy.on('zoom', scheduleLabelSizeUpdate);
    } else {
        locGraphState.cy.batch(() => {
            locGraphState.cy.elements().remove();
            locGraphState.cy.add(elements);
            locGraphState.cy.style(buildLocGraphStyle());
        });
    }

    runLocGraphLayout(false);
}

// Run the chosen layout. `animate` is false on filter changes (the common
// case) so layout runs instantly; the re-layout button passes true to get
// the animated effect.
function runLocGraphLayout(animate) {
    const cy = locGraphState.cy;
    if (!cy) return;
    const sel = document.getElementById('locgraph-layout');
    const name = sel ? sel.value : 'preset';

    let opts;
    if (name === 'preset') {
        opts = { name: 'preset', fit: true, padding: 30 };
    } else if (name === 'fcose') {
        opts = {
            name: 'fcose',
            quality: 'default',
            randomize: false,       // start from current positions
            animate: animate,
            animationDuration: 600,
            nodeRepulsion: 8000,
            idealEdgeLength: 80,
            edgeElasticity: 0.45,
            gravity: 0.25,
            fit: true,
            padding: 30
        };
    } else if (name === 'cose') {
        opts = { name: 'cose', animate: animate, padding: 30, fit: true };
    } else if (name === 'circle') {
        opts = { name: 'circle', animate: animate, padding: 30, fit: true };
    } else if (name === 'concentric') {
        opts = {
            name: 'concentric',
            animate: animate,
            padding: 30,
            fit: true,
            // Higher time spent = more central; Cytoscape expects larger
            // numbers to be more central.
            concentric: (node) => node.data('weight'),
            levelWidth: () => 1
        };
    } else {
        opts = { name: 'preset' };
    }

    const layout = cy.layout(opts);
    layout.one('layoutstop', updateDynamicLabelSize);
    layout.run();
    // Non-animated layouts don't always emit layoutstop synchronously;
    // run once now so the first paint already has correct sizing.
    updateDynamicLabelSize();
}

function applyLocGraphStyle() {
    if (!locGraphState.cy) return;
    locGraphState.cy.style(buildLocGraphStyle());
    updateDynamicLabelSize();
}

// Cytoscape font-size scales with zoom, so on wide maps (geographic
// preset) the fit-to-viewport zoom drops the effective pixel size below
// readability. Counter-act by recomputing font-size inversely proportional
// to the current zoom, clamped to a sensible range.
let _labelSizeRaf = 0;
function scheduleLabelSizeUpdate() {
    if (_labelSizeRaf) return;
    _labelSizeRaf = requestAnimationFrame(() => {
        _labelSizeRaf = 0;
        updateDynamicLabelSize();
    });
}
function updateDynamicLabelSize() {
    const cy = locGraphState.cy;
    if (!cy) return;
    const sel = document.getElementById('locgraph-label-size');
    const userSize = sel ? (parseInt(sel.value, 10) || 14) : 14;
    const zoom = cy.zoom() || 1;
    const target = Math.max(10, Math.min(48, userSize / zoom));
    cy.batch(() => {
        cy.nodes().style({
            'font-size': target,
            'text-margin-y': Math.max(4, target * 0.3),
            'text-outline-width': Math.max(2, target * 0.18)
        });
    });
}

// Click: show a tooltip with top-5 connections. Hover: fade the rest of the
// graph so the node's neighborhood is clear.
function attachLocGraphInteractions(cy) {
    // NB: not built on attachCanvasTooltip — this rides Cytoscape's own
    // node/edge mouseover delegation and renderedPosition (plus a graph-fade
    // side effect), not a raw canvas mousemove hit-test, so the shared helper
    // wouldn't fit without contortion.
    // Tooltip DOM — created lazily, reused across hovers.
    const container = document.getElementById('locgraph-canvas');
    if (!locGraphState.tooltip) {
        const tip = document.createElement('div');
        tip.className = 'locgraph-tooltip';
        tip.style.display = 'none';
        container.parentElement.appendChild(tip);
        locGraphState.tooltip = tip;
    }
    const tip = locGraphState.tooltip;

    const hideTip = () => { tip.style.display = 'none'; };
    const showTipAt = (evt, html) => {
        tip.textContent = '';
        tip.innerHTML = html;
        tip.style.display = 'block';
        // Position via renderedPosition so it tracks pan/zoom.
        const rect = container.getBoundingClientRect();
        const x = evt.renderedPosition ? evt.renderedPosition.x : 0;
        const y = evt.renderedPosition ? evt.renderedPosition.y : 0;
        tip.style.left = (x + 12) + 'px';
        tip.style.top = (rect.height - y - 12) + 'px'; // bottom-up coords
        // Simpler: use originalEvent client position if available.
        if (evt.originalEvent) {
            tip.style.left = (evt.originalEvent.clientX - rect.left + 12) + 'px';
            tip.style.top = (evt.originalEvent.clientY - rect.top + 12) + 'px';
        }
    };

    cy.on('mouseover', 'node', (evt) => {
        const node = evt.target;
        cy.elements().addClass('faded');
        node.removeClass('faded').addClass('highlight');
        node.connectedEdges().removeClass('faded').addClass('highlight');
        node.connectedEdges().connectedNodes().removeClass('faded').addClass('highlight');
        showTipAt(evt, nodeTooltipHtml(node));
    });
    cy.on('mouseover', 'edge', (evt) => {
        const edge = evt.target;
        cy.elements().addClass('faded');
        edge.removeClass('faded').addClass('highlight');
        edge.source().removeClass('faded').addClass('highlight');
        edge.target().removeClass('faded').addClass('highlight');
        showTipAt(evt, edgeTooltipHtml(edge));
    });
    cy.on('mouseout', 'node, edge', () => {
        cy.elements().removeClass('faded').removeClass('highlight');
        hideTip();
    });
    cy.on('tap', (evt) => {
        if (evt.target === cy) hideTip();
    });
}

function nodeTooltipHtml(node) {
    const name = node.data('name');
    // Node time weights are int32 ms (schema v57); the UI shows seconds.
    const total = (node.data('total') || 0) / 1000;
    const byPlayer = node.data('byPlayer') || {};
    const top = Object.entries(byPlayer).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const rows = top.map(([p, t]) => `<div>· ${escapeHtml(p)}: ${(t / 1000).toFixed(1)}s</div>`).join('');
    return `<div><strong>${escapeHtml(name)}</strong></div>
<div>Total time: ${total.toFixed(1)}s</div>
${rows}`;
}

function edgeTooltipHtml(edge) {
    const from = edge.data('source').replace(/^n:/, '');
    const to = edge.data('target').replace(/^n:/, '');
    const kind = edge.data('kind');
    const total = edge.data('total') || 0;
    const byPlayer = edge.data('byPlayer') || {};
    const top = Object.entries(byPlayer).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const rows = top.map(([p, c]) => `<div>· ${escapeHtml(p)}: ${c}</div>`).join('');
    return `<div><strong>${escapeHtml(from)} → ${escapeHtml(to)}</strong> (${kind})</div>
<div>Total transitions: ${total}</div>
${rows}`;
}

// Kept as a thin compatibility shim: the tab-switch handler and old callers
// invoke renderLocGraph(); route them to the new refresh path.
function renderLocGraph() {
    if (locGraphState.cy) {
        locGraphState.cy.resize();
        locGraphState.cy.fit(undefined, 30);
    } else if (locGraphState.graph) {
        buildOrRefreshCytoscape();
    }
    // The heatmap / region-control tables are plain HTML built once at load
    // (initLocGraphView, renderRegionControlFromGo) — unlike the canvas they
    // don't need a re-render when the tab is revealed, and skipping it keeps
    // the user's column sort intact across tab switches.
}

// ─── Loc Heatmap + Region Control matrices ───────────────────────────────────
//
// Both live in the loc-graph tab and share one renderer (renderHeatmapTable):
// a sortable .stats-table — rows on the y-axis, one column per series, each
// cell viridis-shaded by a precomputed intensity with the % printed in it.
// Using a real table (vs. a canvas) gives crisp text and free column sorting
// via makeSortable, and reuses the common renderTableRows tbody builder.
//
//   - Loc heatmap: rows = locs (busiest first); columns are the team
//     aggregates (every member's time combined) then one per player grouped by
//     team. Intensity = each column's share of its time in the loc, normalised
//     PER COLUMN to its own busiest loc (sqrt-curved).
//   - Region control: rows = regions; columns are the seven control states.
//     Intensity = each state's share, normalised PER ROW to the region's
//     busiest control state (Empty excluded — see buildRegionHeatmap).
//
// Normalisation is baked into each cell's intensity by the build* functions, so
// the renderer is policy-free. Team identity rides on the canonical
// TEAM_COLORS-by-timelineState.teams mapping (see CLAUDE.md "Team colors") as a
// coloured underline on the relevant column headers. Data comes from
// result.locGraph + result.demoInfo (locs) and mapState.controlStats (regions)
// — no extra analyzer pass.

// The shared viridis ramp (src/lib/heat-color.js), also the Aim tab's.
function heatColorRGB(t) {
    return MvdLib.heatColorRGB(t);
}

// Readable text color over an [r,g,b] fill — dark ink on light cells, white
// on dark ones (Rec. 601 luma).
function contrastInk(rgb) {
    const lum = 0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2];
    return lum > 150 ? '#10131f' : '#f0f0f0';
}

// Canonical team order: TEAM_COLORS is indexed by position in
// timelineState.teams (frag-sorted, set in displayResults) so the header
// underlines match the rest of the app. Fall back to demoInfo order before
// that runs.
function canonicalTeams(result) {
    if (timelineState.teams && timelineState.teams.length) return [...timelineState.teams];
    const dt = result.demoInfo && result.demoInfo.teams;
    return (dt && dt.length) ? [...dt] : [''];
}

// Short display label for a long player name; the full name is kept on the
// column header's title attribute. (QuakeWorld's in-game short name comes from
// the client-side `cl_fakename` cvar, which is only ever injected as a text
// prefix on say_team messages — never carried in userinfo — so there is no
// per-player short name in the demo stream to read.)
function shortName(name, n = 9) {
    name = String(name || '');
    return name.length > n ? name.slice(0, n - 1) + '…' : name;
}

// ── Shared matrix model ──────────────────────────────────────────────────────
//
// Both build* functions return the same shape, rendered by renderHeatmapTable:
//
//   {
//     rows:    [{ name, cells: [{ i, p }, ...] }]    // i: 0..1 intensity, p: %|null
//     columns: [{ label, full, team, teamIdx, ... }] // teamIdx<0 = no underline
//     teamCols,                                       // separator before col teamCols
//     cellTitle(col, row, ci) -> string               // <td> title text
//   }
//
// Normalisation (per-column for locs, per-row for regions) is baked into each
// cell's intensity `i` by the build* functions, so the renderer is policy-free.

// Build the loc matrix for `metric` ('all' | 'armed' | 'quad'): leading
// team-aggregate columns then one per player grouped by team; rows are locs
// (busiest first by the metric). Intensity is each column's share normalised to
// its own busiest loc (sqrt-curved to lift small shares). Returns null when
// there's nothing to show (e.g. nobody ever held a quad).
function buildLocHeatmap(result, metric) {
    const graph = result && result.locGraph;
    if (!graph || !graph.locs || graph.locs.length === 0) return null;
    const players = (result.demoInfo && result.demoInfo.players) || [];
    const teams = canonicalTeams(result);
    const teamIdxOf = (team) => {
        const i = teams.indexOf(team);
        return i >= 0 ? i : teams.length; // unknown teams share the trailing color
    };

    // Weights for the chosen metric (node itself for 'all', the armed/quad
    // sub-object otherwise) drive every time read below.
    const W = (n) => metricWeightsOf(n, metric);

    const playerTotal = new Map();
    for (const node of graph.locs) {
        for (const [p, t] of Object.entries(W(node).byPlayer || {})) {
            playerTotal.set(p, (playerTotal.get(p) || 0) + t);
        }
    }

    const baseLocs = graph.locs
        .filter(n => W(n).byPlayer && Object.keys(W(n).byPlayer).length > 0)
        .slice()
        .sort((a, b) => (W(b).total || 0) - (W(a).total || 0));
    if (baseLocs.length === 0) return null;

    const hasTeams = !isDuel(result) && teams.length >= 2 && !(teams.length === 1 && teams[0] === '');

    // Columns carry `members` so a cell value is uniformly sum(byPlayer[m]);
    // `kind` ('team' | 'player') drives the title wording. `label` is the short
    // header text, `full` the untruncated name kept on the header title.
    const columns = [];
    if (hasTeams) {
        for (const t of teams) {
            const members = players
                .filter(p => (p.team || '') === t && (playerTotal.get(p.name) || 0) > 0)
                .map(p => p.name);
            const total = members.reduce((s, p) => s + (playerTotal.get(p) || 0), 0);
            if (total > 0) columns.push({ kind: 'team', label: shortName(t), full: t, team: t, teamIdx: teamIdxOf(t), members, total });
        }
    }
    const teamCols = columns.length;

    const teamOfPlayer = new Map();
    for (const p of players) if (p.name) teamOfPlayer.set(p.name, p.team || '');
    const seen = new Set();
    for (const team of teams) {
        for (const p of players) {
            if (!p.name || seen.has(p.name)) continue;
            if ((p.team || '') !== team) continue;
            const total = playerTotal.get(p.name) || 0;
            if (total <= 0) continue;
            columns.push({ kind: 'player', label: shortName(p.name), full: p.name, team, teamIdx: teamIdxOf(team), members: [p.name], total });
            seen.add(p.name);
        }
    }
    for (const [p, total] of playerTotal) {
        if (seen.has(p) || total <= 0) continue;
        const team = teamOfPlayer.get(p) || '';
        columns.push({ kind: 'player', label: shortName(p), full: p, team, teamIdx: teamIdxOf(team), members: [p], total });
        seen.add(p);
    }
    if (columns.length === 0) return null;

    // Per-column max share → full intensity at each column's busiest loc.
    const secOf = (n, c) => { const bp = W(n).byPlayer || {}; return c.members.reduce((s, p) => s + (bp[p] || 0), 0); };
    const colMaxFrac = columns.map(c => {
        let m = 0;
        for (const n of baseLocs) {
            const f = c.total > 0 ? secOf(n, c) / c.total : 0;
            if (f > m) m = f;
        }
        return m || 1;
    });

    let rows = baseLocs.map(n => ({
        name: n.name,
        secs: columns.map(c => secOf(n, c)),
        cells: columns.map((c, ci) => {
            const sec = secOf(n, c);
            const share = c.total > 0 ? sec / c.total : 0;
            const norm = colMaxFrac[ci] > 0 ? share / colMaxFrac[ci] : 0;
            return { i: sec > 0 ? Math.sqrt(norm) : 0, p: sec > 0 ? share * 100 : null };
        }),
    }));
    rows = rows.filter(r => r.secs.some(v => v > 0));
    if (rows.length === 0) return null;

    return {
        rows, columns, teamCols,
        cellTitle: (col, row, ci) => {
            // row.secs holds int32-ms weights (schema v57); show seconds.
            const sec = (row.secs[ci] || 0) / 1000;
            const pct = row.cells[ci].p != null ? row.cells[ci].p : 0;
            const suffix = col.kind === 'team' ? 'of team time' : 'of their time';
            const who = col.full + (col.kind === 'player' && col.team && col.team !== col.full ? ` (${col.team})` : '');
            return `${who} · ${row.name}: ${sec.toFixed(1)}s · ${pct.toFixed(1)}% ${suffix}`;
        },
    };
}

// Build the region-control matrix: rows are regions, columns are the seven
// control states. Colour is normalised per-row to the region's busiest control
// state (Empty excluded — it is filler, not a control state, and would swamp
// the scale), so a row uses the full colormap; the printed % stays absolute.
function buildRegionHeatmap(regions, stats) {
    if (!regions || !regions.length || !stats) return null;
    const first = Object.values(stats)[0];
    if (!first) return null;
    const teamA = first.teamA || 'Team A';
    const teamB = first.teamB || 'Team B';

    // Columns: teamA control/weak (red underline) | contested/cont.weak/empty
    // (neutral) | teamB weak/control (blue underline). `label` is the header
    // text, `full` the title; the team-named columns carry the team identity.
    const columns = [
        { label: teamA,        full: `${teamA} control`,      key: 'teamAControl',     team: teamA, teamIdx: 0 },
        { label: `${teamA} wk`, full: `${teamA} weak control`, key: 'teamAWeakControl', team: teamA, teamIdx: 0 },
        { label: 'Cont',       full: 'Contested',             key: 'contested',        team: '',    teamIdx: -1 },
        { label: 'Cont wk',    full: 'Contested (weak)',      key: 'weakContested',    team: '',    teamIdx: -1 },
        { label: 'Empty',      full: 'Empty (no players)',    key: 'empty',            team: '',    teamIdx: -1, noHeat: true },
        { label: `${teamB} wk`, full: `${teamB} weak control`, key: 'teamBWeakControl', team: teamB, teamIdx: 1 },
        { label: teamB,        full: `${teamB} control`,      key: 'teamBControl',     team: teamB, teamIdx: 1 },
    ];

    const rows = [];
    for (const region of regions) {
        const s = stats[region.name];
        if (!s) continue;
        const rowMax = Math.max(
            s.teamAControl, s.teamAWeakControl, s.contested,
            s.weakContested, s.teamBWeakControl, s.teamBControl) || 1;
        rows.push({
            name: region.name,
            cells: columns.map(c => {
                const v = s[c.key] || 0;
                const i = c.noHeat ? 0 : (rowMax > 0 ? v / rowMax : 0);
                return { i, p: v };
            }),
        });
    }
    if (rows.length === 0) return null;

    return {
        rows, columns, teamCols: 0,
        cellTitle: (col, row, ci) => `${row.name} · ${col.full}: ${row.cells[ci].p}% of match`,
    };
}

function renderLocHeatmap() {
    const panel = document.getElementById('locheatmap-panel');
    if (!panel) return;
    const metric = getLocMetric();
    const data = locGraphState.result ? buildLocHeatmap(locGraphState.result, metric) : null;
    const noData = document.getElementById('locheatmap-no-data');
    if (!data) {
        // Clear any stale table from a previous metric so the empty-state
        // isn't shown alongside an outdated grid. (setHTML is a local of
        // resetUIToCleanState and was never in scope here — reaching this
        // branch threw a ReferenceError out of displayResults, which then
        // skipped the Aim tab, applyUrlState and hideLoadingOverlay. Any
        // demo with no loc-heatmap data hits it, a match-less recording
        // always does.)
        for (const id of ['locheatmap-thead-row', 'locheatmap-body']) {
            const el = document.getElementById(id);
            if (el) el.innerHTML = '';
        }
        const hasGraph = !!(locGraphState.graph && locGraphState.graph.locs && locGraphState.graph.locs.length);
        panel.style.display = hasGraph ? '' : 'none';
        if (noData) noData.style.display = hasGraph ? 'block' : 'none';
        return;
    }
    panel.style.display = '';
    if (noData) noData.style.display = 'none';
    renderHeatmapTable('locheatmap-table', 'locheatmap-thead-row', 'locheatmap-body', data, 'Loc');
}

// Re-render the region-control matrix from the stats stashed on mapState.
// Visibility of the panel itself is owned by initRegionControl.
function renderRegionHeatmap() {
    const data = buildRegionHeatmap(mapState.controlRegions, mapState.controlStats);
    if (!data) return;
    renderHeatmapTable('region-control-table', 'region-control-thead-row', 'region-control-body', data, 'Region');
}

// Render a matrix data model into a .stats-table: a header row (row-axis label
// + one team-underlined <th> per column) and a tbody built with the shared
// renderTableRows helper. Each cell is viridis-shaded by its intensity with the
// % printed and a data-sort-value, so makeSortable can sort any column. The
// table renders crisply and persists across tab switches — no canvas resize
// dance needed.
function renderHeatmapTable(tableId, theadRowId, tbodyId, data, firstColLabel) {
    const table = document.getElementById(tableId);
    const theadRow = document.getElementById(theadRowId);
    if (!table || !theadRow) return;
    const { columns, teamCols } = data;

    const heads = [`<th>${escapeHtml(firstColLabel)}</th>`];
    columns.forEach((col, ci) => {
        const cls = ['heatmap-col'];
        if (col.kind === 'team') cls.push('heatmap-col-team');
        if (teamCols && ci === teamCols) cls.push('heatmap-col-sep');
        const color = col.teamIdx >= 0 ? TEAM_COLORS[col.teamIdx % TEAM_COLORS.length] : '';
        const style = color ? ` style="border-bottom: 3px solid ${color}"` : '';
        heads.push(`<th class="${cls.join(' ')}"${style} title="${escapeHtml(col.full || col.label)}">${escapeHtml(col.label)}</th>`);
    });
    theadRow.innerHTML = heads.join('');

    renderTableRows(tbodyId, data.rows, (row) => {
        const tds = [`<td class="heatmap-rowname"><strong>${escapeHtml(row.name)}</strong></td>`];
        row.cells.forEach((cell, ci) => {
            const cls = ['heatmap-cell'];
            if (teamCols && ci === teamCols) cls.push('heatmap-col-sep');
            if (cell.p == null) { // column not present in this row (e.g. player never here)
                tds.push(`<td class="${cls.join(' ')}" data-sort-value="-1"></td>`);
                return;
            }
            const rgb = cell.i > 0 ? heatColorRGB(cell.i) : null;
            const style = rgb ? ` style="background: rgb(${rgb[0]}, ${rgb[1]}, ${rgb[2]}); color: ${contrastInk(rgb)}"` : '';
            const title = data.cellTitle ? data.cellTitle(columns[ci], row, ci) : '';
            const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
            // Cells default to "<p>%"; pass data.cellText to show another unit
            // (e.g. a raw count) while keeping cell.p as the sort key.
            const text = data.cellText ? data.cellText(cell) : `${Math.round(cell.p)}%`;
            tds.push(`<td class="${cls.join(' ')}"${style} data-sort-value="${cell.p}"${titleAttr}>${text}</td>`);
        });
        return tds.join('');
    });

    makeSortable(table);
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
        updateTimeIndicators(); // tail-calls updateTeamStatus() when range > 0
        updateMapLegend();
        updateRegionStatus();
        updateItemsPanelStatus(mapState.currentTime);
    }
}
