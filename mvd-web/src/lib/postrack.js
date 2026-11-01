// postrack.js — decodes the position pack: every player's native-rate
// position track in binary (result.EncodePositionPack,
// mvd-analytics/result/postrack_wire.go, which documents the layout). The
// WASM worker hands one over beside the Result JSON, which then omits
// `streams.players[].pos`; mvd-api serves the same bytes at
// /v1/demos/{id}/streams/positions.
//
// The columns are typed-array VIEWS over the pack's buffer, not copies.
// They read like the JSON arrays (index, length) but are not Arrays: test
// a column with `.length`, never Array.isArray. Values are the analyzer's
// float32s bit for bit, unrounded (the JSON rounds to 3 decimals). The
// pack is little-endian, as is every platform a browser runs on.

export const POSITION_PACK_VERSION = 1;

/** @typedef {Int32Array | Float32Array | Int16Array | Int8Array} Column */
/** @typedef {Record<string, Column>} PositionTrack */

// Column order and element types, as written. t/x/y/z are always set (the
// JSON always had them); the rest only when non-empty, as the JSON's
// omitempty did.
/** @type {Array<[string, typeof Int32Array | typeof Float32Array | typeof Int16Array | typeof Int8Array, boolean]>} */
const COLUMNS = [
    ['t', Int32Array, true],
    ['x', Float32Array, true],
    ['y', Float32Array, true],
    ['z', Float32Array, true],
    ['li', Int16Array, false],
    ['h', Float32Array, false],
    ['lq', Int8Array, false],
    ['vp', Int16Array, false],
    ['vya', Int16Array, false],
    ['vx', Float32Array, false],
    ['vy', Float32Array, false],
    ['vz', Float32Array, false],
];

/** @param {number} n */
const pad4 = (n) => (4 - (n % 4)) % 4;

/**
 * decodePositionPack: one entry per player in Streams.Players order, pos
 * null for a player without a track. Throws on a foreign or newer pack.
 * @param {ArrayBuffer} buf
 * @returns {Array<{ name: string, pos: PositionTrack | null }>}
 */
export function decodePositionPack(buf) {
    const dv = new DataView(buf);
    const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3));
    const version = dv.getUint32(4, true);
    if (magic !== 'MVDP' || version !== POSITION_PACK_VERSION) {
        throw new Error(`position pack: unsupported (${magic} v${version}, want MVDP v${POSITION_PACK_VERSION})`);
    }
    const count = dv.getUint32(8, true);
    const utf8 = new TextDecoder();
    let off = 12;
    const out = [];
    for (let i = 0; i < count; i++) {
        const nameLen = dv.getUint32(off, true);
        off += 4;
        const name = utf8.decode(new Uint8Array(buf, off, nameLen));
        off += nameLen + pad4(nameLen);
        const has = dv.getUint32(off, true);
        off += 4;
        if (!has) {
            out.push({ name, pos: null });
            continue;
        }
        /** @type {PositionTrack} */
        const pos = {};
        for (const [key, Type, always] of COLUMNS) {
            const n = dv.getUint32(off, true);
            off += 4;
            if (n > 0 || always) pos[key] = new Type(buf, off, n);
            const bytes = n * Type.BYTES_PER_ELEMENT;
            off += bytes + pad4(bytes);
        }
        out.push({ name, pos });
    }
    return out;
}

/**
 * attachPositions puts a pack's tracks back on the Result as
 * `streams.players[i].pos` — where the JSON had them — so readers see one
 * shape whichever way the tracks arrived.
 * @param {any} result
 * @param {ArrayBuffer} buf
 */
export function attachPositions(result, buf) {
    // Written from the same Streams.Players, so the pack is empty exactly
    // when the Result has no streams.
    decodePositionPack(buf).forEach(({ pos }, i) => {
        if (pos) result.streams.players[i].pos = pos;
    });
}
