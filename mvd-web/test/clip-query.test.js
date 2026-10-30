import test from 'node:test';
import assert from 'node:assert/strict';

import {
    SOURCES, PRESETS, SLOW_DEFAULT, MOMENT_GROUPS, viewCall, matches, fieldStats, renderLabel, clipsOf, runQuery,
    fieldPaths, toEditTree, toExpr, validateQuery, encodeQuery, decodeQuery, toDraft, fromDraft,
} from '../src/lib/clip-query.js';

const demo = { key: 'hub:7', gameId: 7, title: 'dm2', offset: 10 };
const result = { streams: { global: { demoOffset: 10000 } }, timelineAnalysis: { playerUserIDs: { bps: 3, valla: 4 } } };

test('where is an and / or / not tree of conditions', () => {
    const row = { player: 'bps', detail: { frags: 3, powerup: 'quad' }, weapons: ['rl', 'LG'], had: false };
    const quad = { field: 'detail.powerup', op: '=', value: 'QUAD' };
    const valla = { field: 'player', op: '=', value: 'valla' };
    assert.equal(matches(row, undefined), true, 'no expression keeps every row');
    assert.equal(matches(row, quad), true, 'a bare condition');
    assert.equal(matches(row, { and: [quad, { field: 'detail.frags', op: '>=', value: 3 }] }), true);
    assert.equal(matches(row, { and: [quad, { field: 'detail.frags', op: '>', value: '3' }] }), false, 'numbers compare as numbers');
    assert.equal(matches(row, { or: [valla, quad] }), true);
    assert.equal(matches(row, { and: [quad, { or: [valla, { not: { field: 'had', op: '=', value: true } }] }] }), true, 'a and (b or not c)');
    assert.equal(matches(row, { not: quad }), false);
    assert.equal(matches(row, { and: [] }), true, 'empty and is true');
    assert.equal(matches(row, { or: [] }), false, 'empty or is false');
    assert.equal(matches(row, { field: 'player', op: 'in', value: ['valla', 'BPS'] }), true, 'in is any of');
    assert.equal(matches(row, { field: 'detail.frags', op: 'in', value: [1, 2] }), false);
    assert.equal(matches(row, { field: 'weapons', op: '~', value: 'lg' }), true, '~ is membership on a list');
    assert.equal(matches(row, { field: 'player', op: '~', value: 'B' }), true, '~ is a substring on text');
    assert.equal(matches(row, { field: 'had', op: '=', value: false }), true, 'booleans');
    assert.equal(matches(row, { field: 'missing.deep', op: '!=', value: 'x' }), true);
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
    assert.deepEqual(hl.map((c) => [c.from, c.to, c.track]), [[37, 42, 3]], 'source defaults: 3 s before, 2 s after');
});

test('the kill log and weapon pickups as sources: killer POV, pickup to next death', () => {
    const kills = clipsOf({ source: 'frags', where: { field: 'isTeamKill', op: '=', value: true } },
        { frags: [{ time: 30000, killer: 'bps', victim: 'x' }, { time: 60000, killer: 'valla', victim: 'y', isTeamKill: true }] }, result, demo);
    assert.deepEqual(kills.map((c) => [c.from, c.to, c.who]), [[65, 72, 'valla']], 'teamkills only; 5 s before, 2 s after (demo seconds)');
    const runs = clipsOf({ source: 'weapon-pickups', where: { field: 'kills', op: '>', value: 0 } }, { pickups: [
        { time: 20000, player: 'bps', weapon: 'rl', kills: 2, nextDeathTime: 50000 },
        { time: 30000, player: 'valla', weapon: 'rl', kills: 0, nextDeathTime: 40000 },
        { time: 60000, player: 'valla', weapon: 'rl', kills: 1 },
    ] }, result, demo);
    assert.deepEqual(runs.map((c) => [c.from, c.to, c.who]), [[28, 61, 'bps'], [68, 71, 'valla']],
        '2 s before the pickup to 1 s after the death; a picker who never died again gets the after only');
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
        [36, 42, 'bps', 'RL pack · bps drops it'],
        [48, 52, 'valla', 'RL pack · valla takes it (enemy)'],
        [56, 62, 'valla', 'LG pack · valla drops it'],
        [60, 64, 'bps', 'LG pack · bps takes it (enemy)'],
        [66, 72, 'bps', 'LG pack · bps drops it'],
    ], 'the drop\'s lead-up, then the pickup; an untaken pack gets one clip');
    const worst = PRESETS.find((p) => p.id === 'packs-enemy-new');
    assert.deepEqual(clipsOf(worst.query, body, result, demo).map((c) => c.from), [36, 48]);
});

test('fieldPaths lists leaf paths, lists of objects by their first element', () => {
    const rows = [{ time: 1, actor: { name: 'a' }, victims: [{ name: 'b', killed: true }], weapons: ['rl'] }, { extra: 2 }];
    assert.deepEqual(fieldPaths(rows), ['actor.name', 'extra', 'time', 'victims.0.killed', 'victims.0.name', 'weapons']);
});

test('the edit tree round-trips the stored one and types its values', () => {
    const expr = { and: [
        { field: 'weapon', op: '=', value: 'rl' },
        { not: { or: [{ field: 'pickerRelation', op: 'in', value: ['team', 'self'] }, { field: 'pickerKills', op: '>', value: 2 }] } },
        { not: { field: 'pickerHadWeapon', op: '=', value: true } },
    ] };
    const tree = toEditTree(expr);
    assert.equal(tree.items[1].not, true);
    assert.equal(tree.items[1].items[0].value, 'team, self');
    assert.deepEqual(toExpr(tree), expr);
    assert.equal(toExpr(toEditTree(undefined)), undefined, 'empty keeps all');
    const t2 = toEditTree(undefined);
    t2.items.push({ kind: 'cond', field: '', op: '=', value: 'x' }, { kind: 'cond', field: 'n', op: '<', value: ' 7 ' });
    assert.deepEqual(toExpr(t2), { field: 'n', op: '<', value: 7 }, 'a condition with no field is dropped, a group of one unwrapped');
});

test('validateQuery keeps good queries and refuses malformed ones', () => {
    for (const p of PRESETS) assert.deepEqual(validateQuery(p.query), p.query, p.id);
    assert.equal(validateQuery({ source: 'nope' }), null);
    assert.equal(validateQuery({ source: 'toString' }), null, 'no prototype keys');
    assert.equal(validateQuery({ source: 'events', params: { evil: 'x' } }), null, 'unknown param');
    assert.equal(validateQuery({ source: 'events', where: { field: 'a', op: 'eval', value: 1 } }), null, 'unknown op');
    assert.equal(validateQuery({ source: 'events', where: { and: [{ field: 'a', op: '=', value: {} }] } }), null, 'object value');
    let deep = { field: 'a', op: '=', value: 1 };
    for (let i = 0; i < 12; i++) deep = { not: deep };
    assert.equal(validateQuery({ source: 'events', where: deep }), null, 'too deep');
    assert.equal(validateQuery({ source: 'events', clip: [{}, {}, {}, {}, {}] }), null, 'too many windows');
    assert.equal(validateQuery({ source: 'events', rank: { by: 'x', limit: -1 } }), null);
});

test('encodeQuery / decodeQuery carry a titled query through a link', () => {
    const q = PRESETS.find((p) => p.id === 'packs-enemy-new').query;
    assert.deepEqual(decodeQuery(encodeQuery(q, 'worst packs ä')), { title: 'worst packs ä', query: q });
    assert.equal(decodeQuery('!!!'), null);
    assert.equal(decodeQuery(Buffer.from(JSON.stringify({ v: 1, q: { source: 'x' } })).toString('base64url')), null);
});

test('the builder draft round-trips every preset', () => {
    for (const p of PRESETS) assert.deepEqual(fromDraft(toDraft(p.query)), p.query, p.id);
    const d = toDraft({ source: 'lives' });
    d.params.minMs = ' 60000 ';
    d.params.bogus = 'x';
    d.clips[0].before = '1.5';
    d.rank = { by: 'damageGiven', desc: true, limit: '3' };
    assert.deepEqual(fromDraft(d), { source: 'lives', params: { minMs: '60000' }, rank: { by: 'damageGiven', desc: true, limit: 3 }, clip: { before: 1500 } });
});

test('a clip spec\'s slow motion: around the event by default, a stretch when asked', () => {
    const result = /** @type {any} */ ({ streams: { global: { demoOffset: 10000 } }, timelineAnalysis: { playerUserIDs: { bps: 3 } } });
    const demo = { key: 'hub:1', gameId: 1, title: 't', offset: 10 };
    const air = { airgibs: [{ time: 30000, actor: { name: 'bps' } }] };
    const [c] = clipsOf({ source: 'highlights', clip: { slow: {} } }, air, result, demo);
    assert.deepEqual(c.slow, { from: 39, to: 41, speed: SLOW_DEFAULT.speed }, 'a second either side, demo time');
    const win = { windows: [{ start: 20000, end: 28000, player: 'bps' }] };
    const [w] = clipsOf({ source: 'top-windows', clip: { slow: { to: 'end', before: 0, after: 0, speed: 0.5 } } }, win, result, demo);
    assert.deepEqual(w.slow, { from: 30, to: 38, speed: 0.5 }, 'from the window start to its end');
    assert.equal(clipsOf({ source: 'highlights' }, air, result, demo)[0].slow, undefined, 'none unless asked');
    for (const id of ['quadbores', 'airgibs', 'telefrags', 'discharges']) {
        assert.ok(/** @type {any} */ (PRESETS.find((p) => p.id === id)?.query.clip)?.slow, `${id} slows at the event`);
    }
    const q = { source: 'highlights', clip: { slow: { at: 'time', before: 500, speed: 0.2 } } };
    assert.deepEqual(validateQuery(q), q);
    assert.equal(validateQuery({ source: 'highlights', clip: { slow: { speed: 3 } } }), null, 'a speed over 1 is not slow');
    assert.deepEqual(fromDraft(toDraft(/** @type {any} */ (q))), q, 'the builder keeps it');
    const off = toDraft(/** @type {any} */ (q));
    off.clips[0].slow.on = false;
    assert.equal(/** @type {any} */ (fromDraft(off).clip)?.slow, undefined, 'unticked: gone');
    const fast = toDraft(/** @type {any} */ (q));
    fast.clips[0].slow.speed = '2';
    assert.equal(/** @type {any} */ (fromDraft(fast).clip)?.slow.speed, 1, 'an out-of-range speed is brought into range, not dropped');
});

test('the Moments groups are presets; airgibs start 2.5 s before the hit', () => {
    for (const id of MOMENT_GROUPS) assert.ok(PRESETS.some((p) => p.id === id), `${id} is a preset`);
    const result = /** @type {any} */ ({ streams: { global: { demoOffset: 10000 } }, timelineAnalysis: { playerUserIDs: { bps: 3 } } });
    const demo = { key: 'hub:1', gameId: 1, title: 't', offset: 10 };
    const air = /** @type {any} */ (PRESETS.find((p) => p.id === 'airgibs')).query;
    const [c] = clipsOf(air, { airgibs: [{ time: 30000, actor: { name: 'bps' }, victims: [{ name: 'x' }] }] }, result, demo);
    assert.equal(c.from, 37, '40 s demo time less 2.5 s, whole seconds');
});

test('fieldStats: coverage, types, values by count, ranges, list members', () => {
    const rows = [
        { time: 1000, type: 'powerup', detail: { powerup: 'quad', frags: 2 }, weapons: ['rl', 'lg'] },
        { time: 5000, type: 'powerup', detail: { powerup: 'quad', frags: 0 }, weapons: ['rl'] },
        { time: 3000, type: 'frag', detail: { victim: 'a' }, weapons: [] },
    ];
    const st = fieldStats(rows);
    assert.deepEqual([...st.keys()], ['detail.frags', 'detail.powerup', 'detail.victim', 'time', 'type', 'weapons']);
    assert.deepEqual(st.get('detail.powerup'), { path: 'detail.powerup', n: 2, type: 'string', list: false, values: [['quad', 2]], distinct: 1 });
    assert.deepEqual([st.get('time').min, st.get('time').max, st.get('time').n], [1000, 5000, 3]);
    assert.deepEqual(st.get('type').values, [['powerup', 2], ['frag', 1]], 'most common first');
    assert.deepEqual([st.get('weapons').list, st.get('weapons').values], [true, [['rl', 2], ['lg', 1]]]);
});

test('a row without the field matches only !=; order by text sorts by text', () => {
    const rows = [{ time: 1, detail: { frags: 2 } }, { time: 2, detail: {} }];
    assert.deepEqual(rows.filter((r) => matches(r, { field: 'detail.frags', op: '<', value: 3 })).map((r) => r.time), [1]);
    assert.deepEqual(rows.filter((r) => matches(r, { field: 'detail.frags', op: '!=', value: 3 })).map((r) => r.time), [1, 2]);
    assert.equal(renderLabel('{time|mmss}', { time: 75000 }), '1:15');
    assert.equal(renderLabel('{nope|mmss}', {}), '?');
});
