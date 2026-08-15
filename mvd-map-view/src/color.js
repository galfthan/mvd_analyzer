// Colour helpers used by the renderer.
//
// hexToRgb is deliberately a private copy rather than something imported from
// the host app: this package has no dependencies, and a three-field hex parse
// is not a "system" worth sharing across the boundary. Loc *naming*, by
// contrast, is a system — see locs.js, which owns the one canonical
// normalizer for the whole app. (Exported since the death-marker pass moved
// in — drawDeathX takes its colour as an [r, g, b] triple.)

export function hexToRgb(hex) {
    return [parseInt(hex.slice(1, 3), 16), parseInt(hex.slice(3, 5), 16), parseInt(hex.slice(5, 7), 16)];
}

export function hexToRgba(hex, alpha) {
    const [r, g, b] = hexToRgb(hex);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

// scaleRgbaAlpha multiplies an rgba() string's alpha by k, optionally capped.
export function scaleRgbaAlpha(rgba, k, cap) {
    return rgba.replace(/rgba\(([^,]+),([^,]+),([^,]+),([^)]+)\)/,
        (m, r, g, b, a) => {
            let na = parseFloat(a) * k;
            if (cap !== undefined && na > cap) na = cap;
            if (na > 1) na = 1;
            return `rgba(${r},${g},${b},${na})`;
        });
}

// Get color for location based on item type in name
export function getLocationColor(name) {
    const nameLower = name.toLowerCase();

    // Powerups - bright colors (dimmed 50%)
    if (nameLower.includes('quad'))  return { fill: 'rgba(80, 120, 255, 0.075)', stroke: 'rgba(80, 120, 255, 0.5)', text: 'rgba(112, 144, 255, 0.5)' };
    if (nameLower.includes('pent'))  return { fill: 'rgba(255, 0, 255, 0.075)', stroke: 'rgba(255, 0, 255, 0.5)', text: 'rgba(255, 102, 255, 0.5)' };
    if (nameLower.includes('ring'))  return { fill: 'rgba(255, 255, 0, 0.075)', stroke: 'rgba(255, 255, 0, 0.5)', text: 'rgba(255, 255, 102, 0.5)' };

    // Armors
    if (nameLower.includes('ra'))    return { fill: 'rgba(255, 80, 80, 0.075)', stroke: 'rgba(255, 80, 80, 0.5)', text: 'rgba(255, 128, 128, 0.5)' };
    if (nameLower.includes('ya'))    return { fill: 'rgba(255, 200, 50, 0.075)', stroke: 'rgba(255, 200, 50, 0.5)', text: 'rgba(255, 216, 102, 0.5)' };
    if (nameLower.includes('ga'))    return { fill: 'rgba(80, 200, 80, 0.075)', stroke: 'rgba(80, 200, 80, 0.5)', text: 'rgba(128, 216, 128, 0.5)' };

    // Health
    if (nameLower.includes('mh'))    return { fill: 'rgba(80, 200, 255, 0.075)', stroke: 'rgba(80, 200, 255, 0.5)', text: 'rgba(128, 216, 255, 0.5)' };

    // Weapons
    if (nameLower.includes('rl'))    return { fill: 'rgba(200, 100, 50, 0.06)', stroke: 'rgba(200, 100, 50, 0.5)', text: 'rgba(216, 128, 80, 0.5)' };
    if (nameLower.includes('lg'))    return { fill: 'rgba(150, 150, 255, 0.06)', stroke: 'rgba(150, 150, 255, 0.5)', text: 'rgba(176, 176, 255, 0.5)' };
    if (nameLower.includes('gl'))    return { fill: 'rgba(100, 180, 100, 0.06)', stroke: 'rgba(100, 180, 100, 0.5)', text: 'rgba(128, 200, 128, 0.5)' };
    if (nameLower.includes('sng') || nameLower.includes('ng'))
                                     return { fill: 'rgba(180, 140, 80, 0.06)', stroke: 'rgba(180, 140, 80, 0.5)', text: 'rgba(200, 160, 96, 0.5)' };

    // Default - neutral gray (brightened so passageways like cemetary.tele
    // stay legible against the dark background).
    return { fill: 'rgba(170, 170, 190, 0.12)', stroke: 'rgba(150, 150, 160, 0.6)', text: 'rgba(180, 180, 190, 0.7)' };
}
