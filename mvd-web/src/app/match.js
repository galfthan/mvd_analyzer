// match.js — keeps the current match (src/state/match.js) in step with the
// demo shown: after every load, the hub is asked for the other maps the two
// sides played around it (src/data/hub.js fetchMatch), and those maps are
// fetched ahead (preloadMaps). A local file has no
// hub row, so no match.
import { currentMatch } from '../state/match.js';
import { demo } from '../state/demo.js';
import { fetchMatch } from '../data/hub.js';
import { matchWith } from '../lib/matches.js';
import { mvdData } from './source.js';

/** @param {import('../data/hub.js').HubGame | null | undefined} game */
export async function syncMatch(game) {
    if (!game) {
        currentMatch.set(null);
        mvdData.keepPreloaded([]);
        return;
    }
    // Another map of the match shown: nothing to ask.
    const cur = currentMatch.get();
    if (cur && matchWith([cur], game.id)) return;
    currentMatch.set(null);
    let match;
    try {
        match = await fetchMatch(game);
    } catch (err) {
        console.warn('match lookup failed:', /** @type {Error} */ (err).message);
        return;
    }
    // A newer load has replaced the demo meanwhile: its own lookup answers.
    if (String(demo.get()?.game?.id) !== String(game.id)) return;
    currentMatch.set(match.games.length > 1 ? match : null);
    mvdData.keepPreloaded(match.games);
    preloadMaps(match, game);
}

// preloadMaps fetches the other maps of the match ahead (src/data/
// page-source.js preload), one at a time from the map after the one shown,
// so a step to another map waits for no download. The page holds one
// match's maps (keepPreloaded above drops the last one's). It stops when
// another match takes its place, and does not start for a viewer who asked
// their browser to save data: a 4on4 map is 10–30 MB.
/**
 * @param {import('../lib/matches.js').Match} match
 * @param {import('../data/hub.js').HubGame} shown
 */
async function preloadMaps(match, shown) {
    if (/** @type {{ connection?: { saveData?: boolean } }} */ (navigator).connection?.saveData) return;
    const at = match.games.findIndex(g => String(g.id) === String(shown.id));
    const rest = [...match.games.slice(at + 1), ...match.games.slice(0, at)];
    for (const game of rest) {
        if (currentMatch.get() !== match) return;
        try {
            await mvdData.preload(game);
        } catch (err) {
            console.warn(`preload of game ${game.id} failed:`, /** @type {Error} */ (err).message);
        }
    }
}
