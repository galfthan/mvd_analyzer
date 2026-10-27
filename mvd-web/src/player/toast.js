// toast.js — the Player tab's transient notices ("Added to playlist: …").
import { store } from '../state/store.js';

export const toastText = store('');
let timer = null;

export function toast(text) {
    toastText.set(text);
    clearTimeout(timer);
    timer = setTimeout(() => toastText.set(''), 2600);
}
