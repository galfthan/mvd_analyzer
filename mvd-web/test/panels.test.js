import test from 'node:test';
import assert from 'node:assert/strict';

// The layout persists to localStorage; node has none, so give it one.
const mem = new Map();
globalThis.localStorage = {
    getItem: (k) => (mem.has(k) ? mem.get(k) : null),
    setItem: (k, v) => mem.set(k, String(v)),
};
// A saved layout from an older page: a panel since removed, one missing,
// a duplicate, and flags on unknown ids.
mem.set('mvd.panels.v1', JSON.stringify({
    pickups: { order: ['item-pickups', 'gone', 'item-pickups'], closed: { gone: true, 'item-pickups': true } },
    aimstats: 'corrupt',
}));

const { PANEL_TABS } = await import('../src/ui/panels/config.js');
const { panelLayouts, updatePanels, movePanel, showAll, resetPanels, isDefault } = await import('../src/ui/panels/layout.js');

test('panel layouts load sanitized: known ids once, new panels at their default place', () => {
    const l = panelLayouts.get();
    assert.deepEqual(l.pickups.order, ['weapon-pickups', 'item-pickups'], 'a panel missing from the saved order joins before its default successor');
    assert.deepEqual(l.pickups.closed, { 'item-pickups': true });
    assert.deepEqual(l.aimstats.order, PANEL_TABS.aimstats.map(([id]) => id), 'a corrupt entry falls back to the default');
    assert.ok(isDefault('aimstats', l.aimstats));
    assert.ok(!isDefault('pickups', l.pickups));
});

test('a saved order keeps its place and a new panel slots in after its default neighbour', () => {
    updatePanels('timeline', (l) => { l.order = ['weapons', 'team-status']; });
    const order = panelLayouts.get().timeline.order;
    assert.equal(order[0], 'weapons');
    assert.equal(order.length, PANEL_TABS.timeline.length);
    assert.ok(order.indexOf('frags-per-player') === order.indexOf('team-status') + 1, 'joins after team-status, its default predecessor');
});

test('move, show all and reset', () => {
    resetPanels('keymoments');
    movePanel('keymoments', 'telefrags', 'powerup-runs');
    assert.equal(panelLayouts.get().keymoments.order[0], 'telefrags', 'the moved panel takes the target slot');
    updatePanels('keymoments', (l) => { l.closed.streaks = true; l.collapsed.markers = true; });
    showAll('keymoments');
    const k = panelLayouts.get().keymoments;
    assert.deepEqual([k.closed, k.collapsed], [{}, {}]);
    assert.equal(k.order[0], 'telefrags', 'show all keeps the order');
    resetPanels('keymoments');
    assert.ok(isDefault('keymoments', panelLayouts.get().keymoments));
    assert.ok(JSON.parse(mem.get('mvd.panels.v1')).keymoments, 'saved');
});
