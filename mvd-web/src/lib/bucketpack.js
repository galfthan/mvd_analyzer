// bucketpack.js — decodes the bucket pack: a column-major bucket view
// (ColumnarBuckets) in binary (view.EncodeColumnarPack,
// mvd-analytics/view/columnar_pack.go, which documents the layout). The
// WASM worker's deferred 50 ms view arrives this way instead of as JSON.
//
// The result has the ColumnarBuckets JSON shape — what frames.js and the
// timeline helpers read — but its columns are typed-array VIEWS over the
// pack's buffer: index and .length work, Array.isArray does not. Booleans
// and the alive mask are 0/1 as in the JSON; armorType is a plain array of
// strings; floats are the analyzer's values unrounded (the JSON rounds to
// 3 decimals). The pack is little-endian, as is every platform a browser
// runs on.

export const BUCKET_PACK_VERSION = 1;

/** @type {Record<string, typeof Int16Array | typeof Int32Array | typeof Float32Array | typeof Float64Array | typeof Uint8Array | typeof Uint16Array>} */
const TYPES = {
    i16: Int16Array,
    i32: Int32Array,
    f32: Float32Array,
    f64: Float64Array,
    u8: Uint8Array,
    s16: Uint16Array,
};

/**
 * decodeBucketPack: the view a ColumnarBuckets JSON would parse to. Throws
 * on a foreign or newer pack, or a column type it does not know.
 * @param {ArrayBuffer} buf
 * @returns {Record<string, any>}
 */
export function decodeBucketPack(buf) {
    const dv = new DataView(buf);
    const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3));
    const version = dv.getUint32(4, true);
    if (magic !== 'MVDB' || version !== BUCKET_PACK_VERSION) {
        throw new Error(`bucket pack: unsupported (${magic} v${version}, want MVDB v${BUCKET_PACK_VERSION})`);
    }
    const headerLen = dv.getUint32(8, true);
    const h = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 12, headerLen)));
    const body = 12 + headerLen + ((8 - ((12 + headerLen) % 8)) % 8);
    const strings = h.strings || [];

    /** @param {{ t: string, o: number, n: number }} c */
    const column = (c) => {
        const Type = TYPES[c.t];
        if (!Type) throw new Error(`bucket pack: unknown column type ${c.t}`);
        const a = new Type(buf, body + c.o, c.n);
        return c.t === 's16' ? Array.from(a, (i) => strings[i]) : a;
    };

    /** @type {Record<string, any>} */
    const view = { windowMs: h.windowMs, start: h.start, count: h.count };
    if (h.timeUnit) view.timeUnit = h.timeUnit;
    if (h.partialLastMs) view.partialLastMs = h.partialLastMs;
    if (h.locTable) view.locTable = h.locTable;
    if (h.players) {
        view.players = {};
        for (const name in h.players) {
            const p = h.players[name];
            /** @type {Record<string, any>} */
            const cp = { first: p.first, n: p.n, alive: column(p.alive) };
            if (p.validFrom) cp.validFrom = p.validFrom;
            for (const k in p.cols) cp[k] = column(p.cols[k]);
            view.players[name] = cp;
        }
    }
    if (h.teams) {
        view.teams = {};
        for (const name in h.teams) {
            const t = h.teams[name];
            /** @type {Record<string, any>} */
            const ct = {};
            for (const k in t.cols) ct[k] = column(t.cols[k]);
            if (t.abt) {
                ct.abt = {};
                for (const k in t.abt) ct.abt[k] = column(t.abt[k]);
            }
            view.teams[name] = ct;
        }
    }
    return view;
}
