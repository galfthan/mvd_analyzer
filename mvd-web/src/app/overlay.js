// overlay.js — the wasm-loading overlay (static markup in index.html, so it
// shows before any script runs) and what happens once the analyzer is up:
// the version tag and the page / wasm build check.

import { buildVersion, buildStale } from '../state/shell.js';
import { mvdTiming } from './timing.js';

/** @typedef {{ tag: string, hash: string, date: string }} BuildVersion  the wasm's build (cmd/wasm) */

let analyzerFailed = false;

// analyzerHasFailed: the wasm did not load; the overlay says why, and a
// failed hub load must not drop it.
export function analyzerHasFailed() {
    return analyzerFailed;
}

// Hide the wasm-loading overlay. When auto-loading a demo from a URL
// (?gameId=...), keep it up through the demo download/analyse so the
// user never sees a half-populated Search/Summary tab in the
// background — displayResults() calls hideLoadingOverlay() once the
// pipeline has finished.
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
// or a cache served a stale asset). Reload once — assets are served
// Cache-Control: no-cache, so the reload revalidates everything — and if
// the mismatch survives the reload (deploy still propagating), just flag it
// in the version tag instead of looping.
/** @param {{ hash?: string } | null | undefined} v */
function checkBuildCoherence(v) {
    /** @type {HTMLMetaElement | null} */
    const meta = document.querySelector('meta[name="build"]');
    const pageBuild = meta ? meta.content : '';
    const wasmBuild = v && v.hash ? v.hash : '';
    if (!pageBuild || pageBuild === 'dev') return;
    if (!wasmBuild || wasmBuild === 'dev' || wasmBuild === 'unknown') return;
    if (pageBuild === wasmBuild) return;

    const guard = 'mvd-build-reload';
    const attempted = (() => { try { return sessionStorage.getItem(guard); } catch { return null; } })();
    const key = `${pageBuild}:${wasmBuild}`;
    if (attempted !== key) {
        try { sessionStorage.setItem(guard, key); } catch {}
        location.reload();
        return;
    }
    buildStale.set(true);
}

// onAnalyzerReady runs once the wasm is up (mvdData.ready): records its
// load time, drops the overlay unless a deep-linked demo is about to load,
// and fills the version tag.
/** @param {{ version: BuildVersion | null, wasmLoadMs: number }} info */
export function onAnalyzerReady(info) {
    if (typeof info.wasmLoadMs === 'number') mvdTiming.wasmLoadMs = info.wasmLoadMs;
    const params = new URLSearchParams(location.search);
    const willAutoLoadDemo = !!(params.get('gameId') || params.get('hub'));
    if (willAutoLoadDemo) {
        // Demo download is about to start; keep the overlay up and
        // show the next phase.
        setLoadingOverlayMessage('Loading demo…');
    } else {
        hideLoadingOverlay();
    }
    if (info.version) buildVersion.set(info.version);
    checkBuildCoherence(info.version);
}

// onAnalyzerFailed: the wasm did not load — the overlay stays up with why.
/** @param {Error} err */
export function onAnalyzerFailed(err) {
    analyzerFailed = true;
    setLoadingOverlayMessage(err.message);
}
