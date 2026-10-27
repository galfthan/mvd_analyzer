// playlist-store.js — the working playlist and the scripted run over it.
// Clips and their encodings are src/lib/playlist.js; this is their state:
// `clips` persists per browser (localStorage; the share link is the durable
// form), `run` is { clips, index, prev, transient } while a run plays —
// `clips` is the playlist itself, or a one-clip list for a plain click on a
// hub link or a moment.

import { store } from '../state/store.js';

const KEY = 'mvd.playlist.v1';

function load() {
    try {
        const saved = JSON.parse(localStorage.getItem(KEY) || 'null');
        if (Array.isArray(saved)) return saved.filter((c) => c && c.to > c.from && c.demo?.key);
    } catch { /* storage blocked or corrupt: start empty */ }
    return [];
}

export const playlist = store({ clips: load(), run: null });

function save(clips) {
    try {
        localStorage.setItem(KEY, JSON.stringify(clips));
    } catch { /* per-viewer convenience */ }
}

// update(fn) applies fn to a copy-on-write state and persists the clips.
function update(fn) {
    const s = playlist.get();
    const next = { clips: s.clips, run: s.run };
    fn(next);
    if (next.clips !== s.clips) save(next.clips);
    playlist.set(next);
}

export function addClips(clips) {
    if (!clips.length) return;
    update((s) => { s.clips = [...s.clips, ...clips]; });
}

// setLabel edits in place: the list is not re-rendered for a keystroke.
export function setLabel(clip, label) {
    clip.label = label;
    save(playlist.get().clips);
}

// Keep a running playlist pointing at the same clip across edits.
export function moveClip(from, to) {
    update((s) => {
        if (to < 0 || to >= s.clips.length || from === to) return;
        const playing = s.run && s.run.clips === s.clips ? s.clips[s.run.index] : null;
        const clips = [...s.clips];
        const [c] = clips.splice(from, 1);
        clips.splice(to, 0, c);
        s.clips = clips;
        if (playing) s.run = { ...s.run, clips, index: clips.indexOf(playing) };
    });
}

export function removeClip(i) {
    update((s) => {
        const playing = s.run && s.run.clips === s.clips ? s.clips[s.run.index] : null;
        const clips = s.clips.filter((_, k) => k !== i);
        s.clips = clips;
        if (playing) s.run = clips.includes(playing) ? { ...s.run, clips, index: clips.indexOf(playing) } : null;
    });
}

export function clearClips() {
    update((s) => { s.clips = []; s.run = null; });
}

export function setRun(run) {
    update((s) => { s.run = run; });
}
