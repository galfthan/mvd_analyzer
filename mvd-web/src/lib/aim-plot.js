// aim-plot.js — the Aim Stats tab's crosshair-placement plots without a DOM:
// which samples a weapon / victim filter selects, the smoothed density grid
// behind the canvas image and its colour scale, the hover read-out, the yaw /
// pitch marginal histograms and the LG ramp histogram. The tab
// (src/tabs/aimstats/) paints them.

import { heatColorRGB } from './heat-color.js';

// Crosshair extents in Quake units at the target, shared by the density
// image and the marginal histograms. Both axes use the same scale, so the
// player's collision box (32 wide × 56 tall) draws true to shape. Yaw is
// wider because horizontal aim is the story.
export const AIM_X_HALF = 96; // ±3 hull widths
export const AIM_Y_HALF = 64;
export const AIM_HIST_BIN = 4;     // marginal-histogram bin width, qu
export const AIM_HULL_HALF_W = 16; // player collision box half-extents
export const AIM_HULL_HALF_H = 28;

// Crosshair offset in Quake units at the target: the signed angular error
// projected at the sample's eye→target distance (dist·sin θ — sign-safe at
// any angle, ≈ dist·tan θ at the small angles that matter). x is flipped so
// enemy-right reads right (+dyaw is enemy-left in Quake).
const AIM_DEG = Math.PI / 180;
export const aimOffX = (c, i) => -Math.sin(c.dyaw[i] * AIM_DEG) * c.dist[i];
export const aimOffY = (c, i) => Math.sin(c.dpitch[i] * AIM_DEG) * c.dist[i];

// aimSampleInFilter applies the victim filter to one crosshair sample. Only
// hits can be team-attributed (misses attribute to enemies by construction),
// and hitscan samples can never be self-hits — so 'self' matches nothing.
export function aimSampleInFilter(c, i, filter) {
    switch (filter) {
        case 'enemy': return !(c.team && c.team[i]);
        case 'team': return !!(c.team && c.team[i]);
        case 'self': return false;
        default: return true;
    }
}

// The indices of one player's crosshair samples for a weapon under the
// victim filter.
export function aimSampleIndices(c, weapon, filter) {
    const idx = [];
    if (c && c.t) {
        for (let i = 0; i < c.t.length; i++) {
            if (c.w[i] === weapon && aimSampleInFilter(c, i, filter)) idx.push(i);
        }
    }
    return idx;
}

export function aimNodataText(weapon, filter) {
    const W = weapon.toUpperCase();
    switch (filter) {
        case 'enemy': return `No ${W} samples on enemies for this player.`;
        case 'team': return `No ${W} samples on teammates for this player.`;
        case 'self': return 'Self-hits are RL/GL splash only — no hitscan samples.';
        default: return `No ${W} fired by this player.`;
    }
}

// Density-image parameters. The fine grid is what gets smoothed and painted;
// the hover grid is coarser so the read-out counts are meaningful.
export const AIM_IMG_CELL = 2;      // fine-grid bin, qu at the target
export const AIM_IMG_SIGMA = 1.4;   // gaussian σ, in fine cells
export const AIM_IMG_SCALE = 5;     // display px per fine cell
export const AIM_HOVER_BIN = 8;     // hover read-out bin, qu at the target

export function aimClampIndex(v, n) { return Math.max(0, Math.min(n - 1, v)); }

// Zero density keeps the surface colour and the lowest densities fade into
// the ramp — matching the table heatmaps, where empty cells keep the page
// background and only real values get the shared viridis heatColorRGB.
export const AIM_IMG_FADE = 0.04; // density fraction over which the ramp fades in

export function aimDensityRGB(t, bg) {
    const rgb = heatColorRGB(t);
    const a = Math.min(1, t / AIM_IMG_FADE);
    for (let i = 0; i < 3; i++) rgb[i] = Math.round(bg[i] + (rgb[i] - bg[i]) * a);
    return rgb;
}

// aimDensityGrid bins the selected samples onto the fine grid, smooths it,
// and counts shots / hits on the coarse hover grid. Returns
// { nx, ny, sm (smoothed, row-major, top row first), max, hx, hy, hShots,
// hHits, hitsKnown }.
export function aimDensityGrid(c, idx) {
    const nx = Math.round(2 * AIM_X_HALF / AIM_IMG_CELL);
    const ny = Math.round(2 * AIM_Y_HALF / AIM_IMG_CELL);
    const hx = Math.round(2 * AIM_X_HALF / AIM_HOVER_BIN);
    const hy = Math.round(2 * AIM_Y_HALF / AIM_HOVER_BIN);
    const grid = new Float32Array(nx * ny);
    const hShots = new Uint32Array(hx * hy), hHits = new Uint32Array(hx * hy);
    // x/y are the enemy's offset relative to the crosshair in qu at the
    // target (enemy-right reads on the right, top = up). Samples beyond the
    // extents are dropped rather than clamped: a clamp pile-up paints a
    // bright rim on the smoothed image. The marginal histograms keep clamp
    // edge bins, so the rare outliers stay visible there.
    for (const i of idx) {
        const x = aimOffX(c, i), y = aimOffY(c, i);
        if (Math.abs(x) > AIM_X_HALF || Math.abs(y) > AIM_Y_HALF) continue;
        const xi = aimClampIndex(Math.floor((x + AIM_X_HALF) / AIM_IMG_CELL), nx);
        const yi = aimClampIndex(Math.floor((AIM_Y_HALF - y) / AIM_IMG_CELL), ny);
        grid[yi * nx + xi]++;
        const hxi = aimClampIndex(Math.floor((x + AIM_X_HALF) / AIM_HOVER_BIN), hx);
        const hyi = aimClampIndex(Math.floor((AIM_Y_HALF - y) / AIM_HOVER_BIN), hy);
        hShots[hyi * hx + hxi]++;
        if (c.hit && c.hit[i]) hHits[hyi * hx + hxi]++;
    }

    // Separable gaussian blur, kernel normalized to sum 1 so values keep the
    // shots-per-cell unit (edge cells replicate — they are clamp catch-alls).
    const r = Math.ceil(3 * AIM_IMG_SIGMA);
    const kern = new Float32Array(2 * r + 1);
    let ksum = 0;
    for (let i = -r; i <= r; i++) {
        kern[i + r] = Math.exp(-(i * i) / (2 * AIM_IMG_SIGMA * AIM_IMG_SIGMA));
        ksum += kern[i + r];
    }
    for (let i = 0; i < kern.length; i++) kern[i] /= ksum;
    const tmp = new Float32Array(nx * ny);
    for (let y = 0; y < ny; y++) for (let x = 0; x < nx; x++) {
        let s = 0;
        for (let d = -r; d <= r; d++) s += kern[d + r] * grid[y * nx + aimClampIndex(x + d, nx)];
        tmp[y * nx + x] = s;
    }
    const sm = new Float32Array(nx * ny);
    let max = 0;
    for (let y = 0; y < ny; y++) for (let x = 0; x < nx; x++) {
        let s = 0;
        for (let d = -r; d <= r; d++) s += kern[d + r] * tmp[aimClampIndex(y + d, ny) * nx + x];
        sm[y * nx + x] = s;
        if (s > max) max = s;
    }
    return { nx, ny, sm, max, hx, hy, hShots, hHits, hitsKnown: !!c.hit };
}

// The colorbar's labels: 0 → max shots per fine cell (smoothed).
export const aimBarLabel = v => (v >= 10 ? String(Math.round(v)) : v.toFixed(1));

// aimHoverTitle is the canvas tooltip for a cursor at (mx, my) in the
// canvas's unscaled CSS px: shot/hit counts in the coarse bin under it, ''
// off the plot. `d` = the grid plus the plot box { ML, MT, PW, PH }.
export function aimHoverTitle(d, mx, my) {
    if (mx < d.ML || mx >= d.ML + d.PW || my < d.MT || my >= d.MT + d.PH) return '';
    const xi = aimClampIndex(Math.floor((mx - d.ML) / d.PW * d.hx), d.hx);
    const yi = aimClampIndex(Math.floor((my - d.MT) / d.PH * d.hy), d.hy);
    const s = d.hShots[yi * d.hx + xi], h = d.hHits[yi * d.hx + xi];
    const fmt = v => (v > 0 ? '+' : '') + v.toFixed(0);
    const xlo = -AIM_X_HALF + xi * AIM_HOVER_BIN;
    const yhi = AIM_Y_HALF - yi * AIM_HOVER_BIN;
    const range = `x ${fmt(xlo)}…${fmt(xlo + AIM_HOVER_BIN)} qu, y ${fmt(yhi - AIM_HOVER_BIN)}…${fmt(yhi)} qu`;
    return s
        ? `${range}: ${s} shot${s === 1 ? '' : 's'}` +
            (d.hitsKnown ? `, ${h} hit (${Math.round(h / s * 100)}%)` : '')
        : `${range}: no shots`;
}

// Marginal histograms under each crosshair image: the same qu-at-target
// samples projected onto one axis at a time. Yaw flips x so enemy-right
// reads right (matching the image); pitch plots ascending so enemy-above
// reads right. Out-of-range samples clamp into the edge bins (unlike the
// image, which drops them — a 1-D strip has room for catch-all bins without
// smearing), and an odd bin count centers the middle bin on zero (dead
// center). bandHalf is the hull half-extent shaded as "on the hitbox" —
// half-width for yaw, half-height for pitch.
//
// aimHistRow returns the row under one image: [] when nothing is selected,
// else yaw, pitch and — for LG with a measured hit column — the ramp.
export function aimHistRow(c, idx, ramp, filter) {
    if (!idx.length) return [];
    const row = [
        aimMarginalHist('Yaw', 'enemy left ← → right', 'qu',
            AIM_X_HALF, AIM_HIST_BIN, i => aimOffX(c, i), c, idx, AIM_HULL_HALF_W),
        aimMarginalHist('Pitch', 'enemy below ← → above', 'qu',
            AIM_Y_HALF, AIM_HIST_BIN, i => aimOffY(c, i), c, idx, AIM_HULL_HALF_H),
    ];
    // The ramp panel is hit-% by construction — without a measured hit
    // column (aim.hitsMeasured false) it would read a fabricated flat 0%.
    if (ramp && ramp.since && ramp.since.length && ramp.hit) row.push(aimRampHist(ramp, filter));
    return row;
}

// One marginal histogram: { kind: 'marginal', name, dirNote, band: {left,
// width} (% of the plot), cols: [{ title, height (%), stub }], axis: [lo,
// mid, hi] labels }. `stub` keeps a 2px bar on a non-empty bin.
export function aimMarginalHist(name, dirNote, unit, half, w, val, c, idx, bandHalf) {
    // Bin i is centered on (i − mid)·w: the middle bin straddles zero and the
    // outermost bin centers sit exactly at ±half (edges overhang by w/2).
    const mid = Math.round(half / w);
    const nb = 2 * mid + 1;
    const span = nb * w;
    const binOf = v => Math.max(0, Math.min(nb - 1, mid + Math.round(v / w)));
    const bins = Array.from({ length: nb }, () => ({ shots: 0, hits: 0 }));
    let max = 0;
    for (const i of idx) {
        const b = bins[binOf(val(i))];
        b.shots++;
        if (c.hit && c.hit[i]) b.hits++;
        if (b.shots > max) max = b.shots;
    }
    return {
        kind: 'marginal',
        name, dirNote,
        band: { left: `${((span / 2 - bandHalf) / span) * 100}%`, width: `${(2 * bandHalf / span) * 100}%` },
        cols: bins.map((b, bi) => {
            const lo = (bi - mid) * w - w / 2;
            return {
                title: `${lo.toFixed(0)} … ${(lo + w).toFixed(0)} ${unit}: ` +
                    `${b.shots} shot${b.shots === 1 ? '' : 's'}` +
                    (b.shots && c.hit ? `, ${b.hits} hit (${Math.round(b.hits / b.shots * 100)}%)` : ''),
                height: `${max ? (b.shots / max) * 100 : 0}%`,
                stub: b.shots > 0,
            };
        }),
        axis: [`−${half}`, '0', `+${half} ${unit}`],
    };
}

// The LG ramp (result.aim lgRamp, computed in Go: time since the shaft
// opened per fire, fires < 150 ms apart grouped into one shaft — one fire ==
// one 100 ms LG cell, so the bins align with the cell cadence) shown as a
// third histogram under the LG image, same style as the marginals: bars =
// the bin's hit-rate on a dynamic y scale (max bin hit % rounded up to the
// next 10%, labelled in a small gutter), hover for the counts. A bin with
// fires but no hits keeps a 2px stub so it reads as 0%, not as no data.
// The last bin collects longer shafts.
export const AIM_RAMP_BIN = 100; // ms
export const AIM_RAMP_BINS = 12; // 0-1100 ms, then 1100+

// { kind: 'ramp', yMax, cols: [{ title, height, opacity, stub }], axis }.
export function aimRampHist(ramp, filter) {
    // Score each fire under the victim filter: ramp.team flags fires that
    // connected but hit no enemy (teammate-only), so enemy = hit && !team,
    // team = hit && team. The denominator stays all LG fires — the ramp is
    // hit% of fires, whatever they hit.
    const rampHitAt = i => {
        const t = !!(ramp.team && ramp.team[i]);
        if (filter === 'enemy') return ramp.hit[i] && !t;
        if (filter === 'team') return ramp.hit[i] && t;
        return ramp.hit[i];
    };
    const bins = Array.from({ length: AIM_RAMP_BINS }, () => ({ shots: 0, hits: 0 }));
    for (let i = 0; i < ramp.since.length; i++) {
        const b = bins[Math.min(AIM_RAMP_BINS - 1, Math.floor(ramp.since[i] / AIM_RAMP_BIN))];
        b.shots++;
        if (rampHitAt(i)) b.hits++;
    }
    const pctOf = b => (b.shots ? b.hits / b.shots * 100 : 0);
    const yMax = Math.max(10, Math.ceil(Math.max(...bins.map(pctOf)) / 10) * 10);
    const maxShots = Math.max(...bins.map(b => b.shots));
    return {
        kind: 'ramp',
        yMax,
        cols: bins.map((b, bi) => {
            const lo = bi * AIM_RAMP_BIN;
            const range = bi === AIM_RAMP_BINS - 1 ? `${lo}+ ms` : `${lo}…${lo + AIM_RAMP_BIN} ms`;
            const pct = pctOf(b);
            return {
                title: `${range}: ${b.shots} shot${b.shots === 1 ? '' : 's'}` +
                    (b.shots ? `, ${b.hits} hit (${Math.round(pct)}%)` : ''),
                height: `${pct / yMax * 100}%`,
                // A tall bar from a handful of fires is weak evidence: fade
                // bars by sample size (sqrt keeps the steeply-decaying tail
                // readable, the floor keeps every non-empty bin visible).
                // Exact counts on hover.
                opacity: (0.25 + 0.75 * Math.sqrt(b.shots / maxShots)).toFixed(2),
                stub: b.shots > 0,
            };
        }),
        axis: ['0', String(AIM_RAMP_BINS * AIM_RAMP_BIN / 2), `${(AIM_RAMP_BINS - 1) * AIM_RAMP_BIN}+ ms`],
    };
}
