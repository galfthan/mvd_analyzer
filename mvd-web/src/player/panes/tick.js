// tick.js — panes redraw on the page clock and on the engine's status (a POV
// switch while paused), at most once per animation frame (the engine poll and
// scrubbing can seek several times a frame), and not at all while the Player
// tab is hidden.
import { clock, activeTab, isWatchTab } from '../../state/index.js';
import { engine } from '../index.js';

/**
 * @param {(t: number) => void} draw  called with the clock's match time
 * @returns {() => void} the unsubscribe
 */
export function onFrame(draw) {
    let queued = 0;
    const schedule = () => {
        if (queued || !isWatchTab(activeTab.get())) return;
        queued = requestAnimationFrame(() => {
            queued = 0;
            draw(clock.t);
        });
    };
    const offs = [clock.subscribe(schedule), activeTab.subscribe(schedule), engine.status.subscribe(schedule)];
    // A frame already queued must not draw into an unmounted pane.
    return () => {
        offs.forEach((off) => off());
        cancelAnimationFrame(queued);
    };
}
