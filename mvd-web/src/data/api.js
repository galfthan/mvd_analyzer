// api.js — the page's mvd-api client. Every call goes to `api/...` on the
// page's own origin; whatever serves the page forwards it to mvd-api's
// /v1/... and adds the key (Caddy's /api route on mvdanalyzer.com, the
// Netlify edge function netlify/edge-functions/api.js on previews,
// scripts/serve-dev.py locally — plan-web-api.md), so the page holds no API
// key. (It does hold the hub's public anon key: src/data/hub.js.)
// No DOM; `fetchImpl` is injectable for tests.

/** @typedef {typeof fetch} FetchImpl */

// Relative, like every asset the page loads (vite base './').
export const API_BASE = 'api/';

// The page's part, sent as ?label= on every call and logged by mvd-api as
// `src`, so its access log tells the analyzer ("web") from the light player
// ("play") — both use the one web key. Set once by each entry point.
let source = '';

/** @param {string} label */
export function setApiSource(label) {
    source = label;
}

// ApiError is a non-2xx answer: its status, and the error envelope's code
// and message ({"error": {"code", "message"}}) when the body had one.
export class ApiError extends Error {
    /**
     * @param {number} status
     * @param {string} code
     * @param {string} message
     */
    constructor(status, code, message) {
        super(message || `mvd-api: HTTP ${status}`);
        this.status = status;
        this.code = code;
    }

    // canFallBack: the answer says nothing about the demo — the API is
    // unreachable, refused the key, is throttling or failing, or is not
    // there at all (a 404 without mvd-api's error envelope: a static server
    // with no /api) — so the hub directly, or analysing in the browser,
    // could still succeed. mvd-api's own 404 (no such game) or a 4xx for
    // the request itself would fail there too.
    get canFallBack() {
        return this.status === 0 || this.status === 401 || this.status === 403 || this.status === 429 ||
            this.status >= 500 || (this.status === 404 && !this.code);
    }
}

/**
 * apiFetch: one request to `api/<path>`; resolves with the Response when
 * it is 2xx, rejects with an ApiError otherwise (status 0 when it never
 * got an answer).
 * @param {string} path  under /v1/, e.g. 'games/123'
 * @param {RequestInit} [init]
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<Response>}
 */
export async function apiFetch(path, init = {}, fetchImpl = fetch) {
    if (source) path += (path.includes('?') ? '&' : '?') + 'label=' + encodeURIComponent(source);
    let resp;
    try {
        resp = await fetchImpl(API_BASE + path, init);
    } catch (err) {
        throw new ApiError(0, 'network', `mvd-api unreachable: ${/** @type {Error} */ (err).message}`);
    }
    if (resp.ok) return resp;
    let code = '', message = '';
    try {
        const body = await resp.json();
        code = body?.error?.code || '';
        message = body?.error?.message || '';
    } catch {}
    throw new ApiError(resp.status, code, message);
}

/**
 * @param {string} path
 * @param {RequestInit} [init]
 * @param {FetchImpl} [fetchImpl]
 * @returns {Promise<any>}
 */
export async function apiJSON(path, init, fetchImpl) {
    return (await apiFetch(path, init, fetchImpl)).json();
}
