// toast.js — the Player tab's transient notices ("Added to playlist: …").
import { store } from '../state/store.js';

export const toastText = store('');
/** @type {ReturnType<typeof setTimeout> | undefined} */
let timer;

/** @param {string} text */
export function toast(text) {
    toastText.set(text);
    clearTimeout(timer);
    timer = setTimeout(() => toastText.set(''), 2600);
}
