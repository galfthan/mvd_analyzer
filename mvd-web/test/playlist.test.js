import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';

import {
    parseHubReplayUrl, hubClipsUrl, singleHubGame, encodePlaylist, decodePlaylist,
    playlistStep, GENERATORS, playerFragClips, MAX_HUB_CLIPS,
} from '../static/playlist.js';

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

test('playlistStep advances on playback crossing `to`, not on a seek past it', () => {
    const clips = [{ from: 10, to: 20 }, { from: 50, to: 60 }];
    assert.deepEqual(playlistStep(clips, 0, 19.9, 20.05, true), { type: 'next', index: 1 });
    assert.deepEqual(playlistStep(clips, 0, 15, 40, true), { type: 'none' }, 'seek past the end');
    assert.deepEqual(playlistStep(clips, 0, 19.9, 20.05, false), { type: 'none' }, 'paused');
    assert.deepEqual(playlistStep(clips, 1, 59.95, 60.1, true), { type: 'end' });
    assert.deepEqual(playlistStep(clips, 0, 12, 55, true), { type: 'follow', index: 1 }, 'seek into another clip');
    assert.deepEqual(playlistStep(clips, 0, null, 20.05, true), { type: 'none' }, 'just jumped');
});

// The generators run against a real pipeline result when the golden corpus
// is present (it is committed, so normally always).
const GOLDEN = new URL('../../mvd-analytics/testdata/golden/4on4_ahoy_bhb_240426_obsidian.json', import.meta.url);
const golden = existsSync(GOLDEN) ? JSON.parse(readFileSync(GOLDEN, 'utf8')) : null;

test('generators produce in-order, demo-relative clips', { skip: !golden && 'golden corpus absent' }, () => {
    const off = golden.streams.global.demoOffset / 1000;
    const quads = GENERATORS.quadRuns.build(golden, demo);
    const runs = golden.timelineAnalysis.powerupEvents.filter((e) => e.powerupType === 'quad');
    assert.equal(quads.length, runs.length);
    assert.ok(quads.length > 0);
    const first = runs.slice().sort((a, b) => a.time - b.time)[0];
    assert.equal(quads[0].from, Math.floor((first.time - 3000) / 1000 + off));
    assert.equal(quads[0].track, first.playerUserID);
    assert.equal(quads[0].who, first.playerName);
    for (let i = 1; i < quads.length; i++) assert.ok(quads[i].from >= quads[i - 1].from);
    for (const c of quads) assert.ok(c.to > c.from);

    for (const key of Object.keys(GENERATORS)) {
        for (const c of GENERATORS[key].build(golden, demo)) {
            assert.ok(c.to > c.from, `${key}: empty clip`);
            assert.deepEqual(c.demo, demo);
        }
    }
});

test('playerFragClips merges kills that overlap', { skip: !golden && 'golden corpus absent' }, () => {
    const name = 'KreatoR';
    const clips = playerFragClips(golden, demo, name);
    const kills = golden.frags.frags.filter((f) => f.killer === name && !f.isSuicide && !f.isTeamKill);
    assert.ok(clips.length > 0 && clips.length <= kills.length);
    for (let i = 1; i < clips.length; i++) assert.ok(clips[i].from >= clips[i - 1].to - 1, 'no overlap left');
    assert.ok(clips.every((c) => c.track === golden.timelineAnalysis.playerUserIDs[name]));
});
