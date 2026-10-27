// Module entry: mounts the Svelte shell and publishes, on window, the ESM
// pieces app.js uses. app.js is still a classic script (index.html carries
// inline handlers that call its globals). Module scripts are deferred: this
// runs after app.js's top level but before DOMContentLoaded — every call
// site in app.js is inside a function that runs later still.
import { mount, flushSync } from 'svelte';
import App from './shell/App.svelte';
import * as MapView from '../../mvd-map-view/src/index.js';
import * as Playlist from './lib/playlist.js';
import * as MvdHub from './data/hub.js';
import { WasmSource } from './data/wasm-source.js';
import * as MvdState from './state/index.js';

window.MapView = MapView;
window.Playlist = Playlist;
window.MvdHub = MvdHub;
// Shared state: the clock, the team colours, the current demo.
window.MvdState = MvdState;
// The data layer: every demo load, view query, region recompute and line of
// sight goes through this one source (src/data/wasm-source.js).
window.mvdData = new WasmSource(new Worker('worker.js'));
// Mounted synchronously (flushSync runs the adopt actions) so the legacy tab
// markup is in place before app.js's DOMContentLoaded setup finds it by id.
mount(App, { target: document.getElementById('app') });
flushSync();
// app.js defines this on its top level and uses it to allocate the camera.
if (window.onMapViewReady) window.onMapViewReady(MapView);
