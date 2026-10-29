// selection-clip.js — the selected segment as a clip: the playback bar's
// "+ clip" and the keys i / o / c. A clip needs a POV, so it is offered
// where one is: the Watch tab — the engine's (Auto counts) when it is up,
// else the player the map follows (follow.js). `selectionPov` is that POV,
// null elsewhere.

import { store } from '../state/store.js';
import { activeTab, demo, segment, clock } from '../state/index.js';
import { mapFollow } from './follow.js';
import { formatDuration, nameOfUserId } from '../lib/result.js';
import { markIn, markOut } from '../lib/playback-bar.js';
import { engine, currentRef } from './index.js';
import { addClips } from './playlist-store.js';
import { toast } from './toast.js';

/** @typedef {{ track: import('../lib/playlist.js').Track, who: string }} Pov */

export const selectionPov = store(/** @type {Pov | null} */ (null));

/** @returns {Pov | null} */
function povNow() {
    const tab = activeTab.get();
    if (tab !== 'player') return null;
    if (engine.drives()) {
        const track = engine.povTrack();
        return { track, who: track === 'auto' ? '' : nameOfUserId(demo.get()?.result, track) || '' };
    }
    const name = mapFollow.get();
    const id = name ? demo.get()?.result?.timelineAnalysis?.playerUserIDs?.[name] : null;
    return id && name ? { track: id, who: name } : null;
}

function refresh() {
    const next = povNow();
    const cur = selectionPov.get();
    if (next?.track !== cur?.track || next?.who !== cur?.who) selectionPov.set(next);
}

// The selection (else the last 10 s up to the playhead) from the current
// POV, appended to the playlist. Whole demo seconds, as every clip.
export function addSelectionClip() {
    const ref = currentRef();
    const pov = povNow();
    if (!ref || !pov) return;
    const seg = segment.get() || { start: Math.max(0, clock.t - 10), end: clock.t };
    const from = Math.max(0, Math.floor(seg.start + ref.offset));
    const to = Math.ceil(seg.end + ref.offset);
    if (to <= from) return;
    const label = `${pov.who || 'Auto'} ${formatDuration(Math.max(0, from - ref.offset))}`;
    addClips([{ from, to, track: pov.track, who: pov.who, label, demo: ref }]);
    toast(`Added: ${label}`);
}

/** @param {KeyboardEvent} e */
function onKey(e) {
    if (!selectionPov.get() || e.repeat || e.ctrlKey || e.metaKey || e.altKey) return;
    if (/** @type {Element} */ (e.target).closest?.('input, textarea, select, [contenteditable]')) return;
    if (e.key === 'i') segment.set(markIn(segment.get(), clock.t, clock.duration));
    else if (e.key === 'o') segment.set(markOut(segment.get(), clock.t));
    else if (e.key === 'c') addSelectionClip();
    else return;
    e.preventDefault();
}

export function setupSelectionClip() {
    activeTab.subscribe(refresh);
    demo.subscribe(refresh);
    engine.status.subscribe(refresh);
    mapFollow.subscribe(refresh);
    window.addEventListener('keydown', onKey);
}
