// light-demo.js — the light player's demos: a hub demo's bytes and just
// enough of a handle for the engine and the runner (src/player/engine.js,
// runner.js) — no analysis. The map comes from hub's game row, the demo
// offset from the clips themselves (each carries its demo's).

import { fetchGame, downloadDemo } from '../data/hub.js';

/**
 * @typedef {{ ref: { key: string, gameId: number }, demoBlob: Blob, gzipped: boolean, result: any }} LightHandle
 */

// lightResult: the few Result fields the engine and demoRefOf read.
/**
 * @param {string} map
 * @param {number} offsetSec
 */
export function lightResult(map, offsetSec) {
    return {
        demoInfo: { map },
        streams: { global: { demoOffset: Math.round(offsetSec * 1000) } },
        timelineAnalysis: { playerUserIDs: {} },
    };
}

/**
 * @param {number} gameId
 * @param {number} offsetSec  the demo's offset, from a clip of it
 * @returns {Promise<LightHandle>}
 */
export async function loadLightDemo(gameId, offsetSec) {
    const game = await fetchGame(gameId);
    const bytes = await downloadDemo(game);
    return {
        ref: { key: `hub:${gameId}`, gameId },
        demoBlob: new Blob([bytes]),
        gzipped: bytes.length > 1 && bytes[0] === 0x1f && bytes[1] === 0x8b,
        result: lightResult(game.map || '', offsetSec),
    };
}
