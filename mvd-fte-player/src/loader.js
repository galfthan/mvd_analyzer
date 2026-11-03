// startFte boots ftewebgl in `win` and resolves with an FteController once
// the engine is up. Emscripten builds publish themselves through globals
// (`Module`, `FTEC`), read their configuration from `win.Module` at script
// load, and cannot be torn down — so one window hosts at most one engine
// for its lifetime. player.html is that window; to play another demo the
// host reloads the iframe.
//
// Mirrors hub.quakeworld.nu src/lib/fte/hooks.ts useFteLoader @ 5c96205c19c7
// (MIT, see ../LICENSE): same Module shape, same "engine is ready once
// Module.getClientState exists" probe.

import { FteController } from './fte-controller.js';
import { demoAssets, FTE_MANIFEST_URL, FTE_SCRIPT_URL } from './assets.js';
import { createAssetFetcher } from './asset-fetch.js';

const READY_POLL_MS = 100;

// `fetchAsset` (url → Promise<ArrayBuffer>, asset-fetch.js): a host's
// fetcher, shared across its engines so the next one boots from memory;
// without, this engine gets one of its own.
export function startFte({ win, canvas, demoUrl, demoPath, mapName, configUrl, demoDuration = null, onProgress = null, fetchAsset = createAssetFetcher() }) {
    if (win.Module) return Promise.reject(new Error('FTE already started in this window'));

    // Every file as a Promise of its bytes, not a URL: FTE would fetch the
    // URLs all at once (asset-fetch.js). A file that cannot be had is left
    // out, as a failed fetch of FTE's own leaves it; FTE logs why.
    const urls = demoAssets({ demoUrl, demoPath, mapName, configUrl });
    const total = Object.keys(urls).length;
    let loaded = 0;
    const files = {};
    for (const [path, url] of Object.entries(urls)) {
        files[path] = fetchAsset(url).then((buf) => {
            loaded++;
            if (onProgress) onProgress(loaded, total);
            return buf;
        });
    }
    let fte = null;

    // Collect the engine's AudioContexts so the host can resume them from
    // its own click (the engine only listens for gestures in this window).
    const audioContexts = [];
    for (const name of ['AudioContext', 'webkitAudioContext']) {
        const Base = win[name];
        if (!Base) continue;
        win[name] = class extends Base {
            constructor(...args) {
                super(...args);
                audioContexts.push(this);
            }
        };
    }
    const resumeAudio = () => {
        for (const ctx of audioContexts) if (ctx.state === 'suspended') ctx.resume();
    };

    // FTE has no end-of-demo callback; its console line is the signal. The
    // engine prints with console.log directly (_emscriptenfte_print, not
    // Module.print), so watch this window's console — it is the engine's own.
    const log = win.console.log.bind(win.console);
    win.console.log = (...args) => {
        log(...args);
        if (fte && String(args[0]).includes('EndOfDemo')) fte.demoEnded();
    };

    win.Module = {
        canvas,
        manifest: FTE_MANIFEST_URL,
        arguments: ['-manifest', FTE_MANIFEST_URL],
        files,
    };

    return new Promise((resolve, reject) => {
        const script = win.document.createElement('script');
        script.src = FTE_SCRIPT_URL;
        script.onerror = () => reject(new Error(`failed to load ${FTE_SCRIPT_URL}`));
        win.document.head.appendChild(script);

        const poll = setInterval(() => {
            if (!win.Module.getClientState) return;
            clearInterval(poll);
            const name = demoPath.split('/').pop().replace(/\.mvd(\.gz)?$/, '');
            fte = new FteController(win, { demoDuration, demoName: name, resumeAudio });
            // Hub starts muted (config.cfg also sets volume 0); keep that.
            fte.mute();
            resolve(fte);
        }, READY_POLL_MS);
    });
}
