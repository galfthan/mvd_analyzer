<script>
    // The light player (play.html): the clips of a shared playlist
    // (#playlist=…) played in the engine, each with its label — no analyzer,
    // so it opens in the time the engine and the demo take to download. The
    // runner, the engine and the clip card are the Player tab's; the demo
    // handle is light-demo.js's. "Open in the full analyzer" carries the
    // same playlist to the Player tab.
    import { onMount } from 'svelte';
    import Play from '@lucide/svelte/icons/play';
    import Pause from '@lucide/svelte/icons/pause';
    import SkipBack from '@lucide/svelte/icons/skip-back';
    import SkipForward from '@lucide/svelte/icons/skip-forward';
    import Volume2 from '@lucide/svelte/icons/volume-2';
    import VolumeX from '@lucide/svelte/icons/volume-x';
    import Snail from '@lucide/svelte/icons/snail';
    import ExternalLink from '@lucide/svelte/icons/external-link';
    import PlayerFrame from '../player/PlayerFrame.svelte';
    import Toast from '../player/Toast.svelte';
    import { PlayerEngine } from '../player/engine.js';
    import { createRunner } from '../player/runner.js';
    import { demoRefOf } from '../player/demo-ref.js';
    import { playlist } from '../player/playlist-store.js';
    import { toast } from '../player/toast.js';
    import { createClock } from '../state/clock.js';
    import { store } from '../state/store.js';
    import { teams } from '../state/teams.js';
    import { formatDuration } from '../lib/result.js';
    import { loadLightDemo } from './light-demo.js';
    import { createPlayerKeys, nextName } from '../player/player-keys.js';
    import { playerMenuItems } from '../player/player-menu.js';
    import MapMenu from '../ui/MapMenu.svelte';
    import Keyboard from '@lucide/svelte/icons/keyboard';
    import Maximize from '@lucide/svelte/icons/maximize';
    import Minimize from '@lucide/svelte/icons/minimize';

    /** @type {{ clips: import('../lib/playlist.js').Clip[], code: string }} */
    let { clips, code } = $props();

    // The page's own clock and stores: one demo at a time, always "on the
    // Player tab".
    const clock = createClock();
    let picks = 0;
    // The demos downloading or downloaded and not yet shown, by gameId: a
    // pick shares a download in flight, and the runner starts the next
    // clip's demo ahead of it (prefetch).
    /** @type {Map<number, Promise<import('./light-demo.js').LightHandle>>} */
    const downloads = new Map();
    /** @param {number} gameId */
    function download(gameId) {
        let d = downloads.get(gameId);
        if (!d) {
            const offset = clips.find((c) => c.demo?.gameId === gameId)?.demo.offset || 0;
            d = loadLightDemo(gameId, offset);
            downloads.set(gameId, d);
            d.catch(() => downloads.delete(gameId));
        }
        return d;
    }
    const demo = store(/** @type {any} */ (null));
    const engine = new PlayerEngine({ clock, demo, activeTab: store('player') });
    // A shared clip is watched for the fight: both teams' health, armor and
    // weapons show from the start (`t` hides them).
    engine.teamInfo = true;
    const runner = createRunner({
        engine, demo, teams, demoRefOf,
        segment: store(/** @type {{ start: number, end: number } | null} */ (null)),
        selectTab: () => {},
        // A hub clip's demo: download it and hand it to the engine — unless
        // it is the one loaded, or a later pick superseded this one (the
        // last pick wins, as loadDemo's does on the full page).
        // (The runner only ever asks for a hub clip's demo by gameId.)
        loadDemo: async (/** @type {any} */ { gameId }, /** @type {any} */ { stillWanted } = {}) => {
            const pick = ++picks;
            if (demo.get()?.ref.gameId === gameId) return true;
            const handle = await download(gameId);
            if (pick !== picks || (stillWanted && !stillWanted())) return false;
            downloads.delete(gameId);
            demo.set(handle);
            return true;
        },
        prefetch: (c) => { if (c.demo.gameId) download(c.demo.gameId).catch(() => {}); },
    });
    const status = engine.status;

    let now = $state(0);   // demo seconds
    onMount(() => {
        const off = engine.onTick((t) => { now = t; });
        if (playable.length) runner.start(playable, 0);
        return () => { off(); runner.stop(); engine.teardown(); };
    });

    // Only hub clips (and title cards) play here: a local file's clip needs
    // its file.
    const playable = $derived(clips.filter((c) => c.card || c.demo?.gameId));
    const skipped = $derived(clips.length - playable.length);
    const nClips = $derived(clips.filter((c) => !c.card).length);

    let run = $derived($playlist.run);
    let index = $derived(run && run.clips === playable ? run.index : -1);
    let current = $derived(index >= 0 ? playable[index] : null);

    // The full analyzer opens this playlist as its own (`&open`: it
    // replaces the list there rather than adding to it) on the demo of the
    // clip playing, that clip selected (?seg=, match seconds) and the
    // playhead at its start — where the viewer is, to edit from.
    const fullUrl = $derived.by(() => {
        // The clip playing, or the next one after a title card.
        const at = [...playable.slice(Math.max(0, index)), ...playable].find((c) => !c.card);
        const params = new URLSearchParams();
        if (at?.demo.gameId) params.set('gameId', String(at.demo.gameId));
        params.set('tab', 'player');
        if (at) {
            const off = at.demo.offset || 0;
            const start = Math.max(0, at.from - off);
            params.set('seg', `${start}-${Math.max(start, at.to - off)}`);
            params.set('t', String(start));
        }
        return `./?${params}#playlist=${code}&open`;
    });

    /** @param {number} i */
    function playFrom(i) {
        if (i < 0 || i >= playable.length) return;
        runner.start(playable, i);
    }

    function togglePlay() {
        if (index < 0) return playFrom(0);
        engine.togglePlay();
    }
    // A click on the game view plays / pauses; the keys are the analyzer's
    // (src/player/player-keys.js), typed on the page or in the game view.
    engine.screenToggle = togglePlay;
    /** @param {1 | -1} dir */
    function step(dir) {
        const ids = (engine.fte?.getPlayers() || []).map((/** @type {{ userid: number }} */ p) => String(p.userid));
        const id = nextName(ids, String(engine.status.get().track), dir);
        if (id) engine.track(Number(id));
    }
    /** @param {number} by */
    const seek = (by) => clock.seek(Math.max(0, clock.t + by));
    // Fullscreen: the game view and its controls (`f`, the button).
    /** @type {HTMLElement | undefined} */
    let stage = $state();
    let fullscreen = $state(false);
    function toggleFullscreen() {
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
        else stage?.requestFullscreen?.()?.catch(() => {});
    }
    const key = createPlayerKeys({ toggle: togglePlay, step, seek, teamInfo: () => engine.toggleTeamInfo(), fullscreen: toggleFullscreen });

    // The player menu (src/player/player-menu.js), as the Player pane's:
    // the keyboard button or a right-click on the game view.
    let menu = $state(/** @type {{ x: number, y: number } | null} */ (null));
    let menuItems = $derived(playerMenuItems({ engine, status: $status, toggle: togglePlay, step, seek, fullscreen: toggleFullscreen }));
    engine.screenMenu = (x, y) => { menu = { x, y }; };
    engine.screenDismiss = () => {
        if (!menu) return false;
        menu = null;
        return true;
    };

    /** @param {import('../lib/playlist.js').Clip} c */
    const len = (c) => formatDuration(c.card ? c.card.ms / 1000 : c.to - c.from);
</script>

<svelte:window onkeydown={key} />
<svelte:document onfullscreenchange={() => { fullscreen = !!document.fullscreenElement; }} />

<div class="clips-page">
    <header class="clips-head">
        <span class="clips-brand">MVD Analyzer</span>
        <span class="clips-title">{nClips} clip{nClips === 1 ? '' : 's'}</span>
        <a class="clips-full" href={fullUrl} title="Open this playlist in the full analyzer's Player tab to edit it, at the clip playing — with its map, graphs and tables (it analyses the demo first)">
            <ExternalLink size={14} /> Open in the full analyzer
        </a>
    </header>
    {#if !playable.length}
        <p class="clips-empty">{clips.length ? 'These clips are from local files — open them in the full analyzer and load the files there.' : 'No clips in this link.'}</p>
    {:else}
        <div class="clips-stage" bind:this={stage}>
            <div class="clips-screen"><PlayerFrame {engine} /></div>
            <div class="clips-controls">
                <button class="clips-btn" title="Previous clip" disabled={index <= 0} onclick={() => playFrom(index - 1)}><SkipBack size={16} /></button>
                <button class="clips-btn clips-play" title={$status.playing ? 'Pause (space)' : 'Play (space)'} onclick={togglePlay}>
                    {#if $status.playing}<Pause size={18} fill="currentColor" />{:else}<Play size={18} fill="currentColor" />{/if}
                </button>
                <button class="clips-btn" title="Next clip" disabled={index < 0 || index >= playable.length - 1} onclick={() => playFrom(index + 1)}><SkipForward size={16} /></button>
                <span class="clips-now">
                    {#if current}
                        <span class="n">{index + 1} / {playable.length}</span>
                        {#if !current.card}<span class="t">{formatDuration(Math.max(0, Math.min(now, current.to) - current.from))} / {len(current)}</span>{/if}
                        <span class="l">{current.label || current.who || 'Clip'}</span>
                    {:else if $status.ready}
                        <span class="l muted">Pick a clip</span>
                    {:else}
                        <span class="l muted">Loading the player and the demo…</span>
                    {/if}
                </span>
                <button class="clips-btn" title={$status.ready && !$status.muted ? 'Sound on — click to mute' : 'Sound off — click to unmute'} onclick={() => engine.toggleMute()}>
                    {#if $status.ready && !$status.muted}<Volume2 size={16} />{:else}<VolumeX size={16} />{/if}
                </button>
                <button class="clips-btn clips-menu-btn" title="Keys and player options (or right-click the game view)"
                    onclick={(e) => {
                        const el = /** @type {HTMLElement} */ (e.currentTarget.closest('.clips-stage'));
                        menu = menu ? null : { x: el.clientWidth - 230, y: el.clientHeight - 400 };
                    }}><Keyboard size={16} /></button>
                <button class="clips-btn" title={fullscreen ? 'Exit fullscreen (f or Esc)' : 'Fullscreen (f)'} aria-label={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
                    onclick={toggleFullscreen}>
                    {#if fullscreen}<Minimize size={16} />{:else}<Maximize size={16} />{/if}
                </button>
            </div>
            <MapMenu bind:at={menu} items={menuItems} keep=".clips-menu-btn" />
        </div>
        <ol class="clips-list">
            {#each playable as c, i (i)}
                <li class:active={i === index}>
                    <button onclick={() => playFrom(i)} title="Play from this clip on">
                        <span class="i">{i + 1}</span>
                        <span class="l">{c.label || 'Clip'}</span>
                        <span class="w">{c.card ? 'title card' : c.who || 'Auto'}</span>
                        {#if c.slow}<span class="s" title="Slow motion at {c.slow.speed}×"><Snail size={12} /></span>{/if}
                        <span class="d">{len(c)}</span>
                        <span class="g">{c.card ? '' : c.demo.title}</span>
                    </button>
                </li>
            {/each}
        </ol>
        {#if skipped}<p class="clips-empty">{skipped} clip{skipped === 1 ? ' is' : 's are'} from a local file and not shown — open the full analyzer to play {skipped === 1 ? 'it' : 'them'}.</p>{/if}
    {/if}
    <footer class="clips-foot">
        <a href={fullUrl}>Full analyzer — map, timeline, stats and the clip finder for these demos</a>
    </footer>
</div>
<Toast />

<style>
    .clips-page {
        max-width: 1100px;
        margin: 0 auto;
        padding: 12px 16px 32px;
        display: flex;
        flex-direction: column;
        gap: 10px;
    }
    .clips-head {
        display: flex;
        align-items: baseline;
        gap: 12px;
        flex-wrap: wrap;
    }
    .clips-brand {
        font-family: 'Rajdhani', sans-serif;
        font-size: 1.3rem;
        font-weight: 700;
        color: var(--accent-cyan);
    }
    .clips-title {
        color: #9ab;
    }
    .clips-full {
        margin-left: auto;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        padding: 3px 10px;
        border: 1px solid rgba(0, 217, 255, 0.4);
        border-radius: 5px;
        color: var(--accent-cyan);
        text-decoration: none;
        font-size: 0.85rem;
    }
    .clips-full:hover {
        background: var(--accent-cyan);
        color: var(--bg-deep);
    }
    .clips-stage {
        position: relative;
    }
    .clips-screen {
        width: 100%;
        aspect-ratio: 16 / 9;
        max-height: 75vh;
        background: #000;
    }
    .clips-stage:fullscreen {
        display: flex;
        flex-direction: column;
        padding: 0 12px;
        background: #000;
    }
    .clips-stage:fullscreen .clips-screen {
        flex: 1;
        min-height: 0;
        aspect-ratio: auto;
        max-height: none;
    }
    .clips-controls {
        display: flex;
        align-items: center;
        gap: 6px;
        padding: 6px 0;
    }
    .clips-btn {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 30px;
        height: 28px;
        background: none;
        border: none;
        border-radius: 4px;
        color: #bcd;
        cursor: pointer;
    }
    .clips-btn:hover:not(:disabled) {
        background: rgba(0, 217, 255, 0.15);
        color: var(--accent-cyan);
    }
    .clips-btn:disabled {
        opacity: 0.3;
        cursor: default;
    }
    .clips-play {
        color: var(--accent-cyan);
    }
    .clips-now {
        flex: 1;
        display: flex;
        align-items: baseline;
        gap: 10px;
        min-width: 0;
        font-size: 0.9rem;
    }
    .clips-now .n,
    .clips-now .t {
        font-family: monospace;
        color: #889;
        white-space: nowrap;
    }
    .clips-now .l {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .clips-list {
        list-style: none;
        margin: 0;
        padding: 0;
        border-top: 1px solid rgba(42, 63, 95, 0.6);
    }
    .clips-list button {
        display: grid;
        grid-template-columns: 2.2em minmax(0, 1fr) auto auto auto;
        grid-template-areas: 'i l w s d' 'i g g g g';
        align-items: baseline;
        gap: 0 10px;
        width: 100%;
        padding: 6px 8px;
        background: none;
        border: none;
        border-bottom: 1px solid rgba(42, 63, 95, 0.4);
        color: inherit;
        text-align: left;
        cursor: pointer;
        font: inherit;
    }
    .clips-list button:hover {
        background: rgba(255, 255, 255, 0.04);
    }
    .clips-list li.active button {
        background: rgba(0, 217, 255, 0.1);
        box-shadow: inset 3px 0 var(--accent-cyan);
    }
    .clips-list .i { grid-area: i; color: #667; font-family: monospace; }
    .clips-list .l { grid-area: l; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .clips-list .w { grid-area: w; color: #9ab; font-size: 0.85rem; }
    .clips-list .s { grid-area: s; color: #e070f0; }
    .clips-list .d { grid-area: d; color: #889; font-family: monospace; font-size: 0.85rem; }
    .clips-list .g { grid-area: g; color: #667; font-size: 0.8rem; }
    .clips-empty {
        color: #9ab;
    }
    .clips-foot {
        margin-top: 8px;
        font-size: 0.85rem;
    }
    .clips-foot a {
        color: #7a8ba8;
    }
    .muted {
        color: #778;
    }
</style>
