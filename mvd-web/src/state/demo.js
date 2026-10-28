// demo.js — the current DemoHandle (src/data/wasm-source.js), null before
// the first load. Set once a load has fully replaced the previous demo.
import { store } from './store.js';

export const demo = store(null);
