import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
    locMetricAvailability, keptLocMetric, locFilterOptions, keptLocFilter, parseLocFilter, metricWeightsOf,
    nodeWeightFor, edgeWeightFor, locGraphElements, locGraphNodeFill, locGraphStyle, locGraphLayout,
    zoomedLabelStyle, nodeTooltipHtml, edgeTooltipHtml, EMPTY_WEIGHTS,
} from '../src/lib/locgraph.js';
import { buildLocHeatmap, buildRegionHeatmap, heatmapTableHtml, contrastInk, shortName, canonicalTeams } from '../src/lib/loc-heatmap.js';
import {
    regionRowsFromRegions, readRegionDefs, resolveRegions, locToRegionOf, regionToGroupsOf, regionOverrideJSON,
    regionFileJSON, regionFileName, parseRegionFile,
} from '../src/lib/regions.js';
import { regionControl, resetRegionControl, applyRegionDefs, reapplyRegions } from '../src/state/regions.js';
import { isDuel } from '../src/lib/result.js';

const golden = (name) => JSON.parse(readFileSync(new URL(`../../mvd-analytics/testdata/golden/${name}.json`, import.meta.url)));
const TEAM2 = golden('2on2_nani_pora_210426_dm6');
const DUEL = golden('1on1_bananfalco_betowen_240426_dm2');
const COLORS = ['#ff5050', '#50a0ff', '#4ecdc4', '#ffc107'];
const ALL = { kind: 'all', key: '' };

test('metric availability and fallback', () => {
    const avail = locMetricAvailability(TEAM2.locGraph);
    assert.equal(avail.all, true);
    assert.equal(avail.armed, true);
    assert.equal(avail.unarmed, true);
    assert.equal(avail.quad, TEAM2.locGraph.locs.some((n) => n.quad));
    assert.deepEqual(locMetricAvailability(null), { all: true, armed: false, unarmed: false, quad: false, pent: false });
    assert.equal(keptLocMetric('pent', { all: true, pent: false }), 'all');
    assert.equal(keptLocMetric('armed', { all: true, armed: true }), 'armed');
});

test('filter options: teams except in a duel, then players', () => {
    const opts = locFilterOptions(TEAM2, isDuel(TEAM2));
    assert.equal(opts[0].value, 'all');
    assert.deepEqual(opts.filter((o) => o.value.startsWith('team:')).map((o) => o.label),
        TEAM2.demoInfo.teams.map((t) => 'Team: ' + t));
    assert.equal(opts.filter((o) => o.value.startsWith('player:')).length, TEAM2.demoInfo.players.filter((p) => p.name).length);
    assert.equal(isDuel(DUEL), true);
    assert.equal(isDuel(TEAM2), false);
    assert.equal(isDuel({}), false);
    assert.ok(!locFilterOptions(DUEL, true).some((o) => o.value.startsWith('team:')));
    assert.equal(keptLocFilter('player:nobody', opts), 'all');
    assert.equal(keptLocFilter(opts[1].value, opts), opts[1].value);
    assert.deepEqual(parseLocFilter('team:a:b'), { kind: 'team', key: 'a:b' });
    assert.deepEqual(parseLocFilter('player:x'), { kind: 'player', key: 'x' });
    assert.deepEqual(parseLocFilter(''), ALL);
});

test('weights per metric and filter', () => {
    const n = TEAM2.locGraph.locs[0];
    assert.equal(metricWeightsOf(n, 'all'), n);
    assert.equal(metricWeightsOf(n, 'armed'), n.armed);
    assert.equal(metricWeightsOf({}, 'pent'), EMPTY_WEIGHTS);
    assert.equal(nodeWeightFor(n, ALL, 'all'), n.total);
    assert.equal(nodeWeightFor(n, { kind: 'player', key: 'gLAd' }, 'armed'), n.armed.byPlayer.gLAd);
    assert.equal(nodeWeightFor(n, { kind: 'team', key: 'nope' }, 'all'), 0);
    const e = TEAM2.locGraph.edges[0];
    assert.equal(edgeWeightFor(e, { kind: 'team', key: 'pora' }, 'unarmed'), e.unarmed.byTeam.pora || 0);
});

test('elements: every node, edges pruned by mode / count / filter, norms', () => {
    const g = TEAM2.locGraph;
    const els = locGraphElements(g, { filter: ALL, metric: 'all', edgeMode: 'all', minEdge: 1 });
    const nodes = els.filter((x) => x.group === 'nodes');
    const edges = els.filter((x) => x.group === 'edges');
    assert.equal(nodes.length, g.locs.length);
    assert.equal(edges.length, g.edges.filter((e) => e.total > 0).length);
    assert.equal(Math.max(...nodes.map((n) => n.data.weightNorm)), 1);
    assert.deepEqual(nodes[0].position, { x: g.locs[0].x, y: -g.locs[0].y });
    assert.ok(nodes.every((n) => n.data.dim === false));
    assert.equal(edges[0].data.id, `e:${g.edges[0].from}->${g.edges[0].to}`);
    const tele = locGraphElements(g, { filter: ALL, metric: 'all', edgeMode: 'teleport', minEdge: 1 }).filter((x) => x.group === 'edges');
    assert.ok(tele.every((e) => e.data.kind === 'teleport'));
    const min5 = locGraphElements(g, { filter: ALL, metric: 'all', edgeMode: 'all', minEdge: 5 }).filter((x) => x.group === 'edges');
    assert.ok(min5.every((e) => e.data.weight >= 5) && min5.length < edges.length);
    const pl = locGraphElements(g, { filter: { kind: 'player', key: 'SS' }, metric: 'armed', edgeMode: 'all', minEdge: 1 });
    const pn = pl.filter((x) => x.group === 'nodes');
    assert.ok(pn.some((n) => n.data.dim) || pn.every((n) => n.data.weight > 0));
    assert.ok(pn.filter((n) => n.data.dim).every((n) => n.data.weight === 0));
    assert.deepEqual(locGraphElements(null, {}), []);
});

test('style, fill, layouts, label zoom, tooltips', () => {
    assert.equal(locGraphNodeFill(ALL, [], COLORS), '#8fb3ff');
    assert.equal(locGraphNodeFill({ kind: 'player', key: 'x' }, [], COLORS), '#ffc107');
    assert.equal(locGraphNodeFill({ kind: 'team', key: 'b' }, ['a', 'b'], COLORS), COLORS[1]);
    assert.equal(locGraphNodeFill({ kind: 'team', key: 'z' }, ['a', 'b'], COLORS), '#8fb3ff');
    const st = locGraphStyle({ showLabels: false, labelSize: 22, nodeFill: '#123456' });
    assert.equal(st[0].style.label, '');
    assert.equal(st[0].style['background-color'], '#123456');
    assert.equal(st[0].style['text-margin-y'], 22 * 0.3);
    assert.equal(locGraphStyle({ showLabels: true, labelSize: 14, nodeFill: '' })[0].style.label, 'data(name)');
    assert.deepEqual(locGraphLayout('preset', true), { name: 'preset', fit: true, padding: 30 });
    assert.equal(locGraphLayout('fcose', true).animate, true);
    assert.equal(locGraphLayout('concentric', false).concentric({ data: () => 7 }), 7);
    assert.deepEqual(locGraphLayout('bogus', false), { name: 'preset' });
    assert.equal(zoomedLabelStyle(14, 0.1)['font-size'], 48);
    assert.equal(zoomedLabelStyle(14, 2)['font-size'], 10);
    assert.equal(zoomedLabelStyle(14, 0)['font-size'], 14);
    const nt = nodeTooltipHtml({ name: 'R<A>', total: 1500, byPlayer: { a: 1000, b: 500 } });
    assert.match(nt, /<strong>R&lt;A&gt;<\/strong>/);
    assert.match(nt, /Total time: 1\.5s/);
    assert.ok(nt.indexOf('a: 1.0s') < nt.indexOf('b: 0.5s'));
    const et = edgeTooltipHtml({ source: 'n:x', target: 'n:y', kind: 'teleport', total: 3, byPlayer: { p: 3 } });
    assert.match(et, /x → y<\/strong> \(teleport\)/);
    assert.match(et, /Total transitions: 3/);
});

test('loc heatmap: team columns then players, per-column normalisation', () => {
    const order = TEAM2.demoInfo.teams;
    const h = buildLocHeatmap(TEAM2, 'all', order, false);
    assert.equal(h.teamCols, 2);
    assert.deepEqual(h.columns.slice(0, 2).map((c) => c.kind), ['team', 'team']);
    assert.ok(h.columns.slice(2).every((c) => c.kind === 'player'));
    for (let ci = 0; ci < h.columns.length; ci++) {
        assert.equal(Math.max(...h.rows.map((r) => r.cells[ci].i)), 1);
    }
    const totals = h.rows.map((r) => TEAM2.locGraph.locs.find((n) => n.name === r.name).total);
    assert.deepEqual(totals, [...totals].sort((a, b) => b - a));
    assert.match(h.cellTitle(h.columns[0], h.rows[0], 0), /of team time$/);
    const duel = buildLocHeatmap(DUEL, 'all', [], true);
    assert.equal(duel.teamCols, 0);
    assert.equal(buildLocHeatmap({ ...TEAM2, locGraph: { locs: TEAM2.locGraph.locs.map(({ pent, ...n }) => n) } }, 'pent', order, false), null);
    assert.equal(buildLocHeatmap({ locGraph: { locs: [] } }, 'all', [], false), null);
    assert.deepEqual(canonicalTeams({ demoInfo: { teams: ['x'] } }, []), ['x']);
    assert.deepEqual(canonicalTeams({}, []), ['']);
    assert.deepEqual(canonicalTeams({}, ['b', 'a']), ['b', 'a']);
    assert.equal(shortName('abcdefghijk'), 'abcdefgh…');
    assert.equal(shortName('abc'), 'abc');
    assert.equal(contrastInk([253, 231, 37]), '#10131f');
    assert.equal(contrastInk([68, 1, 84]), '#f0f0f0');
    const html = heatmapTableHtml(h, 'Loc', COLORS);
    assert.match(html.head, /^<th>Loc<\/th><th class="heatmap-col heatmap-col-team" style="border-bottom: 3px solid /);
    assert.match(html.head, /heatmap-col heatmap-col-sep/);
    assert.equal(html.rows.length, h.rows.length);
    assert.match(html.rows[0], /^<td class="heatmap-rowname"><strong>/);
});

test('region heatmap: per-row normalisation, Empty unshaded, team names', () => {
    const rc = TEAM2.timelineAnalysis.regionControl;
    const h = buildRegionHeatmap(rc.regions, rc.stats, rc.teamA, rc.teamB);
    assert.equal(h.columns[0].label, rc.teamA);
    assert.equal(h.columns[6].label, rc.teamB);
    assert.equal(h.rows.length, rc.regions.filter((r) => rc.stats[r.name]).length);
    for (const r of h.rows) {
        assert.equal(r.cells[4].i, 0);
        assert.ok(r.cells.every((c) => c.i <= 1));
    }
    assert.equal(buildRegionHeatmap(rc.regions, rc.stats, null, '').columns[0].label, 'Team A');
    assert.equal(buildRegionHeatmap(rc.regions, {}, 'a', 'b'), null);
    assert.equal(buildRegionHeatmap([], rc.stats, 'a', 'b'), null);
    assert.equal(buildRegionHeatmap(rc.regions, null, 'a', 'b'), null);
    const html = heatmapTableHtml(h, 'Region', COLORS);
    assert.match(html.head, new RegExp(`border-bottom: 3px solid ${COLORS[0]}`));
    assert.match(html.rows[0], /% of match"/);
});

test('region definitions: rows, read, resolve, lookups, file', () => {
    const rc = TEAM2.timelineAnalysis.regionControl;
    const locs = TEAM2.timelineAnalysis.locationData;
    const rows = regionRowsFromRegions(rc.regions);
    assert.equal(rows.length, rc.regions.length);
    const defs = readRegionDefs([...rows, { name: '  ', locs: 'a' }, { name: 'x', locs: ' , ' }]);
    assert.equal(defs.length, rc.regions.length);
    const back = resolveRegions(defs, locs);
    assert.deepEqual(back.map((r) => r.name), rc.regions.map((r) => r.name));
    // Go's point order is its own; the loc set is the same.
    const names = (rs) => rs.map((r) => [...new Set(r.points.map((p) => p.name))].sort());
    assert.deepEqual(names(back), names(rc.regions));
    const loc0 = locs[0].name;
    const r = resolveRegions([{ name: 'a', locs: [loc0.toLowerCase(), 'nosuch'] }, { name: 'a', locs: [locs[1].name] }, { name: 'b', locs: ['nosuch'] }], locs);
    assert.equal(r.length, 1);
    assert.equal(r[0].points.length, locs.filter((l) => l.name.toLowerCase() === loc0.toLowerCase()).length);
    assert.equal(r[0].centroidX, r[0].points.reduce((s, p) => s + p.x, 0) / r[0].points.length);
    assert.equal(locToRegionOf(r)[loc0], 'a');
    const groups = [{ name: 'g', points: [{ x: r[0].points[0].x + 0.5, y: r[0].points[0].y }] }, { name: 'h', points: [{ x: 1e6, y: 0 }] }];
    assert.deepEqual(Object.keys(regionToGroupsOf(r, groups)), ['a']);
    assert.equal(regionToGroupsOf(r, groups).a[0].name, 'g');
    assert.deepEqual(regionToGroupsOf(r, null), {});
    assert.deepEqual(JSON.parse(regionOverrideJSON(r)), { regions: [{ name: 'a', locs: r[0].points.map((p) => p.name) }] });
    assert.equal(regionFileJSON([{ name: 'a', locs: ['x'] }]), '{\n    "regions": [\n        {\n            "name": "a",\n            "locs": [\n                "x"\n            ]\n        }\n    ]\n}\n');
    assert.equal(regionFileName('DM6!'), '6.json');
    assert.equal(regionFileName('dm6'), 'dm6.json');
    assert.equal(regionFileName(''), 'regions.json');
    assert.match(parseRegionFile('{x').error, /^Failed to parse region JSON: /);
    assert.equal(parseRegionFile('{"regs":[]}').error, 'Region JSON must have a top-level "regions" array.');
    assert.deepEqual(parseRegionFile('{"regions":[{"name":" A ","locs":["x",2]},{"locs":["y"]},{"name":"B"}]}').rows,
        [{ name: 'A', locs: 'x, 2' }, { name: 'B', locs: '' }]);
});

test('region model: reset, apply in two steps, failure, re-apply', async () => {
    const rc = TEAM2.timelineAnalysis.regionControl;
    const seen = [];
    const off = regionControl.subscribe((v) => seen.push(v && v.stage));
    resetRegionControl({ timelineAnalysis: { regionControl: { regions: [] } } }, true);
    assert.equal(regionControl.get(), null);
    resetRegionControl(TEAM2, true);
    const m = regionControl.get();
    assert.equal(m.stage, 'loaded');
    assert.equal(m.regions, rc.regions);
    assert.equal(m.stats, rc.stats);
    assert.equal(m.defs.length, rc.regions.length);
    resetRegionControl(TEAM2, false);
    assert.deepEqual(regionControl.get().defs, []);
    resetRegionControl(TEAM2, true);

    const sent = [];
    const handle = { recomputeRegions: async (json) => { sent.push(JSON.parse(json)); return JSON.stringify({ stats: { x: 1 }, bucketStates: { x: 'A' }, teamA: 'p', teamB: '' }); } };
    const def = { name: 'one', locs: [rc.regions[0].points[0].name] };
    await applyRegionDefs(handle, [def]);
    const c = regionControl.get();
    assert.equal(c.stage, 'computed');
    assert.deepEqual(c.defs, [def]);
    assert.equal(c.regions[0].name, 'one');
    assert.deepEqual(c.stats, { x: 1 });
    assert.equal(c.teamA, 'p');
    assert.equal(c.teamB, '');
    assert.deepEqual(sent[0].regions.map((r) => r.name), ['one']);

    const warn = console.warn;
    const warned = [];
    console.warn = (...a) => warned.push(a.join(' '));
    try {
        await applyRegionDefs({ recomputeRegions: async () => JSON.stringify({ error: 'boom' }) }, []);
        assert.equal(regionControl.get().stage, 'resolved');
        assert.deepEqual(regionControl.get().regions, []);
        assert.deepEqual(regionControl.get().stats, { x: 1 });
        await applyRegionDefs({ recomputeRegions: async () => { throw new Error('gone'); } }, []);
    } finally {
        console.warn = warn;
    }
    assert.deepEqual(warned, ['recomputeRegionControl: boom', 'recomputeRegionControl: gone']);

    await applyRegionDefs(handle, [def]);
    await reapplyRegions(handle);
    assert.deepEqual(sent.at(-1).regions.map((r) => r.name), ['one']);
    regionControl.set(null);
    assert.equal(await reapplyRegions(handle), undefined);
    await applyRegionDefs(handle, [def]);
    assert.equal(regionControl.get(), null);
    off();
    assert.deepEqual(seen.slice(0, 6), [null, null, 'loaded', 'loaded', 'loaded', 'resolved']);
});
