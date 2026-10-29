// preset-runs.js — every built-in clip preset run once over the loaded demo:
// per preset id, how many rows it found and its clips in time order (null
// while it runs). The Moments pane lists them by group, the playlist's
// Generate menu counts them (and leaves out the empty ones). Rerun on a new
// demo or a new canonical team order (the clips' POV and colours).

import { store } from '../state/store.js';
import { demo, teams } from '../state/index.js';
import { PRESETS, viewCall, stages } from '../lib/clip-query.js';
import { currentRef } from './index.js';

/** @typedef {{ rows: number, clips: import('../lib/playlist.js').Clip[] }} PresetRun */

export const presetRuns = store(/** @type {Record<string, PresetRun | null>} */ ({}));

let generation = 0;

function run() {
    const handle = demo.get();
    const ref = handle ? currentRef() : null;
    const gen = ++generation;
    presetRuns.set(Object.fromEntries(PRESETS.map((p) => [p.id, null])));
    if (!handle || !ref) return;
    const view = handle.view.bind(handle);
    for (const p of PRESETS) {
        // A source this demo lacks (no highlights section) finds nothing.
        viewCall(view, p.query.source, p.query.params)
            .then((body) => {
                const r = stages(p.query, body, handle.result, ref);
                return { rows: r.ranked.length, clips: [...r.clips].sort((a, b) => a.from - b.from) };
            })
            .catch(() => ({ rows: 0, clips: [] }))
            .then((v) => {
                if (gen === generation) presetRuns.set({ ...presetRuns.get(), [p.id]: v });
            });
    }
}

let started = false;
// setupPresetRuns: run on every demo / team order change from now on.
export function setupPresetRuns() {
    if (started) return;
    started = true;
    demo.subscribe(run);
    teams.subscribe(run);
}
