// player-slot.js — where the engine shows. Its iframe must never move in
// the page (a moved or removed iframe reloads, restarting the demo), so it
// lives in one fixed layer of the Player tab (PlayerLayer.svelte) and the
// Player pane (PlayerPane.svelte) holds only a placeholder, registered here;
// the layer sits over the placeholder wherever the pane is, and hides when
// there is none (the pane closed or collapsed, its column hidden).

import { store } from '../state/store.js';

export const playerSlot = store(/** @type {HTMLElement | null} */ (null));

/**
 * @typedef {{ left: number, top: number, width: number, height: number }} Box
 * @typedef {{ top: number, right: number, bottom: number, left: number }} Inset
 */

// slotBox: the slot's box and how much of it its clipping ancestors (the
// scrolling columns, a sized pane body, the page's scroller) cut off on
// each side, so the layer shows only what the slot would. `clips` are the
// ancestors' boxes, innermost first.
/**
 * @param {DOMRect | Box} rect
 * @param {Array<DOMRect | Box>} clips
 * @returns {{ box: Box, inset: Inset, visible: boolean }}
 */
export function slotBox(rect, clips) {
    let top = rect.top;
    let left = rect.left;
    let bottom = rect.top + rect.height;
    let right = rect.left + rect.width;
    for (const c of clips) {
        top = Math.max(top, c.top);
        left = Math.max(left, c.left);
        bottom = Math.min(bottom, c.top + c.height);
        right = Math.min(right, c.left + c.width);
    }
    const box = { left: rect.left, top: rect.top, width: rect.width, height: rect.height };
    const inset = {
        top: Math.max(0, top - rect.top),
        left: Math.max(0, left - rect.left),
        bottom: Math.max(0, rect.top + rect.height - bottom),
        right: Math.max(0, rect.left + rect.width - right),
    };
    return { box, inset, visible: bottom > top && right > left && rect.width > 0 && rect.height > 0 };
}

// clipAncestors: the slot's ancestors that clip it (overflow other than
// visible), innermost first, up to the document — or up to the fullscreen
// element, which is drawn over everything outside it.
/** @param {HTMLElement} el */
export function clipAncestors(el) {
    /** @type {HTMLElement[]} */
    const out = [];
    const top = document.fullscreenElement;
    for (let p = el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') out.push(p);
        if (p === top) break;
    }
    return out;
}
