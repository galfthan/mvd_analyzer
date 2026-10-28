// shell.js — the page chrome's state: the top bar's demo line and build
// version, and the load panel's status line, hub box and its button. The
// load orchestration (src/app/) sets them; src/shell/Topbar.svelte and the
// Search tab show them.
import { store } from './store.js';

// The top bar's demo line (markup, src/lib/topbar.js), '' with no demo.
export const topbarDemo = store('');

// The analyzer's build ({ tag, hash, date }) once it is up, and whether the
// page and the wasm came from different builds after the one reload
// (src/app/overlay.js checkBuildCoherence).
export const buildVersion = store(null);
export const buildStale = store(false);

// The load panel: its status line ({ text, cls }), the hub box's text and
// whether its Load button is busy.
export const loadStatus = store({ text: '', cls: 'status' });
export const hubInput = store('');
export const hubBusy = store(false);
