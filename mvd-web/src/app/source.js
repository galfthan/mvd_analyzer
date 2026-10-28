// source.js — the page's data source: every demo load, view query, region
// recompute and line of sight goes through this one WasmSource
// (src/data/wasm-source.js) and its worker.
import { WasmSource } from '../data/wasm-source.js';

export const mvdData = new WasmSource(new Worker('worker.js'));
