import test from 'node:test';
import assert from 'node:assert/strict';

import { computePickupsState, hasPickups, pickupsSection, verifyCell, buildWeaponCols, buildItemCols } from '../src/lib/pickups.js';

const phase = (takenBy, team) => ({ takenBy, team });

function demoResult({ ktx = true } = {}) {
    const p = (name, team, frags, rlTaken, rlTotal) => ({
        name, team, stats: { frags },
        weapons: ktx ? { rl: { pickups: { taken: rlTaken, 'total-taken': rlTotal, 'spawn-taken': 1, 'spawn-total-taken': 1 } } } : undefined,
        items: ktx ? { ra: { took: 2 } } : undefined,
    });
    return {
        demoInfo: { players: [p('b1', 'blue', 5, 2, 3), p('r1', 'red', 9, 1, 1), p('r2', 'red', 1, 0, 0)] },
        items: { items: [
            { kind: 'rl', entNum: 10, loc: 'quad', name: 'weapon_rocketlauncher', phases: [phase('r1', 'red'), phase('b1', 'blue')] },
            { kind: 'ra', entNum: 20, loc: 'ra', phases: [phase('r1'), phase('r1'), phase('b1')] },
            { kind: 'ya', entNum: 30, loc: 'b', phases: [] },
            { kind: 'ya', entNum: 31, loc: 'a', phases: [phase('b1', 'blue')] },
            { kind: 'shells', entNum: 40, phases: [phase('b1')] },
        ] },
        weaponPickups: [
            { weapon: 'rl', source: 'backpack', player: 'b1', team: 'blue', hadBefore: false },
            { weapon: 'rl', source: 'backpack', player: 'b1', team: 'blue', hadBefore: true },
            { weapon: 'rl', source: 'world', player: 'r1', team: 'red' },
        ],
        playerStats: {
            players: [{ name: 'r1', hold: { weapons: { rl: { ms: 12400, runs: 1 } } }, window: { aliveMs: 1, presentMs: 2, matchMs: 3 } }],
            teams: [{ name: 'red', hold: { armor: { ra: { ms: 3000, runs: 2 } } } }],
        },
    };
}

test('columns: a single non-pack weapon gets one cell; RL gets entity, pack, Σ and s', () => {
    const st = computePickupsState(demoResult(), ['red', 'blue']);
    assert.deepEqual(buildWeaponCols(st).map(c => c.type), ['entity-count', 'weap-pack', 'weap-verify', 'hold']);
    // Two YA spawns: one column each, sorted by loc, then Σ.
    const items = buildItemCols(st);
    assert.deepEqual(items.map(c => c.type), ['item-verify', 'hold', 'entity-count', 'entity-count', 'item-verify', 'hold']);
    assert.match(items[2].header, /@ a</);
    assert.match(items[3].header, /@ b</);
    assert.deepEqual(hasPickups(st), { weapon: true, item: true });
    assert.deepEqual(hasPickups(computePickupsState({}, [])), { weapon: false, item: false });
});

test('team order: the canonical order, else the frag-sorted players\' teams', () => {
    assert.deepEqual(computePickupsState(demoResult(), ['blue', 'red']).teamOrder, ['blue', 'red']);
    assert.deepEqual(computePickupsState(demoResult(), []).teamOrder, ['red', 'blue']);
});

test('pickupsSection rows: team rows in canonical order, players by team then frags', () => {
    const st = computePickupsState(demoResult(), ['red', 'blue']);
    const sec = pickupsSection(st, 'weap', 'all');
    assert.match(sec.team.head, /^<th>Team<\/th><th>RL<\/th><th>RL pack<\/th>/);
    assert.match(sec.player.head, /^<th>Player<\/th>/);
    assert.match(sec.team.head, /<th title="Total seconds holding the RL/);
    assert.deepEqual(sec.team.rows.map(r => r.colorIdx), [0, 1]);
    assert.deepEqual(sec.player.rows.map(r => r.html.match(/^<td>(\w+)/)[1]), ['r1', 'r2', 'b1']);
    // r1: entity 1, no packs, KTX Σ = total-taken 1, held 12s.
    assert.equal(sec.player.rows[0].html,
        '<td>r1 <span class="pickups-team-tag">red</span></td><td>1</td>'
        + '<td><span class="muted">·</span></td>'
        + '<td title="ktx first pickup, any source: 1\nktx every touch, any source: 1\nktx first pickup of items: 1\nktx every item touch: 1\nanalytics: 1">1</td>'
        + '<td data-sort-value="12400" title="held 12400 ms · 1 run · window alive 1 / present 2 / match 3 ms">12s</td>');
    // r2: nothing — muted zero, muted dots, no hold.
    assert.match(sec.player.rows[1].html, /<td><span class="muted">0<\/span><\/td>/);
    assert.match(sec.player.rows[1].html, /<td data-sort-value="0">-<\/td>$/);
    assert.equal(pickupsSection(computePickupsState({ items: { items: [] } }, []), 'weap', 'all'), null);
});

test('mode: first pickup per life drops hadBefore packs and reads KTX taken', () => {
    const st = computePickupsState(demoResult(), ['red', 'blue']);
    const cells = (mode) => pickupsSection(st, 'weap', mode).player.rows[2].html.split('</td>');
    const all = cells('all');
    const first = cells('first');
    // b1's pack cell: KTX total-taken - spawn-total-taken = 2; taken - spawn-taken = 1.
    assert.match(all[2], /analytics: 2">2$/);
    assert.match(first[2], /analytics: 1">1$/);
    // Σ: KTX total-taken 3 vs taken 2.
    assert.match(all[3], />3$/);
    assert.match(first[3], />2$/);
});

test('without KTX counters the analytics count is shown', () => {
    const st = computePickupsState(demoResult({ ktx: false }), ['red', 'blue']);
    const sec = pickupsSection(st, 'item', 'all');
    const red = sec.team.rows[0].html;
    assert.match(red, /<td title="analytics: 2\nno KTX pickup counters in this demo">2<\/td>/);
    assert.match(red, /<td data-sort-value="3000" title="held 3000 ms · 2 runs">3s<\/td>/, 'team hold from the team row');
});

test('verifyCell', () => {
    assert.equal(verifyCell(0, 0, null, true), '<td><span class="muted">·</span></td>');
    assert.equal(verifyCell(0, 0, { x: 1 }, true), '<td title="ktx x: 1\nanalytics: 0">0</td>');
    assert.equal(verifyCell(2, 3, null, true), '<td title="ktx: 3\nanalytics: 2">3</td>');
});
