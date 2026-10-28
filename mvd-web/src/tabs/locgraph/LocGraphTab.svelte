<script>
    // The Locs & Regions tab (data-tab "loc-graph"; the URL slug is
    // "locs-regions"): Region Control (RegionControlPanel), the loc-analysis
    // Metric selector, the loc graph (a Cytoscape.js diagram,
    // loc-graph-view.js) and the Loc Heatmap. src/lib/locgraph.js has the
    // graph's elements, style and layouts; src/lib/loc-heatmap.js the two
    // matrices. src/app/tabs.js toggles the `active` class like on every other
    // .tab-content.
    //
    // The graph and the heatmap are built — once, the next time the tab shows
    // — after the demo or the teams changed; a new demo gets a fresh Cytoscape
    // instance. Each time the tab shows (now, on the next frame and 120 ms
    // later, like the other canvas tabs) and when one of its panels comes
    // back into view (onPanelReveal), the graph is resized and fitted. A
    // control change rebuilds at once: Filter / Edges / Min count / Layout
    // swap the element set and re-run the layout, Metric also rebuilds the
    // heatmap, Labels / Label size restyle. The heatmap is not rebuilt on a
    // tab switch, so a column sort survives it. Every panel carries
    // data-panel and sits directly in #tab-loc-graph for src/ui/panels.
    import { onMount, flushSync } from 'svelte';
    import { demo, teams, activeTab } from '../../state/index.js';
    import { isDuel } from '../../lib/result.js';
    import {
        locMetricAvailability, keptLocMetric, locFilterOptions, keptLocFilter, parseLocFilter,
        locGraphElements, locGraphStyle, locGraphNodeFill,
    } from '../../lib/locgraph.js';
    import { buildLocHeatmap } from '../../lib/loc-heatmap.js';
    import { onPanelReveal } from '../../ui/panels/reveal.js';
    import { LocGraphView } from './loc-graph-view.js';
    import { fillHeatmapTable, clearHeatmapTable } from './heatmap-table.js';
    import RegionControlPanel from './RegionControlPanel.svelte';

    const TAB = 'loc-graph';

    let result = null;
    let graph = null;
    // The Filter options, null until the first demo with a loc graph.
    let filterOpts = $state.raw(null);
    let filter = 'all';
    let filterSel = $state();
    let metric = 'all';
    let metricSel = $state();
    let avail = $state.raw({ all: true, armed: true, unarmed: true, quad: true, pent: true });
    let edgeMode = 'all';
    let minEdge = '1';
    let layout = 'preset';
    let showLabels = true;
    let labelSize = '14';
    let graphNoData = $state(false);
    let heatShown = $state(false);
    let heatNoData = $state(false);
    let canvas = $state();
    let heatTable = $state();
    let view = null;

    let dirty = true;
    let queued = false;
    let lastHandle = null;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== TAB) return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === TAB) build();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function onDemo(handle) {
        if (handle !== lastHandle) {
            lastHandle = handle;
            view?.destroy();
        }
        invalidate();
    }

    function build() {
        dirty = false;
        result = demo.get()?.result ?? null;
        if (!result) return;
        graph = result.locGraph || null;
        if (!graph || !graph.locs || graph.locs.length === 0) {
            graphNoData = true;
            view.destroy();
            renderHeatmap();
            return;
        }
        graphNoData = false;
        // A filter / metric pick survives a new demo when the demo has it;
        // the metrics it has no data for are hidden.
        const opts = locFilterOptions(result, isDuel(result));
        filter = keptLocFilter(filter, opts);
        filterOpts = opts;
        flushSync();
        filterSel.value = filter;
        avail = locMetricAvailability(graph);
        metric = keptLocMetric(metric, avail);
        metricSel.value = metric;
        refreshGraph();
        renderHeatmap();
    }

    function graphStyle() {
        const t = teams.get();
        return locGraphStyle({
            showLabels,
            labelSize: parseInt(labelSize, 10) || 14,
            nodeFill: locGraphNodeFill(parseLocFilter(filter), t.order, t.colors),
        });
    }

    function refreshGraph() {
        if (!graph) return;
        const elements = locGraphElements(graph, {
            filter: parseLocFilter(filter),
            metric,
            edgeMode,
            minEdge: parseInt(minEdge, 10) || 1,
        });
        view.refresh(elements, graphStyle(), layout);
    }

    function restyle() {
        view.setStyle(graphStyle());
    }

    // The heatmap for the metric. With a graph but nothing for this metric
    // the panel stays, saying so; without a graph it goes.
    function renderHeatmap() {
        const t = teams.get();
        const data = result ? buildLocHeatmap(result, metric, t.order, isDuel(result)) : null;
        if (!data) {
            // Clear any stale table from a previous metric so the empty-state
            // isn't shown alongside an outdated grid.
            clearHeatmapTable(heatTable);
            const hasGraph = !!(graph && graph.locs && graph.locs.length);
            heatShown = hasGraph;
            heatNoData = hasGraph;
            return;
        }
        heatShown = true;
        heatNoData = false;
        fillHeatmapTable(heatTable, data, 'Loc', t.colors);
    }

    // The tab (or one of its panels) came into view: build if due, else the
    // container may have changed size while hidden — resize and fit.
    function reveal() {
        if (activeTab.get() !== TAB) return;
        if (dirty) build();
        else view.reveal();
    }

    function onTab(tab) {
        if (tab !== TAB) return;
        // Run once src/app/tabs.js has made the tab visible (it follows the same
        // store, after this), then again on the next frame and 120 ms later:
        // a container drawn during the display:none → block switch can
        // measure zero.
        queueMicrotask(reveal);
        requestAnimationFrame(reveal);
        setTimeout(reveal, 120);
    }

    const sel = (set) => (e) => set(e.currentTarget.value);

    onMount(() => {
        view = new LocGraphView(canvas, () => parseInt(labelSize, 10) || 14);
        const offs = [
            demo.subscribe(onDemo),
            teams.subscribe(invalidate),
            activeTab.subscribe(onTab),
            onPanelReveal(TAB, reveal),
        ];
        return () => {
            offs.forEach((off) => off());
            view.destroy();
        };
    });
</script>

<div id="tab-loc-graph" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <RegionControlPanel />

    <!-- Metric selector — governs the loc graph + heatmap below
         (Region Control above is independent of it). -->
    <div data-panel="loc-metric" class="panel locmetric-panel">
        <h3>Loc Analysis Metric</h3>
        <div class="locmetric-row">
            <label class="locmetric-label">Metric:
                <select id="locgraph-metric" title="Weight the loc graph and heatmap by total time, time holding RL/LG (or not), or time with an active quad or pent"
                    bind:this={metricSel} onchange={(e) => { metric = e.currentTarget.value; refreshGraph(); renderHeatmap(); }}>
                    <option value="all" selected>Full time</option>
                    <option value="armed" hidden={!avail.armed} disabled={!avail.armed}>With RL / LG</option>
                    <option value="unarmed" hidden={!avail.unarmed} disabled={!avail.unarmed}>Without RL / LG</option>
                    <option value="quad" hidden={!avail.quad} disabled={!avail.quad}>With Quad</option>
                    <option value="pent" hidden={!avail.pent} disabled={!avail.pent}>With Pent</option>
                </select>
            </label>
            <span class="locmetric-hint">Applies to the loc graph and heatmap below.</span>
        </div>
    </div>

    <div data-panel="loc-graph" class="panel locgraph-panel">
        <h3>Loc Graph</h3>
        <div class="locgraph-controls">
            <label>Filter:
                <select id="locgraph-filter" bind:this={filterSel} onchange={sel((v) => { filter = v; refreshGraph(); })}>
                    {#if filterOpts}
                        {#each filterOpts as o (o.value)}
                            <option value={o.value}>{o.label}</option>
                        {/each}
                    {:else}
                        <option value="all" selected>All</option>
                    {/if}
                </select>
            </label>
            <label>Edges:
                <select id="locgraph-edge-mode" onchange={sel((v) => { edgeMode = v; refreshGraph(); })}>
                    <option value="all" selected>All</option>
                    <option value="normal">Normal only</option>
                    <option value="teleport">Teleports only</option>
                </select>
            </label>
            <label>Min count:
                <select id="locgraph-min-edge" title="Hide edges with fewer than N transitions" onchange={sel((v) => { minEdge = v; refreshGraph(); })}>
                    <option value="1" selected>1</option>
                    <option value="2">2</option>
                    <option value="3">3</option>
                    <option value="5">5</option>
                    <option value="10">10</option>
                </select>
            </label>
            <label>Layout:
                <select id="locgraph-layout" onchange={sel((v) => { layout = v; refreshGraph(); })}>
                    <option value="preset" selected>Geographic (loc coords)</option>
                    <option value="fcose">Force-directed (fcose)</option>
                    <option value="cose">Force-directed (cose)</option>
                    <option value="circle">Circle</option>
                    <option value="concentric">Concentric (by time)</option>
                </select>
            </label>
            <label><input type="checkbox" id="locgraph-show-labels" checked onchange={(e) => { showLabels = e.currentTarget.checked; restyle(); }}> Labels</label>
            <label>Label size:
                <select id="locgraph-label-size" onchange={sel((v) => { labelSize = v; restyle(); })}>
                    <option value="11">Small</option>
                    <option value="14" selected>Medium</option>
                    <option value="18">Large</option>
                    <option value="22">XL</option>
                </select>
            </label>
            <button id="locgraph-relayout" class="locgraph-btn" title="Re-run the current layout" onclick={() => view.runLayout(layout, true)}>Re-layout</button>
            <button id="locgraph-fit" class="locgraph-btn" title="Fit graph to viewport" onclick={() => view.fit()}>Fit</button>
        </div>
        <div class="locgraph-canvas-container">
            <div id="locgraph-canvas" bind:this={canvas}></div>
            <div id="locgraph-no-data" class="map-no-data" style="display:none" style:display={graphNoData ? 'block' : 'none'}>
                <p>No loc graph available for this demo.</p>
            </div>
        </div>
        <div class="timeline-legend">
            <span class="legend-item"><span class="legend-color" style="background:#8fb3ff;"></span> Normal edge</span>
            <span class="legend-item"><span class="legend-color" style="background:#00d9ff;"></span> Teleport edge</span>
            <span class="legend-item">Node size = occupancy · Edge width = transitions · both for the selected metric</span>
            <span class="legend-item">Drag nodes · scroll to zoom · click for details</span>
        </div>
    </div>

    <!-- Loc Heatmap -->
    <div data-panel="loc-heatmap" class="panel locheatmap-panel" id="locheatmap-panel" style="display: none;" style:display={heatShown ? null : 'none'}>
        <h3>Loc Heatmap</h3>
        <details class="panel-info">
            <summary>More info</summary>
            <div class="locheatmap-desc">
                Rows = locs, for the metric selected above. The first columns
                are the teams (all members combined), then one column per
                player grouped by team. Each cell shows the share of that
                column's <em>alive</em> time spent in the loc (time while dead is
                excluded), normalised to its own busiest
                loc; cell intensity uses the viridis scale and the team color
                underlines each header. Click a column header to sort; hover a
                cell for exact time.
            </div>
        </details>
        <div class="heatmap-legend">
            <span>less</span><span class="heatmap-legend-bar"></span><span>more</span>
        </div>
        <div class="heatmap-table-wrap">
            <table class="stats-table heatmap-table" id="locheatmap-table" bind:this={heatTable}>
                <thead><tr id="locheatmap-thead-row"></tr></thead>
                <tbody id="locheatmap-body"></tbody>
            </table>
        </div>
        <div id="locheatmap-no-data" class="map-no-data" style="display:none" style:display={heatNoData ? 'block' : 'none'}>
            <p>No loc data available for this demo.</p>
        </div>
    </div>
</div>
