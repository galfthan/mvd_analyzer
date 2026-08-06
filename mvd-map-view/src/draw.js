// Canvas-2D drawing primitives.
//
// Everything here takes its context, its geometry and its projection
// explicitly — no renderer state is read from module scope. That is what
// makes these the layer a GPU backend replaces: swap the implementations and
// the callers above are unchanged.
//
// `toCanvas` is a projection callback (x, y, z) -> {x, y}. Most callers pass
// the scratch-point variant, which is safe here because every returned point
// is consumed by a moveTo/lineTo before the next call overwrites it.

import { computeRegionOutline } from './locgroups.js';

// Size a player symbol is drawn at before the per-player height scaling —
// every stroke width and font size in drawPlayerSymbolAt is relative to it.
export const PLAYER_SYMBOL_BASE_SIZE = 32;
// Arrowhead length in screen px, for the view / velocity arrows.
export const ARROWHEAD_PX = 7;

// Fill a flat triangle list (9 numbers per triangle — x,y,z per vertex) with
// the given style. All triangles go into a single path and are filled once so
// this stays fast when called every frame with thousands of tris.
export function drawTriangleListFill(ctx, tris, fillStyle, toCanvas) {
    if (!tris || tris.length < 9) return;
    ctx.fillStyle = fillStyle;
    ctx.beginPath();
    for (let i = 0; i + 8 < tris.length; i += 9) {
        let p = toCanvas(tris[i],     tris[i + 1], tris[i + 2]);
        ctx.moveTo(p.x, p.y);
        p = toCanvas(tris[i + 3], tris[i + 4], tris[i + 5]);
        ctx.lineTo(p.x, p.y);
        p = toCanvas(tris[i + 6], tris[i + 7], tris[i + 8]);
        ctx.lineTo(p.x, p.y);
        ctx.closePath();
    }
    ctx.fill();
}

// Stroke a loc region's floor boundary as a set of line segments.
export function drawRegionOutline(ctx, group, toCanvas, strokeStyle, lineWidth) {
    const outline = computeRegionOutline(group);
    if (!outline || outline.length < 6) return;
    ctx.strokeStyle = strokeStyle;
    ctx.lineWidth = lineWidth;
    ctx.beginPath();
    for (let i = 0; i + 5 < outline.length; i += 6) {
        const a = toCanvas(outline[i],     outline[i + 1], outline[i + 2]);
        const b = toCanvas(outline[i + 3], outline[i + 4], outline[i + 5]);
        ctx.moveTo(a.x, a.y);
        ctx.lineTo(b.x, b.y);
    }
    ctx.stroke();
}

// Fill a loc region from its BSP-derived triangle list. Groups with no tris
// (map JSON absent, or a loc that matched no face) silently no-op.
export function fillRegion(ctx, group, fillColor, toCanvas) {
    drawTriangleListFill(ctx, group.tris, fillColor, toCanvas);
}

// renderSolidEntries: painter-sort a floor model by projected centroid depth
// (cached per camera angle — the order only depends on yaw/pitch) and draw,
// batching consecutive same-fill triangles into a single path. `isFaded`
// answers whether an entry's loc is outside the current focus, which selects
// its faded tone; pass null when nothing is focused.
export function renderSolidEntries(ctx, se, cam, toCanvas, isFaded) {
    const w = cam;
    const camKey = w.yaw + '|' + w.pitch;
    if (se.sortedFor !== camKey) {
        for (const e of se.entries) {
            const dx = e.cx - w.cx, dy = e.cy - w.cy, dz = e.cz - w.zMid;
            const yr = dx * w.sinYaw + dy * w.cosYaw;
            e.depth = dz * w.sinPitch - yr * w.cosPitch;
        }
        se.entries.sort((a, b) => a.depth - b.depth);
        se.sortedFor = camKey;
    }
    let curFill = null;
    let open = false;
    // Seal the anti-aliasing seams between adjacent triangles. The painter
    // sort interleaves triangles, so even one continuous floor's same-colour
    // tris land in separate sub-paths; canvas anti-aliases each shared edge
    // against the backdrop, so the triangulation reads as a distracting mesh.
    // Stroking every batch with its own fill colour at a hairline width covers
    // those gaps. Genuine 3D edges (floor-top↔slab-side folds, walls) survive
    // because they're a different colour and so aren't painted over.
    //
    // A depth-buffered backend has no seams to seal and should drop this.
    ctx.lineJoin = 'round';
    ctx.lineWidth = 1;
    const flush = () => {
        ctx.fill();
        ctx.strokeStyle = curFill;
        ctx.stroke();
    };
    for (const e of se.entries) {
        const fill = (isFaded && isFaded(e.name)) ? e.fillFaded : e.fill;
        if (fill !== curFill) {
            if (open) flush();
            ctx.fillStyle = fill;
            ctx.beginPath();
            curFill = fill;
            open = true;
        }
        const t = e.tris, i = e.off;
        let p = toCanvas(t[i], t[i + 1], t[i + 2]);
        ctx.moveTo(p.x, p.y);
        p = toCanvas(t[i + 3], t[i + 4], t[i + 5]);
        ctx.lineTo(p.x, p.y);
        p = toCanvas(t[i + 6], t[i + 7], t[i + 8]);
        ctx.lineTo(p.x, p.y);
        ctx.closePath();
    }
    if (open) flush();
}

// drawMoverMesh: a mover (lift/door/plat/train) as one flat silhouette —
// back faces culled against the view direction, near hull filled once.
// `faces` carries each triangle's outward normal and offset into `mesh`.
export function drawMoverMesh(ctx, mesh, faces, pose, fillStyle, cam, toCanvas) {
    const ox = pose.x, oy = pose.y, oz = pose.z;
    const w = cam;
    const vx = -w.sinYaw * w.cosPitch, vy = -w.cosYaw * w.cosPitch, vz = w.sinPitch;
    ctx.fillStyle = fillStyle;
    ctx.beginPath();
    for (const f of faces) {
        if (f.nx * vx + f.ny * vy + f.nz * vz >= 0) continue; // back-facing
        const i = f.off;
        let p = toCanvas(mesh[i] + ox, mesh[i + 1] + oy, mesh[i + 2] + oz);
        ctx.moveTo(p.x, p.y);
        p = toCanvas(mesh[i + 3] + ox, mesh[i + 4] + oy, mesh[i + 5] + oz);
        ctx.lineTo(p.x, p.y);
        p = toCanvas(mesh[i + 6] + ox, mesh[i + 7] + oy, mesh[i + 8] + oz);
        ctx.lineTo(p.x, p.y);
        ctx.closePath();
    }
    ctx.fill();
}

// drawLiquidVolume: translucent water/slime/lava, shaded per face against a
// fixed light and drawn back to front so the transparency composites right.
export function drawLiquidVolume(ctx, tris, base, alpha, light, cam, toCanvas) {
    const w = cam;
    const faces = [];
    for (let i = 0; i + 8 < tris.length; i += 9) {
        const ax = tris[i],     ay = tris[i + 1], az = tris[i + 2];
        const bx = tris[i + 3], by = tris[i + 4], bz = tris[i + 5];
        const cx = tris[i + 6], cy = tris[i + 7], cz = tris[i + 8];
        const ux = bx - ax, uy = by - ay, uz = bz - az;
        const vx = cx - ax, vy = cy - ay, vz = cz - az;
        let nx = uy * vz - uz * vy, ny = uz * vx - ux * vz, nz = ux * vy - uy * vx;
        const nl = Math.hypot(nx, ny, nz) || 1;
        const shade = 0.5 + 0.5 * Math.abs((nx * light[0] + ny * light[1] + nz * light[2]) / nl);
        const dcx = (ax + bx + cx) / 3 - w.cx, dcy = (ay + by + cy) / 3 - w.cy, dcz = (az + bz + cz) / 3 - w.zMid;
        const yr = dcx * w.sinYaw + dcy * w.cosYaw;
        faces.push({ off: i, shade, depth: dcz * w.sinPitch - yr * w.cosPitch });
    }
    faces.sort((a, b) => a.depth - b.depth); // back to front for translucency
    for (const f of faces) {
        const q = Math.round(f.shade * 10) / 10;
        ctx.fillStyle = `rgba(${Math.round(base[0] * q)}, ${Math.round(base[1] * q)}, ${Math.round(base[2] * q)}, ${alpha})`;
        ctx.beginPath();
        const i = f.off;
        let p = toCanvas(tris[i],     tris[i + 1], tris[i + 2]);
        ctx.moveTo(p.x, p.y);
        p = toCanvas(tris[i + 3], tris[i + 4], tris[i + 5]);
        ctx.lineTo(p.x, p.y);
        p = toCanvas(tris[i + 6], tris[i + 7], tris[i + 8]);
        ctx.lineTo(p.x, p.y);
        ctx.closePath();
        ctx.fill();
    }
}

// ─── Symbols and markers (canvas space) ─────────────────────────────────────

export function drawPlayerSymbolAt(ctx, letter, teamColor, cx, cy, size) {
    const k = size / PLAYER_SYMBOL_BASE_SIZE;
    const r = 13 * k;

    ctx.beginPath();
    ctx.arc(cx, cy, r, 0, Math.PI * 2);
    ctx.fillStyle = '#0a0a15';
    ctx.fill();
    ctx.strokeStyle = teamColor;
    ctx.lineWidth = 2 * k;
    ctx.stroke();

    ctx.font = `bold ${Math.round(16 * k)}px monospace`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillStyle = teamColor;
    ctx.fillText(letter, cx, cy);
}

export function drawBadge(ctx, letter, color, x, y, radius) {
    ctx.beginPath();
    ctx.arc(x, y, radius, 0, Math.PI * 2);
    ctx.fillStyle = color;
    ctx.fill();
    ctx.font = `bold ${Math.round(radius * 1.2)}px monospace`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillStyle = '#000';
    ctx.fillText(letter, x, y);
}

export function drawBadgesAroundCenter(ctx, badges, cx, cy, orbitRadius, badgeRadius) {
    for (const b of badges) {
        const rad = (b.angle - 90) * Math.PI / 180;
        const bx = cx + orbitRadius * Math.cos(rad);
        const by = cy + orbitRadius * Math.sin(rad);
        drawBadge(ctx, b.letter, b.color, bx, by, badgeRadius);
    }
}

// A fading X where a player died. Smaller than the player symbol's circle
// (radius 13) so the two read as different marks.
export function drawDeathX(ctx, x, y, rgb, alpha) {
    const r = 8;
    ctx.save();
    ctx.strokeStyle = `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${alpha.toFixed(2)})`;
    ctx.lineWidth = 2.5;
    ctx.lineCap = 'round';
    ctx.beginPath();
    ctx.moveTo(x - r, y - r);
    ctx.lineTo(x + r, y + r);
    ctx.moveTo(x + r, y - r);
    ctx.lineTo(x - r, y + r);
    ctx.stroke();
    ctx.restore();
}

// A fading "D" over the death-X, marking a death that left an RL or LG
// backpack. Weapon-coded fill with a black outline so it stays readable
// against the team-coloured X behind it.
export function drawDropD(ctx, x, y, weapon, alpha) {
    const a = alpha.toFixed(2);
    let fill;
    if      (weapon === 'rl') fill = `rgba(255, 107, 107, ${a})`;
    else if (weapon === 'lg') fill = `rgba(0, 217, 255, ${a})`;
    else                      fill = `rgba(255, 255, 255, ${a})`;
    ctx.save();
    ctx.font = 'bold 28px sans-serif';
    ctx.textAlign = 'center';
    // Use the alphabetic baseline + measured glyph metrics to put the
    // letter's *visual* center at (x, y). textBaseline:'middle' is close but
    // not exact for sans-serif "D" — it leaves a few pixels of optical drift
    // between the X center and the D center.
    ctx.textBaseline = 'alphabetic';
    const m = ctx.measureText('D');
    const ascent  = m.actualBoundingBoxAscent  || 20; // sane fallback
    const descent = m.actualBoundingBoxDescent || 0;
    const yDraw = y + (ascent - descent) / 2;
    ctx.lineWidth = 5;
    ctx.strokeStyle = `rgba(0, 0, 0, ${a})`;
    ctx.strokeText('D', x, yDraw);
    ctx.fillStyle = fill;
    ctx.fillText('D', x, yDraw);
    ctx.restore();
}

// drawWorldArrow: an arrow from a world-space origin along a world-space
// delta — used for view and velocity indicators, which must foreshorten with
// the camera, so both endpoints are projected rather than drawn in 2D.
export function drawWorldArrow(ctx, ox, oy, oz, dx, dy, dz, color, width, toCanvasNew) {
    const a = toCanvasNew(ox, oy, oz);
    const b = toCanvasNew(ox + dx, oy + dy, oz + dz);
    const sx = b.x - a.x, sy = b.y - a.y;
    const slen = Math.hypot(sx, sy);
    if (slen < 1) return;
    ctx.strokeStyle = color;
    ctx.fillStyle = color;
    ctx.lineWidth = width;
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
    const ux = sx / slen, uy = sy / slen;
    const hl = ARROWHEAD_PX, hw = ARROWHEAD_PX * 0.6;
    ctx.beginPath();
    ctx.moveTo(b.x, b.y);
    ctx.lineTo(b.x - ux * hl - uy * hw, b.y - uy * hl + ux * hw);
    ctx.lineTo(b.x - ux * hl + uy * hw, b.y - uy * hl - ux * hw);
    ctx.closePath();
    ctx.fill();
}

// drawArrow: a plain canvas-space arrow (teleporter links).
export function drawArrow(ctx, x1, y1, x2, y2, headLen) {
    const ang = Math.atan2(y2 - y1, x2 - x1);
    ctx.beginPath();
    ctx.moveTo(x1, y1);
    ctx.lineTo(x2, y2);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(x2, y2);
    ctx.lineTo(x2 - headLen * Math.cos(ang - Math.PI / 6), y2 - headLen * Math.sin(ang - Math.PI / 6));
    ctx.lineTo(x2 - headLen * Math.cos(ang + Math.PI / 6), y2 - headLen * Math.sin(ang + Math.PI / 6));
    ctx.closePath();
    ctx.fill();
}
