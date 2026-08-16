// Canvas-2D icon rasterisers.
//
// The scene renders through WebGL (glworld.js) — the 2D painter that used to
// live here is gone. What remains are the icon helpers the HOST chrome uses
// to bake little player-symbol canvases for DOM panels (the region status
// list), plus the shared sizing constants. Same category as the label atlas:
// a 2D context as a texture/icon rasteriser, never a scene path.

// Size a player symbol is drawn at before the per-player height scaling —
// every stroke width and font size in drawPlayerSymbolAt is relative to it,
// and the GL symbol sprites use the same 13px-radius/2px-ring proportions.
export const PLAYER_SYMBOL_BASE_SIZE = 32;
// Arrowhead length in screen px, for the view / velocity / teleport arrows.
export const ARROWHEAD_PX = 7;

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
