// api.js — the web page's /api path on Netlify deploys (previews and branch
// deploys; production is mvdanalyzer.com, where Caddy does this —
// deploy/Caddyfile). The page calls api/... on its own origin; this forwards
// the request to the production mvd-api's /v1/... with the key added, so the
// key never reaches the browser (plan-web-api.md).
//
// MVD_API_KEY is a Netlify environment variable, scoped to Deploy Previews
// and Branch deploys: an ordinary service key (deploy/README.md §4b). Without
// it every call is a 401 and the page analyses in the browser instead. The
// repository is public: the variable must be marked secret and kept from
// fork pull requests' previews, or a fork could rewrite this file to print
// it (§4b).
//
// Only the routes the page reads are forwarded — the same allowlist as the
// Caddyfile's /api block and scripts/serve-dev.py (test/api-routes.test.js
// pins the three together) — so the path cannot reach the rest of the API on
// this key.

export const UPSTREAM = 'https://mvdanalyzer.com/v1/';

export const GET_ROUTES = /^\/api\/(demos\/[^/]+\/(result|streams\/positions|buckets|region-control|los|top-windows|top-kills|lives|highlights|events|backpacks|frags|weapon-pickups)|games\/(search|[0-9]+))$/;
export const POST_ROUTES = /^\/api\/demos\/[^/]+\/region-control$/;

// Request headers that reach mvd-api; everything else (cookies included)
// stays here.
const FORWARD = ['accept', 'accept-encoding', 'content-type', 'if-none-match'];

/** @param {Request} request */
export default async (request) => {
    const url = new URL(request.url);
    const post = request.method === 'POST';
    if (!(post ? POST_ROUTES : GET_ROUTES).test(url.pathname) || !(post || request.method === 'GET')) {
        return new Response('not found', { status: 404 });
    }
    const headers = new Headers();
    for (const name of FORWARD) {
        const v = request.headers.get(name);
        if (v) headers.set(name, v);
    }
    // @ts-ignore — Netlify's edge runtime global
    headers.set('authorization', `Bearer ${Netlify.env.get('MVD_API_KEY') || ''}`);
    const upstream = await fetch(UPSTREAM + url.pathname.slice('/api/'.length) + url.search, {
        method: request.method,
        headers,
        body: post ? request.body : undefined,
    });
    // Passed through as fetch presents it. Content-Length is dropped: if the
    // runtime inflated a gzip body it no longer matches (and it also drops
    // Content-Encoding then, so the body and its headers agree).
    const out = new Headers(upstream.headers);
    out.delete('content-length');
    return new Response(upstream.body, { status: upstream.status, headers: out });
};

export const config = { path: '/api/*' };
