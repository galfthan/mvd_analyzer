// density-canvas.js — paints one crosshair-placement density image (a grid
// from aimDensityGrid, src/lib/aim-plot.js) onto its canvas: the
// Gaussian-smoothed 2-D histogram with the shared viridis ramp plus a
// colorbar — the MATLAB imagesc look — the dead-center cross, the hull box
// and the axis ticks. The canvas is sized here (intrinsic and CSS), so it
// paints correctly while its panel is hidden. Returns the hover data
// aimHoverTitle reads.

import {
    AIM_X_HALF, AIM_Y_HALF, AIM_HULL_HALF_W, AIM_HULL_HALF_H, AIM_IMG_SCALE, aimDensityRGB, aimBarLabel,
} from '../../lib/aim-plot.js';

function surfaceRGB(canvas) {
    const m = (getComputedStyle(canvas).backgroundColor || '')
        .match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
    return m ? [+m[1], +m[2], +m[3]] : [26, 26, 46]; // --bg-deep fallback
}

export function paintAimDensity(canvas, g) {
    const { nx, ny, sm, max } = g;
    // Paint the fine grid at 1px/cell offscreen, then upscale with bilinear
    // smoothing onto the display canvas — the imagesc-with-interp look.
    const img = document.createElement('canvas');
    img.width = nx;
    img.height = ny;
    const ictx = img.getContext('2d');
    const idata = ictx.createImageData(nx, ny);
    const bg = surfaceRGB(canvas);
    for (let p = 0; p < nx * ny; p++) {
        const rgb = aimDensityRGB(max ? sm[p] / max : 0, bg);
        idata.data[4 * p] = rgb[0];
        idata.data[4 * p + 1] = rgb[1];
        idata.data[4 * p + 2] = rgb[2];
        idata.data[4 * p + 3] = 255;
    }
    ictx.putImageData(idata, 0, 0);

    const ML = 34, MT = 8, MB = 20, GAP = 10, CB = 14, MR = 40;
    const PW = nx * AIM_IMG_SCALE, PH = ny * AIM_IMG_SCALE;
    const W = ML + PW + GAP + CB + MR, H = MT + PH + MB;
    const dpr = window.devicePixelRatio || 1;
    canvas.width = W * dpr;
    canvas.height = H * dpr;
    canvas.style.width = `${W}px`;
    canvas.style.height = `${H}px`;
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    ctx.imageSmoothingEnabled = true;
    ctx.drawImage(img, ML, MT, PW, PH);

    const px = x => ML + (x + AIM_X_HALF) / (2 * AIM_X_HALF) * PW;
    const py = y => MT + (AIM_Y_HALF - y) / (2 * AIM_Y_HALF) * PH;
    const crisp = v => Math.round(v) + 0.5;

    // Dead-center cross + the player collision box, true to shape (32 wide ×
    // 56 tall), plus a dashed outline at the corner-on silhouette (the axis-
    // aligned box viewed diagonally reads √2 wider; same height).
    ctx.strokeStyle = 'rgba(255,255,255,0.28)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(crisp(px(0)), MT);
    ctx.lineTo(crisp(px(0)), MT + PH);
    ctx.moveTo(ML, crisp(py(0)));
    ctx.lineTo(ML + PW, crisp(py(0)));
    ctx.stroke();
    ctx.strokeStyle = 'rgba(255,255,255,0.55)';
    ctx.strokeRect(crisp(px(-AIM_HULL_HALF_W)), crisp(py(AIM_HULL_HALF_H)),
        px(AIM_HULL_HALF_W) - px(-AIM_HULL_HALF_W),
        py(-AIM_HULL_HALF_H) - py(AIM_HULL_HALF_H));
    const dw = AIM_HULL_HALF_W * Math.SQRT2;
    ctx.save();
    ctx.setLineDash([4, 3]);
    ctx.strokeStyle = 'rgba(255,255,255,0.35)';
    ctx.strokeRect(crisp(px(-dw)), crisp(py(AIM_HULL_HALF_H)),
        px(dw) - px(-dw), py(-AIM_HULL_HALF_H) - py(AIM_HULL_HALF_H));
    ctx.restore();
    ctx.strokeStyle = 'rgba(255,255,255,0.15)';
    ctx.strokeRect(crisp(ML), crisp(MT), PW, PH);

    // Axis ticks every hull-width (32 qu), labelled in qu.
    ctx.fillStyle = '#8892a6';
    ctx.font = '10px sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    for (let x = -AIM_X_HALF; x <= AIM_X_HALF; x += 32) {
        ctx.fillRect(crisp(px(x)) - 0.5, MT + PH, 1, 3);
        ctx.fillText(String(x), px(x), MT + PH + 5);
    }
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (let y = -AIM_Y_HALF; y <= AIM_Y_HALF; y += 32) {
        ctx.fillRect(ML - 3, crisp(py(y)) - 0.5, 3, 1);
        ctx.fillText(String(y), ML - 6, py(y));
    }

    // Colorbar: 0 → max shots per fine cell (smoothed), same ramp including
    // the fade-to-surface bottom so bar and image agree exactly.
    const bx = ML + PW + GAP;
    for (let i = 0; i < PH; i++) {
        const rgb = aimDensityRGB(1 - i / (PH - 1), bg);
        ctx.fillStyle = `rgb(${rgb[0]},${rgb[1]},${rgb[2]})`;
        ctx.fillRect(bx, MT + i, CB, 1);
    }
    ctx.strokeStyle = 'rgba(255,255,255,0.15)';
    ctx.strokeRect(crisp(bx), crisp(MT), CB, PH);
    ctx.fillStyle = '#8892a6';
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    ctx.fillText(aimBarLabel(max), bx + CB + 4, MT + 4);
    ctx.fillText(aimBarLabel(max / 2), bx + CB + 4, MT + PH / 2);
    ctx.fillText('0', bx + CB + 4, MT + PH - 3);

    return { ...g, ML, MT, PW, PH, W, H };
}

// The cursor position in the hover data's unscaled CSS px. max-width:100%
// can render the canvas below its intrinsic CSS size; map cursor px back
// into the unscaled ML/PW space or the reported bin drifts on narrow
// layouts.
export function hoverPoint(canvas, d, e) {
    const rect = canvas.getBoundingClientRect();
    const sx = rect.width ? d.W / rect.width : 1;
    const sy = rect.height ? d.H / rect.height : 1;
    return [(e.clientX - rect.left) * sx, (e.clientY - rect.top) * sy];
}
