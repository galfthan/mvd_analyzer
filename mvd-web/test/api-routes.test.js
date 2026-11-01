import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { GET_ROUTES, POST_ROUTES } from '../../netlify/edge-functions/api.js';

// The page's /api path is forwarded with a key by three hosts: Caddy on
// mvdanalyzer.com (deploy/Caddyfile), the Netlify edge function on previews
// and scripts/serve-dev.py locally. Each forwards only the routes the page
// reads; the three allowlists must stay one list, or a route works in one
// place and 404s in another.
const read = (/** @type {string} */ p) => readFileSync(new URL(`../../${p}`, import.meta.url), 'utf8');

test('the Caddyfile, the edge function and the dev server forward the same /api routes', () => {
    // A JS regex's source writes / as \/; the other two do not.
    const want = [GET_ROUTES, POST_ROUTES].map((r) => r.source.replaceAll('\\/', '/'));
    const caddy = [...read('deploy/Caddyfile').matchAll(/path_regexp (\S+)/g)].map((m) => m[1]);
    assert.deepEqual(caddy, want);

    const dev = [...read('scripts/serve-dev.py').matchAll(/_ROUTES = re\.compile\(r"([^"]+)"\)/g)].map((m) => m[1]);
    assert.deepEqual(dev, want);
});

test('the allowlist admits the page\'s routes and nothing else', () => {
    for (const p of ['/api/demos/gameId:222649/result', '/api/demos/gameId:1/streams/positions', '/api/demos/abc/buckets',
        '/api/demos/x/top-kills', '/api/games/search', '/api/games/12345']) {
        assert.ok(GET_ROUTES.test(p), p);
    }
    for (const p of ['/api/demos', '/api/demos/x/overview', '/api/demos/x/artifacts/los', '/api/playlist-links',
        '/api/games/12a', '/api/demos/x/result/extra', '/api/../v1/demos']) {
        assert.ok(!GET_ROUTES.test(p) && !POST_ROUTES.test(p), p);
    }
    assert.ok(POST_ROUTES.test('/api/demos/gameId:1/region-control'));
    assert.ok(!POST_ROUTES.test('/api/demos/gameId:1/result'));
});
