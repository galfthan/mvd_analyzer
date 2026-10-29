import test from 'node:test';
import assert from 'node:assert/strict';
import { hubLinkHtml } from '../src/lib/hub-url.js';

import {
    packDropPickup, packDropStatusFor, packDropRows, filterOptions, filterRows, countText,
    hubAnchor, rowHtml,
} from '../src/lib/packdrops.js';

const drop = (o) => ({ entNum: 7, time: 61000, player: 'a', team: 'red', weapon: 'rl', ...o });

test('packDropRows reads each drop\'s own pickup side, converting ms to s', () => {
    const result = {
        backpacks: [
            drop({ source: 'ktx', fate: 'picked', picker: 'b', pickerTeam: 'red', pickupTime: 63500, pickerNextDeathTime: 80000, pickerKills: 2, pickerHadWeapon: false }),
            drop({ source: 'ktx', entNum: 8, time: 90000, weapon: 'lg', fate: 'expired' }),
            drop({ source: 'ktx', entNum: 9, time: 95000, fate: 'unobserved' }),
        ],
    };
    const rows = packDropRows(result);
    assert.equal(rows.length, 3);
    assert.equal(rows[0].drop.time, 61);
    assert.deepEqual(rows[0].pickup, { player: 'b', team: 'red', time: 63.5, nextDeathTime: 80, kills: 2, hadBefore: false });
    assert.deepEqual(rows[0].status, { label: 'xfer', cls: 'status-xfer' });
    assert.equal(rows[1].pickup, null);
    assert.equal(rows[1].status.label, 'expired', 'KTX said so');
    assert.equal(rows[2].pickup, null);
    assert.equal(rows[2].status.label, 'unobserved', 'no pickup is not "expired"');
    assert.deepEqual(packDropRows({}), []);
});

test('packDropStatusFor: xfer / enemy, with the weapon when the picker already had it', () => {
    const d = { team: 'red', weapon: 'lg' };
    assert.equal(packDropStatusFor(d, { team: 'red', hadBefore: true }).label, 'xfer LG');
    assert.equal(packDropStatusFor(d, { team: 'blue' }).cls, 'status-enemy');
    assert.equal(packDropStatusFor(d, { team: 'blue', hadBefore: true }).label, 'enemy LG');
    assert.equal(packDropStatusFor({ weapon: 'rl' }, { team: '' }).label, 'enemy', 'no team is not same team');
    const own = { team: 'red', weapon: 'rl', fate: 'picked', pickerRelation: 'self' };
    assert.deepEqual(packDropStatusFor(own, { team: 'red', hadBefore: false }), { label: 'self', cls: 'status-self' }, 'took their own pack back');
    assert.equal(packDropStatusFor({ ...own, source: 'reconstructed' }, null).label, 'self', 'both provenances');
});

test('reconstructed drops carry their own fate; kills stay unknown', () => {
    const r = drop({ source: 'reconstructed', fate: 'picked', picker: 'b', pickerTeam: 'blue', pickupTime: 64000 });
    assert.deepEqual(packDropPickup(r), { player: 'b', team: 'blue', time: 64, nextDeathTime: 0, kills: null, hadBefore: undefined });
    assert.equal(packDropStatusFor(r, packDropPickup(r)).label, 'picked');
    assert.equal(packDropPickup({ ...r, fate: 'expired' }), null);
    assert.equal(packDropStatusFor({ ...r, fate: 'expired' }, null).label, 'expired');
    assert.equal(packDropStatusFor({ ...r, fate: undefined }, null).label, 'unobserved');
    assert.equal(packDropPickup(drop({ fate: 'picked' })), null, 'picked with nobody named');
    const [row] = packDropRows({ backpacks: [r] });
    assert.match(rowHtml(row, {}), /<td class="kills-cell">-<\/td>/);
});

test('filterOptions / filterRows / countText', () => {
    const rows = [
        { drop: { player: 'b', team: 'red' }, pickup: { player: 'Z', team: 'blue' }, status: { label: 'enemy' } },
        { drop: { player: 'A', team: 'red' }, pickup: null, status: { label: 'unobserved' } },
    ];
    const o = filterOptions(rows);
    assert.deepEqual(o.dropplayer, ['A', 'b'], 'case-insensitive sort');
    assert.deepEqual(o.pickteam, ['blue']);
    assert.deepEqual(o.status, ['enemy', 'unobserved']);
    const all = { dropplayer: '', dropteam: '', pickplayer: '', pickteam: '', status: '' };
    assert.equal(filterRows(rows, all).length, 2);
    assert.equal(filterRows(rows, { ...all, pickplayer: 'Z' }).length, 1);
    assert.equal(filterRows(rows, { ...all, status: 'unobserved', dropteam: 'red' })[0].drop.player, 'A');
    assert.equal(countText(2, 2), '2 drops');
    assert.equal(countText(1, 2), '1 of 2 drops');
});

test('hubAnchor: demo-relative whole seconds, a play link, "-" without a hub game or userid', () => {
    const ctx = { gameId: 5, userIDs: { a: 11 }, demoOffset: 15.5 };
    assert.equal(hubAnchor(ctx, 51, 63, 'a'),
        hubLinkHtml('https://hub.quakeworld.nu/games/?gameId=5&from=66&to=78&track=11'));
    assert.match(hubAnchor(ctx, -20, 1, 'a'), /from=0&/);
    assert.equal(hubAnchor({ ...ctx, gameId: null }, 1, 2, 'a'), '-');
    assert.equal(hubAnchor(ctx, 1, 2, 'nobody'), '-');
});

test('rowHtml: time cell, escaped names, run link to the next death or +15 s', () => {
    const ctx = { gameId: 5, userIDs: { '<a>': 1, b: 2 }, demoOffset: 0 };
    const row = {
        drop: { time: 75, player: '<a>', team: 'red', weapon: 'rl' },
        pickup: { player: 'b', team: 'red', time: 80, nextDeathTime: 0, kills: 3, hadBefore: true },
        status: { label: 'xfer RL', cls: 'status-xfer-had' },
    };
    const h = rowHtml(row, ctx);
    assert.match(h, /<td class="time-cell time-link">1:15<\/td>/);
    assert.match(h, /<td>&lt;a&gt;<\/td>/);
    assert.match(h, /from=65&to=77&track=1/);
    assert.match(h, /from=77&to=95&track=2/);
    assert.match(h, /<span class="kills-redundant">3<\/span>/);
    assert.match(h, /<td class="weapon-cell weapon-rl">RL<\/td>/);
    assert.match(h, /<span class="pack-status status-xfer-had">xfer RL<\/span>/);
});
