import test from 'node:test';
import assert from 'node:assert/strict';

// The modules persist to localStorage; node has none, so give them one.
const mem = new Map();
globalThis.localStorage = {
    getItem: (k) => (mem.has(k) ? mem.get(k) : null),
    setItem: (k, v) => mem.set(k, String(v)),
};
mem.set('mvd.playerPanes.v1', JSON.stringify(['feed', 'map', 'bogus', 'map']));

const { store } = await import('../src/state/store.js');
const { paneLayout, updateLayout, movePane, showPane, MAX_SPLIT, paneOpt, setPaneOpt } = await import('../src/player/layout.js');
const { playlist, addClips, moveClip, removeClip, setRun, setLabel } = await import('../src/player/playlist-store.js');
const { createRunner } = await import('../src/player/runner.js');
const { demoRefOf } = await import('../src/player/demo-ref.js');
const { syncMapData } = await import('../src/player/panes/map-sync.js');

test('pane layout migrates the PoC order and sanitizes', () => {
    assert.deepEqual(paneLayout.get().order, ['feed', 'map'], 'v1 order kept, unknown and duplicate ids dropped');
    assert.deepEqual(paneLayout.get().below, ['finder', 'playlist'], 'a layout from before the finder and playlist were panes gets them under the player');
    updateLayout((l) => { l.split = 99999; l.width = 420; l.order.push('stack', 'stack'); });
    assert.equal(paneLayout.get().split, MAX_SPLIT);
    assert.equal(paneLayout.get().width, undefined, 'the old px width is dropped');
    assert.deepEqual(paneLayout.get().order, ['feed', 'map', 'stack']);
    movePane('stack', 'order', 0);
    assert.deepEqual(paneLayout.get().order, ['stack', 'feed', 'map']);
    movePane('stack', 'order', 2);
    assert.deepEqual(paneLayout.get().order, ['feed', 'map', 'stack'], 'the dragged pane takes the target slot, both ways');
    assert.deepEqual(JSON.parse(mem.get('mvd.playerPanes.v2')).order, ['feed', 'map', 'stack']);
    movePane('playlist', 'order', 1);
    assert.deepEqual([paneLayout.get().order, paneLayout.get().below], [['feed', 'playlist', 'map', 'stack'], ['finder']], 'across columns');
    movePane('playlist', 'below', 99);
    assert.deepEqual(paneLayout.get().below, ['finder', 'playlist'], 'past the end appends');
    updateLayout((l) => { l.below.push('map'); l.collapsed.finder = true; l.below = l.below.filter((x) => x !== 'finder'); });
    assert.deepEqual([paneLayout.get().order, paneLayout.get().below], [['feed', 'stack'], ['playlist', 'map']], 'a pane is in one column only — the column under the player wins');
    showPane('finder');
    assert.deepEqual(paneLayout.get().below, ['playlist', 'map', 'finder'], 'a closed pane opens under the player');
    assert.equal(paneLayout.get().collapsed.finder, undefined, 'expanded');
    showPane('stack');
    assert.deepEqual(paneLayout.get().order, ['feed', 'stack'], 'an open pane stays where it is');
});

test('pane options persist and read back typed', () => {
    setPaneOpt('map', 'vel', true);
    setPaneOpt('score', 'window', 180);
    const l = paneLayout.get();
    assert.equal(paneOpt(l, 'map', 'vel', false), true);
    assert.equal(paneOpt(l, 'map', 'los', false), false, 'unset falls back');
    assert.equal(paneOpt(l, 'score', 'window', 'x'), 'x', 'another type falls back');
    updateLayout((x) => { x.opts.bogus = { a: 1 }; x.opts.feed = 'nope'; });
    assert.deepEqual(Object.keys(paneLayout.get().opts).sort(), ['map', 'score'], 'unknown panes and non-objects dropped');
    assert.equal(JSON.parse(mem.get('mvd.playerPanes.v2')).opts.score.window, 180);
});

const demoA = { key: 'hub:1', gameId: 1, title: 'a', offset: 10 };
const clip = (from, to, extra = {}) => ({ from, to, track: 'auto', who: '', label: `${from}`, demo: demoA, ...extra });

test('playlist edits keep a running list on the same clip', () => {
    addClips([clip(10, 20), clip(30, 40), clip(50, 60)]);
    const s = playlist.get();
    setRun({ clips: s.clips, index: 1, prev: null, transient: false });
    moveClip(1, 0);
    let r = playlist.get().run;
    assert.equal(r.clips, playlist.get().clips, 'the run follows the edited list');
    assert.equal(r.clips[r.index].from, 30);
    removeClip(2);
    r = playlist.get().run;
    assert.equal(r.clips[r.index].from, 30);
    addClips([clip(70, 80)]);
    r = playlist.get().run;
    assert.equal(r.clips, playlist.get().clips, 'a running list reaches clips added while it plays');
    assert.equal(r.clips[r.index].from, 30);
    removeClip(0);
    assert.equal(playlist.get().run, null, 'removing the playing clip ends the run');
    const before = playlist.get();
    setLabel(before.clips[0], 'renamed');
    assert.equal(playlist.get(), before, 'a label edit does not notify');
    assert.equal(JSON.parse(mem.get('mvd.playlist.v1'))[0].label, 'renamed');
});

// A stand-in engine with a controllable FTE clock.
function fakeEngine(activeTab) {
    const ticks = new Set();
    const log = [];
    const fte = { t: 0, playing: false, ended: false,
        getDemoElapsedTime() { return this.t; }, isPlaying() { return this.playing; }, hasEnded() { return this.ended; } };
    return {
        fte, log, activeTab,
        clock: { seek: (t) => log.push(['seek', t]) },
        offset: () => 10,
        seeking: () => false,
        whenReady: (fn) => fn(),
        jumpToDemoTime: (t) => { log.push(['jump', t]); fte.t = t; },
        track: (u) => log.push(['track', u]),
        play: () => { fte.playing = true; },
        pause: () => { fte.playing = false; log.push(['pause']); },
        onTick: (fn) => { ticks.add(fn); },
        tick: (t) => { fte.t = t; for (const fn of ticks) fn(t); },
    };
}

test('runner plays clips in order and steps on crossing `to`', () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: { streams: { global: { demoOffset: 10000 } } } });
    const teams = store({ order: [] });
    const runner = createRunner({ engine, demo, teams, selectTab: (t) => activeTab.set(t), loadDemo: async () => {}, demoRefOf });
    const clips = [clip(100, 110, { track: 7 }), clip(200, 210, { demo: { key: 'file:x', gameId: null } }), clip(300, 310)];
    runner.start(clips, 0);
    assert.deepEqual(engine.log.slice(0, 2), [['jump', 100], ['track', 7]]);
    engine.tick(109.9);
    engine.tick(110.05);
    // clip 2 is another local file: skipped, so clip 3 plays
    assert.deepEqual(engine.log.at(-2), ['jump', 300]);
    assert.equal(playlist.get().run.index, 2);
    engine.tick(309.5);
    engine.tick(310.1);
    assert.equal(playlist.get().run, null, 'the last clip ends the run');
    assert.deepEqual(engine.log.at(-1), ['pause']);

    runner.start([clip(100, 110)], 0, true);
    demo.set({ ref: { key: 'hub:2', gameId: 2 }, result: {} });
    assert.equal(playlist.get().run, null, 'loading another demo by hand ends a run');
});

test('runner loads another hub demo and carries on there', async () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: {} });
    const teams = store({ order: [] });
    const loads = [];
    const loadDemo = async (input) => {
        loads.push(input);
        demo.set({ ref: { key: 'hub:9', gameId: 9 }, result: {} });
        return true;
    };
    const runner = createRunner({ engine, demo, teams, selectTab: () => {}, loadDemo, demoRefOf });
    runner.start([clip(40, 50, { demo: { key: 'hub:9', gameId: 9, title: 'b', offset: 0 } })], 0);
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(loads, [{ gameId: 9 }]);
    assert.ok(playlist.get().run, 'a demo the run loaded keeps it going');
    assert.deepEqual(engine.log.slice(-2), [['jump', 40], ['track', 'auto']]);
});

test('a demo the user picks while the run loads its own wins and ends the run', async () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: {} });
    const teams = store({ order: [] });
    let finish;
    // The run's load is slow; the user's pick lands first, and the run's
    // load then resolves false (superseded — load.js loadDemo).
    const loadDemo = () => new Promise((r) => { finish = r; });
    const runner = createRunner({ engine, demo, teams, selectTab: () => {}, loadDemo, demoRefOf });
    runner.start([clip(40, 50, { demo: { key: 'hub:9', gameId: 9, title: 'b', offset: 0 } })], 0);
    demo.set({ ref: { key: 'file:mine.mvd', gameId: null }, result: {} });
    assert.ok(playlist.get().run, 'still loading: the run waits for its own load');
    finish(false);
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(playlist.get().run, null, 'the dropped load ends the run');
    assert.ok(!engine.log.some(([k]) => k === 'jump'), 'nothing played on the user\'s demo');
});

test('demoRefOf names the demo for the playlist', () => {
    const handle = { ref: { key: 'hub:5', gameId: 5 }, result: { demoInfo: { map: 'maps/dm3.bsp' }, streams: { global: { demoOffset: 2500 } } } };
    assert.deepEqual(demoRefOf(handle, ['a', 'b', 'c', 'd']), { key: 'hub:5', gameId: 5, title: 'dm3 · a vs b vs c', offset: 2.5 });
    assert.equal(demoRefOf(null, []), null);
});

test('map data sync shares data, not view state', () => {
    const geom = {};
    const src = { mapGeometry: geom, locationGroups: [], bounds: { minX: 0 }, frames: { count: 1 }, initialized: true,
        followPlayer: 'x', currentTime: 5, canvas: 'src-canvas', losByPair: { a: 1 }, trailStartTimes: { x: 3 } };
    const dst = { mapGeometry: null, bounds: null, frames: null, followPlayer: null, canvas: 'dst-canvas', trailStartTimes: {},
        _floorModel: 'stale', _frameRecon: 'stale' };
    assert.equal(syncMapData(src, dst), true, 'new geometry asks for a refit');
    assert.equal(dst.mapGeometry, geom);
    assert.equal(dst.frames, src.frames);
    assert.equal(dst._floorModel, null);
    assert.equal(dst._frameRecon, null);
    assert.equal(dst.followPlayer, null);
    assert.equal(dst.canvas, 'dst-canvas');
    assert.equal(dst.losByPair, src.losByPair, 'line of sight is data');
    assert.deepEqual(dst.trailStartTimes, {}, 'trail start times are each map\'s own');
    dst._floorModel = 'built';
    assert.equal(syncMapData(src, dst), false, 'nothing changed');
    assert.equal(dst._floorModel, 'built', 'caches survive an unchanged sync');
});
