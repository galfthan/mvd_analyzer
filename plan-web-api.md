# Plan: the web app reads hub demos through mvd-api

Branch: `web-api`. Follows the perf work of PR #145 (position and bucket
packs, `/streams/positions`, Accept-negotiated `/buckets`).

## Goal

- A hub demo (search, `?gameId=`, a playlist clip) is analysed by mvd-api;
  the page reads it over HTTP.
- A local file is analysed in the browser by the WASM build, as today.
- The WASM build (worker + `analyzer.wasm`) is only loaded when the user
  opens a local file, or when the API path has to fall back (below).
- The page holds no API key and no Supabase key.

## Owner decisions (2026-11-01)

1. Netlify previews inject their key with an **edge function**, not a
   signed proxy redirect.
2. **Fall back to WASM** when the API fails (5xx, 429, timeout, network).
3. The page's API path is **`/api`**.
4. Previews call the **prod API**. A schema mismatch (`X-Schema-Version`
   differs from the version the page was built for) falls back to WASM for
   that demo; `?engine=wasm` forces WASM. No dev API instance for now.

## Keys

The page calls relative `/api/...`; whatever serves the page forwards to
mvd-api's `/v1/...` and adds `Authorization: Bearer <key>`.

| Host | Mechanism | Key source |
|---|---|---|
| mvdanalyzer.com | Caddy route: `/api/*` → rewrite to `/v1/*`, `header_up Authorization` | `MVD_WEB_KEY` in Caddy's systemd env file |
| Netlify previews | edge function `netlify/edge-functions/api.js` on `/api/*`, `fetch` to `https://mvdanalyzer.com/v1/...`, response streamed through | `MVD_API_KEY` Netlify env var, scoped to Deploy Previews + Branch deploys |
| local dev | Vite dev proxy `/api` → local mvd-api (no auth) | none |

Both proxies forward only an allowlist of the GET paths the page uses
(plus the region-control POST), so `/api` is not a general relay for the
key. Anyone can still call `/api` through the site: the point is that the
key is not copyable and each host's key can be revoked and rate-limited
on its own.

Rate limits: mvd-api limits per key today, so one web key would put every
visitor in one bucket. The prod web key is limited **per client IP**
(Caddy's `X-Forwarded-For` is reliable); the preview key stays one shared
service bucket (its requests come from Netlify's edge, and preview traffic
is small).

## Two PRs, deployed apart

- **PR A — API** (this branch): phase 1, the Caddy `/api` route and the
  deploy docs for the keys. Deployed to prod on its own; the prod web page
  is unchanged (nothing calls `/api` yet, no endpoint or schema changes).
- **PR B — web**: phases 2–5, including the Netlify edge function. Its
  previews call the prod API from PR A, so PR A must be live and the
  preview key set in Netlify first. Merged and deployed once proven on
  the preview.

## Phases

### 1. API additions (backward compatible; deploy before the frontend)

1. `GET /v1/demos/{id}/result` — the Result as the WASM build hands it to
   the page: playerStats with the KTX overlay, streams without position
   tracks. One shared helper in `mvd-analytics/view` builds it for both the
   WASM entry and the API, so they cannot drift. gzip when accepted.
2. `GET /v1/games/{id}` — the verbatim hub row (replaces the page's
   Supabase `fetchGame`).
3. Region control for an edited layout: `POST
   /v1/demos/{id}/region-control`, the layout in the body. The deferred
   default region states need nothing new: `GET
   /region-control?windowMs=50&regions=none` equals the worker's
   argument-less recompute by construction (the stored team pair is the
   view's own inference; classification reads only the regions' locs),
   and matched on all 13 cached demos.
4. Per-client-IP rate limiting for a web key class.
5. openapi.yaml, API.md, mvd-api README, RELEASE_NOTES.

### 2. Plumbing; the Supabase key leaves the code

1. Caddy `/api` route, Netlify edge function, Vite dev proxy; `/api`
   joins the reserved prefixes the web bundle must not use.
2. `hub.js`: search → `/api/games/search?roster=1`, game lookup →
   `/api/games/{id}`. The light player (`play.html`) too. The Supabase
   anon key is removed from the code (it stays in git history; it is the
   hub's public anon key, not ours to rotate).
3. Demo bytes keep coming from the public CDN (`d.quake.world`).

### 3. `ApiSource`; WASM only for local files

1. `src/data/api-source.js`: a handle with the same interface as
   `WasmSource`'s (`result`, `section`, `ready('buckets')`, `view(fn)`,
   `recomputeRegions`, `lineOfSight`), backed by `/result` + the position
   pack, `/buckets` (pack) + region states, the view endpoints, `/los`.
2. `src/app/source.js` picks the source per load: hub → API, file → WASM.
3. The worker (and `analyzer.wasm`) is created on first use, not at page
   load; the WASM-loading overlay and the build-hash check move with it.
4. A hub demo's `.mvd.gz` is fetched only when the Player tab or a
   playlist clip needs it.

### 4. Fallbacks

1. API failure (5xx, 429, timeout, network) → CDN download + WASM.
2. `X-Schema-Version` ≠ the page's build → WASM, logged.
3. `?engine=wasm` forces WASM.

### 5. Verification and docs

1. Node tests for `ApiSource` against recorded responses.
2. Parity: one demo through both sources gives the same Result, bucket
   view and region states.
3. Headless Chromium on a preview: a hub demo loads with no
   `analyzer.wasm` request; a local file loads WASM; the fallbacks work.
4. mvd-web README, deploy/README.md (keys, Caddy route, Netlify env var),
   Caddyfile, netlify.toml, API.md, RELEASE_NOTES.
