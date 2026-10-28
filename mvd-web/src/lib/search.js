// search.js — the Search tab's DOM-free parts: the hub's mode facets, a
// result row's cells, the paging arithmetic and the filter form's value
// rules. The hub client itself (the query, SEARCH_FIELDS) is
// src/data/hub.js.

// The mode facet list lives in ONE place: it fills the Mode dropdown (after
// "Any") and labels the result rows, so a facet cannot be offered in the
// dropdown and render as a raw token in the rows below it.
export const SEARCH_MODE_LABELS = {
    '1on1': '1v1',
    '2on2': '2v2',
    '4on4': '4v4',
    'ffa':  'FFA',
    'ctf':  'CTF',
};

// Which of those hub facets name a mode with no teams. This is the HUB's
// vocabulary on a HUB search row — not a Result — so it cannot read
// match.gameMode: the row is a listing from hub.quakeworld.nu carrying its
// own `mode` string, and no demo has been analysed at that point. It lives
// here beside the facet table so the pair stays in step.
export const SEARCH_INDIVIDUAL_MODES = new Set(['1on1', 'ffa']);

export const EMPTY_FILTERS = Object.freeze({ player: '', team: '', map: '', mode: '', tag: '', from: '', to: '' });

const TEXT_FIELDS = ['player', 'team', 'map', 'tag'];

// searchFilters: the query the form asks for — the text fields trimmed, the
// dates as they are, a mode the dropdown does not offer (a deep link's
// ?mode=bogus selects no option) read as '' like the <select> reads it.
export function searchFilters(form) {
    const f = { ...EMPTY_FILTERS, ...form };
    for (const k of TEXT_FIELDS) f[k] = String(f[k] ?? '').trim();
    if (f.mode !== '' && !(f.mode in SEARCH_MODE_LABELS)) f.mode = '';
    return { player: f.player, team: f.team, map: f.map, mode: f.mode, tag: f.tag, from: f.from, to: f.to };
}

// validDateString: what an <input type="date"> keeps when its value is set
// (yyyy-mm-dd naming a real day); anything else it clears to ''.
export function validDateString(s) {
    const m = /^(\d{4,})-(\d{2})-(\d{2})$/.exec(s);
    if (!m) return false;
    const y = +m[1], mo = +m[2], d = +m[3];
    if (y < 1 || mo < 1 || mo > 12 || d < 1) return false;
    const leap = (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0;
    const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][mo - 1];
    return d <= days;
}

// formValues: `filters` (e.g. read off the URL) as the form holds them once
// written in — only the fields given; a date the input cannot hold reads
// ''. A mode the dropdown does not offer is kept: it selects no option
// (the <select> shows blank), and searchFilters reads it as ''.
export function formValues(filters) {
    const out = {};
    for (const k of Object.keys(EMPTY_FILTERS)) {
        if (filters[k] === undefined) continue;
        let v = String(filters[k]);
        if ((k === 'from' || k === 'to') && v !== '' && !validDateString(v)) v = '';
        out[k] = v;
    }
    return out;
}

// filtersFromParams: the search filters a URL carries (non-empty only), and
// whether it carries any.
export function filtersFromParams(params, fields) {
    const filters = {};
    let any = false;
    for (const f of fields) {
        const v = params.get(f);
        if (v) { filters[f] = v; any = true; }
    }
    return { filters, any };
}

// searchRow: one hub game's result-row cells. Contestants are the top two
// teams for a team mode, the top two players otherwise.
export function searchRow(game) {
    const mode = SEARCH_MODE_LABELS[game.mode] || game.mode || '-';
    const map = game.map || '-';
    const date = (game.timestamp || '').slice(0, 10);
    const isTeamMode = !!game.mode && !SEARCH_INDIVIDUAL_MODES.has(game.mode);
    let contest = '—';
    if (isTeamMode && Array.isArray(game.teams) && game.teams.length >= 2) {
        const t = [...game.teams].sort((a, b) => (b.frags || 0) - (a.frags || 0));
        contest = `${t[0].name} ${t[0].frags} : ${t[1].frags} ${t[1].name}`;
    } else if (Array.isArray(game.players) && game.players.length >= 2) {
        const p = [...game.players].sort((a, b) => (b.frags || 0) - (a.frags || 0));
        contest = `${p[0].name} ${p[0].frags} : ${p[1].frags} ${p[1].name}`;
    } else if (Array.isArray(game.players) && game.players.length === 1) {
        const p = game.players[0];
        contest = `${p.name} ${p.frags || 0}`;
    }
    return { mode, map, contest, tag: game.matchtag || '', date };
}

// nextPaging: the paging state after a page of `count` rows at `offset`
// answered for `filters`. A page without a total keeps the one known before.
export function nextPaging(prev, filters, offset, count, total) {
    return { filters, shown: offset + count, total: total !== null ? total : prev.total };
}

// pagerLabel / hasMore: the footer under the list — "N of M matches", and a
// Load-more button while the hub reports more.
export function pagerLabel({ shown, total }) {
    return total !== null ? `${shown} of ${total} matches` : `${shown} matches`;
}

export function hasMore({ shown, total }) {
    return total !== null && shown < total;
}
