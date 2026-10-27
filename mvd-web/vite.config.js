import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// mvd-web/index.html is the entry; static/ is copied into dist/ as-is
// (app.js is still a classic script there, as are worker.js, wasm_exec.js,
// vendor/ and the map geometry). The Makefile adds the wasm, the corpora
// and fte-player/ around this — see the build target.
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
