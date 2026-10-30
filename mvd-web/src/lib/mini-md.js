// mini-md.js — the little markdown the API spec's descriptions use, as
// HTML: paragraphs, `- ` lists, `code` and **bold**. Everything is escaped
// first, so the output carries no markup the text did not ask for.

/** @param {string} s */
const escape = (s) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);

/** @param {string} s */
const inline = (s) => escape(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');

/**
 * @param {string} text
 * @returns {string}
 */
export function miniMarkdown(text) {
    const out = [];
    for (const block of text.trim().split(/\n\s*\n/)) {
        const lines = block.split('\n');
        if (/^\s*- /.test(lines[0])) {
            /** @type {string[]} */
            const items = [];
            for (const l of lines) {
                if (/^\s*- /.test(l)) items.push(l.replace(/^\s*- /, ''));
                else if (items.length) items[items.length - 1] += ` ${l.trim()}`;
            }
            out.push(`<ul>${items.map((i) => `<li>${inline(i)}</li>`).join('')}</ul>`);
        } else {
            out.push(`<p>${inline(lines.map((l) => l.trim()).join(' '))}</p>`);
        }
    }
    return out.join('');
}
