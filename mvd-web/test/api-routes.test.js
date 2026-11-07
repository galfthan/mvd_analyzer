import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import forward, { GET_ROUTES, POST_ROUTES, UPSTREAM } from '../../netlify/edge-functions/api.js';
import { VIEW_ENDPOINTS } from '../src/data/api-source.js';

// The page's /api path is forwarded with a key by three hosts: Caddy on
// mvdanalyzer.com (deploy/Caddyfile), the Netlify edge function on previews
// and scripts/serve-dev.py locally. Each forwards only the routes the page
// reads; the three allowlists must stay one list — paths and methods — or a
// route works in one place and 404s in another, and that 404 (no mvd-api
// error envelope) would quietly send the page to its fallbacks.
const read = (/** @type {string} */ p) => readFileSync(new URL(`../../${p}`, import.meta.url), 'utf8');

test('the Caddyfile, the edge function and the dev server forward the same /api routes', () => {
    assert.equal(GET_ROUTES.flags + POST_ROUTES.flags, '', 'no flags: the other two have none');
    // A JS regex's source writes / as \/; the other two do not.
    const want = [GET_ROUTES, POST_ROUTES].map((r) => r.source.replaceAll('\\/', '/'));

    const caddy = read('deploy/Caddyfile');
    const matchers = [...caddy.matchAll(/@(\w+) \{\s*method (\w+)\s*path_regexp (\S+)\s*\}/g)].map((m) => [m[1], m[2], m[3]]);
    assert.deepEqual(matchers, [['webapi', 'GET', want[0]], ['webapiPost', 'POST', want[1]]]);

    const dev = read('scripts/serve-dev.py');
    assert.deepEqual([...dev.matchAll(/_ROUTES = re\.compile\(r"([^"]+)"\)/g)].map((m) => m[1]), want);
    assert.match(dev, /def do_GET\(self\):\s*if self\.path\.startswith\("\/api\/"\):\s*self\.forward\(GET_ROUTES\)/);
    assert.match(dev, /def do_POST\(self\):\s*self\.forward\(POST_ROUTES\)/);
});

test('every endpoint the page calls is on the allowlist', () => {
    const id = '/api/demos/gameId:1/';
    const gets = ['result', 'streams/positions', 'buckets', 'region-control', 'los', ...Object.values(VIEW_ENDPOINTS)];
    for (const p of gets) assert.ok(GET_ROUTES.test(id + p), p);
    assert.ok(GET_ROUTES.test('/api/games/search') && GET_ROUTES.test('/api/games/12345'));
    assert.ok(POST_ROUTES.test(id + 'region-control'));
});

test('the allowlist admits the page\'s routes and nothing else', () => {
    for (const p of ['/api/demos', '/api/demos/x/overview', '/api/demos/x/artifacts/los', '/api/playlists',
        '/api/games/12a', '/api/demos/x/result/extra', '/api/../v1/demos']) {
        assert.ok(!GET_ROUTES.test(p) && !POST_ROUTES.test(p), p);
    }
    assert.ok(!POST_ROUTES.test('/api/demos/gameId:1/result'));
});

test('the edge function forwards the page\'s requests with the key, and nothing else', async () => {
    const sent = [];
    const realFetch = globalThis.fetch;
    globalThis.Netlify = { env: { get: (k) => (k === 'MVD_API_KEY' ? 'qwmvd_test' : undefined) } };
    globalThis.fetch = async (url, init) => {
        sent.push({ url, init });
        return new Response('{}', { status: 200, headers: { 'content-type': 'application/json', 'content-length': '2', etag: '"x"' } });
    };
    try {
        const call = (path, init = {}) => forward(new Request('https://preview.example' + path, init));
        const ok = await call('/api/demos/gameId:1/result?x=1', { headers: { Accept: 'application/json', Cookie: 'c=1' } });
        assert.equal(ok.status, 200);
        assert.equal(ok.headers.get('etag'), '"x"');
        assert.equal(ok.headers.get('content-length'), null);
        assert.equal(sent[0].url, UPSTREAM + 'demos/gameId:1/result?x=1');
        assert.equal(sent[0].init.headers.get('authorization'), 'Bearer qwmvd_test');
        assert.equal(sent[0].init.headers.get('accept'), 'application/json');
        assert.equal(sent[0].init.headers.get('cookie'), null, 'cookies stay at the edge');

        await call('/api/demos/gameId:1/region-control', { method: 'POST', body: '{"regions":[]}' });
        assert.equal(sent[1].init.method, 'POST');

        for (const [path, method] of [['/api/demos/gameId:1/result', 'POST'], ['/api/demos/gameId:1/region-control', 'PUT'],
            ['/api/demos/gameId:1/result', 'HEAD'], ['/api/demos/gameId:1/overview', 'GET'], ['/api/demos', 'POST']]) {
            assert.equal((await call(path, { method })).status, 404, `${method} ${path}`);
        }
        assert.equal(sent.length, 2, 'refused requests never reach the API');
    } finally {
        globalThis.fetch = realFetch;
        delete globalThis.Netlify;
    }
});
