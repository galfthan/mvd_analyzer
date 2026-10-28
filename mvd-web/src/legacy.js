// legacy.js — the ONE place the Svelte side reaches into app.js (a classic
// script whose top-level functions are window properties). Everything here
// moves into src/ as the tab that owns it is ported; until then a new
// component imports these instead of touching window itself.

// loadDemo(input) loads and renders a demo (app.js: status line, timing,
// displayResults). Throws on failure.
export function loadDemo(input) {
    return window.loadDemo(input);
}
