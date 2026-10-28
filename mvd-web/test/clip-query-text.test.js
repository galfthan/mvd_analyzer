import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';

import { PRESETS } from '../src/lib/clip-query.js';

// The keep line is parsed by jsep, an npm dependency: skipped where `npm ci`
// has not run.
const installed = existsSync(new URL('../node_modules/jsep/package.json', import.meta.url));
const { parseQueryText, queryToText } = installed ? await import('../src/lib/clip-query-text.js') : /** @type {any} */ ({});
const skip = !installed && 'jsep not installed (npm ci in mvd-web)';

test('every preset prints and parses back to itself', { skip }, () => {
    for (const p of PRESETS) {
        const text = queryToText(p.query, p.title);
        const r = parseQueryText(text);
        assert.equal(r.error, undefined, `${p.id}: ${r.error}\n${text}`);
        assert.deepEqual(r.query, p.query, p.id);
        assert.equal(r.title, p.title);
    }
});

test('a query with every line round-trips', { skip }, () => {
    const query = {
        source: 'top-windows',
        params: { metric: 'frags', windowMs: '30000', players: 'a b, c' },
        where: { and: [{ field: 'score', op: '>=', value: 3 }, { not: { field: 'player', op: '~', value: 'x' } }] },
        rank: { by: 'score', desc: false, limit: 4 },
        clip: [{ from: 'start', before: 2500, to: 'end', after: 0, pov: 'player', label: 'a "q" {player}' }, { before: -1000 }, {}],
        label: '{player} · {score}',
    };
    const text = queryToText(query, 'Mine');
    assert.deepEqual(parseQueryText(text), { query, title: 'Mine' }, text);
    assert.deepEqual(parseQueryText(queryToText({ source: 'lives', rank: { limit: 3 } })).query, { source: 'lives', rank: { limit: 3 } }, 'first N without a field');
});

test('the form is forgiving where it can be', { skip }, () => {
    const r = parseQueryText(`
# my query
source /backpacks weapons=rl
keep pickerRelation = enemy
   and not pickerHadWeapon
order first 2
`);
    assert.deepEqual(r.query, {
        source: 'backpacks', params: { weapons: 'rl' },
        where: { and: [{ field: 'pickerRelation', op: '=', value: 'enemy' }, { not: { field: 'pickerHadWeapon', op: '=', value: true } }] },
        rank: { limit: 2 },
    });
    assert.deepEqual(parseQueryText('source lives\norder kills').query.rank, { by: 'kills', desc: true }, 'order defaults to highest first');
});

test('what it cannot read is an error naming the line', { skip }, () => {
    for (const [bad, want] of [
        ['keep a = 1', /needs a source/],
        ['source nope', /line 1: unknown source/],
        ['source lives\nsource lives metric=frags', /line 2: lives has no param metric/],
        ['source lives\nkeep a = ', /line 2/],
        ['source lives\norder a b', /line 2: cannot read "b"/],
        ['source lives\norder first x', /first takes a count/],
        ['source lives\nclip from=start*2', /line 2: from=start\*2/],
        ['source lives\nclip at=start', /clip takes from=/],
        ['source lives\nwhere a = 1', /unknown line "where"/],
        ['source lives\nclip\nclip\nclip\nclip\nclip', /out of bounds/],
    ]) {
        const r = parseQueryText(bad);
        assert.match(r.error || '', /** @type {RegExp} */ (want), bad);
    }
});
