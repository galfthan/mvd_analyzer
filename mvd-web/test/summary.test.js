import test from 'node:test';
import assert from 'node:assert/strict';

import {
    matchInfo, matchSettings, serverInfoRows, teamsSummary, teamsPanelHidden, noMatchView,
    effPct, enemyWeaponKillCell, enemyKillCell, basicStatsPlayerRows, basicStatsTeamRows,
    weaponStatsPlayerRows, weaponStatsTeamRows, accConventionNote, itemsPlayerRows, formatXfer,
    weaponKillBars, getWeaponName, openingRows,
} from '../src/lib/summary.js';
import {
    ktxHitsConvention, formatAccuracyCell, accOffKtxScale, anyAccOffKtxScale, ACC_OFF_SCALE_FOOTNOTE,
} from '../src/lib/accuracy.js';
import { formatUtcStamp, formatAccuracyMs, matchWallClock, matchDateHtml } from '../src/lib/wall-clock.js';
import { teamFragTotals, teamRowsInColourOrder } from '../src/lib/player-stats.js';

test('match info: demoInfo, then match, then dashes', () => {
    assert.deepEqual(matchInfo({ demoInfo: { map: 'dm3', duration: 600, mode: '4on4', hostname: 'h' }, match: { mapTitle: 'The Abandoned Base' } }),
        { map: 'dm3', mapTitle: 'The Abandoned Base', duration: '10:00', mode: '4on4', hostname: 'h' });
    assert.deepEqual(matchInfo({ match: { map: 'dm2', duration: 61500 } }),
        { map: 'dm2', mapTitle: '', duration: '1:01', mode: '-', hostname: '-' });
    assert.deepEqual(matchInfo({}), { map: '-', mapTitle: '', duration: '-', mode: '-', hostname: '-' });
});

test('match settings: skips empty/zero cells, collects pills, hides when empty', () => {
    assert.equal(matchSettings(undefined), null);
    const v = matchSettings({ mode: 'duel', teamplay: 0, timelimit: 10, spawnmodel: 'KT2', spawnK: 4, overtime: 'sd', midair: true, noweapon: 'gl' });
    assert.deepEqual(v.cells.map(c => [c.label, c.value]), [['Mode', 'duel'], ['Timelimit', '10 min'], ['Spawnmodel', 'KT2'], ['Overtime', 'sudden death']]);
    assert.equal(v.cells[2].title, 'KTX2 respawns (k_spw=4)');
    assert.deepEqual(v.modifiers.map(m => m.label), ['Midair', 'Noweapon: gl']);
    assert.equal(v.shown, true);
    assert.equal(matchSettings({ teamplay: 0 }).shown, false);
});

test('server info: star keys last, epoch decoded, escaped', () => {
    assert.equal(serverInfoRows({}), null);
    const rows = serverInfoRows({ '*version': 'mvdsv', b: '<x>', a: '1', empty: '', epoch: '0' });
    assert.deepEqual(rows, [
        '<td><code>a</code></td><td>1</td>',
        '<td><code>b</code></td><td>&lt;x&gt;</td>',
        '<td><code>epoch</code></td><td>0 (1970-01-01 00:00:00 UTC)</td>',
        '<td><code>*version</code></td><td>mvdsv</td>',
    ]);
});

test('teams box: canonical order, unknown bucket, fallbacks', () => {
    const ps = { playerStats: { players: [
        { name: 'a', team: 'red', score: { frags: 5 } }, { name: 'b', team: 'blue', score: { frags: 9 } }, { name: 'c', score: { frags: 1 } },
    ] } };
    assert.deepEqual(teamsSummary(ps, ['red', 'blue']), [{ name: 'red', frags: 5 }, { name: 'blue', frags: 9 }, { name: 'unknown', frags: 1 }]);
    assert.deepEqual(teamsSummary(ps, []).map(t => t.name), ['blue', 'red', 'unknown']);
    assert.deepEqual(teamsSummary({ match: { teams: [{ name: 'x', frags: 1 }, { name: 'y', frags: 3 }] } }, ['x', 'y']).map(t => t.name), ['x', 'y']);
    assert.deepEqual(teamsSummary({ match: { teams: [{ name: 'x', frags: 1 }, { name: 'y', frags: 3 }] } }, []).map(t => t.name), ['y', 'x']);
    assert.deepEqual(teamsSummary({}, []), []);
    assert.equal(teamsPanelHidden({ noMatch: { reason: 'noPlayRecorded' } }), true);
    assert.equal(teamsPanelHidden({ noMatch: { reason: 'midMatchRecording' }, match: { teams: [{ name: 'x' }] } }), false);
    assert.equal(teamsPanelHidden({}), false);
});

test('player-stats team helpers', () => {
    assert.deepEqual(teamFragTotals([{ team: 'a', score: { frags: 2 } }, { stats: { frags: 3 } }], 'unknown'), { a: 2, unknown: 3 });
    const rows = [{ name: 'b' }, { name: 'z' }, { name: 'a' }];
    assert.deepEqual(teamRowsInColourOrder(rows, ['a', 'b']).map(t => t.name), ['a', 'b', 'z'], 'unknown teams follow, never dropped');
});

test('score cells: absent is "-", measured zero prints', () => {
    assert.equal(effPct({}), '-');
    assert.equal(effPct({ efficiency: 0.5 }), '50.0%');
    assert.equal(enemyWeaponKillCell({}, 'rl'), '-');
    assert.equal(enemyWeaponKillCell({ kills: 3, byEnemyWeapon: { rl: 1, both: 2 } }, 'rl'), 3);
    assert.equal(enemyKillCell({ score: { kills: 1 } }, 'rl'), '<td>-</td>');
    assert.equal(enemyKillCell({ score: { kills: 1, byWeaponVsEnemyWeapon: { rl: { sg: 2 } } } }, 'rl'), '<td title="Victim was holding — sg only: 2">0</td>');
});

test('basic stats rows: frag order, colour index, handicap column', () => {
    const rows = [
        { name: 'a', team: 'red', score: { frags: 1 } },
        { name: 'b', team: 'blue', score: { frags: 7 }, handicap: 50, bot: { skill: 10 } },
    ];
    const v = basicStatsPlayerRows(rows, ['blue', 'red']);
    assert.equal(v.showHandicap, true);
    assert.deepEqual(v.rows.map(r => r.colorIdx), [0, 1]);
    assert.match(v.rows[0].html, /<td>b <span class="bot-badge" title="Frogbot, skill 10">BOT 10<\/span><\/td>/);
    assert.match(v.rows[1].html, /<td class="handicap-col">-<\/td>/);
    assert.equal(basicStatsPlayerRows([{ name: 'x', handicap: 100 }], []).showHandicap, false);
    assert.match(basicStatsPlayerRows([{ name: 'x' }], []).rows[0].html, /<td class="handicap-col" style="display: none;">-<\/td>/);
    const t = basicStatsTeamRows([{ name: 'red', score: { frags: 1 }, members: 4 }, { name: 'blue' }], ['blue', 'red']);
    assert.deepEqual(t.map(r => r.colorIdx), [0, 1]);
    assert.match(t[1].html, /<td>red<\/td>/);
});

test('weapon stats rows: damage order unless no damage family', () => {
    const rows = [{ name: 'a', team: 't', score: { frags: 9 } }, { name: 'b', team: 't', score: { frags: 1 }, damage: { given: 5 } }];
    assert.deepEqual(weaponStatsPlayerRows(rows, []).map(r => r.html.slice(0, 10)), ['<td>b</td>', '<td>a</td>']);
    assert.deepEqual(weaponStatsPlayerRows(rows.map(r => ({ ...r, damage: undefined })), []).map(r => r.html.slice(0, 10)), ['<td>a</td>', '<td>b</td>']);
    assert.equal(weaponStatsTeamRows([{ name: 't' }], ['t'])[0].html.split('<td>').length - 1, 1 + 6 * 4);
});

test('accuracy cell: withheld hits, off-scale mark, recon grade', () => {
    assert.equal(ktxHitsConvention('ssg'), 'pellets');
    assert.equal(ktxHitsConvention('gl'), 'directImpact');
    assert.equal(ktxHitsConvention('lg'), 'anyDamage');
    assert.equal(formatAccuracyCell(null, 'lg'), '-');
    assert.match(formatAccuracyCell({ byWeapon: { sng: { attacks: 40 } } }, 'sng'), /40 atk/);
    const ktx = formatAccuracyCell({ src: 'ktx', byWeapon: { rl: { attacks: 10, hits: 5, hitsConvention: 'directImpact' } } }, 'rl');
    assert.match(ktx, /^<span title="Hits = projectiles[^"]*">50\.0%<\/span>$/);
    const recon = formatAccuracyCell({ src: 'reconstructed', byWeapon: { sg: { attacks: 4, hits: 1, hitsConvention: 'anyDamage' } } }, 'sg');
    assert.match(recon, /^<span class="stat-recon"><span title="[^"]*Not on the server&#39;s own scale[^"]*Hits RECONSTRUCTED[^"]*">25\.0%<sup class="acc-off-scale">≠<\/sup><\/span><\/span>$/);
    assert.equal(accOffKtxScale({}, 'sg'), false);
    assert.equal(anyAccOffKtxScale([{ accuracy: { byWeapon: { ng: { attacks: 1, hits: 1, hitsConvention: 'pellets' } } } }]), false, 'ng has no column');
    assert.equal(accConventionNote([{ accuracy: { byWeapon: { sg: { attacks: 1, hits: 1, hitsConvention: 'anyDamage' } } } }], []), ACC_OFF_SCALE_FOOTNOTE);
    assert.equal(accConventionNote([], []), '');
});

test('items rows and xfer', () => {
    assert.equal(formatXfer(undefined), '-');
    assert.equal(formatXfer({ xfer: 2 }), '2');
    assert.equal(formatXfer({ xfer: 2, xferSelf: 1 }), '<span title="2 to a teammate, 1 self-recovered">3</span>');
    const [r] = itemsPlayerRows([{ name: 'p', team: 'x', pickups: { byKind: { ra: { took: 3 } } } }], ['x']);
    assert.equal(r.colorIdx, 0);
    assert.match(r.html, /<td class="group-start">3<\/td>\s*<td data-sort-value="0">-<\/td>/);
});

test('kills by weapon bars', () => {
    assert.equal(weaponKillBars(undefined), null);
    assert.deepEqual(weaponKillBars({ rl: 10, tele: 5 }), [{ name: 'Rocket Launcher', count: 10, pct: 100 }, { name: 'Telefrag', count: 5, pct: 50 }]);
    assert.equal(getWeaponName('xyz'), 'xyz');
});

test('wall clock: UTC, precision by accuracy, hedged grades', () => {
    assert.equal(formatUtcStamp(Date.UTC(2002, 7, 4, 11, 44, 55)), '2002-08-04 11:44 UTC');
    assert.equal(formatUtcStamp(Date.UTC(2002, 7, 4, 11, 44, 55), 14 * 3600000), '2002-08-04 UTC ±14 h');
    assert.equal(formatAccuracyMs(1000), '1 s');
    assert.equal(formatAccuracyMs(5), '5 ms');
    assert.equal(matchWallClock({}), null);
    const g = { matchStartUnixMs: Date.UTC(2002, 7, 4, 11, 44), matchStartSource: 'demoDate', matchStartConfidence: 'contradicted' };
    assert.equal(matchWallClock({ streams: { global: g } }).text, '~2002-08-04 11:44 UTC (disputed)');
    assert.match(matchWallClock({ streams: { global: { ...g, matchStartConfidence: 'exact' } } }).html, /^<span title="[^"]*">2002-08-04 11:44 UTC<\/span>$/);
    assert.equal(matchDateHtml({ demoInfo: { date: '2026-04-23 <x>' }, streams: { global: g } }), '2026-04-23 &lt;x&gt;');
    assert.equal(matchDateHtml({}), '-');
});

test('no match view: title, reading, evidence, errors only on demoUnreadable', () => {
    assert.equal(noMatchView({}), null);
    const v = noMatchView({ noMatch: { reason: 'noMatchDeclared', detail: 'd', gameDir: 'fortress', dateMarkers: [{ unixMs: 0 }, { unixMs: Date.UTC(2003, 0, 2, 3, 4), source: 'finalscores', raw: 'r' }] }, errors: ['e'] });
    assert.equal(v.title, 'No match was declared in this recording');
    assert.match(v.reading, /no managed match was declared .* This looks like a fortress demo/);
    assert.deepEqual(v.evidence, [
        ['Status at demo open', 'not sent'], ['Match clock ever seen running', 'no'], ['Game dir', 'fortress'],
        ['Kills in the frag log', '0'], ['Date marker on the wire', '2003-01-02 03:04 UTC (finalscores)', 'r'],
    ]);
    assert.equal(v.errors, '');
    assert.equal(noMatchView({ noMatch: { reason: 'demoUnreadable' }, errors: ['a', 'b'] }).errors, 'Reader: a · b');
    assert.equal(noMatchView({ noMatch: { reason: 'other' } }).title, 'This recording holds no analyzable match');
});

test('openingRows: a row per player in canonical team order, spawn and first takes', () => {
    assert.deepEqual(openingRows(/** @type {any} */ ({}), ['a', 'b']), [], 'no opening, no rows');
    const rows = openingRows(/** @type {any} */ ({
        opening: {
            players: [{ name: 'x', team: 'a', loc: 'RL' }, { name: 'y', team: 'b', loc: 'YA' }, { name: 'w', team: 'b' }],
            firstTakes: [
                { item: 'ya', kind: 'ya', time: 1372, takenBy: 'y', team: 'b', loc: 'YA' },
                { item: 'quad', kind: 'quad', time: 2177, takenBy: 'y', team: 'b' },
                { item: 'rl', kind: 'rl', time: 3039, takenBy: 'late', team: 'a' },
            ],
        },
    }), ['b', 'a']);
    assert.deepEqual(rows.map((r) => [r.html.match(/<td>([^<]*)<\/td>/)[1], r.colorIdx]), [['w', 0], ['y', 0], ['late', 1], ['x', 1]]);
    assert.match(rows[1].html, /<td>YA<\/td>.*YA <span class="muted">0:01<\/span>.* · .*Quad <span class="muted">0:02/);
    assert.match(rows[2].html, /<td>-<\/td>.*RL/, 'a first take by someone not at the start still gets a row');
});
