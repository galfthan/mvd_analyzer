// playlist.js — the Player tab's clip model, kept free of DOM and page state
// so it runs under `node --test`.
//
// A clip is { from, to, track, who, label, demo }:
//   from / to  demo-relative whole seconds — the clock FTE's demo_jump and
//              hub's from/to/clip params use (match time + demoOffset)
//   track      a userid (number) or 'auto' (FTE's stats autotrack)
//   who        display name of the tracked player ('' for auto)
//   label      free text
//   demo       { key, gameId, title, offset } — which demo the clip belongs
//              to; key is 'hub:<gameId>' or 'file:<name>', offset its
//              demoOffset in seconds (for showing match time)
//
// Times stay demo-relative in storage because both consumers (the engine
// and hub links) speak it; the page converts to match time for display.

/**
 * @typedef {import('./result.js').Result} Result
 * @typedef {number | 'auto'} Track
 * @typedef {{ key: string, gameId: number | null, title: string, offset: number }} ClipDemo
 * @typedef {{ from: number, to: number, track: Track, who: string, label: string, demo: ClipDemo }} Clip
 * @typedef {{ type: 'follow' | 'next', index: number } | { type: 'none' | 'end', index?: undefined }} PlaylistStep
 */

const HUB_GAMES_URL = 'https://hub.quakeworld.nu/games/';
// hub.quakeworld.nu src/utils/game-url.ts MAX_CLIPS @ 5c96205c19c7
export const MAX_HUB_CLIPS = 20;
const PLAYLIST_VERSION = 1;

// parseHubReplayUrl reads a link built by hubReplayUrl() (src/lib/hub-url.js) — the only
// shape the page emits: /games/?gameId=N&from=F[&to=T]&track=U.
/**
 * @param {string} href
 * @returns {{ gameId: number, from: number, to: number | null, track: Track } | null}
 */
export function parseHubReplayUrl(href) {
    let url;
    try {
        url = new URL(href);
    } catch {
        return null;
    }
    if (url.hostname !== 'hub.quakeworld.nu') return null;
    const p = url.searchParams;
    const gameId = Number.parseInt(p.get('gameId') || '', 10);
    const from = Number.parseInt(p.get('from') || '', 10);
    if (!Number.isFinite(gameId) || !Number.isFinite(from)) return null;
    const toRaw = p.get('to');
    const to = toRaw == null ? null : Number.parseInt(toRaw, 10);
    const trackRaw = p.get('track');
    const track = trackRaw && /^\d+$/.test(trackRaw) && trackRaw !== '0' ? Number(trackRaw) : 'auto';
    return { gameId, from, to: Number.isFinite(to) ? to : null, track };
}

// hubClipsUrl exports clips of ONE hub game as hub's native multi-clip link.
// Hub reads at most MAX_HUB_CLIPS and drops from >= to; the caller checks
// `clips.length` against the cap to tell the user.
/**
 * @param {number} gameId
 * @param {Array<Pick<Clip, 'from' | 'to' | 'track'>>} clips
 */
export function hubClipsUrl(gameId, clips) {
    const params = new URLSearchParams({ gameId: String(gameId) });
    for (const c of clips.slice(0, MAX_HUB_CLIPS)) {
        const from = Math.max(0, Math.floor(c.from));
        const to = Math.max(from + 1, Math.ceil(c.to));
        params.append('clip', `${from}-${to}:${c.track === 'auto' ? 'auto' : c.track}`);
    }
    // URLSearchParams encodes ':' as %3A; hub decodes either, the raw form
    // is the one people paste around.
    return `${HUB_GAMES_URL}?${params.toString().replace(/%3A/g, ':')}`;
}

// The single hub game every clip belongs to, or null when the list is
// empty, mixes demos, or holds a local file.
/**
 * @param {Clip[]} clips
 * @returns {number | null}
 */
export function singleHubGame(clips) {
    if (!clips.length) return null;
    const id = clips[0].demo?.gameId;
    if (!id) return null;
    return clips.every((c) => c.demo?.gameId === id) ? id : null;
}

// ── Share encoding ──────────────────────────────────────────────────────────
//
// `#playlist=<base64url(JSON)>`. Compact rows keep the URL short:
// [from, to, track, who, label, demoIndex] plus a demo table. Local-file
// demos travel by title only — the receiver has to load the file.

/** @param {Clip[]} clips */
export function encodePlaylist(clips) {
    /** @type {Array<[string, number, string, number]>} */
    const demos = [];
    /** @type {Map<string, number>} */
    const index = new Map();
    const rows = clips.map((c) => {
        const key = c.demo?.key || '';
        if (!index.has(key)) {
            index.set(key, demos.length);
            demos.push([key, c.demo?.gameId || 0, c.demo?.title || '', c.demo?.offset || 0]);
        }
        return [c.from, c.to, c.track, c.who || '', c.label || '', index.get(key)];
    });
    return base64UrlEncode(JSON.stringify({ v: PLAYLIST_VERSION, d: demos, c: rows }));
}

/**
 * @param {string} str
 * @returns {Clip[] | null}
 */
export function decodePlaylist(str) {
    try {
        return decodeRows(JSON.parse(base64UrlDecode(str)));
    } catch {
        return null;
    }
}

// A share link is untrusted input: anything malformed drops the row (or the
// whole link), and a demo's key must agree with its gameId — the page loads
// a clip's demo by gameId and then matches it by key.
/**
 * @param {any} doc  parsed share-link JSON, untrusted
 * @returns {Clip[] | null}
 */
function decodeRows(doc) {
    if (!doc || doc.v !== PLAYLIST_VERSION || !Array.isArray(doc.c) || !Array.isArray(doc.d)) return null;
    /** @type {Array<ClipDemo | null>} */
    const demos = doc.d.map((/** @type {unknown} */ row) => {
        if (!Array.isArray(row)) return null;
        const [key, gameId, title, offset] = row;
        if (typeof key !== 'string') return null;
        if (gameId) {
            if (!Number.isInteger(gameId) || gameId <= 0 || key !== `hub:${gameId}`) return null;
        } else if (!key.startsWith('file:')) {
            return null;
        }
        return { key, gameId: gameId || null, title: String(title || ''), offset: Number(offset) || 0 };
    });
    /** @type {Clip[]} */
    const clips = [];
    for (const row of doc.c) {
        if (!Array.isArray(row)) continue;
        const [from, to, track, who, label, di] = row;
        if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) continue;
        const demo = demos[di];
        if (!demo) continue;
        clips.push({
            from, to,
            track: Number.isInteger(track) && track > 0 ? track : 'auto',
            who: String(who || ''), label: String(label || ''),
            demo: { ...demo },
        });
    }
    return clips;
}

/** @param {string} text */
export function base64UrlEncode(text) {
    const bytes = new TextEncoder().encode(text);
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** @param {string} str */
export function base64UrlDecode(str) {
    const b64 = str.replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4));
    return new TextDecoder().decode(Uint8Array.from(bin, (ch) => ch.charCodeAt(0)));
}

// ── Scripted playback ───────────────────────────────────────────────────────

// playlistStep decides what the in-page runner does on one clock tick,
// mirroring hub's useClipListPlayback (src/features/demo-player/clips/
// use-clip-playback.ts @ 5c96205c19c7):
//   - a manual seek into another clip makes that clip the active one
//     ({ type: 'follow', index });
//   - playback crossing the active clip's `to` — not a seek past it, hence
//     the < 1.5 s step — advances ({ type: 'next', index }) or, on the
//     last clip, ends the run ({ type: 'end' });
//   - otherwise { type: 'none' }.
// `prev` is the previous tick's clock (null right after a jump).
/**
 * @param {Array<Pick<Clip, 'from' | 'to'>>} clips
 * @param {number} index
 * @param {number | null} prev
 * @param {number} now
 * @param {boolean} playing
 * @returns {PlaylistStep}
 */
export function playlistStep(clips, index, prev, now, playing) {
    const active = clips[index];
    if (!active) return { type: 'end' };
    const inActive = now >= active.from && now < active.to;
    if (!inActive) {
        const other = clips.findIndex((c, i) => i !== index && now >= c.from && now < c.to);
        if (other !== -1) return { type: 'follow', index: other };
    }
    if (!playing || prev == null) return { type: 'none' };
    const reachedEnd = prev < active.to && now >= active.to && now - prev < 1.5;
    if (!reachedEnd) return { type: 'none' };
    return index + 1 < clips.length ? { type: 'next', index: index + 1 } : { type: 'end' };
}

// ── Generated clips ─────────────────────────────────────────────────────────
//
// Clips built from match times (ms, match-relative — the clip finder in
// src/lib/clip-query.js, playerFragClips below). The conversion to demo
// seconds is the same `+ demoOffset` hubReplayUrl uses.

/**
 * @param {Result} result
 * @param {number} ms
 */
function demoSec(result, ms) {
    const off = result?.streams?.global?.demoOffset || 0;
    return (ms + off) / 1000;
}

/**
 * @param {Result} result
 * @param {ClipDemo} demo
 * @param {number} fromMs
 * @param {number} toMs
 * @param {number | null | undefined} track  a userid; none follows FTE's autotrack
 * @param {string} who
 * @param {string} label
 * @returns {Clip}
 */
export function makeClip(result, demo, fromMs, toMs, track, who, label) {
    return {
        from: Math.max(0, Math.floor(demoSec(result, fromMs))),
        to: Math.ceil(demoSec(result, toMs)),
        track: track || 'auto',
        who: track ? who : '',
        label,
        demo: { ...demo },
    };
}

/** @param {number} ms */
function mmss(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

// playerFragClips: every enemy kill by `name`, from the killer's view,
// 5 s before to 2 s after, with kills closer than that merged into one clip.
/**
 * @param {Result} result
 * @param {ClipDemo} demo
 * @param {string} name
 * @returns {Clip[]}
 */
export function playerFragClips(result, demo, name) {
    const userid = result?.timelineAnalysis?.playerUserIDs?.[name];
    const kills = /** @type {any[]} */ (result?.frags?.frags || [])
        .filter((f) => f.killer === name && !f.isSuicide && !f.isTeamKill)
        .sort((a, b) => a.time - b.time);
    /** @type {Array<{ fromMs: number, toMs: number, n: number, at: number, weapon: string, victim: string }>} */
    const merged = [];
    for (const k of kills) {
        const last = merged[merged.length - 1];
        if (last && k.time - 5000 <= last.toMs) {
            last.toMs = k.time + 2000;
            last.n++;
        } else {
            merged.push({ fromMs: k.time - 5000, toMs: k.time + 2000, n: 1, at: k.time, weapon: k.weapon, victim: k.victim });
        }
    }
    return merged.map((m) => makeClip(result, demo, m.fromMs, m.toMs, userid, name,
        m.n > 1 ? `${name} · ${m.n} frags from ${mmss(m.at)}` : `${name} · ${m.weapon} → ${m.victim}`));
}
