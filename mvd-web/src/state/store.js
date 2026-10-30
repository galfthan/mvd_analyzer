// store.js — a minimal observable that meets Svelte's store contract
// (`subscribe(fn)` calls fn now and on every change, returns an
// unsubscribe), so Svelte components read it as `$store` while plain
// modules subscribe with a callback. Dependency-free so `node --test` runs
// it without node_modules.
//
// Unlike svelte/store's writable, `set` notifies even when the value is
// the same object or number: the page's "set the time again" is how it
// asks every view to redraw after new data arrived.

/**
 * @template T
 * @typedef {object} Store
 * @property {(fn: (value: T) => void) => () => void} subscribe
 * @property {(value: T) => void} set
 * @property {() => T} get
 */

/**
 * A store holding `initial`. T is inferred from it, so a store that starts
 * as null and is set to an object later casts `initial` to the full type.
 * @template T
 * @param {T} initial
 * @returns {Store<T>}
 */
export function store(initial) {
    let value = initial;
    /** @type {Set<(value: T) => void>} */
    const subs = new Set();
    return {
        subscribe(fn) {
            subs.add(fn);
            fn(value);
            return () => subs.delete(fn);
        },
        set(v) {
            value = v;
            for (const fn of [...subs]) fn(value);
        },
        get() {
            return value;
        },
    };
}
