// store.js — a minimal observable that meets Svelte's store contract
// (`subscribe(fn)` calls fn now and on every change, returns an
// unsubscribe), so Svelte components read it as `$store` while app.js
// subscribes with a plain callback. Dependency-free so `node --test` runs
// it without node_modules.
//
// Unlike svelte/store's writable, `set` notifies even when the value is
// the same object or number: the page's "set the time again" is how it
// asks every view to redraw after new data arrived.

export function store(initial) {
    let value = initial;
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
