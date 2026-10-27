// worker-client.js — request/reply over the analyzer worker (static/worker.js).
//
// Each request carries a monotonic `reqId` that the worker echoes on its
// reply, so two in-flight calls on the same lane can never settle each
// other's promise. Messages that are not the first reply to a request (the
// deferred 'buckets' follow-up to an analyze, an error raised after the
// result went out) go to `onFollowup` listeners with their reqId.

// The worker's reply types per request lane: [success, failure].
const LANES = {
    analyze: ['result', 'error'],
    recomputeRegions: ['recompute_result', 'recompute_error'],
    computeLos: ['los_result', 'los_error'],
    viewQuery: ['view_result', 'view_error'],
};

export class WorkerClient {
    constructor(worker) {
        this.worker = worker;
        this.nextReqId = 1;
        this.pending = new Map();
        this.followups = new Set();
        // Resolves with { version, wasmLoadMs } once the wasm is up; rejects
        // when it fails to load (the one error with no reqId).
        this.ready = new Promise((resolve, reject) => {
            this._ready = { resolve, reject };
        });
        worker.onmessage = (e) => this._onMessage(e.data);
    }

    // request posts `{ type, reqId, ...payload }` and resolves with the
    // worker's success reply (the whole message), rejecting on its failure
    // reply.
    request(type, payload = {}, transfer = []) {
        if (!LANES[type]) throw new Error(`unknown worker request: ${type}`);
        const reqId = this.nextReqId++;
        const promise = new Promise((resolve, reject) => {
            this.pending.set(reqId, { lane: LANES[type], resolve, reject });
        });
        this.worker.postMessage({ type, reqId, ...payload }, transfer);
        return { reqId, promise };
    }

    onFollowup(fn) {
        this.followups.add(fn);
        return () => this.followups.delete(fn);
    }

    _onMessage(msg) {
        if (msg.type === 'ready') {
            this._ready.resolve({ version: msg.version || null, wasmLoadMs: msg.wasmLoadMs });
            return;
        }
        if (msg.reqId == null) {
            if (msg.type === 'error') this._ready.reject(new Error(msg.message));
            return;
        }
        const p = this.pending.get(msg.reqId);
        if (p && (msg.type === p.lane[0] || msg.type === p.lane[1])) {
            this.pending.delete(msg.reqId);
            if (msg.type === p.lane[0]) p.resolve(msg);
            else p.reject(new Error(msg.message));
            return;
        }
        for (const fn of this.followups) fn(msg);
    }
}
