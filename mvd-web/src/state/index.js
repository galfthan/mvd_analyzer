// The page's shared state. app.js reaches it as window.MvdState (published
// by src/main.js); Svelte components import it.
import { store } from './store.js';

export { clock } from './clock.js';
export { teams, setCanonicalTeams, teamIndexOf, colorOf, TEAM_PALETTE } from './teams.js';

// The current DemoHandle (src/data/wasm-source.js), null before the first
// load. Set once a load has fully replaced the previous demo.
export const demo = store(null);
export { TABS, activeTab, selectTab } from './ui.js';
