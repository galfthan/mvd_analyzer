// preset-runs.js — every built-in clip preset run once over the loaded demo:
// per preset id, how many rows it found and its clips in time order (null
// while it runs), or the error that stopped it. The Moments pane lists them
// by group, the playlist's Generate menu counts them (and leaves out the
// empty ones). Rerun on a new demo or a new canonical team order (the
// clips' POV and colours) — once per change: a load sets the demo and then
// the team order several times in one task, so the run waits a microtask
// and skips a (demo, order) it already ran. Presets with the same source
// and params share one fetch.

import { store } from '../state/store.js';
import { demo, teams } from '../state/index.js';
import { PRESETS, viewCall, stages } from '../lib/clip-query.js';
import { currentRef } from './index.js';

/** @typedef {{ rows: number, clips: import('../lib/playlist.js').Clip[], error?: string }} PresetRun */

export const presetRuns = store(/** @type {Record<string, PresetRun | null>} */ ({}));

let generation = 0;
let lastKey = /** @type {unknown[]} */ ([]);
let queued = false;

function run() {
    queued = false;
    const handle = demo.get();
    const order = teams.get().order;
    if (lastKey[0] === handle && lastKey[1] === order.join('\u0000')) return;
    lastKey = [handle, order.join('\u0000')];
    const ref = handle ? currentRef() : null;
    const gen = ++generation;
    presetRuns.set(Object.fromEntries(PRESETS.map((p) => [p.id, null])));
    if (!handle || !ref) return;
    const view = handle.view.bind(handle);
    /** @type {Map<string, Promise<any>>} */
    const bodies = new Map();
    for (const p of PRESETS) {
        const key = JSON.stringify([p.query.source, p.query.params || {}]);
        if (!bodies.has(key)) bodies.set(key, viewCall(view, p.query.source, p.query.params));
        /** @type {Promise<any>} */ (bodies.get(key))
            .then((body) => {
                const r = stages(p.query, body, handle.result, ref);
                return { rows: r.ranked.length, clips: [...r.clips].sort((a, b) => a.from - b.from) };
            })
            // A failed run says so (the Moments pane shows it) rather than
            // passing for an empty one.
            .catch((err) => ({ rows: 0, clips: [], error: err?.message || String(err) }))
            .then((v) => {
                if (gen === generation) presetRuns.set({ ...presetRuns.get(), [p.id]: v });
            });
    }
}

function schedule() {
    if (queued) return;
    queued = true;
    queueMicrotask(run);
}

let started = false;
// setupPresetRuns: run on every demo / team order change from now on.
export function setupPresetRuns() {
    if (started) return;
    started = true;
    demo.subscribe(schedule);
    teams.subscribe(schedule);
}
