import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';

import { MvdMap, newState, buildVisByPair, losCovers } from '../src/map.js';
import { fit } from '../src/camera.js';

test('a fresh map has its own state and camera', () => {
    const a = new MvdMap();
    const b = new MvdMap();
    assert.notEqual(a.state, b.state, 'state is per-instance, not shared module scope');
    assert.notEqual(a.camera, b.camera);
    a.state.currentTime = 500;
    assert.equal(b.state.currentTime, 0);
});

test('nested state defaults are not shared between instances', () => {
    const a = new MvdMap();
    const b = new MvdMap();
    a.state.entityFilters.spawn = true;
    a.state.bounds.minX = -99;
    assert.equal(b.state.entityFilters.spawn, false);
    assert.equal(b.state.bounds.minX, 0);
});

test('attach binds a canvas and its 2d context', () => {
    const ctx = {};
    const canvas = { getContext: (kind) => (kind === '2d' ? ctx : null) };
    const map = new MvdMap(canvas);
    assert.equal(map.state.canvas, canvas);
    assert.equal(map.state.ctx, ctx);
});

test('constructing without a canvas leaves the surface unbound', () => {
    const map = new MvdMap();
    assert.equal(map.state.canvas, null);
    assert.equal(map.state.ctx, null);
});

test('the clock starts at zero and paused', () => {
    const s = newState();
    assert.equal(s.currentTime, 0);
    assert.equal(s.isPlaying, false);
    assert.equal(s.playbackSpeed, 1);
});

// The loader invariant (see README): the MCP Apps viewer runs behind a CSP
// with no network origins and inside a host-sized iframe, so a fetch or a
// document lookup anywhere in this package breaks it — and breaks it only
// there, invisibly to every other test. Pin it mechanically.
test('no module in the package loads anything or touches the DOM', () => {
    const dir = new URL('../src/', import.meta.url);
    const banned = [
        /\bfetch\s*\(/, /\bXMLHttpRequest\b/, /\bimport\s*\(/,
        /\bdocument\s*\./, /\bwindow\s*\./, /\blocation\s*\./,
        /\blocalStorage\b/, /\bnavigator\s*\./,
    ];
    for (const file of readdirSync(dir).filter(f => f.endsWith('.js'))) {
        const src = readFileSync(new URL(file, dir), 'utf8')
            // Strip comments — the rule is about code, and the comments
            // discuss exactly these words.
            .replace(/\/\*[\s\S]*?\*\//g, '')
            .split('\n').filter(l => !l.trim().startsWith('//')).join('\n');
        for (const re of banned) {
            assert.equal(re.test(src), false, `${file} violates the loader invariant: ${re}`);
        }
    }
});

test('the package uses no webfonts — only generic families', () => {
    const dir = new URL('../src/', import.meta.url);
    for (const file of readdirSync(dir).filter(f => f.endsWith('.js'))) {
        const src = readFileSync(new URL(file, dir), 'utf8');
        for (const m of src.matchAll(/font\s*=\s*[`'"]([^`'"]*)[`'"]/g)) {
            assert.match(m[1], /(monospace|sans-serif|serif)\s*$/,
                `${file} names a specific font family: ${m[1]}`);
        }
    }
});

// ─── World-layer behaviour ──────────────────────────────────────────────────

test('focusTier classifies the focused loc, its neighbours and the rest', () => {
    const map = new MvdMap();
    assert.equal(map.focusTier('RA.entry'), null, 'no focus → no tier');
    map.state.focusGroupName = 'RA.entry';
    map.state.focusNeighbors = new Set(['RA.tunnel']);
    assert.equal(map.focusTier('RA.entry'), 'focus');
    assert.equal(map.focusTier('RA.tunnel'), 'near');
    assert.equal(map.focusTier('bridge'), 'far');
});

test('farFadePredicate is null unless something is focused', () => {
    const map = new MvdMap();
    assert.equal(map.farFadePredicate(), null);
    map.state.focusGroupName = 'RA.entry';
    const p = map.farFadePredicate();
    assert.equal(p('bridge'), true);
    assert.equal(p('RA.entry'), false);
});

test('tierFill brightens the focus, fades the far, and leaves the base alone', () => {
    const map = new MvdMap();
    const group = { color: { fill: 'rgba(10,20,30,0.1)' } };
    assert.equal(map.tierFill(group, null), 'rgba(10,20,30,0.1)');
    const focus = parseFloat(map.tierFill(group, 'focus').split(',')[3]);
    const near  = parseFloat(map.tierFill(group, 'near').split(',')[3]);
    const far   = parseFloat(map.tierFill(group, 'far').split(',')[3]);
    assert.ok(focus > near && near > 0.1 && far < 0.1, `${focus} ${near} ${far}`);
    assert.ok(focus <= 0.5, 'focus alpha is capped so a region never goes opaque');
});

test('iconScale ramps with zoom and caps at 1.5', () => {
    const map = new MvdMap();
    assert.equal(map.iconScale(), 1);
    map.camera.zoomK = 0.5;
    assert.equal(map.iconScale(), 1, 'zooming out never shrinks labels');
    map.camera.zoomK = 2;
    assert.ok(map.iconScale() > 1 && map.iconScale() < 1.5);
    map.camera.zoomK = 100;
    assert.equal(map.iconScale(), 1.5);
});

test('floorModel rebuilds only when geometry or groups change', () => {
    const map = new MvdMap();
    const tris = [0, 0, 0, 10, 0, 0, 10, 10, 0];
    map.state.mapGeometry = { backdropTris: tris };
    map.state.locationGroups = [];
    const first = map.floorModel();
    assert.ok(first);
    assert.equal(map.floorModel(), first, 'cached across calls');
    map.state.locationGroups = [{ name: 'RA.entry', tris }];
    assert.notEqual(map.floorModel(), first, 'new groups invalidate it');
    map.state.mapGeometry = { backdropTris: tris };   // different object
    assert.notEqual(map.floorModel(), first, 'new geometry invalidates it');
});

test('mover face normals and bboxes are computed once per submodel', () => {
    const map = new MvdMap();
    map.state.submodelMeshes = { 7: [0, 0, 0, 10, 0, 0, 10, 10, 4] };
    const faces = map.moverMeshFaces(7);
    assert.equal(faces.length, 1);
    assert.equal(map.moverMeshFaces(7), faces, 'cached by id');
    const bb = map.moverLocalBBox(7);
    assert.deepEqual(bb, { minX: 0, maxX: 10, minY: 0, maxY: 10, minZ: 0, maxZ: 4 });
    assert.equal(map.moverLocalBBox(7), bb);
});

test('playerOnMover needs both the footprint and the height window', () => {
    const map = new MvdMap();
    map.state.submodelMeshes = { 1: [0, 0, 0, 100, 0, 0, 100, 100, 0] };
    const pose = { x: 0, y: 0, z: 0 };
    const on = (p) => map.playerOnMover(pose, 1, [p]);
    assert.equal(on({ x: 50, y: 50, z: 10 }), true, 'standing on it');
    assert.equal(on({ x: 500, y: 50, z: 10 }), false, 'beside it');
    assert.equal(on({ x: 50, y: 50, z: 500 }), false, 'far above it');
    assert.equal(on({ x: 50, y: 50, z: -500 }), false, 'far below it');
});

test('livingPlayersAtFrame drops the dead and the unpositioned', () => {
    const map = new MvdMap();
    map.state._framePlayerData = {
        alive:   { x: 1, y: 2, z: 3, h: 100 },
        dead:    { x: 1, y: 2, z: 3, d: true },
        zeroHp:  { x: 1, y: 2, z: 3, h: 0 },
        noPos:   { x: 0, y: 0, z: 0, h: 100 },
        missing: null,
    };
    const living = map.livingPlayersAtFrame();
    assert.equal(living.length, 1);
    assert.equal(living[0].h, 100);
});

test('livingPlayersAtFrame is empty before a frame has been composed', () => {
    assert.deepEqual(new MvdMap().livingPlayersAtFrame(), []);
});

test('the world bake takes its scratch canvas from the target canvas document', () => {
    // Not `document.createElement` — the package must not touch the ambient
    // document (loader invariant), and in an MCP iframe the canvas may belong
    // to a document this code never sees a global for.
    let asked = 0;
    const canvas = {
        getContext: () => ({}),
        ownerDocument: { createElement: (t) => { asked++; return { tag: t }; } },
    };
    const map = new MvdMap(canvas);
    assert.deepEqual(map.scratchCanvas(), { tag: 'canvas' });
    assert.equal(asked, 1);
});

// ─── Actor layers ───────────────────────────────────────────────────────────

// Item phases are int32 ms; the renderer's clock is seconds, so every one of
// these paths promotes the clock before comparing. A unit mix-up here would
// show a whole map of items in the wrong state.
const ITEM = { phases: [{ availableFrom: 0, takenAt: 10_000, respawnAt: 30_000 }] };

test('isItemUp brackets the taken window', () => {
    const map = new MvdMap();
    assert.equal(map.isItemUp(ITEM, 5), true, 'before it was taken');
    assert.equal(map.isItemUp(ITEM, 10), false, 'at the instant it was taken');
    assert.equal(map.isItemUp(ITEM, 20), false, 'mid respawn');
    assert.equal(map.isItemUp(ITEM, 30), true, 'at respawn');
});

test('isItemUp treats an item with no phase history as up', () => {
    const map = new MvdMap();
    assert.equal(map.isItemUp({ phases: [] }, 100), true);
    assert.equal(map.isItemUp({}, 100), true);
});

test('isItemUp keeps an open phase up, and a pending MH down', () => {
    const map = new MvdMap();
    assert.equal(map.isItemUp({ phases: [{ availableFrom: 0, takenAt: 0 }] }, 500), true);
    // MH in its rot window: taken, no respawn scheduled yet.
    assert.equal(map.isItemUp({ phases: [{ availableFrom: 0, takenAt: 1000, respawnAt: 0 }] }, 5), false);
});

test('itemStatus reports the wait in seconds and flags the pending MH', () => {
    const map = new MvdMap();
    const up = map.itemStatus(ITEM, 5);
    assert.equal(up.up, true);
    assert.equal(up.secsToRespawn, null, 'no countdown while it is up');
    const down = map.itemStatus(ITEM, 20);
    assert.equal(down.up, false);
    assert.equal(down.secsToRespawn, 10, 'seconds, not milliseconds');
    const mh = map.itemStatus({ phases: [{ availableFrom: 0, takenAt: 1000, respawnAt: 0 }] }, 5);
    assert.equal(mh.pending, true);
});

test('entityCategory maps item kinds and folds both teleporter ends together', () => {
    const map = new MvdMap();
    assert.equal(map.entityCategory({ type: 'item', kind: 'rl' }), 'weapon');
    assert.equal(map.entityCategory({ type: 'item', kind: 'ra' }), 'armor');
    assert.equal(map.entityCategory({ type: 'item', kind: 'nosuch' }), 'item');
    assert.equal(map.entityCategory({ type: 'teleportSrc' }), 'teleporter');
    assert.equal(map.entityCategory({ type: 'teleportDst' }), 'teleporter');
    assert.equal(map.entityCategory({ type: 'spawn' }), 'spawn');
});

test('buildTeleportArrows pairs each entrance with its named exit', () => {
    const map = new MvdMap();
    map.state.mapEntities = [
        { type: 'teleportSrc', target: 'tp1', x: 0, y: 0, z: 0 },
        { type: 'teleportDst', targetName: 'tp1', x: 100, y: 50, z: 24 },
        { type: 'teleportSrc', target: 'nowhere', x: 5, y: 5, z: 0 },
    ];
    map.buildTeleportArrows();
    assert.equal(map.state.teleportArrows.length, 1, 'the unmatched entrance is dropped');
    const a = map.state.teleportArrows[0];
    assert.deepEqual([a.sx, a.sy, a.sz], [0, 0, 0]);
    assert.deepEqual([a.dx, a.dy, a.dz], [100, 50, 24]);
});

test('getActiveBadges surfaces held powerups and weapons, and colours armour by type', () => {
    const map = new MvdMap();
    const letters = (d) => map.getActiveBadges(d).map(b => b.letter).sort();
    assert.deepEqual(letters({}), []);
    assert.deepEqual(letters({ q: true, rl: true }), ['Q', 'R']);
    // The armour badge is keyed off the armour TYPE, not the amount, and its
    // letter is that type — so it clears when the type does, which is what the
    // armour stream reports once a vest is chewed through.
    const armour = map.getActiveBadges({ a: 100, at: 'ra' }).find(b => b.letter === 'RA');
    assert.equal(armour.color, 'rgb(255, 50, 50)', 'red armour reads red');
    assert.deepEqual(letters({ a: 0, at: '' }), []);
});

test('getActiveBadges distinguishes the two nailgun tiers on one badge', () => {
    const map = new MvdMap();
    // sng and ssg share a slot: super-nailgun wins, and each has its own letter.
    assert.deepEqual(map.getActiveBadges({ sng: true }).map(b => b.letter), ['N']);
    assert.deepEqual(map.getActiveBadges({ ssg: true }).map(b => b.letter), ['S']);
    assert.deepEqual(map.getActiveBadges({ sng: true, ssg: true }).map(b => b.letter), ['N']);
});

test('the mega badge tracks overhealth rather than a pickup flag', () => {
    const map = new MvdMap();
    assert.deepEqual(map.getActiveBadges({ h: 100 }).map(b => b.letter), []);
    assert.deepEqual(map.getActiveBadges({ h: 101 }).map(b => b.letter), ['M']);
});

test('streamPosAt carries the last sample forward and reports no track as null', () => {
    const map = new MvdMap();
    map.state.posStreams = {
        nlk: { t: [0, 1000, 2000], x: [0, 10, 20], y: [0, 0, 0], z: [0, 0, 0] },
    };
    assert.equal(map.streamPosAt('missing', 0), null);
    assert.equal(map.streamPosAt('nlk', 1500).x, 10, 'carry-forward, not interpolation');
    assert.equal(map.streamPosAt('nlk', 99999).x, 20);
});

test('the team palette defaults to the canonical order and can be overridden', () => {
    assert.equal(new MvdMap().teamColors[0], '#ff5050');
    const custom = new MvdMap(null, { teamColors: ['#111', '#222'] });
    assert.equal(custom.teamColors[1], '#222');
});

// ─── Overlays and hit testing ───────────────────────────────────────────────

test('losCovers honours the half-open interval convention', () => {
    const iv = [{ s: 1000, e: 2000 }, { s: 5000, e: 6000 }];
    assert.equal(losCovers(iv, 999), false);
    assert.equal(losCovers(iv, 1000), true, 'inclusive start');
    assert.equal(losCovers(iv, 1999), true);
    assert.equal(losCovers(iv, 2000), false, 'exclusive end');
    assert.equal(losCovers(iv, 5500), true, 'later interval');
    assert.equal(losCovers(null, 1500), false, 'no data → not covered');
});

test('buildVisByPair resolves the other-player index to a name per direction', () => {
    const players = [
        { name: 'a', los: [{ other: 1, intervals: [{ s: 0, e: 100 }] }] },
        { name: 'b', los: [] },
        null,
    ];
    const byPair = buildVisByPair(players, 'los');
    assert.deepEqual(byPair.a.b, [{ s: 0, e: 100 }]);
    assert.equal(byPair.b?.a, undefined, 'asymmetric — b never saw a');
    assert.deepEqual(buildVisByPair('junk', 'los'), {}, 'non-array reply → empty');
});

test('regionActiveTint colours a lone team and whites out a contested region', () => {
    const map = new MvdMap(null, { teamColors: ['#ff0000', '#0000ff'] });
    assert.equal(map.regionActiveTint(new Set([1])), 'rgba(0, 0, 255, 0.3)');
    assert.match(map.regionActiveTint(new Set([0, 1])), /rgba\(235, 235, 245/);
    assert.match(map.regionActiveTint(new Set()), /rgba\(235, 235, 245/);
});

test('resolvePlayerLoc prefers the interned index, then the resolved name', () => {
    const map = new MvdMap();
    map.state.locTable = ['', 'RA.entry'];
    map.state.locations = [{ name: 'fallback', x: 0, y: 0 }];
    assert.equal(map.resolvePlayerLoc({ li: 1 }), 'RA.entry');
    assert.equal(map.resolvePlayerLoc({ location: 'bridge' }), 'bridge');
    assert.equal(map.resolvePlayerLoc({ x: 1, y: 1 }), 'fallback', '2D nearest fallback');
});

test('computeOccupiedGroupTeams keys living players by normalized loc and team', () => {
    const map = new MvdMap();
    map.state.locTable = ['', 'RA Entry'];
    map.state.playerSymbols = { p1: { teamIdx: 0 }, p2: { teamIdx: 1 }, p3: { teamIdx: 1 } };
    const occupied = map.computeOccupiedGroupTeams({
        p1: { x: 10, y: 10, li: 1, h: 100 },
        p2: { x: 20, y: 20, li: 1, h: 100 },
        p3: { x: 30, y: 30, li: 1, h: 0 },   // dead — excluded
        p4: null,
    });
    assert.equal(occupied.size, 1);
    const teams = occupied.values().next().value;
    assert.deepEqual([...teams].sort(), [0, 1], 'both teams present, the dead man absent');
});

// A camera fitted to a 100×100 world on a 100×100 canvas at top-down maps
// world (x, y) → canvas (x, 100 - y) — easy to reason about in tests.
function topDownMap() {
    const map = new MvdMap();
    fit(map.camera, { minX: 0, maxX: 100, minY: 0, maxY: 100 }, 100, 100);
    return map;
}

test('pickLocGroupAt resolves stacked floors to the nearer one', () => {
    const map = topDownMap();
    const square = (z) => [10, 10, z, 90, 10, z, 90, 90, z, 10, 10, z, 90, 90, z, 10, 90, z];
    map.state.locationGroups = [
        { name: 'low',  tris: square(0) },
        { name: 'high', tris: square(128) },
        { name: 'thin', tris: [0, 0, 0] },   // malformed — skipped
    ];
    assert.equal(map.pickLocGroupAt(50, 50), 'high', 'top-down: higher z wins');
    assert.equal(map.pickLocGroupAt(5, 5), null, 'outside every region');
});

test('hitTestPlayerSymbol picks the nearest drawn symbol within the radius', () => {
    const map = topDownMap();
    map.state.posStreams = {
        streamed: { t: [0], x: [40], y: [40], z: [0] },
    };
    const bucket = {
        streamed: { x: 90, y: 90, z: 0 },     // stale bucket pos — stream wins
        bucketed: { x: 60, y: 60, z: 0 },
        parked:   { x: 0, y: 0, z: 0 },       // unpositioned — ignored
    };
    // Streamed player draws at canvas (40, 60); bucketed at (60, 40).
    assert.equal(map.hitTestPlayerSymbol(41, 61, 0, bucket), 'streamed');
    assert.equal(map.hitTestPlayerSymbol(59, 41, 0, bucket), 'bucketed');
    assert.equal(map.hitTestPlayerSymbol(5, 5, 0, bucket), null, 'nothing in radius');
    assert.equal(map.hitTestPlayerSymbol(41, 61, 0, null), null, 'no frame data');
});

test('drawTracks pulls a trail start back when the clock rewinds past it', () => {
    const map = new MvdMap();
    const noop = () => {};
    const ctx = new Proxy({}, { get: () => noop, set: () => true });
    map.state.fullTrails = {
        p1: [{ wx: 0, wy: 0, wz: 0, t: 5, teamIdx: 0 }, { wx: 10, wy: 0, wz: 0, t: 6, teamIdx: 0 }],
    };
    map.state.enabledPlayers = { p1: true };
    map.state.trailStartTimes = { p1: 8 };
    map.drawTracks(ctx, 6);
    assert.equal(map.state.trailStartTimes.p1, 6, 'start follows the rewound clock');
});
