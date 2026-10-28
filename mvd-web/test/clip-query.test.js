import test from 'node:test';
import assert from 'node:assert/strict';

import { SOURCES, PRESETS, viewCall, matches, renderLabel, clipsOf, runQuery } from '../src/lib/clip-query.js';

const demo = { key: 'hub:7', gameId: 7, title: 'dm2', offset: 10 };
const result = { streams: { global: { demoOffset: 10000 } }, timelineAnalysis: { playerUserIDs: { bps: 3, valla: 4 } } };

test('where ORs its groups and ANDs inside one', () => {
    const row = { player: 'bps', detail: { frags: 3, powerup: 'quad' }, weapons: ['rl', 'LG'] };
    const quad = { field: 'detail.powerup', op: '=', value: 'QUAD' };
    assert.equal(matches(row, []), true, 'no conditions keep every row');
    assert.equal(matches(row, [[quad, { field: 'detail.frags', op: '>=', value: 3 }]]), true);
    assert.equal(matches(row, [[quad, { field: 'detail.frags', op: '>', value: '3' }]]), false, 'numbers compare as numbers');
    assert.equal(matches(row, [[{ field: 'player', op: '=', value: 'valla' }], [quad]]), true, 'second group matches');
    assert.equal(matches(row, [[{ field: 'weapons', op: '~', value: 'lg' }]]), true, '~ is membership on a list');
    assert.equal(matches(row, [[{ field: 'player', op: '~', value: 'B' }]]), true, '~ is a substring on text');
    assert.equal(matches(row, [[{ field: 'missing.deep', op: '!=', value: 'x' }]]), true);
});

test('label templates fill fields, plurals and seconds', () => {
    const row = { player: 'bps', n: 1, ms: 12600, victims: [{ name: 'valla' }] };
    assert.equal(renderLabel('{player} · {n} frag{n|s} · {ms|sec} s → {victims.0.name}', row), 'bps · 1 frag · 13 s → valla');
    assert.equal(renderLabel('{nope} {ms|s}', row), '? s');
});

test('clipsOf windows rows in demo seconds and tracks the pov by name', () => {
    const body = { events: [
        { time: 50000, type: 'powerup', player: 'valla', detail: { endTime: 80000, frags: 2 } },
        { time: 20000, type: 'powerup', player: 'bps', detail: { endTime: 40000, frags: 2 } },
        { time: 90000, type: 'powerup', player: 'nobody', detail: { endTime: 95000, frags: 5 } },
    ] };
    const q = { source: 'events', clip: { before: 3000, to: 'detail.endTime', after: 2000 }, label: '{player}' };
    const clips = clipsOf(q, body, result, demo);
    assert.deepEqual(clips.map((c) => [c.from, c.to, c.track, c.who]),
        [[27, 52, 3, 'bps'], [57, 92, 4, 'valla'], [97, 107, 'auto', '']], 'time order, unknown player follows autotrack');
    assert.deepEqual(clips[0].demo, demo);

    const ranked = clipsOf({ ...q, rank: { by: 'detail.frags', desc: true, limit: 2 } }, body, result, demo);
    assert.deepEqual(ranked.map((c) => c.label), ['nobody', 'bps'], 'rank, ties by time, then limit');

    const hl = clipsOf({ source: 'highlights' }, { airgibs: [{ time: 30000, actor: { name: 'bps' } }] }, result, demo);
    assert.deepEqual(hl.map((c) => [c.from, c.to, c.track]), [[34, 43, 3]], 'source defaults: 6 s before, 3 s after');
});

test('viewCall maps API params onto view options', async () => {
    /** @type {Array<[string, any]>} */
    const calls = [];
    const view = async (fn, opts) => { calls.push([fn, opts]); return {}; };
    await viewCall(view, 'events', { from: '1000', types: 'powerup, streak', players: 'bps' });
    assert.deepEqual(calls.pop(), ['getEvents', { StartTime: 1000, EndTime: undefined, Players: ['bps'], Types: ['powerup', 'streak'] }]);
    await viewCall(view, 'highlights', { kinds: 'airgib' });
    assert.deepEqual(calls.pop(), ['getHighlights', { Kinds: ['airgib'], Players: undefined }]);
    await viewCall(view, 'top-kills', { weapons: 'rl', dmg: 'raw' });
    assert.equal(calls.pop()[1].Dmg, 'raw', 'an explicit family is passed through');
    await assert.rejects(viewCall(view, 'nope'), /unknown clip source/);
});

test('viewCall defaults the damage family to bounded, falling back to raw', async () => {
    const seen = [];
    const view = async (fn, opts) => {
        seen.push(opts.Dmg);
        if (opts.Dmg === 'bounded') throw new Error('bounded damage family unavailable: section unavailable');
        return { lives: [] };
    };
    assert.deepEqual(await viewCall(view, 'lives', {}), { lives: [] });
    assert.deepEqual(seen, ['bounded', 'raw']);
    const other = async () => { throw new Error('no frag log'); };
    await assert.rejects(viewCall(other, 'lives', {}), /no frag log/, 'other errors are not retried');
});

test('presets name real sources and run end to end', async () => {
    assert.equal(new Set(PRESETS.map((p) => p.id)).size, PRESETS.length);
    for (const p of PRESETS) assert.ok(SOURCES[p.query.source], p.id);
    const best = PRESETS.find((p) => p.id === 'best-quad-runs');
    const view = async () => ({ events: [
        { time: 1000, player: 'bps', detail: { powerup: 'quad', endTime: 9000, frags: 0 } },
        { time: 2000, player: 'bps', detail: { powerup: 'ring', endTime: 9000, frags: 4 } },
        { time: 3000, player: 'valla', detail: { powerup: 'quad', endTime: 9000, frags: 1 } },
    ] });
    const clips = await runQuery(best.query, view, result, demo);
    assert.deepEqual(clips.map((c) => c.label), ['Quad run · valla · 1 frag']);
});

test('a list of clip entries makes one clip per entry and row', () => {
    const body = { backpacks: [
        { time: 30000, player: 'bps', weapon: 'rl', fate: 'picked', picker: 'valla', pickupTime: 40000, pickerNextDeathTime: 90000, pickerRelation: 'enemy', pickerHadWeapon: false },
        { time: 50000, player: 'valla', weapon: 'lg', fate: 'picked', picker: 'bps', pickupTime: 52000, pickerRelation: 'enemy', pickerHadWeapon: true },
        { time: 60000, player: 'bps', weapon: 'lg', fate: 'expired' },
    ] };
    const all = PRESETS.find((p) => p.id === 'pack-drops');
    const clips = clipsOf(all.query, body, result, demo);
    assert.deepEqual(clips.map((c) => [c.from, c.to, c.who, c.label]), [
        [28, 42, 'bps', 'RL pack · bps drops it'],
        [47, 52, 'valla', 'RL pack · valla takes it (enemy)'],
        [48, 62, 'valla', 'LG pack · valla drops it'],
        [59, 64, 'bps', 'LG pack · bps takes it (enemy)'],
        [58, 72, 'bps', 'LG pack · bps drops it'],
    ], 'the drop\'s lead-up, then the pickup; an untaken pack gets one clip');
    const worst = PRESETS.find((p) => p.id === 'packs-enemy-new');
    assert.deepEqual(clipsOf(worst.query, body, result, demo).map((c) => c.from), [28, 47]);
});
