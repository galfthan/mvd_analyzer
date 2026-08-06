// The map's orbit camera: an orthographic projection with yaw about the
// world Z axis, a pitch tilt, and a fit/zoom/pan linear map onto canvas
// pixels.
//
// The camera STATE is a plain object owned by the caller (see newCamera) and
// every function here takes it explicitly. That is deliberate: the host app
// still creates the state at script load, before this module has been
// imported, and a reusable component should not care where its state lives.
// When the stateful MvdMap class lands it will simply hold one of these.

// Camera pitch limits: π/2 is top-down (the 2D view); 0 is a true side
// elevation (looking along the horizon). Nothing in the render path divides
// by sinPitch — the zoom anchor works in view space and toWorld guards its
// inverse — so the full range is allowed.
export const PITCH_MAX = Math.PI / 2;
export const PITCH_MIN = 0;

// Yaw lightly snaps to the four cardinal directions when within this margin,
// so "look straight along x / y" is easy to hit by hand. The orbit drag uses
// absolute deltas from the drag start, so dragging through a snap point
// escapes it naturally.
export const YAW_SNAP = 2 * Math.PI / 180;

// Pitch applied by the "3D" toggle button — tilted enough that floors at
// different heights separate clearly, while the layout stays recognizable.
// 55° from top-down is also ~the isometric tilt (true iso ≈ 54.7°).
export const DEFAULT_PITCH = 55 * Math.PI / 180;

// Yaw for the default view — 45° spins the map onto a corner so it reads as a
// classical isometric / three-quarter view (angled in both x and y) rather
// than looking straight down an axis. Clears the ±2° cardinal snap.
export const DEFAULT_YAW = 45 * Math.PI / 180;

export function newCamera() {
    return {
        scale: 1, offsetX: 0, offsetY: 0, minX: 0, minY: 0, canvasH: 0,
        panX: 0, panY: 0, zoomK: 1,
        yaw: 0, pitch: Math.PI / 2,
        cx: 0, cy: 0, zMid: 0, zMidDefault: 0,
        sinYaw: 0, cosYaw: 1, sinPitch: 1, cosPitch: 0,
    };
}

// is3D: true while the camera is rotated off the exact top-down view.
export function is3D(w) {
    return w.pitch < PITCH_MAX - 1e-6 || Math.abs(w.yaw) > 1e-6;
}

export function refreshTrig(w) {
    w.sinYaw = Math.sin(w.yaw);
    w.cosYaw = Math.cos(w.yaw);
    w.sinPitch = Math.sin(w.pitch);
    w.cosPitch = Math.cos(w.pitch);
}

// setAngles: normalize + snap + clamp the orbit angles and refresh the cached
// trig. The math half of the app's setMapCamera — the UI half (syncing the 3D
// toggle, redrawing) belongs to the host.
export function setAngles(w, yaw, pitch) {
    // Normalize yaw to (-π, π] so the numbers stay sane over long sessions.
    const TWO_PI = 2 * Math.PI;
    yaw = ((yaw % TWO_PI) + TWO_PI) % TWO_PI;
    if (yaw > Math.PI) yaw -= TWO_PI;
    // Light cardinal snap (0 / ±90° / 180°).
    const snap = Math.round(yaw / (Math.PI / 2)) * (Math.PI / 2);
    if (Math.abs(yaw - snap) < YAW_SNAP) yaw = snap;
    w.yaw = yaw;
    w.pitch = Math.min(PITCH_MAX, Math.max(PITCH_MIN, pitch));
    refreshTrig(w);
}

// fit: recompute the world→canvas linear map so `bounds` fills a cssW × cssH
// canvas, and re-centre the orbit pivot on the map's XY centre. panX, panY,
// zoomK, yaw and pitch are intentionally preserved across a refit (a window
// resize must not throw away the user's view); zMid is set by the caller from
// the map's loc height range.
export function fit(w, bounds, cssW, cssH) {
    const { minX, maxX, minY, maxY } = bounds;
    const worldWidth = maxX - minX;
    const worldHeight = maxY - minY;
    const scale = Math.min(cssW / worldWidth, cssH / worldHeight);
    w.scale = scale;
    w.offsetX = (cssW - worldWidth * scale) / 2;
    w.offsetY = (cssH - worldHeight * scale) / 2;
    w.minX = minX;
    w.minY = minY;
    w.canvasH = cssH;
    w.cx = (minX + maxX) / 2;
    w.cy = (minY + maxY) / 2;
}

// project: orbit-camera orthographic projection of a world-space point.
// The point is rotated about the map center by yaw (about the world Z axis),
// then tilted by pitch, and the result is pushed through the same
// fit/zoom/pan linear map the 2D view used. At pitch=π/2, yaw=0 this is
// exactly the old 2D transform (x→x, y→y) and depth degenerates to z, so the
// pre-existing z-sort semantics carry over unchanged.
//
// `depth` is camera closeness — bigger means nearer the viewer — used by the
// painter's sort for players / items / entities.
export function project(w, x, y, z, out) {
    const dx = x - w.cx, dy = y - w.cy, dz = (z || 0) - w.zMid;
    const xr = dx * w.cosYaw - dy * w.sinYaw;
    const yr = dx * w.sinYaw + dy * w.cosYaw;
    const u = w.cx + xr;
    const v = w.cy + yr * w.sinPitch + dz * w.cosPitch;
    const sx = w.scale * w.zoomK;
    out.x = w.offsetX + (u - w.minX) * sx + w.panX;
    out.y = w.canvasH - w.offsetY - (v - w.minY) * sx + w.panY;
    out.depth = dz * w.sinPitch - yr * w.cosPitch;
    return out;
}

// toView: invert only the linear screen part of the projection — canvas pixel
// to the rotated view-plane coordinate (u, v) that project feeds into the
// fit/zoom/pan map. Well-defined at any pitch (no division by sinPitch),
// which is what makes the zoom anchor safe all the way down to a horizontal
// camera.
export function toView(w, cx, cy) {
    const sx = w.scale * w.zoomK;
    return {
        u: w.minX + (cx - w.offsetX - w.panX) / sx,
        v: w.minY + (w.canvasH - w.offsetY + w.panY - cy) / sx,
    };
}

// toWorld: inverse of project — canvas pixel to world coord. Needed for the
// orbit-pivot pick and hit-testing. A single screen point maps to a world
// *ray* under the orbit camera, so the inverse is taken on the horizontal
// plane z = zPlane (default: the orbit center height). At top-down this is
// the exact 2D inverse regardless of zPlane. Near pitch 0 that plane is
// edge-on and the true inverse blows up — sinPitch is floored so callers get
// a finite (if approximate) point instead of NaN.
export function toWorld(w, cx, cy, zPlane) {
    const { u, v } = toView(w, cx, cy);
    const xr = u - w.cx;
    const dz = (zPlane === undefined ? w.zMid : zPlane) - w.zMid;
    const yr = (v - w.cy - dz * w.cosPitch) / Math.max(w.sinPitch, 0.05);
    return {
        x: w.cx + xr * w.cosYaw + yr * w.sinYaw,
        y: w.cy - xr * w.sinYaw + yr * w.cosYaw,
    };
}

// setOrbitCenter: move the orbit pivot to a new world point without any
// visible jump — the new center is projected under the old and new parameters
// and the pan difference is folded in, so the view only changes on the *next*
// rotation, which then pivots about the new point.
export function setOrbitCenter(w, wx, wy, wz) {
    const p0 = project(w, wx, wy, wz, { x: 0, y: 0, depth: 0 });
    w.cx = wx;
    w.cy = wy;
    w.zMid = wz;
    const p1 = project(w, wx, wy, wz, { x: 0, y: 0, depth: 0 });
    w.panX += p0.x - p1.x;
    w.panY += p0.y - p1.y;
}
