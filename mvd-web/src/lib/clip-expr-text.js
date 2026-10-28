// clip-expr-text.js — the clip finder's filter as text, e.g.
//   weapon = "rl" and (pickerRelation = enemy or not pickerHadWeapon)
// parsed with jsep (MIT, https://github.com/EricSmekens/jsep) into its
// syntax tree and converted to the stored expression tree
// (src/lib/clip-query.js). jsep only parses; nothing is evaluated, and any
// node outside the small grammar below is an error, not a pass-through.
//
// Grammar: `and` / `&&`, `or` / `||`, `not` / `!`, parentheses; a
// condition is `field op value` with op one of = (or ==) != < <= > >= ~
// and `in [a, b]`; a bare field means `field = true`. Fields are dotted
// paths, a list element by index (`victims[0].name`); a value is a
// string, a number, true / false, or a bare word taken as text.

import jsep from 'jsep';

/** @typedef {import('./clip-query.js').Expr} Expr */
/** @typedef {import('./clip-query.js').Condition} Condition */

jsep.addBinaryOp('or', 1);
jsep.addBinaryOp('and', 2);
jsep.addUnaryOp('not');
jsep.addBinaryOp('=', 6);
jsep.removeUnaryOp('~');
jsep.addBinaryOp('~', 7);
jsep.addBinaryOp('in', 7);

const COMPARE = /** @type {Record<string, Condition['op']>} */ ({
    '=': '=', '==': '=', '!=': '!=', '<': '<', '<=': '<=', '>': '>', '>=': '>=', '~': '~', in: 'in',
});

/**
 * @param {any} n  a jsep node
 * @returns {string}
 */
function pathOf(n) {
    if (n.type === 'Identifier') return n.name;
    if (n.type === 'MemberExpression') {
        const base = pathOf(n.object);
        if (!n.computed && n.property.type === 'Identifier') return `${base}.${n.property.name}`;
        if (n.computed && n.property.type === 'Literal' && (typeof n.property.value === 'number' || typeof n.property.value === 'string')) {
            return `${base}.${n.property.value}`;
        }
    }
    throw new Error('expected a field name');
}

/**
 * @param {any} n
 * @returns {string | number | boolean}
 */
function valueOf(n) {
    if (n.type === 'Literal' && ['string', 'number', 'boolean'].includes(typeof n.value)) return n.value;
    if (n.type === 'Identifier') return n.name;
    if (n.type === 'UnaryExpression' && n.operator === '-' && n.argument.type === 'Literal' && typeof n.argument.value === 'number') {
        return -n.argument.value;
    }
    throw new Error('expected a value: a number, "text", true or false');
}

/**
 * @param {any} n
 * @returns {Expr}
 */
function convert(n) {
    if (n.type === 'BinaryExpression') {
        const op = n.operator;
        if (op === 'and' || op === '&&' || op === 'or' || op === '||') {
            const key = op === 'and' || op === '&&' ? 'and' : 'or';
            /** @type {Expr[]} */
            const items = [];
            for (const side of [n.left, n.right]) {
                const e = convert(side);
                if (key in e) items.push(.../** @type {any} */ (e)[key]);
                else items.push(e);
            }
            return /** @type {Expr} */ ({ [key]: items });
        }
        const cmp = COMPARE[op];
        if (!cmp) throw new Error(`unknown operator ${op}`);
        const field = pathOf(n.left);
        if (cmp === 'in') {
            if (n.right.type !== 'ArrayExpression') throw new Error('in takes a list: field in [a, b]');
            return { field, op: cmp, value: n.right.elements.map(valueOf) };
        }
        return { field, op: cmp, value: valueOf(n.right) };
    }
    if (n.type === 'UnaryExpression' && (n.operator === 'not' || n.operator === '!')) return { not: convert(n.argument) };
    if (n.type === 'Identifier' || n.type === 'MemberExpression') return { field: pathOf(n), op: '=', value: true };
    throw new Error('expected a condition such as field = value');
}

// parseExpr turns the text into the stored tree; empty text keeps every row.
/**
 * @param {string} text
 * @returns {{ expr: Expr | undefined, error?: undefined } | { error: string, expr?: undefined }}
 */
export function parseExpr(text) {
    if (!text.trim()) return { expr: undefined };
    try {
        return { expr: convert(jsep(text)) };
    } catch (err) {
        return { error: /** @type {Error} */ (err).message || String(err) };
    }
}

/** @param {string} path */
function pathText(path) {
    return path.split('.').map((seg, i) => {
        if (/^\d+$/.test(seg)) return `[${seg}]`;
        if (/^[A-Za-z_$][\w$]*$/.test(seg)) return i ? `.${seg}` : seg;
        return `[${JSON.stringify(seg)}]`;
    }).join('');
}

/** @param {unknown} v */
function valueText(v) {
    if (typeof v === 'string') return JSON.stringify(v);
    return String(v);
}

// exprToText prints the tree back, with the parentheses precedence needs:
// or binds loosest, then and, then the comparisons — and jsep's `not` binds
// tighter than all of them, so whatever it negates is parenthesised
// (`not (kills = 0)`, never `not kills = 0`).
/**
 * @param {Expr | undefined} expr
 * @param {number} [outer]  the enclosing precedence
 * @returns {string}
 */
export function exprToText(expr, outer = 0) {
    if (!expr) return '';
    /** @type {(s: string, p: number) => string} */
    const wrap = (s, p) => (p < outer ? `(${s})` : s);
    if ('or' in expr) return wrap(expr.or.map((e) => exprToText(e, 1)).filter(Boolean).join(' or '), 1);
    if ('and' in expr) return wrap(expr.and.map((e) => exprToText(e, 2)).filter(Boolean).join(' and '), 2);
    if ('not' in expr) return wrap(`not (${exprToText(expr.not)})`, 3);
    const value = Array.isArray(expr.value) ? `[${expr.value.map(valueText).join(', ')}]` : valueText(expr.value);
    return `${pathText(expr.field)} ${expr.op} ${value}`;
}
