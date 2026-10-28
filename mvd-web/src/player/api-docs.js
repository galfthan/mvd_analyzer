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

// The first sentence of a description, for a tooltip.
/** @param {string | undefined} text */
export function firstSentence(text) {
    if (!text) return '';
    const flat = text.replace(/\s+/g, ' ').replace(/[`*]/g, '').trim();
    const m = /^(.{20,}?[.!?])(\s|$)/.exec(flat);
    const s = m ? m[1] : flat;
    return s.length > 220 ? `${s.slice(0, 217)}…` : s;
}
