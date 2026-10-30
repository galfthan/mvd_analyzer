// fullscreen.js — a tab's fullscreen: the tab's panel fills the screen and
// the unified playback bar moves into it on top, so seeking, the speed
// and the way out stay at hand, and back out however fullscreen ends (the
// bar's button, `f`, Escape, the browser). A tab offers it by naming its panel here and reacts to
// `fullscreenTab` (the map resizes its canvas, the player caps its height
// through the panel's class).

import { store } from '../state/store.js';
import { activeTab } from '../state/ui.js';

/** @type {Record<string, string>} tab id → the panel that goes fullscreen */
export const FULLSCREEN_PANELS = {
    player: '#tab-player .player-panel',
    map: '#tab-player .player-panel',
};

// The tab whose panel is fullscreen, or null.
export const fullscreenTab = store(/** @type {string | null} */ (null));

/** @type {{ parent: ParentNode, next: ChildNode | null } | null} */
let barHome = null;

function onChange() {
    const el = document.fullscreenElement;
    // Two tabs can share a panel (Map and Player): the active one is it.
    const active = activeTab.get();
    const tab = !el ? null : Object.hasOwn(FULLSCREEN_PANELS, active) && el.matches(FULLSCREEN_PANELS[active])
        ? active : Object.keys(FULLSCREEN_PANELS).find((id) => el.matches(FULLSCREEN_PANELS[id])) || null;
    if (tab === fullscreenTab.get()) return;
    const bar = document.getElementById('unified-timeline');
    if (bar) {
        if (barHome) {
            barHome.parent.insertBefore(bar, barHome.next);
            barHome = null;
        }
        if (tab && el) {
            barHome = { parent: /** @type {ParentNode} */ (bar.parentNode), next: bar.nextSibling };
            el.insertBefore(bar, el.firstChild);
        }
    }
    fullscreenTab.set(tab);
}

// toggleFullscreen leaves fullscreen, or puts the tab's panel into it.
export function toggleFullscreen(tab = activeTab.get()) {
    if (document.fullscreenElement) {
        document.exitFullscreen().catch(() => {});
        return;
    }
    const panel = Object.hasOwn(FULLSCREEN_PANELS, tab) ? document.querySelector(FULLSCREEN_PANELS[tab]) : null;
    panel?.requestFullscreen?.()?.catch(() => {});
}

/** @param {EventTarget | null} t */
const typing = (t) => t instanceof HTMLElement && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));

export function installFullscreen() {
    document.addEventListener('fullscreenchange', onChange);
    // `f` toggles it on a tab that has it, unless a field has the keys.
    document.addEventListener('keydown', (e) => {
        if (e.key !== 'f' || e.ctrlKey || e.metaKey || e.altKey || e.repeat || typing(e.target)) return;
        if (!document.fullscreenElement && !Object.hasOwn(FULLSCREEN_PANELS, activeTab.get())) return;
        e.preventDefault();
        toggleFullscreen();
    });
}
