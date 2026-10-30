// html.js — HTML-string helpers shared by the topbar and the Svelte tabs
// that build table rows as markup.

/** @type {Record<string, string>} */
const HTML_ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

/** @param {unknown} s */
export function escapeHtml(s) {
    if (s == null) return '';
    return String(s).replace(/[&<>"']/g, c => HTML_ESCAPES[c]);
}
