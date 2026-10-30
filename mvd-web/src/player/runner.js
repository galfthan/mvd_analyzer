// runner.js — scripted playback of clips: jump to a clip, set its POV, play,
// and step to the next one when playback crosses its end (the stepping rules
// are hub's, in src/lib/playlist.js playlistStep). A clip of another hub
// demo loads that demo and carries on there; a clip of another local file
// is skipped with a note. A run plays in the engine, or — started on the
// Map tab without a Player pane — on the map, by the page's own playback
// (page-driver.js); the driver is picked when it starts. The clip playing
// is the selected segment (the
// playback bar shows it, the Timeline tab zooms to it); stopping clears it
// unless the user selected another meanwhile. Inside a clip's slow-motion
// window (clip.slow) the engine plays at its speed, then at the viewer's
// again.

import { playlistStep, slowAt } from '../lib/playlist.js';
import { playlist, setRun } from './playlist-store.js';
import { toast } from './toast.js';

/**
 * @typedef {import('../lib/playlist.js').Clip} Clip
 * @typedef {import('../lib/playlist.js').PlaylistStep} PlaylistStep
 * @typedef {import('./playlist-store.js').Run} Run
 * @typedef {ReturnType<typeof import('./page-driver.js').createPageDriver>} PageDriver
 * @typedef {import('./engine.js').PlayerEngine | PageDriver} Driver
 * @typedef {import('../data/wasm-source.js').DemoHandle} DemoHandle
 *
 * @typedef {object} RunnerDeps
 * @property {import('./engine.js').PlayerEngine} engine
 * @property {PageDriver} [page]  the map's (none on the light page)
 * @property {() => Driver} [driverFor]  what a run started now plays in
 * @property {import('../state/store.js').Store<DemoHandle | null>} demo
 * @property {typeof import('../state/teams.js').teams} teams
 * @property {(id: string) => void} selectTab
 * @property {typeof import('../app/load.js').loadDemo} loadDemo
 * @property {typeof import('./demo-ref.js').demoRefOf} demoRefOf
 * @property {import('../state/store.js').Store<{ start: number, end: number } | null>} segment
 */

/** @param {RunnerDeps} deps */
export function createRunner({ engine, page, driverFor = () => engine, demo, teams, selectTab, loadDemo, demoRefOf, segment }) {
    /** @type {Driver} */
    let drv = engine;         // what the run plays in
    /** @type {Run | null} */
    let loadingRun = null;   // the run whose next clip's demo is being loaded
    let awaitingFrames = false;   // a clip waits for the shown engine to repaint
    let showTries = 0;            // two-frame waits for the engine to become active
    const MAX_SHOW_TRIES = 30;

    const currentRef = () => demoRefOf(demo.get(), teams.get().order);
    /** @param {Clip} c */
    const isCurrent = (c) => {
        const ref = currentRef();
        return !!ref && c.demo?.key === ref.key;
    };

    /**
     * @param {Clip[]} clips
     * @param {number} [index]
     * @param {boolean} [transient]
     */
    function start(clips, index = 0, transient = false) {
        if (!clips.length) return;
        const next = driverFor();
        if (next !== drv) {
            slowTo(null);
            drv = next;
        }
        setRun({ clips, index, prev: null, transient });
        playClip(index);
    }

    /** @type {{ start: number, end: number } | null} */
    let shownSpan = null;   // the segment this run set, while it is still the segment
    /** @param {Clip} c  a clip of the current demo */
    function showSpan(c) {
        const off = drv.offset();
        shownSpan = { start: Math.max(0, c.from - off), end: Math.max(0, c.to - off) };
        segment.set(shownSpan);
    }

    /** @type {number | null} */
    let slowing = null;   // the slow-motion speed the engine is at, if any
    /** @param {number | null} speed */
    function slowTo(speed) {
        if (speed === slowing) return;
        slowing = speed;
        drv.slowTo(speed);
    }

    function stop() {
        setRun(null);
    }

    // However a run ends — stop(), or the playlist removing the clip that
    // plays or clearing the list under it — the engine leaves slow motion
    // and the run's segment goes.
    playlist.subscribe((s) => {
        if (s.run || (slowing == null && !shownSpan)) return;
        slowTo(null);
        if (shownSpan && segment.get() === shownSpan) segment.set(null);
        shownSpan = null;
    });

    /**
     * @param {number} index
     * @returns {void | Promise<void>}
     */
    function playClip(index) {
        const run = playlist.get().run;
        if (!run) return;
        const c = run.clips[index];
        if (!c) return stop();
        slowTo(null);
        setRun({ ...run, index, prev: null });

        if (!isCurrent(c)) {
            if (c.demo?.gameId) return loadForRun(c);
            toast(`Skipping "${c.label}" — load ${c.demo?.title || 'its demo'} to play it`);
            return playClip(index + 1);
        }
        showSpan(c);
        if (drv === engine && !engine.active()) {
            // The engine's iframe was hidden (or not there: selectTab
            // opens a Player pane too): showing it costs a repaint
            // (seconds under software GL), and a jump sent before that
            // repaint plays on through it — the backward jump overshoots
            // its target and the controller's POV restore never sees the
            // clock arrive. Jump once frames flow again.
            // The engine's sync poll starts with the tab; until the jump is
            // sent its clock is the old one, so the run must not step on it.
            // selectTab shows the pane; should it still not be active after
            // a second or so, the run stops rather than ask again forever.
            if (++showTries > MAX_SHOW_TRIES) {
                showTries = 0;
                toast('The Player pane cannot show — open it to play clips');
                stop();
                return;
            }
            selectTab('player');
            const pending = playlist.get().run;
            awaitingFrames = true;
            requestAnimationFrame(() => requestAnimationFrame(() => {
                awaitingFrames = false;
                if (playlist.get().run === pending) playClip(index);
            }));
            return;
        }
        showTries = 0;
        if (drv === engine && !engine.fte) {
            // Engine still booting: park the cursor at the clip (the opening
            // jump goes there) and start once it is up.
            engine.clock.seek(Math.max(0, c.from - engine.offset()));
            engine.whenReady(() => playClip(index));
            return;
        }
        drv.jumpToDemoTime(c.from);
        drv.track(c.track);
        drv.play();
    }

    /** @param {Clip} c  a hub clip: demo.gameId is set */
    async function loadForRun(c) {
        // A run is what plays the clip.
        const run = /** @type {Run} */ (playlist.get().run);
        toast(`Loading ${c.demo.title || `game ${c.demo.gameId}`}…`);
        loadingRun = run;
        let shown;
        try {
            shown = await loadDemo({ gameId: /** @type {number} */ (c.demo.gameId) });
        } catch (err) {
            toast(`Could not load game ${c.demo.gameId}: ${/** @type {Error} */ (err).message}`);
            if (playlist.get().run === run) stop();
            return;
        } finally {
            // A newer run may be loading its own demo by now; it is still
            // the one carried on.
            if (loadingRun === run) loadingRun = null;
        }
        if (playlist.get().run !== run) return;
        // The user loaded another demo meanwhile, and the last pick wins
        // (loadDemo dropped this one): the run ends, as it does on any demo
        // the user loads.
        if (!shown) {
            stop();
            return;
        }
        if (!isCurrent(c)) {
            // The hub demo is not the one the clip names (a hand-made link).
            toast(`Game ${c.demo.gameId} is not "${c.demo.title}" — stopping`);
            stop();
            return;
        }
        playClip(run.index);
    }

    // One tick of the run, after every poll of the driver it plays in.
    // Skipped while a jump is in flight (the clock is still at the pre-jump
    // time).
    /**
     * @param {Driver} d
     * @param {number} demoT
     */
    function tick(d, demoT) {
        const run = playlist.get().run;
        if (!run || d !== drv || !d.ready() || awaitingFrames) return;
        if (d.seeking()) {
            run.prev = null;
            return;
        }
        // Step over the clips of this demo only; indices map back to the list.
        /** @type {number[]} */
        const local = [];
        run.clips.forEach((c, i) => { if (isCurrent(c)) local.push(i); });
        const li = local.indexOf(run.index);
        if (li < 0) return;
        // The demo can run out inside a clip padded past its last frame; FTE
        // then reports not playing and `to` is never crossed — it is done.
        /** @type {PlaylistStep} */
        const step = d.hasEnded()
            ? { type: 'end' }
            : playlistStep(local.map((i) => run.clips[i]), li, run.prev, demoT, d.isPlaying());
        run.prev = demoT;
        // Paused, nothing is slowed: play resumes at the rate and the next
        // tick slows it again inside the window.
        if (step.type === 'none') slowTo(d.isPlaying() ? slowAt(run.clips[run.index], demoT) : null);
        if (step.type === 'follow') {
            // A seek into another clip: it is the active one, with its POV.
            const next = run.clips[local[step.index]];
            setRun({ ...run, index: local[step.index] });
            showSpan(next);
            if (next.track !== run.clips[run.index].track) d.track(next.track);
        } else if (step.type === 'next' || step.type === 'end') {
            if (run.index + 1 < run.clips.length) {
                playClip(run.index + 1);
            } else {
                d.pause();
                if (!run.transient) toast('Playlist finished');
                stop();
            }
        }
    }
    engine.onTick((demoT) => tick(engine, demoT));
    page?.onTick((demoT) => tick(page, demoT));

    // A demo the user loaded ends the run; one the run loaded itself
    // (loadForRun) carries it on.
    let lastHandle = demo.get();
    demo.subscribe((handle) => {
        if (handle === lastHandle) return;
        lastHandle = handle;
        const run = playlist.get().run;
        if (run && loadingRun !== run) stop();
    });

    return { start, stop, isCurrent, currentRef, driver: () => drv };
}
