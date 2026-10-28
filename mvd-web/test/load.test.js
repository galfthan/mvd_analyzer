// The load orchestration's DOM-free parts: the canonical team order, the
// top bar line and the URL state.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { canonicalTeamOrder, fallbackTeamOrder, canonicalPlayers } from '../src/lib/canonical-teams.js';
import { topbarView } from '../src/lib/topbar.js';
import { urlQuery, parseSeg, parseTime, resolveTabName } from '../src/lib/url-state.js';
import { isMultiPlayerIndividual } from '../src/lib/result.js';
import { SEARCH_FIELDS } from '../src/data/hub.js';

const golden = (name) => JSON.parse(readFileSync(new URL(`../../mvd-analytics/testdata/golden/${name}.json`, import.meta.url)));
const DUEL = golden('1on1_42_ahemlockslie_240426_skull');
const RECON = golden('2on2_archive_dm4_qw240_recon');
const FFA = golden('ffa_countdown_dm6_260106');
const NOMATCH = golden('4on4_blue_red_200626_e1m2_sameslot_rejoin');

test('canonical order: frag-sorted teams, players in an individual layout', () => {
    assert.deepEqual(canonicalTeamOrder(RECON), { order: ['nme', 'korv'], perPlayer: false });
    assert.deepEqual(canonicalTeamOrder(DUEL), { order: ['42', 'AHemlocksLie'], perPlayer: false }, 'a duel keeps the team palette');
    assert.deepEqual(canonicalTeamOrder(FFA), { order: ['keith', 'rdleifriaf', 'Polish Rifle'], perPlayer: true });
    assert.equal(isMultiPlayerIndividual(FFA), true);
    assert.equal(isMultiPlayerIndividual(DUEL), false);
    assert.deepEqual(canonicalTeamOrder(NOMATCH).order, []);
    assert.equal(fallbackTeamOrder(NOMATCH), null);
    assert.deepEqual(fallbackTeamOrder(RECON), { order: ['korv', 'nme'], perPlayer: false }, "the demo's own list, unsorted");
    assert.ok(canonicalPlayers(RECON).every(p => p.name && p.team));
});

test('canonical order: without playerStats the teams sort by demoInfo frags', () => {
    const r = {
        match: { sources: { teams: 'serverinfo' } },
        demoInfo: { teams: ['a', 'b'], players: [{ name: 'x', team: 'a', stats: { frags: 3 } }, { name: 'y', team: 'b', stats: { frags: 8 } }] },
    };
    assert.deepEqual(canonicalTeamOrder(r), { order: ['b', 'a'], perPlayer: false });
    assert.deepEqual(canonicalTeamOrder({ match: { teams: [{ name: 'p' }, { name: 'q' }] } }).order, ['p', 'q']);
    assert.deepEqual(canonicalTeamOrder({ match: { sources: { teams: 'individual' }, players: [{ name: 'm', frags: 1 }, { name: 'n', frags: 4 }] } }).order, ['n', 'm']);
});

test('topbar: a matchup, a field, a demo without teams', () => {
    const recon = topbarView(RECON, ['nme', 'korv']);
    assert.match(recon.html, /^<span class="topbar-team-a">nme<\/span> <span class="topbar-vs">vs<\/span> <span class="topbar-team-b">korv<\/span><span class="topbar-sep">·<\/span><span class="topbar-score">59 - 49<\/span><span class="topbar-sep">·<\/span><span class="topbar-map">dm4<\/span><span class="topbar-sep">·<\/span><span class="topbar-date"><span class="date-unverified"/);
    assert.equal(recon.title, 'MVD | nme korv dm4');
    const ffa = topbarView(FFA, canonicalTeamOrder(FFA).order);
    assert.match(ffa.html, /^<span class="topbar-team-a">keith<\/span> <span class="topbar-score">39<\/span><span class="topbar-sep">·<\/span><span class="topbar-field">3 players<\/span>/);
    assert.equal(ffa.title, 'MVD | keith dm6');
    const none = topbarView(NOMATCH, []);
    assert.ok(!none.html.includes('topbar-team-a'));
    assert.equal(none.title, 'MVD Analyzer');
    assert.equal(topbarView(RECON, []).title, 'MVD | korv nme dm4', "no order: the demo's own team list");
    assert.equal(topbarView({}, []).html, '');
});

test('url state: what is written, and the deep-link readers', () => {
    const demo = { gameId: 212260, activeTab: 'loc-graph', learn: true, t: 12.6, seg: { start: 30.2, end: 90.7 } };
    assert.equal(urlQuery(demo, null, SEARCH_FIELDS), 'gameId=212260&tab=locs-regions&learn=1&t=13&seg=30-91');
    assert.equal(urlQuery({ activeTab: 'summary', t: 0, seg: null }, null, SEARCH_FIELDS), '', 'Summary at 0 keeps nothing');
    assert.equal(urlQuery(null, { player: 'nexus', map: '', mode: '4on4' }, SEARCH_FIELDS), 'player=nexus&mode=4on4');
    assert.equal(urlQuery({ gameId: 7, activeTab: 'search' }, { map: 'dm2' }, SEARCH_FIELDS), 'gameId=7&tab=search&map=dm2');
    assert.deepEqual(parseSeg('30-90'), { start: 30, end: 90 });
    for (const bad of [null, '', '5', 'a-b']) assert.equal(parseSeg(bad), null, String(bad));
    assert.equal(parseTime('42.5'), 42.5);
    assert.equal(parseTime('x'), null);
    assert.equal(parseTime(null), null);
    assert.equal(resolveTabName('locs-regions'), 'loc-graph');
    assert.equal(resolveTabName('map'), 'map');
});
