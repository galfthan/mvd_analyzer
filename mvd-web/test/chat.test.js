import test from 'node:test';
import assert from 'node:assert/strict';

import {
    CHAT_PX_PER_SEC, CHAT_ITEM_HEIGHT, chatDuration, chatEvents, bucketChat, chatTitles,
    contentHeight, axisTicks, timeLineTop, scrollTarget, stackRow, rowBottom,
    escapeHtml, formatQuakeMessage,
} from '../src/lib/chat.js';

const ev = (time, type, team, player = 'p', message = 'm') => ({ time, type, team, player, message });

test('chatEvents converts ms to seconds; chatDuration defaults to 600 s', () => {
    assert.equal(chatEvents({}), null);
    assert.equal(chatEvents(null), null);
    assert.deepEqual(chatEvents({ messages: { messages: [{ time: 1500, type: 'chat' }] } }),
        [{ time: 1.5, type: 'chat' }]);
    assert.equal(chatDuration({ match: { duration: 1200000 } }), 1200);
    assert.equal(chatDuration({ match: { duration: 0 } }), 600);
    assert.equal(chatDuration(null), 600);
});

test('bucketChat: kills, then say lines by side, clipped to the match window', () => {
    const events = [
        ev(-1, 'frag'), ev(1, 'frag'), ev(2, 'chat', 'red'), ev(3, 'teamsay', 'blue'),
        ev(4, 'chat', 'spec'), ev(5, 'powerup', 'red'), ev(700, 'chat', 'red'), ev(600, 'chat', 'blue'),
    ];
    const { kills, a, b } = bucketChat(events, ['red', 'blue'], 600);
    assert.deepEqual(kills.map(e => e.time), [1]);
    assert.deepEqual(a.map(e => e.time), [2]);
    assert.deepEqual(b.map(e => e.time), [3, 600], 'the end of the window is inside it');
});

test('bucketChat keeps exact repeats (no client-side dedup)', () => {
    const events = [ev(1, 'chat', 'red', 'x', 'gg'), ev(1, 'chat', 'red', 'x', 'gg')];
    assert.equal(bucketChat(events, ['red', 'blue'], 600).a.length, 2);
});

test('bucketChat: hide team chat filters say_team only, by type', () => {
    const events = [ev(1, 'frag'), ev(2, 'teamsay', 'red'), ev(3, 'chat', 'red'), ev(4, 'teamsay', 'blue')];
    const r = bucketChat(events, ['red', 'blue'], 600, { hideTeam: true });
    assert.equal(r.kills.length, 1);
    assert.deepEqual(r.a.map(e => e.type), ['chat']);
    assert.equal(r.b.length, 0);
    const single = bucketChat(events, ['p1', 'p2', 'p3'], 600, { single: true, hideTeam: true });
    assert.deepEqual(single.a.map(e => e.time), [3]);
});

test('bucketChat single column takes every say line, whoever speaks', () => {
    const events = [ev(1, 'chat', 'p3'), ev(2, 'teamsay', 'p1'), ev(3, 'chat', 'spectator')];
    const r = bucketChat(events, ['p1', 'p2', 'p3'], 600, { single: true });
    assert.deepEqual(r.a.map(e => e.time), [1, 2, 3]);
    assert.equal(r.b.length, 0);
});

test('chatTitles', () => {
    assert.deepEqual(chatTitles(['red', 'blue'], false), ['red Chat', 'blue Chat']);
    assert.deepEqual(chatTitles(['a', 'b', 'c'], true), ['Chat', 'Team B Chat']);
    assert.deepEqual(chatTitles([], false), ['Team A Chat', 'Team B Chat']);
});

test('layout: px per second, ticks, line and scroll', () => {
    assert.equal(contentHeight(600), Math.round(600 * CHAT_PX_PER_SEC));
    const ticks = axisTicks(12);
    assert.deepEqual(ticks, [
        { top: 0, label: '0:00' },
        { top: Math.round(5 * CHAT_PX_PER_SEC), label: '0:05' },
        { top: Math.round(10 * CHAT_PX_PER_SEC), label: '0:10' },
    ]);
    assert.equal(axisTicks(600).length, 121);
    assert.equal(timeLineTop(300), 5250);
    assert.equal(scrollTarget(300, 700), 4900);
    assert.equal(scrollTarget(5, 700), 0);
});

test('stackRow pushes an overlapping row below the one above', () => {
    assert.deepEqual(stackRow(10, -Infinity), { top: 175, displaced: false });
    assert.deepEqual(stackRow(10, 180), { top: 180, displaced: true });
    assert.equal(rowBottom(180, 0), 180 + CHAT_ITEM_HEIGHT, 'unmeasured rows take the floor');
    assert.equal(rowBottom(180, 40), 220, 'a wrapped row is as tall as measured');
});

test('formatQuakeMessage renders colour codes, gold text and escapes', () => {
    assert.equal(formatQuakeMessage(''), '');
    assert.equal(formatQuakeMessage('<b>&'), '&lt;b&gt;&amp;');
    assert.equal(formatQuakeMessage('hi!K'), 'hi', 'sound trigger stripped');
    assert.equal(formatQuakeMessage('{x}[y]'), 'xy', 'macro delimiters skipped');
    assert.equal(formatQuakeMessage('&cf00red&rwhite'), '<span style="color:rgb(240,0,0)">red</span>white');
    assert.equal(formatQuakeMessage('&c0f0a&cfffb'), '<span style="color:rgb(0,240,0)">a</span>b');
    assert.equal(formatQuakeMessage(String.fromCharCode(128 + 65)), '<span class="quake-gold">A</span>');
    assert.equal(formatQuakeMessage('&c00f' + String.fromCharCode(128 + 60)), '<span style="color:rgb(0,0,240)">&lt;</span>');
    assert.equal(escapeHtml(`"'`), '&quot;&#39;');
    assert.equal(escapeHtml(null), '');
});
