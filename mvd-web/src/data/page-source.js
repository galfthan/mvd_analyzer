// page-source.js — the page's one data source (src/app/source.js): it picks
// where each demo is analysed (plan-web-api.md).
//
//   a hub demo     mvd-api (ApiSource); the browser loads no analyzer
//   a local file   the in-browser WASM build (WasmSource), created on the
//                  first load that needs it
//   ?engine=wasm   every demo in the browser, as before the API
//
// A hub demo falls back to the browser when the API cannot answer for it:
// unreachable, the key refused, throttled, failing, or serving another
// schema version than this page was built for (canFallBack). The demo's
// row comes from the API too, so a demo it cannot even look up fails.
//
// Loads can overlap, across both sources: the last load() asked for wins,
// as WasmSource does on its own — an older one rejects with a
// SupersededError, and is kept from posting its analyze to the worker.

import * as hub from './hub.js';
import { SupersededError } from './wasm-source.js';

/**
 * @typedef {import('./wasm-source.js').WasmSource} WasmSource
 * @typedef {import('./api-source.js').ApiSource} ApiSource
 * @typedef {import('./wasm-source.js').LoadInput} LoadInput
 * @typedef {import('./wasm-source.js').LoadOptions} LoadOptions
 * @typedef {import('../state/demo.js').DemoHandle} DemoHandle
 * @typedef {import('./hub.js').FetchImpl} FetchImpl
 */

export class PageSource {
    /**
     * @param {{ api: ApiSource, makeWasm: () => WasmSource, engine?: string | null, fetchImpl?: FetchImpl, onWasm?: (wasm: WasmSource) => void }} deps
     *   engine: 'wasm' analyses every demo in the browser; onWasm sees the
     *   WASM source once it is created
     */
    constructor({ api, makeWasm, engine = null, fetchImpl = (...a) => fetch(...a), onWasm = () => {} }) {
        this.api = api;
        this.makeWasm = makeWasm;
        this.wasmOnly = engine === 'wasm';
        this.fetchImpl = fetchImpl;
        this.onWasm = onWasm;
        /** @type {WasmSource | null} */
        this._wasm = null;
        this._loadSeq = 0;
    }

    // wasm: the in-browser analyzer, started on first use.
    wasm() {
        if (!this._wasm) {
            this._wasm = this.makeWasm();
            this.onWasm(this._wasm);
        }
        return this._wasm;
    }

    /**
     * load(input, opts) → a DemoHandle, from the API or the browser.
     * onStage reports 'info' (the hub row), 'server' (mvd-api analysing),
     * 'engine' (the browser's analyzer starting) and WasmSource's
     * 'download' / 'analyze'.
     * @param {LoadInput} input
     * @param {LoadOptions} [opts]
     * @returns {Promise<DemoHandle>}
     */
    async load(input, opts = {}) {
        const seq = ++this._loadSeq;
        const stale = () => seq !== this._loadSeq;
        try {
            return await this._load(input, opts, stale);
        } catch (err) {
            throw stale() ? new SupersededError() : err;
        }
    }

    /**
     * @param {LoadInput} input
     * @param {LoadOptions} opts
     * @param {() => boolean} stale
     * @returns {Promise<DemoHandle>}
     */
    async _load(input, opts, stale) {
        const { onStage = () => {}, stillWanted } = opts;
        const wanted = () => !stale() && (!stillWanted || stillWanted());
        const live = () => { if (stale()) throw new SupersededError(); };
        if ('bytes' in input || this.wasmOnly) return this._inBrowser(input, opts, wanted);

        /** @type {{ gameInfoFetch?: number }} */
        const net = {};
        let game = 'game' in input ? input.game : null;
        if (!game) {
            onStage('info');
            const t = performance.now();
            game = await hub.fetchGame(/** @type {{ gameId: number | string }} */ (input).gameId, this.fetchImpl);
            live();
            net.gameInfoFetch = +(performance.now() - t).toFixed(1);
        }
        if (!wanted()) throw new SupersededError();
        onStage('server');
        try {
            const handle = await this.api.load(game, net);
            live();
            return handle;
        } catch (err) {
            if (/** @type {{ superseded?: boolean }} */ (err).superseded) throw err;
            live();
            if (!(/** @type {{ canFallBack?: boolean }} */ (err).canFallBack)) throw err;
            console.warn(`[mvd] analysing in the browser: ${/** @type {Error} */ (err).message}`);
            const handle = await this._inBrowser({ game }, opts, wanted);
            Object.assign(/** @type {{ net: object }} */ (handle.timings).net, net);
            return handle;
        }
    }

    /**
     * @param {LoadInput} input
     * @param {LoadOptions} opts
     * @param {() => boolean} wanted  false once a newer load was asked for,
     *   or the caller dropped this one: the worker then keeps its demo
     * @returns {Promise<DemoHandle>}
     */
    _inBrowser(input, opts, wanted) {
        const wasm = this.wasm();
        if (!wasm.up) opts.onStage?.('engine');
        return wasm.load(input, { onStage: opts.onStage, stillWanted: wanted });
    }
}
