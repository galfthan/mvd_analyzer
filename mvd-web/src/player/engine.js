// engine.js — the Player tab's engine: mvd-fte-player (ftewebgl, controller
// ported from hub) in a SAME-ORIGIN iframe, fed the bytes of the demo we
// analysed through a blob URL — no second download, and local files play
// too. Same origin is the point: we drive the engine directly via
// iframe.contentWindow (seek, POV, speed) instead of reloading a
// cross-origin hub embed, which accepts no commands. The engine boots on the
// first visit to the tab (~6 MB of wasm plus assets) and is discarded on the
// next demo load; one iframe page holds one engine for its lifetime.
//
// Clock: FTE runs on demo-relative seconds, the page on match-relative
// ones (demoOffsetSec). While the tab is active the engine is the clock: a
// poll once per display frame pushes its time into the page clock as an
// 'engine' seek — the Map pane's follow camera moves with it, so a slower
// poll shows as stepping — and any other seek (scrub, time links) becomes a
// throttled demo_jump.

import { store } from '../state/store.js';
import { demoOffsetSec, mapFileKey, userIdOf, nameOfUserId } from '../lib/result.js';

const SEEK_THROTTLE_MS = 150;
// demo_jump lands on whole seconds and takes a few frames; until the engine
// clock is near the target (or this long has passed), the sync poll must
// not drag the cursor back to the pre-jump time.
const SEEK_SETTLE_MS = 2000;
// The engine reports ready (Module.getClientState exists) before `playdemo`
// has started the demo, and a demo_jump sent then is lost — seen as the
// opening jump landing at 0:00. Wait for the demo clock to move.
const CLOCK_WAIT_MS = 30000;

const IDLE = { ready: false, playing: false, speed: 100, ended: false, track: null, autotrack: false, muted: true };

export class PlayerEngine {
    constructor({ clock, demo, activeTab }) {
        this.clock = clock;
        this.activeTab = activeTab;
        // What the POV bar, the timeline's speed buttons and the panes show.
        this.status = store({ ...IDLE });
        this.handle = null;
        this.host = null;
        this.iframe = null;
        this.blobUrl = null;
        this.fte = null;
        this.syncFrame = null;
        this.seekTimer = null;
        this.pendingSeek = null;   // match time awaiting the throttled jump
        this.seekTarget = null;    // demo time of the last jump, until reached
        this.seekIssuedAt = 0;
        this.onReady = null;       // run once when the engine is up
        this.tickListeners = new Set();

        demo.subscribe((handle) => {
            if (handle === this.handle) return;
            this.teardown();
            this.handle = handle;
            if (this.tabActive()) this.ensure();
        });
        activeTab.subscribe((tab) => {
            if (tab === 'player') {
                this.ensure();
                this.startSync();
            } else if (this.fte) {
                this.stopSync();
                this.fte.pause();
                this.publish();
            }
        });
        clock.subscribe(({ t, from }) => {
            if (from !== 'engine' && this.drives()) this.requestSeek(t);
        });
    }

    tabActive() {
        return this.activeTab.get() === 'player';
    }

    // The engine is the page clock while it is up and its tab is showing.
    drives() {
        return !!this.fte && this.tabActive();
    }

    offset() {
        return demoOffsetSec(this.handle?.result);
    }

    // attachHost is called by PlayerFrame with the element the iframe lives in.
    attachHost(el) {
        this.host = el;
        if (this.tabActive()) this.ensure();
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
            iframe.contentWindow.mvdFte.start({ demoUrl, demoPath, mapName: mapFileKey(handle.result) })
                .then((fte) => waitForDemoClock(fte, () => this.iframe === iframe).then(() => fte))
                .then((fte) => {
                    if (this.iframe !== iframe) return;
                    this.fte = fte;
                    // Open where the cursor already is, paused — the same
                    // entry hub's ?from= gives.
                    if (this.clock.t > 0) this.jump(this.clock.t);
                    fte.pause();
                    if (this.tabActive()) this.startSync();
                    this.publish();
                    const onReady = this.onReady;
                    this.onReady = null;
                    if (onReady) onReady();
                }, (err) => console.error('player:', err));
        }, { once: true });
        this.blobUrl = demoUrl;
        this.iframe = iframe;
        this.host.appendChild(iframe);
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
    onTick(fn) {
        this.tickListeners.add(fn);
        return () => this.tickListeners.delete(fn);
    }

    // A jump is in flight: the engine clock still shows the pre-jump time.
    seeking() {
        return this.pendingSeek != null || this.seekTarget != null;
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
            const t = demoT - this.offset();
            if (Math.abs(t - this.clock.t) > 0.01) this.clock.seek(t, 'engine');
        }
        this.publish();
        for (const fn of this.tickListeners) fn(demoT);
    }

    // publish refreshes the status store when anything in it changed.
    publish() {
        const fte = this.fte;
        if (!fte) return;
        const next = {
            ready: true,
            playing: fte.isPlaying(),
            speed: fte.getDemoSpeed(),
            ended: fte.hasEnded(),
            track: fte.getTrackUserid(),
            autotrack: fte.isUsingAutotrack(),
            muted: fte.isMuted(),
        };
        const cur = this.status.get();
        if (Object.keys(next).some((k) => next[k] !== cur[k])) this.status.set(next);
    }

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

    jump(matchTime) {
        const demoT = matchTime + this.offset();
        this.seekTarget = Math.floor(demoT);
        this.seekIssuedAt = performance.now();
        this.fte.demoJump(demoT);
    }

    // jumpToDemoTime seeks the engine directly. A page seek clamps to the
    // match window, so a clip in the countdown or after the match end would
    // land outside itself and its run would never see `to` crossed; the
    // cursor shows the clamped match time meanwhile.
    jumpToDemoTime(demoT) {
        this.clock.seek(Math.max(0, demoT - this.offset()), 'engine');
        if (this.seekTimer) clearTimeout(this.seekTimer);
        this.seekTimer = null;
        this.pendingSeek = null;
        this.seekTarget = Math.floor(demoT);
        this.seekIssuedAt = performance.now();
        this.fte.demoJump(demoT);
    }

    // The engine's clock in demo seconds, or the cursor's before it is up.
    demoNow() {
        if (this.fte) return this.fte.getDemoElapsedTime();
        return this.clock.t + this.offset();
    }

    // The timeline's speed buttons, mapped onto demo_setspeed (percent).
    // Same toggle semantics as the map's playback: pressing the running
    // speed pauses. Play after the demo ran out starts over at match start.
    setSpeed(speed) {
        const fte = this.fte;
        if (!fte || speed < 0) return;
        const pct = Math.round(speed * 100);
        if (fte.hasEnded()) this.jump(0);
        if (fte.isPlaying() && fte.getDemoSpeed() === pct) fte.pause();
        else fte.setDemoSpeed(pct);
        this.publish();
    }

    play() {
        if (this.fte && !this.fte.isPlaying()) this.fte.play();
        this.publish();
    }

    pause() {
        this.fte?.pause();
        this.publish();
    }

    // track(userid | 'auto')
    track(userid) {
        if (!this.fte) return;
        if (userid === 'auto') this.fte.enableAutotrack();
        else this.fte.track(userid);
        this.publish();
    }

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

    toggleMute() {
        this.fte?.toggleMute();
        this.publish();
    }

    // whenReady(fn) runs fn once the engine is up (now, if it is).
    whenReady(fn) {
        if (this.fte) fn();
        else {
            this.onReady = fn;
            if (this.tabActive()) this.ensure();
        }
    }
}

// waitForDemoClock resolves once the engine's demo clock moves (or after
// CLOCK_WAIT_MS). `live()` turning false — the engine torn down by a new
// demo — ends the poll too.
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
