import test from 'node:test';
import assert from 'node:assert/strict';
import { createPlayerKeys, nextName } from '../src/player/player-keys.js';

const ev = (key, o = {}) => ({ key, repeat: false, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, target: null, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...o });

function setup(withStep = true) {
    const log = [];
    const key = createPlayerKeys({
        toggle: () => log.push('toggle'),
        step: withStep ? (dir) => log.push(dir > 0 ? 'next' : 'prev') : null,
        seek: (by) => log.push(by),
    });
    return { log, key };
}

test('Space plays / pauses, ↑ / ↓ step players, ← / → seek, Shift seeks ten', () => {
    const { log, key } = setup();
    key(ev(' '));
    key(ev(' ', { repeat: true }));
    key(ev('ArrowDown'));
    key(ev('ArrowUp'));
    key(ev('ArrowLeft'));
    key(ev('ArrowRight', { shiftKey: true }));
    key(ev('ArrowRight', { ctrlKey: true }));
    key(ev('Control'));
    assert.deepEqual(log, ['toggle', 'next', 'prev', -1, 10], 'a held Space does not repeat; Ctrl is not ours');
});

test('with nobody to watch, ↑ / ↓ are left to scroll the page', () => {
    const { log, key } = setup(false);
    const e = ev('ArrowDown');
    key(e);
    assert.deepEqual(log, []);
    assert.equal(e.defaultPrevented, false);
});

test('keys typed into a field are left alone', () => {
    const { log, key } = setup();
    const field = { closest: (sel) => (sel.includes('input') ? {} : null) };
    key(ev(' ', { target: field }));
    key(ev('ArrowLeft', { target: field }));
    const button = (keyboard) => ({ closest: (sel) => (sel === 'button, a' ? { matches: () => keyboard } : null) });
    key(ev(' ', { target: button(true) }));
    key(ev('ArrowLeft', { target: button(true) }));
    key(ev(' ', { target: button(false) }));
    assert.deepEqual(log, [-1, 'toggle'], 'Space presses a keyboard-focused button, not one a click left focused; arrows still step');
});

test('a key something took already (a focused pane divider) is not ours', () => {
    const { log, key } = setup();
    key(ev('ArrowLeft', { defaultPrevented: true }));
    assert.deepEqual(log, []);
});

test('nextName wraps both ways and starts from an end', () => {
    assert.equal(nextName(['a', 'b', 'c'], 'b'), 'c');
    assert.equal(nextName(['a', 'b', 'c'], 'c'), 'a');
    assert.equal(nextName(['a', 'b', 'c'], 'a', -1), 'c');
    assert.equal(nextName(['a', 'b'], null), 'a');
    assert.equal(nextName(['a', 'b'], null, -1), 'b');
    assert.equal(nextName([], 'a'), null);
});
