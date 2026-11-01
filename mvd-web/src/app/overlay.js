// overlay.js — the loading overlay (static markup in index.html, so it
// shows before any script runs), the version tag, and the page / wasm
// build check once the in-browser analyzer is up.

import { buildVersion, buildStale } from '../state/shell.js';
import { mvdTiming } from './timing.js';

/** @typedef {{ tag: string, hash: string, date: string }} BuildVersion  the build (`make build` stamps the page; the wasm embeds the same) */

// prepareLoadingOverlay: on a deep link (?gameId=) the overlay stays up
// through the demo's load, so the user never sees a half-populated tab
// behind it — displayResults() drops it once the demo is shown; otherwise
// it goes at once (nothing has to load before the page is usable).
/** @param {boolean} deepLink */
export function prepareLoadingOverlay(deepLink) {
    if (deepLink) setLoadingOverlayMessage('Loading demo…');
    else hideLoadingOverlay();
}

export function hideLoadingOverlay() {
    const overlay = document.getElementById('wasm-loading');
    if (overlay) overlay.style.display = 'none';
}

/** @param {string} text */
function setLoadingOverlayMessage(text) {
    const overlay = document.getElementById('wasm-loading');
    if (!overlay) return;
    // The subtitle is the second flex child (the small status line).
    const subtitle = overlay.children[1];
    if (subtitle) subtitle.textContent = text;
}

// Version handshake: `make build` stamps <meta name="build"> in index.html
// with the git hash, and the wasm reports the hash it was built from. Both
// deploy as one unit, so a mismatch means the browser is running mixed
// cached versions (a deploy landed between fetching the page and the wasm,
// or a cache served a stale asset). The wasm loads mid-session now — on the
// first local file, or a hub demo the API cannot answer for — so a reload
// would drop what the user just asked for: the version tag asks them to
// reload instead (assets are served Cache-Control: no-cache, so a reload
// revalidates everything).
/** @param {{ hash?: string } | null | undefined} v */
function checkBuildCoherence(v) {
    /** @type {HTMLMetaElement | null} */
    const meta = document.querySelector('meta[name="build"]');
    const pageBuild = meta ? meta.content : '';
    const wasmBuild = v && v.hash ? v.hash : '';
    if (!pageBuild || pageBuild === 'dev') return;
    if (!wasmBuild || wasmBuild === 'dev' || wasmBuild === 'unknown') return;
    if (pageBuild === wasmBuild) return;

    console.warn(`[mvd] this page is build ${pageBuild}, the analyzer ${wasmBuild}: reload for the current version`);
    buildStale.set(true);
}

// showBuildVersion fills the version tag from the build stamps on the page
// (`make build`); a dev build has none and shows no tag.
export function showBuildVersion() {
    /** @param {string} name */
    const meta = (name) => /** @type {HTMLMetaElement | null} */ (document.querySelector(`meta[name="${name}"]`))?.content || '';
    const hash = meta('build');
    if (!hash || hash === 'dev') return;
    buildVersion.set({ tag: meta('build-tag'), hash, date: meta('build-date') });
}

// onAnalyzerReady runs once the in-browser analyzer is up (the first load
// that needs it): records its load time and checks its build against the
// page's.
/** @param {{ version: BuildVersion | null, wasmLoadMs: number }} info */
export function onAnalyzerReady(info) {
    if (typeof info.wasmLoadMs === 'number') mvdTiming.wasmLoadMs = info.wasmLoadMs;
    checkBuildCoherence(info.version);
}
