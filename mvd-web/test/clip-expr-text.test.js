import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';

import { PRESETS } from '../src/lib/clip-query.js';

// The parser is jsep, an npm dependency: skipped where `npm ci` has not run.
const installed = existsSync(new URL('../node_modules/jsep/package.json', import.meta.url));
const { parseExpr, exprToText } = installed ? await import('../src/lib/clip-expr-text.js') : /** @type {any} */ ({});
const skip = !installed && 'jsep not installed (npm ci in mvd-web)';

test('text parses into the stored tree', { skip }, () => {
    assert.deepEqual(parseExpr('weapon = "rl" and (pickerRelation = enemy or not pickerHadWeapon)').expr, { and: [
        { field: 'weapon', op: '=', value: 'rl' },
        { or: [{ field: 'pickerRelation', op: '=', value: 'enemy' }, { not: { field: 'pickerHadWeapon', op: '=', value: true } }] },
    ] });
    assert.deepEqual(parseExpr('a and b and c').expr, { and: ['a', 'b', 'c'].map((field) => ({ field, op: '=', value: true })) }, 'chains flatten');
    assert.deepEqual(parseExpr('victims[0].name ~ "BPS" && detail.frags >= -2').expr, { and: [
        { field: 'victims.0.name', op: '~', value: 'BPS' }, { field: 'detail.frags', op: '>=', value: -2 },
    ] });
    assert.deepEqual(parseExpr('pickerRelation in [team, "self"] || !(x == 1)').expr, { or: [
        { field: 'pickerRelation', op: 'in', value: ['team', 'self'] }, { not: { field: 'x', op: '=', value: 1 } },
    ] });
    assert.deepEqual(parseExpr('order = 1 or android = 2').expr.or.map((c) => c.field), ['order', 'android'], 'word operators need a word boundary');
    assert.equal(parseExpr('  ').expr, undefined, 'empty keeps all');
});

test('text the grammar does not cover is an error, not a pass-through', { skip }, () => {
    for (const bad of ['a = b(1)', '"x" = a', 'a = ', 'a + 1', 'a in b', 'a = [1]', 'a = b.c', 'this', 'a = {x: 1}']) {
        assert.ok(parseExpr(bad).error, bad);
    }
});

test('printing and parsing round-trip every preset and nested trees', { skip }, () => {
    const exprs = PRESETS.map((p) => p.query.where).filter(Boolean);
    exprs.push({ or: [
        { and: [{ field: 'a', op: '<', value: 3 }, { not: { or: [{ field: 'b.c', op: '!=', value: 'x y' }, { field: 'd', op: '=', value: false }] } }] },
        { field: 'victims.0.name', op: 'in', value: ['a', 2] },
        { field: 'victims.[cmd] witka', op: '>=', value: 2 },
        { field: 'byWeapon.rl', op: '~', value: 'q"uote' },
    ] });
    for (const e of exprs) {
        const text = exprToText(e);
        assert.deepEqual(parseExpr(text).expr, e, text);
    }
    const notCond = { not: { field: 'pickerKills', op: '=', value: 0 } };
    assert.equal(exprToText(notCond), 'not (pickerKills = 0)', 'not binds tighter than =');
    assert.deepEqual(parseExpr(exprToText(notCond)).expr, notCond);
    assert.equal(exprToText({ and: [{ field: 'a', op: '=', value: 1 }, { or: [{ field: 'b', op: '=', value: 2 }, { field: 'c', op: '=', value: 3 }] }] }),
        'a = 1 and (b = 2 or c = 3)');
});
