// The Player tab's controller: one engine, one playlist runner, the hub link
// routing and the share-link imports (playlists and clip queries). The unified bar's play / speed
// (src/ui/PlaybackBar.svelte) drive the engine while it is the clock;
// components import from here.

import { clock, demo, teams, activeTab, selectTab, segment } from '../state/index.js';
import { loadDemo } from '../app/load.js';
import { decodePlaylist } from '../lib/playlist.js';
import { decodeQuery } from '../lib/clip-query.js';
import { crossedEnd } from '../lib/playback-bar.js';
import { openInFinder } from './clip-presets.js';
import { PlayerEngine } from './engine.js';
import { createRunner } from './runner.js';
import { demoRefOf } from './demo-ref.js';
import { playlist, addClips } from './playlist-store.js';
import { installHubLinkRouting } from './hub-links.js';
import { toast } from './toast.js';

export const engine = new PlayerEngine({ clock, demo, activeTab });
export const runner = createRunner({ engine, demo, teams, selectTab, loadDemo, demoRefOf, segment });

export function currentRef() {
    return runner.currentRef();
}

// A share link's #playlist= on page load: append its clips (an empty
// playlist simply becomes it) and drop the fragment so a reload does not
// import twice.
function importPlaylistFromHash() {
    const m = location.hash.match(/^#playlist=([A-Za-z0-9_-]+)$/);
    if (!m) return;
    history.replaceState(null, '', location.pathname + location.search);
    const decoded = decodePlaylist(m[1]);
    if (!decoded || !decoded.length) return;
    // Opening your own link again must not double the list.
    /** @type {(a: import('../lib/playlist.js').Clip, b: import('../lib/playlist.js').Clip) => boolean} */
    const same = (a, b) => a.demo.key === b.demo.key && a.from === b.from && a.to === b.to && a.track === b.track
        && JSON.stringify(a.slow ?? null) === JSON.stringify(b.slow ?? null);
    const have = playlist.get().clips;
    const clips = decoded.filter((c) => !have.some((o) => same(o, c)));
    addClips(clips);
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
