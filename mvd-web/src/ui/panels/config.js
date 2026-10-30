// config.js — the tabs whose panels the viewer can arrange, and each
// panel's title (for the "Add panel" list and a collapsed panel that has
// no heading of its own). The panel elements carry `data-panel="<id>"`;
// the order here is the default.

/** @type {Record<string, Array<[id: string, title: string]>>} */
export const PANEL_TABS = {
    summary: [
        ['match-info', 'Match info'],
        ['match-settings', 'Match settings'],
        ['server-info', 'Server info'],
        ['teams', 'Teams'],
        ['basic-stats', 'Basic stats'],
        ['opening', 'Opening'],
        ['weapon-stats', 'Weapon stats'],
        ['items', 'Item pickups & drops'],
        ['kills-by-weapon', 'Kills by weapon'],
    ],
    timeline: [
        ['team-status', 'Team status'],
        ['frags-per-player', 'Frags / deaths per player'],
        ['weapons-per-player', 'Weapons per player'],
        ['ha-per-player', 'Health / armor per player'],
        ['score', 'Score timeline'],
        ['powerups', 'Powerups'],
        ['weapons', 'Weapons'],
        ['health-armor', 'Team health / armor'],
        ['region-control', 'Region control'],
    ],
    'loc-graph': [
        ['region-control', 'Region control'],
        ['loc-metric', 'Loc analysis metric'],
        ['loc-graph', 'Loc graph'],
        ['loc-heatmap', 'Loc heatmap'],
    ],
    keymoments: [
        ['powerup-runs', 'Powerup runs'],
        ['streaks', 'Longest frag streaks'],
        ['top-damage', 'Top damage windows'],
        ['top-rl', 'Top RL kills'],
        ['markers', 'Demo markers'],
        ['top-lg', 'Top LG kills'],
        ['airgibs', 'Direct rocket air hits'],
        ['discharges', 'Discharges'],
        ['quadbores', 'Quadbores'],
        ['telefrags', 'Telefrags'],
    ],
    pickups: [
        ['weapon-pickups', 'Weapon pickups'],
        ['item-pickups', 'Item pickups'],
    ],
    aimstats: [
        ['accuracy', 'Accuracy & effectiveness'],
        ['crosshair', 'Crosshair placement'],
    ],
};
