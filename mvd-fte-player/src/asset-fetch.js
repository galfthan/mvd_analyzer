// createAssetFetcher: the engine's files, fetched a few at a time and kept.
//
// FTE fetches every file of its manifest at once when handed URLs — about
// 450 requests in the same instant (assets.js). Chrome turns part of such a
// burst down with net::ERR_INSUFFICIENT_RESOURCES, and FTE does not retry:
// each file that failed is missing from that engine for its lifetime — a
// model that never draws (seen: the backpack, the player), with only an
// "Unable to load progs/….mdl" line in its console. FTE also takes a
// Promise of the bytes per file (ftewebgl.js, Module.files), so the loader
// hands it these instead of URLs: at most `concurrency` requests in flight,
// a network failure or a 5xx retried, and the bytes kept by URL — a host
// that shares one fetcher across engines (mvd-web's PlayerEngine, which
// starts one per demo) boots the next engine from memory. Each engine gets
// its own copy: FTE adopts the buffer it is given as the file's storage.
//
// Only http(s) URLs are kept and retried; a blob: URL (the demo) is one
// engine's, and fails for good once its engine is gone (revoked).

/**
 * @param {{ concurrency?: number, retries?: number, retryDelayMs?: number, fetch?: typeof fetch }} [opts]
 * @returns {(url: string) => Promise<ArrayBuffer>}
 */
export function createAssetFetcher({ concurrency = 16, retries = 4, retryDelayMs = 250, fetch: doFetch = (u) => globalThis.fetch(u) } = {}) {
    /** @type {Map<string, Promise<ArrayBuffer>>} */
    const kept = new Map();
    /** @type {Array<() => void>} */
    const queue = [];
    let active = 0;

    // slot resolves once fewer than `concurrency` requests are in flight;
    // the caller releases it when its request is done.
    const slot = () => new Promise((resolve) => {
        if (active < concurrency) {
            active++;
            resolve(undefined);
        } else {
            queue.push(() => {
                active++;
                resolve(undefined);
            });
        }
    });
    const release = () => {
        active--;
        const next = queue.shift();
        if (next) next();
    };

    /**
     * @param {string} url
     * @param {number} tries  retries left after the first attempt
     */
    async function load(url, tries) {
        for (let attempt = 0; ; attempt++) {
            await slot();
            let res;
            try {
                res = await doFetch(url);
                if (res.ok) return await res.arrayBuffer();
            } catch (err) {
                // A network failure (the burst limit among them): retried.
                if (attempt >= tries) throw err;
                res = null;
            } finally {
                release();
            }
            // A 4xx is the file's answer (FTE probes names that do not exist).
            if (res && (res.status < 500 || attempt >= tries)) throw new Error(`${url}: HTTP ${res.status}`);
            await new Promise((r) => setTimeout(r, retryDelayMs * 2 ** attempt));
        }
    }

    return (url) => {
        const keep = /^https?:/.test(url);
        let p = keep ? kept.get(url) : undefined;
        if (!p) {
            p = load(url, keep ? retries : 0);
            if (keep) {
                kept.set(url, p);
                p.catch(() => kept.delete(url));
            }
        }
        return p.then((buf) => buf.slice(0));
    };
}
