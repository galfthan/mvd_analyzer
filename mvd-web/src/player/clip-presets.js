// clip-presets.js — the clip finder's state: the viewer's own saved presets
// (localStorage, validated like a share link on the way in) and the query
// the finder is editing, which a preset pick or a #clipquery= link sets.

import { store } from '../state/store.js';
import { validateQuery } from '../lib/clip-query.js';

/**
 * @typedef {import('../lib/clip-query.js').ClipQuery} ClipQuery
 * @typedef {{ id: string, title: string, query: ClipQuery }} UserPreset
 */

const KEY = 'mvd.clipPresets.v1';

/** @returns {UserPreset[]} */
function load() {
    try {
        const saved = JSON.parse(localStorage.getItem(KEY) || 'null');
        if (!Array.isArray(saved)) return [];
        return saved.flatMap((/** @type {any} */ p) => {
            const query = validateQuery(p?.query);
            return query && typeof p.id === 'string' && typeof p.title === 'string' ? [{ id: p.id, title: p.title, query }] : [];
        });
    } catch {
        return [];
    }
}

export const userPresets = store(load());

/** @param {UserPreset[]} list */
function save(list) {
    try {
        localStorage.setItem(KEY, JSON.stringify(list));
    } catch { /* per-viewer convenience */ }
    userPresets.set(list);
}

// savePreset stores a query under `title`, replacing a preset of the same
// title; returns its id.
/**
 * @param {string} title
 * @param {ClipQuery} query
 */
export function savePreset(title, query) {
    const list = userPresets.get();
    const same = list.find((p) => p.title === title);
    const id = same?.id || `my-${Date.now().toString(36)}`;
    save([...list.filter((p) => p !== same), { id, title, query: structuredClone(query) }]);
    return id;
}

/** @param {string} id */
export function deletePreset(id) {
    save(userPresets.get().filter((p) => p.id !== id));
}

// The query handed to the finder from outside it (a preset pick, a link):
// { title, query, n } — n changes on every hand-over so the same preset
// picked twice still reloads.
export const finderInput = store(/** @type {{ title: string, query: ClipQuery, n: number } | null} */ (null));

/**
 * @param {string} title
 * @param {ClipQuery} query
 */
export function openInFinder(title, query) {
    finderInput.set({ title, query: structuredClone(query), n: (finderInput.get()?.n || 0) + 1 });
}
