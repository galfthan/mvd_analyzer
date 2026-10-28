// locgraph.js — the Locs & Regions tab's loc graph as data: which metrics a
// demo has, the filter options, the Cytoscape element set and stylesheet,
// the layout options and the hover tooltips. DOM-free; the Cytoscape
// instance itself is src/tabs/locgraph/loc-graph-view.js.
//
// Everything reads result.locGraph: loc nodes weighted by alive, observed
// time (int32 ms since schema v57), transition edges, and per-player /
// per-team breakdowns baked onto every node and edge.

import { escapeHtml } from './html.js';

export const EMPTY_WEIGHTS = { total: 0, byPlayer: {}, byTeam: {} };

// Show only the loc-analysis metrics this demo actually has data for —
// "With Quad"/"With Pent" disappear on maps/modes where nobody held that
// powerup (e.g. quad in most 1v1s), so the user can't land on an empty
// graph + table. A node carries the conditioned sub-object only when some
// sample met the condition (omitempty), so presence == availability.
export function locMetricAvailability(graph) {
    const avail = { all: true, armed: false, unarmed: false, quad: false, pent: false };
    const locs = (graph && graph.locs) || [];
    for (const n of locs) {
        if (n.armed) avail.armed = true;
        if (n.unarmed) avail.unarmed = true;
        if (n.quad) avail.quad = true;
        if (n.pent) avail.pent = true;
        if (avail.armed && avail.unarmed && avail.quad && avail.pent) break;
    }
    return avail;
}

// The metric to keep showing: the current pick if this demo has it, else
// "Full time".
export function keptLocMetric(current, avail) {
    return avail[current] ? current : 'all';
}

// The Filter options: All, then one per team (not in a duel — every team
// name is a player name there), then one per player.
export function locFilterOptions(result, duel) {
    const opts = [{ value: 'all', label: 'All' }];
    const teams = (result.demoInfo && result.demoInfo.teams) || [];
    const players = (result.demoInfo && result.demoInfo.players) || [];
    // Hide team options in duel mode (team name == player name for every player).
    if (!duel) {
        for (const t of teams) opts.push({ value: 'team:' + t, label: 'Team: ' + t });
    }
    for (const p of players) {
        if (!p.name) continue;
        opts.push({ value: 'player:' + p.name, label: p.name });
    }
    return opts;
}

// The filter to keep: the previous pick when this demo offers it, else All.
export function keptLocFilter(prev, opts) {
    return opts.some((o) => o.value === prev) ? prev : 'all';
}

export function parseLocFilter(value) {
    const val = value || 'all';
    if (val.startsWith('team:')) return { kind: 'team', key: val.slice(5) };
    if (val.startsWith('player:')) return { kind: 'player', key: val.slice(7) };
    return { kind: 'all', key: '' };
}

// Resolve a node's weight bundle for the chosen metric. 'all' is the node's
// own Total/ByPlayer/ByTeam; 'armed'/'quad' read the optional sub-objects
// (absent when no sample met the condition).
export function metricWeightsOf(node, metric) {
    if (metric === 'armed') return node.armed || EMPTY_WEIGHTS;
    if (metric === 'unarmed') return node.unarmed || EMPTY_WEIGHTS;
    if (metric === 'quad') return node.quad || EMPTY_WEIGHTS;
    if (metric === 'pent') return node.pent || EMPTY_WEIGHTS;
    return node;
}

export function nodeWeightFor(node, filter, metric) {
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

export function edgeWeightFor(edge, filter, metric) {
    const w = metricEdgeWeightsOf(edge, metric);
    if (filter.kind === 'player') return w.byPlayer?.[filter.key] || 0;
    if (filter.kind === 'team') return w.byTeam?.[filter.key] || 0;
    return w.total || 0;
}

// Build Cytoscape elements from the graph + filter + metric + edge mode +
// minimum edge count. Nodes/edges carry their filtered weight in data so
// styles can reference it directly instead of re-computing in the mapper.
export function locGraphElements(graph, { filter, metric, edgeMode, minEdge }) {
    if (!graph) return [];
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
                dim: w === 0 && (filter.kind !== 'all' || metric !== 'all'),
            },
            // Preset/geographic layout reads world coords directly from data.
            // Invert Y so "up" on screen matches "up" in map (QW Y is up).
            position: { x: n.x || 0, y: -(n.y || 0) },
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
                byTeam: ew.byTeam || {},
            },
        });
    }

    return elements;
}

// The node fill for a filter: a "Team: red" filter tints the nodes with
// that team's canonical colour (src/state/teams.js — order[i] owns
// colors[i]), a player filter is amber, "all" a neutral blue.
export function locGraphNodeFill(filter, teamOrder, teamColors) {
    if (filter.kind === 'team') {
        const idx = teamOrder.indexOf(filter.key);
        return (idx >= 0 && idx < teamColors.length) ? teamColors[idx] : '#8fb3ff';
    }
    if (filter.kind === 'player') return '#ffc107';
    return '#8fb3ff';
}

export function locGraphStyle({ showLabels, labelSize, nodeFill }) {
    return [
        {
            selector: 'node',
            style: {
                'background-color': nodeFill,
                'border-color': 'rgba(0, 217, 255, 0.6)',
                'border-width': 1.5,
                // sqrt so diameter scales with sqrt(time); "eye" area ≈ time.
                'width': 'mapData(weightNorm, 0, 1, 16, 60)',
                'height': 'mapData(weightNorm, 0, 1, 16, 60)',
                'label': showLabels ? 'data(name)' : '',
                'color': '#dfe6f5',
                'font-size': labelSize,
                'font-family': 'Inter, sans-serif',
                'font-weight': 500,
                'text-valign': 'bottom',
                'text-margin-y': Math.max(4, labelSize * 0.3),
                'text-outline-color': '#0a0a15',
                'text-outline-width': Math.max(2, labelSize * 0.18),
            },
        },
        {
            selector: 'node[?dim]',
            style: {
                'background-opacity': 0.25,
                'border-opacity': 0.2,
                'text-opacity': 0.35,
            },
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
                'opacity': 0.8,
            },
        },
        {
            selector: 'edge[kind = "teleport"]',
            style: {
                'line-color': '#00d9ff',
                'target-arrow-color': '#00d9ff',
                'line-style': 'dashed',
                'line-dash-pattern': [8, 4],
            },
        },
        {
            selector: ':selected',
            style: {
                'border-color': '#ffc107',
                'border-width': 3,
                'line-color': '#ffc107',
                'target-arrow-color': '#ffc107',
            },
        },
        {
            selector: '.highlight',
            style: {
                'opacity': 1,
                'z-index': 50,
            },
        },
        {
            selector: '.faded',
            style: {
                'opacity': 0.15,
            },
        },
    ];
}

// The layout to run. `animate` is false on filter changes (the common
// case) so layout runs instantly; the re-layout button passes true to get
// the animated effect.
export function locGraphLayout(name, animate) {
    if (name === 'preset') {
        return { name: 'preset', fit: true, padding: 30 };
    } else if (name === 'fcose') {
        return {
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
            padding: 30,
        };
    } else if (name === 'cose') {
        return { name: 'cose', animate: animate, padding: 30, fit: true };
    } else if (name === 'circle') {
        return { name: 'circle', animate: animate, padding: 30, fit: true };
    } else if (name === 'concentric') {
        return {
            name: 'concentric',
            animate: animate,
            padding: 30,
            fit: true,
            // Higher time spent = more central; Cytoscape expects larger
            // numbers to be more central.
            concentric: (node) => node.data('weight'),
            levelWidth: () => 1,
        };
    }
    return { name: 'preset' };
}

// Cytoscape font-size scales with zoom, so on wide maps (geographic
// preset) the fit-to-viewport zoom drops the effective pixel size below
// readability. Counter-act by recomputing font-size inversely proportional
// to the current zoom, clamped to a sensible range.
export function zoomedLabelStyle(userSize, zoom) {
    const target = Math.max(10, Math.min(48, userSize / (zoom || 1)));
    return {
        'font-size': target,
        'text-margin-y': Math.max(4, target * 0.3),
        'text-outline-width': Math.max(2, target * 0.18),
    };
}

// Tooltips, from a node's / edge's data(). Node time weights are int32 ms
// (schema v57); the UI shows seconds.
export function nodeTooltipHtml(d) {
    const total = (d.total || 0) / 1000;
    const byPlayer = d.byPlayer || {};
    const top = Object.entries(byPlayer).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const rows = top.map(([p, t]) => `<div>· ${escapeHtml(p)}: ${(t / 1000).toFixed(1)}s</div>`).join('');
    return `<div><strong>${escapeHtml(d.name)}</strong></div>
<div>Total time: ${total.toFixed(1)}s</div>
${rows}`;
}

export function edgeTooltipHtml(d) {
    const from = d.source.replace(/^n:/, '');
    const to = d.target.replace(/^n:/, '');
    const total = d.total || 0;
    const byPlayer = d.byPlayer || {};
    const top = Object.entries(byPlayer).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const rows = top.map(([p, c]) => `<div>· ${escapeHtml(p)}: ${c}</div>`).join('');
    return `<div><strong>${escapeHtml(from)} → ${escapeHtml(to)}</strong> (${d.kind})</div>
<div>Total transitions: ${total}</div>
${rows}`;
}
