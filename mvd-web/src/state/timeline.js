// timeline.js — the selected time segment of the match: { start, end } in
// match seconds, or null for the whole match. It is the view window of every
// Timeline graph (src/tabs/timeline/). app.js's unified bar selects one (a
// drag across the bar; a click or double-click clears it) and mirrors it
// onto the bar's overlay and label and the URL (`seg=`); the Timeline's
// Ctrl+wheel zoom, drag pan and double-click set it too.
import { store } from './store.js';

export const segment = store(null);
