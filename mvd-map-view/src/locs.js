// Loc-name handling. normalizeLocationName is THE canonical normalizer for
// the whole app — the frontend must not grow a second one that drifts from
// it (loc names are matched against the analyzer's own normalisation, and
// two spellings of "ya.box" is a bug that only shows up as a silently empty
// region). Callers outside this package import it from here.

// Item keywords that stay upper-case through normalisation, so "ra.entry"
// and "RA.entry" resolve to the same loc.
export const ITEM_KEYWORDS = ['RA', 'YA', 'GA', 'MH', 'RL', 'LG', 'GL', 'NG', 'SNG', 'SSG', 'SG', 'MEGA', 'QUAD', 'PENT', 'RING'];

export function normalizeLocationName(name) {
    return name
        .trim()
        .replace(/[\s-]+/g, '.')
        .split('.')
        .map(part => {
            const upper = part.toUpperCase();
            return ITEM_KEYWORDS.includes(upper) ? upper : part.toLowerCase();
        })
        .join('.');
}

// findNearestLocation: nearest loc centre in XY. Used where a world point has
// no loc index of its own (map clicks, item spawners).
export function findNearestLocation(x, y, locations) {
    if (!locations || locations.length === 0) return '';
    let bestDist = Infinity;
    let bestName = '';
    for (const loc of locations) {
        const dx = x - loc.x, dy = y - loc.y;
        const d = dx * dx + dy * dy;
        if (d < bestDist) {
            bestDist = d;
            bestName = loc.name;
        }
    }
    return bestName;
}
