import test from 'node:test';
import assert from 'node:assert/strict';

import { createAssetFetcher } from '../src/asset-fetch.js';

const ok = (bytes) => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array(bytes).buffer });
const status = (code) => ({ ok: false, status: code, arrayBuffer: async () => new ArrayBuffer(0) });

test('at most `concurrency` requests are in flight', async () => {
    let inFlight = 0;
    let peak = 0;
    const get = createAssetFetcher({
        concurrency: 3,
        fetch: async () => {
            inFlight++;
            peak = Math.max(peak, inFlight);
            await new Promise((r) => setTimeout(r, 5));
            inFlight--;
            return ok([1]);
        },
    });
    await Promise.all(Array.from({ length: 20 }, (_, i) => get(`https://a/${i}`)));
    assert.equal(peak, 3);
});

test('a network failure and a 5xx are retried; a 4xx is not', async () => {
    const calls = {};
    const answers = {
        'https://a/net': [() => { throw new TypeError('net::ERR_INSUFFICIENT_RESOURCES'); }, () => ok([7])],
        'https://a/5xx': [() => status(503), () => ok([8])],
        'https://a/404': [() => status(404), () => ok([9])],
    };
    const get = createAssetFetcher({
        retryDelayMs: 1,
        fetch: async (url) => {
            const n = (calls[url] = (calls[url] || 0) + 1);
            return answers[url][n - 1]();
        },
    });
    assert.deepEqual([...new Uint8Array(await get('https://a/net'))], [7]);
    assert.deepEqual([...new Uint8Array(await get('https://a/5xx'))], [8]);
    await assert.rejects(get('https://a/404'), /HTTP 404/);
    assert.deepEqual(calls, { 'https://a/net': 2, 'https://a/5xx': 2, 'https://a/404': 1 });
});

test('retries give up after `retries`', async () => {
    let calls = 0;
    const get = createAssetFetcher({ retries: 2, retryDelayMs: 1, fetch: async () => { calls++; throw new TypeError('down'); } });
    await assert.rejects(get('https://a/x'), /down/);
    assert.equal(calls, 3);
});

test('bytes are kept by URL and each caller gets its own copy; blob: URLs are not kept', async () => {
    let calls = 0;
    const get = createAssetFetcher({ fetch: async () => { calls++; return ok([1, 2, 3]); } });
    const a = await get('https://a/m.mdl');
    const b = await get('https://a/m.mdl');
    assert.equal(calls, 1);
    assert.notEqual(a, b, 'FTE adopts the buffer it gets: one per engine');
    new Uint8Array(a)[0] = 99;
    assert.deepEqual([...new Uint8Array(b)], [1, 2, 3]);
    await get('blob:https://x/1');
    await get('blob:https://x/1');
    assert.equal(calls, 3);
});

test('a failed URL is fetched again next time', async () => {
    let calls = 0;
    const get = createAssetFetcher({ retries: 0, fetch: async () => { calls++; if (calls === 1) throw new TypeError('down'); return ok([5]); } });
    await assert.rejects(get('https://a/x'));
    assert.deepEqual([...new Uint8Array(await get('https://a/x'))], [5]);
});

test('a blob: URL is not retried', async () => {
    let calls = 0;
    const get = createAssetFetcher({ retryDelayMs: 1, fetch: async () => { calls++; throw new TypeError('net::ERR_FILE_NOT_FOUND'); } });
    await assert.rejects(get('blob:https://x/1'));
    assert.equal(calls, 1);
});
