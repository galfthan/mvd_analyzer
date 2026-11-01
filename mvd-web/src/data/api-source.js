// api-source.js — the data layer's mvd-api implementation: a hub demo is
// analysed by the server and read over HTTP (src/data/api.js; the host adds
// the key). It answers the calls a WasmSource DemoHandle answers, from the
// endpoints that serve the same views (plan-web-api.md):
//
//   result              /result (view.PageResult, the WASM build's payload)
//                       + /streams/positions (the position pack)
//   ready('buckets')    /buckets as a bucket pack (the worker's default view)
//                       + /region-control?windowMs=50 (its region states)
//   view(fn, opts)      the endpoint of that view, opts as its query params
//   recomputeRegions    POST /region-control with the layout
//   lineOfSight         /los
//   demo()              the .mvd.gz from the hub's CDN, fetched only when the
//                       Player wants it
//
// The server keeps nothing per page, so a handle stays queryable after a
// newer load (unlike the worker's, which holds one demo).

import { apiFetch, apiJSON, ApiError } from './api.js';
import * as hub from './hub.js';
import { attachPositions } from '../lib/postrack.js';
import { decodeBucketPack } from '../lib/bucketpack.js';

/**
 * @typedef {import('./hub.js').HubGame} HubGame
 * @typedef {import('./api.js').FetchImpl} FetchImpl
 * @typedef {{ gameInfoFetch?: number, resultFetch?: number }} ApiNet
 */

const BUCKET_PACK = 'application/vnd.mvd-analyzer.buckets';

// The view queries the page runs (DemoHandle.view) and their endpoints.
/** @type {Record<string, string>} */
const VIEW_ENDPOINTS = {
    getTopWindows: 'top-windows',
    getTopKills: 'top-kills',
    getLives: 'lives',
    getHighlights: 'highlights',
    getEvents: 'events',
    getBackpacks: 'backpacks',
    getFrags: 'frags',
    getWeaponPickups: 'weapon-pickups',
};

// SchemaMismatchError: the API serves another schema version than this page
// was built for (a preview of a branch that changed it reads the production
// API). The page's own WASM build speaks its schema, so the demo is analysed
// there instead.
export class SchemaMismatchError extends Error {
    /**
     * @param {number} served
     * @param {number} built
     */
    constructor(served, built) {
        super(`mvd-api serves schema v${served}, this page is built for v${built}`);
        this.canFallBack = true;
    }
}

/**
 * viewParams: a view's options as its endpoint's query params. The options
 * are the Go view structs' fields, as the WASM bridge takes them (either
 * case: Go's JSON matching ignores it); the params are the same names in
 * lower camel case, but for the event windows' StartTime / EndTime, which
 * the API calls from / to. Lists become CSV; unset values are left out.
 * @param {object} opts
 * @returns {string}
 */
export function viewParams(opts) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(opts)) {
        if (v == null || v === '' || (Array.isArray(v) && v.length === 0)) continue;
        const name = k === 'StartTime' ? 'from' : k === 'EndTime' ? 'to' : k[0].toLowerCase() + k.slice(1);
        q.set(name, Array.isArray(v) ? v.join(',') : String(v));
    }
    return q.toString();
}

export class ApiSource {
    /**
     * @param {{ fetchImpl?: FetchImpl, schema?: number | null }} [opts]
     *   schema: the Result schema the page was built for; null skips the check
     */
    constructor({ fetchImpl = (...a) => fetch(...a), schema = null } = {}) {
        this.fetchImpl = fetchImpl;
        this.schema = schema;
    }

    /**
     * load: the demo's Result from the server. Rejects with an ApiError or
     * a SchemaMismatchError; either says whether analysing in the browser
     * could still work (canFallBack).
     * @param {HubGame} game
     * @param {ApiNet} net  timings so far (the game lookup)
     * @returns {Promise<ApiDemoHandle>}
     */
    async load(game, net) {
        const id = `gameId:${game.id}`;
        const t = performance.now();
        const [resp, posResp] = await Promise.all([
            apiFetch(`demos/${id}/result`, {}, this.fetchImpl),
            apiFetch(`demos/${id}/streams/positions`, {}, this.fetchImpl),
        ]);
        const served = Number(resp.headers.get('X-Schema-Version'));
        if (this.schema != null && served && served !== this.schema) throw new SchemaMismatchError(served, this.schema);
        const [result, pos] = await Promise.all([resp.json(), posResp.arrayBuffer()]);
        const tParse = performance.now();
        attachPositions(result, pos);
        return new ApiDemoHandle(this, game, result, {
            net: { ...net, resultFetch: +(tParse - t).toFixed(1) },
            worker: null,
            parseMs: performance.now() - tParse,
        });
    }
}

export class ApiDemoHandle {
    /**
     * @param {ApiSource} source
     * @param {HubGame} game
     * @param {import('../lib/result.js').Result} result
     * @param {{ net: ApiNet, worker: null, parseMs: number }} timings
     */
    constructor(source, game, result, timings) {
        this.source = source;
        this.game = game;
        this.ref = { key: `hub:${game.id}`, gameId: game.id };
        this.result = result;
        this.timings = timings;
        this.id = `gameId:${game.id}`;
        /** @type {Promise<{ bucketsMs: number, regionMs: number }> | null} */
        this._buckets = null;
        /** @type {Promise<{ blob: Blob, gzipped: boolean }> | null} */
        this._demo = null;
    }

    /**
     * @param {string} path
     * @param {RequestInit} [init]
     */
    _fetch(path, init) {
        return apiFetch(`demos/${this.id}/${path}`, init, this.source.fetchImpl);
    }

    /** @param {string} name */
    async section(name) {
        return this.result[name];
    }

    // ready('buckets') resolves once the 50 ms bucket view and the default
    // layout's region states are on the Result, as the worker's deferred
    // build puts them there.
    /** @param {'buckets'} what */
    ready(what) {
        if (what !== 'buckets') throw new Error(`unknown readiness: ${what}`);
        if (!this._buckets) this._buckets = this._loadBuckets();
        return this._buckets;
    }

    async _loadBuckets() {
        const ta = this.result.timelineAnalysis;
        if (!ta) return { bucketsMs: 0, regionMs: 0 };
        const t = performance.now();
        const regions = ta.regionControl
            ? this._fetch('region-control?windowMs=50&regions=none').then((r) => r.json(), (err) => {
                // A layout region control cannot run on (not two sides) is a
                // 422, as the worker's recompute answers an error envelope.
                if (/** @type {ApiError} */ (err).status !== 422) console.warn('deferred region states failed:', err.message);
                return null;
            })
            : Promise.resolve(null);
        const buckets = await this._fetch('buckets?windowMs=50&includeTeam=true', { headers: { Accept: BUCKET_PACK } });
        ta.bucketView = decodeBucketPack(await buckets.arrayBuffer());
        const bucketsMs = performance.now() - t;
        const rs = await regions;
        if (rs?.bucketStates && ta.regionControl) {
            ta.regionControl.bucketStates = rs.bucketStates;
            if (rs.stats) ta.regionControl.stats = rs.stats;
        }
        return { bucketsMs, regionMs: performance.now() - t - bucketsMs };
    }

    /**
     * view(fn, opts): one view query, from its endpoint. A demo whose mode
     * skips the bounded damage reconstruction answers an explicit
     * dmg=bounded with 422 bounded_unavailable; it rejects with the message
     * the WASM view gives, which clip-query's boundedOrRaw retries on raw.
     * @param {string} fn
     * @param {object} [opts]
     * @returns {Promise<any>}
     */
    async view(fn, opts = {}) {
        const endpoint = VIEW_ENDPOINTS[fn];
        if (!endpoint) throw new Error(`unknown view query: ${fn}`);
        try {
            return await (await this._fetch(`${endpoint}?${viewParams(opts)}`)).json();
        } catch (err) {
            if (err instanceof ApiError && err.code === 'bounded_unavailable') {
                throw new Error(`bounded damage family unavailable: ${err.message}`);
            }
            throw err;
        }
    }

    // recomputeRegions: region control over an edited layout, as raw JSON —
    // an {"error": …} envelope when the server cannot run it, as the
    // worker's recompute answers.
    /** @param {string} overrideJSON */
    async recomputeRegions(overrideJSON) {
        try {
            const resp = await this._fetch('region-control?windowMs=50', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: overrideJSON,
            });
            return await resp.text();
        } catch (err) {
            if (err instanceof ApiError && err.status < 500 && err.status !== 0) return JSON.stringify({ error: err.message });
            throw err;
        }
    }

    // lineOfSight: the players' LOS / PVS tracks as raw JSON, the worker's
    // shape ([{ name, los, pvs }]).
    async lineOfSight() {
        const body = await (await this._fetch('los')).json();
        return JSON.stringify(body.players || []);
    }

    // demo(): the demo file, for the Player. A failed download is not kept,
    // so the next ask tries again.
    demo() {
        if (!this._demo) {
            this._demo = hub.downloadDemo(this.game, this.source.fetchImpl).then((bytes) => ({
                blob: new Blob([bytes]),
                gzipped: bytes.length > 1 && bytes[0] === 0x1f && bytes[1] === 0x8b,
            }));
            this._demo.catch(() => { this._demo = null; });
        }
        return this._demo;
    }
}
