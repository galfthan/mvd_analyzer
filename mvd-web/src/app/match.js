// match.js — keeps the current match (src/state/match.js) in step with the
// demo shown: after every load, the hub is asked for the other maps the two
// sides played around it (src/data/hub.js fetchMatch). A local file has no
// hub row, so no match.
import { currentMatch } from '../state/match.js';
import { demo } from '../state/demo.js';
import { fetchMatch } from '../data/hub.js';
import { matchWith } from '../lib/matches.js';

// The match the hub last answered with: cycling through its maps asks
// nothing again.
/** @type {import('../lib/matches.js').Match | null} */
let fetched = null;

/** @param {import('../data/hub.js').HubGame | null | undefined} game */
export async function syncMatch(game) {
    const cur = currentMatch.get();
    if (!game) {
        currentMatch.set(null);
        return;
    }
    if (cur && matchWith([cur], game.id)) {
        if (cur === fetched) return;
    } else {
        currentMatch.set(null);
    }
    let match;
    try {
        match = await fetchMatch(game);
    } catch (err) {
        console.warn('match lookup failed:', /** @type {Error} */ (err).message);
        return;
    }
    // A newer load has replaced the demo meanwhile: its own lookup answers.
    if (String(demo.get()?.game?.id) !== String(game.id)) return;
    fetched = match;
    currentMatch.set(match.games.length > 1 ? match : null);
}
