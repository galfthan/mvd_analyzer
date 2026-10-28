// timeline-canvas.js — the canvas renderers behind the Timeline tab's graphs
// (src/tabs/timeline/) and the Player tab's health / armor strips
// (src/player/panes/StackPane.svelte): a diverging bar graph, the per-player
// mini stacks, and span rows. The series they draw come from
// src/lib/timeline.js.
//
// Every renderer sizes its canvas to its container and draws in DEVICE
// pixels (no ctx.scale), so every fillRect edge lands on a whole device
// pixel. With ctx.scale(dpr, dpr) at a fractional dpr (fractional desktop
// scaling, browser zoom ≠ 100 %) each CSS-pixel edge falls between two
// device pixels at ~50 % coverage, which shows as dark seams — and at one
// fillRect per CSS pixel, as a moiré of stripes.
//
// A canvas is passed as the element or its id.

import {
    PLOT_BG_COLOR, DIVERGING_GRAPH_LAYOUT, RC_ROW_H, RC_AXIS_H, axisTicks, makeScanlineSampler,
} from '../lib/timeline.js';

function canvasOf(c) {
    return typeof c === 'string' ? document.getElementById(c) : c;
}

// setupGraphCanvas sizes a canvas for device-pixel drawing (its container's
// width × cssHeight) and returns its context with both sizes; null without
// a canvas.
function setupGraphCanvas(c, cssHeight) {
    const canvas = canvasOf(c);
    if (!canvas || !canvas.getContext) return null;
    const Wcss = canvas.parentElement.clientWidth;
    const Hcss = cssHeight;
    const dpr = window.devicePixelRatio || 1;
    const W = Math.round(Wcss * dpr);
    const H = Math.round(Hcss * dpr);
    canvas.width = W;
    canvas.height = H;
    canvas.style.width = Wcss + 'px';
    canvas.style.height = Hcss + 'px';
    return { canvas, ctx: canvas.getContext('2d'), Wcss, Hcss, W, H, dpr };
}

// The x-axis tick marks and time labels under a graph; density keys off the
// CSS width so it does not change with dpr. Coordinates in device pixels.
function drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime }) {
    const ticks = axisTicks(startTime, endTime, Wcss, 12);
    if (!ticks.length) return;
    ctx.fillStyle = '#888';
    ctx.font = `${Math.round(10 * dpr)}px monospace`;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    ctx.strokeStyle = 'rgba(255,255,255,0.08)';
    for (const { frac, label } of ticks) {
        const x = frac * W;
        ctx.beginPath();
        ctx.moveTo(x, graphH);
        ctx.lineTo(x, graphH + 4 * dpr);
        ctx.stroke();
        ctx.fillText(label, x, graphH + 5 * dpr);
    }
}

// Fill one 1px-wide stacked column of segments, snapping each boundary to a
// pixel row so stacked segments meet on exact rows. `from` is the baseline y
// (device px), `dir` -1 to stack up and +1 down, `scale` the pixel span that
// maps to `maxValue`.
function fillStackedColumn(ctx, px, segs, from, dir, scale, maxValue) {
    if (!segs) return;
    let yAcc = from;
    let yPrev = Math.round(from);
    for (const seg of segs) {
        if (seg.h > 0) {
            yAcc += dir * ((seg.h / maxValue) * scale);
            const yCur = Math.round(yAcc);
            const segH = (yCur - yPrev) * dir;
            if (segH > 0) {
                ctx.fillStyle = seg.color;
                ctx.fillRect(px, Math.min(yPrev, yCur), 1, segH);
            }
            yPrev = yCur;
        }
    }
}

// renderDivergingGraph draws a diverging bar graph: team A stacks up from
// the centre line, team B down, sampled per pixel column (hold-last).
//   dataPoints [{ t, dt, up: [{ h, color }], down: [...] }]
//   dropMarks  [{ time, color, isTop }] (optional — the weapon graph's RL / LG
//              drops), dots in a strip reserved at the top and bottom of the
//              plot so bars never overlap them.
export function renderDivergingGraph(c, { startTime, endTime, dataPoints, maxValue, dropMarks }) {
    const setup = setupGraphCanvas(c, DIVERGING_GRAPH_LAYOUT.H);
    if (!setup) return;
    const { ctx, Wcss, W, H, dpr } = setup;

    const AXIS_H = Math.round(DIVERGING_GRAPH_LAYOUT.AXIS_H * dpr);
    const PAD = Math.round(DIVERGING_GRAPH_LAYOUT.PAD * dpr);
    const graphH = H - AXIS_H;
    // The drop strips are reserved even under tall bars — the weapons bar
    // scales with players per team, and a 5v5 full rollout would otherwise
    // paint straight through the dots. Sized for one row of ~6 px dots.
    const DROP_STRIP_H = Math.round(DIVERGING_GRAPH_LAYOUT.DROP_STRIP_H * dpr);
    const hasDropMarks = !!(dropMarks && dropMarks.length);
    const stripZone = hasDropMarks ? DROP_STRIP_H + Math.round(2 * dpr) : 0;
    const midY = PAD + (graphH - PAD) / 2;
    const barH = midY - PAD - stripZone;
    const duration = endTime - startTime;

    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, graphH);

    // Grid lines at ±50 %, under the bars.
    ctx.strokeStyle = 'rgba(255,255,255,0.06)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, midY - barH * 0.5); ctx.lineTo(W, midY - barH * 0.5);
    ctx.moveTo(0, midY + barH * 0.5); ctx.lineTo(W, midY + barH * 0.5);
    ctx.stroke();

    if (duration > 0 && dataPoints && dataPoints.length > 0) {
        const sampleAt = makeScanlineSampler(dataPoints);
        for (let px = 0; px < W; px++) {
            const pt = sampleAt(startTime + (px / W) * duration);
            if (pt == null) continue;
            fillStackedColumn(ctx, px, pt.up, midY, -1, barH, maxValue);
            fillStackedColumn(ctx, px, pt.down, midY, 1, barH, maxValue);
        }
    }

    // Drop dots: top strip team A, bottom strip team B, coloured by weapon.
    if (hasDropMarks && duration > 0) {
        const dotR = 3 * dpr;
        const topY = PAD + DROP_STRIP_H / 2;
        const bottomY = graphH - PAD - DROP_STRIP_H / 2;
        for (const m of dropMarks) {
            const x = ((m.time - startTime) / duration) * W;
            if (x < -dotR || x > W + dotR) continue;
            ctx.fillStyle = m.color;
            ctx.beginPath();
            ctx.arc(x, m.isTop ? topY : bottomY, dotR, 0, Math.PI * 2);
            ctx.fill();
        }
    }

    // The zero line, over the bars, on a whole pixel row.
    ctx.fillStyle = 'rgba(255, 255, 255, 0.85)';
    ctx.fillRect(0, Math.round(midY), W, Math.max(1, Math.round(dpr)));

    drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime });
}

// renderMiniStack draws a single-direction stack up from the baseline — the
// per-player health / armor rows — with no mirror, zero line or axis.
export function renderMiniStack(c, { startTime, endTime, points, maxValue, height }) {
    const setup = setupGraphCanvas(c, height || 44);
    if (!setup) return;
    const { ctx, W, H, dpr } = setup;
    const PAD = Math.round(2 * dpr);
    const baseY = H - PAD;
    const usableH = H - 2 * PAD;

    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, H);

    const duration = endTime - startTime;
    if (duration <= 0 || !points || points.length === 0 || maxValue <= 0) return;

    const sampleAt = makeScanlineSampler(points);
    for (let px = 0; px < W; px++) {
        const pt = sampleAt(startTime + (px / W) * duration);
        if (!pt) continue;
        fillStackedColumn(ctx, px, pt.up, baseY, -1, usableH, maxValue);
    }
}

// renderMiniDiverging draws a compact up / down row about a faint centre
// line — the per-player frags / deaths rows.
export function renderMiniDiverging(c, { startTime, endTime, points, maxValue, height }) {
    const setup = setupGraphCanvas(c, height || 44);
    if (!setup) return;
    const { ctx, W, H, dpr } = setup;
    const PAD = Math.round(2 * dpr);
    const midY = Math.round(H / 2);
    const halfH = (H - 2 * PAD) / 2;

    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, H);

    const duration = endTime - startTime;
    if (duration > 0 && points && points.length && maxValue > 0) {
        const sampleAt = makeScanlineSampler(points);
        for (let px = 0; px < W; px++) {
            const pt = sampleAt(startTime + (px / W) * duration);
            if (!pt) continue;
            fillStackedColumn(ctx, px, pt.up, midY, -1, halfH, maxValue);
            fillStackedColumn(ctx, px, pt.down, midY, 1, halfH, maxValue);
        }
    }

    ctx.fillStyle = 'rgba(255, 255, 255, 0.5)';
    ctx.fillRect(0, midY, W, Math.max(1, Math.round(dpr)));
}

// renderSpansTimeline draws rows of { start, end, state } spans, coloured
// through stateColors (a state without a colour is a gap), with the row
// labels as DOM elements in `labelsEl` and an x axis below — the powerup,
// region-control and per-player weapons timelines. `rows` [{ name, spans,
// color? }]; `dropMarks` [{ row, time, color }] (optional) are dots in a
// row; `rowH` stretches the rows (default RC_ROW_H, which the powerup hover
// assumes).
//
// The labels go in first and the canvas's pinned CSS width is cleared before
// it is measured: the label column is as wide as its names, and a canvas
// pinned at the width it had beside the previous rows' labels would hold
// the column narrower than the names (and cut them off).
export function renderSpansTimeline(c, labelsEl, { startTime, endTime, rows, stateColors, dropMarks, rowH }) {
    const canvas = canvasOf(c);
    if (!labelsEl || !canvas) return;
    const ROW_H_CSS = rowH || RC_ROW_H;

    labelsEl.innerHTML = '';
    for (const r of rows) {
        const lab = document.createElement('div');
        lab.className = 'region-timeline-label';
        lab.style.height = ROW_H_CSS + 'px';
        lab.style.lineHeight = ROW_H_CSS + 'px';
        if (r.color) lab.style.color = r.color;
        lab.textContent = r.name;
        lab.title = r.name;
        labelsEl.appendChild(lab);
    }

    canvas.style.width = '';
    const setup = setupGraphCanvas(canvas, rows.length * ROW_H_CSS + RC_AXIS_H);
    const { ctx, Wcss, W, dpr } = setup;
    const ROW_H = Math.round(ROW_H_CSS * dpr);
    const ROW_PAD = Math.max(1, Math.round(dpr));

    const graphH = rows.length * ROW_H;
    ctx.fillStyle = PLOT_BG_COLOR;
    ctx.fillRect(0, 0, W, graphH);

    const duration = endTime - startTime;
    if (duration <= 0) return;

    rows.forEach((row, idx) => {
        const y = idx * ROW_H;
        for (const span of row.spans) {
            const color = stateColors[span.state];
            if (!color) continue;
            const x1 = Math.round(((span.start - startTime) / duration) * W);
            const x2 = Math.round(((span.end - startTime) / duration) * W);
            const w = x2 - x1;
            if (w <= 0) continue;
            ctx.fillStyle = color;
            ctx.fillRect(x1, y + ROW_PAD, w, ROW_H - 2 * ROW_PAD);
        }
    });

    if (dropMarks && dropMarks.length) {
        const dotR = 3 * dpr;
        for (const m of dropMarks) {
            const x = ((m.time - startTime) / duration) * W;
            if (x < -dotR || x > W + dotR) continue;
            ctx.fillStyle = m.color;
            ctx.beginPath();
            ctx.arc(x, m.row * ROW_H + ROW_H / 2, dotR, 0, Math.PI * 2);
            ctx.fill();
        }
    }

    drawXAxisTicks(ctx, { W, Wcss, dpr, graphH, startTime, endTime });
}
