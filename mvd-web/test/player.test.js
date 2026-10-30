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
const { paneLayout, updateLayout, movePane, showPane, MAX_SPLIT, paneOpt, setPaneOpt, preset, setPreset } = await import('../src/player/layout.js');
const { playlist, addClips, moveClip, removeClip, setRun, setLabel, keepDemos, clearClips } = await import('../src/player/playlist-store.js');
const { createRunner } = await import('../src/player/runner.js');
const { demoRefOf } = await import('../src/player/demo-ref.js');
const { syncMapData } = await import('../src/player/panes/map-sync.js');

test('pane layout migrates the PoC order and sanitizes', () => {
    assert.equal(preset.get(), 'map', 'a fresh visit starts in the Map view');
    assert.deepEqual([paneLayout.get().below, paneLayout.get().order], [['map'], ['players', 'moments', 'items', 'region-status']]);
    setPreset('player');
    assert.equal(preset.get(), 'player');
    assert.deepEqual(paneLayout.get().order, ['feed', 'map'], 'a pre-preset layout is the Player view: v1 order kept, unknown and duplicate ids dropped');
    assert.deepEqual(paneLayout.get().below, ['player', 'playlist'], 'a layout from before the main column gets the default: the player and the playlist (the finder is off)');
    updateLayout((l) => { l.below = ['finder', 'playlist']; });
    updateLayout((l) => { l.split = 99999; l.width = 420; l.order.push('stack', 'stack'); });
    assert.equal(paneLayout.get().split, MAX_SPLIT);
    assert.equal(paneLayout.get().width, undefined, 'the old px width is dropped');
    assert.deepEqual(paneLayout.get().order, ['feed', 'map', 'stack']);
    movePane('stack', 'order', 0);
    assert.deepEqual(paneLayout.get().order, ['stack', 'feed', 'map']);
    movePane('stack', 'order', 2);
    assert.deepEqual(paneLayout.get().order, ['feed', 'map', 'stack'], 'the dragged pane takes the target slot, both ways');
    assert.deepEqual(JSON.parse(mem.get('mvd.playerPanes.v4')).layouts.player.order, ['feed', 'map', 'stack']);
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
    assert.equal(JSON.parse(mem.get('mvd.playerPanes.v4')).layouts.player.opts.score.window, 180);
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

test('keepDemos drops the other demos\' clips and a run over the list', () => {
    clearClips();
    const other = { ...demoA, key: 'hub:2', gameId: 2 };
    addClips([clip(10, 20), clip(30, 40, { demo: other }), clip(50, 60)]);
    setRun({ clips: playlist.get().clips, index: 1, prev: null, transient: false });
    keepDemos(['hub:1']);
    assert.deepEqual(playlist.get().clips.map((c) => c.from), [10, 50]);
    assert.equal(playlist.get().run, null);
    const before = playlist.get();
    keepDemos(['hub:1']);
    assert.equal(playlist.get(), before, 'nothing to drop: no change');
    clearClips();
});

// A stand-in engine with a controllable FTE clock.
function fakeEngine(activeTab) {
    const ticks = new Set();
    const log = [];
    const fte = { t: 0, playing: false, ended: false,
        getDemoElapsedTime() { return this.t; }, isPlaying() { return this.playing; }, hasEnded() { return this.ended; } };
    return {
        fte, log, activeTab,
        active: () => activeTab.get() === 'player',
        ready: () => true,
        isPlaying: () => fte.playing,
        hasEnded: () => fte.ended,
        clock: { seek: (t) => log.push(['seek', t]) },
        offset: () => 10,
        seeking: () => false,
        whenReady: (fn) => fn(),
        jumpToDemoTime: (t) => { log.push(['jump', t]); fte.t = t; },
        track: (u) => log.push(['track', u]),
        play: () => { fte.playing = true; },
        pause: () => { fte.playing = false; log.push(['pause']); },
        slowTo: (sp) => log.push(['slow', sp]),
        onTick: (fn) => { ticks.add(fn); },
        tick: (t) => { fte.t = t; for (const fn of ticks) fn(t); },
    };
}

test('runner plays clips in order and steps on crossing `to`', () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: { streams: { global: { demoOffset: 10000 } } } });
    const teams = store({ order: [] });
    const segment = store(null);
    const runner = createRunner({ engine, page: fakeEngine(store('map')), driverFor: () => engine, demo, teams, selectTab: (t) => activeTab.set(t), loadDemo: async () => {}, demoRefOf, segment });
    const clips = [clip(100, 110, { track: 7 }), clip(200, 210, { demo: { key: 'file:x', gameId: null } }), clip(300, 310)];
    runner.start(clips, 0);
    assert.deepEqual(engine.log.slice(0, 2), [['jump', 100], ['track', 7]]);
    assert.deepEqual(segment.get(), { start: 90, end: 100 }, 'the clip playing is the segment, in match time');
    engine.tick(109.9);
    engine.tick(110.05);
    // clip 2 is another local file: skipped, so clip 3 plays
    assert.deepEqual(engine.log.at(-2), ['jump', 300]);
    assert.equal(playlist.get().run.index, 2);
    engine.tick(309.5);
    engine.tick(310.1);
    assert.equal(playlist.get().run, null, 'the last clip ends the run');
    assert.equal(segment.get(), null, 'and clears its segment');
    assert.deepEqual(engine.log.at(-1), ['pause']);

    runner.start([clip(100, 110)], 0, true);
    segment.set({ start: 1, end: 2 });
    runner.stop();
    assert.deepEqual(segment.get(), { start: 1, end: 2 }, 'a segment the user chose meanwhile stays');
    runner.start([clip(100, 110)], 0, true);
    demo.set({ ref: { key: 'hub:2', gameId: 2 }, result: {} });
    assert.equal(playlist.get().run, null, 'loading another demo by hand ends a run');
});

test('runner plays a clip\'s slow-motion window at its speed, then at the viewer\'s', () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: { streams: { global: { demoOffset: 10000 } } } });
    const runner = createRunner({ engine, page: fakeEngine(store('map')), driverFor: () => engine, demo, teams: store({ order: [] }), selectTab: () => {}, loadDemo: async () => {}, demoRefOf, segment: store(null) });
    runner.start([clip(100, 110, { slow: { from: 104, to: 106, speed: 0.3 } })], 0);
    engine.tick(103);
    engine.tick(104.2);
    engine.tick(105);
    engine.tick(106.1);
    assert.deepEqual(engine.log.filter((e) => e[0] === 'slow'), [['slow', 0.3], ['slow', null]], 'once in, once out');
    engine.tick(104.5);
    engine.fte.playing = false;
    engine.tick(104.6);
    engine.fte.playing = true;
    engine.tick(104.7);
    assert.deepEqual(engine.log.filter((e) => e[0] === 'slow').slice(2), [['slow', 0.3], ['slow', null], ['slow', 0.3]], 'a pause drops it, play inside the window slows again');
    runner.stop();
    assert.deepEqual(engine.log.at(-1), ['slow', null], 'stopping restores the rate');
    runner.start([clip(100, 110, { slow: { from: 104, to: 106, speed: 0.3 } })], 0);
    engine.tick(104.5);
    assert.deepEqual(engine.log.at(-1), ['slow', 0.3]);
    setRun(null);
    assert.deepEqual(engine.log.at(-1), ['slow', null], 'a run the playlist ends (its clip removed, the list cleared) restores it too');
});

test('a run started for the map plays there: no tab switch, the engine untouched', () => {
    const activeTab = store('map');
    const engine = fakeEngine(activeTab);
    const page = fakeEngine(activeTab);
    let driver = page;
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: {} });
    const runner = createRunner({ engine, page, driverFor: () => driver, demo, teams: store({ order: [] }),
        selectTab: (t) => activeTab.set(t), loadDemo: async () => {}, demoRefOf, segment: store(null) });
    runner.start([clip(100, 110, { track: 7, slow: { from: 104, to: 106, speed: 0.3 } }), clip(200, 210)], 0);
    assert.equal(activeTab.get(), 'map', 'stays on the Map tab');
    assert.deepEqual(page.log.slice(0, 2), [['jump', 100], ['track', 7]]);
    engine.tick(104.5);
    assert.deepEqual(page.log.filter((e) => e[0] === 'slow'), [], "the engine's ticks do not step a map run");
    page.tick(104.5);
    assert.deepEqual(page.log.at(-1), ['slow', 0.3]);
    page.tick(109.9);
    page.tick(110.05);
    assert.deepEqual(page.log.at(-2), ['jump', 200], 'steps on to the next clip');
    assert.equal(runner.driver(), page);
    assert.deepEqual(engine.log, [], 'the engine is not driven');
    runner.start([clip(100, 110, { slow: { from: 104, to: 106, speed: 0.3 } })], 0);
    page.tick(104.5);
    activeTab.set('player');
    driver = engine;
    runner.start([clip(100, 110)], 0);
    assert.deepEqual(page.log.at(-1), ['slow', null], 'a run in the other driver leaves the map unslowed');
    assert.equal(runner.driver(), engine);
    runner.stop();
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
    const runner = createRunner({ engine, page: fakeEngine(store('map')), driverFor: () => engine, demo, teams, selectTab: () => {}, loadDemo, demoRefOf, segment: store(null) });
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
    const runner = createRunner({ engine, page: fakeEngine(store('map')), driverFor: () => engine, demo, teams, selectTab: () => {}, loadDemo, demoRefOf, segment: store(null) });
    runner.start([clip(40, 50, { demo: { key: 'hub:9', gameId: 9, title: 'b', offset: 0 } })], 0);
    demo.set({ ref: { key: 'file:mine.mvd', gameId: null }, result: {} });
    assert.ok(playlist.get().run, 'still loading: the run waits for its own load');
    finish(false);
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(playlist.get().run, null, 'the dropped load ends the run');
    assert.ok(!engine.log.some(([k]) => k === 'jump'), 'nothing played on the user\'s demo');
});

test('a title card pauses the game for its time, fetching the next demo meanwhile', async () => {
    const activeTab = store('player');
    const engine = fakeEngine(activeTab);
    const demo = store({ ref: { key: 'hub:1', gameId: 1 }, result: { streams: { global: { demoOffset: 10000 } } } });
    /** @type {any[]} */
    const fetched = [];
    const runner = createRunner({ engine, page: fakeEngine(store('map')), driverFor: () => engine, demo, teams: store({ order: [] }),
        selectTab: () => {}, loadDemo: async () => {}, demoRefOf, segment: store(null), prefetch: (c) => fetched.push(c.demo.gameId) });
    const { makeCard } = await import('../src/lib/playlist.js');
    engine.fte.playing = true;
    const other = clip(50, 60, { demo: { key: 'hub:2', gameId: 2, title: 'b', offset: 0 } });
    runner.start([makeCard({ ms: 400, text: [{ t: 'hi' }] }), clip(100, 110), other], 0);
    assert.deepEqual(engine.log, [['pause']], 'the game pauses behind the card; nothing jumps yet');
    await new Promise((r) => setTimeout(r, 200));
    assert.equal(playlist.get().run.index, 0, 'the card holds for its time');
    await new Promise((r) => setTimeout(r, 300));
    assert.equal(playlist.get().run.index, 1, 'then the next item plays');
    assert.deepEqual(engine.log.slice(1, 3), [['jump', 100], ['track', 'auto']]);
    assert.deepEqual(fetched, [2], 'the clip after it is another demo: fetched while this one plays');
    runner.stop();
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

test('a layout from before the player was a pane gets it on top of the main column', async () => {
    mem.delete('mvd.playerPanes.v4');
    mem.set('mvd.playerPanes.v2', JSON.stringify({ order: ['map'], below: ['finder', 'playlist'], split: 30 }));
    const fresh = await import('../src/player/layout.js?v2-migration');
    fresh.setPreset('player');
    assert.deepEqual([fresh.paneLayout.get().order, fresh.paneLayout.get().below], [['map'], ['player', 'finder', 'playlist']]);
});

test('presets: each view keeps its own layout; reset restores the one in use', async () => {
    mem.delete('mvd.playerPanes.v4');
    mem.delete('mvd.playerPanes.v2');
    const p = await import('../src/player/layout.js?presets');
    p.updateLayout((l) => { l.order.push('stack'); });
    assert.ok(p.paneLayout.get().order.includes('stack'));
    p.setPreset('player');
    assert.ok(!p.paneLayout.get().order.includes('stack'), 'the Player view is untouched');
    p.setPreset('map');
    assert.ok(p.paneLayout.get().order.includes('stack'), 'back to the Map view as it was left');
    p.resetPreset();
    assert.deepEqual(p.paneLayout.get().order, ['players', 'moments', 'items', 'region-status']);
    assert.equal(JSON.parse(mem.get('mvd.playerPanes.v4')).preset, undefined, 'the view in use is not stored');
    p.setPreset('bogus');
    assert.equal(p.preset.get(), 'map', 'an unknown view changes nothing');
});

test('slotBox: the layer over the slot, clipped to its scrolling ancestors', async () => {
    const { slotBox } = await import('../src/player/player-slot.js');
    const r = slotBox({ left: 10, top: 50, width: 200, height: 100 }, [{ left: 0, top: 80, width: 180, height: 500 }]);
    assert.deepEqual(r.box, { left: 10, top: 50, width: 200, height: 100 });
    assert.deepEqual(r.inset, { top: 30, left: 0, bottom: 0, right: 30 });
    assert.ok(r.visible);
    assert.ok(!slotBox({ left: 10, top: 50, width: 200, height: 100 }, [{ left: 0, top: 200, width: 180, height: 50 }]).visible, 'scrolled out of view');
});

test('presets: a v3 layout becomes the Player view; corrupt storage falls back; the tab picks the view', async () => {
    mem.delete('mvd.playerPanes.v4');
    mem.delete('mvd.playerPanes.v2');
    mem.delete('mvd.playerPanes.v1');
    mem.set('mvd.playerPanes.v3', JSON.stringify({ order: ['feed'], below: ['player', 'finder'], split: 25 }));
    const v3 = await import('../src/player/layout.js?v3');
    const { activeTab } = await import('../src/state/ui.js');
    activeTab.set('player');
    assert.equal(v3.preset.get(), 'player', 'the Player tab shows the Player view');
    assert.deepEqual([v3.paneLayout.get().order, v3.paneLayout.get().below], [['feed'], ['player', 'finder']]);
    activeTab.set('map');
    assert.equal(v3.preset.get(), 'map');
    activeTab.set('summary');
    assert.equal(v3.preset.get(), 'map', 'another tab leaves the view as it is');

    mem.delete('mvd.playerPanes.v3');
    mem.set('mvd.playerPanes.v4', '{not json');
    const bad = await import('../src/player/layout.js?corrupt');
    assert.deepEqual(bad.paneLayout.get().below, ['map'], 'corrupt storage: the defaults');

    mem.delete('mvd.playerPanes.v4');
    const fresh = await import('../src/player/layout.js?fresh');
    fresh.setPreset('player');
    assert.deepEqual(fresh.paneLayout.get().order, ['map', 'players', 'moments', 'team-weapons'], 'a fresh Player view, Moments on');
});
