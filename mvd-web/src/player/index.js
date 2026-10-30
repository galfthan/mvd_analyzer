// The Player tab's controller: one engine, one playlist runner, the hub link
// routing and the share-link imports (playlists and clip queries). The unified bar's play / speed
// (src/ui/PlaybackBar.svelte) drive the engine while it is the clock;
// components import from here.

import { clock, demo, teams, activeTab, selectTab, segment, setPlaybackGate } from '../state/index.js';
import { store } from '../state/store.js';
import { paneLayout, columnOf, showPane } from './layout.js';
import { loadDemo } from '../app/load.js';
import { decodePlaylist } from '../lib/playlist.js';
import { decodeQuery } from '../lib/clip-query.js';
import { crossedEnd } from '../lib/playback-bar.js';
import { openInFinder } from './clip-presets.js';
import { PlayerEngine } from './engine.js';
import { createRunner } from './runner.js';
import { createPageDriver } from './page-driver.js';
import { demoRefOf } from './demo-ref.js';
import { playlist, addClips, keepDemos } from './playlist-store.js';
import { installHubLinkRouting } from './hub-links.js';
import { toast } from './toast.js';

// The engine is wanted while the layout shown (the Map or Player tab's)
// shows a Player pane — open, not collapsed, not in a hidden side column.
// It follows the layout, not the pane's placeholder: that remounts a
// microtask later (and on a move across columns), which left an engine
// playing on the Map tab or booting for nothing. Unwanted, the engine is
// paused and suspended at once, and dropped after UNLOAD_AFTER_MS away
// (a quick Map ↔ Player switch keeps it).
const UNLOAD_AFTER_MS = 20000;
/** @param {import('./layout.js').PaneLayout} l */
const showsPlayer = (l) => !!columnOf(l, 'player') && !l.collapsed.player && !(l.hidden && columnOf(l, 'player') === 'order');
const wanted = store(showsPlayer(paneLayout.get()));
paneLayout.subscribe((l) => {
    const on = showsPlayer(l);
    if (on !== wanted.get()) wanted.set(on);
});

export const engine = new PlayerEngine({ clock, demo, activeTab, wanted });
// The engine, up or loading, is the clock on its tab: the page's loop
// stays off.
setPlaybackGate(() => engine.active());

/** @type {ReturnType<typeof setTimeout> | undefined} */
let unloadTimer;
wanted.subscribe((on) => {
    clearTimeout(unloadTimer);
    if (!on) unloadTimer = setTimeout(() => { if (!wanted.get()) engine.teardown(); }, UNLOAD_AFTER_MS);
});

// A clip in the engine plays on the Player tab (its view), with its Player
// pane shown — the viewer may have closed or collapsed it, or hidden the
// side column it is in.
/** @param {string} tab */
function showPlayer(tab) {
    selectTab(tab);
    if (!showsPlayer(paneLayout.get())) showPane('player');
}
// On the Map tab without a Player pane a clip plays there, on the map
// (page-driver.js); anywhere else in the engine.
const page = createPageDriver();
const onMap = () => activeTab.get() === 'map' && !showsPlayer(paneLayout.get());
const driverFor = () => (onMap() ? page : engine);
export const runner = createRunner({ engine, page, driverFor, demo, teams, selectTab: showPlayer, loadDemo, demoRefOf, segment });
// A run ends when what it plays in goes: the tab, or its Player pane,
// changed under it.
const keepDriver = () => {
    if (playlist.get().run && runner.driver() !== driverFor()) runner.stop();
};
activeTab.subscribe(keepDriver);
paneLayout.subscribe(keepDriver);

// A demo the user loads starts the playlist over: the clips of other
// demos go (its own stay — a reload, or a link to its clips). Not when a
// run loads its next clip's demo; and the first load after a share link
// was imported keeps the link's demos too (a list over several opens on
// the first of them).
/** @type {string[]} */
let linkDemos = [];
let lastDemo = demo.get();
demo.subscribe((handle) => {
    if (handle === lastDemo || !handle) return;
    lastDemo = handle;
    const keep = [handle.ref.key, ...linkDemos];
    linkDemos = [];
    if (!runner.loadsForRun()) keepDemos(keep);
});

export function currentRef() {
    return runner.currentRef();
}

// A share link's #playlist= on page load: append its clips (an empty
// playlist simply becomes it) and drop the fragment so a reload does not
// import twice.
async function importPlaylistFromHash() {
    const m = location.hash.match(/^#playlist=([A-Za-z0-9_-]+)$/);
    if (!m) return;
    history.replaceState(null, '', location.pathname + location.search);
    const decoded = await decodePlaylist(m[1]);
    if (!decoded || !decoded.length) return;
    // Opening your own link again must not double the list.
    /** @type {(a: import('../lib/playlist.js').Clip, b: import('../lib/playlist.js').Clip) => boolean} */
    const same = (a, b) => a.demo.key === b.demo.key && a.from === b.from && a.to === b.to && a.track === b.track
        && JSON.stringify(a.slow ?? null) === JSON.stringify(b.slow ?? null);
    const have = playlist.get().clips;
    const clips = decoded.filter((c) => !have.some((o) => same(o, c)));
    addClips(clips);
    linkDemos = decoded.map((c) => c.demo.key);
    toast(clips.length
        ? `Imported ${clips.length} clip${clips.length === 1 ? '' : 's'} into the playlist`
        : 'Those clips are already in your playlist');
}

// A query link's #clipquery= on page load: open it in the clip finder (not
// saved — the viewer can save it as a preset from there).
function importQueryFromHash() {
    const m = location.hash.match(/^#clipquery=([A-Za-z0-9_-]+)$/);
    if (!m) return;
    history.replaceState(null, '', location.pathname + location.search);
    const decoded = decodeQuery(m[1]);
    if (!decoded) {
        toast('That clip query link is not one this page can read');
        return;
    }
    openInFinder(decoded.title, decoded.query);
    toast(`Opened ${decoded.title ? `"${decoded.title}"` : 'a clip query'} in the clip finder`);
}

// Outside a playlist run, the engine playing into the selected segment's
// end pauses there, as the page's own playback stops (a run steps on its
// clips' ends itself).
function stopAtSegmentEnd() {
    /** @type {number | null} */
    let prev = null;
    engine.onTick(() => {
        const t = clock.t;
        if (playlist.get().run || engine.seeking()) {
            prev = null;
            return;
        }
        if (engine.fte?.isPlaying() && crossedEnd(segment.get(), prev, t)) engine.pause();
        prev = t;
    });
}

export function setupPlayer() {
    installHubLinkRouting({ runner, demo, addClips, toast });
    stopAtSegmentEnd();
    importPlaylistFromHash();
    importQueryFromHash();
}
