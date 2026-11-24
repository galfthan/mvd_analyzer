import test from 'node:test';
import assert from 'node:assert/strict';
import {
    groupMatches, gameSides, mapWinner, mapScore, matchWith, siblingFilters, mergeGames, matchRow, restartedGames,
    searchMatches, cutShort, matchRestarted, missingMaps,
} from '../src/lib/matches.js';
import { isQwlGame, seriesBySha, mergeSeries, seriesLabel } from '../src/lib/qwl.js';

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

test('matches: a game broken off and restarted is in the match and wins nobody a map', () => {
    // dm2 breaks at 2–0 for sr and starts again two minutes later; fu wins the real one.
    const games = [game(0, 'dm2', 2, 0), game(2, 'dm2', 150, 200), game(27, 'dm3', 180, 120)];
    const [m] = groupMatches(games);
    assert.equal(m.games.length, 3);
    assert.deepEqual([...restartedGames(m.games)], [String(games[0].id)]);
    assert.deepEqual(m.sides, [{ name: 'fu', wins: 1 }, { name: 'sr', wins: 1 }], 'the name order of a level score');
    // A full map apart is no restart.
    assert.equal(restartedGames([game(0, 'dm2', 1, 2), game(20, 'dm3', 1, 2)]).size, 0);
    // Duels are also played to 5 minutes: only a start within 3 is a restart.
    assert.equal(restartedGames([duel(0, 'dm4', 12, 9), duel(6, 'dm4', 10, 11)]).size, 0);
    assert.equal(restartedGames([duel(0, 'dm4', 1, 0), duel(2, 'dm4', 10, 11)]).size, 1);
});

// A QWLeague series over the given games (each given a demo sha), team 1
// being the games' first team.
function seriesOver(games, extra = {}) {
    games.forEach((g, i) => { g.demo_sha256 = `sha${g.id}`; g.server_hostname = 'hamburg.qwleague.com:28501'; });
    return {
        id: 6000 + games[0].id, url: 'https://qwleague.com/match/1', mode: '4on4', kind: 'ladder', bestOf: 3, format: 'bestof',
        endedAt: games[games.length - 1].timestamp,
        maps: games.map((g, i) => ({ map: g.map, matchId: 100 + g.id, sha: g.demo_sha256, frags: [g.teams[0].frags, g.teams[1].frags] })),
        ...extra,
    };
}

test('matches: the maps of a QWLeague series are one match, whatever the rosters and the clock say', () => {
    // Two series between "red" and "blue" back to back, the same players in
    // red: by rosters and times alone they are one match of four maps.
    const o = { ta: 'red', tb: 'blue' };
    const one = [game(0, 'dm2', 200, 150, o), game(25, 'dm3', 210, 100, o)];
    const two = [game(48, 'e1m2', 100, 180, { ...o, b: ['b1', 'b2', 'b3', 'z9'] }), game(72, 'dm2', 90, 150, { ...o, b: ['b1', 'b2', 'b3', 'z9'] })];
    assert.deepEqual(groupMatches([...one, ...two]).map(m => m.games.length), [4]);

    const s1 = seriesOver(one, { score: [2, 0] });
    const s2 = seriesOver(two, { score: [0, 2] });
    const ms = groupMatches([...one, ...two], [s1, s2]);
    assert.deepEqual(ms.map(m => m.games.map(g => g.map)), [['e1m2', 'dm2'], ['dm2', 'dm3']], 'two matches, the newest first');
    assert.equal(ms[0].qwl, s2);
    assert.deepEqual(ms[0].sides, [{ name: 'blue', wins: 2 }, { name: 'red', wins: 0 }]);
    assert.deepEqual(ms[1].sides, [{ name: 'red', wins: 2 }, { name: 'blue', wins: 0 }]);
    assert.deepEqual(cutShort(ms, { map: 'dm2' }, true), [], 'a series names its own maps: no search for more');
});

test('matches: a series\' score and clan names are QWLeague\'s; a quick restart on its servers is still a map', () => {
    // Map two was forfeited (no demo): the hub has map one only, QWLeague
    // scores the series 2-0. The clans are named behind the hub's tags.
    const g1 = game(0, 'dm2', 120, 150, { ta: 'book', tb: '-s-' });
    const s = seriesOver([g1], { kind: 'tournament', forfeit: true, score: [0, 2], teams: [{ name: 'Black Book' }, { name: 'Suddendeath' }],
        tournament: { name: '4on4 · Season 1', division: 'Division 2', stage: 'group', round: 1 } });
    s.maps.push({ map: 'dm3', matchId: 999 });
    const [m] = groupMatches([g1], [s]);
    assert.deepEqual(m.sides, [{ name: '-s-', wins: 2, label: 'Suddendeath' }, { name: 'book', wins: 0, label: 'Black Book' }]);
    assert.deepEqual(missingMaps(m), [], 'a map without a demo is not asked for');
    assert.equal(seriesLabel(s), '4on4 · Season 1 · Division 2 · group, round 1 · Bo3 · forfeit');
    assert.equal(seriesLabel({ ...s, kind: 'ladder', forfeit: false, format: 'fixed', tournament: undefined }), 'ladder · 3 maps');

    // Two maps three minutes apart: a restart by the clock, two maps by the series.
    const quick = [game(100, 'dm2', 150, 100), game(103, 'dm3', 150, 100), game(130, 'e1m2', 1, 2)];
    assert.equal(matchRestarted(groupMatches(quick)[0]).size, 1);
    const qs = seriesOver(quick.slice(0, 2));
    const [other, series] = groupMatches(quick, [qs]);
    assert.equal(matchRestarted(series).size, 0);
    assert.deepEqual(series.sides, [{ name: 'sr', wins: 2 }, { name: 'fu', wins: 0 }], 'both counted');
    assert.equal(other.qwl, undefined);

    // The search returned one map of the series: the other is named by its sha.
    const [part] = searchMatches([quick[1]], [], [qs]);
    assert.deepEqual(missingMaps(part), [quick[0].demo_sha256]);
    assert.deepEqual(searchMatches([quick[1]], [quick[0]], [qs])[0].games.map(g => g.map), ['dm2', 'dm3'], 'in the series\' order');
});

test('qwl: a game on a QWLeague server, and series by demo', () => {
    assert.equal(isQwlGame({ server_hostname: 'Stockholm.QWLeague.com:28501' }), true);
    assert.equal(isQwlGame({ server_hostname: 'qwleague.com' }), true);
    assert.equal(isQwlGame({ server_hostname: 'notqwleague.com:28501' }), false);
    assert.equal(isQwlGame({ server_hostname: 'qwleague.com.example.org:1' }), false);
    assert.equal(isQwlGame({}), false);
    const a = { id: 2, maps: [{ matchId: 1, sha: 'x' }, { matchId: 2 }] };
    const grown = { id: 3, maps: [{ matchId: 1, sha: 'x' }, { matchId: 2, sha: 'y' }, { matchId: 3, sha: 'z' }] };
    assert.deepEqual([...seriesBySha([a]).keys()], ['x']);
    assert.deepEqual(mergeSeries([a], [grown]), [grown], 'a series read again replaces its earlier copy');
});
