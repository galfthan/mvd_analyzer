// adopt — a Svelte action that moves existing DOM nodes (the legacy tab
// markup in index.html's #legacy-dom, which app.js finds by id) into the
// element it is placed on, after its own children or (`first`) before them.
// Nodes keep their ids and listeners.
export function adopt(el, { selector, first = false }) {
    const nodes = [...document.querySelectorAll(selector)];
    if (first) el.prepend(...nodes);
    else el.append(...nodes);
}
