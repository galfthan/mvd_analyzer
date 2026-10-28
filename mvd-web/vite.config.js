import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// mvd-web/index.html is the entry; static/ is Vite's publicDir, copied into
// dist/ as-is (worker.js, the fallback wasm_exec.js, the stylesheet,
// vendor/, probe.html and the map geometry). The Makefile adds the wasm,
// the toolchain's wasm_exec.js over the fallback, the loc / map-entity /
// BSP corpora and fte-player/ around this — see the build target.
export default defineConfig({
    base: './',
    publicDir: 'static',
    plugins: [svelte()],
    build: {
        outDir: '../dist',
        emptyOutDir: false,
        // The map, the playlist and their callers are ours; nothing needs
        // transpiling for old browsers (WebGL2 + WASM are required anyway).
        target: 'es2022',
    },
});
