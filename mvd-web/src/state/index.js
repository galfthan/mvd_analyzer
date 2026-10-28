// The page's shared state. Components and src/app/ import it; src/main.js
// publishes it as window.MvdState for the console and the headless checks.
export { clock } from './clock.js';
export { teams, setCanonicalTeams, teamIndexOf, colorOf, TEAM_PALETTE } from './teams.js';

export { demo } from './demo.js';
export { TABS, activeTab, selectTab } from './ui.js';
export { regionControl, resetRegionControl, applyRegionDefs, reapplyRegions } from './regions.js';
export { segment } from './timeline.js';
export { TABS_WITH_TIMELINE, playback, playAtSpeed, stopPlayback } from './playback.js';
