// Module entry. app.js is still a classic script (index.html carries inline
// handlers that call its globals), so the ESM pieces it uses are published
// on window here. Module scripts are deferred: this runs after app.js's top
// level but before DOMContentLoaded — every MapView.* / Playlist.* call
// site is inside a function that runs later still.
import * as MapView from '../../mvd-map-view/src/index.js';
import * as Playlist from './lib/playlist.js';

window.MapView = MapView;
window.Playlist = Playlist;
// app.js defines this on its top level and uses it to allocate the camera.
if (window.onMapViewReady) window.onMapViewReady(MapView);
