// runner.js — scripted playback of clips: jump to a clip, set its POV, play,
// and step to the next one when playback crosses its end (the stepping rules
// are hub's, in src/lib/playlist.js playlistStep). A clip of another hub
// demo loads that demo and carries on there; a clip of another local file
// is skipped with a note.

import { playlistStep } from '../lib/playlist.js';
import { playlist, setRun } from './playlist-store.js';
import { toast } from './toast.js';

export function createRunner({ engine, demo, teams, selectTab, loadDemo, demoRefOf }) {
    let loadingRun = null;   // the run whose next clip's demo is being loaded
    let awaitingFrames = false;   // a clip waits for the shown engine to repaint

    const currentRef = () => demoRefOf(demo.get(), teams.get().order);
    const isCurrent = (c) => {
        const ref = currentRef();
        return !!ref && c.demo?.key === ref.key;
    };

    function start(clips, index = 0, transient = false) {
        if (!clips.length) return;
        setRun({ clips, index, prev: null, transient });
        playClip(index);
    }

    function stop() {
        setRun(null);
    }

    function playClip(index) {
        const run = playlist.get().run;
        if (!run) return;
        const c = run.clips[index];
        if (!c) return stop();
        setRun({ ...run, index, prev: null });

        if (!isCurrent(c)) {
            if (c.demo?.gameId) return loadForRun(c);
            toast(`Skipping "${c.label}" — load ${c.demo?.title || 'its demo'} to play it`);
            return playClip(index + 1);
        }
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

    async function loadForRun(c) {
        const run = playlist.get().run;
        toast(`Loading ${c.demo.title || `game ${c.demo.gameId}`}…`);
        loadingRun = run;
        let shown;
        try {
            shown = await loadDemo({ gameId: c.demo.gameId });
        } catch (err) {
            toast(`Could not load game ${c.demo.gameId}: ${err.message}`);
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
        const local = [];
        run.clips.forEach((c, i) => { if (isCurrent(c)) local.push(i); });
        const li = local.indexOf(run.index);
        if (li < 0) return;
        // The demo can run out inside a clip padded past its last frame; FTE
        // then reports not playing and `to` is never crossed — it is done.
        const step = fte.hasEnded()
            ? { type: 'end' }
            : playlistStep(local.map((i) => run.clips[i]), li, run.prev, demoT, fte.isPlaying());
        run.prev = demoT;
        if (step.type === 'follow') {
            setRun({ ...run, index: local[step.index] });
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
