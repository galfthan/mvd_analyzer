import test from 'node:test';
import assert from 'node:assert/strict';

import {
    KEYMOMENTS_FILTER_DEFAULTS, cleanFilterValue, powerupRuns, filterPowerupRuns, fragStreaks, demoMarkers,
    formatMarkerTime, powerupRunRowHtml, streakRowHtml, markerRowHtml, TOP_QUERIES, topRows, topRowHtml, topWatchCell,
    highlightStack, highlightHad, dischargeValue, HIGHLIGHT_TABLES,
} from '../src/lib/keymoments.js';
import { damageProvenanceHtml, coveragePct, provenanceClass } from '../src/lib/provenance.js';

const hub = { gameId: 42, userIDs: { a: 7 }, demoOffset: 10 };
const local = { gameId: undefined, userIDs: { a: 7 }, demoOffset: 10 };
const href = (html) => html.match(/href="([^"]+)"/)?.[1] ?? null;

test('filter values: clamp at 0, frags round up, non-finite reads as 0', () => {
    assert.deepEqual(KEYMOMENTS_FILTER_DEFAULTS, { minDur: 5, minFrags: 3 });
    assert.equal(cleanFilterValue('-4', 'minDur'), 0);
    assert.equal(cleanFilterValue('2.2', 'minFrags'), 3);
    assert.equal(cleanFilterValue('2.2', 'minDur'), 2.2);
    assert.equal(cleanFilterValue('1e309', 'minDur'), 0);
    assert.equal(cleanFilterValue('', 'minFrags'), 0);
});

test('powerup runs convert ms to s and filter on the rounded duration AND frags', () => {
    const result = { timelineAnalysis: { powerupEvents: [
        { time: 61000, endTime: 65500, duration: 4500, frags: 3, powerupType: 'quad' },
        { time: 1000, duration: 30000, frags: 2, powerupType: 'pent' },
        { time: 2000, duration: 4400, frags: 9 },
    ] } };
    const runs = powerupRuns(result);
    assert.deepEqual([runs[0].time, runs[0].endTime, runs[0].duration], [61, 65.5, 4.5]);
    assert.equal(runs[1].endTime, 0);
    assert.deepEqual(filterPowerupRuns(runs, KEYMOMENTS_FILTER_DEFAULTS).map(r => r.time), [61], '4.5 s shows as 5 s and passes');
    assert.equal(filterPowerupRuns(runs, { minDur: 0, minFrags: 0 }).length, 3);
    assert.deepEqual(powerupRuns({}), []);
});

test('powerup run row: hub clip -2/+1 s on demo time, only with a userid', () => {
    const ev = { time: 61, endTime: 70, duration: 9, frags: 4, powerupType: 'quad', playerName: '<b>', team: 'red', playerUserID: 7 };
    const html = powerupRunRowHtml(ev, hub);
    assert.equal(href(html), 'https://hub.quakeworld.nu/games/?gameId=42&from=69&to=81&track=7');
    assert.match(html, /<td class="powerup-cell quad">Quad<\/td>/);
    assert.match(html, /&lt;b&gt;/);
    assert.match(html, /<td>9s<\/td>/);
    assert.equal(href(powerupRunRowHtml({ ...ev, playerUserID: 0 }, hub)), null, 'a slot number is not a userid');
    assert.equal(href(powerupRunRowHtml(ev, local)), null);
});

test('streak and marker rows', () => {
    const [s] = fragStreaks({ timelineAnalysis: { fragStreaks: [{ time: 5000, endTime: 20000, duration: 15000, frags: 6, ewep: 'rl', playerName: 'a' }] } });
    const sh = streakRowHtml(s, hub);
    assert.equal(href(sh), 'https://hub.quakeworld.nu/games/?gameId=42&from=15&to=32&track=0');
    assert.match(sh, /<td>RL<\/td>/);
    assert.match(sh, /<td>15s<\/td>/);

    const ms = demoMarkers({ timelineAnalysis: { demoMarkers: [{ time: -30500, playerName: 's', spectator: true, team: 'x', label: 'gg' }] } });
    assert.equal(ms[0].time, -30.5);
    const mh = markerRowHtml(ms[0], hub);
    assert.match(mh, /-0:30/);
    assert.match(mh, /<td>spec<\/td>/);
    assert.equal(href(mh), 'https://hub.quakeworld.nu/games/?gameId=42&from=0&to=10&track=0');
    assert.equal(formatMarkerTime(75), '1:15');
    assert.equal(formatMarkerTime(-30.5), '-0:30');
});

test('view queries leave the damage family to boundedOrRaw, and name the per-weapon gaps', () => {
    assert.deepEqual(TOP_QUERIES.map(([k, fn, o]) => [k, fn, o.dmg, o.gapMs ?? o.windowMs, o.limit]), [
        ['topdmg', 'getTopWindows', undefined, 10000, 10],
        ['toprl', 'getTopKills', undefined, 2300, 10],
        ['toplg', 'getTopKills', undefined, 1200, 5],
    ]);
});

test('view rows: windows and kills at seconds, clip links by resolved userid only', () => {
    assert.deepEqual(topRows('topdmg', null), []);
    assert.deepEqual(topRows('toprl', { kills: null }), []);
    const [w] = topRows('topdmg', { windows: [{ start: 60000, end: 70000, player: 'a', score: 300, kills: 2 }] });
    assert.equal(w.t, 60);
    const wh = topRowHtml('topdmg', w, hub);
    assert.equal(href(wh), 'https://hub.quakeworld.nu/games/?gameId=42&from=69&to=80&track=7');
    assert.match(wh, /<td>300<\/td>\s*<td>2<\/td>/);
    const [k] = topRows('toprl', { kills: [{ time: 100000, spanMs: 1500, killer: 'a', victim: 'b', damage: 210, hits: 2, victimWep: 'lg' }] });
    const kh = topRowHtml('toprl', k, hub);
    assert.equal(href(kh), 'https://hub.quakeworld.nu/games/?gameId=42&from=105&to=113&track=7');
    assert.match(kh, /a → b/);
    assert.match(kh, /1\.5 s/);
    assert.match(kh, /<td>LG<\/td>\s*<td>0<\/td>/);
    assert.equal(topWatchCell('nobody', 0, 1, hub), '-');
    assert.equal(topWatchCell('a', 0, 1, local), '-');
});

test('highlight cells: stack, armor type, spawn, gear', () => {
    assert.equal(highlightStack(null), '?');
    assert.equal(highlightStack({ health: 100, armor: 0, armorType: 'ga' }), '<span class="hl-stack">100/0</span>');
    assert.equal(highlightStack({ health: 118, armor: 180, armorType: 'ra', stateSource: 'spawn' }),
        '<span class="hl-stack">118/<span class="armor-ra">180 RA</span></span> <span class="hl-spawn">(spawn)</span>');
    assert.equal(highlightHad({ weapons: ['rl', 'lg'], powerups: ['quad'] }),
        '<span class="hl-had"><span class="wep-rl">RL</span> <span class="wep-lg">LG</span></span> <span class="powerup-cell quad">Quad</span>');
    assert.equal(highlightHad({}), '');
});

test('highlight tables: default orders and sort columns', () => {
    const tbl = Object.fromEntries(HIGHLIGHT_TABLES.map(t => [t.id, t]));
    assert.deepEqual(HIGHLIGHT_TABLES.map(t => [t.id, t.defaultSort]),
        [['airgibs', 'aboveShooter'], ['discharges', 'value'], ['quadbores', 'quadLeft'], ['telefrags', 'stack']]);

    const air = tbl.airgibs.rows({ highlights: { airgibs: [
        { time: 2000, height: 50, actor: { name: 'a' }, victims: [{ name: 'b' }] },
        { time: 1000, height: 90, heightAboveAttacker: 80, actor: { name: 'a', userId: 7 }, victims: [{ name: 'b', killed: true, loc: 'ra' }] },
    ] } });
    assert.deepEqual(air.map(a => a.time), [1000, 2000]);
    const ah = tbl.airgibs.rowHtml(air[0], hub);
    assert.match(ah, /airgib-lethal/);
    assert.equal(href(ah), 'https://hub.quakeworld.nu/games/?gameId=42&from=8&to=13&track=7', 'an airgib: 2.5 s before the hit');

    const armedQuad = { killed: true, relation: 'enemy', weapons: ['rl'], powerups: ['quad'] };
    assert.equal(dischargeValue({ victims: [armedQuad, { ...armedQuad, relation: 'team' }, { ...armedQuad, killed: false }] }), 2);
    const dis = tbl.discharges.rows({ highlights: { discharges: [
        { time: 1, enemyKills: 1, victims: [] },
        { time: 2, enemyKills: 0, victims: [armedQuad], actor: { killed: true } },
    ] } });
    assert.deepEqual(dis.map(d => d.time), [2, 1]);
    assert.match(tbl.discharges.rowHtml(dis[0], local), /data-sort-value="-1">-1</, 'enemy 0 − team 0 − self 1');

    const qb = tbl.quadbores.rows({ highlights: { quadbores: [{ time: 1, quadHeldMs: 25000 }, { time: 2, quadHeldMs: 1000 }] } });
    assert.deepEqual(qb.map(q => q.quadLeft), [29000, 5000]);
    assert.match(tbl.quadbores.rowHtml(qb[0], local), /29\.0 s/);

    const tf = tbl.telefrags.rows({ highlights: { telefrags: [
        { time: 1, teleKind: 'deflect', actor: { name: 't', killed: true }, victims: [{ name: 'p', survived: true }] },
        { time: 2, actor: { name: 'k' }, victims: [{ name: 'v', killed: true, stack: 200, relation: 'team' }] },
    ] } });
    assert.deepEqual(tf.map(t => t.time), [2, 1]);
    const th = tbl.telefrags.rowHtml(tf[1], local);
    assert.match(th, /hl-survived/);
    assert.match(th, /hl-self">died/);
    assert.match(th, /hl-kind">deflect/);
    assert.match(tbl.telefrags.rowHtml(tf[0], local), /hl-team">team/);
});

test('damage provenance: measured is null, coverage printed as a magnitude, never 100 below 1', () => {
    assert.equal(damageProvenanceHtml({ damage: { source: 'log' } }), null);
    assert.equal(damageProvenanceHtml(null), null);
    assert.match(damageProvenanceHtml({ damage: { source: 'reconstructed' } }).html, /was not assessed/);
    const full = damageProvenanceHtml({ damage: { source: 'reconstructed', coverage: { ratio: 1, kills: 30, covered: 30 } } });
    assert.equal(full.warn, false);
    assert.match(full.html, /all 30 of/);
    const part = damageProvenanceHtml({ damage: { source: 'reconstructed', coverage: { ratio: 0.051, kills: 100, covered: 5 } } });
    assert.equal(part.warn, true);
    assert.match(part.html, /5\.1%/);
    assert.equal(coveragePct(0.996), '99.60');
    assert.equal(coveragePct(0.5), '50');
    assert.equal(provenanceClass(part), 'panel-provenance warn');
    assert.equal(provenanceClass(null), 'panel-provenance');
});
