// WebGL backend for the static world — the floor model and the liquid
// volumes, i.e. exactly the layers the 2D path bakes to an offscreen canvas
// and re-bakes on every camera-motion frame.
//
// Design notes, in the order they matter:
//
// - **Painter order is kept.** Floors are near-opaque (FLOOR_TOP_ALPHA) and
//   liquids genuinely translucent, and the 2D path composites both back to
//   front. Rendering the same order with premultiplied-alpha blending
//   reproduces that compositing exactly — no depth buffer, no z-fighting,
//   and no look change beyond the anti-aliasing. The sort only depends on
//   yaw/pitch (same invariant renderSolidEntries relies on), so pan/zoom
//   frames re-upload nothing but a handful of uniforms.
//
// - **No seam hack.** The 2D path strokes every triangle batch with its own
//   fill colour to seal anti-aliasing seams between adjacent triangles
//   (see renderSolidEntries). GL rasterisation of shared-edge triangles in
//   one draw call has no such seams, so the hack — and the double
//   rasterisation it cost — simply disappears.
//
// - **The camera is six coefficients.** project() is affine in (x, y, z):
//   u/v are linear maps of the rotated point and the screen map is linear
//   too. makeWorldTransform folds the whole thing into row vectors for
//   clip-x and clip-y, computed on the CPU each frame.
//
// - **The loader invariant holds.** This module receives its WebGL context
//   from the caller (who created the backing canvas via
//   canvas.ownerDocument.createElement) and touches no ambient global.
//
// The 2D implementations stay: they are the fallback when a context cannot
// be created, and the parity harness's anchor (?gl=0 in mvd-web).

// Parse an rgba()/rgb() string into premultiplied [r, g, b, a] floats.
// Memoised — the floor model reuses a handful of distinct colour strings
// across thousands of entries.
const _colorCache = new Map();
export function parseColor(str) {
    let c = _colorCache.get(str);
    if (c) return c;
    const m = /rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)/.exec(str);
    if (m) {
        const a = m[4] === undefined ? 1 : parseFloat(m[4]);
        c = [(+m[1] / 255) * a, (+m[2] / 255) * a, (+m[3] / 255) * a, a];
    } else {
        c = [0, 0, 0, 1];
    }
    _colorCache.set(str, c);
    return c;
}

// makeWorldTransform: fold the orbit projection (camera.js project()) and
// the CSS-px → clip-space map into two row vectors:
//   clipX = rx[0]*x + rx[1]*y + rx[2]*z + rx[3]
//   clipY = ry[0]*x + ry[1]*y + ry[2]*z + ry[3]
// cssW/cssH are the logical surface size project() targets.
export function makeWorldTransform(w, cssW, cssH) {
    const A = w.scale * w.zoomK;
    // u = cosYaw*x - sinYaw*y + cu
    const cu = w.cx - w.cosYaw * w.cx + w.sinYaw * w.cy;
    // v = sinYaw*sinPitch*x + cosYaw*sinPitch*y + cosPitch*z + cv
    const cv = w.cy - (w.cx * w.sinYaw + w.cy * w.cosYaw) * w.sinPitch - w.zMid * w.cosPitch;
    // screenX = A*u + bx;  clipX = screenX * 2/cssW - 1
    const bx = w.offsetX - A * w.minX + w.panX;
    const kx = (2 * A) / cssW;
    // screenY = -A*v + by;  clipY = 1 - screenY * 2/cssH
    const by = w.canvasH - w.offsetY + A * w.minY + w.panY;
    const ky = (2 * A) / cssH;
    return {
        rx: [kx * w.cosYaw, -kx * w.sinYaw, 0, kx * cu + (2 * bx) / cssW - 1],
        ry: [ky * w.sinYaw * w.sinPitch, ky * w.cosYaw * w.sinPitch, ky * w.cosPitch,
             ky * cv - (2 * by) / cssH + 1],
    };
}

// entryDepth: camera closeness of an entry centroid — identical to the sort
// key renderSolidEntries and drawLiquidVolume compute.
export function entryDepth(w, cx, cy, cz) {
    const dx = cx - w.cx, dy = cy - w.cy, dz = cz - w.zMid;
    const yr = dx * w.sinYaw + dy * w.cosYaw;
    return dz * w.sinPitch - yr * w.cosPitch;
}

// buildEntryVertices: interleaved [x, y, z, r, g, b, a] × 3 per entry, with
// premultiplied per-vertex colours. `colorOf(entry)` picks fill vs faded.
// Also returns the per-entry centroids for the angle sort.
export function buildEntryVertices(entries, colorOf) {
    const verts = new Float32Array(entries.length * 3 * 7);
    const centroids = new Float32Array(entries.length * 3);
    let vi = 0;
    for (let e = 0; e < entries.length; e++) {
        const entry = entries[e];
        const [r, g, b, a] = parseColor(colorOf(entry));
        const t = entry.tris, i = entry.off;
        for (let v = 0; v < 9; v += 3) {
            verts[vi++] = t[i + v];
            verts[vi++] = t[i + v + 1];
            verts[vi++] = t[i + v + 2];
            verts[vi++] = r;
            verts[vi++] = g;
            verts[vi++] = b;
            verts[vi++] = a;
        }
        centroids[e * 3]     = entry.cx;
        centroids[e * 3 + 1] = entry.cy;
        centroids[e * 3 + 2] = entry.cz;
    }
    return { verts, centroids };
}

// sortedIndices: entry draw order, back to front, as a vertex index buffer
// (3 vertices per entry).
export function sortedIndices(w, centroids) {
    const n = centroids.length / 3;
    const order = new Array(n);
    const depths = new Float64Array(n);
    for (let e = 0; e < n; e++) {
        order[e] = e;
        depths[e] = entryDepth(w, centroids[e * 3], centroids[e * 3 + 1], centroids[e * 3 + 2]);
    }
    order.sort((a, b) => depths[a] - depths[b]);
    const idx = new Uint32Array(n * 3);
    let j = 0;
    for (const e of order) {
        idx[j++] = e * 3;
        idx[j++] = e * 3 + 1;
        idx[j++] = e * 3 + 2;
    }
    return idx;
}

const VERT_SRC = `#version 300 es
uniform vec4 uRowX;
uniform vec4 uRowY;
in vec3 aPos;
in vec4 aColor;
out vec4 vColor;
void main() {
    vec4 p = vec4(aPos, 1.0);
    gl_Position = vec4(dot(uRowX, p), dot(uRowY, p), 0.0, 1.0);
    vColor = aColor;
}`;

const FRAG_SRC = `#version 300 es
precision mediump float;
in vec4 vColor;
out vec4 outColor;
void main() { outColor = vColor; }`;

// One sortable, blendable batch of triangles (the floor model, or one liquid
// volume). Owns its VBO and its angle-sorted index buffer.
class GlBatch {
    constructor(gl, verts, centroids) {
        this.gl = gl;
        this.count = centroids.length / 3;
        this.centroids = centroids;
        this.vbo = gl.createBuffer();
        gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, verts, gl.STATIC_DRAW);
        this.ibo = gl.createBuffer();
        this.sortedFor = null;
    }

    // ensureSorted re-uploads the index buffer when the camera ANGLE changed;
    // pan/zoom keys are excluded on purpose (the painter order only depends
    // on yaw/pitch, exactly like the 2D path's sortedFor key).
    ensureSorted(w) {
        const key = w.yaw + '|' + w.pitch;
        if (this.sortedFor === key) return;
        const gl = this.gl;
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, this.ibo);
        gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, sortedIndices(w, this.centroids), gl.DYNAMIC_DRAW);
        this.sortedFor = key;
    }

    dispose() {
        this.gl.deleteBuffer(this.vbo);
        this.gl.deleteBuffer(this.ibo);
    }
}

// GlWorld: the renderer. Owns the context, the shader program and the
// current batches; rebuilds them when the floor model, the liquids or the
// focus predicate change (rare, user-driven), and otherwise renders any
// camera with two uniform vec4s.
export class GlWorld {
    // `canvas` is the offscreen backing canvas the caller created. Returns a
    // working renderer or null when WebGL2 is unavailable or the program
    // fails to build (caller falls back to the 2D path either way).
    static create(canvas) {
        try {
            const gl = canvas.getContext('webgl2', {
                alpha: true,
                antialias: true,
                depth: false,
                premultipliedAlpha: true,
                preserveDrawingBuffer: false,
            });
            if (!gl) return null;
            return new GlWorld(canvas, gl);
        } catch (e) {
            return null;
        }
    }

    constructor(canvas, gl) {
        this.canvas = canvas;
        this.gl = gl;
        const compile = (type, src) => {
            const sh = gl.createShader(type);
            gl.shaderSource(sh, src);
            gl.compileShader(sh);
            if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS)) {
                throw new Error('shader: ' + gl.getShaderInfoLog(sh));
            }
            return sh;
        };
        const prog = gl.createProgram();
        gl.attachShader(prog, compile(gl.VERTEX_SHADER, VERT_SRC));
        gl.attachShader(prog, compile(gl.FRAGMENT_SHADER, FRAG_SRC));
        gl.linkProgram(prog);
        if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
            throw new Error('link: ' + gl.getProgramInfoLog(prog));
        }
        this.prog = prog;
        this.uRowX = gl.getUniformLocation(prog, 'uRowX');
        this.uRowY = gl.getUniformLocation(prog, 'uRowY');
        this.aPos = gl.getAttribLocation(prog, 'aPos');
        this.aColor = gl.getAttribLocation(prog, 'aColor');
        gl.disable(gl.DEPTH_TEST);
        gl.enable(gl.BLEND);
        // Premultiplied-alpha over — matches the canvas compositor and the
        // 2D source-over the painter order emulates.
        gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
        this.floorBatch = null;
        this.liquidBatch = null;
        this._floorModel = null;
        this._floorFocus = null;
        this._liquidFaces = null;
    }

    // syncFloor (re)builds the floor batch when the model or the focus
    // changed — both rare, user-driven events. Model identity is the cache
    // key (floorModel() memoises per geometry+groups); `focusName` keys the
    // faded-fill variant exactly like the 2D bake key does.
    syncFloor(model, focusName, isFaded) {
        const focus = focusName ?? null;
        if (model === this._floorModel && focus === this._floorFocus) return;
        if (this.floorBatch) { this.floorBatch.dispose(); this.floorBatch = null; }
        this._floorModel = model;
        this._floorFocus = focus;
        if (!model) return;
        const { verts, centroids } = buildEntryVertices(model.entries,
            (e) => (isFaded && isFaded(e.name)) ? e.fillFaded : e.fill);
        this.floorBatch = new GlBatch(this.gl, verts, centroids);
    }

    // syncLiquids (re)builds the liquid batch. `faces` is a prebuilt entry
    // list (same {tris, off, cx, cy, cz, fill} shape as floor entries,
    // identity-cached by the caller per geometry) — the Lambert shade
    // against the fixed light is static, so the colours never change.
    syncLiquids(faces) {
        if (faces === this._liquidFaces) return;
        if (this.liquidBatch) { this.liquidBatch.dispose(); this.liquidBatch = null; }
        this._liquidFaces = faces;
        if (!faces || faces.length === 0) return;
        const { verts, centroids } = buildEntryVertices(faces, (e) => e.fill);
        this.liquidBatch = new GlBatch(this.gl, verts, centroids);
    }

    _drawBatch(batch, w) {
        if (!batch) return;
        const gl = this.gl;
        batch.ensureSorted(w);
        gl.bindBuffer(gl.ARRAY_BUFFER, batch.vbo);
        gl.enableVertexAttribArray(this.aPos);
        gl.vertexAttribPointer(this.aPos, 3, gl.FLOAT, false, 28, 0);
        gl.enableVertexAttribArray(this.aColor);
        gl.vertexAttribPointer(this.aColor, 4, gl.FLOAT, false, 28, 12);
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, batch.ibo);
        gl.drawElements(gl.TRIANGLES, batch.count * 3, gl.UNSIGNED_INT, 0);
    }

    // render draws floors then liquids (the 2D layer order) for the given
    // camera into the backing canvas, sized to pxW × pxH physical pixels.
    // cssW/cssH are the logical size the camera math targets.
    render(w, cssW, cssH, pxW, pxH) {
        const gl = this.gl;
        if (this.canvas.width !== pxW || this.canvas.height !== pxH) {
            this.canvas.width = pxW;
            this.canvas.height = pxH;
        }
        gl.viewport(0, 0, pxW, pxH);
        gl.clearColor(0, 0, 0, 0);
        gl.clear(gl.COLOR_BUFFER_BIT);
        gl.useProgram(this.prog);
        const t = makeWorldTransform(w, cssW, cssH);
        gl.uniform4fv(this.uRowX, t.rx);
        gl.uniform4fv(this.uRowY, t.ry);
        this._drawBatch(this.floorBatch, w);
        this._drawBatch(this.liquidBatch, w);
    }
}

// buildLiquidFaces: precompute the per-face liquid entries (flat shade
// against the fixed light, quantised exactly like drawLiquidVolume) so the
// GL batch and its colours are geometry-static.
export function buildLiquidFaces(liquids, baseByKind, alpha, light) {
    const faces = [];
    if (!Array.isArray(liquids)) return faces;
    for (const lq of liquids) {
        if (!lq || !Array.isArray(lq.tris) || lq.tris.length < 9) continue;
        const base = baseByKind[lq.kind] || baseByKind.water;
        const tris = lq.tris;
        for (let i = 0; i + 8 < tris.length; i += 9) {
            const ax = tris[i],     ay = tris[i + 1], az = tris[i + 2];
            const bx = tris[i + 3], by = tris[i + 4], bz = tris[i + 5];
            const cx = tris[i + 6], cy = tris[i + 7], cz = tris[i + 8];
            const ux = bx - ax, uy = by - ay, uz = bz - az;
            const vx = cx - ax, vy = cy - ay, vz = cz - az;
            const nx = uy * vz - uz * vy, ny = uz * vx - ux * vz, nz = ux * vy - uy * vx;
            const nl = Math.hypot(nx, ny, nz) || 1;
            const shade = 0.5 + 0.5 * Math.abs((nx * light[0] + ny * light[1] + nz * light[2]) / nl);
            const q = Math.round(shade * 10) / 10;
            faces.push({
                tris, off: i,
                cx: (ax + bx + cx) / 3,
                cy: (ay + by + cy) / 3,
                cz: (az + bz + cz) / 3,
                fill: `rgba(${Math.round(base[0] * q)}, ${Math.round(base[1] * q)}, ${Math.round(base[2] * q)}, ${alpha})`,
            });
        }
    }
    return faces;
}
