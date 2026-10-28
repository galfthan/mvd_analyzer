// clip-query.js — the Player tab's clip finder: a query names one mvd-api
// endpoint and its query params, filters and ranks the rows it returns, and
// turns each row into a clip. Kept free of DOM and page state so it runs
// under `node --test`.
//
// A query is { source, params, where, rank, clip, label }:
//   source   an endpoint under /v1/demos/{id}/: 'events', 'highlights',
//            'top-kills', 'top-windows' or 'lives'
//   params   that endpoint's query params, by their API names and in their
//            API form (csv strings, ms) — sent as-is when the query runs
//            against mvd-api, translated to the view's options (viewCall)
//            when it runs on the demo the browser holds
//   where    rows to keep: a list of groups ORed together, each a list of
//            conditions ANDed — { field, op, value }, field a dotted path
//            into the row ('detail.frags', 'victims.0.name'); [] keeps all
//   rank     optional { by, desc, limit }: order by that field (ties by
//            time); without it clips play in time order
//   clip     { from, before, to, after, pov }: the window runs from row field
//            `from` minus `before` ms to field `to` plus `after` ms, tracking
//            the player named by field `pov`; unset fields take the source's
//   label    a template: {field} is the row's value, {field|s} a plural 's'
//            unless the value is 1, {field|sec} ms as whole seconds
//
// Rows, fields and params are the endpoint's own — mvd-api/openapi/openapi.yaml
// is their reference. The finder adds no detection of its own.

import { makeClip } from './playlist.js';

/**
 * @typedef {import('./result.js').Result} Result
 * @typedef {import('./playlist.js').Clip} Clip
 * @typedef {import('./playlist.js').ClipDemo} ClipDemo
 * @typedef {{ field: string, op: '=' | '!=' | '<' | '<=' | '>' | '>=' | '~', value: string | number }} Condition
 * @typedef {{ from?: string, before?: number, to?: string, after?: number, pov?: string }} ClipSpec
 * @typedef {object} ClipQuery
 * @property {string} source
 * @property {Record<string, string | number>} [params]
 * @property {Condition[][]} [where]
 * @property {{ by: string, desc?: boolean, limit?: number }} [rank]
 * @property {ClipSpec} [clip]
 * @property {string} [label]
 * @typedef {{ fn: string, opts: (p: Record<string, string | number>) => object, rows: (body: any) => any[], clip: Required<ClipSpec> }} Source
 * @typedef {(fn: string, opts: object) => Promise<any>} ViewFn
 */

/** @param {string | number | undefined} v */
const csv = (v) => (v == null || v === '' ? undefined : String(v).split(',').map((s) => s.trim()).filter(Boolean));
/** @param {string | number | undefined} v */
const int = (v) => (v == null || v === '' ? undefined : Number(v));

// The params each endpoint takes, mapped onto the view options the WASM
// export reads (mvd-api's handlers do the same mapping in Go).
/** @type {Record<string, Source>} */
export const SOURCES = {
    events: {
        fn: 'getEvents',
        opts: (p) => ({ StartTime: int(p.from), EndTime: int(p.to), Players: csv(p.players), Types: csv(p.types) }),
        rows: (b) => b?.events || [],
        clip: { from: 'time', before: 3000, to: 'time', after: 3000, pov: 'player' },
    },
    highlights: {
        fn: 'getHighlights',
        opts: (p) => ({ Kinds: csv(p.kinds), Players: csv(p.players) }),
        rows: (b) => [...(b?.discharges || []), ...(b?.quadbores || []), ...(b?.telefrags || []), ...(b?.airgibs || [])],
        clip: { from: 'time', before: 6000, to: 'time', after: 3000, pov: 'actor.name' },
    },
    'top-kills': {
        fn: 'getTopKills',
        opts: (p) => ({ Weapons: csv(p.weapons), Players: csv(p.players), GapMs: int(p.gapMs), ContestedMs: int(p.contestedMs), Limit: int(p.limit), Dmg: p.dmg }),
        rows: (b) => b?.kills || [],
        clip: { from: 'time', before: 5000, to: 'time', after: 2000, pov: 'killer' },
    },
    'top-windows': {
        fn: 'getTopWindows',
        opts: (p) => ({
            Metric: p.metric, Mode: p.mode, WindowMs: int(p.windowMs), GapMs: int(p.gapMs),
            Players: csv(p.players), Limit: int(p.limit), PerPlayer: int(p.perPlayer), Dmg: p.dmg,
        }),
        rows: (b) => b?.windows || [],
        clip: { from: 'start', before: 2000, to: 'end', after: 2000, pov: 'player' },
    },
    lives: {
        fn: 'getLives',
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

/**
 * @param {any} row
 * @param {Condition} c
 */
function test(row, c) {
    const v = fieldOf(row, c.field);
    if (c.op === '~') {
        const want = String(c.value).toLowerCase();
        if (Array.isArray(v)) return v.some((x) => String(x).toLowerCase() === want);
        return v != null && String(v).toLowerCase().includes(want);
    }
    const num = typeof v === 'number' && c.value !== '' && Number.isFinite(Number(c.value));
    const a = num ? v : String(v ?? '').toLowerCase();
    const b = num ? Number(c.value) : String(c.value).toLowerCase();
    switch (c.op) {
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
 * @param {Condition[][] | undefined} where
 */
export function matches(row, where) {
    if (!where?.length) return true;
    return where.some((group) => group.every((c) => test(row, c)));
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
        return v == null ? '?' : String(v);
    });
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
    const src = SOURCES[query.source];
    const spec = { ...src.clip, ...query.clip };
    const ids = result?.timelineAnalysis?.playerUserIDs || {};
    const rows = src.rows(body).filter((r) => matches(r, query.where));
    const start = (/** @type {any} */ r) => Number(fieldOf(r, spec.from)) || 0;
    const rank = query.rank;
    if (rank?.by) {
        const dir = rank.desc ? -1 : 1;
        const key = (/** @type {any} */ r) => Number(fieldOf(r, rank.by)) || 0;
        rows.sort((a, b) => dir * (key(a) - key(b)) || start(a) - start(b));
    } else {
        rows.sort((a, b) => start(a) - start(b));
    }
    const kept = rank?.limit ? rows.slice(0, rank.limit) : rows;
    return kept.map((r) => {
        const who = String(fieldOf(r, spec.pov) ?? '');
        const fromMs = start(r) - spec.before;
        const toMs = (Number(fieldOf(r, spec.to)) || start(r)) + spec.after;
        return makeClip(result, demo, fromMs, toMs, ids[who], who, renderLabel(query.label || '', r));
    });
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

// The presets the playlist's Generate menu offers.
/** @type {Array<{ id: string, title: string, query: ClipQuery }>} */
export const PRESETS = [
    {
        id: 'quad-runs', title: 'Quad runs',
        query: {
            source: 'events', params: { types: 'powerup' },
            where: [[{ field: 'detail.powerup', op: '=', value: 'quad' }]],
            clip: { before: 3000, to: 'detail.endTime', after: 2000 },
            label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
        },
    },
    {
        id: 'best-quad-runs', title: 'Best quad runs (most frags)',
        query: {
            source: 'events', params: { types: 'powerup' },
            where: [[{ field: 'detail.powerup', op: '=', value: 'quad' }, { field: 'detail.frags', op: '>', value: 0 }]],
            rank: { by: 'detail.frags', desc: true, limit: 5 },
            clip: { before: 3000, to: 'detail.endTime', after: 2000 },
            label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
        },
    },
    {
        id: 'frag-streaks', title: 'Frag streaks',
        query: {
            source: 'events', params: { types: 'streak' },
            clip: { before: 0, to: 'detail.endTime', after: 3000 },
            label: 'Streak · {player} · {detail.length} frags',
        },
    },
    {
        id: 'quadbores', title: 'Quadbores',
        query: { source: 'highlights', params: { kinds: 'quadbore' }, label: 'Quadbore · {actor.name} · quad held {quadHeldMs|sec} s' },
    },
    {
        id: 'airgibs', title: 'Airgibs',
        query: { source: 'highlights', params: { kinds: 'airgib' }, label: 'Airgib · {actor.name} → {victims.0.name}' },
    },
    {
        id: 'telefrags', title: 'Telefrags',
        query: { source: 'highlights', params: { kinds: 'telefrag' }, label: 'Telefrag · {actor.name} → {victims.0.name}' },
    },
    {
        id: 'discharges', title: 'Discharges',
        query: { source: 'highlights', params: { kinds: 'discharge' }, label: 'Discharge · {actor.name} · {enemyKills} kill{enemyKills|s}' },
    },
];
