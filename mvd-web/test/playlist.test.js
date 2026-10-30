import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';

import {
    parseHubReplayUrl, hubClipsUrl, singleHubGame, encodePlaylist, decodePlaylist,
    playlistStep, playerFragClips, MAX_HUB_CLIPS, cleanSlow, slowAt, playlistName, markdownLink,
} from '../src/lib/playlist.js';
import { matchDay } from '../src/lib/wall-clock.js';

const demo = { key: 'hub:212260', gameId: 212260, title: 'dm3', offset: 10.3 };

test('parseHubReplayUrl reads what hubReplayUrl emits', () => {
    assert.deepEqual(
        parseHubReplayUrl('https://hub.quakeworld.nu/games/?gameId=212260&from=130&to=150&track=10'),
        { gameId: 212260, from: 130, to: 150, track: 10 });
    assert.deepEqual(
        parseHubReplayUrl('https://hub.quakeworld.nu/games/?gameId=5&from=7&track=0'),
        { gameId: 5, from: 7, to: null, track: 'auto' }, 'track 0 (no userid) is autotrack');
    assert.equal(parseHubReplayUrl('https://example.com/games/?gameId=5&from=7'), null);
    assert.equal(parseHubReplayUrl('not a url'), null);
});

test('hubClipsUrl writes hub clip params and caps the count', () => {
    const clips = [
        { from: 80, to: 105, track: 8 },
        { from: 330.4, to: 354.2, track: 'auto' },
    ];
    assert.equal(hubClipsUrl(1, clips),
        'https://hub.quakeworld.nu/games/?gameId=1&clip=80-105:8&clip=330-355:auto');
    const many = Array.from({ length: 30 }, (_, i) => ({ from: i * 10, to: i * 10 + 5, track: 1 }));
    assert.equal(new URL(hubClipsUrl(1, many)).searchParams.getAll('clip').length, MAX_HUB_CLIPS);
});

test('singleHubGame only answers for one hub demo', () => {
    assert.equal(singleHubGame([{ demo }, { demo }]), 212260);
    assert.equal(singleHubGame([{ demo }, { demo: { key: 'hub:9', gameId: 9 } }]), null);
    assert.equal(singleHubGame([{ demo: { key: 'file:x.mvd' } }]), null);
    assert.equal(singleHubGame([]), null);
});

test('encode/decode round-trips clips across demos', async () => {
    const clips = [
        { from: 10, to: 20, track: 3, who: 'milton', label: 'quad run · ä', demo },
        { from: 30, to: 40, track: 'auto', who: '', label: '', demo: { key: 'file:a.mvd', gameId: null, title: 'a', offset: 0 } },
        { from: 50, to: 60, track: 4, who: 'bps', label: 'x', demo },
    ];
    const code = await encodePlaylist(clips);
    assert.match(code, /^z[A-Za-z0-9_-]+$/, 'compressed, URL-safe');
    assert.deepEqual(await decodePlaylist(code), clips);
    assert.equal(await decodePlaylist('garbage!!'), null);
    assert.equal(await decodePlaylist('zgarbage'), null);
    const legacy = Buffer.from(JSON.stringify({ v: 1, d: [['hub:1', 1, 't', 0]], c: [[1, 2, 'auto', '', 'old', 0]] })).toString('base64url');
    assert.equal((await decodePlaylist(legacy))[0].label, 'old', 'a link from before the compression still decodes');

    const many = Array.from({ length: 20 }, (_, i) => ({ from: i * 60, to: i * 60 + 25, track: 3 + (i % 4), who: ['gore', 'nexus', 'blaze', 'vegeta'][i % 4],
        label: `Quad run · ${['gore', 'nexus', 'blaze', 'vegeta'][i % 4]} · ${i % 6} frags`, demo }));
    const z = await encodePlaylist(many);
    const raw = (await import('../src/lib/playlist.js')).base64UrlEncode(JSON.stringify({ v: 1, d: [[demo.key, demo.gameId, demo.title, demo.offset]],
        c: many.map((c) => [c.from, c.to, c.track, c.who, c.label, 0]) }));
    assert.ok(z.length < raw.length * 0.6, `about half: ${z.length} vs ${raw.length}`);
});

test('decodePlaylist rejects malformed and inconsistent links', async () => {
    const enc = (doc) => Buffer.from(JSON.stringify(doc)).toString('base64url');
    assert.equal((await decodePlaylist(enc({ v: 1, d: [0], c: [[1, 2, 3, '', '', 0]] }))).length, 0, 'non-array demo row');
    assert.equal((await decodePlaylist(enc({ v: 1, d: [['hub:5', 6, 't', 0]], c: [[1, 2, 3, '', '', 0]] }))).length, 0, 'key/gameId disagree');
    assert.equal((await decodePlaylist(enc({ v: 1, d: [['hub:5', '5 or 1=1', 't', 0]], c: [[1, 2, 3, '', '', 0]] }))).length, 0, 'gameId not a number');
    assert.equal(await decodePlaylist(enc({ v: 2, d: [], c: [] })), null, 'unknown version');
    const ok = await decodePlaylist(enc({ v: 1, d: [['hub:5', 5, 't', 1.5]], c: [[1, 2, 'x', '', '', 0]] }));
    assert.deepEqual(ok, [{ from: 1, to: 2, track: 'auto', who: '', label: '', demo: { key: 'hub:5', gameId: 5, title: 't', offset: 1.5 } }]);
});

test('playlistStep advances on playback crossing `to`, not on a seek past it', () => {
    const clips = [{ from: 10, to: 20 }, { from: 50, to: 60 }];
    assert.deepEqual(playlistStep(clips, 0, 19.9, 20.05, true), { type: 'next', index: 1 });
    assert.deepEqual(playlistStep(clips, 0, 15, 40, true), { type: 'none' }, 'seek past the end');
    assert.deepEqual(playlistStep(clips, 0, 19.9, 20.05, false), { type: 'none' }, 'paused');
    assert.deepEqual(playlistStep(clips, 1, 59.95, 60.1, true), { type: 'end' });
    assert.deepEqual(playlistStep(clips, 0, 12, 55, true), { type: 'follow', index: 1 }, 'seek into another clip');
    assert.deepEqual(playlistStep(clips, 0, null, 20.05, true), { type: 'none' }, 'just jumped');
    const overlap = [{ from: 10, to: 20 }, { from: 17, to: 25 }];
    assert.deepEqual(playlistStep(overlap, 0, 19.9, 20.05, true), { type: 'next', index: 1 }, 'playing into an overlapping next clip still advances to it');
    assert.deepEqual(playlistStep(overlap, 0, 12, 22, true), { type: 'follow', index: 1 }, 'a seek into it follows');
});

// playerFragClips runs against a real pipeline result when the golden
// corpus is present (it is committed, so normally always).
const GOLDEN = new URL('../../mvd-analytics/testdata/golden/4on4_ahoy_bhb_240426_obsidian.json', import.meta.url);
const golden = existsSync(GOLDEN) ? JSON.parse(readFileSync(GOLDEN, 'utf8')) : null;

test('playerFragClips merges kills that overlap', { skip: !golden && 'golden corpus absent' }, () => {
    const name = 'KreatoR';
    const clips = playerFragClips(golden, demo, name);
    const kills = golden.frags.frags.filter((f) => f.killer === name && !f.isSuicide && !f.isTeamKill);
    assert.ok(clips.length > 0 && clips.length <= kills.length);
    for (let i = 1; i < clips.length; i++) assert.ok(clips[i].from >= clips[i - 1].to - 1, 'no overlap left');
    assert.ok(clips.every((c) => c.track === golden.timelineAnalysis.playerUserIDs[name]));
});

test('slow motion: clamped into the clip, carried by the share link, read per time', async () => {
    const demo = { key: 'hub:5', gameId: 5, title: 't', offset: 10 };
    assert.deepEqual(cleanSlow({ from: 20, to: 30 }, 18, 22.04, 0.3), { from: 20, to: 22, speed: 0.3 }, 'clamped, 0.1 s steps');
    assert.equal(cleanSlow({ from: 20, to: 30 }, 31, 33, 0.3), null, 'outside the clip');
    assert.equal(cleanSlow({ from: 20, to: 30 }, 21, 22, 2), null, 'not slow');
    assert.equal(cleanSlow({ from: 20, to: 30 }, '21', 22, 0.3), null, 'untrusted types');
    const clips = [
        { from: 20, to: 30, track: 3, who: 'a', label: 'x', demo, slow: { from: 24, to: 26, speed: 0.25 } },
        { from: 40, to: 50, track: 'auto', who: '', label: 'y', demo },
    ];
    const back = await decodePlaylist(await encodePlaylist(/** @type {any} */ (clips)));
    assert.deepEqual(back, clips);
    assert.equal(slowAt(clips[0], 24), 0.25);
    assert.equal(slowAt(clips[0], 26), null, 'the window is half-open');
    assert.equal(slowAt(clips[1], 45), null);
});

test('playlistName: mvda, the demo, what the list holds', () => {
    const d = { key: 'hub:1', gameId: 1, title: 'dm2 · PEX vs tbg', offset: 10 };
    const c = (label, demo = d) => ({ from: 1, to: 2, track: 'auto', who: '', label, demo });
    const loaded = { key: 'hub:1', date: '2026-03-08' };
    assert.equal(playlistName([c('Quad run · gore · 2 frags'), c('Quad run · nexus · 5 frags'), c('Airgib · a → b')], loaded),
        'mvda · 2026-03-08 · dm2 · PEX vs tbg · Quad run ×2, Airgib');
    assert.equal(playlistName([c('Airgib · a → b')], loaded), 'mvda · 2026-03-08 · dm2 · PEX vs tbg · Airgib · a → b', 'one clip by its label');
    assert.equal(playlistName([c('A'), c('B'), c('C'), c('D')], null), 'mvda · dm2 · PEX vs tbg · 4 clips', 'no date without the demo loaded');
    assert.equal(playlistName([c('A'), c('B', { ...d, key: 'hub:2' })], loaded), 'mvda · 2 demos · A, B');
    assert.equal(markdownLink('x [y]', 'https://u'), '[x (y)](https://u)');
});

test('matchDay: the demoinfo date, else the hedged wall clock', () => {
    assert.equal(matchDay({ demoInfo: { date: '2026-03-08 21:16:56 +0000' } }), '2026-03-08');
    assert.equal(matchDay({ streams: { global: { matchStartUnixMs: Date.UTC(2002, 7, 4, 11), matchStartConfidence: 'unverified' } } }), '~2002-08-04');
    assert.equal(matchDay({}), '');
});
