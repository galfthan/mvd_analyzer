// match.js — the match the shown demo is one map of (src/lib/matches.js),
// null when it stands alone or nothing says otherwise yet. src/app/match.js
// sets it after every load; src/shell/MatchTabs.svelte shows its maps.
import { store } from './store.js';

export const currentMatch = store(/** @type {import('../lib/matches.js').Match | null} */ (null));
