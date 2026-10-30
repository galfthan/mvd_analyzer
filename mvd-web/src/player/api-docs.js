// api-docs.js — what each clip-finder source, param and row field means:
// the API spec's own text, extracted into src/lib/api-docs.json
// (mvd-api/webdocs_test.go). Loaded on first use, in its own chunk.

import { store } from '../state/store.js';

/**
 * @typedef {{ description?: string, type?: string, enum?: string[], default?: unknown }} ParamDoc
 * @typedef {{ description?: string, type?: string, enum?: string[] }} FieldDoc
 * @typedef {{ summary: string, description: string, params: Record<string, ParamDoc>, fields: Record<string, FieldDoc> }} SourceDoc
 */

export const apiDocs = store(/** @type {Record<string, SourceDoc> | null} */ (null));

let loading = false;
export function loadApiDocs() {
    if (loading) return;
    loading = true;
    import('../lib/api-docs.json').then((m) => apiDocs.set(m.default), () => { loading = false; });
}

// fieldDoc finds a row field's entry; a list index in the path ('victims.3.name')
// reads as the documented first element.
/**
 * @param {SourceDoc | undefined} doc
 * @param {string} path
 * @returns {FieldDoc | undefined}
 */
export function fieldDoc(doc, path) {
    return doc?.fields[path] || doc?.fields[path.replace(/\.\d+(?=\.|$)/g, '.0')];
}

// fieldEnum: the values a row field allows — its own enum, else the enum
// of the param that filters by it (events' `type` by `types`, highlights'
// `kind` by `kinds`), which the spec states once, on the param.
/**
 * @param {SourceDoc | undefined} doc
 * @param {string} path
 * @returns {string[]}
 */
export function fieldEnum(doc, path) {
    return fieldDoc(doc, path)?.enum || doc?.params[`${path}s`]?.enum || [];
}

/** @param {string} text */
const flat = (text) => text.replace(/\s+/g, ' ').replace(/[`*]/g, '').trim();

// Up to `max` characters of whole sentences (one at least, cut short past
// the limit).
/**
 * @param {string} text
 * @param {number} max
 */
function sentences(text, max) {
    const all = text.match(/[^.!?]+[.!?]+(?=\s|$)|[^.!?]+$/g) || [text];
    let out = '';
    for (const s of all) {
        if (out && out.length + s.length > max) break;
        out += s;
    }
    return out.length > max + 40 ? `${out.slice(0, max - 1).trimEnd()}…` : out.trim();
}

// shortDoc is a description cut to a tooltip: its opening paragraph's first
// sentences, and the list item that is about `source` when the text has
// one per endpoint; "(ⓘ for more)" when anything was left out.
/**
 * @param {string | undefined} text
 * @param {string} [source]
 */
export function shortDoc(text, source) {
    if (!text) return '';
    const blocks = text.trim().split(/\n\s*\n/);
    let tip = sentences(flat(blocks[0]), 220);
    const own = source && text.split('\n- ').slice(1).find((it) => it.startsWith(`**/${source}**`));
    if (own) tip += `\n${sentences(flat(own), 220)}`;
    return tip.replace(/\s+$/, '').length < flat(text).length ? `${tip} (ⓘ for more)` : tip;
}
