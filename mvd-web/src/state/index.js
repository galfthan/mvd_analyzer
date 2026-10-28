// The page's shared state. app.js reaches it as window.MvdState (published
// by src/main.js); Svelte components import it.
export { clock } from './clock.js';
export { teams, setCanonicalTeams, teamIndexOf, colorOf, TEAM_PALETTE } from './teams.js';

export { demo } from './demo.js';
export { TABS, activeTab, selectTab } from './ui.js';
export { regionControl, resetRegionControl, applyRegionDefs, reapplyRegions } from './regions.js';
export { segment } from './timeline.js';
export { TABS_WITH_TIMELINE, playback, playAtSpeed, stopPlayback } from './playback.js';
