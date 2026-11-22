import test from 'node:test';
import assert from 'node:assert/strict';
import {
    groupMatches, gameSides, mapWinner, mapScore, matchWith, siblingFilters, mergeGames, matchRow,
    searchMatches, cutShort,
} from '../src/lib/matches.js';

// A 4on4 hub row: `a` and `b` are the two teams' four players, `at` minutes
// after 20:00.
let nextId = 1;
function game(at, map, fa, fb, { a = ['a1', 'a2', 'a3', 'a4'], b = ['b1', 'b2', 'b3', 'b4'], ta = 'sr', tb = 'fu', mode = '4on4', tag = null } = {}) {
    const ts = new Date(Date.UTC(2026, 10, 21, 20, 0) + at * 60000).toISOString();
    return {
        id: nextId++, timestamp: ts, mode, map, matchtag: tag,
        teams: [{ name: ta, frags: fa }, { name: tb, frags: fb }],
        players: [...a.map(name => ({ name, team: ta, frags: 1 })), ...b.map(name => ({ name, team: tb, frags: 1 }))],
    };
}
function duel(at, map, fa, fb, a = 'bps', b = 'milton') {
    const ts = new Date(Date.UTC(2026, 10, 21, 20, 0) + at * 60000).toISOString();
    return { id: nextId++, timestamp: ts, mode: '1on1', map, matchtag: null, teams: [], players: [{ name: a, team: 'x', frags: fa }, { name: b, team: 'y', frags: fb }] };
}

test('matches: maps played in a row by the same two sides are one match, scored in maps', () => {
    const games = [game(0, 'dm2', 200, 150), game(25, 'dm3', 120, 180), game(52, 'e1m2', 210, 90, { tag: 'qwsl' })];
    const [m] = groupMatches([games[2], games[0], games[1]]);
    assert.deepEqual(m.games.map(g => g.map), ['dm2', 'dm3', 'e1m2'], 'in the order played');
    assert.equal(m.id, games[0].id);
    assert.deepEqual(m.sides, [{ name: 'sr', wins: 2 }, { name: 'fu', wins: 1 }]);
    assert.deepEqual(matchRow(m), { tag: 'qwsl', date: '2026-11-21' });
    assert.equal(matchWith([m], games[1].id), m);
    assert.equal(matchWith([m], 'nope'), undefined);
});

test('matches: the leader is named first, a draw is counted for neither', () => {
    const [m] = groupMatches([game(0, 'dm2', 100, 150), game(25, 'dm3', 100, 100), game(50, 'e1m2', 90, 210)]);
    assert.deepEqual(m.sides, [{ name: 'fu', wins: 2 }, { name: 'sr', wins: 0 }]);
    assert.equal(mapWinner(m.games[1]), null);
    assert.deepEqual(mapScore(m.games[0], 'fu'), { own: 150, other: 100 });
});

test('matches: a long break, another roster or other team names start a new match', () => {
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(35, 'dm3', 1, 2)]).length, 1, '20 min of play + 15 of break');
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(36, 'dm3', 1, 2)]).length, 2);
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(25, 'dm3', 1, 2, { a: ['a1', 'a2', 'a3', 'x'] })]).length, 1, 'three of four stayed');
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(25, 'dm3', 1, 2, { a: ['a1', 'a2', 'x', 'y'] })]).length, 2);
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(25, 'dm3', 1, 2, { tb: 'other' })]).length, 2);
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(25, 'dm3', 1, 2, { ta: 'SR ' })]).length, 1, 'names fold case and blanks');
    assert.equal(groupMatches([game(0, 'dm2', 1, 2), game(25, 'dm3', 1, 2, { mode: '2on2' })]).length, 2);
});

test('matches: two matches at once on different servers stay apart, newest first', () => {
    const other = { a: ['c1', 'c2', 'c3', 'c4'], b: ['d1', 'd2', 'd3', 'd4'], ta: 'red', tb: 'blue' };
    const ms = groupMatches([game(0, 'dm2', 1, 2), game(5, 'dm2', 3, 1, other), game(25, 'dm3', 1, 2), game(30, 'dm3', 3, 1, other)]);
    assert.deepEqual(ms.map(m => [m.sides[0].name, m.games.length]), [['red', 2], ['fu', 2]]);
    // The same two names on both servers: pickups called red and blue.
    const here = { ta: 'red', tb: 'blue' };
    const pickups = groupMatches([game(0, 'dm2', 1, 2, here), game(5, 'dm2', 3, 1, other), game(25, 'dm3', 1, 2, here), game(30, 'dm3', 3, 1, other)]);
    assert.deepEqual(pickups.map(m => m.games.length), [2, 2]);
    assert.deepEqual(pickups.map(m => m.games.map(g => g.map)), [['dm2', 'dm3'], ['dm2', 'dm3']]);
});

test('matches: a duel is its two players; a team of two needs both', () => {
    const [m, lone] = groupMatches([duel(0, 'dm4', 20, 10), duel(12, 'dm6', 5, 9), duel(24, 'aerowalk', 7, 3), duel(-60, 'dm2', 1, 0, 'bps', 'other')]);
    assert.deepEqual(m.sides, [{ name: 'bps', wins: 2 }, { name: 'milton', wins: 1 }]);
    assert.equal(lone.games.length, 1);
    const two = { a: ['a1', 'a2'], b: ['b1', 'b2'], mode: '2on2' };
    assert.equal(groupMatches([game(0, 'dm4', 1, 2, two), game(12, 'dm6', 1, 2, two)]).length, 1);
    assert.equal(groupMatches([game(0, 'dm4', 1, 2, two), game(12, 'dm6', 1, 2, { ...two, a: ['a1', 'x'] })]).length, 2);
});

test('matches: a game without two sides is a match of its own', () => {
    const ffa = { id: 900, timestamp: '2026-11-21T20:00:00+00:00', mode: 'ffa', map: 'dm6', players: [{ name: 'a', team: '' }, { name: 'b', team: '' }, { name: 'c', team: '' }] };
    assert.equal(gameSides(ffa), null);
    assert.equal(gameSides({ id: 1, timestamp: 'x' }), null);
    const [m] = groupMatches([ffa]);
    assert.equal(m.sides, null);
    assert.equal(siblingFilters(ffa), null);
});

test('matches: bots are no side; the hub team totals win over the roster sum', () => {
    const g = game(0, 'dm2', 200, 150);
    g.players.push({ name: 'bot', team: 'bots', frags: 0, is_bot: true });
    const sides = gameSides(g);
    assert.deepEqual(sides.map(s => [s.name, s.frags]), [['fu', 150], ['sr', 200]]);
});

test('matches: the search for a game\'s other maps names its mode, sides and days', () => {
    assert.deepEqual(siblingFilters(game(0, 'dm2', 1, 2)), { mode: '4on4', team: 'fu,sr', from: '2026-11-21', to: '2026-11-22' });
    assert.deepEqual(siblingFilters(duel(-900, 'dm2', 1, 2)), { mode: '1on1', player: 'bps,milton', from: '2026-11-20', to: '2026-11-21' });
    assert.equal(siblingFilters(game(0, 'dm2', 1, 2, { ta: 'a,b' })), null, 'a comma cannot go in the list');
});

test('matches: the grouped search list', () => {
    const [g1, g2, g3] = [game(0, 'dm2', 2, 1), game(25, 'dm3', 2, 1), game(50, 'e1m2', 1, 2)];
    const stranger = game(300, 'dm2', 1, 2, { ta: 'x', tb: 'y' });
    // The search found only the dm3 game; the other maps came later.
    assert.deepEqual(searchMatches([g2], []).map(m => m.games.length), [1]);
    const ms = searchMatches([g2], [g1, g2, g3, stranger]);
    assert.deepEqual(ms.map(m => m.games.map(g => g.map)), [['dm2', 'dm3', 'e1m2']], 'whole, and nothing the search did not find');
    assert.equal(mergeGames([g1], [g1, g2]).length, 2);

    const list = groupMatches([g1, g2, stranger]);
    assert.deepEqual(cutShort(list, {}, false), []);
    assert.deepEqual(cutShort(list, {}, true), [list[1]], 'the oldest may continue on the next page');
    assert.deepEqual(cutShort(list, { map: 'dm3' }, false), list);
    // g1 starts 20:00 on the 21st: within a map's reach of neither bound of that day…
    assert.deepEqual(cutShort(groupMatches([g1, g2]), { from: '2026-11-21', to: '2026-11-21' }, false), []);
    // …and well inside it of a `to` that ends at midnight after a 23:40 start.
    const late = groupMatches([game(220, 'dm2', 1, 2)]);
    assert.deepEqual(cutShort(late, { to: '2026-11-21' }, false), late);
    assert.deepEqual(cutShort(late, { from: '2026-11-21' }, false), []);
});
