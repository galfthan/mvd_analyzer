// Texture atlas for the GL sprite pass: text labels and letter glyphs baked
// through an offscreen 2D canvas used purely as a rasteriser (the ground
// rule for the pure-GL renderer — 2D contexts may bake textures, never draw
// the scene).
//
// Whole strings are baked as single entries rather than per-glyph: a map
// has a few dozen distinct loc names, item labels and player letters, and
// whole-string entries sidestep glyph layout in GL entirely. Keys are
// (font, color, text[, stroke]) — a zoom step changes the font px and
// simply bakes new entries. When the atlas page fills, it resets and
// entries rebake lazily on the next frame (rare: a page holds hundreds of
// labels).

const PAGE = 1024;   // atlas page size (px). One page is plenty per map.
const PAD = 2;       // padding around entries so linear sampling never bleeds

export class GlAtlas {
    // `doc` is the document that owns the render canvas (loader invariant:
    // never the ambient `document`).
    constructor(doc) {
        this.canvas = doc.createElement('canvas');
        this.canvas.width = PAGE;
        this.canvas.height = PAGE;
        this.ctx = this.canvas.getContext('2d', { willReadFrequently: false });
        this.reset();
    }

    reset() {
        this.entries = new Map();
        this.shelfX = PAD;
        this.shelfY = PAD;
        this.shelfH = 0;
        this.ctx.clearRect(0, 0, PAGE, PAGE);
        this.dirty = true;
        this.generation = (this.generation || 0) + 1;
    }

    // get returns {u0, v0, u1, v1, w, h} for the baked string, baking it on
    // first use. `stroke` optionally draws a stroked underlay (the drop-D
    // outline). Returns null only for entries that cannot fit at all.
    get(text, font, color, stroke = null) {
        const key = font + '|' + color + '|' + (stroke || '') + '|' + text;
        let e = this.entries.get(key);
        if (e) return e;

        const ctx = this.ctx;
        ctx.font = font;
        const m = ctx.measureText(text);
        const ascent = m.actualBoundingBoxAscent ?? 10;
        const descent = m.actualBoundingBoxDescent ?? 3;
        const w = Math.ceil(m.width) + 2 * PAD + (stroke ? 6 : 0);
        const h = Math.ceil(ascent + descent) + 2 * PAD + (stroke ? 6 : 0);
        if (w > PAGE || h > PAGE) return null;

        // Shelf packing; on overflow, reset the page (callers rebake next
        // frame through this same path).
        if (this.shelfX + w > PAGE) {
            this.shelfX = PAD;
            this.shelfY += this.shelfH + PAD;
            this.shelfH = 0;
        }
        if (this.shelfY + h > PAGE) {
            this.reset();
            ctx.font = font;
        }
        const x = this.shelfX, y = this.shelfY;
        this.shelfX += w + PAD;
        this.shelfH = Math.max(this.shelfH, h);

        ctx.textAlign = 'left';
        ctx.textBaseline = 'alphabetic';
        const tx = x + PAD + (stroke ? 3 : 0);
        const ty = y + PAD + (stroke ? 3 : 0) + ascent;
        if (stroke) {
            ctx.lineWidth = 5;
            ctx.strokeStyle = stroke;
            ctx.strokeText(text, tx, ty);
        }
        ctx.fillStyle = color;
        ctx.fillText(text, tx, ty);

        e = {
            u0: x / PAGE, v0: y / PAGE,
            u1: (x + w) / PAGE, v1: (y + h) / PAGE,
            w, h,
            // Optical centre: distance from the entry's top to the middle of
            // the glyph box, so callers can centre a label on a point.
            midY: PAD + (stroke ? 3 : 0) + (ascent + descent) / 2,
        };
        this.entries.set(key, e);
        this.dirty = true;
        return e;
    }
}
