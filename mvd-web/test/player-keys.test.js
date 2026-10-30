import test from 'node:test';
import assert from 'node:assert/strict';
import { createPlayerKeys, nextName } from '../src/player/player-keys.js';

const ev = (key, o = {}) => ({ key, repeat: false, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, target: null, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...o });

function setup() {
    const log = [];
    const keys = createPlayerKeys({ toggle: () => log.push('toggle'), next: () => log.push('next'), seek: (by) => log.push(by) });
    return { log, keys };
}

test('hub keys: Space next, arrows step, Shift steps ten', () => {
    const { log, keys } = setup();
    keys.down(ev(' '));
    keys.down(ev(' ', { repeat: true }));
    keys.down(ev('ArrowLeft'));
    keys.down(ev('ArrowRight', { shiftKey: true }));
    keys.down(ev('ArrowRight', { ctrlKey: true }));
    assert.deepEqual(log, ['next', -1, 10], 'a held Space does not cycle; Ctrl+arrow is not ours');
});

test('Ctrl alone plays / pauses; Ctrl with a key or a click does not', () => {
    const { log, keys } = setup();
    keys.down(ev('Control'));
    keys.down(ev('Control', { repeat: true }));
    keys.up(ev('Control'));
    keys.down(ev('Control'));
    keys.down(ev('c', { ctrlKey: true }));
    keys.up(ev('Control'));
    keys.down(ev('Control'));
    keys.cancel();
    keys.up(ev('Control'));
    assert.deepEqual(log, ['toggle']);
});

test('keys typed into a field are left alone', () => {
    const { log, keys } = setup();
    const field = { closest: (sel) => (sel.includes('input') ? {} : null) };
    keys.down(ev(' ', { target: field }));
    keys.down(ev('ArrowLeft', { target: field }));
    const button = { closest: (sel) => (sel === 'button, a' ? {} : null) };
    keys.down(ev(' ', { target: button }));
    keys.down(ev('ArrowLeft', { target: button }));
    assert.deepEqual(log, [-1], 'Space presses a focused button; arrows still step');
});

test('nextName wraps and starts from the first', () => {
    assert.equal(nextName(['a', 'b', 'c'], 'b'), 'c');
    assert.equal(nextName(['a', 'b', 'c'], 'c'), 'a');
    assert.equal(nextName(['a', 'b'], null), 'a');
    assert.equal(nextName(['a', 'b'], 'zz'), 'a');
    assert.equal(nextName([], 'a'), null);
});
