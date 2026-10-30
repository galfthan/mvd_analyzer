// player-menu.js — the player's menu (MapMenu.svelte, as the map's): its
// keys (src/player/player-keys.js) as items to click, the engine's own
// (Tab, the console), then the team info, autotrack and sound. Opened from the POV bar's
// keyboard button or a right-click on the game view (engine.screenMenu);
// the full page and the light player build it from their own actions.

import { store } from '../state/store.js';

// Where the full page's player menu is open (Player pane coordinates), or
// null.
export const playerMenu = store(/** @type {{ x: number, y: number } | null} */ (null));

/**
 * @param {object} p
 * @param {import('./engine.js').PlayerEngine} p.engine
 * @param {{ autotrack: boolean, muted: boolean, ready: boolean, teamInfo: boolean }} p.status
 * @param {() => void} p.toggle
 * @param {(dir: 1 | -1) => void} p.step
 * @param {(by: number) => void} p.seek
 * @param {() => void} p.fullscreen
 * @param {boolean} [p.selection]  list the playback bar's selection keys
 *   (the full page's: src/player/selection-clip.js)
 * @returns {import('../ui/map-menu.js').MenuItem[]}
 */
export function playerMenuItems({ engine, status, toggle, step, seek, fullscreen, selection = false }) {
    return [
        { kind: 'action', label: 'Play / pause', run: toggle, keys: 'Space' },
        { kind: 'action', label: 'Next player', run: () => step(1), keys: '↓' },
        { kind: 'action', label: 'Previous player', run: () => step(-1), keys: '↑' },
        { kind: 'action', label: 'Back 1 s', run: () => seek(-1), keys: '←' },
        { kind: 'action', label: 'Forward 1 s', run: () => seek(1), keys: '→' },
        { kind: 'action', label: 'Back 10 s', run: () => seek(-10), keys: 'Shift ←' },
        { kind: 'action', label: 'Forward 10 s', run: () => seek(10), keys: 'Shift →' },
        { kind: 'action', label: 'Fullscreen', run: fullscreen, close: true, keys: 'f' },
        ...(selection ? /** @type {import('../ui/map-menu.js').MenuItem[]} */ ([
            { kind: 'info', label: 'Selection start / end', title: 'At the playhead', keys: 'i / o' },
            { kind: 'info', label: 'Selection as a clip', title: 'Adds the selection to the playlist, from the POV you watch', keys: 'c' },
        ]) : []),
        { kind: 'info', label: 'Scoreboard (hold)', title: 'Hold Tab with the game view focused (click it first — the click also plays / pauses)', keys: 'Tab' },
        { kind: 'action', label: 'Console', title: 'The engine\'s console; ` again closes it', run: () => engine.toggleConsole(), close: true, keys: '`' },
        { kind: 'sep' },
        { kind: 'check', label: 'Team info', title: 'Both teams\' health, armor and weapons, at the right edge', on: status.ready && status.teamInfo, run: () => engine.toggleTeamInfo(), keys: 't' },
        { kind: 'check', label: 'Autotrack', title: 'Follow whoever the engine picks', on: status.ready && status.autotrack, run: () => engine.track('auto') },
        { kind: 'check', label: 'Sound', on: status.ready && !status.muted, run: () => engine.toggleMute() },
    ];
}
