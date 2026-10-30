import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
    aimModeText, aimWeaponView, aimHitsValue, aimHitsCell, aimDamageCell, aimDamageText, aimCol, AIM_COL,
    AIM_TABLE_COLS, AIM_TABLE_GROUPS, aimWeaponTables, aimChips, RECON_ANYDAMAGE_NOTE, AIM_MEASURED_ONLY_NOTE,
    AIM_SPLIT_WITHHELD_NOTE,
} from '../src/lib/aim.js';
import {
    aimSampleIndices, aimNodataText, aimDensityGrid, aimDensityRGB, aimHoverTitle, aimHistRow, aimRampHist,
    aimMarginalHist, aimBarLabel, aimOffX, aimOffY,
} from '../src/lib/aim-plot.js';
import { heatColorRGB } from '../src/lib/heat-color.js';
import { hasTeammates, isIndividualLayout, isTeamBasedMode } from '../src/lib/result.js';

const golden = (name) => JSON.parse(readFileSync(new URL(`../../mvd-analytics/testdata/golden/${name}.json`, import.meta.url)));
const TEAM2 = golden('2on2_nani_pora_210426_dm6');
const RECON = golden('2on2_archive_dm4_qw240_recon');
const COLORS = ['#ff5050', '#50a0ff', '#4ecdc4', '#ffc107'];

test('group spans cover every column', () => {
    for (const [wn, groups] of Object.entries(AIM_TABLE_GROUPS)) {
        assert.equal(groups.reduce((s, [, n]) => s + n, 0), AIM_TABLE_COLS[wn].length, wn);
    }
});

test('mode line', () => {
    assert.match(aimModeText({ mode: 'duel' }), /duel \(exact/);
    assert.match(aimModeText({ mode: 'team' }), /nearest-crosshair-enemy/);
});

test('team predicates: layout vs ruleset', () => {
    const ffa = { match: { sources: { teams: 'individual' }, gameMode: { teamBased: false } } };
    const ctfDuel = { match: { sources: { teams: 'individual' }, gameMode: { teamBased: true } } };
    const team = { match: { sources: { teams: 'derived' }, gameMode: { teamBased: true } } };
    assert.equal(hasTeammates(ffa), false);
    assert.equal(hasTeammates(ctfDuel), false);
    assert.equal(isIndividualLayout(ctfDuel) && isTeamBasedMode(ctfDuel), true);
    assert.equal(hasTeammates(team), true);
    assert.equal(hasTeammates(null), false);
});

test('victim projection: absent enemy split rides through, buckets read 0, null direct stays null', () => {
    const w = { weapon: 'rl', shots: 10, hits: 5, direct: 2, splash: 3, missed: 5 };
    assert.equal(aimWeaponView(w, 'all'), w);
    assert.equal(aimWeaponView(w, 'enemy').hits, 5);
    assert.equal(aimWeaponView(w, 'enemy').recon, null);
    const t = aimWeaponView(w, 'team');
    assert.deepEqual([t.hits, t.direct, t.splash, t.missed], [0, 0, 0, 10]);
    const s = aimWeaponView({ ...w, self: { hits: 3, direct: 1 } }, 'self');
    assert.deepEqual([s.hits, s.direct, s.splash, s.missed], [3, 1, 2, 7]);
    const un = aimWeaponView({ weapon: 'gl', shots: 4, hits: 1, direct: null }, 'team');
    assert.deepEqual([un.direct, un.splash], [null, null]);
    // LG misses have no victim: they don't move with the filter.
    assert.equal(aimWeaponView({ weapon: 'lg', shots: 5, miss: 4 }, 'team').miss, 4);
});

test('hits: measured, reconstructed (marked, rl/gl carry the any-path note), absent', () => {
    assert.deepEqual(aimHitsValue({ hits: 3 }), { n: 3, recon: false });
    assert.deepEqual(aimHitsValue({ hitsMeasured: false, recon: { hits: 0 } }), { n: 0, recon: true });
    assert.equal(aimHitsValue({ hitsMeasured: false }), null);
    const rl = aimHitsCell({ weapon: 'rl', hitsMeasured: false, recon: { hits: 7 } }, String);
    assert.match(rl, /^<span class="stat-recon"/);
    assert.ok(rl.includes(RECON_ANYDAMAGE_NOTE.slice(0, 30)));
    assert.ok(!aimHitsCell({ weapon: 'lg', hitsMeasured: false, recon: { hits: 7 } }, String).includes('ANY fire'));
    assert.match(aimHitsCell({ weapon: 'ng', hitsMeasured: false, reconTier: true, filter: 'all' }, String), /covers LG, SG/);
    assert.match(aimHitsCell({ weapon: 'lg', hitsMeasured: false, reconTier: true, filter: 'enemy' }, String), /no victim split/);
    assert.match(aimHitsCell({ weapon: 'lg', hitsMeasured: false, reconTier: false, filter: 'all' }, String), /none could be reconstructed/);
});

test('measured-only columns and the per-row split withhold', () => {
    assert.equal(aimCol(AIM_COL.shots, false), AIM_COL.shots);
    assert.ok(aimCol(AIM_COL.full, false).cell({}).includes('Not measured'));
    assert.ok(AIM_MEASURED_ONLY_NOTE.startsWith('Not measured'));
    assert.ok(AIM_COL.direct.cell({ direct: null }).includes(AIM_SPLIT_WITHHELD_NOTE.slice(0, 20)));
    assert.equal(AIM_COL.direct.sort({ direct: null }), -1);
    assert.equal(AIM_COL.direct.cell({ direct: 0 }), '0');
});

test('damage cell: measured zeros, unmeasured self, lower bound', () => {
    const dmg = { byWeapon: { rl: 100 }, byWeaponTeam: { rl: 20 }, byWeaponSelf: { rl: 5 }, taken: 300 };
    assert.deepEqual(aimDamageCell(dmg, 'rl', 'all'), { n: 125, lower: false, note: '' });
    assert.equal(aimDamageCell(dmg, 'lg', 'enemy').n, 0);
    const noSelf = { ...dmg, taken: null };
    assert.equal(aimDamageCell(noSelf, 'rl', 'self').n, null);
    const lb = aimDamageCell(noSelf, 'rl', 'all');
    assert.deepEqual([lb.n, lb.lower], [120, true]);
    assert.equal(aimDamageText(lb), `<span title="${lb.note}">&ge;120</span>`);
    assert.equal(aimDamageText(aimDamageCell(null, 'rl', 'all')), '<span class="aim-na" title="No damage information for this player on this demo.">-</span>');
});

test('weapon tables off a golden: order, rows by shots, stripes, groups, sortable values', () => {
    const order = ['nAnI', 'pora'];
    const tables = aimWeaponTables(TEAM2, 'all', order, COLORS);
    const fired = new Set(TEAM2.aim.players.flatMap(p => p.weapons.map(w => w.weapon)));
    assert.deepEqual(tables.map(t => t.weapon), ['lg', 'sg', 'ssg', 'rl', 'gl', 'ng', 'sng', 'axe'].filter(w => fired.has(w)));
    const rl = tables.find(t => t.weapon === 'rl').html;
    assert.match(rl, /^<h4>Rocket Launcher<\/h4><table class="stats-table aim-weapon-table"><thead><tr><th rowspan="2">Player<\/th>/);
    assert.match(rl, /<th class="col-group group-start" colspan="6">Shots<\/th>/);
    assert.ok(rl.includes('border-left: 3px solid #ff5050'));
    assert.ok(rl.includes('border-left: 3px solid #50a0ff'));
    // Rows run most shots first.
    const shots = [...rl.matchAll(/<tr style[^>]*><td>[^<]*<\/td><td class="group-start">(\d+)</g)].map(m => +m[1]);
    assert.deepEqual(shots, [...shots].sort((a, b) => b - a));
    assert.equal(aimWeaponTables({}, 'all', order, COLORS).length, 0);
});

test('weapon tables on a reconstructed demo: recon hits, measured-only dashes, Enemy drops recon', () => {
    const all = aimWeaponTables(RECON, 'all', ['korv', 'x'], COLORS).find(t => t.weapon === 'lg').html;
    assert.ok(all.includes('class="stat-recon"'));
    assert.ok(all.includes('Not measured on this demo'));
    const enemy = aimWeaponTables(RECON, 'enemy', ['korv', 'x'], COLORS).find(t => t.weapon === 'lg').html;
    assert.ok(!enemy.includes('class="stat-recon"'));
    assert.ok(enemy.includes('set Victims to All'));
});

test('chips: result.aim order, canonical colour by team', () => {
    const chips = aimChips(TEAM2, ['pora', 'nAnI'], COLORS);
    assert.deepEqual(chips.map(c => c.player), TEAM2.aim.players.map(p => p.player));
    for (const c of chips) {
        const team = TEAM2.aim.players.find(p => p.player === c.player).team;
        assert.equal(c.color, team === 'pora' ? COLORS[0] : COLORS[1]);
    }
    // No canonical order: no colours.
    assert.ok(aimChips(TEAM2, [], COLORS).every(c => c.color === ''));
});

test('samples: weapon + filter, no-data text', () => {
    const c = { t: [0, 1, 2, 3], w: ['lg', 'lg', 'sg', 'lg'], team: [0, 1, 0, 0] };
    assert.deepEqual(aimSampleIndices(c, 'lg', 'all'), [0, 1, 3]);
    assert.deepEqual(aimSampleIndices(c, 'lg', 'enemy'), [0, 3]);
    assert.deepEqual(aimSampleIndices(c, 'lg', 'team'), [1]);
    assert.deepEqual(aimSampleIndices(c, 'lg', 'self'), []);
    assert.deepEqual(aimSampleIndices(null, 'lg', 'all'), []);
    assert.equal(aimNodataText('sg', 'all'), 'No SG fired by this player.');
    assert.match(aimNodataText('lg', 'self'), /no hitscan samples/);
});

test('offsets: enemy-right reads right, above reads up', () => {
    const c = { dyaw: [-10], dpitch: [10], dist: [100] };
    assert.ok(aimOffX(c, 0) > 17 && aimOffX(c, 0) < 18);
    assert.ok(aimOffY(c, 0) > 17);
});

test('density grid: dead-centre sample peaks at the centre, outliers dropped, hover counts', () => {
    const c = { t: [0, 1, 2], w: ['lg', 'lg', 'lg'], dyaw: [0, 0, 80], dpitch: [0, 0, 0], dist: [500, 500, 500], hit: [1, 0, 0] };
    const g = aimDensityGrid(c, [0, 1, 2]);
    assert.deepEqual([g.nx, g.ny, g.hx, g.hy], [96, 64, 24, 16]);
    const peak = g.sm.reduce((bi, v, i, a) => (v > a[bi] ? i : bi), 0);
    assert.deepEqual([peak % g.nx, Math.floor(peak / g.nx)], [48, 32]);
    assert.equal(g.hShots.reduce((a, b) => a + b, 0), 2);
    const d = { ...g, ML: 0, MT: 0, PW: 480, PH: 320 };
    assert.equal(aimHoverTitle(d, 241, 161), 'x 0…+8 qu, y -8…0 qu: 2 shots, 1 hit (50%)');
    assert.equal(aimHoverTitle(d, 5, 5), 'x -96…-88 qu, y +56…+64 qu: no shots');
    assert.equal(aimHoverTitle(d, -1, 5), '');
    assert.equal(aimHoverTitle({ ...d, hitsKnown: false }, 241, 161), 'x 0…+8 qu, y -8…0 qu: 2 shots');
});

test('colour scale: zero keeps the surface, full density is the ramp top', () => {
    const bg = [26, 26, 46];
    assert.deepEqual(aimDensityRGB(0, bg), bg);
    assert.deepEqual(aimDensityRGB(1, bg), heatColorRGB(1));
    assert.deepEqual(heatColorRGB(1), [253, 231, 37]);
    assert.deepEqual(heatColorRGB(-1), [68, 1, 84]);
    assert.equal(aimBarLabel(12.4), '12');
    assert.equal(aimBarLabel(2.44), '2.4');
});

test('marginal histogram: zero-centred odd bins, clamp edges, stub', () => {
    const c = { hit: [1, 0, 0] };
    const vals = [0, 1000, -1000];
    const h = aimMarginalHist('Yaw', 'note', 'qu', 96, 4, i => vals[i], c, [0, 1, 2], 16);
    assert.equal(h.cols.length, 49);
    assert.equal(h.cols[24].title, '-2 … 2 qu: 1 shot, 1 hit (100%)');
    assert.equal(h.cols[48].height, '100%');
    assert.equal(h.cols[0].stub, true);
    assert.equal(h.cols[1].stub, false);
    assert.deepEqual(h.axis, ['−96', '0', '+96 qu']);
    assert.equal(h.band.width, `${32 / 196 * 100}%`);
});

test('ramp: hit % per 100 ms bin under the filter, y scale rounds up, opacity by sample size', () => {
    const ramp = { since: [0, 50, 150, 5000], hit: [1, 0, 1, 1], team: [0, 0, 1, 0] };
    const all = aimRampHist(ramp, 'all');
    assert.equal(all.yMax, 100);
    assert.equal(all.cols[0].title, '0…100 ms: 2 shots, 1 hit (50%)');
    assert.equal(all.cols[11].title, '1100+ ms: 1 shot, 1 hit (100%)');
    assert.equal(all.cols[0].opacity, '1.00');
    assert.equal(all.cols[2].stub, false);
    const enemy = aimRampHist(ramp, 'enemy');
    assert.equal(enemy.cols[1].title, '100…200 ms: 1 shot, 0 hit (0%)');
    assert.deepEqual(all.axis, ['0', '600', '1100+ ms']);
    // The ramp only joins the row where hits are measured.
    const c = { hit: [1], dyaw: [0], dpitch: [0], dist: [100] };
    assert.equal(aimHistRow(c, [0], ramp, 'all').length, 3);
    assert.equal(aimHistRow(c, [0], { since: [1] }, 'all').length, 2);
    assert.deepEqual(aimHistRow(c, [], ramp, 'all'), []);
});
