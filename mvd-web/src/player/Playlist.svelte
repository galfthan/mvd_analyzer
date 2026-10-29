<script>
    import Play from '@lucide/svelte/icons/play';
    import Square from '@lucide/svelte/icons/square';
    import Link from '@lucide/svelte/icons/link';
    import Share2 from '@lucide/svelte/icons/share-2';
    import Trash2 from '@lucide/svelte/icons/trash-2';
    import ArrowUp from '@lucide/svelte/icons/arrow-up';
    import ArrowDown from '@lucide/svelte/icons/arrow-down';
    import X from '@lucide/svelte/icons/x';
    import { demo, teams, colorOf, teamIndexOf } from '../state/index.js';
    import { formatDuration } from '../lib/result.js';
    import {
        playerFragClips, singleHubGame, hubClipsUrl, encodePlaylist, MAX_HUB_CLIPS,
    } from '../lib/playlist.js';
    import { PRESETS, runQuery } from '../lib/clip-query.js';
    import { userPresets } from './clip-presets.js';
    import { runner, currentRef } from './index.js';
    import { playlist, addClips, setLabel, moveClip, removeClip, clearClips } from './playlist-store.js';
    import { toast } from './toast.js';

    let clips = $derived($playlist.clips);
    let run = $derived($playlist.run);
    let onList = $derived(!!run && !run.transient && run.clips === clips);
    let ref = $derived($demo && $teams ? currentRef() : null);
    // The clip a run over this list is on; -1 matches no row.
    let activeIndex = $derived(onList ? /** @type {NonNullable<typeof run>} */ (run).index : -1);

    let players = $derived.by(() => {
        const ids = $demo?.result?.timelineAnalysis?.playerUserIDs || {};
        const t = $teams;
        const idx = (/** @type {string} */ n) => teamIndexOf(t, n, t.perPlayer);
        return Object.keys(ids).sort((a, b) => (idx(a) - idx(b)) || a.localeCompare(b));
    });

    const when = (/** @type {import('../lib/playlist.js').Clip} */ c) => {
        const off = c.demo?.offset || 0;
        return `${formatDuration(Math.max(0, c.from - off))}–${formatDuration(Math.max(0, c.to - off))}`;
    };

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    async function generate(e) {
        const value = e.currentTarget.value;
        e.currentTarget.value = '';
        const handle = $demo;
        if (!ref || !handle || !value) return;
        /** @type {import('../lib/playlist.js').Clip[]} */
        let out = [];
        let what = '';
        if (value.startsWith('preset:')) {
            const id = value.slice(7);
            const p = PRESETS.find((x) => x.id === id) || $userPresets.find((x) => x.id === id);
            if (!p) return;
            what = p.title.toLowerCase();
            try {
                out = await runQuery(p.query, handle.view.bind(handle), handle.result, ref);
            } catch (err) {
                toast(`No ${what}: ${/** @type {Error} */ (err).message || err}`);
                return;
            }
        } else if (value.startsWith('frags:')) {
            const name = value.slice(6);
            out = playerFragClips(handle.result, ref, name);
            what = `frags of ${name}`;
        }
        if (!out.length) {
            toast(`No ${what} in this demo`);
            return;
        }
        addClips(out);
        toast(`Added ${out.length} clip${out.length === 1 ? '' : 's'}: ${what}`);
    }

    /**
     * @param {string} text
     * @param {string} what
     */
    async function copyText(text, what) {
        try {
            await navigator.clipboard.writeText(text);
            toast(`${what} copied`);
        } catch {
            window.prompt(`Copy the ${what.toLowerCase()}:`, text);
        }
    }

    function copyHubLink() {
        const gameId = singleHubGame(clips);
        if (!gameId) {
            toast(clips.length ? 'A hub link needs every clip from one hub demo — use the share link' : 'The playlist is empty');
            return;
        }
        if (clips.length > MAX_HUB_CLIPS) toast(`Hub plays only the first ${MAX_HUB_CLIPS} clips`);
        copyText(hubClipsUrl(gameId, clips), 'Hub link');
    }

    // Share link: this page, opening the first clip's hub demo in the
    // Player tab, with the whole playlist in the fragment (#playlist=…).
    function copyShareLink() {
        if (!clips.length) {
            toast('The playlist is empty');
            return;
        }
        const first = clips.find((c) => c.demo?.gameId);
        const params = new URLSearchParams();
        if (first) params.set('gameId', String(first.demo.gameId));
        params.set('tab', 'player');
        copyText(`${location.origin}${location.pathname}?${params}#playlist=${encodePlaylist(clips)}`, 'Share link');
    }

    function clear() {
        if (clips.length > 3 && !window.confirm(`Remove all ${clips.length} clips?`)) return;
        clearClips();
    }

    let dragOver = $state(-1);

    /**
     * @param {DragEvent} e  a drag event carries dataTransfer
     * @param {number} i
     */
    function dragStart(e, i) {
        if (/** @type {Element} */ (e.target).closest('input')) return e.preventDefault();
        const dt = /** @type {DataTransfer} */ (e.dataTransfer);
        dt.setData('text/x-mvd-clip', String(i));
        dt.effectAllowed = 'move';
    }

    /**
     * @param {DragEvent} e
     * @param {number} i
     */
    function dragOverRow(e, i) {
        if (!/** @type {DataTransfer} */ (e.dataTransfer).types.includes('text/x-mvd-clip')) return;
        e.preventDefault();
        dragOver = i;
    }

    /**
     * @param {DragEvent} e
     * @param {number} i
     */
    function drop(e, i) {
        dragOver = -1;
        const from = Number(/** @type {DataTransfer} */ (e.dataTransfer).getData('text/x-mvd-clip'));
        if (!Number.isInteger(from)) return;
        e.preventDefault();
        moveClip(from, i);
    }
</script>

<div class="player-playlist">
    <div class="player-playlist-head">
        <span class="pt-group">
            <button class="pt-btn" title="Play the clips in order, here" disabled={!clips.length} onclick={() => runner.start(clips, 0)}><Play size={14} /> Play all</button>
            <button class="pt-btn" title="Stop the scripted run" disabled={!run} onclick={() => runner.stop()}><Square size={13} /> Stop</button>
        </span>
        <select class="pt-select" title="Append generated clips from this demo" onchange={generate}>
            <option value="">Generate…</option>
            {#if ref}
                {#each PRESETS as p (p.id)}
                    <option value="preset:{p.id}">{p.title}</option>
                {/each}
                {#if $userPresets.length}
                    <optgroup label="My presets">
                        {#each $userPresets as p (p.id)}
                            <option value="preset:{p.id}">{p.title}</option>
                        {/each}
                    </optgroup>
                {/if}
                {#if players.length}
                    <optgroup label="Frags of…">
                        {#each players as name (name)}
                            <option value="frags:{name}">{name}</option>
                        {/each}
                    </optgroup>
                {/if}
            {/if}
        </select>
        <span class="pt-group">
            <button class="pt-btn" title="Copy one hub.quakeworld.nu link playing every clip (one hub demo, up to 20 clips)" onclick={copyHubLink}><Link size={14} /> Hub link</button>
            <button class="pt-btn" title="Copy a link to this page carrying the playlist" onclick={copyShareLink}><Share2 size={14} /> Share link</button>
        </span>
        <button class="pt-btn icon" title="Remove every clip" disabled={!clips.length} onclick={clear}><Trash2 size={15} /></button>
        <span class="player-clip-hint">
            {run ? (run.transient ? `Playing: ${run.clips[0].label}` : `Playing ${run.index + 1} / ${run.clips.length}`) : ''}
        </span>
    </div>
    <ol class="playlist-items">
        {#each clips as c, i (c)}
            {@const current = !!ref && c.demo?.key === ref.key}
            <li class="playlist-item" class:other-demo={!current} class:active={activeIndex === i}
                class:drag-over={dragOver === i}
                style:--pov-color={current && c.who ? colorOf($teams, c.who, $teams.perPlayer) : null}
                draggable="true" ondragstart={(e) => dragStart(e, i)} ondragover={(e) => dragOverRow(e, i)}
                ondragleave={() => (dragOver = -1)} ondrop={(e) => drop(e, i)}>
                <button class="play" title="Play from this clip on" onclick={() => runner.start(clips, i)}><Play size={13} /></button>
                <span class="idx" title="Drag to reorder">{i + 1}</span>
                <span class="when">{when(c)}</span>
                <span class="who">{c.who || 'Auto'}</span>
                <input class="label" type="text" value={c.label || ''} placeholder="name this clip"
                    oninput={(e) => setLabel(c, e.currentTarget.value)}>
                <span class="demo-tag">{current ? '' : (c.demo?.title || c.demo?.key || '')}</span>
                <span class="ops">
                    <button class="up" title="Move up" onclick={() => moveClip(i, i - 1)}><ArrowUp size={13} /></button>
                    <button class="down" title="Move down" onclick={() => moveClip(i, i + 1)}><ArrowDown size={13} /></button>
                    <button class="del" title="Remove" onclick={() => removeClip(i)}><X size={13} /></button>
                </span>
            </li>
        {/each}
    </ol>
    {#if !clips.length}
        <div class="player-clip-hint">No clips yet — select a stretch on the playback bar and press its + clip (or c), shift-click a play link on any tab, pick Generate, or build a query in the clip finder.</div>
    {/if}
</div>
