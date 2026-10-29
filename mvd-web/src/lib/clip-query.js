// clip-query.js — the Player tab's clip finder: a query names one mvd-api
// endpoint and its query params, filters and ranks the rows it returns, and
// turns each row into a clip. Kept free of DOM and page state so it runs
// under `node --test`.
//
// A query is { source, params, where, rank, clip, label }:
//   source   an endpoint under /v1/demos/{id}/: 'events', 'highlights',
//            'backpacks', 'top-kills', 'top-windows' or 'lives'
//   params   that endpoint's query params, by their API names and in their
//            API form (csv strings, ms) — sent as-is when the query runs
//            against mvd-api, translated to the view's options (viewCall)
//            when it runs on the demo the browser holds
//   where    rows to keep: an expression tree — { and: [...] }, { or: [...] },
//            { not: expr } or a condition { field, op, value }, field a
//            dotted path into the row ('detail.frags', 'victims.0.name');
//            absent keeps all
//   rank     optional { by, desc, limit }: order by that field (ties by
//            time), then keep the first `limit`; without `by` rows stay in
//            time order, so `limit` alone keeps the earliest
//   clip     { from, before, to, after, pov, label }: the window runs from row
//            field `from` minus `before` ms to field `to` plus `after` ms,
//            tracking the player named by field `pov`; unset fields take the
//            source's. A list of them makes one clip per entry for each row
//            (a pack's drop and its pickup), skipping an entry whose `from`
//            field the row lacks. `slow` { at, before, to, after, speed }
//            plays row field `at` minus `before` ms to field `to` plus
//            `after` ms at `speed` — slow motion at the event; `at` defaults
//            to the clip's `from` field, `to` to `at`, the pads to 1 s, the
//            speed to 0.3 (clamped into the clip).
//   label    a template: {field} is the row's value, {field|s} a plural 's'
//            unless the value is 1, {field|sec} ms as whole seconds,
//            {field|upper} upper case; a clip entry's own label wins
//
// Rows, fields and params are the endpoint's own — mvd-api/openapi/openapi.yaml
// is their reference. The finder adds no detection of its own.

import { makeClip, base64UrlEncode, base64UrlDecode, SLOW_MIN, SLOW_MAX } from './playlist.js';

/**
 * @typedef {import('./result.js').Result} Result
 * @typedef {import('./playlist.js').Clip} Clip
 * @typedef {import('./playlist.js').ClipDemo} ClipDemo
 * @typedef {string | number | boolean} Scalar
 * @typedef {{ field: string, op: '=' | '!=' | '<' | '<=' | '>' | '>=' | '~' | 'in', value: Scalar | Scalar[] }} Condition
 * @typedef {Condition | { and: Expr[] } | { or: Expr[] } | { not: Expr }} Expr
 * @typedef {{ at?: string, before?: number, to?: string, after?: number, speed?: number }} SlowSpec
 * @typedef {{ from?: string, before?: number, to?: string, after?: number, pov?: string, label?: string, slow?: SlowSpec }} ClipSpec
 * @typedef {object} ClipQuery
 * @property {string} source
 * @property {Record<string, string | number>} [params]
 * @property {Expr} [where]
 * @property {{ by?: string, desc?: boolean, limit?: number }} [rank]
 * @property {ClipSpec | ClipSpec[]} [clip]
 * @property {string} [label]
 * @typedef {{ fn: string, params: string[], opts: (p: Record<string, string | number>) => object, rows: (body: any) => any[], clip: Required<Omit<ClipSpec, 'label' | 'slow'>> }} Source
 * @typedef {(fn: string, opts: object) => Promise<any>} ViewFn
 * @typedef {{ kind: 'cond', field: string, op: Condition['op'], value: string }} EditCond
 * @typedef {{ kind: 'group', op: 'and' | 'or', not: boolean, items: Array<EditGroup | EditCond> }} EditGroup
 * @typedef {{ on: boolean, at: string, before: string, to: string, after: string, speed: string }} DraftSlow
 * @typedef {{ from: string, before: string, to: string, after: string, pov: string, label: string, slow: DraftSlow }} DraftClip
 * @typedef {object} Draft  the builder's form: every field as typed text, times in seconds
 * @property {string} source
 * @property {Record<string, string>} params
 * @property {EditGroup} where
 * @property {{ by: string, desc: boolean, limit: string }} rank
 * @property {DraftClip[]} clips
 * @property {string} label
 */

// A slow-motion window's defaults: a second either side of the event, at
// 0.3×.
export const SLOW_DEFAULT = { before: 1000, after: 1000, speed: 0.3 };

/** @param {string | number | undefined} v */
const csv = (v) => (v == null || v === '' ? undefined : String(v).split(',').map((s) => s.trim()).filter(Boolean));
/** @param {string | number | undefined} v */
const int = (v) => (v == null || v === '' ? undefined : Number(v));

// The params of each endpoint the finder offers, mapped onto the view
// options the WASM export reads (mvd-api's handlers do the same mapping in
// Go). What each param and row field means is the spec's, extracted into
// api-docs.json (mvd-api/webdocs_test.go pins it and checks these names).
/** @type {Record<string, Source>} */
export const SOURCES = {
    events: {
        fn: 'getEvents',
        params: ['types', 'players', 'from', 'to'],
        opts: (p) => ({ StartTime: int(p.from), EndTime: int(p.to), Players: csv(p.players), Types: csv(p.types) }),
        rows: (b) => b?.events || [],
        clip: { from: 'time', before: 3000, to: 'time', after: 3000, pov: 'player' },
    },
    highlights: {
        fn: 'getHighlights',
        params: ['kinds', 'players'],
        opts: (p) => ({ Kinds: csv(p.kinds), Players: csv(p.players) }),
        rows: (b) => [...(b?.discharges || []), ...(b?.quadbores || []), ...(b?.telefrags || []), ...(b?.airgibs || [])],
        clip: { from: 'time', before: 6000, to: 'time', after: 3000, pov: 'actor.name' },
    },
    backpacks: {
        fn: 'getBackpacks',
        params: ['weapons', 'players', 'from', 'to'],
        opts: (p) => ({ Players: csv(p.players), Weapons: csv(p.weapons), From: int(p.from), To: int(p.to) }),
        rows: (b) => b?.backpacks || [],
        clip: { from: 'time', before: 8000, to: 'time', after: 2000, pov: 'player' },
    },
    'top-kills': {
        fn: 'getTopKills',
        params: ['weapons', 'players', 'gapMs', 'contestedMs', 'limit', 'dmg'],
        opts: (p) => ({ Weapons: csv(p.weapons), Players: csv(p.players), GapMs: int(p.gapMs), ContestedMs: int(p.contestedMs), Limit: int(p.limit), Dmg: p.dmg }),
        rows: (b) => b?.kills || [],
        clip: { from: 'time', before: 5000, to: 'time', after: 2000, pov: 'killer' },
    },
    'top-windows': {
        fn: 'getTopWindows',
        params: ['metric', 'mode', 'windowMs', 'gapMs', 'players', 'limit', 'perPlayer', 'dmg'],
        opts: (p) => ({
            Metric: p.metric, Mode: p.mode, WindowMs: int(p.windowMs), GapMs: int(p.gapMs),
            Players: csv(p.players), Limit: int(p.limit), PerPlayer: int(p.perPlayer), Dmg: p.dmg,
        }),
        rows: (b) => b?.windows || [],
        clip: { from: 'start', before: 2000, to: 'end', after: 2000, pov: 'player' },
    },
    lives: {
        fn: 'getLives',
        params: ['players', 'from', 'to', 'minMs', 'dmg'],
        opts: (p) => ({ Players: csv(p.players), From: int(p.from), To: int(p.to), MinMs: int(p.minMs), Dmg: p.dmg }),
        rows: (b) => b?.lives || [],
        clip: { from: 'start', before: 0, to: 'end', after: 2000, pov: 'player' },
    },
};

// Endpoints whose damage family the API defaults to bounded, falling back
// to raw on a demo without one; the views default to raw, so the call
// names the family the way the handler does.
const BOUNDED_DEFAULT = new Set(['top-kills', 'top-windows', 'lives']);

// viewCall runs a query's endpoint over the demo the browser holds and
// resolves with the body mvd-api would answer.
/**
 * @param {ViewFn} view  DemoHandle.view
 * @param {string} source
 * @param {Record<string, string | number>} [params]
 */
export async function viewCall(view, source, params = {}) {
    const src = SOURCES[source];
    if (!src) throw new Error(`unknown clip source: ${source}`);
    const opts = src.opts(params);
    if (!BOUNDED_DEFAULT.has(source) || params.dmg) return view(src.fn, opts);
    try {
        return await view(src.fn, { ...opts, Dmg: 'bounded' });
    } catch (err) {
        if (!/bounded damage family unavailable/.test(String(/** @type {Error} */ (err)?.message))) throw err;
        return view(src.fn, { ...opts, Dmg: 'raw' });
    }
}

/**
 * @param {any} row
 * @param {string} path
 */
export function fieldOf(row, path) {
    let v = row;
    for (const k of path.split('.')) {
        if (v == null) return undefined;
        v = v[k];
    }
    return v;
}

export const OPS = /** @type {const} */ (['=', '!=', '<', '<=', '>', '>=', '~', 'in']);

// Comparisons: numbers as numbers when the row's value is one, everything
// else as case-insensitive text; `~` is a substring on text and membership
// on a list, `in` is "equals any of" a list.
/**
 * @param {unknown} v
 * @param {string} op
 * @param {Scalar} want
 */
function compare(v, op, want) {
    if (op === '~') {
        const w = String(want).toLowerCase();
        if (Array.isArray(v)) return v.some((x) => String(x).toLowerCase() === w);
        return v != null && String(v).toLowerCase().includes(w);
    }
    const num = typeof v === 'number' && want !== '' && Number.isFinite(Number(want));
    const a = num ? v : String(v ?? '').toLowerCase();
    const b = num ? Number(want) : String(want).toLowerCase();
    switch (op) {
        case '=': return a === b;
        case '!=': return a !== b;
        case '<': return a < b;
        case '<=': return a <= b;
        case '>': return a > b;
        case '>=': return a >= b;
    }
    return false;
}

/**
 * @param {any} row
 * @param {Expr | undefined} where
 * @returns {boolean}
 */
export function matches(row, where) {
    if (!where) return true;
    if ('and' in where) return where.and.every((e) => matches(row, e));
    if ('or' in where) return where.or.some((e) => matches(row, e));
    if ('not' in where) return !matches(row, where.not);
    const v = fieldOf(row, where.field);
    if (where.op === 'in') return (Array.isArray(where.value) ? where.value : [where.value]).some((w) => compare(v, '=', w));
    return compare(v, where.op, /** @type {Scalar} */ (where.value));
}

/**
 * @param {string} template
 * @param {any} row
 */
export function renderLabel(template, row) {
    return template.replace(/\{([^}|]+)(?:\|(\w+))?\}/g, (_, path, filter) => {
        const v = fieldOf(row, path.trim());
        if (filter === 's') return v === 1 ? '' : 's';
        if (filter === 'sec') return String(Math.round((Number(v) || 0) / 1000));
        if (filter === 'upper') return v == null ? '?' : String(v).toUpperCase();
        return v == null ? '?' : String(v);
    });
}

// stages runs a query over an endpoint's body one stage at a time, so the
// finder can show what each keeps: the rows the endpoint returned, those
// the filter kept, those the order and limit kept, and the clips cut from
// them.
/**
 * @param {ClipQuery} query
 * @param {any} body
 * @param {Result} result
 * @param {ClipDemo} demo
 * @returns {{ rows: any[], kept: any[], ranked: any[], clips: Clip[] }}
 */
export function stages(query, body, result, demo) {
    const src = SOURCES[query.source];
    const specs = (Array.isArray(query.clip) ? query.clip : [query.clip || {}]).map((c) => ({ ...src.clip, ...c }));
    const ids = result?.timelineAnalysis?.playerUserIDs || {};
    const rows = src.rows(body);
    const kept = rows.filter((r) => matches(r, query.where));
    const start = (/** @type {any} */ r) => Number(fieldOf(r, specs[0].from)) || 0;
    const rank = query.rank;
    const sorted = [...kept];
    if (rank?.by) {
        const by = rank.by;
        const dir = rank.desc ? -1 : 1;
        const key = (/** @type {any} */ r) => Number(fieldOf(r, by)) || 0;
        sorted.sort((a, b) => dir * (key(a) - key(b)) || start(a) - start(b));
    } else {
        sorted.sort((a, b) => start(a) - start(b));
    }
    const ranked = rank?.limit ? sorted.slice(0, rank.limit) : sorted;
    const clips = ranked.flatMap((r) => specs.flatMap((spec) => {
        const from = fieldOf(r, spec.from);
        if (from == null) return [];
        const who = String(fieldOf(r, spec.pov) ?? '');
        const fromMs = Number(from) - spec.before;
        const toMs = (Number(fieldOf(r, spec.to)) || Number(from)) + spec.after;
        return [makeClip(result, demo, fromMs, toMs, ids[who], who, renderLabel(spec.label ?? query.label ?? '', r), slowOf(spec, r))];
    }));
    return { rows, kept, ranked, clips };
}

// slowOf: a clip spec's slow-motion window on one row, in match ms; none
// without `slow` or when the row lacks its field.
/**
 * @param {Required<Omit<ClipSpec, 'label' | 'slow'>> & { slow?: SlowSpec }} spec
 * @param {any} row
 */
function slowOf(spec, row) {
    const sl = spec.slow;
    if (!sl) return undefined;
    const at = Number(fieldOf(row, sl.at || spec.from));
    if (!Number.isFinite(at)) return undefined;
    const to = Number(fieldOf(row, sl.to || sl.at || spec.from));
    return {
        fromMs: at - (sl.before ?? SLOW_DEFAULT.before),
        toMs: (Number.isFinite(to) ? to : at) + (sl.after ?? SLOW_DEFAULT.after),
        speed: sl.speed ?? SLOW_DEFAULT.speed,
    };
}

// clipsOf turns an endpoint's body into clips: filter, rank, window.
/**
 * @param {ClipQuery} query
 * @param {any} body
 * @param {Result} result
 * @param {ClipDemo} demo
 * @returns {Clip[]}
 */
export function clipsOf(query, body, result, demo) {
    return stages(query, body, result, demo).clips;
}

// runQuery runs a query on the demo the browser holds.
/**
 * @param {ClipQuery} query
 * @param {ViewFn} view
 * @param {Result} result
 * @param {ClipDemo} demo
 */
export async function runQuery(query, view, result, demo) {
    return clipsOf(query, await viewCall(view, query.source, query.params), result, demo);
}

// A pack's two clips: the lead-up to the drop from the dropper's view — why
// they died with it — then a short look at the pickup from the picker's (a
// pack nobody took has no pickupTime and gets only the first).
/** @type {ClipSpec} */
const PACK_DROP = { before: 12000, after: 2000, label: '{weapon|upper} pack · {player} drops it' };
/** @type {ClipSpec} */
const PACK_PICKUP = { from: 'pickupTime', before: 3000, to: 'pickupTime', after: 2000, pov: 'picker', label: '{weapon|upper} pack · {picker} takes it ({pickerRelation})' };

// The built-in presets, for the finder's Load menu and the playlist's
// Generate menu; `about` says in a line what each finds.
/** @type {Array<{ id: string, title: string, about: string, query: ClipQuery }>} */
export const PRESETS = [
    {
        id: 'quad-runs', title: 'Quad runs', about: 'Every quad, from the pickup until it runs out, watched from the player who took it.',
        query: {
            source: 'events', params: { types: 'powerup' },
            where: { field: 'detail.powerup', op: '=', value: 'quad' },
            clip: { before: 3000, to: 'detail.endTime', after: 2000 },
            label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
        },
    },
    {
        id: 'best-quad-runs', title: 'Best quad runs (most frags)', about: 'The five quad runs with the most frags.',
        query: {
            source: 'events', params: { types: 'powerup' },
            where: { and: [{ field: 'detail.powerup', op: '=', value: 'quad' }, { field: 'detail.frags', op: '>', value: 0 }] },
            rank: { by: 'detail.frags', desc: true, limit: 5 },
            clip: { before: 3000, to: 'detail.endTime', after: 2000 },
            label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
        },
    },
    {
        id: 'frag-streaks', title: 'Frag streaks', about: 'Every frag streak, from its first frag to three seconds after its last.',
        query: {
            source: 'events', params: { types: 'streak' },
            clip: { before: 0, to: 'detail.endTime', after: 3000 },
            label: 'Streak · {player} · {detail.length} frags',
        },
    },
    {
        id: 'pack-drops', title: 'Pack drops (RL/LG)', about: 'Every RL / LG pack dropped: the 12 s before the death that dropped it, then a short look at whoever took it.',
        query: { source: 'backpacks', clip: [PACK_DROP, PACK_PICKUP] },
    },
    {
        id: 'packs-enemy', title: 'Packs the enemy took', about: 'RL / LG packs the other team took — why it was dropped, and who got it.',
        query: { source: 'backpacks', where: { field: 'pickerRelation', op: '=', value: 'enemy' }, clip: [PACK_DROP, PACK_PICKUP] },
    },
    {
        id: 'packs-team', title: 'Packs a teammate took', about: 'RL / LG packs a teammate took.',
        query: { source: 'backpacks', where: { field: 'pickerRelation', op: '=', value: 'team' }, clip: [PACK_DROP, PACK_PICKUP] },
    },
    {
        id: 'packs-enemy-new', title: 'Packs the enemy took without having the weapon', about: 'The worst case: the enemy took the pack and did not have that weapon before.',
        query: {
            source: 'backpacks',
            where: { and: [{ field: 'pickerRelation', op: '=', value: 'enemy' }, { field: 'pickerHadWeapon', op: '=', value: false }] },
            clip: [PACK_DROP, PACK_PICKUP],
        },
    },
    {
        id: 'quadbores', title: 'Quadbores', about: 'Players killing themselves with their own rocket or grenade while holding quad.',
        query: { source: 'highlights', params: { kinds: 'quadbore' }, clip: { slow: {} }, label: 'Quadbore · {actor.name} · quad held {quadHeldMs|sec} s' },
    },
    {
        id: 'airgibs', title: 'Airgibs', about: 'Direct rocket hits on enemies in mid-air.',
        query: { source: 'highlights', params: { kinds: 'airgib' }, clip: { slow: {} }, label: 'Airgib · {actor.name} → {victims.0.name}' },
    },
    {
        id: 'telefrags', title: 'Telefrags', about: 'Kills by teleporting onto someone.',
        query: { source: 'highlights', params: { kinds: 'telefrag' }, clip: { slow: {} }, label: 'Telefrag · {actor.name} → {victims.0.name}' },
    },
    {
        id: 'discharges', title: 'Discharges', about: 'Lightning-gun discharges in water.',
        query: { source: 'highlights', params: { kinds: 'discharge' }, clip: { slow: {} }, label: 'Discharge · {actor.name} · {enemyKills} kill{enemyKills|s}' },
    },
];

// fieldPaths lists the dotted paths a builder can offer for rows: every leaf
// of the first rows, a list of objects by its first element
// ('victims.0.name'), a list of values as itself.
/**
 * @param {any[]} rows
 * @returns {string[]}
 */
export function fieldPaths(rows) {
    const out = new Set();
    /**
     * @param {any} v
     * @param {string} path
     * @param {number} depth
     */
    const walk = (v, path, depth) => {
        if (v && typeof v === 'object' && !Array.isArray(v) && depth < 4) {
            for (const k of Object.keys(v)) walk(v[k], path ? `${path}.${k}` : k, depth + 1);
        } else if (Array.isArray(v) && v.length && v[0] && typeof v[0] === 'object' && depth < 4) {
            walk(v[0], `${path}.0`, depth + 1);
        } else if (path) {
            out.add(path);
        }
    };
    for (const r of rows.slice(0, 200)) walk(r, '', 0);
    return [...out].sort();
}

// ── The builder's editable tree ─────────────────────────────────────────────
//
// The builder edits groups (an and / or over items, optionally negated) and
// conditions with their value as typed text; toExpr turns that back into the
// stored tree, typing values (numbers, true / false, comma lists for `in`),
// dropping conditions with no field and unwrapping a group of one.

/** @param {string} text */
function typed(text) {
    const t = text.trim();
    if (/^-?\d+(\.\d+)?$/.test(t)) return Number(t);
    if (t === 'true' || t === 'false') return t === 'true';
    return t;
}

/**
 * @param {Expr | undefined} expr
 * @returns {EditGroup}
 */
export function toEditTree(expr) {
    /** @type {(e: Expr) => EditGroup | EditCond} */
    const node = (e) => {
        if ('and' in e || 'or' in e) {
            const or = 'or' in e;
            return { kind: 'group', op: or ? 'or' : 'and', not: false, items: (or ? /** @type {{ or: Expr[] }} */ (e).or : /** @type {{ and: Expr[] }} */ (e).and).map(node) };
        }
        if ('not' in e) {
            const inner = node(e.not);
            if (inner.kind === 'group' && !inner.not) return { ...inner, not: true };
            return { kind: 'group', op: 'and', not: true, items: [inner] };
        }
        return { kind: 'cond', field: e.field, op: e.op, value: Array.isArray(e.value) ? e.value.join(', ') : String(e.value) };
    };
    if (!expr) return { kind: 'group', op: 'and', not: false, items: [] };
    const root = node(expr);
    return root.kind === 'group' ? root : { kind: 'group', op: 'and', not: false, items: [root] };
}

/**
 * @param {EditGroup} root
 * @returns {Expr | undefined}
 */
export function toExpr(root) {
    /** @type {(n: EditGroup | EditCond) => Expr | null} */
    const node = (n) => {
        if (n.kind === 'cond') {
            if (!n.field.trim()) return null;
            const value = n.op === 'in' ? n.value.split(',').map(typed).filter((v) => v !== '') : typed(n.value);
            return { field: n.field.trim(), op: n.op, value };
        }
        const items = /** @type {Expr[]} */ (n.items.map(node).filter(Boolean));
        if (items.length === 1) return n.not ? { not: items[0] } : items[0];
        /** @type {Expr} */
        const g = n.op === 'or' ? { or: items } : { and: items };
        return n.not ? { not: g } : g;
    };
    const e = node(root);
    if (e && 'and' in e && !e.and.length) return undefined;
    return e || undefined;
}

/** @param {number | undefined} ms */
const secText = (ms) => (ms == null ? '' : String(ms / 1000));

/**
 * @param {ClipQuery} q
 * @returns {Draft}
 */
export function toDraft(q) {
    const specs = q.clip == null ? [{}] : Array.isArray(q.clip) ? q.clip : [q.clip];
    return {
        source: q.source,
        params: Object.fromEntries(Object.entries(q.params || {}).map(([k, v]) => [k, String(v)])),
        where: toEditTree(q.where),
        rank: { by: q.rank?.by || '', desc: q.rank?.desc ?? true, limit: q.rank?.limit ? String(q.rank.limit) : '' },
        clips: specs.map((c) => ({
            from: c.from || '', before: secText(c.before), to: c.to || '', after: secText(c.after), pov: c.pov || '', label: c.label || '',
            slow: {
                on: !!c.slow, at: c.slow?.at || '', before: secText(c.slow?.before), to: c.slow?.to || '', after: secText(c.slow?.after),
                speed: c.slow?.speed == null ? '' : String(c.slow.speed),
            },
        })),
        label: q.label || '',
    };
}

/**
 * @param {Draft} d
 * @returns {ClipQuery}
 */
export function fromDraft(d) {
    /** @type {ClipQuery} */
    const q = { source: d.source };
    const params = Object.fromEntries(Object.entries(d.params).filter(([k, v]) => v.trim() && (SOURCES[d.source]?.params || []).includes(k)).map(([k, v]) => [k, v.trim()]));
    if (Object.keys(params).length) q.params = params;
    const where = toExpr(d.where);
    if (where) q.where = where;
    const limit = Number.parseInt(d.rank.limit, 10);
    if (d.rank.by.trim()) q.rank = { by: d.rank.by.trim(), desc: d.rank.desc, ...(limit > 0 ? { limit } : {}) };
    else if (limit > 0) q.rank = { limit };
    const specs = d.clips.map((c) => {
        /** @type {ClipSpec} */
        const spec = {};
        for (const k of /** @type {const} */ (['from', 'to', 'pov', 'label'])) if (c[k].trim()) spec[k] = c[k].trim();
        for (const k of /** @type {const} */ (['before', 'after'])) {
            const sec = Number.parseFloat(c[k]);
            if (Number.isFinite(sec)) spec[k] = Math.round(sec * 1000);
        }
        if (c.slow.on) {
            /** @type {SlowSpec} */
            const sl = {};
            for (const k of /** @type {const} */ (['at', 'to'])) if (c.slow[k].trim()) sl[k] = c.slow[k].trim();
            for (const k of /** @type {const} */ (['before', 'after'])) {
                const sec = Number.parseFloat(c.slow[k]);
                if (Number.isFinite(sec)) sl[k] = Math.round(sec * 1000);
            }
            const speed = Number.parseFloat(c.slow.speed);
            if (speed >= SLOW_MIN && speed <= SLOW_MAX) sl.speed = speed;
            spec.slow = sl;
        }
        return spec;
    });
    if (specs.length > 1) q.clip = specs;
    else if (specs.length && Object.keys(specs[0]).length) q.clip = specs[0];
    if (d.label.trim()) q.label = d.label.trim();
    return q;
}

// ── Untrusted queries: share links and stored presets ───────────────────────

const MAX_TEXT = 300;
/** @param {unknown} v */
const text = (v) => (typeof v === 'string' && v.length <= MAX_TEXT ? v : null);
/** @param {unknown} v */
const scalar = (v) => (typeof v === 'number' && Number.isFinite(v)) || typeof v === 'boolean' || text(v) != null;

/**
 * @param {unknown} e
 * @param {number} depth
 * @param {{ n: number }} budget
 * @returns {Expr | null}
 */
function cleanExpr(e, depth, budget) {
    if (!e || typeof e !== 'object' || depth > 8 || ++budget.n > 200) return null;
    const o = /** @type {Record<string, any>} */ (e);
    for (const k of /** @type {const} */ (['and', 'or'])) {
        if (k in o) {
            if (!Array.isArray(o[k])) return null;
            const items = o[k].map((/** @type {unknown} */ x) => cleanExpr(x, depth + 1, budget));
            return items.includes(null) ? null : /** @type {Expr} */ ({ [k]: items });
        }
    }
    if ('not' in o) {
        const inner = cleanExpr(o.not, depth + 1, budget);
        return inner && { not: inner };
    }
    const field = text(o.field);
    if (!field || !OPS.includes(o.op)) return null;
    const ok = Array.isArray(o.value) ? o.value.length <= 50 && o.value.every(scalar) : scalar(o.value);
    return ok ? { field, op: o.op, value: o.value } : null;
}

/**
 * @param {unknown} c
 * @returns {ClipSpec | null}
 */
function cleanSpec(c) {
    if (!c || typeof c !== 'object') return null;
    const o = /** @type {Record<string, any>} */ (c);
    /** @type {ClipSpec} */
    const out = {};
    for (const k of /** @type {const} */ (['from', 'to', 'pov', 'label'])) {
        if (o[k] == null) continue;
        const t = text(o[k]);
        if (t == null) return null;
        out[k] = t;
    }
    for (const k of /** @type {const} */ (['before', 'after'])) {
        if (o[k] == null) continue;
        if (!ms(o[k])) return null;
        out[k] = o[k];
    }
    if (o.slow != null) {
        const sl = cleanSlowSpec(o.slow);
        if (!sl) return null;
        out.slow = sl;
    }
    return out;
}

/** @param {unknown} v  an offset in ms, at most 10 minutes */
const ms = (v) => typeof v === 'number' && Number.isFinite(v) && Math.abs(v) <= 600000;

/**
 * @param {unknown} c
 * @returns {SlowSpec | null}
 */
function cleanSlowSpec(c) {
    if (!c || typeof c !== 'object') return null;
    const o = /** @type {Record<string, any>} */ (c);
    /** @type {SlowSpec} */
    const out = {};
    for (const k of /** @type {const} */ (['at', 'to'])) {
        if (o[k] == null) continue;
        const t = text(o[k]);
        if (t == null) return null;
        out[k] = t;
    }
    for (const k of /** @type {const} */ (['before', 'after'])) {
        if (o[k] == null) continue;
        if (!ms(o[k])) return null;
        out[k] = o[k];
    }
    if (o.speed != null) {
        if (!(typeof o.speed === 'number' && o.speed >= SLOW_MIN && o.speed <= SLOW_MAX)) return null;
        out.speed = o.speed;
    }
    return out;
}

// validateQuery returns a clean copy of a query from outside the page (a
// share link, localStorage), or null when anything in it is malformed —
// unknown source or param, a bad operator, an oversized tree.
/**
 * @param {unknown} raw
 * @returns {ClipQuery | null}
 */
export function validateQuery(raw) {
    if (!raw || typeof raw !== 'object') return null;
    const q = /** @type {Record<string, any>} */ (raw);
    const src = typeof q.source === 'string' && Object.hasOwn(SOURCES, q.source) ? SOURCES[q.source] : null;
    if (!src) return null;
    /** @type {ClipQuery} */
    const out = { source: q.source };
    if (q.params != null) {
        if (typeof q.params !== 'object') return null;
        /** @type {Record<string, string | number>} */
        const params = {};
        for (const [k, v] of Object.entries(q.params)) {
            if (!src.params.includes(k) || !(typeof v === 'number' || text(v) != null)) return null;
            params[k] = v;
        }
        out.params = params;
    }
    if (q.where != null) {
        const w = cleanExpr(q.where, 0, { n: 0 });
        if (!w) return null;
        out.where = w;
    }
    if (q.rank != null) {
        const by = q.rank?.by == null ? '' : text(q.rank.by);
        const limit = q.rank?.limit;
        if (by == null || (limit != null && !(Number.isInteger(limit) && limit > 0 && limit <= 1000)) || (!by && !limit)) return null;
        out.rank = by ? { by, desc: !!q.rank.desc, ...(limit ? { limit } : {}) } : { limit };
    }
    if (q.clip != null) {
        const specs = (Array.isArray(q.clip) ? q.clip : [q.clip]).map(cleanSpec);
        if (!specs.length || specs.length > 4 || specs.includes(null)) return null;
        out.clip = Array.isArray(q.clip) ? /** @type {ClipSpec[]} */ (specs) : /** @type {ClipSpec} */ (specs[0]);
    }
    if (q.label != null) {
        const l = text(q.label);
        if (l == null) return null;
        out.label = l;
    }
    return out;
}

const QUERY_VERSION = 1;

/**
 * @param {ClipQuery} query
 * @param {string} [title]
 */
export function encodeQuery(query, title = '') {
    return base64UrlEncode(JSON.stringify({ v: QUERY_VERSION, t: title, q: query }));
}

/**
 * @param {string} str
 * @returns {{ title: string, query: ClipQuery } | null}
 */
export function decodeQuery(str) {
    try {
        const doc = JSON.parse(base64UrlDecode(str));
        if (doc?.v !== QUERY_VERSION) return null;
        const query = validateQuery(doc.q);
        return query && { title: text(doc.t) || '', query };
    } catch {
        return null;
    }
}
