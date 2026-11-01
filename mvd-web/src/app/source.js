// source.js — the page's data source: every demo load, view query, region
// recompute and line of sight goes through it (src/data/page-source.js) —
// mvd-api for a hub demo, the in-browser analyzer for a local file or when
// the API cannot answer. The analyzer's worker and wasm are created on the
// first load that needs them; a page that only reads hub demos never loads
// them. ?engine=wasm analyses every demo in the browser.
import { PageSource } from '../data/page-source.js';
import { ApiSource } from '../data/api-source.js';
import { WasmSource } from '../data/wasm-source.js';
import { onAnalyzerReady } from './overlay.js';

// The Result schema this page was built for (`make build` stamps it; absent
// in a dev build, which then trusts whatever the API serves).
const schemaMeta = /** @type {HTMLMetaElement | null} */ (document.querySelector('meta[name="schema"]'));
const schema = Number(schemaMeta?.content) || null;

export const mvdData = new PageSource({
    api: new ApiSource({ schema }),
    makeWasm: () => new WasmSource(new Worker('worker.js')),
    engine: new URLSearchParams(location.search).get('engine'),
    onWasm: (wasm) => { wasm.ready.then(onAnalyzerReady, () => {}); },
});
