// chat.js — the Chat tab's DOM-free parts: the message list in seconds, the
// split into the Kills / side A / side B columns, the time axis and the
// px-per-second layout, and the Quake text markup.

import { formatDuration } from './result.js';
import { escapeHtml } from './html.js';

// The chat-track density. It has to stay in sync with the 700px chat
// viewport in styles.css (.chat-scroll-viewport) — change one, change the
// other.
export const CHAT_PX_PER_SEC = 17.5; // ~same density as the original 40s/700px window
// The floor of a row's height when stacking rows (see stackRow).
export const CHAT_ITEM_HEIGHT = 18;
const TICK_INTERVAL = 5;

/**
 * @typedef {import('./result.js').Result} Result
 * A Result message (messages.messages[]) with `time` in seconds; the rest
 * of its fields as the Go side ships them.
 * @typedef {{ time: number, type: string, team?: string, [field: string]: any }} ChatEvent
 */

// The match length in seconds; 600 when the result carries none.
/** @param {Result | null | undefined} result */
export function chatDuration(result) {
    return (result?.match?.duration || 600000) * 0.001;
}

// The result's messages with `time` in seconds (Schema v57: messages[].time
// is int32 ms on the raw result), or null when the result has none.
/**
 * @param {Result | null | undefined} result
 * @returns {ChatEvent[] | null}
 */
export function chatEvents(result) {
    const messages = result?.messages?.messages;
    if (!messages) return null;
    return messages.map(/** @param {ChatEvent} e */ e => ({ ...e, time: e.time * 0.001 }));
}

// bucketChat splits the messages into the three columns. `teams` is the
// canonical team order (src/state/teams.js); `single` is the individual-field
// layout, where column A takes every say line and B stays empty.
//
// No client-side text dedup: the only real duplication source — KTX
// sprints each say/say_team line once per recipient, all sharing one
// wire-ms — is already collapsed upstream by messages.go's seenChat on an
// exact (time,type,player,message) key (the documented CLAUDE.md filter
// exception; see mvd-reader/MVD_FORMAT.md §chat). Obituaries are broadcast
// once and pass through verbatim. A previous 3s message-text filter here
// also swallowed authentic repeats (a player re-sending the same bind, or
// two genuinely identical deaths within 3s), violating the
// surface-authoritative-data rule, so it was removed. Only clip to the
// match window.
/**
 * @param {ChatEvent[]} events
 * @param {string[]} teams
 * @param {number} duration
 * @param {{ single?: boolean, hideTeam?: boolean }} [opts]
 */
export function bucketChat(events, teams, duration, { single = false, hideTeam = false } = {}) {
    /** @type {ChatEvent[]} */
    const kills = [];
    /** @type {ChatEvent[]} */
    const a = [];
    /** @type {ChatEvent[]} */
    const b = [];
    for (const event of events) {
        if (event.time < 0 || event.time > duration) continue;
        if (event.type === 'frag') {
            kills.push(event);
        } else if (event.type === 'teamsay' || event.type === 'chat') {
            // "Hide team chat" is a display filter over the say columns only;
            // frags and public say keep their rows. It filters on message TYPE,
            // not on side, so it keeps its meaning in an individual field: an
            // FFA's say_team still goes only to same-tag players.
            if (hideTeam && event.type === 'teamsay') continue;
            if (single) a.push(event);
            else if (event.team === teams[0]) a.push(event);
            else if (event.team === teams[1]) b.push(event);
        }
    }
    return { kills, a, b };
}

// The column headings. In the individual field the one say column is plain
// "Chat": teams[0]/[1] are two players out of the field there, not sides.
/**
 * @param {string[]} teams
 * @param {boolean} single
 */
export function chatTitles(teams, single) {
    if (single) return ['Chat', 'Team B Chat'];
    if (teams.length >= 2) return [`${teams[0]} Chat`, `${teams[1]} Chat`];
    return ['Team A Chat', 'Team B Chat'];
}

/** @param {number} duration */
export function contentHeight(duration) {
    return Math.round(duration * CHAT_PX_PER_SEC);
}

// The time axis: a tick every TICK_INTERVAL seconds, 0 through duration.
/** @param {number} duration */
export function axisTicks(duration) {
    /** @type {Array<{ top: number, label: string }>} */
    const ticks = [];
    for (let t = 0; t <= duration; t += TICK_INTERVAL) {
        ticks.push({ top: Math.round(t * CHAT_PX_PER_SEC), label: formatDuration(t) });
    }
    return ticks;
}

/** @param {number} t */
export function timeLineTop(t) {
    return t * CHAT_PX_PER_SEC;
}

// The viewport scroll that centres time t.
/**
 * @param {number} t
 * @param {number} viewportHeight
 */
export function scrollTarget(t, viewportHeight) {
    return Math.max(0, timeLineTop(t) - viewportHeight / 2);
}

// stackRow places a row at its time, or — when that would overlap the row
// above, whose bottom is lastBottom — just below it, marked displaced.
/**
 * @param {number} time
 * @param {number} lastBottom
 */
export function stackRow(time, lastBottom) {
    const top = Math.round(time * CHAT_PX_PER_SEC);
    if (top < lastBottom) return { top: lastBottom, displaced: true };
    return { top, displaced: false };
}

// The next row's lastBottom: a row is at least CHAT_ITEM_HEIGHT tall, taller
// when its message wraps (measured; 0 when the tab is not laid out).
/**
 * @param {number} top
 * @param {number} [measuredHeight]
 */
export function rowBottom(top, measuredHeight) {
    return top + Math.max(CHAT_ITEM_HEIGHT, measuredHeight || 0);
}

export { escapeHtml };

// Format Quake chat messages with colors (HTML).
// Based on ezQuake source code character rendering:
// - Characters 0-127: Normal white text
// - Characters 128-255: "Gold/Brown" alternate text (same glyphs as 0-127)
// - &cRGB: Set color to RGB (hex digits 0-F, each multiplied by 16)
// - &r: Reset color to white
/** @param {string | null | undefined} text */
export function formatQuakeMessage(text) {
    if (!text) return '';

    // Remove sound triggers at end of messages (!K, !H, !G, !C, etc.)
    let result = text.replace(/![A-Z]$/g, '');

    let output = '';
    let currentColor = null;
    let i = 0;

    while (i < result.length) {
        const charCode = result.charCodeAt(i);

        // Check for &cRGB color code
        if (result.slice(i, i + 2) === '&c') {
            // Check for &cfff (white/reset)
            if (result.slice(i, i + 5).toLowerCase() === '&cfff') {
                if (currentColor) output += '</span>';
                currentColor = null;
                i += 5;
                continue;
            }
            // Check for 3-digit hex color
            const colorMatch = result.slice(i + 2, i + 5).match(/^[0-9a-fA-F]{3}/);
            if (colorMatch) {
                if (currentColor) output += '</span>';
                // ezQuake uses r * 16 for each hex digit (0-240 range)
                const r = parseInt(colorMatch[0][0], 16) * 16;
                const g = parseInt(colorMatch[0][1], 16) * 16;
                const b = parseInt(colorMatch[0][2], 16) * 16;
                currentColor = `rgb(${r},${g},${b})`;
                output += `<span style="color:${currentColor}">`;
                i += 5;
                continue;
            }
        }

        // Check for &r reset
        if (result.slice(i, i + 2) === '&r') {
            if (currentColor) output += '</span>';
            currentColor = null;
            i += 2;
            continue;
        }

        // High-bit gold characters (128-255)
        if (charCode >= 128 && charCode <= 255) {
            const baseChar = String.fromCharCode(charCode - 128);
            if (currentColor === null) {
                output += `<span class="quake-gold">${escapeHtml(baseChar)}</span>`;
            } else {
                output += escapeHtml(baseChar);
            }
            i++;
            continue;
        }

        // Skip macro delimiters (curly braces and square brackets)
        // These are Quake client markup, not displayed text
        if (result[i] === '{' || result[i] === '}' ||
            result[i] === '[' || result[i] === ']') {
            i++;
            continue;
        }

        // Regular character
        output += escapeHtml(result[i]);
        i++;
    }

    if (currentColor) output += '</span>';
    return output;
}
