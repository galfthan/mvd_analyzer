// demo-ref.js — the loaded demo in the playlist's terms (src/lib/playlist.js:
// a clip's `demo`): { key, gameId, title, offset }.
import { demoOffsetSec, mapFileKey } from '../lib/result.js';

/**
 * @param {import('../data/wasm-source.js').DemoHandle | null} handle
 * @param {string[] | null | undefined} teamOrder
 * @returns {import('../lib/playlist.js').ClipDemo | null}
 */
export function demoRefOf(handle, teamOrder) {
    if (!handle?.result) return null;
    const teams = (teamOrder || []).slice(0, 3).join(' vs ');
    return {
        key: handle.ref.key,
        gameId: handle.ref.gameId,
        title: [mapFileKey(handle.result), teams].filter(Boolean).join(' · '),
        offset: demoOffsetSec(handle.result),
    };
}
