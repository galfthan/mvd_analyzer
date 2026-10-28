// load.js — loading a demo and putting it on the page: the file drop /
// upload, the hub box, a search result and the playlist all come through
// loadDemo(), which loads through the data source (src/app/source.js) and
// hands the Result to displayResults() — reset, the canonical team order,
// the body classes, the top bar, the clock, the map and regions, the URL.
// Every tab follows the stores from there (src/state/).

import { clock } from '../state/clock.js';
import { teams, setCanonicalTeams } from '../state/teams.js';
import { demo } from '../state/demo.js';
import { segment } from '../state/timeline.js';
import { regionControl, resetRegionControl } from '../state/regions.js';
import { topbarDemo, loadStatus, hubInput, hubBusy } from '../state/shell.js';
import { parseGameId } from '../data/hub.js';
import * as mapModel from '../tabs/map/map-model.js';
import { playerStatsTeamRows } from '../lib/player-stats.js';
import { isDuel, isIndividualLayout, isMultiPlayerIndividual } from '../lib/result.js';
import { matchDurationSec } from '../lib/timeline.js';
import { canonicalTeamOrder, fallbackTeamOrder, canonicalPlayers } from '../lib/canonical-teams.js';
import { topbarView } from '../lib/topbar.js';
import { makeSortable } from '../ui/sortable.js';
import { mvdData } from './source.js';
import { startLoadTiming, recordLoadTimings, timeRender, finishLoadTiming } from './timing.js';
import { hideLoadingOverlay, analyzerHasFailed } from './overlay.js';
import { applyUrlState, updateUrlState } from './url.js';
import { switchTab, activeTabName } from './tabs.js';
import { urlTab } from '../lib/url-state.js';

// The DemoHandle behind the page: its view queries, region recompute and
// line of sight answer for that demo only.
let currentDemo = null;

const LOAD_STAGE_TEXT = {
    info: 'Fetching game info...',
    download: 'Downloading demo...',
    analyze: 'Analyzing...',
};

function setStatus(text, cls) {
    loadStatus.set({ text, cls });
}

function setLoadError(error) {
    setStatus('Error: ' + error.message, 'status error');
}

// The load-status line after a successful analyse. A demo the marker calls
// match-less analysed fine, so it is not an error (the red `status error` is
// reserved for a load or parse that failed) — but "Analysis complete!" over a
// Summary tab with no scoreboard reads as a bug, so it says what happened and
// points at the panel that explains it.
function setAnalysisStatus(result) {
    if (result.noMatch) {
        setStatus('Analysed — this recording holds no match (see Summary).', 'status');
        return;
    }
    setStatus('Analysis complete!', 'status success');
}

// loadDemo loads one demo through the data source (`input` is what
// WasmSource.load takes: a file's bytes, a hub row, or a hub game id) and
// renders it. Shared by the file drop, the hub box, search clicks and
// playlist runs. Resolves true once the demo is shown, false when a newer
// load was asked for before this one finished (the last pick wins — the
// data source drops the older one, its failure included). A flag, not the
// handle: window.loadDemo hands its answer to the headless checks, which
// would serialise the whole Result. Throws on
// failure for the caller to report. `deepLink` is the boot's ?gameId= load
// (loadDeepLink), the one load that takes the URL's tab / time / segment /
// learn mode; every other load starts on Summary at 0:00.
export async function loadDemo(input, { deepLink = false } = {}) {
    setStatus(loadStatus.get().text, 'status loading');
    const hubId = input.game?.id ?? input.gameId;
    if (hubId != null) hubInput.set(String(hubId));
    // The timing window opens once the analyzer is up, so a deep link's
    // total does not include the wasm load (logged on its own).
    await mvdData.ready;
    startLoadTiming();
    let handle;
    try {
        handle = await mvdData.load(input, {
            onStage: (stage) => { setStatus(LOAD_STAGE_TEXT[stage], loadStatus.get().cls); },
        });
    } catch (err) {
        if (err.superseded) return false;
        throw err;
    }
    recordLoadTimings(handle.timings);

    setAnalysisStatus(handle.result);
    currentDemo = handle;
    const result = handle.result;
    demo.set(handle);
    if (handle.game) {
        result.hubInfo = {
            gameId: handle.game.id,
            viewerUrl: `https://hub.quakeworld.nu/games/?gameId=${handle.game.id}`,
            players: handle.game.players
        };
    }
    handle.ready('buckets').then(
        (info) => { if (handle === currentDemo) onBucketsReady(info); },
        (err) => console.warn('deferred buckets failed:', err.message));

    displayResults(result, deepLink);
    return true;
}

// The load panel's three ways in. Each reports its own failure on the
// status line.

export async function uploadFile(file) {
    setStatus('Analyzing...', 'status loading');
    try {
        await loadDemo({ bytes: new Uint8Array(await file.arrayBuffer()), name: file.name });
    } catch (error) {
        setLoadError(error);
    }
}

export function loadFromHub() {
    return loadHubInput(false);
}

// loadDeepLink is the boot's auto-load of a ?gameId= link (src/app/boot.js):
// the hub box's load, plus the link's URL state.
export function loadDeepLink() {
    return loadHubInput(true);
}

async function loadHubInput(deepLink) {
    const input = hubInput.get().trim();
    if (!input) {
        alert('Please enter a game ID or URL');
        return;
    }

    setStatus('Fetching from QuakeWorld Hub...', 'status loading');
    hubBusy.set(true);

    try {
        await loadDemo({ gameId: parseGameId(input) }, { deepLink });
    } catch (error) {
        setLoadError(error);
        // If we were holding the loading overlay up for a deep-link auto-load,
        // drop it so the user sees the error message instead of a stuck
        // spinner — unless the analyzer itself failed, which the overlay says.
        if (!analyzerHasFailed()) hideLoadingOverlay();
    } finally {
        hubBusy.set(false);
    }
}

export async function loadGameFromSearch(game) {
    try {
        await loadDemo({ game });
        // displayResults() switches to the Summary tab as part of finishing a load.
    } catch (error) {
        setLoadError(error);
    }
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
    if (currentDemo.result.timelineAnalysis) loadMapAndRegions(currentDemo.result);
    switchTab(activeTabName());

    if (typeof data.bucketsMs === 'number') {
        const deferred = (data.bucketsMs + (data.regionMs || 0)).toFixed(0);
        console.log(`[mvd-timing] Timeline/Map ready — ${deferred} ms of bucket work ran off the critical path`);
    }
}

function displayResults(result, deepLink) {
    // Wipe every store and JS-side state object that could carry data from
    // a previous demo. Critical when swapping between demos with different
    // shapes (e.g. 4on4 → FFA where teams/regions/etc. are absent and would
    // otherwise survive).
    resetUIToCleanState();

    document.body.classList.remove('no-demo');

    // Land the user on the Summary tab after a fresh load, unless this is
    // the deep link and it names a tab (?tab=map should win —
    // applyUrlState() is called later and switches to it). Any other load
    // ignores the URL: it still describes the previous demo.
    if (!(deepLink && urlTab(new URLSearchParams(location.search).get('tab')))) {
        switchTab('summary');
    }

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
    // tab — each builds from the teams store — sees this demo's order, not
    // the previous demo's (src/lib/canonical-teams.js). Duels keep the
    // team palette (perPlayer is false at two participants), so a 1v1
    // colours exactly as it did before individual modes existed.
    {
        const { order, perPlayer } = canonicalTeamOrder(result);
        setCanonicalTeams(order, perPlayer, canonicalPlayers(result));
    }

    const top = topbarView(result, teams.get().order);
    topbarDemo.set(top.html);
    document.title = top.title;

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

    // The deep link's URL state (tab, time, segment, learn mode); any other
    // load writes the URL afresh for this demo.
    if (deepLink) applyUrlState();
    else updateUrlState();

    // Reveal the now-populated UI (overlay may already be hidden if the
    // demo was loaded interactively rather than via ?gameId=).
    hideLoadingOverlay();

    // Flush the consolidated load-timing breakdown to the console.
    finishLoadTiming();
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
    // are hidden by resetRegionControl, NOT by a missing-data gate — the
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

// resetUIToCleanState wipes everything that could carry data from a
// previous demo, at the top of displayResults(), so each load starts from a
// blank slate — conditionally-populated panels must not survive a swap from
// e.g. a 4on4 demo to an FFA demo that simply lacks those fields. The tabs
// clear themselves on the demo store; the playback stops on it
// (src/state/playback.js).
function resetUIToCleanState() {
    setCanonicalTeams([]);
    segment.set(null);
    clock.setDuration(0);

    document.title = 'MVD Analyzer';
    topbarDemo.set('');

    // Two-sided default for the timeline AND the chat columns (CSS);
    // applyDuelModeUI re-applies the individual field layout if this demo
    // wants it.
    setIndividualFieldLayout(false);

    mapModel.resetMap();
    // The Locs & Regions tab (src/tabs/locgraph/) clears its region editor
    // and matrix on this, and its loc graph on the demo store; the map drops
    // its control regions.
    regionControl.set(null);

    // Body classes — duel-mode / individual-mode / no-team-rows / no-match
    // collapse some panels via CSS; clear them so a teamplay demo loaded after
    // a duel, an FFA or a match-less recording doesn't inherit the layout.
    document.body.classList.remove('duel-mode');
    document.body.classList.remove('individual-mode');
    document.body.classList.remove('no-team-rows');
    document.body.classList.remove('no-match');

    clock.seek(0, 'reset');
}

// initTimeline sets the match length on the clock (the bar, the playback
// wrap and every clamp read it) and, when displayResults found no team
// order, the demo's own. The shared playhead was put at 0 by
// resetUIToCleanState; it is deliberately NOT reset here, so a ?t= deep
// link or early scrubbing during the deferred-bucket window survives.
function initTimeline(result) {
    if (teams.get().order.length === 0) {
        const fallback = fallbackTeamOrder(result);
        if (fallback) setCanonicalTeams(fallback.order, fallback.perPlayer, canonicalPlayers(result));
    }
    clock.setDuration(matchDurationSec(result));
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
    mapModel.loadMap(result);
    resetRegionControl(result, !isMultiPlayerIndividual(result));
}
