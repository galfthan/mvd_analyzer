// engine.js — the Player tab's engine: mvd-fte-player (ftewebgl, controller
// ported from hub) in a SAME-ORIGIN iframe, fed the bytes of the demo we
// analysed through a blob URL — no second download, and local files play
// too. Same origin is the point: we drive the engine directly via
// iframe.contentWindow (seek, POV, speed) instead of reloading a
// cross-origin hub embed, which accepts no commands. The engine boots when
// the Map or Player tab shows a Player pane (~6 MB of wasm plus assets) and
// is dropped 20 s after none does, or on the next demo load
// (src/player/index.js); one iframe page holds one engine for its lifetime.
//
// Clock: FTE runs on demo-relative seconds, the page on match-relative
// ones (demoOffsetSec). While it is active() the engine is the clock: a
// poll once per display frame pushes its time into the page clock as an
// 'engine' seek — the Map pane's follow camera moves with it, so a slower
// poll shows as stepping — and any other seek (scrub, time links) becomes a
// throttled demo_jump.

import { store } from '../state/store.js';
import { WATCH_TABS } from '../state/ui.js';
import { demoOffsetSec, mapFileKey, userIdOf, nameOfUserId } from '../lib/result.js';

/**
 * @typedef {import('../../../mvd-fte-player/src/fte-controller.js').FteController} FteController
 * @typedef {import('../data/wasm-source.js').DemoHandle} DemoHandle
 * @typedef {ReturnType<typeof import('../state/clock.js').createClock>} Clock
 * @typedef {import('../state/store.js').Store<string>} TabStore
 * @typedef {import('../lib/playlist.js').Track} Track
 * @typedef {object} EngineStatus
 * @property {boolean} ready
 * @property {boolean} playing
 * @property {number} speed        demo speed, percent (0 while paused)
 * @property {number} rate         the speed play runs at, percent
 * @property {boolean} ended
 * @property {number | null} track  the tracked userid
 * @property {boolean} autotrack
 * @property {boolean} muted
 * @property {boolean} teamInfo   the team info table is shown
 */

const SEEK_THROTTLE_MS = 150;
// demo_jump lands on whole seconds and takes a few frames; until the engine
// clock is near the target (or this long has passed), the sync poll must
// not drag the cursor back to the pre-jump time.
const SEEK_SETTLE_MS = 2000;
// The engine reports ready (Module.getClientState exists) before `playdemo`
// has started the demo, and a demo_jump sent then is lost — seen as the
// opening jump landing at 0:00. Wait for the demo clock to move.
const CLOCK_WAIT_MS = 30000;

/** @type {EngineStatus} */
const IDLE = { ready: false, playing: false, speed: 100, rate: 100, ended: false, track: null, autotrack: false, muted: true, teamInfo: false };

export class PlayerEngine {
    /**
     * @param {{ clock: Clock, demo: import('../state/store.js').Store<DemoHandle | null>, activeTab: TabStore, wanted?: import('../state/store.js').Store<boolean> }} deps
     *   `wanted`: whether something shows the engine (the Player tab: a
     *   Player pane is open); without, the engine is dropped. Default: always.
     */
    constructor({ clock, demo, activeTab, wanted = store(true) }) {
        this.clock = clock;
        this.activeTab = activeTab;
        this.wanted = wanted;
        // What the POV bar, the playback bar and the panes show.
        this.status = store({ ...IDLE });
        // The speed play runs at (percent): kept here, since FTE's own speed
        // reads 0 while paused.
        this.rate = 100;
        // A clip's slow-motion speed (percent) while the runner has one on:
        // it, not the rate, is what plays then.
        /** @type {number | null} */
        this.slow = null;
        // Play / pause from a click on the game view, the menu from a
        // right-click (hookScreen; game-view pixels) — none by default.
        /** @type {() => void} */
        this.screenToggle = () => this.togglePlay();
        /** @type {((x: number, y: number) => void) | null} */
        this.screenMenu = null;
        // A click that closes an open menu instead (true: taken).
        /** @type {() => boolean} */
        this.screenDismiss = () => false;
        // Whether the engine's console is open (toggleConsole, and ` / ~ /
        // Escape typed in the game view): its keys then stay the engine's.
        this.consoleOpen = false;
        // FTE's team info table (hud_teaminfo, mvd-fte-player/config.cfg)
        // and sound on or off: kept across engines, set on each as it comes
        // up (carrySettings) — each demo gets a fresh engine, which starts
        // muted, so a run moving on to another demo's clip would mute again.
        this.teamInfo = false;
        this.sound = false;
        /** @type {DemoHandle | null} */
        this.handle = null;
        /** @type {HTMLElement | null} */
        this.host = null;
        /** @type {HTMLIFrameElement | null} */
        this.iframe = null;
        /** @type {string | null} */
        this.blobUrl = null;
        /** @type {FteController | null} */
        this.fte = null;
        /** @type {number | null} */
        this.syncFrame = null;
        /** @type {ReturnType<typeof setTimeout> | null} */
        this.seekTimer = null;
        /** @type {number | null} */
        this.pendingSeek = null;   // match time awaiting the throttled jump
        /** @type {number | null} */
        this.seekTarget = null;    // demo time of the last jump, until reached
        this.seekIssuedAt = 0;
        /** @type {(() => void) | null} */
        this.onReady = null;       // run once when the engine is up
        /** @type {Set<(demoT: number) => void>} */
        this.tickListeners = new Set();

        demo.subscribe((handle) => {
            if (handle === this.handle) return;
            this.teardown();
            this.handle = handle;
            if (this.active()) this.ensure();
        });
        activeTab.subscribe(() => this.syncActive());
        wanted.subscribe(() => this.syncActive());
        clock.subscribe(({ t, from }) => {
            if (from !== 'engine' && this.drives()) this.requestSeek(t);
        });
    }

    // On the Map or Player tab (WATCH_TABS), with something showing the
    // engine.
    active() {
        return WATCH_TABS.includes(this.activeTab.get()) && this.wanted.get();
    }

    // Off its tab or unwanted it is paused and its frame loop suspended —
    // the hidden iframe would otherwise go on rendering every frame (the
    // host drops it after a while unwanted: src/player/index.js).
    syncActive() {
        if (this.active()) {
            // Back from suspension: the clock may have moved meanwhile (a
            // time link on another tab), and the first tick would pull it
            // back to the engine's time — jump the engine there instead.
            const back = !!this.fte && !this.syncFrame;
            this.fte?.resume();
            this.ensure();
            if (back && this.fte && Math.abs(this.matchTime(this.fte.getDemoElapsedTime()) - this.clock.t) > 0.5) this.requestSeek(this.clock.t);
            this.startSync();
        } else if (this.fte) {
            this.stopSync();
            this.fte.pause();
            this.fte.suspend();
            this.publish();
        }
        // FTE takes the keyboard focus inside its frame; hidden, the frame
        // would keep it and swallow every key the page's own shortcuts
        // listen for (`f`, the map's Escape).
        if (!this.active() && this.iframe && document.activeElement === this.iframe) this.iframe.blur();
    }

    // The engine is the page clock while it is up and active.
    drives() {
        return !!this.fte && this.active();
    }

    // Wanted on the tab but not up yet: the clock waits for it.
    loading() {
        return this.active() && !this.fte;
    }

    offset() {
        return demoOffsetSec(this.handle?.result);
    }

    // attachHost is called by PlayerFrame with the element the iframe lives in.
    /** @param {HTMLElement} el */
    attachHost(el) {
        this.host = el;
        if (this.active()) this.ensure();
    }

    ensure() {
        if (this.iframe || !this.handle?.result || !this.host) return;
        const handle = this.handle;
        const demoUrl = URL.createObjectURL(handle.demoBlob);
        const demoPath = handle.gzipped ? 'qw/match.mvd.gz' : 'qw/match.mvd';
        const iframe = document.createElement('iframe');
        iframe.src = 'fte-player/player.html';
        iframe.title = 'Demo player';
        iframe.allow = 'fullscreen; autoplay';
        iframe.addEventListener('load', () => {
            if (this.iframe !== iframe) return;
            // player.html's window.mvdFte (mvd-fte-player/README.md).
            this.hookScreen(/** @type {Window} */ (iframe.contentWindow));
            /** @type {any} */ (iframe.contentWindow).mvdFte.start({ demoUrl, demoPath, mapName: mapFileKey(handle.result) })
                .then((/** @type {FteController} */ fte) => waitForDemoClock(fte, () => this.iframe === iframe).then(() => fte))
                .then((/** @type {FteController} */ fte) => {
                    if (this.iframe !== iframe) return;
                    this.fte = fte;
                    this.carrySettings(fte);
                    // Open where the cursor already is, paused — the same
                    // entry hub's ?from= gives.
                    if (this.clock.t > 0) this.jump(this.clock.t);
                    fte.pause();
                    if (this.active()) this.startSync();
                    else fte.suspend();
                    this.publish();
                    const onReady = this.onReady;
                    this.onReady = null;
                    if (onReady) onReady();
                }, (/** @type {unknown} */ err) => console.error('player:', err));
        }, { once: true });
        this.blobUrl = demoUrl;
        this.iframe = iframe;
        this.host.appendChild(iframe);
    }

    // carrySettings sets a fresh engine to the settings the user picked on
    // the ones before it (team info, sound).
    /** @param {FteController} fte */
    carrySettings(fte) {
        if (this.teamInfo) fte.command('hud_teaminfo_show 1');
        if (this.sound) fte.unmute();
    }

    // The game view's input. A click plays / pauses (`screenToggle`, the
    // host's play / pause: the full page's playback bar, the light page's
    // play button; the engine's own by default); a right-click opens the
    // host's player menu (`screenMenu`, src/player/player-menu.js). Keys typed with the game
    // in focus — its frame keeps the focus after any click there — go to
    // the page as if typed there (src/player/player-keys.js, the bar's i /
    // o / c, f for fullscreen), but for the engine's own binds: Tab (the
    // scoreboard) and ` / ~ (the console), and all of them while the
    // console is open. FTE's config binds nothing else
    // (mvd-fte-player/config.cfg), so nothing of the engine's is lost.
    /** @param {Window} win */
    hookScreen(win) {
        win.addEventListener('click', (e) => {
            if (e.button === 0 && !this.screenDismiss()) this.screenToggle();
        }, true);
        win.addEventListener('contextmenu', (e) => {
            if (!this.screenMenu) return;
            e.preventDefault();
            this.screenMenu(e.clientX, e.clientY);
        }, true);
        /** @param {KeyboardEvent} e */
        const forward = (e) => {
            if (e.key === '`' || e.key === '~') {
                if (e.type === 'keydown' && !e.repeat) this.consoleOpen = !this.consoleOpen;
                return;
            }
            if (this.consoleOpen) {
                if (e.key === 'Escape' && e.type === 'keydown') this.consoleOpen = false;
                return;
            }
            if (e.key === 'Tab') return;
            e.preventDefault();
            e.stopPropagation();
            document.dispatchEvent(new KeyboardEvent(e.type, {
                key: e.key, code: e.code, repeat: e.repeat, bubbles: true, cancelable: true,
                shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, altKey: e.altKey, metaKey: e.metaKey,
            }));
        };
        win.addEventListener('keydown', forward, true);
        win.addEventListener('keyup', forward, true);

    }

    // teardown drops the engine; removing the iframe frees it.
    teardown() {
        this.stopSync();
        if (this.seekTimer) clearTimeout(this.seekTimer);
        if (this.iframe) this.iframe.remove();
        if (this.blobUrl) URL.revokeObjectURL(this.blobUrl);
        Object.assign(this, {
            blobUrl: null, iframe: null, fte: null, seekTimer: null,
            pendingSeek: null, seekTarget: null, onReady: null,
            rate: 100, slow: null, consoleOpen: false,
        });
        this.status.set({ ...IDLE });
    }

    startSync() {
        if (this.syncFrame || !this.fte) return;
        const loop = () => {
            this.syncFrame = requestAnimationFrame(loop);
            this.tick();
        };
        this.syncFrame = requestAnimationFrame(loop);
    }

    stopSync() {
        if (this.syncFrame) cancelAnimationFrame(this.syncFrame);
        this.syncFrame = null;
    }

    // onTick(fn): fn(demoT) after every sync poll; the playlist runner
    // steps on it. Returns an unsubscribe.
    /** @param {(demoT: number) => void} fn */
    onTick(fn) {
        this.tickListeners.add(fn);
        return () => this.tickListeners.delete(fn);
    }

    // Up, playing, run out — what the playlist runner steps on (the page
    // driver, page-driver.js, answers the same).
    ready() {
        return !!this.fte;
    }

    isPlaying() {
        return !!this.fte?.isPlaying();
    }

    hasEnded() {
        return !!this.fte?.hasEnded();
    }

    // A jump is in flight: the engine clock still shows the pre-jump time.
    seeking() {
        return this.pendingSeek != null || this.seekTarget != null;
    }

    // matchTime: a demo time as the page clock holds it — clamped as the
    // clock clamps, so the countdown (before the match) reads 0, not a
    // difference that would re-seek the clock every frame.
    /** @param {number} demoT */
    matchTime(demoT) {
        return Math.min(Math.max(0, demoT - this.offset()), this.clock.duration);
    }

    tick() {
        const fte = this.fte;
        if (!fte) return;
        const demoT = fte.getDemoElapsedTime();
        // A seek after the demo ended restarts it from 0 before jumping;
        // keep the cursor where the user put it until the jump has been sent.
        if (fte.isRestarting()) {
            this.seekIssuedAt = performance.now();
            return;
        }
        if (this.seekTarget != null) {
            const arrived = Math.abs(demoT - this.seekTarget) < 1.5;
            if (arrived || performance.now() - this.seekIssuedAt > SEEK_SETTLE_MS) this.seekTarget = null;
        }
        if (!this.seeking()) {
            const t = this.matchTime(demoT);
            if (Math.abs(t - this.clock.t) > 0.01) this.clock.seek(t, 'engine');
        }
        this.publish();
        for (const fn of this.tickListeners) fn(demoT);
    }

    // publish refreshes the status store when anything in it changed.
    publish() {
        const fte = this.fte;
        if (!fte) return;
        /** @type {EngineStatus} */
        const next = {
            ready: true,
            playing: fte.isPlaying(),
            speed: fte.getDemoSpeed(),
            rate: this.rate,
            ended: fte.hasEnded(),
            track: fte.getTrackUserid(),
            autotrack: fte.isUsingAutotrack(),
            muted: fte.isMuted(),
            teamInfo: this.teamInfo,
        };
        const cur = this.status.get();
        if (/** @type {Array<keyof EngineStatus>} */ (Object.keys(next)).some((k) => next[k] !== cur[k])) this.status.set(next);
    }

    /** @param {number} matchTime */
    requestSeek(matchTime) {
        this.pendingSeek = matchTime;
        if (this.seekTimer) return;
        this.seekTimer = setTimeout(() => {
            this.seekTimer = null;
            const t = this.pendingSeek;
            this.pendingSeek = null;
            if (t != null && this.fte) this.jump(t);
        }, SEEK_THROTTLE_MS);
    }

    /** @param {number} matchTime  callers hold an engine (this.fte set) */
    jump(matchTime) {
        const demoT = matchTime + this.offset();
        this.seekTarget = Math.floor(demoT);
        this.seekIssuedAt = performance.now();
        /** @type {FteController} */ (this.fte).demoJump(demoT);
    }

    // jumpToDemoTime seeks the engine directly. A page seek clamps to the
    // match window, so a clip in the countdown or after the match end would
    // land outside itself and its run would never see `to` crossed; the
    // cursor shows the clamped match time meanwhile.
    /** @param {number} demoT  the runner calls it once the engine is up */
    jumpToDemoTime(demoT) {
        this.clock.seek(Math.max(0, demoT - this.offset()), 'engine');
        if (this.seekTimer) clearTimeout(this.seekTimer);
        this.seekTimer = null;
        this.pendingSeek = null;
        this.seekTarget = Math.floor(demoT);
        this.seekIssuedAt = performance.now();
        /** @type {FteController} */ (this.fte).demoJump(demoT);
    }

    // The engine's clock in demo seconds, or the cursor's before it is up.
    demoNow() {
        if (this.fte) return this.fte.getDemoElapsedTime();
        return this.clock.t + this.offset();
    }

    // The playback bar's speed (× 1), mapped onto demo_setspeed (percent):
    // playing, it changes at once; paused, the next play runs at it. FTE
    // cannot play backwards.
    /** @param {number} speed */
    setRate(speed) {
        if (!(speed > 0)) return;
        this.rate = Math.round(speed * 100);
        // Inside a slow-motion window the new rate waits for its end.
        if (this.fte?.isPlaying() && this.slow == null) this.fte.setDemoSpeed(this.rate);
        this.publish();
    }

    // A clip's slow motion (× 1), or null for the rate again; it changes
    // what plays only while playing, and the rate (the bar's speed) stays
    // what it was.
    /** @param {number | null} speed */
    slowTo(speed) {
        this.slow = speed == null ? null : Math.round(speed * 100);
        const fte = this.fte;
        if (!fte?.isPlaying()) return;
        fte.setDemoSpeed(this.slow ?? this.rate);
        this.publish();
    }

    play() {
        if (this.fte && !this.fte.isPlaying()) this.fte.setDemoSpeed(this.rate);
        this.publish();
    }

    // The bar's play / pause. Play after the demo ran out starts over at
    // match start (a caller that jumps first, like the runner, uses play).
    togglePlay() {
        const fte = this.fte;
        if (!fte) return;
        if (fte.isPlaying()) return this.pause();
        if (fte.hasEnded()) this.jump(0);
        this.play();
    }

    pause() {
        this.fte?.pause();
        this.publish();
    }

    // track(userid | 'auto')
    /** @param {Track} userid */
    track(userid) {
        if (!this.fte) return;
        if (userid === 'auto') this.fte.enableAutotrack();
        else this.fte.track(userid);
        this.publish();
    }

    /** @param {string} name */
    trackName(name) {
        const id = userIdOf(this.handle?.result, name);
        if (id) this.track(id);
    }

    // The tracked player's name, or null (autotrack with nobody / no engine).
    povName() {
        const id = this.status.get().track;
        return id == null ? null : nameOfUserId(this.handle?.result, id);
    }

    // The POV a new clip records: a userid, or 'auto'.
    povTrack() {
        const s = this.status.get();
        return !s.ready || s.autotrack || s.track == null ? 'auto' : s.track;
    }

    // toggleConsole opens / closes the engine's console (the player menu's
    // Console), keeping hookScreen's key routing in step.
    toggleConsole() {
        if (!this.fte) return;
        this.fte.command('toggleconsole');
        this.consoleOpen = !this.consoleOpen;
    }

    // toggleTeamInfo shows / hides both teams' health, armor and weapons.
    toggleTeamInfo() {
        this.teamInfo = !this.teamInfo;
        this.fte?.command(`hud_teaminfo_show ${this.teamInfo ? 1 : 0}`);
        this.publish();
    }

    toggleMute() {
        if (!this.fte) return;
        this.fte.toggleMute();
        this.sound = !this.fte.isMuted();
        this.publish();
    }

    // whenReady(fn) runs fn once the engine is up (now, if it is).
    /** @param {() => void} fn */
    whenReady(fn) {
        if (this.fte) fn();
        else {
            this.onReady = fn;
            if (this.active()) this.ensure();
        }
    }
}

// waitForDemoClock resolves once the engine's demo clock moves (or after
// CLOCK_WAIT_MS). `live()` turning false — the engine torn down by a new
// demo — ends the poll too.
/**
 * @param {FteController} fte
 * @param {() => boolean} live
 * @returns {Promise<void>}
 */
function waitForDemoClock(fte, live) {
    return new Promise((resolve) => {
        const started = performance.now();
        const poll = setInterval(() => {
            if (!live() || fte.getDemoElapsedTime() > 0 || performance.now() - started > CLOCK_WAIT_MS) {
                clearInterval(poll);
                resolve();
            }
        }, 50);
    });
}
