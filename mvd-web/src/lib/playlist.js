// playlist.js — the Player tab's clip model, kept free of DOM and page state
// so it runs under `node --test`.
//
// A clip is { from, to, track, who, label, demo, slow? }:
//   from / to  demo-relative whole seconds — the clock FTE's demo_jump and
//              hub's from/to/clip params use (match time + demoOffset)
//   track      a userid (number) or 'auto' (FTE's stats autotrack)
//   who        display name of the tracked player ('' for auto)
//   label      free text
//   demo       { key, gameId, title, offset } — which demo the clip belongs
//              to; key is 'hub:<gameId>' or 'file:<name>', offset its
//              demoOffset in seconds (for showing match time)
//   slow       optional { from, to, speed }: play [from, to) (demo seconds,
//              inside the clip) at `speed` (× 1) — slow motion at the event;
//              hub links have no such thing and drop it
//
// Times stay demo-relative in storage because both consumers (the engine
// and hub links) speak it; the page converts to match time for display.

/**
 * @typedef {import('./result.js').Result} Result
 * @typedef {number | 'auto'} Track
 * @typedef {{ key: string, gameId: number | null, title: string, offset: number }} ClipDemo
 * @typedef {{ from: number, to: number, speed: number }} Slow
 * @typedef {{ from: number, to: number, track: Track, who: string, label: string, demo: ClipDemo, slow?: Slow }} Clip
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

// playlistName names a shared playlist for the link text people paste
// into Discord: "mvda", the demo (its date where the loaded demo gives
// one, its map and teams — the clip's demo title) and what the list holds,
// read off the clips' labels (a generated label opens with its kind,
// "Quad run · gore · 2 frags"): one clip by its label, up to three kinds
// with their counts, else the number of clips. A list over several demos
// says how many.
/**
 * @param {Clip[]} clips
 * @param {{ key: string, date: string } | null} [loaded]  the loaded demo's key and date
 */
export function playlistName(clips, loaded) {
    const parts = ['mvda'];
    const keys = new Set(clips.map((c) => c.demo?.key));
    if (keys.size === 1) {
        const d = clips[0].demo;
        if (loaded?.date && d?.key === loaded.key) parts.push(loaded.date);
        if (d?.title) parts.push(d.title);
    } else if (keys.size > 1) {
        parts.push(`${keys.size} demos`);
    }
    /** @type {Map<string, number>} */
    const kinds = new Map();
    for (const c of clips) {
        const kind = (c.label || '').split(' · ')[0].trim() || 'Clip';
        kinds.set(kind, (kinds.get(kind) || 0) + 1);
    }
    if (clips.length === 1) parts.push(clips[0].label || 'Clip');
    else if (kinds.size <= 3) parts.push([...kinds].map(([k, n]) => (n > 1 ? `${k} ×${n}` : k)).join(', '));
    else parts.push(`${clips.length} clips`);
    return parts.join(' · ');
}

// markdownLink: `[text](url)`, the text's brackets swapped for parentheses
// so a label cannot end the link early.
/**
 * @param {string} text
 * @param {string} url
 */
export function markdownLink(text, url) {
    return `[${text.replace(/\[/g, '(').replace(/\]/g, ')')}](${url})`;
}

// ── Share encoding ──────────────────────────────────────────────────────────
//
// `#playlist=z<base64url(deflate-raw(JSON))>`. Compact rows keep the URL
// short: [from, to, track, who, label, demoIndex, slow?] plus a demo table
// (slow as [from, to, speed]); deflate then takes the labels' repeats (a
// generated list's labels share their kind and players) — about half the
// length. Links from before the compression, `#playlist=<base64url(JSON)>`,
// still decode: their base64 opens with "eyJ" ('{"'), never with the "z".
// Local-file demos travel by title only — the receiver has to load the
// file.

/**
 * @param {Clip[]} clips
 * @returns {Promise<string>}
 */
export async function encodePlaylist(clips) {
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
        /** @type {unknown[]} */
        const row = [c.from, c.to, c.track, c.who || '', c.label || '', index.get(key)];
        if (c.slow) row.push([c.slow.from, c.slow.to, c.slow.speed]);
        return row;
    });
    const json = JSON.stringify({ v: PLAYLIST_VERSION, d: demos, c: rows });
    return `z${bytesToBase64Url(await pipeBytes(new TextEncoder().encode(json), new CompressionStream('deflate-raw')))}`;
}

/**
 * @param {string} str
 * @returns {Promise<Clip[] | null>}
 */
export async function decodePlaylist(str) {
    try {
        const json = str.startsWith('z')
            ? new TextDecoder().decode(await pipeBytes(base64UrlToBytes(str.slice(1)), new DecompressionStream('deflate-raw')))
            : base64UrlDecode(str);
        return decodeRows(JSON.parse(json));
    } catch {
        return null;
    }
}

/**
 * @param {Uint8Array<ArrayBuffer>} bytes
 * @param {CompressionStream | DecompressionStream} through
 */
async function pipeBytes(bytes, through) {
    const out = new Blob([bytes]).stream().pipeThrough(through);
    return new Uint8Array(await new Response(out).arrayBuffer());
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
        const [from, to, track, who, label, di, slow] = row;
        if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) continue;
        const demo = demos[di];
        if (!demo) continue;
        /** @type {Clip} */
        const c = {
            from, to,
            track: Number.isInteger(track) && track > 0 ? track : 'auto',
            who: String(who || ''), label: String(label || ''),
            demo: { ...demo },
        };
        const sl = Array.isArray(slow) ? cleanSlow(c, slow[0], slow[1], slow[2]) : null;
        if (sl) c.slow = sl;
        clips.push(c);
    }
    return clips;
}

// Slow-motion speeds a clip may carry (× 1).
export const SLOW_MIN = 0.05;
export const SLOW_MAX = 1;

// cleanSlow clamps a slow-motion window into its clip (0.1 s steps); null
// when nothing of it is left or the speed is out of range — untrusted
// input (share links, storage) goes through it.
/**
 * @param {Pick<Clip, 'from' | 'to'>} clip
 * @param {unknown} from
 * @param {unknown} to
 * @param {unknown} speed
 * @returns {Slow | null}
 */
export function cleanSlow(clip, from, to, speed) {
    if (typeof from !== 'number' || typeof to !== 'number' || typeof speed !== 'number') return null;
    if (!(speed >= SLOW_MIN && speed <= SLOW_MAX)) return null;
    const a = Math.round(Math.max(clip.from, from) * 10) / 10;
    const b = Math.round(Math.min(clip.to, to) * 10) / 10;
    return a < b ? { from: a, to: b, speed } : null;
}

// slowAt: the speed a clip plays at demo time `t` — its slow-motion speed
// inside that window, else null (the viewer's own speed).
/**
 * @param {Pick<Clip, 'slow'> | null | undefined} clip
 * @param {number} t
 */
export function slowAt(clip, t) {
    const s = clip?.slow;
    return s && t >= s.from && t < s.to ? s.speed : null;
}

/** @param {string} text */
export function base64UrlEncode(text) {
    return bytesToBase64Url(new TextEncoder().encode(text));
}

/** @param {string} str */
export function base64UrlDecode(str) {
    return new TextDecoder().decode(base64UrlToBytes(str));
}

/** @param {Uint8Array} bytes */
function bytesToBase64Url(bytes) {
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** @param {string} str */
function base64UrlToBytes(str) {
    const b64 = str.replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4));
    return Uint8Array.from(bin, (ch) => ch.charCodeAt(0));
}

// ── Scripted playback ───────────────────────────────────────────────────────

// playlistStep decides what the in-page runner does on one clock tick,
// after hub's useClipListPlayback (src/features/demo-player/clips/
// use-clip-playback.ts @ 5c96205c19c7):
//   - playback crossing the active clip's `to` — not a seek past it, hence
//     the < 1.5 s step — advances ({ type: 'next', index }) or, on the
//     last clip, ends the run ({ type: 'end' });
//   - a manual seek into another clip makes that clip the active one
//     ({ type: 'follow', index });
//   - otherwise { type: 'none' }.
// Unlike hub, crossing the end wins over landing in another clip: the
// next clip often overlaps the end of this one (a pack's pickup, seconds
// after its drop), and following into it would keep this clip's POV and
// skip the next one's lead-in.
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
    if (playing && prev != null && prev < active.to && now >= active.to && now - prev < 1.5) {
        return index + 1 < clips.length ? { type: 'next', index: index + 1 } : { type: 'end' };
    }
    if (now >= active.from && now < active.to) return { type: 'none' };
    const other = clips.findIndex((c, i) => i !== index && now >= c.from && now < c.to);
    return other !== -1 ? { type: 'follow', index: other } : { type: 'none' };
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
 * @param {{ fromMs: number, toMs: number, speed: number }} [slow]  match ms
 * @returns {Clip}
 */
export function makeClip(result, demo, fromMs, toMs, track, who, label, slow) {
    /** @type {Clip} */
    const c = {
        from: Math.max(0, Math.floor(demoSec(result, fromMs))),
        to: Math.ceil(demoSec(result, toMs)),
        track: track || 'auto',
        who: track ? who : '',
        label,
        demo: { ...demo },
    };
    const sl = slow && cleanSlow(c, demoSec(result, slow.fromMs), demoSec(result, slow.toMs), slow.speed);
    if (sl) c.slow = sl;
    return c;
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
