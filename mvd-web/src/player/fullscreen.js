// fullscreen.js — the Player tab's fullscreen: the whole player panel (the
// engine, its bars, the clip finder, the playlist and the pane column) fills
// the screen, and the unified playback bar moves into it so seeking and the
// speed buttons stay at hand — as the Map tab does with its panel — and back
// out on exit, however fullscreen ends (the button, Escape, the browser).

import { store } from '../state/store.js';

export const fullscreen = store(false);

const PANEL = '#tab-player .player-panel';
/** @type {{ parent: ParentNode, next: ChildNode | null } | null} */
let barHome = null;

function onChange() {
    const panel = document.querySelector(PANEL);
    const on = !!panel && document.fullscreenElement === panel;
    if (on === fullscreen.get()) return;
    const bar = document.getElementById('unified-timeline');
    if (bar && panel) {
        if (on) {
            barHome = { parent: /** @type {ParentNode} */ (bar.parentNode), next: bar.nextSibling };
            panel.insertBefore(bar, panel.firstChild);
        } else if (barHome) {
            barHome.parent.insertBefore(bar, barHome.next);
            barHome = null;
        }
    }
    panel?.classList.toggle('player-panel--fullscreen', on);
    fullscreen.set(on);
}

export function installFullscreen() {
    document.addEventListener('fullscreenchange', onChange);
}

export function toggleFullscreen() {
    const panel = document.querySelector(PANEL);
    if (!panel) return;
    if (document.fullscreenElement === panel) document.exitFullscreen().catch(() => {});
    else panel.requestFullscreen?.()?.catch(() => {});
}
