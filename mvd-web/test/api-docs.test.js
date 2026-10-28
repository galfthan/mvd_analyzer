import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { shortDoc } from '../src/player/api-docs.js';

const docs = JSON.parse(readFileSync(new URL('../src/lib/api-docs.json', import.meta.url), 'utf8'));

test('a tooltip is the opening sentences and the endpoint\'s own item', () => {
    const tip = shortDoc(docs['top-kills'].params.weapons.description, 'top-kills');
    assert.match(tip, /^CSV of weapon codes/);
    assert.match(tip, /\n\/top-kills filters the KILLING weapon/);
    assert.doesNotMatch(tip, /\/frags|water|legacy alias/);
    assert.match(tip, /\(ⓘ for more\)$/);
    assert.ok(tip.length < 600, String(tip.length));
});

test('a short description is the whole tooltip', () => {
    assert.equal(shortDoc('Match-relative ms.'), 'Match-relative ms.');
    assert.equal(shortDoc(undefined), '');
});

test('every finder tooltip stays short', () => {
    for (const [name, d] of Object.entries(docs)) {
        for (const p of Object.values(d.params)) assert.ok(shortDoc(p.description, name).length < 600, name);
        for (const f of Object.values(d.fields)) assert.ok(shortDoc(f.description).length < 600, name);
    }
});
