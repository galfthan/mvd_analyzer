// playlist-store.js — the working playlist and the scripted run over it.
// Clips and their encodings are src/lib/playlist.js; this is their state:
// `clips` persists per browser (localStorage; the share link is the durable
// form), `run` is { clips, index, prev, transient } while a run plays —
// `clips` is the playlist itself, or a one-clip list for a plain click on a
// hub link or a moment.

import { store } from '../state/store.js';
import { cleanSlow } from '../lib/playlist.js';

/**
 * @typedef {import('../lib/playlist.js').Clip} Clip
 * @typedef {{ clips: Clip[], index: number, prev: number | null, transient: boolean }} Run
 * @typedef {{ clips: Clip[], run: Run | null }} PlaylistState
 */

const KEY = 'mvd.playlist.v1';

/** @returns {Clip[]} */
function load() {
    try {
        const saved = JSON.parse(localStorage.getItem(KEY) || 'null');
        if (!Array.isArray(saved)) return [];
        return saved.filter((/** @type {any} */ c) => c && c.to > c.from && c.demo?.key).map((/** @type {any} */ c) => {
            const { slow, ...clip } = c;
            const sl = slow && cleanSlow(clip, slow.from, slow.to, slow.speed);
            return sl ? { ...clip, slow: sl } : clip;
        });
    } catch { /* storage blocked or corrupt: start empty */ }
    return [];
}

export const playlist = store(/** @type {PlaylistState} */ ({ clips: load(), run: null }));

/** @param {Clip[]} clips */
function save(clips) {
    try {
        localStorage.setItem(KEY, JSON.stringify(clips));
    } catch { /* per-viewer convenience */ }
}

// update(fn) applies fn to a copy-on-write state and persists the clips.
/** @param {(s: PlaylistState) => void} fn */
function update(fn) {
    const s = playlist.get();
    const next = { clips: s.clips, run: s.run };
    fn(next);
    if (next.clips !== s.clips) save(next.clips);
    playlist.set(next);
}

// A "Play all" run plays the list itself, so it reaches clips added while
// it plays.
/** @param {Clip[]} clips */
export function addClips(clips) {
    if (!clips.length) return;
    update((s) => {
        const next = [...s.clips, ...clips];
        if (s.run && s.run.clips === s.clips) s.run = { ...s.run, clips: next };
        s.clips = next;
    });
}

// setLabel edits in place: the list is not re-rendered for a keystroke.
/**
 * @param {Clip} clip
 * @param {string} label
 */
export function setLabel(clip, label) {
    clip.label = label;
    save(playlist.get().clips);
}

// Keep a running playlist pointing at the same clip across edits.
/**
 * @param {number} from
 * @param {number} to
 */
export function moveClip(from, to) {
    update((s) => {
        if (to < 0 || to >= s.clips.length || from === to) return;
        const playing = s.run && s.run.clips === s.clips ? s.clips[s.run.index] : null;
        const clips = [...s.clips];
        const [c] = clips.splice(from, 1);
        clips.splice(to, 0, c);
        s.clips = clips;
        // A clip playing means a run.
        if (playing) s.run = { .../** @type {Run} */ (s.run), clips, index: clips.indexOf(playing) };
    });
}

/** @param {number} i */
export function removeClip(i) {
    update((s) => {
        const playing = s.run && s.run.clips === s.clips ? s.clips[s.run.index] : null;
        const clips = s.clips.filter((_, k) => k !== i);
        s.clips = clips;
        if (playing) s.run = clips.includes(playing) ? { .../** @type {Run} */ (s.run), clips, index: clips.indexOf(playing) } : null;
    });
}

export function clearClips() {
    update((s) => { s.clips = []; s.run = null; });
}

// setClips replaces the list (a run over it ends).
/** @param {Clip[]} clips */
export function setClips(clips) {
    update((s) => { s.clips = [...clips]; s.run = null; });
}

// keepDemos drops the clips of every demo but `keys` (a run over the list
// ends with them).
/** @param {string[]} keys */
export function keepDemos(keys) {
    const s = playlist.get();
    const kept = s.clips.filter((c) => keys.includes(c.demo?.key));
    if (kept.length === s.clips.length) return;
    update((n) => {
        n.clips = kept;
        if (n.run && !n.run.transient) n.run = null;
    });
}

/** @param {Run | null} run */
export function setRun(run) {
    update((s) => { s.run = run; });
}
