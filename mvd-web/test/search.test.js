import test from 'node:test';
import assert from 'node:assert/strict';

import {
    SEARCH_MODE_LABELS, EMPTY_FILTERS, searchFilters, validDateString, formValues, filtersFromParams,
    searchRow, nextPaging, pagerLabel, hasMore,
} from '../src/lib/search.js';
import { SEARCH_FIELDS } from '../src/data/hub.js';

test('search: the form reads with the text fields trimmed, mode and dates as typed', () => {
    assert.deepEqual(searchFilters({ player: '  nexus ', team: ' ]sr[', map: 'dm2 ', mode: '4on4', tag: ' qwsl ', from: '2024-05-01', to: '' }),
        { player: 'nexus', team: ']sr[', map: 'dm2', mode: '4on4', tag: 'qwsl', from: '2024-05-01', to: '' });
    assert.deepEqual(searchFilters({}), { ...EMPTY_FILTERS });
    assert.deepEqual(Object.keys(searchFilters({})), SEARCH_FIELDS, 'the hub client and the form name the same fields');
});

test('search: an <input type=date> keeps only a real yyyy-mm-dd day', () => {
    for (const ok of ['2024-05-01', '2024-02-29', '2000-02-29', '0001-01-01', '2023-12-31']) assert.equal(validDateString(ok), true, ok);
    for (const bad of ['2024-13-01', '2023-02-29', '1900-02-29', '2024-04-31', '2024-5-1', '24-05-01', '0000-01-01', '2024-05-00', 'yesterday', ''])
        assert.equal(validDateString(bad), false, bad);
});

test('search: URL filters as the form holds them', () => {
    assert.deepEqual(formValues({ map: ' dm2 ', mode: 'bogus', from: '2024-13-45', to: '2024-02-30' }),
        { map: ' dm2 ', mode: 'bogus', from: '', to: '' }, 'a bad date clears; an unknown mode stays (selecting no option)');
    assert.equal(searchFilters({ mode: 'bogus' }).mode, '', 'and reads as no mode');
    assert.deepEqual(formValues({ mode: 'ffa', from: '2024-05-01' }), { mode: 'ffa', from: '2024-05-01' }, 'only the fields given');
    const p = new URLSearchParams('?gameId=1&map=dm2&player=&mode=4on4&t=3');
    assert.deepEqual(filtersFromParams(p, SEARCH_FIELDS), { filters: { map: 'dm2', mode: '4on4' }, any: true });
    assert.deepEqual(filtersFromParams(new URLSearchParams('?gameId=1&player='), SEARCH_FIELDS), { filters: {}, any: false });
});

test('search: a result row names the top two teams, or players in an individual mode', () => {
    const team = {
        id: 7, mode: '4on4', map: 'dm2', timestamp: '2024-05-01T20:00:00+00:00', matchtag: 'qwsl',
        teams: [{ name: 'red', frags: 100 }, { name: 'blue', frags: 150 }, { name: 'x', frags: 1 }],
        players: [{ name: 'a', frags: 9 }, { name: 'b', frags: 30 }],
    };
    assert.deepEqual(searchRow(team), { mode: '4v4', map: 'dm2', contest: 'blue 150 : 100 red', tag: 'qwsl', date: '2024-05-01' });
    assert.equal(searchRow({ ...team, mode: '1on1' }).contest, 'b 30 : 9 a', 'a duel lists players');
    assert.equal(searchRow({ ...team, mode: 'ffa' }).mode, 'FFA');
    assert.equal(searchRow({ ...team, mode: '' }).contest, 'b 30 : 9 a', 'no mode: players');
    assert.equal(searchRow({ ...team, mode: 'race' }).mode, 'race', 'an unknown facet shows raw');
    assert.equal(searchRow({ ...team, teams: [team.teams[0]] }).contest, 'b 30 : 9 a', 'one team: players');
    assert.equal(searchRow({ id: 1, players: [{ name: 'solo' }] }).contest, 'solo 0');
    assert.deepEqual(searchRow({ id: 1 }), { mode: '-', map: '-', contest: '—', tag: '', date: '' });
    for (const k of Object.keys(SEARCH_MODE_LABELS)) assert.ok(SEARCH_MODE_LABELS[k]);
});

test('search: paging counts rows across pages and keeps a known total', () => {
    const f = { map: 'dm2' };
    let p = nextPaging({ filters: null, shown: 0, total: null }, f, 0, 100, 250);
    assert.deepEqual(p, { filters: f, shown: 100, total: 250 });
    assert.equal(pagerLabel(p), '100 of 250 matches');
    assert.equal(hasMore(p), true);
    p = nextPaging(p, f, 100, 100, null);
    assert.deepEqual(p, { filters: f, shown: 200, total: 250 }, 'a page without a total keeps the last one');
    p = nextPaging(p, f, 200, 50, 250);
    assert.equal(hasMore(p), false);
    assert.equal(pagerLabel({ shown: 3, total: null }), '3 matches');
    assert.equal(hasMore({ shown: 3, total: null }), false);
});
