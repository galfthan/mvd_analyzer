// WebGL backend for the static world — the floor model and the liquid
// volumes, i.e. exactly the layers the 2D path bakes to an offscreen canvas
// and re-bakes on every camera-motion frame.
//
// Design notes, in the order they matter:
//
// - **Opaque floors ride the depth buffer.** The floor model's "near-opaque,
//   higher floor covers lower" design is exactly what a z-buffer gives for
//   free, so the opaque pass draws in plain array order — the per-angle
//   painter sort the 2D path needed no longer exists for floors, and the
//   depth buffer this leaves behind is the foundation for occlusion, fog
//   and the other depth-aware effects. Genuinely translucent content
//   (liquids; the focus fade) blends on top with depth reads but no writes;
//   liquids keep a back-to-front sort re-keyed only on camera angle.
//
// - **No seam hack.** The 2D path strokes every triangle batch with its own
//   fill colour to seal anti-aliasing seams between adjacent triangles
//   (see renderSolidEntries). GL rasterisation of shared-edge triangles in
//   one draw call has no such seams, so the hack — and the double
//   rasterisation it cost — simply disappears.
//
// - **The camera is three row vectors.** project() is affine in (x, y, z):
//   u/v are linear maps of the rotated point, the screen map is linear, and
//   camera closeness is affine too. makeWorldTransform folds the whole
//   thing into row vectors for clip x/y/z, computed on the CPU each frame.
//
// - **The loader invariant holds.** This module receives its WebGL context
//   from the caller (who created the backing canvas via
//   canvas.ownerDocument.createElement) and touches no ambient global.

// Parse an rgba()/rgb()/#rrggbb string into premultiplied [r, g, b, a]
// floats. Memoised — the floor model reuses a handful of distinct colour
// strings across thousands of entries.
const ONE_TINT = [1, 1, 1, 1];

const _colorCache = new Map();
export function parseColor(str) {
    let c = _colorCache.get(str);
    if (c) return c;
    const m = /rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)/.exec(str);
    const h = /^#([0-9a-f]{6})$/i.exec(str);
    if (m) {
        const a = m[4] === undefined ? 1 : parseFloat(m[4]);
        c = [(+m[1] / 255) * a, (+m[2] / 255) * a, (+m[3] / 255) * a, a];
    } else if (h) {
        const v = parseInt(h[1], 16);
        c = [((v >> 16) & 255) / 255, ((v >> 8) & 255) / 255, (v & 255) / 255, 1];
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
// Clip-space depth scale: camera-closeness (world units) × DEPTH_SCALE →
// clip z. Quake worlds live within ±4096 units per axis, so closeness stays
// well inside ±32768; a 24-bit depth buffer over that range still resolves
// ~256 steps per world unit.
export const DEPTH_SCALE = 1 / 32768;

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
    // closeness = -sinYaw*cosPitch*x - cosYaw*cosPitch*y + sinPitch*z + cd
    // (entryDepth as a row vector). Nearer fragments must win the LESS
    // depth test, so clipZ = -closeness * DEPTH_SCALE.
    const cd = (w.cx * w.sinYaw + w.cy * w.cosYaw) * w.cosPitch - w.zMid * w.sinPitch;
    const kz = -DEPTH_SCALE;
    return {
        rx: [kx * w.cosYaw, -kx * w.sinYaw, 0, kx * cu + (2 * bx) / cssW - 1],
        ry: [ky * w.sinYaw * w.sinPitch, ky * w.cosYaw * w.sinPitch, ky * w.cosPitch,
             ky * cv - (2 * by) / cssH + 1],
        rz: [kz * -w.sinYaw * w.cosPitch, kz * -w.cosYaw * w.cosPitch,
             kz * w.sinPitch, kz * cd],
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
// `forceOpaque` snaps alpha to 1 (the depth-buffered opaque pass — floor
// fills carry the historical 0.95, which the z-buffer makes meaningless).
// Also returns the per-entry centroids for the angle sort.
export function buildEntryVertices(entries, colorOf, forceOpaque = false) {
    const verts = new Float32Array(entries.length * 3 * 7);
    const centroids = new Float32Array(entries.length * 3);
    let vi = 0;
    for (let e = 0; e < entries.length; e++) {
        const entry = entries[e];
        let [r, g, b, a] = parseColor(colorOf(entry));
        if (forceOpaque && a > 0) {
            r /= a; g /= a; b /= a; a = 1;
        }
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

// rendererString: the context's renderer identity. Browsers expose the
// unmasked GPU string through WEBGL_debug_renderer_info; fall back to the
// plain RENDERER when the extension is absent.
export function rendererString(gl) {
    try {
        const dbg = gl.getExtension('WEBGL_debug_renderer_info');
        if (dbg) return String(gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) || '');
        return String(gl.getParameter(gl.RENDERER) || '');
    } catch (e) {
        return '';
    }
}

// isSoftwareRenderer: the known software-rasteriser identities Chrome and
// Mesa report on machines without a usable GPU.
export function isSoftwareRenderer(renderer) {
    return /swiftshader|llvmpipe|softpipe|software|basic render/i.test(renderer);
}

const VERT_SRC = `#version 300 es
uniform vec4 uRowX;
uniform vec4 uRowY;
uniform vec4 uRowZ;
uniform vec3 uOffset;
uniform vec4 uTint;
in vec3 aPos;
in vec4 aColor;
out vec4 vColor;
void main() {
    vec4 p = vec4(aPos + uOffset, 1.0);
    gl_Position = vec4(dot(uRowX, p), dot(uRowY, p), dot(uRowZ, p), 1.0);
    vColor = aColor * uTint;
}`;

const FRAG_SRC = `#version 300 es
precision mediump float;
in vec4 vColor;
out vec4 outColor;
void main() { outColor = vColor; }`;

// Point sprites (projectile / nail dots; trail and actor markers):
// world-space centre, per-point size in physical pixels, shape cut in the
// fragment shader — 0: disc, 1: ✕, 2: ring (param = inner radius fraction),
// 3: filled square, 4: square outline (param = border fraction).
const POINT_VERT_SRC = `#version 300 es
uniform vec4 uRowX;
uniform vec4 uRowY;
uniform vec4 uRowZ;
uniform vec2 uViewPx;
in vec3 aPos;
in vec2 aOff;     // screen-space offset in physical px (badge orbit slots)
in vec4 aColor;
in float aSize;
in float aShape;
in float aParam;
out vec4 vColor;
flat out float vShape;
flat out float vParam;
void main() {
    vec4 p = vec4(aPos, 1.0);
    vec3 clip = vec3(dot(uRowX, p), dot(uRowY, p), dot(uRowZ, p));
    vec2 px = vec2((clip.x + 1.0) * 0.5 * uViewPx.x, (1.0 - clip.y) * 0.5 * uViewPx.y) + aOff;
    gl_Position = vec4(px.x / uViewPx.x * 2.0 - 1.0, 1.0 - px.y / uViewPx.y * 2.0, clip.z, 1.0);
    gl_PointSize = aSize;
    vColor = aColor;
    vShape = aShape;
    vParam = aParam;
}`;

const POINT_FRAG_SRC = `#version 300 es
precision mediump float;
in vec4 vColor;
flat in float vShape;
flat in float vParam;
out vec4 outColor;
void main() {
    vec2 c = gl_PointCoord * 2.0 - 1.0;
    if (vShape < 0.5) {
        if (dot(c, c) > 1.0) discard;                  // disc
    } else if (vShape < 1.5) {
        if (abs(abs(c.x) - abs(c.y)) > 0.4) discard;   // ✕ arms
    } else if (vShape < 2.5) {
        float r2 = dot(c, c);                          // ring
        if (r2 > 1.0 || r2 < vParam * vParam) discard;
    } else if (vShape < 3.5) {
        // filled square — nothing cut
    } else {
        if (max(abs(c.x), abs(c.y)) < 1.0 - vParam) discard;  // square outline
    }
    outColor = vColor;
}`;

// Textured billboards: a world-space anchor plus a physical-pixel corner
// offset, sampling the label atlas. Tint multiplies (fades the drop-D).
const SPRITE_VERT_SRC = `#version 300 es
uniform vec4 uRowX;
uniform vec4 uRowY;
uniform vec4 uRowZ;
uniform vec2 uViewPx;
in vec3 aPos;
in vec2 aOffPx;
in vec2 aUV;
in vec4 aTint;
out vec2 vUV;
out vec4 vTint;
void main() {
    vec4 p = vec4(aPos, 1.0);
    vec3 clip = vec3(dot(uRowX, p), dot(uRowY, p), dot(uRowZ, p));
    vec2 px = vec2((clip.x + 1.0) * 0.5 * uViewPx.x, (1.0 - clip.y) * 0.5 * uViewPx.y) + aOffPx;
    gl_Position = vec4(px.x / uViewPx.x * 2.0 - 1.0, 1.0 - px.y / uViewPx.y * 2.0, clip.z, 1.0);
    vUV = aUV;
    vTint = aTint;
}`;

const SPRITE_FRAG_SRC = `#version 300 es
precision mediump float;
uniform sampler2D uTex;
in vec2 vUV;
in vec4 vTint;
out vec4 outColor;
void main() { outColor = texture(uTex, vUV) * vTint; }`;

// Plain screen-space triangles (arrowheads): physical-pixel coordinates in,
// flat colour out.
const TRI2D_VERT_SRC = `#version 300 es
uniform vec2 uViewPx;
in vec2 aPx;
in vec4 aColor;
out vec4 vColor;
void main() {
    gl_Position = vec4(aPx.x / uViewPx.x * 2.0 - 1.0, 1.0 - aPx.y / uViewPx.y * 2.0, 0.0, 1.0);
    vColor = aColor;
}`;

// Screen-space extruded line segments (beams; later trails / sightlines).
// Each vertex carries both endpoints plus (side, end): the shader projects
// the endpoints, extrudes perpendicular in physical-pixel space by the
// half-width, and re-emits clip coordinates — constant on-screen width at
// any camera.
const LINE_VERT_SRC = `#version 300 es
uniform vec4 uRowX;
uniform vec4 uRowY;
uniform vec4 uRowZ;
uniform vec2 uViewPx;
uniform vec4 uTint;        // multiplies aColor — lets cached geometry restyle per draw
uniform float uWidthScale; // multiplies aHalfWidth — same reason
in vec3 aStart;
in vec3 aEnd;
in vec2 aParam;   // x: side (-1 | 1), y: end (0 | 1)
in vec4 aColor;
in float aHalfWidth; // physical px (pre-scale)
in vec2 aDash;       // (on px, off px); (0, 0) = solid
out vec4 vColor;
out float vDistPx;
flat out vec2 vDash;
vec3 clipOf(vec3 world) {
    vec4 p = vec4(world, 1.0);
    return vec3(dot(uRowX, p), dot(uRowY, p), dot(uRowZ, p));
}
void main() {
    vec3 s = clipOf(aStart);
    vec3 e = clipOf(aEnd);
    vec2 sPx = vec2((s.x + 1.0) * 0.5 * uViewPx.x, (1.0 - s.y) * 0.5 * uViewPx.y);
    vec2 ePx = vec2((e.x + 1.0) * 0.5 * uViewPx.x, (1.0 - e.y) * 0.5 * uViewPx.y);
    vec2 d = ePx - sPx;
    float len = max(length(d), 1e-6);
    vec2 n = vec2(-d.y, d.x) / len;
    vec2 base = mix(sPx, ePx, aParam.y);
    vec2 px = base + n * aParam.x * aHalfWidth * uWidthScale;
    float z = mix(s.z, e.z, aParam.y);
    gl_Position = vec4(px.x / uViewPx.x * 2.0 - 1.0, 1.0 - px.y / uViewPx.y * 2.0, z, 1.0);
    vColor = aColor * uTint;
    vDistPx = aParam.y * len;
    vDash = aDash;
}`;

// Line fragments honour the per-segment dash pattern (distance along the
// segment in physical px — each segment restarts its phase, which matches
// how the dashes are actually used: a dashed segment IS one teleport jump).
const LINE_FRAG_SRC = `#version 300 es
precision mediump float;
in vec4 vColor;
in float vDistPx;
flat in vec2 vDash;
out vec4 outColor;
void main() {
    if (vDash.x > 0.0 && mod(vDistPx, vDash.x + vDash.y) > vDash.x) discard;
    outColor = vColor;
}`;

// One batch of triangles. Opaque batches draw in array order under the
// depth test — no sorting exists for them at all. Sortable (translucent)
// batches own an angle-sorted index buffer for back-to-front blending.
class GlBatch {
    constructor(gl, verts, centroids, { sortable = false } = {}) {
        this.gl = gl;
        this.count = centroids.length / 3;
        this.centroids = centroids;
        this.vbo = gl.createBuffer();
        gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, verts, gl.STATIC_DRAW);
        this.sortable = sortable;
        this.ibo = sortable ? gl.createBuffer() : null;
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
        if (this.ibo) this.gl.deleteBuffer(this.ibo);
    }
}

// GlWorld: the renderer. Owns the context, the shader program and the
// current batches; rebuilds them when the floor model, the liquids or the
// focus predicate change (rare, user-driven), and otherwise renders any
// camera with two uniform vec4s.
export class GlWorld {
    // `canvas` is the offscreen backing canvas the caller created. Returns a
    // working renderer or null when WebGL2 is unavailable, the context is a
    // software rasteriser (unless `allowSoftware`), or the program fails to
    // build — the caller falls back to the 2D path in every case.
    static create(canvas, { allowSoftware = false } = {}) {
        try {
            const gl = canvas.getContext('webgl2', {
                alpha: true,
                antialias: true,
                depth: true,
                premultipliedAlpha: true,
                preserveDrawingBuffer: false,
            });
            if (!gl) return null;
            // A GPU-less machine gets SwiftShader/llvmpipe, and software GL
            // is strictly slower than the canvas-2D painter it would replace
            // (measured: ~45 ms vs ~32 ms per rotated dm3 frame). Prefer 2D
            // there; `allowSoftware` is the tester's override.
            if (!allowSoftware && isSoftwareRenderer(rendererString(gl))) return null;
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
        const link = (vs, fs) => {
            const prog = gl.createProgram();
            gl.attachShader(prog, compile(gl.VERTEX_SHADER, vs));
            gl.attachShader(prog, compile(gl.FRAGMENT_SHADER, fs));
            gl.linkProgram(prog);
            if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) {
                throw new Error('link: ' + gl.getProgramInfoLog(prog));
            }
            return prog;
        };

        const tri = link(VERT_SRC, FRAG_SRC);
        this.prog = tri;
        this.uRowX = gl.getUniformLocation(tri, 'uRowX');
        this.uRowY = gl.getUniformLocation(tri, 'uRowY');
        this.uRowZ = gl.getUniformLocation(tri, 'uRowZ');
        this.uOffset = gl.getUniformLocation(tri, 'uOffset');
        this.uTint = gl.getUniformLocation(tri, 'uTint');
        this.aPos = gl.getAttribLocation(tri, 'aPos');
        this.aColor = gl.getAttribLocation(tri, 'aColor');

        const pt = link(POINT_VERT_SRC, POINT_FRAG_SRC);
        this.pointProg = {
            prog: pt,
            uRowX: gl.getUniformLocation(pt, 'uRowX'),
            uRowY: gl.getUniformLocation(pt, 'uRowY'),
            uRowZ: gl.getUniformLocation(pt, 'uRowZ'),
            aPos: gl.getAttribLocation(pt, 'aPos'),
            aColor: gl.getAttribLocation(pt, 'aColor'),
            aSize: gl.getAttribLocation(pt, 'aSize'),
            aShape: gl.getAttribLocation(pt, 'aShape'),
            aParam: gl.getAttribLocation(pt, 'aParam'),
            aOff: gl.getAttribLocation(pt, 'aOff'),
            uViewPx: gl.getUniformLocation(pt, 'uViewPx'),
            vbo: gl.createBuffer(),
        };

        const sp = link(SPRITE_VERT_SRC, SPRITE_FRAG_SRC);
        this.spriteProg = {
            prog: sp,
            uRowX: gl.getUniformLocation(sp, 'uRowX'),
            uRowY: gl.getUniformLocation(sp, 'uRowY'),
            uRowZ: gl.getUniformLocation(sp, 'uRowZ'),
            uViewPx: gl.getUniformLocation(sp, 'uViewPx'),
            uTex: gl.getUniformLocation(sp, 'uTex'),
            aPos: gl.getAttribLocation(sp, 'aPos'),
            aOffPx: gl.getAttribLocation(sp, 'aOffPx'),
            aUV: gl.getAttribLocation(sp, 'aUV'),
            aTint: gl.getAttribLocation(sp, 'aTint'),
            vbo: gl.createBuffer(),
        };
        this.atlasTex = gl.createTexture();
        gl.bindTexture(gl.TEXTURE_2D, this.atlasTex);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
        gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
        this._atlasGeneration = -1;

        const t2 = link(TRI2D_VERT_SRC, FRAG_SRC);
        this.tri2dProg = {
            prog: t2,
            uViewPx: gl.getUniformLocation(t2, 'uViewPx'),
            aPx: gl.getAttribLocation(t2, 'aPx'),
            aColor: gl.getAttribLocation(t2, 'aColor'),
            vbo: gl.createBuffer(),
        };

        const ln = link(LINE_VERT_SRC, LINE_FRAG_SRC);
        this.lineProg = {
            prog: ln,
            uRowX: gl.getUniformLocation(ln, 'uRowX'),
            uRowY: gl.getUniformLocation(ln, 'uRowY'),
            uRowZ: gl.getUniformLocation(ln, 'uRowZ'),
            uViewPx: gl.getUniformLocation(ln, 'uViewPx'),
            uTint: gl.getUniformLocation(ln, 'uTint'),
            uWidthScale: gl.getUniformLocation(ln, 'uWidthScale'),
            aStart: gl.getAttribLocation(ln, 'aStart'),
            aEnd: gl.getAttribLocation(ln, 'aEnd'),
            aParam: gl.getAttribLocation(ln, 'aParam'),
            aColor: gl.getAttribLocation(ln, 'aColor'),
            aHalfWidth: gl.getAttribLocation(ln, 'aHalfWidth'),
            aDash: gl.getAttribLocation(ln, 'aDash'),
            vbo: gl.createBuffer(),
        };

        // Premultiplied-alpha over for the blended passes — matches the
        // canvas compositor.
        gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
        gl.depthFunc(gl.LESS);
        this.floorOpaque = null;   // depth-tested, unsorted
        this.floorFaded = null;    // focus-faded regions — blended, no depth write
        this.liquidBatch = null;   // blended, angle-sorted
        this._floorModel = null;
        this._floorFocus = null;
        this._liquidFaces = null;
        this._moverMeshes = new Map();  // submodel id -> {vbo, count}
        // Region-overlay geometry, keyed by the source array's identity —
        // group tris / outlines are rebuilt wholesale on geometry reload, so
        // a WeakMap keeps exactly the live generation alive.
        this._fillVbos = new WeakMap();     // group tris -> {vbo, count}
        this._outlineVbos = new WeakMap();  // group outline -> {vbo, count}
    }

    // syncFloor (re)builds the floor batches when the model or the focus
    // changed — both rare, user-driven events. Model identity is the cache
    // key (floorModel() memoises per geometry+groups). The entries split by
    // focus tier: everything outside the focus fade renders opaque under the
    // depth test (order-free), the faded remainder blends on top without
    // depth writes.
    syncFloor(model, focusName, isFaded) {
        const focus = focusName ?? null;
        if (model === this._floorModel && focus === this._floorFocus) return;
        if (this.floorOpaque) { this.floorOpaque.dispose(); this.floorOpaque = null; }
        if (this.floorFaded) { this.floorFaded.dispose(); this.floorFaded = null; }
        this._floorModel = model;
        this._floorFocus = focus;
        if (!model) return;
        const opaque = [];
        const faded = [];
        for (const e of model.entries) {
            if (isFaded && isFaded(e.name)) faded.push(e);
            else opaque.push(e);
        }
        if (opaque.length > 0) {
            const { verts, centroids } = buildEntryVertices(opaque, (e) => e.fill, true);
            this.floorOpaque = new GlBatch(this.gl, verts, centroids);
        }
        if (faded.length > 0) {
            const { verts, centroids } = buildEntryVertices(faded, (e) => e.fillFaded);
            this.floorFaded = new GlBatch(this.gl, verts, centroids);
        }
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
        this.liquidBatch = new GlBatch(this.gl, verts, centroids, { sortable: true });
    }

    _bindBatch(batch) {
        const gl = this.gl;
        gl.bindBuffer(gl.ARRAY_BUFFER, batch.vbo);
        gl.enableVertexAttribArray(this.aPos);
        gl.vertexAttribPointer(this.aPos, 3, gl.FLOAT, false, 28, 0);
        gl.enableVertexAttribArray(this.aColor);
        gl.vertexAttribPointer(this.aColor, 4, gl.FLOAT, false, 28, 12);
    }

    // Opaque pass: depth-tested, depth-written, array order — the GPU's
    // z-buffer does what the 2D path needed a per-angle painter sort for.
    _drawOpaque(batch) {
        if (!batch) return;
        const gl = this.gl;
        this._bindBatch(batch);
        gl.drawArrays(gl.TRIANGLES, 0, batch.count * 3);
    }

    // Blended pass: reads depth (so opaque geometry occludes it) but never
    // writes it. `sorted` draws back-to-front through the angle-keyed index
    // buffer (liquids — visibly translucent volumes); unsorted is for the
    // focus fade, whose alpha is too low for ordering to read.
    _drawBlended(batch, w) {
        if (!batch) return;
        const gl = this.gl;
        this._bindBatch(batch);
        if (batch.sortable) {
            batch.ensureSorted(w);
            gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, batch.ibo);
            gl.drawElements(gl.TRIANGLES, batch.count * 3, gl.UNSIGNED_INT, 0);
        } else {
            gl.drawArrays(gl.TRIANGLES, 0, batch.count * 3);
        }
    }

    // _moverMesh: a submodel's mesh as a position-only VBO, uploaded once
    // per id (poses are pure translations, applied via uOffset).
    _moverMesh(sub, tris) {
        let m = this._moverMeshes.get(sub);
        if (m) return m;
        const gl = this.gl;
        const verts = new Float32Array(tris.length);
        verts.set(tris);
        const vbo = gl.createBuffer();
        gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
        gl.bufferData(gl.ARRAY_BUFFER, verts, gl.STATIC_DRAW);
        m = { vbo, count: tris.length / 3 };
        this._moverMeshes.set(sub, m);
        return m;
    }

    // _drawMovers: opaque, depth-tested meshes at their per-frame poses.
    // The z-buffer replaces the 2D path's backface-culled silhouette trick:
    // drawing every face flat-coloured under depth testing produces the same
    // near hull. `movers` is [{sub, tris, x, y, z, tint: [r,g,b,a] premult}].
    _drawMovers(movers) {
        const gl = this.gl;
        gl.disableVertexAttribArray(this.aColor);
        gl.vertexAttrib4f(this.aColor, 1, 1, 1, 1);
        for (const m of movers) {
            const mesh = this._moverMesh(m.sub, m.tris);
            gl.bindBuffer(gl.ARRAY_BUFFER, mesh.vbo);
            gl.enableVertexAttribArray(this.aPos);
            gl.vertexAttribPointer(this.aPos, 3, gl.FLOAT, false, 12, 0);
            gl.uniform3f(this.uOffset, m.x, m.y, m.z);
            gl.uniform4fv(this.uTint, m.tint);
            gl.drawArrays(gl.TRIANGLES, 0, mesh.count);
        }
        gl.uniform3f(this.uOffset, 0, 0, 0);
        gl.uniform4f(this.uTint, 1, 1, 1, 1);
    }

    // _drawFills: translucent region tints ([{tris, tint: [r,g,b,a]}]) —
    // per-group position VBOs cached by the tris array's identity, coloured
    // by the tint uniform, in list order (control under occupancy).
    _drawFills(fills) {
        if (!fills || fills.length === 0) return;
        const gl = this.gl;
        gl.disableVertexAttribArray(this.aColor);
        gl.vertexAttrib4f(this.aColor, 1, 1, 1, 1);
        for (const f of fills) {
            let mesh = this._fillVbos.get(f.tris);
            if (!mesh) {
                const vbo = gl.createBuffer();
                gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
                gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(f.tris), gl.STATIC_DRAW);
                mesh = { vbo, count: f.tris.length / 3 };
                this._fillVbos.set(f.tris, mesh);
            }
            gl.bindBuffer(gl.ARRAY_BUFFER, mesh.vbo);
            gl.enableVertexAttribArray(this.aPos);
            gl.vertexAttribPointer(this.aPos, 3, gl.FLOAT, false, 12, 0);
            gl.uniform4fv(this.uTint, f.tint);
            gl.drawArrays(gl.TRIANGLES, 0, mesh.count);
        }
        gl.uniform4f(this.uTint, 1, 1, 1, 1);
    }

    // _drawOutlines: region boundary strokes ([{outline, tint, halfWidth}])
    // through the quad-line program. The segment geometry is camera-free
    // (extrusion happens in the shader), so each outline bakes to a static
    // VBO; colour and width restyle per draw via uniforms.
    _drawOutlines(outlines, t, pxW, pxH) {
        if (!outlines || outlines.length === 0) return;
        const gl = this.gl;
        const p = this.lineProg;
        gl.useProgram(p.prog);
        gl.uniform4fv(p.uRowX, t.rx);
        gl.uniform4fv(p.uRowY, t.ry);
        gl.uniform4fv(p.uRowZ, t.rz);
        gl.uniform2f(p.uViewPx, pxW, pxH);
        const CORNERS = [[-1, 0], [1, 0], [1, 1], [-1, 0], [1, 1], [-1, 1]];
        for (const o of outlines) {
            let mesh = this._outlineVbos.get(o.outline);
            if (!mesh) {
                const segs = o.outline.length / 6;
                const data = new Float32Array(segs * 6 * 15);
                let i = 0;
                for (let s = 0; s + 5 < o.outline.length; s += 6) {
                    for (const [side, end] of CORNERS) {
                        data[i++] = o.outline[s];     data[i++] = o.outline[s + 1]; data[i++] = o.outline[s + 2];
                        data[i++] = o.outline[s + 3]; data[i++] = o.outline[s + 4]; data[i++] = o.outline[s + 5];
                        data[i++] = side; data[i++] = end;
                        data[i++] = 1; data[i++] = 1; data[i++] = 1; data[i++] = 1;
                        data[i++] = 1;
                        data[i++] = 0; data[i++] = 0;
                    }
                }
                const vbo = gl.createBuffer();
                gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
                gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW);
                mesh = { vbo, count: segs * 6 };
                this._outlineVbos.set(o.outline, mesh);
            }
            gl.bindBuffer(gl.ARRAY_BUFFER, mesh.vbo);
            this._bindLineAttribs();
            gl.uniform4fv(p.uTint, o.tint);
            gl.uniform1f(p.uWidthScale, o.halfWidth);
            gl.drawArrays(gl.TRIANGLES, 0, mesh.count);
        }
        gl.uniform4f(p.uTint, 1, 1, 1, 1);
        gl.uniform1f(p.uWidthScale, 1);
        // Back to the triangle program for whoever draws next.
        gl.useProgram(this.prog);
    }

    _bindLineAttribs() {
        const gl = this.gl;
        const p = this.lineProg;
        const stride = 60;
        gl.enableVertexAttribArray(p.aStart);
        gl.vertexAttribPointer(p.aStart, 3, gl.FLOAT, false, stride, 0);
        gl.enableVertexAttribArray(p.aEnd);
        gl.vertexAttribPointer(p.aEnd, 3, gl.FLOAT, false, stride, 12);
        gl.enableVertexAttribArray(p.aParam);
        gl.vertexAttribPointer(p.aParam, 2, gl.FLOAT, false, stride, 24);
        gl.enableVertexAttribArray(p.aColor);
        gl.vertexAttribPointer(p.aColor, 4, gl.FLOAT, false, stride, 32);
        gl.enableVertexAttribArray(p.aHalfWidth);
        gl.vertexAttribPointer(p.aHalfWidth, 1, gl.FLOAT, false, stride, 48);
        gl.enableVertexAttribArray(p.aDash);
        gl.vertexAttribPointer(p.aDash, 2, gl.FLOAT, false, stride, 52);
    }

    // _drawPoints: shaped dots ([{x, y, z, size(px), shape, param, offX,
    // offY, color: [r,g,b,a]}]) as point sprites from a per-frame stream
    // buffer. offX/offY hang the sprite at a screen offset from its anchor
    // (badge orbit slots).
    _drawPoints(points, t, pxW, pxH) {
        if (!points || points.length === 0) return;
        const gl = this.gl;
        const p = this.pointProg;
        const data = new Float32Array(points.length * 12);
        let i = 0;
        for (const pt of points) {
            data[i++] = pt.x; data[i++] = pt.y; data[i++] = pt.z;
            data[i++] = pt.offX || 0; data[i++] = pt.offY || 0;
            data[i++] = pt.color[0]; data[i++] = pt.color[1];
            data[i++] = pt.color[2]; data[i++] = pt.color[3];
            data[i++] = pt.size;
            data[i++] = pt.shape || 0;
            data[i++] = pt.param || 0;
        }
        gl.useProgram(p.prog);
        gl.uniform4fv(p.uRowX, t.rx);
        gl.uniform4fv(p.uRowY, t.ry);
        gl.uniform4fv(p.uRowZ, t.rz);
        gl.uniform2f(p.uViewPx, pxW, pxH);
        gl.bindBuffer(gl.ARRAY_BUFFER, p.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, data, gl.STREAM_DRAW);
        const stride = 48;
        gl.enableVertexAttribArray(p.aPos);
        gl.vertexAttribPointer(p.aPos, 3, gl.FLOAT, false, stride, 0);
        gl.enableVertexAttribArray(p.aOff);
        gl.vertexAttribPointer(p.aOff, 2, gl.FLOAT, false, stride, 12);
        gl.enableVertexAttribArray(p.aColor);
        gl.vertexAttribPointer(p.aColor, 4, gl.FLOAT, false, stride, 20);
        gl.enableVertexAttribArray(p.aSize);
        gl.vertexAttribPointer(p.aSize, 1, gl.FLOAT, false, stride, 36);
        gl.enableVertexAttribArray(p.aShape);
        gl.vertexAttribPointer(p.aShape, 1, gl.FLOAT, false, stride, 40);
        gl.enableVertexAttribArray(p.aParam);
        gl.vertexAttribPointer(p.aParam, 1, gl.FLOAT, false, stride, 44);
        gl.drawArrays(gl.POINTS, 0, points.length);
    }

    // syncAtlas uploads the label atlas page when it changed.
    syncAtlas(atlas) {
        if (!atlas || (!atlas.dirty && this._atlasGeneration === atlas.generation)) return;
        const gl = this.gl;
        gl.bindTexture(gl.TEXTURE_2D, this.atlasTex);
        gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, true);
        gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, atlas.canvas);
        gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
        atlas.dirty = false;
        this._atlasGeneration = atlas.generation;
    }

    // _drawSprites: textured billboards ([{x, y, z, offX, offY, w, h, e:
    // atlas entry, tint?}]) — a world anchor plus a physical-pixel corner
    // offset, sampling the atlas.
    _drawSprites(sprites, t, pxW, pxH) {
        if (!sprites || sprites.length === 0) return;
        const gl = this.gl;
        const p = this.spriteProg;
        const data = new Float32Array(sprites.length * 6 * 11);
        let i = 0;
        for (const sp of sprites) {
            const e = sp.e;
            const tint = sp.tint || ONE_TINT;
            const corners = [
                [sp.offX, sp.offY, e.u0, e.v0],
                [sp.offX + sp.w, sp.offY, e.u1, e.v0],
                [sp.offX + sp.w, sp.offY + sp.h, e.u1, e.v1],
                [sp.offX, sp.offY, e.u0, e.v0],
                [sp.offX + sp.w, sp.offY + sp.h, e.u1, e.v1],
                [sp.offX, sp.offY + sp.h, e.u0, e.v1],
            ];
            for (const [ox, oy, u, v] of corners) {
                data[i++] = sp.x; data[i++] = sp.y; data[i++] = sp.z;
                data[i++] = ox; data[i++] = oy;
                data[i++] = u; data[i++] = v;
                data[i++] = tint[0]; data[i++] = tint[1];
                data[i++] = tint[2]; data[i++] = tint[3];
            }
        }
        gl.useProgram(p.prog);
        gl.uniform4fv(p.uRowX, t.rx);
        gl.uniform4fv(p.uRowY, t.ry);
        gl.uniform4fv(p.uRowZ, t.rz);
        gl.uniform2f(p.uViewPx, pxW, pxH);
        gl.activeTexture(gl.TEXTURE0);
        gl.bindTexture(gl.TEXTURE_2D, this.atlasTex);
        gl.uniform1i(p.uTex, 0);
        gl.bindBuffer(gl.ARRAY_BUFFER, p.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, data, gl.STREAM_DRAW);
        const stride = 44;
        gl.enableVertexAttribArray(p.aPos);
        gl.vertexAttribPointer(p.aPos, 3, gl.FLOAT, false, stride, 0);
        gl.enableVertexAttribArray(p.aOffPx);
        gl.vertexAttribPointer(p.aOffPx, 2, gl.FLOAT, false, stride, 12);
        gl.enableVertexAttribArray(p.aUV);
        gl.vertexAttribPointer(p.aUV, 2, gl.FLOAT, false, stride, 20);
        gl.enableVertexAttribArray(p.aTint);
        gl.vertexAttribPointer(p.aTint, 4, gl.FLOAT, false, stride, 28);
        gl.drawArrays(gl.TRIANGLES, 0, sprites.length * 6);
    }

    // _drawScreenTris: flat-coloured physical-pixel triangles
    // ([{pts: [x0,y0,x1,y1,x2,y2], color}]) — the arrowheads.
    _drawScreenTris(tris, pxW, pxH) {
        if (!tris || tris.length === 0) return;
        const gl = this.gl;
        const p = this.tri2dProg;
        const data = new Float32Array(tris.length * 3 * 6);
        let i = 0;
        for (const tr of tris) {
            for (let v = 0; v < 6; v += 2) {
                data[i++] = tr.pts[v]; data[i++] = tr.pts[v + 1];
                data[i++] = tr.color[0]; data[i++] = tr.color[1];
                data[i++] = tr.color[2]; data[i++] = tr.color[3];
            }
        }
        gl.useProgram(p.prog);
        gl.uniform2f(p.uViewPx, pxW, pxH);
        gl.bindBuffer(gl.ARRAY_BUFFER, p.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, data, gl.STREAM_DRAW);
        gl.enableVertexAttribArray(p.aPx);
        gl.vertexAttribPointer(p.aPx, 2, gl.FLOAT, false, 24, 0);
        gl.enableVertexAttribArray(p.aColor);
        gl.vertexAttribPointer(p.aColor, 4, gl.FLOAT, false, 24, 8);
        gl.drawArrays(gl.TRIANGLES, 0, tris.length * 3);
    }

    // _drawLines: screen-space extruded segments
    // ([{sx..sz, ex..ez, halfWidth(px), color: [r,g,b,a]}]).
    _drawLines(segs, t, pxW, pxH) {
        if (!segs || segs.length === 0) return;
        const gl = this.gl;
        const p = this.lineProg;
        // Two triangles per segment; 15 floats per vertex.
        const CORNERS = [[-1, 0], [1, 0], [1, 1], [-1, 0], [1, 1], [-1, 1]];
        const data = new Float32Array(segs.length * 6 * 15);
        let i = 0;
        for (const s of segs) {
            const dashOn = s.dash ? s.dash[0] : 0;
            const dashOff = s.dash ? s.dash[1] : 0;
            for (const [side, end] of CORNERS) {
                data[i++] = s.sx; data[i++] = s.sy; data[i++] = s.sz;
                data[i++] = s.ex; data[i++] = s.ey; data[i++] = s.ez;
                data[i++] = side; data[i++] = end;
                data[i++] = s.color[0]; data[i++] = s.color[1];
                data[i++] = s.color[2]; data[i++] = s.color[3];
                data[i++] = s.halfWidth;
                data[i++] = dashOn; data[i++] = dashOff;
            }
        }
        gl.useProgram(p.prog);
        gl.uniform4fv(p.uRowX, t.rx);
        gl.uniform4fv(p.uRowY, t.ry);
        gl.uniform4fv(p.uRowZ, t.rz);
        gl.uniform2f(p.uViewPx, pxW, pxH);
        gl.uniform4f(p.uTint, 1, 1, 1, 1);
        gl.uniform1f(p.uWidthScale, 1);
        gl.bindBuffer(gl.ARRAY_BUFFER, p.vbo);
        gl.bufferData(gl.ARRAY_BUFFER, data, gl.STREAM_DRAW);
        this._bindLineAttribs();
        gl.drawArrays(gl.TRIANGLES, 0, segs.length * 6);
    }

    // render draws the world for the given camera into the backing canvas,
    // sized to pxW × pxH physical pixels; cssW/cssH are the logical size the
    // camera math targets. Pass order (mirrors the 2D layer stack):
    //   1. opaque floors + movers — depth-tested and -written
    //   2. focus fade + liquids — blended, depth-read-only
    //   3. thin region outlines — the quiet baseline strokes
    //   4. region tints (control under occupancy) + the occupied outlines
    //   5. weapon fire (projectile dots, beam lines) — on top, always visible
    // Everything from 3 down draws depth-test-off, like the 2D overlays did.
    // `dyn` is the per-frame dynamic content from MvdMap._glDynamic.
    render(w, cssW, cssH, pxW, pxH, dyn = {}) {
        const gl = this.gl;
        if (this.canvas.width !== pxW || this.canvas.height !== pxH) {
            this.canvas.width = pxW;
            this.canvas.height = pxH;
        }
        gl.viewport(0, 0, pxW, pxH);
        gl.clearColor(0, 0, 0, 0);
        gl.clearDepth(1);
        gl.depthMask(true);
        gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
        gl.useProgram(this.prog);
        const t = makeWorldTransform(w, cssW, cssH);
        gl.uniform4fv(this.uRowX, t.rx);
        gl.uniform4fv(this.uRowY, t.ry);
        gl.uniform4fv(this.uRowZ, t.rz);
        gl.uniform3f(this.uOffset, 0, 0, 0);
        gl.uniform4f(this.uTint, 1, 1, 1, 1);

        gl.enable(gl.DEPTH_TEST);
        gl.disable(gl.BLEND);
        this._drawOpaque(this.floorOpaque);
        if (dyn.movers && dyn.movers.length > 0) this._drawMovers(dyn.movers);

        gl.enable(gl.BLEND);
        gl.depthMask(false);
        this._drawBlended(this.floorFaded, w);
        this._drawBlended(this.liquidBatch, w);

        gl.disable(gl.DEPTH_TEST);
        if (dyn.atlas) this.syncAtlas(dyn.atlas);
        this._drawOutlines(dyn.regionOutlines, t, pxW, pxH);
        this._drawSprites(dyn.labels, t, pxW, pxH);
        gl.useProgram(this.prog);
        this._drawFills(dyn.fills);
        this._drawOutlines(dyn.fillOutlines, t, pxW, pxH);
        this._drawSprites(dyn.boldLabels, t, pxW, pxH);
        // Lines under points: trail/death markers and projectile dots read
        // on top of trail lines, sightlines and beams.
        this._drawLines(dyn.lines, t, pxW, pxH);
        this._drawPoints(dyn.points, t, pxW, pxH);

        // The actor pass: an ordered command list (the caller z-sorts
        // drawables and flushes on primitive-type change, so cross-type
        // occlusion between overlapping actors keeps the painter order).
        if (dyn.actorBatches && dyn.actorBatches.length > 0) {
            for (const b of dyn.actorBatches) {
                if (b.type === 'points') this._drawPoints(b.items, t, pxW, pxH);
                else if (b.type === 'lines') this._drawLines(b.items, t, pxW, pxH);
                else if (b.type === 'sprites') this._drawSprites(b.items, t, pxW, pxH);
                else if (b.type === 'tris') this._drawScreenTris(b.items, pxW, pxH);
            }
        }
        gl.depthMask(true);
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
