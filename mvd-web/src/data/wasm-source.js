// wasm-source.js — the data layer's in-browser implementation: demos are
// analysed by the Go pipeline compiled to WASM, running in static/worker.js.
//
// The page asks a source to `load()` a demo and gets a DemoHandle back; it
// never posts to the worker or fetches from the hub itself. A later
// API-backed source answers the same calls from mvd-api (see
// plan-frontend-restructure.md), which is why the handle speaks in Result
// sections and view-query names rather than worker messages.
//
// The worker holds ONE analysed demo (the Go side's lastResult), so a handle
// is only queryable while it is the current one: a new load() supersedes it
// the moment its analyze is posted, and its view queries reject from then on
// instead of silently answering for the newer demo.
//
// Loads can overlap (a slow hub download, then a quick local file): the
// last load() ASKED FOR wins. Every older one rejects with a
// SupersededError at its next step — before it posts its analyze (which
// would replace the worker's demo under the newer one's handle), and after
// its reply — whether it would have succeeded or failed, so a stale
// failure never reports over the newer demo.

import { WorkerClient } from './worker-client.js';
import * as hub from './hub.js';

class SupersededError extends Error {
    constructor() {
        super('load superseded by a newer one');
        this.superseded = true;
    }
}

export class WasmSource {
    constructor(worker, { fetchImpl = (...a) => fetch(...a) } = {}) {
        this.client = new WorkerClient(worker);
        this.fetchImpl = fetchImpl;
        this.ready = this.client.ready;
        this.current = null;
        this._loadSeq = 0;
        this._byReqId = new Map();
        this.client.onFollowup((msg) => this._onFollowup(msg));
    }

    // load(input, { onStage }) → DemoHandle.
    //   input: { bytes, name }  a local file's bytes
    //          { game }         a hub row already in hand (search results)
    //          { gameId }       a hub game id, looked up first
    //   onStage('info' | 'download' | 'analyze') reports progress.
    // Waits for the wasm if it is still loading. Rejects with a
    // SupersededError once a newer load() was called.
    async load(input, opts = {}) {
        const seq = ++this._loadSeq;
        const stale = () => seq !== this._loadSeq;
        try {
            return await this._load(input, opts, stale);
        } catch (err) {
            throw stale() ? new SupersededError() : err;
        }
    }

    async _load(input, { onStage = () => {} }, stale) {
        const live = () => { if (stale()) throw new SupersededError(); };
        await this.ready;
        live();
        const net = {};
        let { bytes, name, game } = input;
        if (input.gameId != null) {
            onStage('info');
            const t = performance.now();
            game = await hub.fetchGame(input.gameId, this.fetchImpl);
            live();
            net.gameInfoFetch = +(performance.now() - t).toFixed(1);
        }
        if (game) {
            onStage('download');
            const t = performance.now();
            bytes = await hub.downloadDemo(game, this.fetchImpl);
            live();
            net.demoDownload = +(performance.now() - t).toFixed(1);
            name = hub.demoFilename(game);
        }
        onStage('analyze');
        // The analyze transfers (detaches) the buffer, so the player's copy
        // is taken first.
        const handle = new DemoHandle(this, {
            name, game: game || null,
            blob: new Blob([bytes]),
            gzipped: bytes.length > 1 && bytes[0] === 0x1f && bytes[1] === 0x8b,
        });
        const prev = this.current;
        if (prev) prev.current = false;
        this.current = handle;
        const { reqId, promise } = this.client.request(
            'analyze', { bytes: bytes.buffer, filename: name }, [bytes.buffer]);
        this._byReqId.set(reqId, handle);
        // A failed analyze leaves the Go side holding the previous demo
        // (cmd/wasm analyze returns before replacing lastResult), so the
        // previous handle is queryable again.
        const fail = (err) => {
            this._byReqId.delete(reqId);
            handle.current = false;
            if (this.current === handle) {
                this.current = prev;
                if (prev) prev.current = true;
            }
            throw err;
        };
        let reply;
        try {
            reply = await promise;
        } catch (err) {
            fail(err);
        }
        // Analysed, but a newer load was asked for meanwhile: the worker
        // holds this demo until that one posts, and nobody shows it.
        live();
        const tParse = performance.now();
        const result = JSON.parse(reply.json);
        if (result.error) fail(new Error(result.error));
        handle.result = result;
        handle.timings = { net, worker: reply.timings || null, parseMs: performance.now() - tParse };
        return handle;
    }

    // The deferred 50 ms bucket view and region states for an analyze that
    // already answered, or an error the worker raised while building them.
    _onFollowup(msg) {
        const handle = this._byReqId.get(msg.reqId);
        if (!handle) return;
        this._byReqId.delete(msg.reqId);
        if (msg.type === 'buckets') handle._applyBuckets(msg);
        else if (msg.type === 'error') handle._buckets.reject(new Error(msg.message));
    }

    async _view(handle, type, payload) {
        if (!handle.current) throw new Error('demo superseded by a newer load');
        const { promise } = this.client.request(type, payload);
        const reply = await promise;
        return reply.json;
    }
}

export class DemoHandle {
    constructor(source, { name, game, blob, gzipped }) {
        this.source = source;
        this.name = name;           // file name; the engine needs the extension
        this.game = game;           // the hub row, or null for a local file
        this.ref = {
            key: game ? `hub:${game.id}` : `file:${name}`,
            gameId: game ? game.id : null,
        };
        this.current = true;
        this.result = null;         // the whole Result
        this.timings = null;
        this.demoBlob = blob;       // the original demo, for the player
        this.gzipped = gzipped;
        let settle;
        const p = new Promise((resolve, reject) => { settle = { resolve, reject }; });
        p.catch(() => {});
        this._buckets = { promise: p, ...settle };
    }

    // section(name) resolves one top-level Result section.
    async section(name) {
        return this.result[name];
    }

    // ready('buckets') resolves once the deferred bucket view and region
    // states are on the Result (with their build timings), and rejects when
    // the worker failed to build them.
    ready(what) {
        if (what !== 'buckets') throw new Error(`unknown readiness: ${what}`);
        return this._buckets.promise;
    }

    // view(fn, opts) runs one mvd-analytics view query (getTopWindows,
    // getTopKills, …) and resolves with its decoded JSON; an {"error":…}
    // envelope rejects, so one .catch covers transport and the view's own
    // unavailable cases.
    async view(fn, opts) {
        const json = await this.source._view(this, 'viewQuery', { fn, optsJSON: JSON.stringify(opts || {}) });
        const v = JSON.parse(json);
        if (v && v.error) throw new Error(v.error);
        return v;
    }

    // recomputeRegions re-runs region control for an edited region layout;
    // resolves with the worker's raw JSON string.
    recomputeRegions(overrideJSON) {
        return this.source._view(this, 'recomputeRegions', { overrideJSON });
    }

    // lineOfSight runs the lazy LOS/PVS pass; resolves with raw JSON.
    lineOfSight() {
        return this.source._view(this, 'computeLos', {});
    }

    _applyBuckets(msg) {
        const ta = this.result?.timelineAnalysis;
        if (ta && msg.bucketsJSON) {
            try {
                const view = JSON.parse(msg.bucketsJSON);
                // Column-major ColumnarBuckets object.
                if (view && typeof view === 'object' && !view.error) ta.bucketView = view;
            } catch (e) {
                console.warn('parse deferred buckets failed:', e);
            }
        }
        if (ta && ta.regionControl && msg.regionStatesJSON) {
            try {
                const rs = JSON.parse(msg.regionStatesJSON);
                if (!rs.error && rs.bucketStates) {
                    ta.regionControl.bucketStates = rs.bucketStates;
                    if (rs.stats) ta.regionControl.stats = rs.stats;
                }
            } catch (e) {
                console.warn('parse deferred region states failed:', e);
            }
        }
        this._buckets.resolve({ bucketsMs: msg.bucketsMs, regionMs: msg.regionMs });
    }
}
