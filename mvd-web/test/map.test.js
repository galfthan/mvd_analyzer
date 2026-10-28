import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
    mapTeams, assignPlayerSymbols, mapBounds, dropEventsOf, posStreamsOf, mapCanvasSize, MAP_CANVAS_BASE_WIDTH,
    legendTeams, legendRowHtml, teamFragsAt, stackCell, weaponsText, regionStateLabel, regionStatusRows,
    panelItems, itemStatusCell, entityRows, entityRowsHtml, trailButtons,
} from '../src/lib/map.js';
import {
    barAxisTicks, barPct, barTime, dragSpan, isClick, overlayBox, segmentLabel, speedButtons, SPEED_BUTTONS,
} from '../src/lib/playback-bar.js';
import { ITEM_MARKER_STYLES } from '../../mvd-map-view/src/map.js';

const golden = (name) => JSON.parse(readFileSync(new URL(`../../mvd-analytics/testdata/golden/${name}.json`, import.meta.url)));
const TEAM2 = golden('2on2_nani_pora_210426_dm6');
const FFA = golden('ffa_countdown_dm6_260106');

test('mapTeams: the canonical order when it has two sides, else the demo\'s', () => {
    assert.deepEqual(mapTeams(['b', 'a'], TEAM2), ['b', 'a']);
    const own = mapTeams([], TEAM2);
    assert.ok(own.length >= 2);
    assert.deepEqual(mapTeams([], { match: { teams: [{ name: 'x' }, { name: 'y' }] } }), ['x', 'y']);
    assert.deepEqual(mapTeams(['solo'], null), []);
});

test('assignPlayerSymbols: first free letter per player, team order, side index', () => {
    const result = { demoInfo: { players: [
        { name: 'Anna', team: 'red' }, { name: 'adam', team: 'red' }, { name: 'Bob', team: 'blue' }, { name: '123', team: 'blue' },
        { name: 'ghost', team: 'spec' },
    ] } };
    const sym = assignPlayerSymbols(result, ['blue', 'red']);
    assert.deepEqual(sym.Bob, { symbol: 'B', team: 'blue', teamIdx: 0 });
    assert.deepEqual(sym['123'], { symbol: '1', team: 'blue', teamIdx: 0 }, 'no letter: its first character');
    assert.equal(sym.Anna.symbol, 'A');
    assert.equal(sym.adam.symbol, 'D', 'A taken: the next letter of the name');
    assert.equal(sym.Anna.teamIdx, 1);
    assert.ok(!('ghost' in sym), 'a team outside the order gets none');
    // Without a demoinfo roster: playerStats.
    const ffa = assignPlayerSymbols(FFA, mapTeams([], FFA).length ? mapTeams([], FFA) : FFA.playerStats.players.map((p) => p.team));
    assert.ok(Object.keys(ffa).length > 0);
});

test('mapBounds: locs and living positions, padded; a default box when empty', () => {
    const view = { players: { a: { n: 3, x: [0, 100, 50], y: [0, 300, 20], alive: [1, 1, 0] } } };
    const b = mapBounds([{ x: -100, y: 0 }], view);
    assert.deepEqual(b, { minX: -120, maxX: 120, minY: -30, maxY: 330 });
    assert.deepEqual(mapBounds([], null), { minX: -1200, maxX: 1200, minY: -1200, maxY: 1200 });
});

test('drops in seconds; position streams with samples only', () => {
    assert.deepEqual(dropEventsOf({ backpacks: [{ time: 1500, origin: [1, 2, 3], weapon: 'rl' }, { time: 0, weapon: 'lg' }] }), [
        { t: 1.5, wx: 1, wy: 2, wz: 3, weapon: 'rl' }, { t: 0, wx: 0, wy: 0, wz: 0, weapon: 'lg' },
    ]);
    const pos = { t: [1] };
    assert.deepEqual(posStreamsOf({ streams: { players: [{ name: 'a', pos }, { name: 'b', pos: { t: [] } }, null] } }), { a: pos });
    assert.deepEqual(posStreamsOf(null), {});
});

test('mapCanvasSize: fixed width at the world aspect, or the fullscreen box', () => {
    assert.deepEqual(mapCanvasSize(false, null, { minX: 0, maxX: 1000, minY: 0, maxY: 500 }), [MAP_CANVAS_BASE_WIDTH, 425]);
    assert.deepEqual(mapCanvasSize(false, null, { minX: 0, maxX: 1000, minY: 0, maxY: 100 }), [850, 400]);
    assert.deepEqual(mapCanvasSize(false, null, { minX: 0, maxX: 0, minY: 0, maxY: 0 }), [850, 700]);
    assert.deepEqual(mapCanvasSize(true, { width: 1200.7, height: 150 }, null), [1200, 200]);
});

test('legend: teams in colour order, players by name, cells', () => {
    const sym = { zed: { symbol: 'Z', team: 'a', teamIdx: 0 }, amy: { symbol: 'A', team: 'a', teamIdx: 0 }, bo: { symbol: 'B', team: 'b', teamIdx: 1 } };
    const blocks = legendTeams(['a', 'b'], sym, ['#1', '#2']);
    assert.deepEqual(blocks.map((b) => [b.team, b.color, b.players.map((p) => p.name)]), [['a', '#1', ['amy', 'zed']], ['b', '#2', ['bo']]]);
    assert.match(legendRowHtml('<x>', 'X', '#1'), /data-player="&lt;x&gt;"/);
    assert.equal(teamFragsAt('a', sym, { zed: 3, amy: 2, bo: 9 }), 5);
    assert.deepEqual(stackCell(null), { sort: '0', html: null });
    assert.deepEqual(stackCell({ h: 0, a: 50 }), { sort: '50', html: null });
    assert.equal(stackCell({ h: 100, a: 150, at: 'ra' }).html, '<span class="stack-h">100</span> <span class="armor-ra">150 RA</span>');
    assert.equal(stackCell({ h: 100, a: 20 }).html, '<span class="stack-h">100</span> <span>20</span>');
    assert.equal(stackCell({ h: 70 }).sort, '70');
    assert.equal(weaponsText({ rl: 1, lg: 1 }), 'RL LG');
    assert.equal(weaponsText({}), '-');
    assert.equal(weaponsText(null), '-');
});

test('region status: labels and the players in each region', () => {
    const colors = ['#a', '#b'];
    assert.deepEqual(regionStateLabel('teamAWeakControl', ['red'], colors, [0, 1]), ['red (weak)', '#a']);
    assert.deepEqual(regionStateLabel('teamBControl', [], colors, [0, 1]), ['B', '#b']);
    assert.deepEqual(regionStateLabel(undefined, [], colors, [0, 1]), ['Empty', '#555']);
    // Go's side A is canonical index 1: its name and colour are index 1's.
    assert.deepEqual(regionStateLabel('teamAControl', ['blue', 'red'], colors, [1, 0]), ['red', '#b']);
    assert.deepEqual(regionStateLabel('teamBWeakControl', ['blue', 'red'], colors, [1, 0]), ['blue (weak)', '#a']);
    const rows = regionStatusRows({
        regions: [{ name: 'RA' }, { name: 'YA' }],
        states: { RA: 'contested' },
        playerData: {
            p: { x: 1, y: 1, h: 100, loc: 'ra' }, q: { x: 1, y: 1, h: 100, loc: 'ra' },
            dead: { x: 1, y: 1, h: 0, loc: 'ra' }, nowhere: { x: 0, y: 0, loc: 'ra' },
        },
        locOf: (d) => d.loc, locToRegion: { ra: 'RA' },
        symbols: { p: { teamIdx: 1 }, q: { teamIdx: 0 } }, teams: ['red', 'blue'], colors, sides: [0, 1],
    });
    assert.deepEqual(rows.map((r) => [r.name, r.label, r.players.map((p) => p.name)]), [['RA', 'Contested', ['q', 'p']], ['YA', 'Empty', []]]);
});

test('items list: the contested kinds in display order; status cells', () => {
    const items = [{ kind: 'rl', name: 'b' }, { kind: 'shells', name: 'a' }, { kind: 'ra', name: 'z' }, { kind: 'rl', name: 'a' }];
    assert.deepEqual(panelItems(items, ITEM_MARKER_STYLES).map((i) => i.kind + i.name), ['raz', 'rla', 'rlb']);
    assert.deepEqual(panelItems(null, ITEM_MARKER_STYLES), []);
    assert.deepEqual(itemStatusCell({ up: true }), { text: 'up', cls: 'item-status up' });
    assert.deepEqual(itemStatusCell({ up: false, pending: true }), { text: 'held', cls: 'item-status pending' });
    assert.deepEqual(itemStatusCell({ up: false, secsToRespawn: 3.25 }), { text: '3.3s', cls: 'item-status respawn' });
});

test('entity table: shown categories, teleporters entrance → exit', () => {
    const ents = [
        { type: 'item', kind: 'rl', name: 'weapon_rocketlauncher', loc: 'rl' },
        { type: 'spawn', name: 'info_player_deathmatch', loc: 'x' },
        { type: 'teleportSrc', name: 'tele', target: 't1', loc: 'in' },
        { type: 'teleportDst', name: 'dest', targetName: 't1', loc: 'out' },
        { type: 'teleportDst', name: 'lone', targetName: 't2', loc: 'far' },
    ];
    const cat = (e) => e.type === 'item' ? 'weapon' : e.type;
    const rows = entityRows(ents, { weapon: true, spawn: false, teleporter: true }, cat);
    assert.deepEqual(rows.map((r) => [r.cls, r.type, r.name, r.loc, r.source]), [
        ['Teleporter', '', 'lone', '', 'far'],
        ['Teleporter', '', 'tele', 'in', 'out'],
        ['Weapon', 'rl', 'weapon_rocketlauncher', 'rl', ''],
    ]);
    assert.equal(entityRows(ents, { weapon: false, teleporter: false }, cat).length, 0);
    assert.equal(entityRowsHtml([{ cls: 'A', type: '<', name: '', loc: '', source: '' }]), '<tr><td>A</td><td>&lt;</td><td></td><td></td><td></td></tr>');
});

test('trail buttons', () => {
    assert.deepEqual(trailButtons([], {}), { all: false, none: true, some: false });
    assert.deepEqual(trailButtons(['a', 'b'], { a: true }), { all: false, none: false, some: true });
    assert.deepEqual(trailButtons(['a', 'b'], { a: true, b: true }), { all: true, none: false, some: false });
});

test('playback bar: axis, cursor, pointer time, drag, overlay, buttons', () => {
    assert.deepEqual(barAxisTicks(0), []);
    const ticks = barAxisTicks(1200);
    assert.equal(ticks.length, 11);
    assert.deepEqual(ticks[10], { pct: 100, label: '20:00' });
    assert.equal(barAxisTicks(120).length, 5, 'at least four intervals');
    assert.equal(barPct(50, 200), 25);
    assert.equal(barPct(500, 200), 100);
    assert.equal(barPct(5, 0), null);
    assert.equal(barTime(150, { left: 100, width: 200 }, 600), 150);
    assert.equal(barTime(50, { left: 100, width: 200 }, 600), 0);
    assert.equal(barTime(50, { left: 100, width: 0 }, 600), null);
    assert.deepEqual(dragSpan(30, 10), { start: 10, end: 30 });
    assert.ok(isClick({ start: 10, end: 12 }));
    assert.ok(!isClick({ start: 10, end: 12.5 }));
    assert.deepEqual(overlayBox({ start: 30, end: 90 }, 600), { left: '5%', width: '10%' });
    assert.equal(overlayBox({ start: 30, end: 90 }, 0), null);
    assert.equal(segmentLabel({ start: 65, end: 130 }), '1:05 – 2:10');
    assert.equal(segmentLabel(null), '');
    const own = speedButtons({ drives: false, engine: {}, playing: true, speed: 5 });
    assert.deepEqual(own.map((b) => [b.id, b.text, b.active, b.disabled]), [
        ['tl-rev', '-1x', false, false], ['tl-slow', '0.2x', false, false], ['tl-play-pause', '1x', false, false], ['tl-5x', '⏸', true, false],
    ]);
    const eng = speedButtons({ drives: true, engine: { playing: true, speed: 100 }, playing: true, speed: 5 });
    assert.deepEqual(eng.map((b) => [b.text, b.disabled]), [['-1x', true], ['0.2x', false], ['⏸', false], ['5x', false]]);
    assert.equal(SPEED_BUTTONS.length, 4);
});

test('playback: the loop moves the clock, toggles, wraps, stops at 0 and on a tab', async () => {
    // A hand-cranked frame clock.
    let frames = [];
    let now = 0;
    globalThis.requestAnimationFrame = (fn) => { frames.push(fn); return frames.length; };
    globalThis.cancelAnimationFrame = () => { frames = []; };
    const realNow = performance.now;
    performance.now = () => now;
    const crank = (ms) => { now += ms; const f = frames; frames = []; f.forEach((fn) => fn()); };
    try {
        const { clock, activeTab, playback, playAtSpeed, stopPlayback } = await import('../src/state/index.js');
        const seen = [];
        const off = clock.subscribe(({ from }) => seen.push(from));
        clock.setDuration(100);
        activeTab.set('map');
        clock.seek(10);
        playAtSpeed(5);
        crank(10); // under a frame: skipped
        assert.equal(clock.t, 10);
        crank(190);
        assert.ok(Math.abs(clock.t - 11) < 1e-9, `5x over 0.2 s (${clock.t})`);
        assert.equal(seen.at(-1), 'playback');
        playAtSpeed(1); // a speed change keeps playing
        crank(1000);
        assert.ok(Math.abs(clock.t - 12) < 1e-9);
        playAtSpeed(1); // the running speed again: pause, with an untagged seek
        assert.deepEqual(playback.get(), { playing: false, speed: 1 });
        assert.equal(seen.at(-1), null);
        crank(1000);
        assert.ok(Math.abs(clock.t - 12) < 1e-9, 'paused');
        clock.seek(99.9);
        playAtSpeed(1);
        crank(200);
        assert.equal(clock.t, 0, 'past the end: back to the start');
        assert.ok(playback.get().playing);
        clock.seek(0.5);
        playAtSpeed(-1);
        crank(1000);
        assert.equal(clock.t, 0);
        assert.ok(!playback.get().playing, 'reverse stops at 0');
        playAtSpeed(1);
        activeTab.set('chat');
        assert.ok(playback.get().playing, 'a tab with the bar keeps it');
        activeTab.set('summary');
        assert.ok(!playback.get().playing, 'a tab without it stops it');
        playAtSpeed(1);
        activeTab.set('player');
        assert.ok(!playback.get().playing, 'the Player engine is the clock there');
        stopPlayback();
        off();
    } finally {
        performance.now = realNow;
    }
});

test('region status: Go\'s side A named and coloured by name, not as index 0', () => {
    // 1on1 skull: demoInfo lists AHemlocksLie first, 42 won the frags.
    const r = golden('1on1_42_ahemlockslie_240426_skull');
    const rc = r.timelineAnalysis.regionControl;
    const teams = ['42', 'AHemlocksLie'];
    const colors = ['#a', '#b'];
    const sides = [teams.indexOf(rc.teamA), teams.indexOf(rc.teamB)];
    const rows = regionStatusRows({
        regions: [{ name: 'X' }], states: { X: 'teamAControl' }, playerData: null,
        locOf: () => null, locToRegion: {}, symbols: {}, teams, colors, sides,
    });
    assert.equal(rc.teamA, 'AHemlocksLie');
    assert.deepEqual([rows[0].label, rows[0].color], ['AHemlocksLie', '#b']);
});
