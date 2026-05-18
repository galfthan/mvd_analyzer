// Web Worker for MVD analysis via WASM
// Loads Go WASM binary and exposes analyzeMVD() off the main thread

importScripts('wasm_exec.js');

// Synchronous loc fetcher exposed to the WASM module. The Go side calls
// this from internal/loc/loader_wasm.go during analysis to pull the
// per-map .loc file on demand instead of bundling all locs into the
// WASM binary. Sync XHR is deprecated on the main thread but is still
// allowed inside Web Workers, which is exactly where we run.
//
// We return a Uint8Array (binary) rather than responseText. Loc files
// from quakeworld.nu often encode item-name shorthands as high-bit
// ASCII (e.g. "ssg" → 0xf3 0xf3 0xe7) which is not valid UTF-8;
// responseText would silently mangle those bytes into U+FFFD, breaking
// substituteVariables() on the Go side and producing garbled location
// names like "o?=o?=o?=.RL.low" instead of "SSG.RL.low".
self.fetchLocSync = function(mapName) {
    if (!mapName) return null;
    try {
        const xhr = new XMLHttpRequest();
        xhr.open('GET', 'locs/' + mapName + '.loc', false); // false = synchronous
        xhr.responseType = 'arraybuffer';
        xhr.send(null);
        if (xhr.status === 200 && xhr.response) {
            return new Uint8Array(xhr.response);
        }
    } catch (e) {
        // Network errors / CORS / 404 — fall through to null so Go reports
        // "no loc file for map ...".
    }
    return null;
};

// Synchronous BSP fetcher exposed to the WASM module. The Go side calls
// this from mvd-analytics/locvis/loader_wasm.go during analysis to pull
// the per-map .bsp file used by the visibility-aware loc attribution
// filter (V6 / V6a). Returns null on 404 / missing BSP, which causes
// locvis to degrade to V1 (pure Euclidean nearest-neighbour) for that
// map — never a hard error. BSPs are deployed to dist/bsps/ by `make
// build` when `make bsps` has been run; deployments without the BSP
// directory simply get V1 everywhere.
self.fetchBspSync = function(mapName) {
    if (!mapName) return null;
    try {
        const xhr = new XMLHttpRequest();
        xhr.open('GET', 'bsps/' + mapName + '.bsp', false);
        xhr.responseType = 'arraybuffer';
        xhr.send(null);
        if (xhr.status === 200 && xhr.response) {
            return new Uint8Array(xhr.response);
        }
    } catch (e) {
        // 404 / network / CORS — null falls back to V1 on the Go side.
    }
    return null;
};

let wasmReady = false;

async function initWasm() {
    const go = new Go();
    const result = await WebAssembly.instantiateStreaming(
        fetch('analyzer.wasm'), go.importObject
    );
    go.run(result.instance);
    wasmReady = true;
    postMessage({ type: 'ready', version: self.wasmVersion || null });
}

initWasm().catch(err => {
    postMessage({ type: 'error', message: 'Failed to load WASM: ' + err.message });
});

onmessage = function(e) {
    if (e.data.type === 'analyze') {
        if (!wasmReady) {
            postMessage({ type: 'error', message: 'WASM not loaded yet' });
            return;
        }
        try {
            const bytes = new Uint8Array(e.data.bytes);
            const filename = e.data.filename || 'demo.mvd';
            const jsonStr = analyzeMVD(bytes, filename);
            // Schema v7: highResBuckets and regionControl.bucketStates
            // are no longer on the parse-time result. Build them via
            // the bridge (lives on the worker's global where the WASM
            // exports do) and pass back so the main thread can stash
            // both onto the parsed result — keeping every existing
            // panel's read pattern working unchanged.
            let bucketsJSON = '';
            try {
                bucketsJSON = getDefaultBuckets();
            } catch (err) {
                bucketsJSON = '';
            }
            let regionStatesJSON = '';
            try {
                const parsed = JSON.parse(jsonStr);
                const rc = parsed.timelineAnalysis && parsed.timelineAnalysis.regionControl;
                if (rc && rc.regions && rc.teamA && rc.teamB) {
                    const overrideJSON = JSON.stringify({
                        regions: rc.regions.map(r => ({
                            name: r.name,
                            locs: [...new Set((r.points || []).map(p => p.name))],
                        })),
                    });
                    regionStatesJSON = recomputeRegionControl(overrideJSON);
                }
            } catch (err) {
                regionStatesJSON = '';
            }
            postMessage({ type: 'result', json: jsonStr, bucketsJSON, regionStatesJSON });
        } catch (err) {
            postMessage({ type: 'error', message: err.message || String(err) });
        }
    } else if (e.data.type === 'recomputeRegions') {
        // recomputeRegionControl is a Go export living on this worker's
        // self (js.Global() resolves to the worker scope). Calling it
        // from the main page would NameError — that's what the v6
        // shipping bug was — so the round-trip goes through here.
        if (!wasmReady) {
            postMessage({ type: 'recompute_error', message: 'WASM not loaded yet' });
            return;
        }
        try {
            const jsonStr = recomputeRegionControl(e.data.overrideJSON);
            postMessage({ type: 'recompute_result', json: jsonStr });
        } catch (err) {
            postMessage({ type: 'recompute_error', message: err.message || String(err) });
        }
    }
};
