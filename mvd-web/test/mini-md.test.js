import test from 'node:test';
import assert from 'node:assert/strict';

import { miniMarkdown } from '../src/lib/mini-md.js';

test('paragraphs, lists, code and bold', () => {
    assert.equal(miniMarkdown('One `a`\nline.\n\n- **b** x\n  more\n- c'),
        '<p>One <code>a</code> line.</p><ul><li><strong>b</strong> x more</li><li>c</li></ul>');
});

test('markup in the text is escaped, never passed through', () => {
    assert.equal(miniMarkdown('<img src=x onerror="y"> `<b>`'), '<p>&#60;img src=x onerror=&#34;y&#34;&#62; <code>&#60;b&#62;</code></p>');
});
