<script>
    // The Timeline tab: the two team rosters at the playhead (Team status),
    // the Score, Powerups, Weapons, Team Health/Armor and Region Control
    // graphs over the view window, and the per-player drill-downs under
    // Score, Weapons and Team Health/Armor. src/lib/timeline.js has the
    // series, the roster rows and the hover read-outs;
    // src/ui/timeline-canvas.js draws them; interaction.js is the zoom / pan
    // / scrub / hover handling. src/app/tabs.js toggles the `active` class like
    // on every other .tab-content.
    //
    // The view window is the selected segment (MvdState.segment): the
    // unified bar selects one, a graph's Ctrl+wheel zoom and drag pan here
    // set it, and the bar shows it. A segment
    // change redraws at once; a demo, teams, buckets or region change
    // redraws on the next frame the tab shows. Each time the tab shows (now,
    // on the next frame and 120 ms later — a canvas drawn during the
    // display:none → block switch can stay invisible in Firefox) and when
    // one of its panels comes back into view (onPanelReveal), everything is
    // drawn again: a canvas sizes itself from its container. The clock moves
    // only the current-time lines, the per-player frag labels and the
    // rosters — at once on a seek, every 200 ms (trailing) under the page's
    // playback (src/state/playback.js) or the Player engine. Nothing runs while the tab is
    // hidden.
    //
    // A field of more than two individual players (the teams store's
    // perPlayer, the same isMultiPlayerIndividual src/app/load.js puts on <body> as
    // `individual-field`, which the CSS hides the two-sided panels off) has
    // no pair of sides: the three per-player views leave their <details>
    // under the team graphs and render in panels of their own, with taller
    // rows. Every panel carries data-panel and sits directly in
    // #tab-timeline for src/ui/panels.
    import { onMount, flushSync } from 'svelte';
    import { demo, teams, activeTab, clock, segment, regionControl } from '../../state/index.js';
    import { reconstructBucketPlayers, bucketIndexAtTime } from '../../../../mvd-map-view/src/frames.js';
    import { formatDuration } from '../../lib/result.js';
    import { regionSides } from '../../lib/regions.js';
    import {
        timelineEvents, fragsAt, viewRange, clampSegment, indicatorPct, axisTicks,
        prepWeaponsData, computeBackpackDrops, prepHealthArmorData, prepScoreData,
        prepPowerupRowsData, prepRegionControlData, rosterTeamOf, playersByTeam,
        prepPlayerHAData, sharedStackMax, prepPlayerFragDeathData, fragDeathLabels,
        prepPlayerWeaponSpans, playerDropMarks, WEAPON_SPAN_COLORS,
        teamStrongColor, dimColor, RC_ROW_H,
        TEAM_STATUS_SORT_DEFAULT, nextTeamStatusSort, teamStatusRows, teamStatusHtml,
        hitDropMark, dropTooltipHtml, hitPowerupSpan, powerupTooltipHtml,
    } from '../../lib/timeline.js';
    import {
        renderDivergingGraph, renderMiniStack, renderMiniDiverging, renderSpansTimeline,
    } from '../../ui/timeline-canvas.js';
    import { onPanelReveal } from '../../ui/panels/reveal.js';
    import { createInteraction, tooltip } from './interaction.js';

    const TAB = 'timeline';
    // The canvases a window resize or a reveal re-measures (DOM order).
    const CANVAS_IDS = [
        'score-canvas', 'powerup-canvas', 'detail-graph-canvas',
        'health-armor-canvas', 'region-control-canvas', 'weapons-per-player-canvas',
    ];

    // The demo being drawn; refreshed on every redraw.
    let result = null;
    let view = null;
    let ev = timelineEvents(null);
    let teamOf = new Map();
    let order = [];
    let colors = [];

    // The per-player layout (see the header).
    let individual = $state(false);
    let rangeLabel = $state('');
    let indLeft = $state(null);
    let statusA = $state('');
    let statusB = $state('');
    let sort = TEAM_STATUS_SORT_DEFAULT;

    // Legends and y-axis labels keep their last values while a graph is not
    // drawn (fewer than two sides), as the markup they replace did.
    let scoreLegend = $state.raw({ a: 'Team A leading ↑', b: 'Team B leading ↓', ca: null, cb: null });
    let sideLegend = $state.raw({ a: 'Team A', b: 'Team B' });
    let yScore = $state.raw(['+20', '-20']);
    let yWeapons = $state.raw([4, 4]);
    let yHealth = $state.raw([800, 800]);
    let puShown = $state(false);
    let puLegend = $state.raw({ a: 'Team A', b: 'Team B', ca: null, cb: null });
    let rcShown = $state(false);
    let rcLegend = $state.raw({ a: 'Team A', b: 'Team B', ca: null, cb: null });

    // Per-player rows: the frags / deaths and health / armor grids
    // ({ name, cid, ti, color }), their shared axes (null = none), the frag
    // labels at the playhead.
    let fdRows = $state.raw([]);
    let haRows = $state.raw([]);
    let fdAxis = $state.raw(null);
    let haAxis = $state.raw(null);
    let fdStats = $state.raw({});
    let fdSig = null;
    let haSig = null;

    let fdOpen = $state(false);
    let wpOpen = $state(false);
    let haOpen = $state(false);

    let el = $state({});
    let fdTrack = $state();
    let haTrack = $state();

    // What the last render drew, for the hover read-outs.
    const dropHit = { startTime: 0, endTime: 0, W: 0, dropMarks: [] };
    const puHit = { startTime: 0, endTime: 0, W: 0, rows: [] };

    let dirty = true;
    let queued = false;
    let lastHandle = null;
    let syncTimer = null;
    let resizeRaf = null;

    const shown = () => activeTab.get() === TAB;
    const duration = () => (Number.isFinite(clock.duration) ? clock.duration : 0);
    const range = () => viewRange(segment.get(), duration());

    const ia = createInteraction({
        range,
        setRange(start, end) {
            const s = clampSegment(start, end, duration());
            if (s !== undefined) segment.set(s);
        },
        reset() {
            const s = clampSegment(0, duration(), duration());
            if (s !== undefined) segment.set(s);
        },
        time: () => clock.t,
        seek: (t) => clock.seek(t),
    });

    function schedule() {
        if (queued || !dirty || !shown()) return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && shown()) redraw();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function onDemo(handle) {
        if (handle !== lastHandle) {
            lastHandle = handle;
            sort = TEAM_STATUS_SORT_DEFAULT;
            dropHit.W = 0; dropHit.dropMarks = [];
            puHit.W = 0; puHit.rows = [];
            // A graph of the previous demo must not show through a panel
            // this one does not draw.
            for (const id of CANVAS_IDS) {
                const c = document.getElementById(id);
                if (c) c.getContext('2d').clearRect(0, 0, c.width, c.height);
            }
            rangeLabel = '';
            statusA = '';
            statusB = '';
            handle?.ready('buckets').then(() => { if (demo.get() === handle) invalidate(); }, () => {});
        }
        invalidate();
    }

    // redraw draws every graph for the view window, then the playhead.
    function redraw() {
        dirty = false;
        result = demo.get()?.result ?? null;
        view = result?.timelineAnalysis?.bucketView || null;
        ev = timelineEvents(result);
        teamOf = rosterTeamOf(result);
        const t = teams.get();
        order = t.order;
        colors = t.colors;
        individual = t.perPlayer;
        flushSync();
        const [start, end] = range();
        rangeLabel = segment.get() ? `(${formatDuration(start)} - ${formatDuration(end)})` : '';
        if (order.length >= 2) sideLegend = { a: order[0], b: order[1] };
        // DOM order: score, powerups, weapons, health / armor, region control.
        drawScore(start, end);
        drawPowerups(start, end);
        drawWeapons(start, end);
        drawHealthArmor(start, end);
        drawRegionControl(start, end);
        sync();
    }

    // sync puts the playhead on the drawn window: the current-time lines,
    // the per-player frag labels, the rosters.
    function sync() {
        const [start, end] = range();
        const pct = indicatorPct(clock.t, start, end);
        if (pct === null) return;
        indLeft = `${pct}%`;
        if (fdRows.length) fdStats = fragDeathLabels(ev.killEvents, ev.deathEvents, fdRows.map((r) => r.name), clock.t);
        syncTeamStatus();
    }

    function syncTeamStatus() {
        if (individual || !view || !view.count || order.length < 2) {
            statusA = '';
            statusB = '';
            return;
        }
        const time = clock.t;
        const i = bucketIndexAtTime(view, time);
        const frame = i >= 0 ? reconstructBucketPlayers(view, i) : {};
        const frags = fragsAt(result, time);
        const html = [0, 1].map((ti) => teamStatusHtml(result,
            teamStatusRows(result, teamOf, frame, frags, order[ti], sort), order[ti], colors[ti] || '#ccc', sort, time));
        statusA = html[0];
        statusB = html[1];
    }

    // One delegated click for both rosters: they are rebuilt on every tick,
    // so a listener per header would not survive.
    function onStatusClick(e) {
        const th = e.target.closest('th[data-key]');
        if (!th || !e.currentTarget.contains(th)) return;
        sort = nextTeamStatusSort(sort, th.dataset.key);
        syncTeamStatus();
    }

    function drawScore(start, end) {
        if (order.length < 2) return;
        if (individual) { drawFragsPerPlayer(start, end); return; }
        const { points, max } = prepScoreData(ev.fragEvents, start, end, order, colors);
        scoreLegend = { a: `${order[0]} leading ↑`, b: `${order[1]} leading ↓`, ca: colors[0], cb: colors[1] };
        renderDivergingGraph(el.score, { startTime: start, endTime: end, dataPoints: points, maxValue: max });
        yScore = [`+${max}`, `-${max}`];
        if (fdOpen) drawFragsPerPlayer(start, end);
    }

    function drawWeapons(start, end) {
        if (order.length < 2) return;
        // The A↑ / B↓ graph is hidden in the per-player layout: draw only
        // its per-player view — this runs on every pan / zoom frame.
        if (individual) { drawWeaponsPerPlayer(start, end); return; }
        const { points, max } = prepWeaponsData(view, start, end, order);
        const dropMarks = computeBackpackDrops(ev.backpacks, start, end, order);
        renderDivergingGraph(el.weapons, { startTime: start, endTime: end, dataPoints: points, maxValue: max, dropMarks });
        yWeapons = [max, max];
        dropHit.startTime = start;
        dropHit.endTime = end;
        dropHit.W = el.weapons ? el.weapons.clientWidth : 0;
        dropHit.dropMarks = dropMarks;
        if (wpOpen) drawWeaponsPerPlayer(start, end);
    }

    function drawHealthArmor(start, end) {
        if (order.length < 2) return;
        if (individual) { drawHealthPerPlayer(start, end); return; }
        const { points, max } = prepHealthArmorData(view, start, end, order);
        renderDivergingGraph(el.health, { startTime: start, endTime: end, dataPoints: points, maxValue: max });
        yHealth = [max, max];
        if (haOpen) drawHealthPerPlayer(start, end);
    }

    function drawPowerups(start, end) {
        // Rows are per powerup TYPE coloured by which of two TEAMS holds it,
        // so a field of players would paint the top two and grey the rest:
        // hidden there, as region control is.
        const data = order.length < 2 || individual ? null : prepPowerupRowsData(ev.powerupEvents, start, end, order);
        // Hidden too without a run in the window — out of the way on maps
        // without powerups.
        if (!data || !data.rows.some((r) => r.spans.length > 0)) {
            puShown = false;
            return;
        }
        puShown = true;
        const ca = teamStrongColor(colors[0]);
        const cb = teamStrongColor(colors[1]);
        puLegend = { a: data.teamA, b: data.teamB, ca, cb };
        flushSync();
        renderSpansTimeline(el.powerup, el.powerupLabels, {
            startTime: start, endTime: end, rows: data.rows,
            stateColors: { teamA: ca, teamB: cb, other: 'rgba(180, 180, 180, 0.85)' },
        });
        puHit.startTime = start;
        puHit.endTime = end;
        puHit.W = el.powerup ? el.powerup.clientWidth : 0;
        puHit.rows = data.rows;
    }

    // The region set in effect (src/state/regions.js). A field of players
    // has no two sides to colour: the model is not editable there and the
    // panel stays hidden, like the editor on Locs & Regions.
    function drawRegionControl(start, end) {
        const rc = regionControl.get();
        const data = order.length < 2 || !rc?.editable ? null : prepRegionControlData(rc, view, start, end, order);
        if (!data) {
            rcShown = false;
            return;
        }
        rcShown = true;
        // Go's side A is not necessarily canonical index 0 (regionSides).
        const [ia, ib] = regionSides(order, data);
        const ca = teamStrongColor(colors[ia]);
        const cb = teamStrongColor(colors[ib]);
        rcLegend = { a: data.teamA, b: data.teamB, ca, cb };
        flushSync();
        renderSpansTimeline(el.region, el.regionLabels, {
            startTime: start, endTime: end, rows: data.rows,
            stateColors: { teamAControl: ca, contested: 'rgb(255, 255, 255)', teamBControl: cb },
        });
    }

    // Row heights: compact under a team graph; taller in the per-player
    // layout, where these rows carry the tab.
    const miniH = () => (individual ? 60 : 44);
    const spanRowH = () => (individual ? 28 : RC_ROW_H);

    // The rows of a per-player grid, rebuilt only when the roster changes.
    function gridRows(prefix, prevSig) {
        const grouped = playersByTeam(view, order, teamOf, individual);
        const sig = grouped.map((g) => g.join('|')).join('#');
        if (sig === prevSig) return null;
        const rows = [];
        grouped.forEach((names, ti) => names.forEach((name, idx) =>
            rows.push({ name, cid: `${prefix}-${ti}-${idx}`, ti, color: colors[ti] || '#ccc' })));
        return { rows, sig };
    }

    function measureAxis(track, start, end) {
        return axisTicks(start, end, track?.clientWidth || 300, 10, 1e-6);
    }

    // One +/- row per player: running kills minus deaths, each row on its
    // own |net| scale so a small swing still fills it.
    function drawFragsPerPlayer(start, end) {
        if (!view || !view.count || order.length < 2) {
            fdRows = [];
            fdSig = null;
            fdAxis = null;
            fdStats = {};
            return;
        }
        const g = gridRows('fd-pp', fdSig);
        if (g) { fdRows = g.rows; fdSig = g.sig; }
        if (fdAxis === null) fdAxis = [];
        flushSync();
        for (const { name, cid, ti } of fdRows) {
            const color = colors[ti] || '#ccc';
            const d = prepPlayerFragDeathData(ev.killEvents, ev.deathEvents, name, start, end, color, dimColor(color));
            const cv = document.getElementById(cid);
            if (cv) cv.style.width = ''; // let the cell reflow on resize
            renderMiniDiverging(cv, { startTime: start, endTime: end, points: d.points, maxValue: d.max, height: miniH() });
        }
        fdStats = fragDeathLabels(ev.killEvents, ev.deathEvents, fdRows.map((r) => r.name), clock.t);
        fdAxis = measureAxis(fdTrack, start, end);
    }

    // One health / armor stack per player, on one shared scale.
    function drawHealthPerPlayer(start, end) {
        if (!view || !view.count || order.length < 2) {
            haRows = [];
            haSig = null;
            haAxis = null;
            return;
        }
        const g = gridRows('ha-pp', haSig);
        if (g) { haRows = g.rows; haSig = g.sig; }
        if (haAxis === null) haAxis = [];
        flushSync();
        const series = haRows.map(({ name }) => prepPlayerHAData(view, view.players[name], start, end));
        const maxValue = sharedStackMax(series);
        haRows.forEach(({ cid }, i) => {
            const cv = document.getElementById(cid);
            if (cv) cv.style.width = '';
            renderMiniStack(cv, { startTime: start, endTime: end, points: series[i] || [], maxValue, height: miniH() });
        });
        haAxis = measureAxis(haTrack, start, end);
    }

    // One row per player coloured by RL / LG / both held, drops as dots.
    function drawWeaponsPerPlayer(start, end) {
        // By id: the rows move between the <details> and their own panel.
        const labels = document.getElementById('weapons-per-player-labels');
        if (!labels) return;
        if (!view || !view.count || order.length < 2) { labels.innerHTML = ''; return; }
        const grouped = playersByTeam(view, order, teamOf, individual);
        const rows = [];
        const names = [];
        grouped.forEach((group, ti) => {
            for (const name of group) {
                rows.push({ name, color: colors[ti] || '#ccc', spans: prepPlayerWeaponSpans(view, view.players[name], start, end) });
                names.push(name);
            }
        });
        if (rows.length === 0) { labels.innerHTML = ''; return; }
        renderSpansTimeline('weapons-per-player-canvas', labels, {
            startTime: start, endTime: end, rows, rowH: spanRowH(),
            stateColors: WEAPON_SPAN_COLORS,
            dropMarks: playerDropMarks(ev.backpacks, names, start, end),
        });
    }

    // A drill-down opened: draw it at the current window, then seat its
    // freshly made current-time lines.
    function onToggle(draw) {
        return (e) => {
            if (!e.currentTarget.open || !shown()) return;
            draw(...range());
            sync();
        };
    }

    // The renderers pin a canvas's CSS width in pixels, which (with the flex
    // default min-width: auto) wedges its container open; clear it so the
    // container reports its true width. `shownOnly` leaves the canvases of
    // hidden panels, which the redraw will not size again, as they are.
    function unpinWidths(shownOnly) {
        for (const id of CANVAS_IDS) {
            const c = document.getElementById(id);
            if (c && !(shownOnly && c.offsetParent === null)) c.style.width = '';
        }
    }

    // The tab (or one of its panels) came into view: the window may have
    // changed size while it was hidden.
    function reveal() {
        if (!shown()) return;
        unpinWidths(true);
        redraw();
    }

    function onTab(tab) {
        if (tab !== TAB) return;
        // Run once src/app/tabs.js has made the tab visible (it follows the same
        // store, after this), then again on the next frame and 120 ms later.
        queueMicrotask(reveal);
        requestAnimationFrame(reveal);
        setTimeout(reveal, 120);
    }

    function onSegment() {
        if (shown()) redraw();
        else dirty = true;
    }

    function onClock({ from }) {
        if (from === 'reset') {
            clearTimeout(syncTimer);
            syncTimer = null;
            return;
        }
        if (!shown()) return;
        // The page's playback and the Player engine tick every
        // frame: follow them at 200 ms.
        if (from === 'playback' || from === 'engine') {
            if (!syncTimer) {
                syncTimer = setTimeout(() => {
                    syncTimer = null;
                    if (shown()) sync();
                }, 200);
            }
            return;
        }
        sync();
    }

    // A window resize re-measures every canvas.
    function onResize() {
        if (!shown() || !result || resizeRaf !== null) return;
        resizeRaf = requestAnimationFrame(() => {
            resizeRaf = null;
            unpinWidths(false);
            redraw();
        });
    }

    const dropTip = (canvas) => tooltip(canvas, { hitTest: (x, y) => hitDropMark(dropHit, x, y), html: dropTooltipHtml });
    const puTip = (canvas) => tooltip(canvas, { hitTest: (x, y) => hitPowerupSpan(puHit, x, y), html: powerupTooltipHtml });

    onMount(() => {
        const offs = [
            ia.installGlobals(),
            demo.subscribe(onDemo),
            teams.subscribe(invalidate),
            regionControl.subscribe((rc) => { if (rc?.stage !== 'resolved') invalidate(); }),
            segment.subscribe(onSegment),
            clock.subscribe(onClock),
            activeTab.subscribe(onTab),
            onPanelReveal(TAB, reveal),
        ];
        window.addEventListener('resize', onResize);
        return () => {
            offs.forEach((off) => off());
            window.removeEventListener('resize', onResize);
            clearTimeout(syncTimer);
        };
    });
</script>

{#snippet axisCell(ticks, bindTrack)}
    <div class="per-player-cell per-player-axis-cell">
        <div class="per-player-label"></div>
        <div class="per-player-axis-track" use:bindTrack>
            {#each ticks as tick (tick.t)}
                <span style:left="{tick.frac * 100}%">{tick.label}</span>
            {/each}
        </div>
    </div>
{/snippet}

{#snippet fragsGrid()}
    <div class="per-player-grid" id="frags-per-player">
        {#each fdRows as r (r.cid)}
            <div class="per-player-cell">
                <div class="per-player-label fd-label" style:color={r.color}>
                    <div class="fd-name" title={r.name}>{r.name}</div>
                    <div class="fd-stat" title={fdStats[r.name]?.title}>{fdStats[r.name]?.text ?? ''}</div>
                </div>
                <div class="per-player-canvas-wrap">
                    <canvas id={r.cid} class="timeline-canvas" use:ia.panZoom></canvas>
                    <div class="current-time-indicator pp-time-indicator" style:left={indLeft} use:ia.scrub></div>
                </div>
            </div>
        {/each}
        {#if fdAxis}{@render axisCell(fdAxis, (n) => { fdTrack = n; })}{/if}
    </div>
{/snippet}

{#snippet weaponsRows()}
    <div class="region-timeline-wrapper per-player-rows" id="weapons-per-player-rows">
        <div class="region-timeline-labels" id="weapons-per-player-labels"></div>
        <div class="region-timeline-outer">
            <canvas class="timeline-canvas" id="weapons-per-player-canvas" use:ia.panZoom></canvas>
            <div class="current-time-indicator pp-time-indicator" id="weapons-pp-time-indicator" style:left={indLeft} use:ia.scrub></div>
        </div>
    </div>
{/snippet}

{#snippet healthGrid()}
    <div class="per-player-grid" id="ha-per-player">
        {#each haRows as r (r.cid)}
            <div class="per-player-cell">
                <div class="per-player-label" title={r.name} style:color={r.color}>{r.name}</div>
                <div class="per-player-canvas-wrap">
                    <canvas id={r.cid} class="timeline-canvas" use:ia.panZoom></canvas>
                    <div class="current-time-indicator pp-time-indicator" style:left={indLeft} use:ia.scrub></div>
                </div>
            </div>
        {/each}
        {#if haAxis}{@render axisCell(haAxis, (n) => { haTrack = n; })}{/if}
    </div>
{/snippet}

{#snippet rangeSpan()}<span id="time-range-label">{rangeLabel}</span>{/snippet}

<div id="tab-timeline" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <!-- Team Status at Current Time -->
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions: the sortable headers inside, delegated -->
    <div data-panel="team-status" class="panel team-status-panel" onclick={onStatusClick}>
        <div class="team-status-row">
            <div class="team-status-side" id="team-status-a">{@html statusA}</div>
            <div class="team-status-side" id="team-status-b">{@html statusB}</div>
        </div>
    </div>

    <!-- The per-player views as panels of their own (the per-player layout) -->
    <div data-panel="frags-per-player" class="panel per-player-panel" id="frags-per-player-panel" style="display: none;" style:display={individual ? null : 'none'}>
        <h3>Frags / deaths per player</h3>
        <div class="per-player-panel-body" id="frags-per-player-host">{#if individual}{@render fragsGrid()}{/if}</div>
    </div>
    <div data-panel="weapons-per-player" class="panel per-player-panel" id="weapons-per-player-panel" style="display: none;" style:display={individual ? null : 'none'}>
        <h3 id="weapons-per-player-heading">Weapons per player{#if individual}{@render rangeSpan()}{/if}</h3>
        <div class="per-player-panel-body" id="weapons-per-player-host">{#if individual}{@render weaponsRows()}{/if}</div>
    </div>
    <div data-panel="ha-per-player" class="panel per-player-panel" id="ha-per-player-panel" style="display: none;" style:display={individual ? null : 'none'}>
        <h3>Health / armor per player</h3>
        <div class="per-player-panel-body" id="ha-per-player-host">{#if individual}{@render healthGrid()}{/if}</div>
    </div>

    <!-- Cumulative Frags Timeline -->
    <div data-panel="score" class="panel timeline-health-panel-full" id="score-timeline-panel">
        <h3>Score Timeline</h3>
        <div class="health-graph-wrapper">
            <div class="timeline-y-axis" id="score-y-axis">
                <span class="y-label y-top" id="score-y-top">{yScore[0]}</span>
                <span class="y-label y-mid">0</span>
                <span class="y-label y-bottom" id="score-y-bottom">{yScore[1]}</span>
            </div>
            <div class="health-graph-outer">
                <canvas class="timeline-canvas" id="score-canvas" bind:this={el.score} use:ia.panZoom></canvas>
                <div class="current-time-indicator" id="score-time-indicator" style:left={indLeft} use:ia.scrub></div>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span id="legend-score-team-a" style:color={scoreLegend.ca}>{scoreLegend.a}</span></span>
            <span class="legend-item"><span id="legend-score-team-b" style:color={scoreLegend.cb}>{scoreLegend.b}</span></span>
        </div>
        <details class="per-player-detail" id="frags-per-player-details" bind:open={fdOpen} ontoggle={onToggle(drawFragsPerPlayer)}>
            <summary>Per-player frags / deaths (efficiency)</summary>
            {#if !individual}{@render fragsGrid()}{/if}
        </details>
    </div>

    <!-- Powerups -->
    <div data-panel="powerups" class="panel timeline-region-panel" id="powerup-timeline-panel" style="display: none;" style:display={puShown ? null : 'none'}>
        <h3>Powerups</h3>
        <div class="region-timeline-wrapper">
            <div class="region-timeline-labels" id="powerup-timeline-labels" bind:this={el.powerupLabels}></div>
            <div class="region-timeline-outer">
                <canvas class="timeline-canvas" id="powerup-canvas" bind:this={el.powerup} use:ia.panZoom use:puTip></canvas>
                <div class="current-time-indicator" id="powerup-time-indicator" style:left={indLeft} use:ia.scrub></div>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span class="legend-color" id="pu-legend-a" style:background={puLegend.ca}></span> <span id="pu-tl-teamA">{puLegend.a}</span></span>
            <span class="legend-item"><span class="legend-color" id="pu-legend-b" style:background={puLegend.cb}></span> <span id="pu-tl-teamB">{puLegend.b}</span></span>
        </div>
    </div>

    <!-- Weapons (full width) -->
    <div data-panel="weapons" class="panel timeline-detail-graph-panel-full" id="weapons-timeline-panel">
        <h3 id="weapons-timeline-heading">Weapons{#if !individual}{@render rangeSpan()}{/if}</h3>
        <div class="detail-graph-wrapper">
            <div class="timeline-y-axis" id="detail-y-axis">
                <span class="y-label y-top">{yWeapons[0]}</span>
                <span class="y-label y-mid">0</span>
                <span class="y-label y-bottom">{yWeapons[1]}</span>
            </div>
            <div class="detail-graph-outer">
                <canvas class="timeline-canvas" id="detail-graph-canvas" bind:this={el.weapons} use:ia.panZoom use:dropTip></canvas>
                <div class="current-time-indicator" id="detail-time-indicator" style:left={indLeft} use:ia.scrub></div>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span id="legend-weapons-team-a">{sideLegend.a} ↑</span></span>
            <span class="legend-item"><span id="legend-weapons-team-b">{sideLegend.b} ↓</span></span>
            <span class="legend-item"><span class="legend-color rl"></span> RL</span>
            <span class="legend-item"><span class="legend-color lg"></span> LG</span>
            <span class="legend-item"><span class="legend-color rllg"></span> RL+LG</span>
            <span class="legend-item"><span class="legend-dot rl"></span> RL drop</span>
            <span class="legend-item"><span class="legend-dot lg"></span> LG drop</span>
        </div>
        <details class="per-player-detail" id="weapons-per-player-details" bind:open={wpOpen} ontoggle={onToggle(drawWeaponsPerPlayer)}>
            <summary>Per-player weapons</summary>
            {#if !individual}{@render weaponsRows()}{/if}
        </details>
    </div>

    <!-- Health/Armor (diverging style) -->
    <div data-panel="health-armor" class="panel timeline-health-panel-full" id="health-armor-timeline-panel">
        <h3>Team Health/Armor</h3>
        <div class="health-graph-wrapper">
            <div class="timeline-y-axis" id="health-y-axis">
                <span class="y-label y-top" id="health-y-top">{yHealth[0]}</span>
                <span class="y-label y-mid">0</span>
                <span class="y-label y-bottom" id="health-y-bottom">{yHealth[1]}</span>
            </div>
            <div class="health-graph-outer">
                <canvas class="timeline-canvas" id="health-armor-canvas" bind:this={el.health} use:ia.panZoom></canvas>
                <div class="current-time-indicator" id="health-time-indicator" style:left={indLeft} use:ia.scrub></div>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span id="legend-health-team-a">{sideLegend.a} ↑</span></span>
            <span class="legend-item"><span id="legend-health-team-b">{sideLegend.b} ↓</span></span>
            <span class="legend-item"><span class="legend-color health"></span> Health</span>
            <span class="legend-item"><span class="legend-color ra"></span> RA</span>
            <span class="legend-item"><span class="legend-color ya"></span> YA</span>
            <span class="legend-item"><span class="legend-color ga"></span> GA</span>
        </div>
        <details class="per-player-detail" id="ha-per-player-details" bind:open={haOpen} ontoggle={onToggle(drawHealthPerPlayer)}>
            <summary>Per-player health/armor</summary>
            {#if !individual}{@render healthGrid()}{/if}
        </details>
    </div>

    <!-- Region Control -->
    <div data-panel="region-control" class="panel timeline-region-panel" id="region-control-timeline-panel" style="display: none;" style:display={rcShown ? null : 'none'}>
        <h3>Region Control</h3>
        <div class="region-timeline-wrapper">
            <div class="region-timeline-labels" id="region-timeline-labels" bind:this={el.regionLabels}></div>
            <div class="region-timeline-outer">
                <canvas class="timeline-canvas" id="region-control-canvas" bind:this={el.region} use:ia.panZoom></canvas>
                <div class="current-time-indicator" id="region-time-indicator" style:left={indLeft} use:ia.scrub></div>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span class="legend-color" id="rc-legend-a-ctrl" style:background={rcLegend.ca}></span> <span id="rc-tl-teamA">{rcLegend.a}</span> ctrl</span>
            <span class="legend-item"><span class="legend-color" style="background: rgb(255, 255, 255);"></span> Contested</span>
            <span class="legend-item"><span class="legend-color" id="rc-legend-b-ctrl" style:background={rcLegend.cb}></span> <span id="rc-tl-teamB">{rcLegend.b}</span> ctrl</span>
        </div>
    </div>
</div>
