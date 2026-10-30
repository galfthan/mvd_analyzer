import test from 'node:test';
import assert from 'node:assert/strict';

import { store } from '../src/state/store.js';
import { createClock } from '../src/state/clock.js';
import { teams, setCanonicalTeams, teamIndexOf, colorOf, assignTeamColors, assignPlayerColors, TEAM_PALETTE } from '../src/state/teams.js';

test('store notifies on subscribe and on every set, same value included', () => {
    const s = store(1);
    const seen = [];
    const off = s.subscribe((v) => seen.push(v));
    s.set(1);
    s.set(2);
    off();
    s.set(3);
    assert.deepEqual(seen, [1, 1, 2]);
    assert.equal(s.get(), 3);
});

test('clock clamps to the match and tags the mover', () => {
    const c = createClock();
    const seen = [];
    c.subscribe((tick) => seen.push(tick));
    c.setDuration(100);
    c.seek(150, 'engine');
    c.seek(-3);
    assert.deepEqual(seen.slice(1), [{ t: 100, from: 'engine' }, { t: 0, from: null }]);
    c.setDuration(0);
    c.seek(1e6);
    assert.equal(c.t, 1e6, 'no duration = unclamped above');
});

test('team colours follow the team NAME, not the frag order', () => {
    const a = assignTeamColors(['zeta', 'alpha']);
    const b = assignTeamColors(['alpha', 'zeta']);
    assert.equal(a[1], b[0], 'alpha keeps its colour whoever won');
    assert.deepEqual(assignTeamColors(['x', 'Blue', 'red']).slice(0, 3), [TEAM_PALETTE[2], TEAM_PALETTE[1], TEAM_PALETTE[0]]);
    assert.equal(assignTeamColors(['a']).length, TEAM_PALETTE.length, 'padded to the palette');
    assert.deepEqual(assignPlayerColors(['b', 'a']), [assignPlayerColors(['a', 'b'])[1], assignPlayerColors(['a', 'b'])[0]]);
});

test('setCanonicalTeams maps players to their side', () => {
    setCanonicalTeams(['BhB', 'ahoy'], false, [{ name: 'milton', team: 'ahoy' }, { name: 'milton', team: 'BhB' }]);
    const s = teams.get();
    assert.equal(teamIndexOf(s, 'milton', false), 1, 'first mapping wins');
    assert.equal(colorOf(s, 'milton', false), s.colors[1]);
    assert.equal(teamIndexOf(s, 'nobody', false), -1);
    assert.equal(colorOf(s, 'nobody', false), '#ccc');
    setCanonicalTeams(['p1', 'p2', 'p3'], true);
    assert.equal(teamIndexOf(teams.get(), 'p2', true), 1, 'individual: a player is their own side');
});
