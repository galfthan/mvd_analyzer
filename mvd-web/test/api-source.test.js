import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { ApiSource, SchemaMismatchError, viewParams } from '../src/data/api-source.js';
import { PageSource } from '../src/data/page-source.js';
import { boundedOrRaw } from '../src/lib/clip-query.js';

const tick = () => new Promise((r) => setTimeout(r, 0));

// A position pack with no players (lib/postrack.js).
function emptyPack() {
    const buf = new ArrayBuffer(12);
    const dv = new DataView(buf);
    [...'MVDP'].forEach((c, i) => dv.setUint8(i, c.charCodeAt(0)));
    dv.setUint32(4, 1, true);
    return buf;
}

/** @param {string} name */
function fixture(name) {
    const b = readFileSync(new URL(`./fixtures/${name}`, import.meta.url));
    return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
}

/**
 * A fetch standing in for the page's /api, answering by path. Each route is
 * (url, init) → { status, json, body, headers }; unknown paths 404.
 * @param {Record<string, (url: URL, init: RequestInit) => any>} routes
 */
function fakeApi(routes) {
    const calls = [];
    const fetchImpl = async (/** @type {string} */ url, /** @type {RequestInit} */ init = {}) => {
        const u = new URL(url, 'http://page/');
        calls.push({ url: u, init });
        const route = Object.entries(routes).find(([p]) => u.pathname === p || u.href.includes(p));
        const r = route ? await route[1](u, init) : { status: 404, json: { error: { code: 'not_found', message: 'no route' } } };
        const status = r.status ?? 200;
        return {
            ok: status >= 200 && status < 300,
            status,
            headers: new Map(Object.entries(r.headers || {})),
            json: async () => r.json,
            text: async () => JSON.stringify(r.json),
            arrayBuffer: async () => r.body,
        };
    };
    return { fetchImpl, calls };
}

const GAME = { id: 7, map: 'dm2', demo_sha256: 'abcdef', timestamp: '2026-01-02T03:04:05Z' };
const resultRoutes = (extra = {}) => ({
    '/api/demos/gameId:7/result': () => ({ json: { schemaVersion: 78, match: { map: 'dm2' } }, headers: { 'X-Schema-Version': '78' } }),
    '/api/demos/gameId:7/streams/positions': () => ({ body: emptyPack() }),
    ...extra,
});

test('the API source reads the Result and positions for a hub game', async () => {
    const { fetchImpl, calls } = fakeApi(resultRoutes());
    const h = await new ApiSource({ fetchImpl, schema: 78 }).load(GAME, { gameInfoFetch: 3 });
    assert.deepEqual(await h.section('match'), { map: 'dm2' });
    assert.deepEqual(h.ref, { key: 'hub:7', gameId: 7 });
    assert.equal(h.game, GAME);
    assert.equal(h.timings.net.gameInfoFetch, 3);
    assert.ok(h.timings.net.resultFetch >= 0);
    assert.ok(calls.every((c) => c.url.pathname.startsWith('/api/')), 'every call through the page\'s /api');
});

test('another schema version than the page\'s is a mismatch the page can fall back from', async () => {
    const { fetchImpl } = fakeApi(resultRoutes());
    await assert.rejects(new ApiSource({ fetchImpl, schema: 77 }).load(GAME, {}),
        (e) => e instanceof SchemaMismatchError && e.canFallBack && /v78.*v77/.test(e.message));
    // A dev build (no schema stamped) takes what it is served.
    await new ApiSource({ fetchImpl, schema: null }).load(GAME, {});
});

test('ready(buckets) puts the pack view and the region states on the Result', async () => {
    const { fetchImpl, calls } = fakeApi(resultRoutes({
        '/api/demos/gameId:7/result': () => ({ json: { timelineAnalysis: { regionControl: { teamA: 'a', teamB: 'b' } } }, headers: {} }),
        '/api/demos/gameId:7/buckets': () => ({ body: fixture('bucket-pack.bin') }),
        '/api/demos/gameId:7/region-control': () => ({ json: { bucketStates: { quad: 'AB' }, stats: { quad: {} } } }),
    }));
    const h = await new ApiSource({ fetchImpl }).load(GAME, {});
    const info = await h.ready('buckets');
    assert.ok(info.bucketsMs >= 0);
    const ta = h.result.timelineAnalysis;
    assert.ok(ta.bucketView.players['bär!'].x instanceof Float32Array);
    assert.deepEqual(ta.regionControl.bucketStates, { quad: 'AB' });
    const b = calls.find((c) => c.url.pathname.endsWith('/buckets'));
    assert.equal(b.init.headers.Accept, 'application/vnd.mvd-analyzer.buckets');
    assert.deepEqual(Object.fromEntries(b.url.searchParams), { windowMs: '50', includeTeam: 'true' });
    assert.equal(h.ready('buckets'), h.ready('buckets'), 'fetched once');
});

test('a layout region control cannot run on leaves the bucket view in place', async () => {
    const { fetchImpl } = fakeApi(resultRoutes({
        '/api/demos/gameId:7/result': () => ({ json: { timelineAnalysis: { regionControl: {} } }, headers: {} }),
        '/api/demos/gameId:7/buckets': () => ({ body: fixture('bucket-pack.bin') }),
        '/api/demos/gameId:7/region-control': () => ({ status: 422, json: { error: { code: 'region_control_unavailable', message: 'no' } } }),
    }));
    const h = await new ApiSource({ fetchImpl }).load(GAME, {});
    await h.ready('buckets');
    assert.ok(h.result.timelineAnalysis.bucketView);
    assert.equal(h.result.timelineAnalysis.regionControl.bucketStates, undefined);
});

test('view options become the endpoint\'s query params', () => {
    assert.equal(viewParams({ StartTime: 1000, EndTime: 2000, Players: ['a', 'b'], Types: [], Dmg: 'raw', x: null }),
        'from=1000&to=2000&players=a%2Cb&dmg=raw');
    assert.equal(viewParams({ metric: 'damageGiven', windowMs: 30000, limit: 10, GapMs: 2300 }),
        'metric=damageGiven&windowMs=30000&limit=10&gapMs=2300');
});

test('a view whose bounded family is missing retries on raw, as in the browser', async () => {
    const seen = [];
    const { fetchImpl } = fakeApi(resultRoutes({
        '/api/demos/gameId:7/top-kills': (u) => {
            seen.push(u.searchParams.get('dmg'));
            return u.searchParams.get('dmg') === 'bounded'
                ? { status: 422, json: { error: { code: 'bounded_unavailable', message: 'mode ca' } } }
                : { json: { dmg: 'raw', kills: [] } };
        },
    }));
    const h = await new ApiSource({ fetchImpl }).load(GAME, {});
    const v = await boundedOrRaw((f, o) => h.view(f, o), 'getTopKills', { Weapons: ['rl'] });
    assert.equal(v.dmg, 'raw');
    assert.deepEqual(seen, ['bounded', 'raw']);
    await assert.rejects(h.view('getNothing'), /unknown view query/);
});

test('region recompute, line of sight and the demo file', async () => {
    let posted;
    let downloads = 0;
    const { fetchImpl } = fakeApi(resultRoutes({
        '/api/demos/gameId:7/region-control': (u, init) => {
            posted = { body: init.body, method: init.method, windowMs: u.searchParams.get('windowMs') };
            return init.body === 'bad' ? { status: 400, json: { error: { code: 'invalid_param', message: 'body: x' } } } : { json: { bucketStates: {} } };
        },
        '/api/demos/gameId:7/los': () => ({ json: { timeUnit: 'ms', players: [{ name: 'a', los: [] }] } }),
        'd.quake.world': () => { downloads++; return { body: new Uint8Array([0x1f, 0x8b, 1]).buffer }; },
    }));
    const h = await new ApiSource({ fetchImpl }).load(GAME, {});
    assert.deepEqual(JSON.parse(await h.recomputeRegions('{"regions":[]}')), { bucketStates: {} });
    assert.deepEqual(posted, { body: '{"regions":[]}', method: 'POST', windowMs: '50' });
    assert.deepEqual(JSON.parse(await h.recomputeRegions('bad')), { error: 'body: x' });
    assert.deepEqual(JSON.parse(await h.lineOfSight()), [{ name: 'a', los: [] }]);

    assert.equal(downloads, 0, 'the demo file waits for the player');
    const [a, b] = await Promise.all([h.demo(), h.demo()]);
    assert.equal(a, b);
    assert.equal(a.gzipped, true);
    assert.equal(downloads, 1);
});

// --- PageSource: which analyzer a load goes to ---

class FakeWasm {
    constructor() {
        this.loads = [];
        this.up = false;
        this.ready = Promise.resolve();
    }
    async load(input, opts) {
        this.loads.push(input);
        opts.onStage?.('analyze');
        return { result: {}, timings: { net: {} }, input };
    }
}

/** @param {Record<string, any>} routes */
function page(routes, engine = null) {
    const { fetchImpl } = fakeApi(routes);
    const wasms = [];
    const src = new PageSource({
        api: new ApiSource({ fetchImpl, schema: 78 }),
        makeWasm: () => { const w = new FakeWasm(); wasms.push(w); return w; },
        engine,
        fetchImpl,
    });
    return { src, wasms };
}

const gameRoute = { '/api/games/7': () => ({ json: GAME }) };

test('a hub demo is read from the API, and no analyzer is started', async () => {
    const { src, wasms } = page({ ...gameRoute, ...resultRoutes() });
    const stages = [];
    const h = await src.load({ gameId: 7 }, { onStage: (s) => stages.push(s) });
    assert.equal(h.game.id, 7);
    assert.deepEqual(stages, ['info', 'server']);
    assert.equal(wasms.length, 0);
});

test('a local file starts the in-browser analyzer, once', async () => {
    const { src, wasms } = page({});
    const stages = [];
    await src.load({ bytes: new Uint8Array([1]), name: 'a.mvd' }, { onStage: (s) => stages.push(s) });
    await src.load({ bytes: new Uint8Array([2]), name: 'b.mvd' });
    assert.equal(wasms.length, 1);
    assert.equal(wasms[0].loads.length, 2);
    assert.deepEqual(stages, ['engine', 'analyze']);
});

test('an API that cannot answer sends the demo to the browser; a missing game does not', async () => {
    const down = page({ ...gameRoute, '/api/demos/gameId:7/result': () => ({ status: 503, json: {} }), '/api/demos/gameId:7/streams/positions': () => ({ body: emptyPack() }) });
    const h = await down.src.load({ gameId: 7 });
    assert.deepEqual(down.wasms[0].loads, [{ game: GAME }]);
    assert.ok(h.timings.net.gameInfoFetch >= 0, 'the lookup\'s timing is kept');

    const stale = page({ ...gameRoute, ...resultRoutes({ '/api/demos/gameId:7/result': () => ({ json: {}, headers: { 'X-Schema-Version': '99' } }) }) });
    await stale.src.load({ gameId: 7 });
    assert.equal(stale.wasms.length, 1, 'a schema mismatch is analysed in the browser');

    const missing = page({ '/api/games/7': () => ({ status: 404, json: { error: { code: 'demo_not_found', message: 'no game 7' } } }) });
    await assert.rejects(missing.src.load({ gameId: 7 }), /no game 7/);
    assert.equal(missing.wasms.length, 0);
});

test('?engine=wasm analyses hub demos in the browser too', async () => {
    const { src, wasms } = page({}, 'wasm');
    await src.load({ gameId: 7 });
    assert.deepEqual(wasms[0].loads, [{ gameId: 7 }]);
});

test('the last load asked for wins, across the API and the browser', async () => {
    let release;
    const { src } = page({
        ...gameRoute,
        ...resultRoutes({
            '/api/demos/gameId:7/result': async () => {
                await new Promise((r) => { release = r; });
                return { json: {}, headers: {} };
            },
        }),
    });
    const slow = src.load({ gameId: 7 });
    while (!release) await tick();
    const file = await src.load({ bytes: new Uint8Array([1]), name: 'a.mvd' });
    release();
    await assert.rejects(slow, (e) => e.superseded === true);
    assert.equal(file.input.name, 'a.mvd');
});
