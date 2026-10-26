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
    constructor(win, { demoDuration = null } = {}) {
        this._win = win;
        this._demoDuration = demoDuration;
        this._demoSpeed = 100;
        this._lastPositiveDemoSpeed = 100;
        this._autotrack = AUTOTRACK_ON;
        this._lastTrackUserId = null;
        this._restoreTimer = null;
        this._restoreTarget = 0;
        this._volume = 0;
        this._lastVolume = 0;
        this._maxVolume = 0.2;
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
        const current = Math.floor(this.getDemoElapsedTime());
        if (Math.abs(target - current) < 1) return;

        this.command('demo_jump', target);

        if (target < current) {
            this._restoreTarget = target;
            this._restoreTimer = setInterval(() => this._restorePlayback(), 25);
        }
    }

    _restorePlayback() {
        if (!this._restoreTimer) return;
        if (this.getDemoElapsedTime() - this._restoreTarget > 1) return;
        clearInterval(this._restoreTimer);
        this._restoreTimer = null;
        if (this.isUsingAutotrack()) this.enableAutotrack();
        else if (this._lastTrackUserId) this.track(this._lastTrackUserId);
        this.command('demo_setspeed', this._demoSpeed);
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
    isPlaying() { return this._demoSpeed > 0; }
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

    unmute() {
        if (this._lastVolume <= this._maxVolume / 100) this._lastVolume = this._maxVolume / 10;
        this.setVolume(this._lastVolume);
    }

    toggleMute() {
        this.isMuted() ? this.unmute() : this.mute();
    }
}
