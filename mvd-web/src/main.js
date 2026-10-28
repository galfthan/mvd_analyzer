// Module entry: mounts the Svelte shell and starts the page (src/app/boot.js).
//
// What it publishes on window is for the browser console and the headless
// checks only — no page code reads these globals:
//   MvdState   the shared stores (src/state/index.js)
//   MvdMapTab  the Map tab's map view and loader (src/tabs/map/map-model.js)
//   MvdPlayer  the Player tab's engine (src/player/engine.js)
//   mvdData    the data source (src/app/source.js)
//   loadDemo   load and render a demo: { gameId } / { game } / { bytes, name }
// (and window.__mvdTimings, the last load's timing breakdown, src/app/timing.js).
import { mount, flushSync } from 'svelte';
import App from './shell/App.svelte';
import * as MvdState from './state/index.js';
import { engine, setupPlayer } from './player/index.js';
import * as MvdMapTab from './tabs/map/map-model.js';
import { setupPanels } from './ui/panels/index.js';
import { mvdData } from './app/source.js';
import { loadDemo } from './app/load.js';
import { boot } from './app/boot.js';

window.MvdState = MvdState;
window.MvdPlayer = engine;
window.MvdMapTab = MvdMapTab;
window.mvdData = mvdData;
window.loadDemo = loadDemo;
// The CSS mirror of the team colours (styles.css --team-a..--team-d): the
// canonical order's first four.
MvdState.teams.subscribe((t) => {
    ['--team-a', '--team-b', '--team-c', '--team-d'].forEach((v, i) =>
        document.documentElement.style.setProperty(v, t.colors[i]));
});
// Mounted synchronously (flushSync runs the mount's effects) so every tab
// is in the page before the boot looks tabs up by id.
mount(App, { target: /** @type {HTMLElement} */ (document.getElementById('app')) });
flushSync();
setupPlayer();
// The viewer's panel arrangement on the multi-panel tabs.
setupPanels();
// The URL, the overlay, a deep-linked demo, the URL's search. A module
// script runs once the document is parsed, before DOMContentLoaded.
boot();
