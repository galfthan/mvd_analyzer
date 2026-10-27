import test from 'node:test';
import assert from 'node:assert/strict';

import { FteController, AUTOTRACK_OFF, AUTOTRACK_ON } from '../src/fte-controller.js';
import { demoAssets } from '../src/assets.js';

// A stand-in for the window ftewebgl publishes into: FTEC.cbufadd records
// the console commands, Module answers the clock and the tracked player.
function fakeWin({ demoTime = 0, trackUserid = 3 } = {}) {
    const win = {
        commands: [],
        demoTime,
        FTEC: { cbufadd: (cmd) => win.commands.push(cmd) },
        Module: {
            getDemoTime: () => win.demoTime,
            getClientState: () => ({
                getPlayerView: () => ({ getTrackedPlayer: () => ({ userid: trackUserid }) }),
            }),
        },
    };
    return win;
}

test('demoJump sends whole seconds and skips sub-second jumps', () => {
    const win = fakeWin({ demoTime: 100.4 });
    const fte = new FteController(win);
    fte.demoJump(100.9);
    assert.deepEqual(win.commands, [], 'same second — nothing sent');
    fte.demoJump(250.7);
    assert.deepEqual(win.commands, ['demo_jump 250\n']);
});

test('demoJump clamps to [0, duration + 1.1]', () => {
    const win = fakeWin({ demoTime: 100 });
    const fte = new FteController(win, { demoDuration: 600 });
    fte.demoJump(-20);
    fte.demoJump(5000);
    assert.deepEqual(win.commands, ['demo_jump 0\n', 'demo_jump 601\n']);
});

test('a rewind re-applies track and speed once the clock has arrived', async () => {
    const win = fakeWin({ demoTime: 300 });
    const fte = new FteController(win);
    fte.track(7);
    fte.setDemoSpeed(50);
    win.commands.length = 0;

    fte.demoJump(120);
    assert.deepEqual(win.commands, ['demo_jump 120\n']);
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 120\n'], 'clock not there yet — nothing restored');

    win.demoTime = 120.2;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 120\n', 'track 7\n', 'demo_setspeed 50\n']);
});

test('a rewind restores even when the first reading overshoots the target', async () => {
    const win = fakeWin({ demoTime: 300 });
    const fte = new FteController(win);
    fte.track(7);
    win.commands.length = 0;
    fte.demoJump(65);
    win.demoTime = 67.4;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 65\n', 'track 7\n', 'demo_setspeed 100\n']);
});

test('a one-second rewind restores once it lands', async () => {
    const win = fakeWin({ demoTime: 66.05 });
    const fte = new FteController(win);
    fte.track(8);
    win.commands.length = 0;
    fte.demoJump(65);
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 65\n'], 'clock still at the start — the jump has not landed');
    win.demoTime = 65.02;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 65\n', 'track 8\n', 'demo_setspeed 100\n']);
});

test('a forward jump restores nothing', async () => {
    const win = fakeWin({ demoTime: 100 });
    const fte = new FteController(win);
    fte.demoJump(200);
    win.demoTime = 200;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['demo_jump 200\n']);
});

test('track leaves autotrack; enableAutotrack returns to it', () => {
    const win = fakeWin({ trackUserid: 3 });
    const fte = new FteController(win);
    assert.equal(fte.isUsingAutotrack(), true);
    fte.track('9');
    assert.equal(fte.isUsingAutotrack(), false);
    fte.enableAutotrack();
    assert.equal(fte.isUsingAutotrack(), true);
    assert.deepEqual(win.commands, [
        `cl_autotrack ${AUTOTRACK_OFF}\n`, 'track 9\n', `cl_autotrack ${AUTOTRACK_ON}\n`,
    ]);
});

test('pause and play remember the last running speed', () => {
    const win = fakeWin();
    const fte = new FteController(win);
    fte.setDemoSpeed(500);
    fte.pause();
    assert.equal(fte.isPlaying(), false);
    fte.play();
    assert.equal(fte.getDemoSpeed(), 500);
});

test('demoAssets places the demo, map and config where config.cfg expects them', () => {
    const a = demoAssets({ demoUrl: 'blob:demo', demoPath: 'qw/match.mvd.gz', mapName: 'dm3', configUrl: 'cfg' });
    assert.equal(a['qw/match.mvd.gz'], 'blob:demo');
    assert.equal(a['id1/config.cfg'], 'cfg');
    assert.equal(a['id1/maps/dm3.bsp'], 'https://assets.quake.world/maps/dm3.bsp');
    assert.equal(a['id1/gfx.wad'], 'https://a.quake.world/fte/id1/gfx.wad');
});

test('a seek after the demo ended restarts it, then jumps and restores', async () => {
    const win = fakeWin({ demoTime: 1216.3 });
    const fte = new FteController(win, { demoName: 'match' });
    fte.track(7);
    fte.setDemoSpeed(50);
    fte.demoEnded();
    assert.equal(fte.isPlaying(), false, 'an ended demo is not playing');
    win.commands.length = 0;

    fte.demoJump(200);
    assert.deepEqual(win.commands, ['playdemo match\n']);
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['playdemo match\n'], 'old clock still showing — wait');

    win.demoTime = 0.4;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['playdemo match\n', 'demo_jump 200\n']);
    assert.equal(fte.isRestarting(), true, 'still fast-forwarding to the target');

    win.demoTime = 200.1;
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(win.commands, ['playdemo match\n', 'demo_jump 200\n', 'track 7\n', 'demo_setspeed 50\n']);
    assert.equal(fte.isRestarting(), false);
    assert.equal(fte.hasEnded(), false);
    assert.equal(fte.isPlaying(), true);
});

test('unmute resumes audio and picks an audible volume', () => {
    const win = fakeWin();
    let resumed = 0;
    const fte = new FteController(win, { resumeAudio: () => resumed++ });
    fte.mute();
    fte.toggleMute();
    assert.equal(resumed, 1);
    assert.equal(fte.isMuted(), false);
    assert.deepEqual(win.commands, ['volume 0\n', 'volume 0.1\n']);
});
