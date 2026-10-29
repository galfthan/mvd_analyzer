// runner.js — scripted playback of clips: jump to a clip, set its POV, play,
// and step to the next one when playback crosses its end (the stepping rules
// are hub's, in src/lib/playlist.js playlistStep). A clip of another hub
// demo loads that demo and carries on there; a clip of another local file
// is skipped with a note. The clip playing is the selected segment (the
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
 * @typedef {import('../data/wasm-source.js').DemoHandle} DemoHandle
 *
 * @typedef {object} RunnerDeps
 * @property {import('./engine.js').PlayerEngine} engine
 * @property {import('../state/store.js').Store<DemoHandle | null>} demo
 * @property {typeof import('../state/teams.js').teams} teams
 * @property {(id: string) => void} selectTab
 * @property {typeof import('../app/load.js').loadDemo} loadDemo
 * @property {typeof import('./demo-ref.js').demoRefOf} demoRefOf
 * @property {import('../state/store.js').Store<{ start: number, end: number } | null>} segment
 */

/** @param {RunnerDeps} deps */
export function createRunner({ engine, demo, teams, selectTab, loadDemo, demoRefOf, segment }) {
    /** @type {Run | null} */
    let loadingRun = null;   // the run whose next clip's demo is being loaded
    let awaitingFrames = false;   // a clip waits for the shown engine to repaint

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
        setRun({ clips, index, prev: null, transient });
        playClip(index);
    }

    /** @type {{ start: number, end: number } | null} */
    let shownSpan = null;   // the segment this run set, while it is still the segment
    /** @param {Clip} c  a clip of the current demo */
    function showSpan(c) {
        const off = engine.offset();
        shownSpan = { start: Math.max(0, c.from - off), end: Math.max(0, c.to - off) };
        segment.set(shownSpan);
    }

    /** @type {number | null} */
    let slowing = null;   // the slow-motion speed the engine is at, if any
    /** @param {number | null} speed */
    function slowTo(speed) {
        if (speed === slowing) return;
        slowing = speed;
        engine.slowTo(speed);
    }

    function stop() {
        slowTo(null);
        setRun(null);
        if (shownSpan && segment.get() === shownSpan) segment.set(null);
        shownSpan = null;
    }

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
        if (engine.activeTab.get() !== 'player') {
            // The engine's iframe was hidden: showing it costs a repaint
            // (seconds under software GL), and a jump sent before that
            // repaint plays on through it — the backward jump overshoots
            // its target and the controller's POV restore never sees the
            // clock arrive. Jump once frames flow again.
            // The engine's sync poll starts with the tab; until the jump is
            // sent its clock is the old one, so the run must not step on it.
            selectTab('player');
            const pending = playlist.get().run;
            awaitingFrames = true;
            requestAnimationFrame(() => requestAnimationFrame(() => {
                awaitingFrames = false;
                if (playlist.get().run === pending) playClip(index);
            }));
            return;
        }
        if (!engine.fte) {
            // Engine still booting: park the cursor at the clip (the opening
            // jump goes there) and start once it is up.
            engine.clock.seek(Math.max(0, c.from - engine.offset()));
            engine.whenReady(() => playClip(index));
            return;
        }
        engine.jumpToDemoTime(c.from);
        engine.track(c.track);
        engine.play();
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
            loadingRun = null;
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

    // One tick of the run, after every engine poll. Skipped while a jump is
    // in flight (the clock is still at the pre-jump time).
    engine.onTick((demoT) => {
        const run = playlist.get().run;
        const fte = engine.fte;
        if (!run || !fte || awaitingFrames) return;
        if (engine.seeking()) {
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
        const step = fte.hasEnded()
            ? { type: 'end' }
            : playlistStep(local.map((i) => run.clips[i]), li, run.prev, demoT, fte.isPlaying());
        run.prev = demoT;
        // Paused, nothing is slowed: play resumes at the rate and the next
        // tick slows it again inside the window.
        if (step.type === 'none') slowTo(fte.isPlaying() ? slowAt(run.clips[run.index], demoT) : null);
        if (step.type === 'follow') {
            // A seek into another clip: it is the active one, with its POV.
            const next = run.clips[local[step.index]];
            setRun({ ...run, index: local[step.index] });
            showSpan(next);
            if (next.track !== run.clips[run.index].track) engine.track(next.track);
        } else if (step.type === 'next' || step.type === 'end') {
            if (run.index + 1 < run.clips.length) {
                playClip(run.index + 1);
            } else {
                engine.pause();
                if (!run.transient) toast('Playlist finished');
                stop();
            }
        }
    });

    // A demo the user loaded ends the run; one the run loaded itself
    // (loadForRun) carries it on.
    let lastHandle = demo.get();
    demo.subscribe((handle) => {
        if (handle === lastHandle) return;
        lastHandle = handle;
        const run = playlist.get().run;
        if (run && loadingRun !== run) stop();
    });

    return { start, stop, isCurrent, currentRef };
}
