// wall-clock.js — the match wall clock (streams.global, schema v72): the
// Summary "Date" cell and the topbar date (src/lib/topbar.js).
//
// The anchors are Unix epoch MILLISECONDS. The schema publishes no local-time
// field on purpose: the stamp a demo carries was written in whatever zone the
// server ran in, and on the pre-2005 half it carries no zone at all (the
// marker is read as UTC, `assumedUtc`, accuracy ±14 h). Rendering it in the
// VIEWER's zone would therefore invent a precision the anchor does not have
// and would move the date under a reader in Auckland, so this UI renders
// **UTC** and says so in the text. See RESULT_SCHEMA.md, GlobalStream.

import { escapeHtml } from './html.js';

// `accuracyMs` (the anchor's ±, absent where the caller has none) sets the
// PRECISION PRINTED, by magnitude rather than by grade name — the same rule
// formatAccuracyMs formats by. An hour-scale anchor does not name a minute:
// the ±14 h `assumedUtc` grade puts the true instant anywhere in a 28-hour
// window, so printing "11:44" claims a minute the stamp does not have. Those
// print the day plus the ± in the TEXT; everything second-scale or tighter
// keeps the minute.
/**
 * @param {number} unixMs
 * @param {number} [accuracyMs]  absent compares false: the minute is printed
 */
export function formatUtcStamp(unixMs, accuracyMs) {
    // toISOString is UTC by definition: "2002-08-04T11:44:55.000Z".
    const iso = new Date(unixMs).toISOString();
    const acc = /** @type {number} */ (accuracyMs);
    if (acc >= 3600000) return `${iso.slice(0, 10)} UTC ±${formatAccuracyMs(acc)}`;
    return `${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC`;
}

// The accuracy ladder is 1 ms / 1 s / 1 h / 14 h, but this formats the number
// rather than switching on those four values, so a new rung reads correctly
// without a change here.
/** @param {number} ms */
export function formatAccuracyMs(ms) {
    if (ms >= 3600000) return `${Math.round(ms / 3600000)} h`;
    if (ms >= 1000) return `${Math.round(ms / 1000)} s`;
    return `${ms} ms`;
}

// matchWallClock projects the graded match-start anchor onto {html, text} for
// a cell, or null when the demo carries none (then the caller shows whatever
// it showed before — never a placeholder).
//
// `exact` is served plainly. Every other grade is HEDGED IN THE TEXT with a
// leading "~", not only in a tooltip: a ±14 h tz-unknown stamp rendered as a
// plain date is the one way this field misleads. The anchor is never dropped
// or coerced — the schema grades it rather than withholding it, and so does
// this; the grade, the source, the ± and the note all ride the tooltip.
/**
 * @param {import('./result.js').Result | null | undefined} result
 * @returns {{ html: string, text: string } | null}
 */
export function matchWallClock(result) {
    const g = result?.streams?.global;
    if (!g || !g.matchStartUnixMs) return null;
    const text = formatUtcStamp(g.matchStartUnixMs, g.matchStartAccuracyMs);
    const bits = [`Match start, read off the demo's own wall-clock marker (${g.matchStartSource || 'unknown source'})`];
    if (g.matchStartAccuracyMs) bits.push(`accurate to ±${formatAccuracyMs(g.matchStartAccuracyMs)}`);
    if (g.matchStartConfidence) bits.push(`confidence: ${g.matchStartConfidence}`);
    if (g.matchStartNote) bits.push(g.matchStartNote);
    const title = escapeHtml(bits.join(' · '));
    if (g.matchStartConfidence === 'exact') {
        return { html: `<span title="${title}">${text}</span>`, text };
    }
    // `unverified` (something unpinned) and `contradicted` (a hard check
    // failed) are both hedged; the second is coloured as a warning because it
    // is the grade that says another signal disagrees with this stamp — and
    // it says "(disputed)" in the TEXT too, for the same reason the "~" is in
    // the text: the two grades must not differ by colour alone, which neither
    // a screenshot in a channel nor a colour-blind reader can tell apart.
    const disputed = g.matchStartConfidence === 'contradicted';
    const cls = disputed ? 'date-contradicted' : 'date-unverified';
    const shown = disputed ? `~${text} (disputed)` : `~${text}`;
    return { html: `<span class="${cls}" title="${title}">${shown}</span>`, text: shown };
}

// The Summary "Date" cell's markup. KTX's own demoinfo `date` wins where the
// block exists — it is the server's stamp, to the second, with its zone
// printed (text, escaped here). The graded anchor fills the half of the
// archive that has no block at all, which used to render "-".
/** @param {import('./result.js').Result | null | undefined} result */
export function matchDateHtml(result) {
    const ktxDate = result?.demoInfo?.date;
    if (ktxDate) return escapeHtml(ktxDate);
    const wc = matchWallClock(result);
    return wc ? wc.html : '-';
}

// matchDay: the match's day (YYYY-MM-DD) by the topbar's precedence —
// KTX's demoinfo date, else the wall-clock anchor, "~"-hedged as the
// topbar hedges it — or '' with neither.
/** @param {import('./result.js').Result | null | undefined} result */
export function matchDay(result) {
    const kt = String(result?.demoInfo?.date || '').match(/\d{4}-\d{2}-\d{2}/);
    if (kt) return kt[0];
    const w = matchWallClock(result);
    const day = w?.text.match(/\d{4}-\d{2}-\d{2}/);
    if (!w || !day) return '';
    return (w.text.startsWith('~') ? '~' : '') + day[0];
}
