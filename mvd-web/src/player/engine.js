// engine.js — the Player tab's engine: mvd-fte-player (ftewebgl, controller
// ported from hub) in a SAME-ORIGIN iframe, fed the bytes of the demo we
// analysed through a blob URL — no second download, and local files play
// too. Same origin is the point: we drive the engine directly via
// iframe.contentWindow (seek, POV, speed) instead of reloading a
// cross-origin hub embed, which accepts no commands. The engine boots when
// the Map or Player tab shows a Player pane (~6 MB of wasm plus assets) and
// is dropped 20 s after none does (src/player/index.js); the next demo
// loads into the same engine (FteController.loadDemo) — one iframe page
// holds one engine for its lifetime.
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
import { createAssetFetcher } from '../../../mvd-fte-player/src/asset-fetch.js';
import { mapPath, mapUrl } from '../../../mvd-fte-player/src/assets.js';

// The engine's files, fetched once for the page and kept: a second engine
// (the light player's standby) boots from memory (asset-fetch.js).
const fetchAsset = createAssetFetcher();

/**
 * @typedef {import('../../../mvd-fte-player/src/fte-controller.js').FteController} FteController
 * @typedef {import('../state/demo.js').DemoHandle} DemoHandle  (or the light player's LightHandle: result, ref, demo())
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
 * @property {string} error       why the engine cannot start ('' when it can)
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
// The standby runs on every this-many display frames while it loads and
// seeks: its frames are taken from the shown engine's (one thread).
const STANDBY_EVERY = 4;

/** @type {EngineStatus} */
const IDLE = { ready: false, playing: false, speed: 100, rate: 100, ended: false, track: null, autotrack: false, muted: true, teamInfo: false, error: '' };

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
        // reads 0 while paused — and across engines, the bar's speed being
        // the user's, not the demo's (play() applies it).
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
        // up on a demo (carrySettings) — a fresh engine starts muted, and the
        // standby is kept silent, so a run moving on to another demo's clip
        // would mute again.
        // Sound starts on; the browser holds the audio until the first play
        // (FteController.play resumes it).
        this.teamInfo = false;
        this.sound = true;
        // The player last followed by hand, by name — userids differ from
        // demo to demo: followed on the next engine when it has them, else
        // the engine's autotrack. null: autotrack.
        /** @type {string | null} */
        this.trackedName = null;
        /** @type {DemoHandle | null} */
        this.handle = null;
        /** @type {HTMLElement | null} */
        this.host = null;
        // The engine shown: its iframe and the demo it holds or loads.
        /** @type {Slot | null} */
        this.shown = null;
        /** @type {Promise<void> | null} */
        this.demoWait = null;      // the demo's bytes, while they download
        this.wakeHooked = false;   // the page's gestures wake the audio (attachHost)
        // The shown engine, once it plays the current demo.
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
        // The other engine (prepare: the light player's runs) — in the same
        // host under the shown one, invisible, paused at the next clip's
        // start with its frame loop stopped. The demo store moving to its
        // demo shows it, and the one shown before waits in its place: no
        // engine boots for that step, nor for any after it — the next demo
        // loads into whichever engine is not shown (loadInto).
        // `standbyWanted`: the demo asked for, until it can load (the shown
        // engine up, the bytes in, the standby not loading another);
        // `standbyReady`: the standby is parked at the clip asked for.
        /** @type {Slot | null} */
        this.standby = null;
        /** @type {{ handle: DemoHandle, demoT: number, track: Track } | null} */
        this.standbyWanted = null;
        this.standbyReady = store(false);

        demo.subscribe((handle) => {
            if (handle === this.handle) return;
            this.handle = handle;
            this.switchTo(handle);
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

    // Wanted on the tab but not up yet: the clock waits for it — unless it
    // cannot start (the demo file did not download).
    loading() {
        return this.active() && !this.fte && !this.status.get().error;
    }

    offset() {
        return demoOffsetSec(this.handle?.result);
    }

    // attachHost is called by PlayerFrame with the element the iframe lives in.
    /** @param {HTMLElement} el */
    attachHost(el) {
        this.host = el;
        // Sound starts on, but the browser holds the audio until a user
        // gesture: the first click or key anywhere on the page wakes it
        // (one inside the game view, hookScreen).
        if (!this.wakeHooked) {
            this.wakeHooked = true;
            const wake = () => this.fte?.wakeAudio();
            el.ownerDocument.addEventListener('pointerdown', wake, true);
            el.ownerDocument.addEventListener('keydown', wake, true);
        }
        if (this.active()) this.ensure();
    }

    // ensure has the shown engine play the current demo once its bytes are
    // in: at once for a local file, after the hub CDN download for a demo
    // read from mvd-api, which fetches them only when something wants to
    // play it (DemoHandle.demo()). An engine already shown loads it (no
    // boot); none, one starts. A teardown or another demo meanwhile drops it.
    ensure() {
        const shown = this.shown;
        if (this.demoWait || !this.handle?.result || !this.host) return;
        // Playing it, or loading another: that load ends in booted(), which
        // asks again.
        if (shown && (shown.handle === this.handle || shown.loading)) return;
        const handle = this.handle;
        const wait = handle.demo().then((file) => {
            if (this.demoWait !== wait) return;
            this.demoWait = null;
            if (this.handle !== handle || !this.host || !this.active()) return;
            const s = this.shown;
            if (s?.loading) return;
            if (s?.ctl) this.loadInto(s, handle, file);
            else this.spawn(handle, file, false);
        }, (/** @type {unknown} */ err) => {
            if (this.demoWait !== wait) return;
            this.demoWait = null;
            // Shown over the frame (PlayerFrame); the next ensure() — the tab
            // shown again — tries again.
            console.error('player: the demo download failed:', err);
            this.status.set({ ...this.status.get(), error: `The demo file could not be downloaded (${/** @type {Error} */ (err)?.message || err}).` });
        });
        this.demoWait = wait;
        if (this.status.get().error) this.status.set({ ...this.status.get(), error: '' });
    }

    // switchTo: the demo store moved on to `handle`. Its demo in the
    // standby: the two swap, the engine shown before waiting in the
    // standby's place with its demo (a run that comes back to it, or the
    // next demo, loads there). Another demo: the shown engine loads it
    // (ensure) — or a standby that is up does, the two swapping as before.
    // Two engines at most ever live; none once no demo is.
    /** @param {DemoHandle | null} handle */
    switchTo(handle) {
        if (!handle) {
            this.teardown();
            this.dropStandby();
            return;
        }
        const sb = this.standby;
        this.resetShown();
        if (sb && (sb.handle === handle || sb.ctl)) {
            const out = this.shown;
            this.standby = null;
            this.standbyReady.set(false);
            this.shown = sb;
            sb.iframe.classList.remove('player-standby');
            if (out) this.keepAsStandby(out);
            if (sb.ctl) sb.ctl.throttle(1);
            // Holding this demo: shown at once — parked at its clip, as it
            // is; stopped where it was, opening at the cursor. Still loading
            // it: its load adopts it (booted); another demo: ensure loads
            // this one.
            if (sb.ctl && sb.handle === handle && !sb.loading) this.adopt(sb.ctl, !sb.ready);
        }
    }

    // spawn starts an engine (a new iframe) on a demo's bytes: the shown
    // one, or the standby. Whichever it is by the time its demo runs takes
    // it (booted) — a standby shown while it booted is the shown one by then.
    /**
     * @param {DemoHandle} handle
     * @param {{ blob: Blob, gzipped: boolean }} file
     * @param {boolean} standby
     * @returns {Slot}
     */
    spawn(handle, { blob, gzipped }, standby) {
        const host = /** @type {HTMLElement} */ (this.host);
        const demoUrl = URL.createObjectURL(blob);
        const demoPath = gzipped ? 'qw/match.mvd.gz' : 'qw/match.mvd';
        const iframe = document.createElement('iframe');
        iframe.src = 'fte-player/player.html';
        iframe.title = 'Demo player';
        iframe.allow = 'fullscreen; autoplay';
        /** @type {Slot} */
        const slot = { iframe, blobUrl: demoUrl, ctl: null, handle, demoT: null, track: 'auto', loading: true, ready: false, seq: 0 };
        const live = () => this.shown === slot || this.standby === slot;
        iframe.addEventListener('load', () => {
            if (!live()) return;
            // player.html's window.mvdFte (mvd-fte-player/README.md).
            this.hookScreen(/** @type {Window} */ (iframe.contentWindow));
            /** @type {any} */ (iframe.contentWindow).mvdFte.start({ demoUrl, demoPath, mapName: mapFileKey(handle.result), fetchAsset })
                .then((/** @type {FteController} */ fte) => {
                    if (this.standby === slot) fte.throttle(STANDBY_EVERY);
                    return waitForDemoClock(fte, live).then(() => fte);
                })
                .then((/** @type {FteController} */ fte) => {
                    slot.ctl = fte;
                    if (live()) this.booted(slot);
                }, (/** @type {unknown} */ err) => console.error('player:', err));
        }, { once: true });
        if (standby) {
            // Over the shown one in the host, but drawn transparent and
            // taking no input: still rendering (a display:none frame gets
            // no frames), throttled once it can be (STANDBY_EVERY).
            iframe.classList.add('player-standby');
            this.standby = slot;
        } else {
            this.shown = slot;
        }
        host.appendChild(iframe);
        return slot;
    }

    // loadInto plays another demo in an engine that is up — the bytes in,
    // the map fetched when the engine has not got it (FteController.loadDemo)
    // — then hands it on as a boot would (booted). The shown one stops
    // playing the demo before meanwhile; the standby loads throttled.
    /**
     * @param {Slot} slot  one with an engine (ctl), not loading
     * @param {DemoHandle} handle
     * @param {{ blob: Blob, gzipped: boolean }} file
     */
    loadInto(slot, handle, { blob, gzipped }) {
        const ctl = /** @type {FteController} */ (slot.ctl);
        slot.handle = handle;
        slot.loading = true;
        slot.ready = false;
        const seq = ++slot.seq;
        const live = () => slot.seq === seq && (this.shown === slot || this.standby === slot);
        ctl.pause();
        ctl.resume();
        ctl.throttle(this.standby === slot ? STANDBY_EVERY : 1);
        const map = mapFileKey(handle.result);
        const bsp = map && !ctl.hasFile(mapPath(map)) ? fetchAsset(mapUrl(map)).catch(() => null) : null;
        Promise.all([blob.arrayBuffer(), bsp]).then(([demo, mapBytes]) => {
            if (!live()) return false;
            // A map that could not be had is left to the engine
            // (cl_download_mapsrc, config.cfg).
            return ctl.loadDemo({ demo, gzipped, files: mapBytes ? { [mapPath(map)]: mapBytes } : {} }).then(() => true);
        }).then((done) => {
            if (done && live()) this.booted(slot);
        }, (/** @type {unknown} */ err) => console.error('player:', err));
    }

    // booted: a slot's engine plays its demo. Shown, it is adopted — or,
    // the demo store having moved on meanwhile, loads that one (ensure).
    // The standby parks at its clip, or loads the demo asked for since.
    /** @param {Slot} slot */
    booted(slot) {
        slot.loading = false;
        const ctl = /** @type {FteController} */ (slot.ctl);
        if (slot === this.shown) {
            if (slot.handle === this.handle) this.adopt(ctl);
            else if (this.active()) this.ensure();
        } else if (slot === this.standby) {
            if (this.standbyWanted) this.spawnStandby();
            else this.park(slot);
        }
    }

    // adopt takes an engine once its demo runs: the settings carried from
    // the engines before, then whatever waited for it (whenReady — a
    // playlist clip, which so sets its POV after the carried one). `open`:
    // jump to where the cursor is (a standby is at its clip already, and
    // the cursor still holds the demo before's time).
    /**
     * @param {FteController} fte
     * @param {boolean} [open]
     */
    adopt(fte, open = true) {
        this.fte = fte;
        this.carrySettings(fte);
        // Open where the cursor already is, paused — the same entry hub's
        // ?from= gives.
        if (open && this.clock.t > 0) this.jump(this.clock.t);
        fte.pause();
        if (this.active()) {
            fte.resume();
            this.startSync();
        } else {
            fte.suspend();
        }
        this.publish();
        const onReady = this.onReady;
        this.onReady = null;
        if (onReady) onReady();
        this.spawnStandby();
    }

    // prepare(handle, demoT, track): have the standby on that demo, parked
    // at demoT with that POV, for the clip that plays next (the light
    // player's runs). Only one waits: another demo asked for replaces it.
    /**
     * @param {DemoHandle} handle
     * @param {number} demoT
     * @param {Track} [track]
     */
    prepare(handle, demoT, track = 'auto') {
        if (handle === this.handle) return;
        const sb = this.standby;
        if (sb?.handle === handle && !this.standbyWanted) {
            if (sb.demoT === demoT && sb.track === track) return;
            sb.demoT = demoT;
            sb.track = track;
            this.standbyReady.set(false);
            if (sb.ctl && !sb.loading) this.park(sb);
            return;
        }
        if (this.standbyWanted?.handle === handle) return;
        this.standbyWanted = { handle, demoT, track };
        this.standbyReady.set(false);
        this.spawnStandby();
    }

    // spawnStandby has the standby asked for load once the shown engine is
    // up — two loading at once would both be slow — and its bytes are in:
    // into the standby engine when there is one, else in a new one.
    spawnStandby() {
        const want = this.standbyWanted;
        if (!want || !this.fte || !this.host || this.standby?.loading) return;
        want.handle.demo().then((file) => {
            const sb = this.standby;
            if (this.standbyWanted !== want || !this.fte || !this.host || sb?.loading) return;
            this.standbyWanted = null;
            if (sb) {
                sb.demoT = want.demoT;
                sb.track = want.track;
                this.loadInto(sb, want.handle, file);
            } else {
                const slot = this.spawn(want.handle, file, true);
                slot.demoT = want.demoT;
                slot.track = want.track;
            }
        }, () => {});
    }

    // park: the standby plays its demo — silent, throttled, at its clip's
    // start with its POV, paused, and once the jump has landed, its frame
    // loop stopped (standbyReady).
    /** @param {Slot} slot */
    park(slot) {
        const ctl = /** @type {FteController} */ (slot.ctl);
        const seq = ++slot.seq;
        ctl.resume();
        ctl.throttle(STANDBY_EVERY);
        ctl.mute();
        ctl.pause();
        const demoT = slot.demoT;
        if (demoT == null) {
            ctl.suspend();
            return;
        }
        ctl.demoJump(demoT);
        if (slot.track === 'auto') ctl.enableAutotrack();
        else ctl.track(slot.track);
        const started = performance.now();
        const poll = setInterval(() => {
            if (this.standby !== slot || slot.seq !== seq) return clearInterval(poll);
            if (Math.abs(ctl.getDemoElapsedTime() - Math.floor(demoT)) < 1.5 || performance.now() - started > CLOCK_WAIT_MS) {
                clearInterval(poll);
                ctl.suspend();
                slot.ready = true;
                this.standbyReady.set(true);
            }
        }, 100);
    }

    // keepAsStandby: the engine shown until now waits hidden, silent and
    // stopped where it was, its demo still in — for a run that comes back
    // to it, or to load the next demo into. One still booting is dropped.
    /** @param {Slot} out */
    keepAsStandby(out) {
        if (!out.ctl) return removeSlot(out);
        out.iframe.classList.add('player-standby');
        if (document.activeElement === out.iframe) out.iframe.blur();
        out.ctl.mute();
        out.ctl.pause();
        out.ctl.suspend();
        out.ready = false;
        out.demoT = null;
        this.standby = out;
    }

    dropStandby() {
        this.standbyWanted = null;
        this.standbyReady.set(false);
        const sb = this.standby;
        this.standby = null;
        if (sb) removeSlot(sb);
    }

    // carrySettings sets an engine come up on a demo to the settings the
    // user picked before (team info, sound, the followed player; the rate
    // applies on play).
    /** @param {FteController} fte */
    carrySettings(fte) {
        if (this.teamInfo) fte.command('hud_teaminfo_show 1');
        if (this.sound) fte.unmute();
        const id = this.trackedName == null ? null : userIdIn(fte, this.trackedName);
        if (id != null) fte.track(id);
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
        win.addEventListener('pointerdown', () => this.fte?.wakeAudio(), true);
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

    // The shown engine's iframe (PlayerLayer hooks its wheel).
    get iframe() {
        return this.shown?.iframe ?? null;
    }

    // resetShown: the shown engine no longer plays the current demo — the
    // sync, the seek and whatever waited for it go; the engine stays.
    resetShown() {
        this.stopSync();
        if (this.seekTimer) clearTimeout(this.seekTimer);
        Object.assign(this, {
            demoWait: null, fte: null, seekTimer: null,
            pendingSeek: null, seekTarget: null, onReady: null,
            slow: null, consoleOpen: false,
        });
        this.status.set({ ...IDLE, rate: this.rate });
    }

    // teardown drops the shown engine; removing the iframe frees it.
    teardown() {
        this.resetShown();
        if (this.shown) removeSlot(this.shown);
        this.shown = null;
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
            error: '',
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

    // track(userid | 'auto'). `pick`: the user's own choice, carried to the
    // next engines (carrySettings); a playlist clip's POV is not — the
    // playlist set it, and a demo loaded by hand later should not inherit it.
    /**
     * @param {Track} userid
     * @param {{ pick?: boolean }} [opts]
     */
    track(userid, { pick = true } = {}) {
        if (!this.fte) return;
        if (userid === 'auto') this.fte.enableAutotrack();
        else this.fte.track(userid);
        if (pick) this.trackedName = userid === 'auto' ? null : nameIn(this.fte, userid);
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

/**
 * An engine and the demo it holds: `ctl` once it is up, `loading` while it
 * boots or loads a demo, `demoT` / `track` where the standby parks (null:
 * where it stopped), `ready` once it is parked there; `seq` drops a load or
 * a park overtaken by another.
 * @typedef {{ iframe: HTMLIFrameElement, blobUrl: string, ctl: FteController | null, handle: DemoHandle, demoT: number | null, track: Track, loading: boolean, ready: boolean, seq: number }} Slot
 */

/** @param {Slot} slot */
function removeSlot(slot) {
    slot.iframe.remove();
    URL.revokeObjectURL(slot.blobUrl);
}

// The engine's own player list, not the Result's: the light player's
// stand-in Result has no userids (src/play/light-demo.js).
/**
 * @param {FteController} fte
 * @param {string} name
 * @returns {number | null}
 */
function userIdIn(fte, name) {
    const p = fte.getPlayers().find((/** @type {any} */ x) => x.getNamePlain() === name);
    return p ? p.userid : null;
}

/**
 * @param {FteController} fte
 * @param {number} userid
 * @returns {string | null}
 */
function nameIn(fte, userid) {
    const p = fte.getPlayers().find((/** @type {any} */ x) => x.userid === Number(userid));
    return p ? p.getNamePlain() : null;
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
