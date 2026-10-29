// hub-url.js — the hub.quakeworld.nu replay link every tab builds, and
// its anchor (hubLinkHtml). src/lib/playlist.js parseHubReplayUrl reads it
// back, and src/player/hub-links.js routes a click on one (a.hub-link) to
// the Player tab.

// Build a hub.quakeworld.nu replay URL. `to` is optional — omit it for a
// single "jump here" link (hubWatchLinkHtml) and pass it for a windowed
// clip (powerups / streaks / airgibs / pack drops). Callers do their own
// demoOffset conversion + window padding (each surface pads differently),
// then hand the final demo-relative from/to/track here so the URL scheme
// lives in exactly one place.
//
// from/to ARE DEMO-RELATIVE SECONDS — hub feeds `from` straight to
// fte.demoJump() and pauses when the demo's elapsed time passes `to`
// (hub.quakeworld.nu src/pages/games/player/clips/hooks.ts), with no
// countdown arithmetic on either. So streams.global.demoOffset is the
// correct conversion and the only one that puts the clip on the content.
//
// What is NOT correct is hub's displayed clock: fteController.ts hard-codes
// _countdownDuration = 10.0 and shows demoElapsed-10, while the demo's own
// hub metadata carries the real countdown (countdown_duration = 15.119 s on
// qwleague servers, equal to our demoOffset to the millisecond) and the
// player never reads it. On those demos hub's clock therefore reads
// demoOffset-10 s HIGHER than the match time in our tables. That is an
// upstream bug. Do not "fix" it by building links off hub's 10 s constant:
// it drags every clip demoOffset-10 s earlier, and because `to` is a hard
// pause point it truncates the payload — an airgib clip then ends before
// the gib. Correct content, wrong-looking clock, is the trade we keep.
/**
 * @param {{ gameId: number | string, from: number | string, to?: number | string | null, track: number | string }} link
 */
export function hubReplayUrl({ gameId, from, to, track }) {
    let url = `https://hub.quakeworld.nu/games/?gameId=${gameId}&from=${from}`;
    if (to != null) url += `&to=${to}`;
    url += `&track=${track}`;
    return url;
}

export const HUB_LINK_TITLE = 'Play in the Player tab · Shift: add to the playlist · Ctrl / Cmd: open on hub';

// hubLinkHtml: the anchor every tab shows for a replay URL. It reads
// "play"; holding Shift or Ctrl relabels it "+add" / "hub" (the classes
// src/player/hub-links.js puts on <html>, styles.css swaps the label).
// `compact` is the small inline form of the roster tables.
/**
 * @param {string} url
 * @param {boolean} [compact]
 */
export function hubLinkHtml(url, compact = false) {
    const cls = compact ? 'hub-link hub-link--compact' : 'hub-link';
    return `<a href="${url}" target="_blank" class="${cls}" title="${HUB_LINK_TITLE}"><span class="hub-link-label">play</span></a>`;
}
