// timeline.js — the selected time segment of the match: { start, end } in
// match seconds, or null for the whole match. It is the view window of every
// Timeline graph (src/tabs/timeline/). The unified bar
// (src/ui/PlaybackBar.svelte) selects one (a drag across the bar; a click or
// double-click clears it) and shows it; the Timeline's Ctrl+wheel zoom, drag
// pan and double-click set it too; src/app/url.js writes it to the URL (`seg=`).
import { store } from './store.js';

export const segment = store(/** @type {{ start: number, end: number } | null} */ (null));
