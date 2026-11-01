// demo.js — the current DemoHandle, null before the first load: from mvd-api
// (src/data/api-source.js) or the browser's analyzer (src/data/wasm-source.js),
// which answer the same calls. Set once a load has fully replaced the
// previous demo.
import { store } from './store.js';

/** @typedef {import('../data/wasm-source.js').DemoHandle | import('../data/api-source.js').ApiDemoHandle} DemoHandle */

export const demo = store(/** @type {DemoHandle | null} */ (null));
