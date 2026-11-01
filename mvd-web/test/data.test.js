import test from 'node:test';
import assert from 'node:assert/strict';

import { readFileSync } from 'node:fs';

import { WasmSource } from '../src/data/wasm-source.js';
import { parseGameId, demoFilename, searchGames, downloadDemo } from '../src/data/hub.js';

// A stand-in for static/worker.js speaking its message protocol.
class FakeWorker {
    constructor() {
        this.posted = [];
        this.onmessage = null;
    }
    postMessage(msg) {
        this.posted.push(msg);
    }
    reply(data) {
        this.onmessage({ data });
    }
    last(type) {
        return this.posted.filter((m) => m.type === type).at(-1);
    }
}

const tick = () => new Promise((r) => setTimeout(r, 0));

// A position pack with no players (lib/postrack.js), what the worker sends
// beside a Result without streams.
function emptyPack() {
    const buf = new ArrayBuffer(12);
    const dv = new DataView(buf);
    [...'MVDP'].forEach((c, i) => dv.setUint8(i, c.charCodeAt(0)));
    dv.setUint32(4, 1, true);
    return buf;
}

// The Go-written bucket pack the decoder test uses (bucketpack.test.js).
function bucketPack() {
    const b = readFileSync(new URL('./fixtures/bucket-pack.bin', import.meta.url));
    return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
}

async function readySource() {
    const w = new FakeWorker();
    const src = new WasmSource(w);
    w.reply({ type: 'ready', version: { hash: 'abc' }, wasmLoadMs: 12 });
    assert.deepEqual(await src.ready, { version: { hash: 'abc' }, wasmLoadMs: 12 });
    return { w, src };
}

async function analyze(w, src, result, name = 'a.mvd', pos = emptyPack()) {
    const p = src.load({ bytes: new Uint8Array([1, 2, 3]), name });
    await tick();
    const req = w.last('analyze');
    w.reply({ type: 'result', reqId: req.reqId, json: JSON.stringify(result), pos, timings: { wasmAnalyzeMs: 5 } });
    return { handle: await p, req };
}

test('load analyses bytes, keeps a copy for the player, and names the demo', async () => {
    const { w, src } = await readySource();
    const { handle, req } = await analyze(w, src, { match: { map: 'dm3' } });
    assert.equal(req.filename, 'a.mvd');
    assert.deepEqual([...new Uint8Array(await handle.demoBlob.arrayBuffer())], [1, 2, 3]);
    assert.equal(handle.gzipped, false);
    assert.deepEqual(handle.ref, { key: 'file:a.mvd', gameId: null });
    assert.deepEqual(await handle.section('match'), { map: 'dm3' });
    assert.equal(handle.timings.worker.wasmAnalyzeMs, 5);
});

test('the position tracks the worker sends beside the JSON land on its players', async () => {
    const { w, src } = await readySource();
    // The Go-written fixture's three players (see postrack.test.js).
    const b = readFileSync(new URL('./fixtures/position-pack.bin', import.meta.url));
    const pack = b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
    const players = [{ name: 'bär!' }, { name: 'xy' }, { name: 'spec' }];
    const { handle } = await analyze(w, src, { streams: { players } }, 'a.mvd', pack);
    const got = (await handle.section('streams')).players;
    assert.deepEqual(Array.from(got[0].pos.t), [0, 13, -7]);
    assert.equal(got[2].pos, undefined);
});

test('a pack the page cannot read fails the load and keeps the previous demo', async () => {
    const { w, src } = await readySource();
    const { handle } = await analyze(w, src, {});
    const newer = emptyPack();
    new DataView(newer).setUint32(4, 2, true);
    await assert.rejects(analyze(w, src, {}, 'b.mvd', newer), /position pack: unsupported/);
    assert.equal(src.current, handle);
    assert.equal(handle.current, true);
});

test('a failed load rejects and leaves the previous demo queryable', async () => {
    const { w, src } = await readySource();
    const { handle } = await analyze(w, src, {});
    await assert.rejects(analyze(w, src, { error: 'bad demo' }, 'bad.mvd'), /bad demo/);
    assert.equal(src.current, handle);
    const p = handle.lineOfSight();
    w.reply({ type: 'los_result', reqId: w.last('computeLos').reqId, json: '[]' });
    assert.equal(await p, '[]');

    const q = src.load({ bytes: new Uint8Array([1]), name: 'c.mvd' });
    await tick();
    w.reply({ type: 'error', reqId: w.last('analyze').reqId, message: 'worker died' });
    await assert.rejects(q, /worker died/);
    assert.equal(handle.current, true);
});

test('deferred buckets land on their own demo only', async () => {
    const { w, src } = await readySource();
    const first = await analyze(w, src, { timelineAnalysis: { regionControl: {} } });
    const second = await analyze(w, src, { timelineAnalysis: {} }, 'b.mvd');
    w.reply({
        type: 'buckets', reqId: first.req.reqId, bucketsMs: 3, regionMs: 1,
        buckets: bucketPack(),
        regionStatesJSON: JSON.stringify({ bucketStates: [1], stats: { x: 1 } }),
    });
    assert.deepEqual(await first.handle.ready('buckets'), { bucketsMs: 3, regionMs: 1 });
    const view = first.handle.result.timelineAnalysis.bucketView;
    assert.equal(view.windowMs, 700);
    assert.ok(view.players['bär!'].x instanceof Float32Array);
    assert.deepEqual(first.handle.result.timelineAnalysis.regionControl, { bucketStates: [1], stats: { x: 1 } });
    assert.equal(second.handle.result.timelineAnalysis.bucketView, undefined);
    w.reply({ type: 'error', reqId: second.req.reqId, message: 'buckets blew up' });
    await assert.rejects(second.handle.ready('buckets'), /buckets blew up/);
});

test('view queries answer for the current demo and refuse a superseded one', async () => {
    const { w, src } = await readySource();
    const { handle } = await analyze(w, src, {});
    const p = handle.view('getTopKills', { limit: 3 });
    const q = w.last('viewQuery');
    assert.equal(q.fn, 'getTopKills');
    assert.deepEqual(JSON.parse(q.optsJSON), { limit: 3 });
    w.reply({ type: 'view_result', reqId: q.reqId, json: '[1,2]' });
    assert.deepEqual(await p, [1, 2]);

    const e = handle.view('getTopWindows');
    w.reply({ type: 'view_result', reqId: w.last('viewQuery').reqId, json: '{"error":"no damage log"}' });
    await assert.rejects(e, /no damage log/);

    await analyze(w, src, {}, 'b.mvd');
    await assert.rejects(handle.view('getTopKills'), /superseded/);
    await assert.rejects(handle.lineOfSight(), /superseded/);
});

test('replies are matched by reqId, not arrival order', async () => {
    const { w, src } = await readySource();
    const { handle } = await analyze(w, src, {});
    const a = handle.recomputeRegions('{"regions":[]}');
    const b = handle.recomputeRegions('{"regions":[1]}');
    const [qa, qb] = w.posted.filter((m) => m.type === 'recomputeRegions');
    w.reply({ type: 'recompute_result', reqId: qb.reqId, json: 'B' });
    w.reply({ type: 'recompute_error', reqId: qa.reqId, message: 'A failed' });
    assert.equal(await b, 'B');
    await assert.rejects(a, /A failed/);
});

test('overlapping loads: the last one asked for wins, a slow older one never posts', async () => {
    let release;
    const fetchImpl = async (url) => {
        if (url.includes('supabase')) return { ok: true, json: async () => [{ id: 7, demo_sha256: 'abcd', timestamp: '2026-01-02T03:04:05Z' }] };
        await new Promise((r) => { release = r; });
        return { ok: true, arrayBuffer: async () => new Uint8Array([9]).buffer };
    };
    const w = new FakeWorker();
    const src = new WasmSource(w, { fetchImpl });
    w.reply({ type: 'ready' });
    const stages = [];
    const slow = src.load({ gameId: 7 }, { onStage: (s) => stages.push(s) });
    while (!release) await tick();
    const fast = await analyze(w, src, { match: { map: 'dm2' } }, 'b.mvd');
    release();
    await assert.rejects(slow, (e) => e.superseded === true);
    assert.equal(w.posted.filter((m) => m.type === 'analyze').length, 1, 'the stale load never replaced the worker\'s demo');
    assert.deepEqual(stages, ['info', 'download'], 'and reported no stage after the newer load began');
    assert.equal(src.current, fast.handle);
    assert.equal(fast.handle.current, true);
});

test('overlapping loads: an older analyze answering late is dropped, its failure too', async () => {
    const { w, src } = await readySource();
    const a = src.load({ bytes: new Uint8Array([1]), name: 'a.mvd' });
    await tick();
    const reqA = w.last('analyze');
    const b = src.load({ bytes: new Uint8Array([2]), name: 'b.mvd' });
    await tick();
    const reqB = w.last('analyze');
    w.reply({ type: 'result', reqId: reqA.reqId, json: '{}', pos: emptyPack() });
    await assert.rejects(a, (e) => e.superseded === true);
    w.reply({ type: 'result', reqId: reqB.reqId, json: '{}', pos: emptyPack() });
    const hb = await b;
    assert.equal(src.current, hb);

    const c = src.load({ bytes: new Uint8Array([3]), name: 'c.mvd' });
    await tick();
    const reqC = w.last('analyze');
    const d = src.load({ bytes: new Uint8Array([4]), name: 'd.mvd' });
    await tick();
    w.reply({ type: 'result', reqId: reqC.reqId, json: '{"error":"bad demo"}' });
    await assert.rejects(c, (e) => e.superseded === true && !/bad demo/.test(e.message));
    w.reply({ type: 'result', reqId: w.last('analyze').reqId, json: '{}', pos: emptyPack() });
    assert.equal(src.current, await d);
});

test('a wasm load failure rejects ready', async () => {
    const w = new FakeWorker();
    const src = new WasmSource(w);
    w.reply({ type: 'error', message: 'Failed to load WASM: 404' });
    await assert.rejects(src.ready, /404/);
});

test('hub loads go info → download → analyze with the hub file name', async () => {
    const game = { id: 7, demo_sha256: 'abcdef', teams: [{ name: 'red' }, { name: 'b l' }], map: 'dm3', mode: '4on4', timestamp: '2026-04-24T01:37:35Z', players: [] };
    const urls = [];
    const fetchImpl = async (url) => {
        urls.push(url);
        if (url.includes('supabase')) return { ok: true, json: async () => [game] };
        return { ok: true, arrayBuffer: async () => new Uint8Array([9]).buffer };
    };
    const w = new FakeWorker();
    const src = new WasmSource(w, { fetchImpl });
    w.reply({ type: 'ready' });
    const stages = [];
    const p = src.load({ gameId: 7 }, { onStage: (s) => stages.push(s) });
    while (!w.last('analyze')) await tick();
    const req = w.last('analyze');
    assert.equal(req.filename, '4on4_red_vs_b_l[dm3]2026042401373.mvd.gz');
    w.reply({ type: 'result', reqId: req.reqId, json: '{}', pos: emptyPack() });
    const handle = await p;
    assert.deepEqual(stages, ['info', 'download', 'analyze']);
    assert.match(urls[1], /^https:\/\/d\.quake\.world\/abc\/abcdef\.mvd\.gz$/);
    assert.deepEqual(handle.ref, { key: 'hub:7', gameId: 7 });
    assert.equal(handle.game, game);
    assert.ok(handle.timings.net.gameInfoFetch >= 0 && handle.timings.net.demoDownload >= 0);
});

test('hub helpers', async () => {
    assert.equal(parseGameId(' 212260 '), 212260);
    assert.equal(parseGameId('https://hub.quakeworld.nu/games/?gameId=5&from=7'), 5);
    assert.throws(() => parseGameId('nope'), /Could not parse/);
    assert.equal(demoFilename({ timestamp: '2026-01-02T03:04:05Z' }), 'unknown_unknown[unknown]2026010203040.mvd.gz');

    let seen;
    const fetchImpl = async (url, init) => {
        seen = { url, init };
        return { ok: true, json: async () => [], headers: new Map([['content-range', '0-99/1234']]) };
    };
    const { total } = await searchGames({ player: 'milton', to: '2026-01-01' }, 100, fetchImpl);
    assert.equal(total, 1234);
    assert.match(seen.url, /offset=100/);
    assert.match(seen.url, /players_fts=fts\.'milton'/);
    assert.match(seen.url, /timestamp=lte\.2026-01-01T23%3A59%3A59/);
    assert.equal(seen.init.headers.Prefer, 'count=exact');

    const fallback = async (url) => (url.startsWith('https://d.quake.world')
        ? { ok: false } : { ok: true, arrayBuffer: async () => new Uint8Array([4]).buffer });
    assert.deepEqual([...await downloadDemo({ demo_sha256: 'xyz1', demo_source_url: 'http://srv/x' }, fallback)], [4]);
    await assert.rejects(downloadDemo({}, fallback), /No download URL/);
});
