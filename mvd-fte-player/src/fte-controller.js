// @ts-nocheck — mvd-fte-player is its own package; mvd-web's checkJs does not cover it.
// FteController — a thin command/query wrapper around a running FTE
// (ftewebgl) instance playing one demo.
//
// Ported from hub.quakeworld.nu src/lib/fte/fte-controller.ts @ 5c96205c19c7
// (MIT, Copyright (c) 2022 Viktor Persson — see ../LICENSE), minus the
// React/event plumbing and hub's overlay helpers (teams, colours, console
// toggle). Behaviour of what remains is unchanged; deviations are commented.
//
// `win` is the window FTE runs in (it publishes `Module` and `FTEC` there);
// passing it rather than reading globals keeps this usable from the parent
// page of a same-origin iframe as well as from inside it.

export const AUTOTRACK_ON = 'stats';
export const AUTOTRACK_OFF = 'user';

export class FteController {
    constructor(win, { demoDuration = null, demoName = 'match', resumeAudio = null } = {}) {
        this._win = win;
        this._demoDuration = demoDuration;
        this._demoName = demoName;
        this._resumeAudio = resumeAudio;
        this._ended = false;
        this._restartTimer = null;
        this._demoSpeed = 100;
        this._lastPositiveDemoSpeed = 100;
        this._autotrack = AUTOTRACK_ON;
        this._lastTrackUserId = null;
        this._restoreTimer = null;
        this._restoreTarget = 0;
        this._volume = 0;
        this._lastVolume = 0;
        this._maxVolume = 0.2;
        this._gate = this._gateFrameLoop();
    }

    get module() {
        return this._win.Module;
    }

    command(command, value) {
        const str = value !== undefined ? `${command} ${value}` : command;
        try {
            this._win.FTEC.cbufadd(`${str}\n`);
        } catch (e) {
            console.log(`fte command error: ${e}`);
        }
    }

    getClientState() {
        return this.module.getClientState();
    }

    // Demo-relative seconds (0 = first frame of the demo, countdown
    // included) — the same clock as hub's from/to URL params.
    getDemoElapsedTime() {
        try {
            return this.module.getDemoTime();
        } catch {
            return 0;
        }
    }

    getPlayers() {
        const players = [];
        try {
            const state = this.getClientState();
            for (let i = 0; i < state.allocated_client_slots; i++) {
                const p = state.getPlayer(i);
                if (p.spectator !== 0 || ['', '[ServeMe]'].includes(p.getNamePlain())) continue;
                players.push(p);
            }
        } catch {
            return [];
        }
        players.sort((a, b) => a.getNamePlain().localeCompare(b.getNamePlain()));
        return players;
    }

    getTrackUserid() {
        try {
            const tracked = this.getClientState().getPlayerView(0).getTrackedPlayer();
            return tracked ? tracked.userid : null;
        } catch {
            return null;
        }
    }

    // demo_jump takes whole seconds. A jump backwards restarts the demo
    // internally, which drops the tracked player and the speed, so both are
    // re-applied once the clock has arrived (hub polls every 25 ms; same here).
    demoJump(demoTime) {
        if (this._restoreTimer) {
            clearInterval(this._restoreTimer);
            this._restoreTimer = null;
        }
        const upper = this._demoDuration != null ? 1.1 + this._demoDuration : Infinity;
        // Hub floors before clamping and can send `demo_jump 601.1`; floor last.
        const target = Math.floor(Math.max(0, Math.min(demoTime, upper)));
        if (this._ended || this._restartTimer) {
            this._restart(target);
            return;
        }
        const now = this.getDemoElapsedTime();
        const current = Math.floor(now);
        if (Math.abs(target - current) < 1) return;

        this.command('demo_jump', target);

        if (target < current) {
            this._restoreTarget = target;
            this._restoreFrom = now;
            this._restoreTimer = setInterval(() => this._restorePlayback(), 25);
        }
    }

    // Not in hub: once the demo runs out FTE disconnects ("EndOfDemo") and
    // answers every demo_jump with "not playing a demo", so a seek after the
    // end was a dead end. The loader forwards that console line to
    // demoEnded(); the next seek starts the demo again, jumps once the fresh
    // clock is running, and re-applies track + speed as after a rewind.
    demoEnded() {
        this._ended = true;
    }

    hasEnded() {
        return this._ended;
    }

    // True from a post-end seek until the restarted demo has been jumped.
    isRestarting() {
        return this._restartTimer !== null;
    }

    _restart(target) {
        if (this._restartTimer) clearInterval(this._restartTimer);
        if (this._ended) {
            this._ended = false;
            this._restartFrom = this.getDemoElapsedTime();
            this.command('playdemo', this._demoName);
        }
        // Two steps: wait for the fresh demo's clock, jump, then wait for the
        // clock to reach the target (fast-forwarding from 0 takes seconds)
        // before restoring — isRestarting() stays true throughout.
        let jumped = false;
        this._restartTimer = setInterval(() => {
            const t = this.getDemoElapsedTime();
            if (this._ended) {
                // Target was past the end; it ran out again.
                clearInterval(this._restartTimer);
                this._restartTimer = null;
                return;
            }
            if (!jumped) {
                if (!(t > 0 && t < this._restartFrom - 1)) return;
                jumped = true;
                this.command('demo_jump', target);
                return;
            }
            if (t < target - 1) return;
            clearInterval(this._restartTimer);
            this._restartTimer = null;
            this._restoreTarget = target;
            this._restorePlayback(true);
        }, 25);
    }

    _restorePlayback(now = false) {
        if (!now) {
            if (!this._restoreTimer) return;
            // Deviation from hub, which waits for the clock to sit within
            // 1 s of the target: a slow frame can carry it past that window
            // and the restore never fires. Arrived = back below where the
            // jump started (the demo clock never runs backwards by itself)
            // and at (or past) the target. The start is the exact clock, not
            // the whole second the jump compares: a rewind of one second
            // (66.0 → 65) lands less than a second below it.
            const t = this.getDemoElapsedTime();
            if (!(t < this._restoreFrom - 0.25 && t >= this._restoreTarget - 0.5)) return;
            clearInterval(this._restoreTimer);
            this._restoreTimer = null;
        }
        if (this.isUsingAutotrack()) this.enableAutotrack();
        else if (this._lastTrackUserId) this.track(this._lastTrackUserId);
        this.command('demo_setspeed', this._demoSpeed);
    }

    // Not in hub: suspend() stops the engine's frame loop while its page is
    // not shown, resume() restarts it. FTE drives itself with its own loop
    // (FTEC.step, rescheduling through requestAnimationFrame — emscripten's
    // Module.pauseMainLoop exists in the build but does not drive it), and a
    // hidden same-origin iframe keeps rendering at 60 fps. The controller
    // gates the loop from the start by wrapping FTEC.step, which the loop
    // reschedules by name: a suspended step
    // parks instead of running (so no second loop can start), after one
    // more frame so commands queued just before — the host's pause — have
    // run. resume() restarts a parked loop; one that never parked carries on.
    _gateFrameLoop() {
        const ftec = this._win.FTEC;
        const step = ftec.step;
        const g = { suspended: false, grace: 0, parked: false };
        ftec.step = (ts) => {
            if (g.suspended && g.grace-- <= 0) {
                g.parked = true;
                return;
            }
            step(ts);
        };
        return g;
    }

    suspend() {
        this._gate.suspended = true;
        this._gate.grace = 1;
    }

    resume() {
        const g = this._gate;
        if (!g.suspended) return;
        g.suspended = false;
        if (g.parked) {
            g.parked = false;
            this._win.requestAnimationFrame(this._win.FTEC.step);
        }
    }

    // Speed in percent: 100 = normal, 0 = paused.
    getDemoSpeed() {
        return this._demoSpeed;
    }

    setDemoSpeed(speed) {
        if (this._demoSpeed > 0) this._lastPositiveDemoSpeed = this._demoSpeed;
        this._demoSpeed = Number(speed);
        this.command('demo_setspeed', this._demoSpeed);
    }

    play() { this.setDemoSpeed(this._lastPositiveDemoSpeed); }
    pause() { this.setDemoSpeed(0); }
    isPlaying() { return this._demoSpeed > 0 && !this._ended; }
    togglePlay() { this.isPlaying() ? this.pause() : this.play(); }

    isUsingAutotrack() {
        return this._autotrack === AUTOTRACK_ON;
    }

    enableAutotrack() {
        this._autotrack = AUTOTRACK_ON;
        this.command('cl_autotrack', this._autotrack);
    }

    disableAutotrack() {
        this._lastTrackUserId = this.getTrackUserid();
        this._autotrack = AUTOTRACK_OFF;
        this.command('cl_autotrack', this._autotrack);
    }

    track(userid) {
        if (this.isUsingAutotrack()) this.disableAutotrack();
        this._lastTrackUserId = Number(userid);
        this.command('track', this._lastTrackUserId);
    }

    isMuted() {
        return this._volume === 0;
    }

    setVolume(value) {
        this._lastVolume = this._volume;
        this._volume = Number(value);
        this.command('volume', this._volume);
    }

    mute() {
        this.setVolume(0);
    }

    // Hub unmutes to a tenth of its slider; we have no slider, so half.
    // Browsers keep audio suspended until a user gesture in the engine's
    // window — resumeAudio (from the loader) resumes it from the host's
    // click, which counts for a same-origin child frame.
    unmute() {
        if (this._resumeAudio) this._resumeAudio();
        if (this._lastVolume <= this._maxVolume / 100) this._lastVolume = this._maxVolume / 2;
        this.setVolume(this._lastVolume);
    }

    toggleMute() {
        this.isMuted() ? this.unmute() : this.mute();
    }
}
