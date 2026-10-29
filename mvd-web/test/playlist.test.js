import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';

import {
    parseHubReplayUrl, hubClipsUrl, singleHubGame, encodePlaylist, decodePlaylist,
    playlistStep, playerFragClips, MAX_HUB_CLIPS, cleanSlow, slowAt,
} from '../src/lib/playlist.js';

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

test('encode/decode round-trips clips across demos', () => {
    const clips = [
        { from: 10, to: 20, track: 3, who: 'milton', label: 'quad run · ä', demo },
        { from: 30, to: 40, track: 'auto', who: '', label: '', demo: { key: 'file:a.mvd', gameId: null, title: 'a', offset: 0 } },
        { from: 50, to: 60, track: 4, who: 'bps', label: 'x', demo },
    ];
    const back = decodePlaylist(encodePlaylist(clips));
    assert.deepEqual(back, clips);
    assert.equal(decodePlaylist('garbage!!'), null);
});

test('decodePlaylist rejects malformed and inconsistent links', () => {
    const enc = (doc) => Buffer.from(JSON.stringify(doc)).toString('base64url');
    assert.equal(decodePlaylist(enc({ v: 1, d: [0], c: [[1, 2, 3, '', '', 0]] })).length, 0, 'non-array demo row');
    assert.equal(decodePlaylist(enc({ v: 1, d: [['hub:5', 6, 't', 0]], c: [[1, 2, 3, '', '', 0]] })).length, 0, 'key/gameId disagree');
    assert.equal(decodePlaylist(enc({ v: 1, d: [['hub:5', '5 or 1=1', 't', 0]], c: [[1, 2, 3, '', '', 0]] })).length, 0, 'gameId not a number');
    assert.equal(decodePlaylist(enc({ v: 2, d: [], c: [] })), null, 'unknown version');
    const ok = decodePlaylist(enc({ v: 1, d: [['hub:5', 5, 't', 1.5]], c: [[1, 2, 'x', '', '', 0]] }));
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

test('slow motion: clamped into the clip, carried by the share link, read per time', () => {
    const demo = { key: 'hub:5', gameId: 5, title: 't', offset: 10 };
    assert.deepEqual(cleanSlow({ from: 20, to: 30 }, 18, 22.04, 0.3), { from: 20, to: 22, speed: 0.3 }, 'clamped, 0.1 s steps');
    assert.equal(cleanSlow({ from: 20, to: 30 }, 31, 33, 0.3), null, 'outside the clip');
    assert.equal(cleanSlow({ from: 20, to: 30 }, 21, 22, 2), null, 'not slow');
    assert.equal(cleanSlow({ from: 20, to: 30 }, '21', 22, 0.3), null, 'untrusted types');
    const clips = [
        { from: 20, to: 30, track: 3, who: 'a', label: 'x', demo, slow: { from: 24, to: 26, speed: 0.25 } },
        { from: 40, to: 50, track: 'auto', who: '', label: 'y', demo },
    ];
    const back = decodePlaylist(encodePlaylist(/** @type {any} */ (clips)));
    assert.deepEqual(back, clips);
    assert.equal(slowAt(clips[0], 24), 0.25);
    assert.equal(slowAt(clips[0], 26), null, 'the window is half-open');
    assert.equal(slowAt(clips[1], 45), null);
});
