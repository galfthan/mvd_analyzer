// hub-links.js — every hub link on the other tabs (hubReplayUrl:
// a.viewer-link, a.hub-watch-link) for the loaded demo: a plain click plays
// the clip in the Player tab, shift-click adds it to the playlist, and
// ctrl/cmd-click and middle-click keep the browser's own "open hub in a new
// tab". Capture phase, so a row's own click handler (seek the cursor) does
// not also fire.

import { parseHubReplayUrl } from '../lib/playlist.js';
import { formatDuration, nameOfUserId } from '../lib/result.js';

const SELECTOR = 'a.viewer-link, a.hub-watch-link';
const TITLE = 'Click: play in the Player tab · Shift-click: add to playlist · Ctrl/middle-click: open on hub';
const DEFAULT_S = 15;

/**
 * @param {object} deps
 * @param {ReturnType<typeof import('./runner.js').createRunner>} deps.runner
 * @param {import('../state/store.js').Store<import('../data/wasm-source.js').DemoHandle | null>} deps.demo
 * @param {(clips: import('../lib/playlist.js').Clip[]) => void} deps.addClips
 * @param {(text: string) => void} deps.toast
 */
export function installHubLinkRouting({ runner, demo, addClips, toast }) {
    /**
     * @param {HTMLAnchorElement} a
     * @returns {import('../lib/playlist.js').Clip | null}
     */
    const clipFromLink = (a) => {
        const parsed = parseHubReplayUrl(a.href);
        const ref = runner.currentRef();
        if (!parsed || !ref || parsed.gameId !== ref.gameId) return null;
        const who = parsed.track === 'auto' ? '' : (nameOfUserId(demo.get()?.result, parsed.track) || '');
        const section = a.closest('.panel, section')?.querySelector('h2, h3')?.textContent.trim() || 'Clip';
        const to = parsed.to != null && parsed.to > parsed.from ? parsed.to : parsed.from + DEFAULT_S;
        const label = `${section} · ${who || 'Auto'} ${formatDuration(Math.max(0, parsed.from - ref.offset))}`;
        return { from: parsed.from, to, track: parsed.track, who, label, demo: ref };
    };

    document.addEventListener('click', (e) => {
        const a = /** @type {HTMLAnchorElement | null | undefined} */ (/** @type {Element} */ (e.target).closest?.(SELECTOR));
        if (!a || e.button !== 0 || e.ctrlKey || e.metaKey || e.altKey) return;
        const c = clipFromLink(a);
        if (!c) return;
        e.preventDefault();
        e.stopPropagation();
        if (e.shiftKey) {
            addClips([c]);
            toast(`Added to playlist: ${c.label}`);
        } else {
            runner.start([c], 0, true);
        }
    }, true);
    document.addEventListener('mouseover', (e) => {
        const a = /** @type {HTMLAnchorElement | null | undefined} */ (/** @type {Element} */ (e.target).closest?.(SELECTOR));
        if (a && a.title !== TITLE) a.title = TITLE;
    }, true);
}
