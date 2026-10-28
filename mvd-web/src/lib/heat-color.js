// heat-color.js — the shared sequential colour ramp of the table heatmaps
// (the Locs & Regions tab, src/lib/loc-heatmap.js) and the Aim
// tab's crosshair-density image.

// Sequential colormap stops [t, [r,g,b]] — viridis. Chosen because it is
// perceptually uniform and safe for red/green colour-vision deficiency (no
// red↔green crossover; intensity reads monotonically in greyscale too). Kept
// in sync with the CSS gradient legend (.heatmap-legend-bar) in styles.css.
const HEAT_STOPS = [
    [0.00, [ 68,   1,  84]], // deep purple
    [0.25, [ 59,  82, 139]], // blue
    [0.50, [ 33, 145, 140]], // teal
    [0.75, [ 94, 201,  98]], // green
    [1.00, [253, 231,  37]], // yellow
];

export function heatColorRGB(t) {
    t = Math.max(0, Math.min(1, t));
    for (let i = 1; i < HEAT_STOPS.length; i++) {
        if (t <= HEAT_STOPS[i][0]) {
            const [t0, c0] = HEAT_STOPS[i - 1];
            const [t1, c1] = HEAT_STOPS[i];
            const f = (t - t0) / (t1 - t0 || 1);
            return [
                Math.round(c0[0] + (c1[0] - c0[0]) * f),
                Math.round(c0[1] + (c1[1] - c0[1]) * f),
                Math.round(c0[2] + (c1[2] - c0[2]) * f),
            ];
        }
    }
    return HEAT_STOPS[HEAT_STOPS.length - 1][1].slice();
}
