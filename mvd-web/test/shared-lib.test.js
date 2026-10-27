import test from 'node:test';
import assert from 'node:assert/strict';

import { firstSortDir, nextSortDir, compareSortText } from '../src/lib/sortable.js';
import { fragsOfRow, sortByFragsDesc, teamOrder, holdCell, playerStatsRows } from '../src/lib/player-stats.js';
import { hubReplayUrl } from '../src/lib/hub-url.js';
import { escapeHtml } from '../src/lib/html.js';
import { parseHubReplayUrl } from '../src/lib/playlist.js';

test('sort: numeric columns start descending, text ascending; clicks flip', () => {
    assert.equal(firstSortDir(['', '-', '12s']), 'desc');
    assert.equal(firstSortDir(['-', 'abc', '3']), 'asc');
    assert.equal(firstSortDir([]), 'asc');
    assert.equal(nextSortDir('asc', ['1']), 'desc');
    assert.equal(nextSortDir('desc', ['a']), 'asc');
    const nums = ['3.5%', '12 (30s)', '42'];
    assert.deepEqual([...nums].sort((a, b) => compareSortText(a, b, 'desc')), ['42', '12 (30s)', '3.5%']);
    assert.deepEqual(['b', 'A', 'c'].sort((a, b) => compareSortText(a, b, 'asc')), ['A', 'b', 'c']);
});

test('player-stats readers', () => {
    assert.equal(fragsOfRow({ score: { frags: 3 }, stats: { frags: 9 } }), 3);
    assert.equal(fragsOfRow({ stats: { frags: 9 } }), 9);
    assert.equal(fragsOfRow(null), 0);
    const ps = [{ name: 'a', stats: { frags: 1 } }, { name: 'b', stats: { frags: 5 } }, { name: 'c', stats: { frags: 1 } }];
    assert.deepEqual(sortByFragsDesc(ps).map(p => p.name), ['b', 'a', 'c'], 'stable');
    assert.deepEqual(teamOrder(['x', 'y'], []), ['x', 'y']);
    assert.deepEqual(teamOrder(['x'], [{ team: 'b' }, { team: '' }, { team: 'a' }, { team: 'b' }]), ['b', 'a']);
    assert.deepEqual(playerStatsRows({}), []);
    assert.equal(holdCell(null), '<td data-sort-value="0">-</td>');
    assert.equal(holdCell({ ms: 1500, shareAlive: 0.25, runs: 1, longestMs: 1400 }, { aliveMs: 10, presentMs: 30, matchMs: 20 }),
        '<td data-sort-value="1500" title="held 1500 ms · 25% of alive time · 1 run · longest 1s · window alive 10 / present 30 / match 20 ms, member-summed">2s</td>');
});

test('hubReplayUrl round-trips through parseHubReplayUrl; escapeHtml', () => {
    const url = hubReplayUrl({ gameId: 9, from: 10, to: 20, track: 4 });
    assert.equal(url, 'https://hub.quakeworld.nu/games/?gameId=9&from=10&to=20&track=4');
    assert.deepEqual(parseHubReplayUrl(url), { gameId: 9, from: 10, to: 20, track: 4 });
    assert.equal(hubReplayUrl({ gameId: 9, from: 10, track: 4 }), 'https://hub.quakeworld.nu/games/?gameId=9&from=10&track=4');
    assert.equal(escapeHtml(`<a href="x">'&`), '&lt;a href=&quot;x&quot;&gt;&#39;&amp;');
    assert.equal(escapeHtml(null), '');
});
