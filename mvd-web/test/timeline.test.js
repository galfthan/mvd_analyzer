import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
    timelineEvents, fragsAt, matchDurationSec, viewRange, clampSegment, indicatorPct, axisTicks,
    pickTickInterval, makeScanlineSampler, prepWeaponsData, computeBackpackDrops, prepHealthArmorData,
    buildHASegments, prepScoreData, prepPowerupRowsData, prepRegionControlData, rosterTeamOf, playersByTeam,
    prepPlayerHAData, sharedStackMax, prepPlayerFragDeathData, fragDeathLabels, prepPlayerWeaponSpans,
    playerDropMarks, nextTeamStatusSort, compareTeamStatusRows, teamStatusRows, teamStatusHtml,
    hubWatchLinkHtml, hitDropMark, dropTooltipHtml, hitPowerupSpan, powerupTooltipHtml,
    teamStrongColor, dimColor, GRAPH_COLORS, HR_DUR, TEAM_STATUS_SORT_DEFAULT, rollingWindow,
} from '../src/lib/timeline.js';
import { segment } from '../src/state/timeline.js';
import { hubLinkHtml } from '../src/lib/hub-url.js';

const golden = (name) => JSON.parse(readFileSync(new URL(`../../mvd-analytics/testdata/golden/${name}.json`, import.meta.url)));
const TEAM2 = golden('2on2_nani_pora_210426_dm6');
const FFA = golden('ffa_countdown_dm6_260106');
const COLORS = ['#ff5050', '#50a0ff', '#4ecdc4', '#ffc107'];

// A 4-bucket view (50 ms from t = 0): two players on two teams.
function makeView() {
    return {
        windowMs: 50, start: 0, count: 4,
        players: {
            a: { first: 0, n: 4, alive: [1, 1, 0, 1], h: [100, 80, 0, 50], a: [200, 150, 0, 0], at: ['ra', 'ra', '', ''], rl: [1, 1, 0, 0], lg: [0, 1, 0, 1] },
            b: { first: 1, n: 3, alive: [1, 1, 1], h: [100, 90, 70], a: [0, 50, 50], at: ['', 'ya', 'ya'], rl: [0, 0, 0], lg: [0, 0, 0] },
        },
        teams: {
            red: { rl: [1, 1, 0, 0], lg: [0, 0, 0, 1], rllg: [0, 1, 0, 0], th: [100, 80, 0, 50], ta: [200, 150, 0, 0], abt: { ra: [1, 1, 0, 0], ya: [0, 0, 0, 0], ga: [0, 0, 0, 0] } },
            blue: { rl: [0, 0, 0, 0], lg: [0, 0, 0, 0], rllg: [0, 0, 0, 0], th: [0, 100, 90, 70], ta: [0, 0, 50, 50], abt: { ra: [0, 0, 0, 0], ya: [0, 0, 1, 1], ga: [0, 0, 0, 0] } },
        },
    };
}

test('event intake: seconds, sorted frags, running counts, memoised', () => {
    const ev = timelineEvents(TEAM2);
    assert.equal(ev, timelineEvents(TEAM2));
    assert.equal(ev.fragEvents.length, TEAM2.timelineAnalysis.fragEvents.length);
    assert.equal(ev.fragEvents[0].time, TEAM2.timelineAnalysis.fragEvents[0].time * 0.001);
    for (let i = 1; i < ev.fragEvents.length; i++) assert.ok(ev.fragEvents[i].time >= ev.fragEvents[i - 1].time);
    const pu = TEAM2.timelineAnalysis.powerupEvents[0];
    assert.equal(ev.powerupEvents[0].endTime, pu.endTime * 0.001);
    assert.equal(ev.powerupEvents[0].duration, pu.duration * 0.001);
    assert.equal(ev.backpacks[0].time, TEAM2.backpacks[0].time * 0.001);
    // Running frags: the last entry is the whole-match total per player.
    const last = ev.fragCounts.at(-1).cumulative;
    const want = {};
    for (const f of TEAM2.timelineAnalysis.fragEvents) want[f.player] = (want[f.player] || 0) + (f.delta || 1);
    assert.deepEqual(last, want);
    assert.deepEqual(fragsAt(TEAM2, -1), {});
    assert.deepEqual(fragsAt(TEAM2, 1e9), want);
    const first = ev.fragEvents[0];
    assert.equal(fragsAt(TEAM2, first.time)[first.player], first.delta || 1);
    assert.deepEqual(timelineEvents(null).fragEvents, []);
    assert.equal(matchDurationSec(TEAM2), TEAM2.match.duration / 1000);
    assert.equal(matchDurationSec({}), 600);
});

test('view range: clamp, minimum span, whole match is null', () => {
    assert.deepEqual(viewRange(null, 600), [0, 600]);
    assert.deepEqual(viewRange({ start: 10, end: 20 }, 600), [10, 20]);
    assert.equal(clampSegment(0, 600, 600), null);
    assert.equal(clampSegment(-50, 700, 600), null);
    assert.equal(clampSegment(0, 10, 0), undefined);
    assert.deepEqual(clampSegment(100, 100.5, 600), { start: 99.25, end: 101.25 });
    // Slid back inside without shrinking.
    assert.deepEqual(clampSegment(-10, 90, 600), { start: 0, end: 100 });
    assert.deepEqual(clampSegment(550, 650, 600), { start: 500, end: 600 });
    assert.equal(indicatorPct(50, 0, 100), 50);
    assert.equal(indicatorPct(-5, 0, 100), 0);
    assert.equal(indicatorPct(500, 0, 100), 100);
    assert.equal(indicatorPct(1, 5, 5), null);
});

test('axis ticks', () => {
    assert.equal(pickTickInterval(600, 10), 60);
    assert.equal(pickTickInterval(1e6, 4), 3600);
    const t = axisTicks(0, 600, 1000, 12);
    assert.deepEqual(t.map((x) => x.label), ['0:00', '1:00', '2:00', '3:00', '4:00', '5:00', '6:00', '7:00', '8:00', '9:00', '10:00']);
    assert.equal(t[1].frac, 60 / 600);
    // Density never under 4 ticks' worth (600 s / 4 → every 5:00), never
    // over the cap.
    assert.equal(axisTicks(0, 600, 100, 12).length, 3);
    assert.equal(axisTicks(0, 600, 5000, 10).length, 11);
    assert.deepEqual(axisTicks(5, 5, 1000, 12), []);
    assert.equal(axisTicks(0, 60, 400, 10, 1e-6).at(-1).t, 60);
});

test('scanline sampler holds the last point and stops past its dt', () => {
    const s = makeScanlineSampler([{ t: 1, dt: 1 }, { t: 2, dt: 0.5 }]);
    assert.equal(s(0.5), null);
    assert.equal(s(1.2).t, 1);
    assert.equal(s(2.4).t, 2);
    assert.equal(s(2.6), null);
});

test('team series: weapons, health / armor, score', () => {
    const view = makeView();
    const w = prepWeaponsData(view, 0, 1, ['red', 'blue']);
    assert.equal(w.points.length, 4);
    assert.equal(w.points[0].dt, HR_DUR);
    assert.equal(w.max, 4);
    assert.deepEqual(w.points[1].up.map((s) => s.h), [1, 0, 1]);
    assert.equal(w.points[1].up[0].color, GRAPH_COLORS.RLLG);
    assert.deepEqual(prepWeaponsData(null, 0, 1, ['red', 'blue']), { points: [], max: 4 });
    // The window starts at the first bucket at or after it.
    assert.equal(prepWeaponsData(view, 0.06, 0.1, ['red', 'blue']).points.length, 1);

    const ha = prepHealthArmorData(view, 0, 1, ['red', 'blue']);
    assert.equal(ha.max, 400);
    assert.deepEqual(ha.points[0].up.map((s) => [s.h, s.color]), [[100, GRAPH_COLORS.HEALTH], [200, GRAPH_COLORS.RA]]);
    assert.deepEqual(buildHASegments({ th: 0, ta: 30 }), [{ h: 30, color: GRAPH_COLORS.YA }]);
    assert.deepEqual(buildHASegments({ th: 10, ta: 90, abt: { ra: 1, ya: 2, ga: 0 } }).map((s) => s.h), [10, 60, 30]);

    const ev = timelineEvents(TEAM2);
    const teams = TEAM2.demoInfo.teams;
    const sc = prepScoreData(ev.fragEvents, 0, 600, teams, COLORS);
    const at = (t) => { let v = 0; for (const f of ev.fragEvents) { if (f.time > t) break; v += f.team === teams[0] ? (f.delta || 1) : f.team === teams[1] ? -(f.delta || 1) : 0; } return v; };
    for (const p of sc.points.filter((_, i) => i % 37 === 0)) {
        const v = p.up.length ? p.up[0].h : p.down.length ? -p.down[0].h : 0;
        assert.equal(v, at(p.t));
    }
    assert.equal(sc.points.length, 400);
    assert.equal(sc.points.find((p) => p.up.length)?.up[0].color ?? 'rgba(255,80,80,0.8)', 'rgba(255,80,80,0.8)');
    assert.ok(sc.max >= 10);
    // Mid-window: the left edge carries the score so far.
    const mid = prepScoreData(ev.fragEvents, 300, 400, teams, COLORS);
    const v0 = mid.points[0].up.length ? mid.points[0].up[0].h : mid.points[0].down.length ? -mid.points[0].down[0].h : 0;
    assert.equal(v0, at(299.9999));
    assert.deepEqual(prepScoreData(ev.fragEvents, 0, 600, ['x'], COLORS), { points: [], max: 10 });
});

test('drop dots, powerup rows, region rows', () => {
    const ev = timelineEvents(TEAM2);
    const teams = TEAM2.demoInfo.teams;
    const drops = computeBackpackDrops(ev.backpacks, 0, 600, teams);
    assert.ok(drops.length > 0);
    for (const d of drops) {
        assert.equal(d.isTop, d.drop.team === teams[0]);
        assert.equal(d.color, d.drop.weapon === 'rl' ? GRAPH_COLORS.RL : GRAPH_COLORS.LG);
    }
    assert.equal(computeBackpackDrops(ev.backpacks, 0, 600, ['x', 'y']).length, 0);

    const pu = prepPowerupRowsData(ev.powerupEvents, 0, 600, teams);
    assert.deepEqual(pu.rows.map((r) => r.name), ['Quad', 'Pent', 'Ring']);
    assert.equal(pu.rows.reduce((n, r) => n + r.spans.length, 0), ev.powerupEvents.filter((e) => e.endTime > e.time).length);
    const clipped = prepPowerupRowsData(ev.powerupEvents, 12, 13, teams).rows[2].spans[0];
    assert.deepEqual([clipped.start, clipped.end, clipped.state], [12, 13, 'teamB']);
    assert.equal(prepPowerupRowsData([], 0, 600, teams), null);
    assert.equal(prepPowerupRowsData(ev.powerupEvents, 0, 600, ['solo']), null);

    const view = makeView();
    const rc = { regions: [{ name: 'R1' }, { name: 'R2' }, { name: 'none' }], bucketStates: { R1: 'AACb', R2: 'a' }, teamA: 'red', teamB: null };
    const r = prepRegionControlData(rc, view, 0, 1, ['x', 'blue']);
    assert.equal(r.teamA, 'red');
    assert.equal(r.teamB, 'blue');
    assert.deepEqual(r.rows.map((row) => row.name), ['R1', 'R2']);
    assert.deepEqual(r.rows[0].spans.map((s) => [s.state, s.start, s.end]),
        [['teamAControl', 0, 0.1], ['contested', 0.1, 0.15], ['teamBWeakControl', 0.15, 1]]);
    // Past the end of a state string: empty.
    assert.deepEqual(r.rows[1].spans.map((s) => s.state), ['teamAWeakControl', 'empty']);
    assert.equal(prepRegionControlData({ regions: [], bucketStates: {} }, view, 0, 1, []), null);
    assert.equal(prepRegionControlData(rc, null, 0, 1, []), null);
});

test('roster grouping: two sides, or one per player', () => {
    const view = makeView();
    view.players.spec = { first: 0, n: 0, alive: [] };
    const teamOf = new Map([['a', 'red'], ['b', 'blue']]);
    assert.deepEqual(playersByTeam(view, ['red', 'blue'], teamOf, false), [['a'], ['b']]);
    assert.deepEqual(playersByTeam(view, ['blue', 'red'], teamOf, false), [['b'], ['a']]);
    assert.deepEqual(playersByTeam(view, ['b', 'a', 'spec'], teamOf, true), [['b'], ['a'], ['spec']]);
    assert.deepEqual(playersByTeam(null, ['red', 'blue'], teamOf, false), [[], []]);
    assert.deepEqual(playersByTeam(view, ['red'], teamOf, false), [[], []]);

    const t2 = rosterTeamOf(TEAM2);
    for (const p of TEAM2.demoInfo.players) assert.equal(t2.get(p.name), p.team);
    // No demoinfo block: playerStats names the teams.
    const noKtx = { ...TEAM2, demoInfo: null };
    assert.equal(rosterTeamOf(noKtx).size, TEAM2.playerStats.players.length);
    assert.ok(rosterTeamOf(FFA).size > 2);
});

test('per-player series', () => {
    const view = makeView();
    const ha = prepPlayerHAData(view, view.players.a, 0, 1);
    assert.equal(ha.length, 4);
    assert.deepEqual(ha[2].up, []);
    assert.deepEqual(ha[0].up.map((s) => [s.h, s.color]), [[100, GRAPH_COLORS.HEALTH], [200, GRAPH_COLORS.RA]]);
    assert.equal(sharedStackMax([ha]), 300);
    assert.equal(sharedStackMax([[{ up: [{ h: 250 }, { h: 200 }] }]]), 450);
    assert.deepEqual(prepPlayerHAData(view, undefined, 0, 1), []);

    const sp = prepPlayerWeaponSpans(view, view.players.a, 0, 1);
    assert.deepEqual(sp.map((s) => [s.state, s.start, s.end]), [['rl', 0, 0.05], ['rllg', 0.05, 0.1], ['lg', 0.15, 1]]);
    assert.deepEqual(prepPlayerWeaponSpans(view, view.players.b, 0, 1), []);

    const ev = timelineEvents(TEAM2);
    const name = TEAM2.demoInfo.players[0].name;
    const fd = prepPlayerFragDeathData(ev.killEvents, ev.deathEvents, name, 0, 600, '#ff5050', dimColor('#ff5050'));
    const kills = ev.killEvents.filter((k) => k.player === name).length;
    const deaths = ev.deathEvents.filter((d) => d.player === name).length;
    assert.equal(fd.kills, kills);
    assert.equal(fd.deaths, deaths);
    assert.equal(fd.points.length, 400);
    const labels = fragDeathLabels(ev.killEvents, ev.deathEvents, [name, 'nobody'], 1e9);
    const eff = Math.round(kills / (kills + deaths) * 100);
    assert.equal(labels[name].text, `${kills}/${deaths} · ${eff}%`);
    assert.equal(labels.nobody.text, '0/0 · 0%');
    assert.match(labels[name].title, /efficiency at /);

    const marks = playerDropMarks(ev.backpacks, [ev.backpacks[0].player], 0, 600);
    assert.ok(marks.length > 0 && marks.every((m) => m.row === 0));
});

test('team status rows and markup', () => {
    assert.deepEqual(nextTeamStatusSort(TEAM_STATUS_SORT_DEFAULT, 'name'), { key: 'name', dir: -1 });
    assert.deepEqual(nextTeamStatusSort(TEAM_STATUS_SORT_DEFAULT, 'frags'), { key: 'frags', dir: -1 });
    assert.deepEqual(nextTeamStatusSort({ key: 'frags', dir: -1 }, 'frags'), { key: 'frags', dir: 1 });
    const rows = [{ name: 'b', frags: 2 }, { name: 'a', frags: 2 }, { name: 'c', frags: 5 }];
    assert.deepEqual([...rows].sort(compareTeamStatusRows({ key: 'frags', dir: -1 })).map((r) => r.name), ['c', 'a', 'b']);
    assert.deepEqual([...rows].sort(compareTeamStatusRows({ key: 'name', dir: -1 })).map((r) => r.name), ['c', 'b', 'a']);

    const result = { ...TEAM2, hubInfo: { gameId: 42 } };
    const teamOf = rosterTeamOf(result);
    const team = TEAM2.demoInfo.teams[0];
    const members = TEAM2.demoInfo.players.filter((p) => p.team === team).map((p) => p.name);
    const frame = { [members[0]]: { h: 90, a: 100, at: 'ra', rl: true, lg: true, q: true } };
    const st = teamStatusRows(result, teamOf, frame, { [members[0]]: 3 }, team, TEAM_STATUS_SORT_DEFAULT);
    assert.deepEqual(st.map((p) => p.name), [...members].sort((a, b) => a.localeCompare(b)));
    const alive = st.find((p) => p.name === members[0]);
    assert.equal(alive.dead, false);
    assert.equal(alive.frags, 3);
    assert.ok(st.filter((p) => p.name !== members[0]).every((p) => p.dead && p.health === 0));
    const html = teamStatusHtml(result, st, team, '#ff5050', TEAM_STATUS_SORT_DEFAULT, 100);
    assert.match(html, /^<h4 style="color: #ff5050">/);
    assert.match(html, /— 3 frags<\/h4>/);
    assert.match(html, /<th class="sortable sort-asc" data-key="name">Player<\/th>/);
    assert.match(html, /<span class="armor-ra">100 RA<\/span><\/td><td>RL\+LG, Quad<\/td>/);
    assert.match(html, /<tr class="totals-row"><td>Total<\/td><td>3<\/td><td>90<\/td><td>100<\/td>/);
    const uid = TEAM2.timelineAnalysis.playerUserIDs[members[0]];
    const from = Math.floor(100 + TEAM2.streams.global.demoOffset / 1000);
    assert.ok(html.includes(`gameId=42&from=${from}&track=${uid}`));
    assert.equal(hubWatchLinkHtml('x', 10, null, {}, 0), '');
    assert.equal(hubWatchLinkHtml('x', 10, { gameId: 1 }, {}, 0), '');
    assert.equal(hubWatchLinkHtml('x', 10.7, { gameId: 1 }, { x: 5 }, 2),
        hubLinkHtml('https://hub.quakeworld.nu/games/?gameId=1&from=12&track=5', true));
});

test('hover read-outs', () => {
    const marks = [{ time: 50, isTop: true, drop: { player: 'a<b', weapon: 'rl', loc: 'ra', time: 50 } }, { time: 52, isTop: false, drop: { player: 'c', weapon: 'lg', time: 52 } }];
    const hit = { startTime: 0, endTime: 100, W: 1000, dropMarks: marks };
    assert.equal(hitDropMark(hit, 500, 8), marks[0]);
    assert.equal(hitDropMark(hit, 520, 172), marks[1]);
    assert.equal(hitDropMark(hit, 500, 100), null);
    assert.equal(hitDropMark({ ...hit, W: 0 }, 500, 8), null);
    assert.equal(dropTooltipHtml(marks[0]), '<div><strong>a&lt;b</strong> dropped <strong>RL</strong></div>\n<div>Loc: ra</div><div>Time: 0:50</div>');
    const ev = { playerName: 'p', team: 't', frags: 2, duration: 29.6 };
    const rows = [{ name: 'Quad', spans: [{ start: 10, end: 40, event: ev }] }, { name: 'Pent', spans: [] }];
    const ph = { startTime: 0, endTime: 100, W: 1000, rows };
    assert.equal(hitPowerupSpan(ph, 200, 5).sp.event, ev);
    assert.equal(hitPowerupSpan(ph, 200, 25), null);
    assert.equal(hitPowerupSpan(ph, 500, 5), null);
    assert.equal(hitPowerupSpan(ph, 200, 45), null);
    assert.equal(powerupTooltipHtml(hitPowerupSpan(ph, 200, 5)),
        '<div><strong>Quad</strong> · <strong>p</strong></div>\n<div>Team: t</div><div>Frags: 2</div>\n<div>Duration: 30s</div>');
});

test('colours and the segment store', () => {
    assert.equal(teamStrongColor('#ff5050'), 'rgb(179, 56, 56)');
    assert.equal(dimColor('#50a0ff'), 'rgb(44,88,140)');
    const seen = [];
    const off = segment.subscribe((s) => seen.push(s));
    segment.set({ start: 1, end: 5 });
    segment.set(null);
    off();
    assert.deepEqual(seen, [null, { start: 1, end: 5 }, null]);
});

test('rollingWindow keeps the playhead three quarters in, inside the match', () => {
    assert.deepEqual(rollingWindow(300, 120, 1200), [210, 330]);
    assert.deepEqual(rollingWindow(10, 120, 1200), [0, 120], 'clamped at the start');
    assert.deepEqual(rollingWindow(1190, 120, 1200), [1080, 1200], 'and at the end');
    assert.deepEqual(rollingWindow(300, 0, 1200), [0, 1200], '0 is the whole match');
    assert.deepEqual(rollingWindow(300, 1800, 1200), [0, 1200]);
});
