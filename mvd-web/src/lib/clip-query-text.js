// clip-query-text.js — a whole clip query as text, one line per stage:
//
//   name   "Best quad runs"
//   source events types=powerup
//   keep   detail.powerup = quad and detail.frags > 0
//   order  detail.frags desc first 5
//   clip   from=time-3s to=detail.endTime+2s watch=player label="{player}"
//   label  "Quad run · {player} · {detail.frags} frag{detail.frags|s}"
//
// `source` names the endpoint and its params (key=value, API names and
// values as-is); `keep` is the filter (src/lib/clip-expr-text.js);
// `order` a field, asc or desc and `first N`, each optional; each `clip`
// line one clip per row — from / to a row field with an offset in seconds,
// watch the field naming the player to follow, label its own; `label` the
// default label. Lines but `source` are optional; an indented line
// continues the one above; `#` starts a comment line. Values with spaces
// are "quoted" (JSON string escapes).

import { SOURCES, validateQuery } from './clip-query.js';
import { parseExpr, exprToText } from './clip-expr-text.js';

/**
 * @typedef {import('./clip-query.js').ClipQuery} ClipQuery
 * @typedef {import('./clip-query.js').ClipSpec} ClipSpec
 */

const TOKEN = /\s*(?:([^\s="]+)=("(?:[^"\\]|\\.)*"|[^\s"]*)|("(?:[^"\\]|\\.)*"|[^\s"]+))/y;

/** @param {string} raw */
function unquote(raw) {
    return raw.startsWith('"') ? JSON.parse(raw) : raw;
}

// tokens splits a line into bare words and key=value pairs.
/**
 * @param {string} line
 * @returns {Array<{ key?: string, value: string }>}
 */
function tokens(line) {
    /** @type {Array<{ key?: string, value: string }>} */
    const out = [];
    TOKEN.lastIndex = 0;
    while (TOKEN.lastIndex < line.length) {
        if (!line.slice(TOKEN.lastIndex).trim()) break;
        const at = TOKEN.lastIndex;
        const m = TOKEN.exec(line);
        if (!m || TOKEN.lastIndex === at) throw new Error(`cannot read "${line.slice(at).trim()}"`);
        out.push(m[1] != null ? { key: m[1], value: unquote(m[2]) } : { value: unquote(m[3]) });
    }
    return out;
}

/** @param {string} v */
const quote = (v) => (/^[^\s"=#]+$/.test(v) ? v : JSON.stringify(v));

/** @param {number} ms */
const sec = (ms) => `${ms / 1000}s`;

const EDGE = /^([A-Za-z_$][\w$.]*)?(?:([-+])(\d+(?:\.\d+)?)s)?$/;

// edge reads `field±Ns` (either part optional) as a field and ms.
/**
 * @param {string} v
 * @param {string} key
 */
function edge(v, key) {
    const m = EDGE.exec(v);
    if (!m) throw new Error(`${key}=${v}: write a field, an offset in seconds or both, e.g. ${key}=time${key === 'from' ? '-3s' : '+2s'}`);
    const ms = m[3] == null ? undefined : Math.round(Number(m[3]) * 1000) * (m[2] === '-' ? -1 : 1);
    return { field: m[1], ms };
}

// parseQueryText reads the text into a query and its name; any line it
// cannot read is an error naming the line.
/**
 * @param {string} text
 * @returns {{ query: ClipQuery, title: string, error?: undefined } | { error: string, query?: undefined, title?: undefined }}
 */
export function parseQueryText(text) {
    /** @type {Array<{ n: number, text: string }>} */
    const lines = [];
    text.split('\n').forEach((l, i) => {
        if (!l.trim() || l.trim().startsWith('#')) return;
        if (/^\s/.test(l) && lines.length) lines[lines.length - 1].text += ` ${l.trim()}`;
        else lines.push({ n: i + 1, text: l.trim() });
    });
    /** @type {Record<string, any>} */
    const q = {};
    let title = '';
    /** @type {ClipSpec[]} */
    const clips = [];
    for (const { n, text: line } of lines) {
        const kw = line.split(/\s/, 1)[0];
        const rest = line.slice(kw.length).trim();
        try {
            if (kw === 'name') {
                title = String(tokens(rest).map((t) => t.value).join(' '));
            } else if (kw === 'source') {
                const [name, ...params] = tokens(rest);
                if (!name || name.key) throw new Error('name the source first, e.g. source top-windows');
                const src = name.value.replace(/^\//, '');
                if (!Object.hasOwn(SOURCES, src)) throw new Error(`unknown source ${src} — one of ${Object.keys(SOURCES).join(', ')}`);
                q.source = src;
                for (const p of params) {
                    if (!p.key) throw new Error(`write params as name=value, not "${p.value}"`);
                    if (!SOURCES[src].params.includes(p.key)) throw new Error(`${src} has no param ${p.key} — it takes ${SOURCES[src].params.join(', ')}`);
                    (q.params ||= {})[p.key] = p.value;
                }
            } else if (kw === 'keep') {
                const r = parseExpr(rest);
                if (r.error) throw new Error(r.error);
                if (r.expr) q.where = r.expr;
            } else if (kw === 'order') {
                /** @type {{ by?: string, desc?: boolean, limit?: number }} */
                const rank = {};
                const ts = tokens(rest).map((t) => (t.key ? `${t.key}=${t.value}` : t.value));
                for (let i = 0; i < ts.length; i++) {
                    const t = ts[i];
                    if (t === 'desc' || t === 'asc') rank.desc = t === 'desc';
                    else if (t === 'first') {
                        const limit = Number(ts[++i]);
                        if (!Number.isInteger(limit) || limit < 1) throw new Error('first takes a count, e.g. first 5');
                        rank.limit = limit;
                    } else if (!rank.by && /^[A-Za-z_$][\w$.]*$/.test(t)) rank.by = t;
                    else throw new Error(`cannot read "${t}" — order <field> [asc|desc] [first N]`);
                }
                if (rank.by) q.rank = { by: rank.by, desc: rank.desc ?? true, ...(rank.limit ? { limit: rank.limit } : {}) };
                else if (rank.limit) q.rank = { limit: rank.limit };
            } else if (kw === 'clip') {
                /** @type {ClipSpec} */
                const spec = {};
                for (const t of tokens(rest)) {
                    if (t.key === 'from' || t.key === 'to') {
                        const e = edge(t.value, t.key);
                        if (e.field) spec[t.key] = e.field;
                        if (e.ms != null) spec[t.key === 'from' ? 'before' : 'after'] = t.key === 'from' ? 0 - e.ms || 0 : e.ms;
                    } else if (t.key === 'watch') spec.pov = t.value;
                    else if (t.key === 'label') spec.label = t.value;
                    else throw new Error(`cannot read "${t.key ? `${t.key}=` : ''}${t.value}" — clip takes from=, to=, watch= and label=`);
                }
                clips.push(spec);
            } else if (kw === 'label') {
                q.label = tokens(rest).map((t) => t.value).join(' ');
            } else {
                throw new Error(`unknown line "${kw}" — lines are name, source, keep, order, clip and label`);
            }
        } catch (err) {
            return { error: `line ${n}: ${/** @type {Error} */ (err).message}` };
        }
    }
    if (!q.source) return { error: 'a query needs a source line, e.g. source events types=frag' };
    if (clips.length > 1) q.clip = clips;
    else if (clips.length && Object.keys(clips[0]).length) q.clip = clips[0];
    const query = validateQuery(q);
    if (!query) return { error: 'the query is out of bounds (too many clip lines, an offset over 10 minutes, or text too long)' };
    return { query, title };
}

// queryToText prints a query in that form; parseQueryText reads it back.
/**
 * @param {ClipQuery} query
 * @param {string} [title]
 */
export function queryToText(query, title = '') {
    const out = [];
    if (title) out.push(`name   ${JSON.stringify(title)}`);
    const params = Object.entries(query.params || {}).map(([k, v]) => ` ${k}=${quote(String(v))}`).join('');
    out.push(`source ${query.source}${params}`);
    if (query.where) out.push(`keep   ${exprToText(query.where)}`);
    const r = query.rank;
    if (r && (r.by || r.limit)) out.push(`order  ${[r.by ? `${r.by} ${r.desc ? 'desc' : 'asc'}` : '', r.limit ? `first ${r.limit}` : ''].filter(Boolean).join(' ')}`);
    const specs = query.clip == null ? [] : Array.isArray(query.clip) ? query.clip : [query.clip];
    for (const c of specs) {
        const parts = [];
        if (c.from != null || c.before != null) parts.push(`from=${c.from || ''}${c.before != null ? (c.before > 0 ? `-${sec(c.before)}` : `+${sec(-c.before)}`) : ''}`);
        if (c.to != null || c.after != null) parts.push(`to=${c.to || ''}${c.after != null ? (c.after >= 0 ? `+${sec(c.after)}` : `-${sec(-c.after)}`) : ''}`);
        if (c.pov) parts.push(`watch=${quote(c.pov)}`);
        if (c.label) parts.push(`label=${JSON.stringify(c.label)}`);
        out.push(`clip   ${parts.join(' ')}`.trimEnd());
    }
    if (query.label) out.push(`label  ${JSON.stringify(query.label)}`);
    return out.join('\n');
}
