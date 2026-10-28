// MVD Analyzer Dashboard — Pure client-side via WASM
//
// What is left of the classic script: the Search tab, file / hub loading,
// the load orchestration (displayResults: reset, team order, body classes,
// topbar, the map and regions load, URL state), tab switching and the URL.
// Every other tab is Svelte (src/); this file reaches it through the
// window globals src/main.js publishes (MvdState, MvdLib, MvdMapTab, mvdData,
// MvdHub).

// ─── Teams ──────────────────────────────────────────────────────────────────
//
// Team colours live in src/state/teams.js (MvdState) — the one team → colour
// mapping, chosen by team NAME (see CLAUDE.md "Team colors").

// setCanonicalTeams sets the match's canonical team order (once per demo, from
// displayResults). In an individual layout with more than two players
// (`perPlayer`) every player is their own side and is coloured from the
// player palette.
function setCanonicalTeams(teams, perPlayer = false) {
    const players = [...(currentResult?.match?.players || []), ...(currentResult?.demoInfo?.players || [])];
    MvdState.setCanonicalTeams(teams, perPlayer, players);
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
// RE-RUNS the bucket-dependent loads (the map: its frames, bounds, trails;
// then the demo's own regions, now with their control states). They already
// ran once in displayResults with the bucket-derived fields empty (blank map
// trails / region overlay). Re-selecting the active tab re-runs its show
// path. (The Timeline tab, src/tabs/timeline/, redraws itself when the
// buckets land.)
function onBucketsReady(data) {
    if (currentResult.timelineAnalysis) loadMapAndRegions(currentResult);
    switchTab(activeTabName());

    if (typeof data.bucketsMs === 'number') {
        const deferred = (data.bucketsMs + (data.regionMs || 0)).toFixed(0);
        console.log(`[mvd-timing] Timeline/Map ready — ${deferred} ms of bucket work ran off the critical path`);
    }
}

// ─── Setup ──────────────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
    // Shared state (src/state/) the URL keeps (?t=, ?seg=, ?learn=). The
    // stores call their subscriber at once; that first call is not a change.
    let subscribed = false;
    MvdState.clock.subscribe((tick) => { if (subscribed) onClockTick(tick); });
    MvdState.segment.subscribe(() => { if (subscribed) updateUrlState(); });
    MvdMapTab.learnMode.subscribe(() => { if (subscribed) updateUrlState(); });
    subscribed = true;
    setupFileUpload();
    setupTabs();
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
// clock.js); every view follows it.
function setCurrentTime(time) {
    MvdState.clock.seek(time);
}

// onClockTick keeps the URL's ?t= in step. Playback ticks every frame and a
// stop seeks once more, so it is skipped; the Player engine ('engine') ticks
// every frame too, and is followed every 200 ms. A demo reset ('reset')
// happens mid-load.
let _engineSyncTimer = null;
function onClockTick({ from }) {
    if (from === 'reset') {
        clearTimeout(_engineSyncTimer);
        _engineSyncTimer = null;
    }
    if (from === 'playback' || from === 'reset') return;
    if (from === 'engine') {
        if (!_engineSyncTimer) {
            _engineSyncTimer = setTimeout(() => {
                _engineSyncTimer = null;
                updateUrlState();
            }, 200);
        }
        return;
    }
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

            if (MvdMapTab.learnMode.get()) {
                params.set('learn', '1');
            }

            if (MvdState.clock.t > 0) {
                params.set('t', Math.round(MvdState.clock.t));
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
    if (params.get('learn') === '1' && MvdMapTab.view.state.mapEntities.length > 0) {
        MvdMapTab.learnMode.set(true);
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
    updateUrlState();
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

    const order = MvdState.teams.get().order;
    let teams = order.length >= 2 ? [...order] : [];
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
    // team→colour source — the teams store's colors[i] belongs to order[i],
    // winner at 0, with the palette entry at each index
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

    if (result.timelineAnalysis || result.messages?.messages) {
        timeRender('initTimeline', () => initTimeline(result));
    }

    // The map and the demo's own regions. Cheap on the main thread; the
    // multi-second cost was the worker's bucket build, which now runs after
    // the result is delivered. These run here with the bucket-derived fields
    // still empty (map trails / region overlay blank) and onBucketsReady()
    // re-runs them once the 'buckets' message lands.
    if (result.timelineAnalysis) {
        timeRender('initMapView', () => loadMapAndRegions(result));
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

// ─── Demo reset and the match timeline ─────────────────────────────────────

// resetUIToCleanState wipes everything that could carry data from a
// previous demo, at the top of displayResults(), so each load starts from a
// blank slate — conditionally-populated panels must not survive a swap from
// e.g. a 4on4 demo to an FFA demo that simply lacks those fields. The
// ported tabs clear themselves on the demo store; the playback stops on it
// (src/state/playback.js).
function resetUIToCleanState() {
    MvdState.setCanonicalTeams([]);
    MvdState.segment.set(null);
    MvdState.clock.setDuration(0);

    document.title = 'MVD Analyzer';
    const topbar = document.getElementById('topbar-demo-info');
    if (topbar) topbar.innerHTML = '';

    // Two-sided default for the timeline AND the chat columns (CSS);
    // applyDuelModeUI re-applies the individual field layout if this demo
    // wants it.
    setIndividualFieldLayout(false);

    MvdMapTab.resetMap();
    // The Locs & Regions tab (src/tabs/locgraph/) clears its region editor
    // and matrix on this, and its loc graph on the demo store; the map drops
    // its control regions.
    MvdState.regionControl.set(null);

    // Body classes — duel-mode / individual-mode / no-team-rows / no-match
    // collapse some panels via CSS; clear them so a teamplay demo loaded after
    // a duel, an FFA or a match-less recording doesn't inherit the layout.
    document.body.classList.remove('duel-mode');
    document.body.classList.remove('individual-mode');
    document.body.classList.remove('no-team-rows');
    document.body.classList.remove('no-match');

    MvdState.clock.seek(0, 'reset');
}

// initTimeline sets the match length on the clock (the bar, the playback
// wrap and every clamp read it) and, when displayResults found no team
// order, the demo's own — on load and again when the deferred buckets land.
// The shared playhead was put at 0 by resetUIToCleanState; it is
// deliberately NOT reset here, so a ?t= deep link or early scrubbing during
// the deferred-bucket window survives.
function initTimeline(result) {
    if (MvdState.teams.get().order.length === 0) {
        const individual = isIndividualLayout(result);
        if (!individual && result.demoInfo?.teams) {
            setCanonicalTeams([...result.demoInfo.teams], false);
        } else if (result.match?.teams) {
            setCanonicalTeams(result.match.teams.map(t => t.name), isMultiPlayerIndividual(result));
        }
    }
    MvdState.clock.setDuration(MvdLib.matchDurationSec(result));
}

// loadMapAndRegions loads the demo into the Map tab's map view
// (src/tabs/map/map-model.js), then puts the demo's own regions in effect
// (src/state/regions.js): the map overlay and region status, the Locs &
// Regions editor and matrix and the Timeline's Region Control panel follow
// it. Region control is a two-SIDES display (view/region_control.go builds
// buckets only for exactly two labels): an individual layout with more than
// two players has no such pair, so though the Go side ships `regions` for
// it all the same (the region GEOMETRY is a property of the map) none of
// those surfaces is offered. A two-participant match (a duel, a 1v1 FFA)
// has two sides and keeps them.
function loadMapAndRegions(result) {
    MvdMapTab.loadMap(result);
    MvdState.resetRegionControl(result, !isMultiPlayerIndividual(result));
}
