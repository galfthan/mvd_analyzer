// timing.js — structured per-stage timing for the demo load path, printed
// to the console (see mvd-web/README.md "Load timing"). `mvdTiming` holds
// one-time facts (wasm load); the per-demo accumulator is built up across
// network → WASM → render and flushed by finishLoadTiming(). Also stashed
// on window.__mvdTimings.

/**
 * @typedef {import('../data/wasm-source.js').LoadTimings} LoadTimings
 * @typedef {{ stage: string, ms: number }} RenderTiming
 * @typedef {object} LoadTimingSummary  the last load's breakdown (window.__mvdTimings)
 * @property {number | null} wasmLoadMs
 * @property {LoadTimings['net']} network
 * @property {any} worker               the worker's own timings, as it sent them
 * @property {number} parseMs
 * @property {RenderTiming[]} render
 * @property {number} downloadToUiMs
 */

export const mvdTiming = { wasmLoadMs: /** @type {number | null} */ (null) };
/** @type {{ t0: number, net: LoadTimings['net'], render: RenderTiming[], worker: any, parseMs: number } | null} */
let loadTiming = null;

export function startLoadTiming() {
    loadTiming = { t0: performance.now(), net: {}, render: [], worker: null, parseMs: 0 };
}

// recordLoadTimings copies a DemoHandle's network / worker / parse timings in.
/** @param {LoadTimings} timings */
export function recordLoadTimings(timings) {
    if (!loadTiming) return;
    Object.assign(loadTiming.net, timings.net);
    loadTiming.worker = timings.worker;
    loadTiming.parseMs = timings.parseMs;
}

/**
 * @template R
 * @param {string} name
 * @param {() => R} fn
 * @returns {R}
 */
export function timeRender(name, fn) {
    if (!loadTiming) return fn();
    const t = performance.now();
    try {
        return fn();
    } finally {
        loadTiming.render.push({ stage: name, ms: +(performance.now() - t).toFixed(2) });
    }
}

export function finishLoadTiming() {
    if (!loadTiming) return;
    const lt = loadTiming;
    loadTiming = null; // close the window before any async render fires
    const total = performance.now() - lt.t0;
    /** @type {LoadTimingSummary} */
    const summary = {
        wasmLoadMs: mvdTiming.wasmLoadMs,
        network: lt.net,
        worker: lt.worker,
        parseMs: +lt.parseMs.toFixed(1),
        render: lt.render,
        downloadToUiMs: +total.toFixed(1),
    };
    window.__mvdTimings = summary;
    try {
        console.groupCollapsed(`[mvd-timing] load breakdown — ${total.toFixed(0)} ms download→UI`);
        if (mvdTiming.wasmLoadMs != null) {
            console.log(`wasm load (one-time): ${mvdTiming.wasmLoadMs.toFixed(1)} ms`);
        }
        console.log('network (ms):', lt.net);
        if (lt.worker) {
            const w = lt.worker;
            const locMs = (w.locFetch || []).reduce(/** @param {number} s @param {{ ms: number }} f */ (s, f) => s + f.ms, 0);
            const bspMs = (w.bspFetch || []).reduce(/** @param {number} s @param {{ ms: number }} f */ (s, f) => s + f.ms, 0);
            console.log(
                `WASM analyze: ${w.wasmAnalyzeMs.toFixed(1)} ms ` +
                `(incl. loc fetch ${locMs.toFixed(1)} ms, bsp fetch ${bspMs.toFixed(1)} ms — ` +
                `subtract from finalize:timelineAnalysis for pure loc compute)`
            );
            console.table((w.goPhases || []).map(/** @param {{ name: string, ms: number }} p */ p => ({ phase: p.name, ms: +p.ms.toFixed(2) })));
        }
        console.log(`result JSON.parse (main thread): ${lt.parseMs.toFixed(1)} ms`);
        console.table(lt.render);
        console.groupEnd();
    } catch (e) {
        console.warn('[mvd-timing] log failed:', e);
    }
}
