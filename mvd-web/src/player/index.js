// The Player tab's controller: one engine, one playlist runner, the hub link
// routing and the share-link import. The unified bar's speed buttons
// (src/ui/PlaybackBar.svelte) drive the engine while it is the clock;
// components import from here.

import { clock, demo, teams, activeTab, selectTab } from '../state/index.js';
import { loadDemo } from '../app/load.js';
import { decodePlaylist } from '../lib/playlist.js';
import { PlayerEngine } from './engine.js';
import { createRunner } from './runner.js';
import { demoRefOf } from './demo-ref.js';
import { playlist, addClips } from './playlist-store.js';
import { installHubLinkRouting } from './hub-links.js';
import { toast } from './toast.js';

export const engine = new PlayerEngine({ clock, demo, activeTab });
export const runner = createRunner({ engine, demo, teams, selectTab, loadDemo, demoRefOf });

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
    const same = (a, b) => a.demo.key === b.demo.key && a.from === b.from && a.to === b.to && a.track === b.track;
    const have = playlist.get().clips;
    const clips = decoded.filter((c) => !have.some((o) => same(o, c)));
    addClips(clips);
    toast(clips.length
        ? `Imported ${clips.length} clip${clips.length === 1 ? '' : 's'} into the playlist`
        : 'Those clips are already in your playlist');
}

export function setupPlayer() {
    installHubLinkRouting({ runner, demo, addClips, toast });
    importPlaylistFromHash();
}
