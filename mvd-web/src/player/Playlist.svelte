<script>
    import { demo, teams, colorOf, teamIndexOf } from '../state/index.js';
    import { formatDuration } from '../lib/result.js';
    import {
        GENERATORS, playerFragClips, singleHubGame, hubClipsUrl, encodePlaylist, MAX_HUB_CLIPS,
    } from '../lib/playlist.js';
    import { runner, currentRef } from './index.js';
    import { playlist, addClips, setLabel, moveClip, removeClip, clearClips } from './playlist-store.js';
    import { toast } from './toast.js';

    let clips = $derived($playlist.clips);
    let run = $derived($playlist.run);
    let onList = $derived(!!run && !run.transient && run.clips === clips);
    let ref = $derived($demo && $teams ? currentRef() : null);

    let players = $derived.by(() => {
        const ids = $demo?.result?.timelineAnalysis?.playerUserIDs || {};
        const t = $teams;
        const idx = (n) => teamIndexOf(t, n, t.perPlayer);
        return Object.keys(ids).sort((a, b) => (idx(a) - idx(b)) || a.localeCompare(b));
    });

    const when = (c) => {
        const off = c.demo?.offset || 0;
        return `${formatDuration(Math.max(0, c.from - off))}–${formatDuration(Math.max(0, c.to - off))}`;
    };

    function generate(e) {
        const value = e.currentTarget.value;
        e.currentTarget.value = '';
        if (!ref || !value) return;
        let out = [];
        let what = '';
        if (value.startsWith('gen:')) {
            const g = GENERATORS[value.slice(4)];
            if (!g) return;
            out = g.build($demo.result, ref);
            what = g.title.toLowerCase();
        } else if (value.startsWith('frags:')) {
            const name = value.slice(6);
            out = playerFragClips($demo.result, ref, name);
            what = `frags of ${name}`;
        }
        if (!out.length) {
            toast(`No ${what} in this demo`);
            return;
        }
        addClips(out);
        toast(`Added ${out.length} clip${out.length === 1 ? '' : 's'}: ${what}`);
    }

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
        if (first) params.set('gameId', first.demo.gameId);
        params.set('tab', 'player');
        copyText(`${location.origin}${location.pathname}?${params}#playlist=${encodePlaylist(clips)}`, 'Share link');
    }

    function clear() {
        if (clips.length > 3 && !window.confirm(`Remove all ${clips.length} clips?`)) return;
        clearClips();
    }

    let dragOver = $state(-1);

    function dragStart(e, i) {
        if (e.target.closest('input')) return e.preventDefault();
        e.dataTransfer.setData('text/x-mvd-clip', String(i));
        e.dataTransfer.effectAllowed = 'move';
    }

    function dragOverRow(e, i) {
        if (!e.dataTransfer.types.includes('text/x-mvd-clip')) return;
        e.preventDefault();
        dragOver = i;
    }

    function drop(e, i) {
        dragOver = -1;
        const from = Number(e.dataTransfer.getData('text/x-mvd-clip'));
        if (!Number.isInteger(from)) return;
        e.preventDefault();
        moveClip(from, i);
    }
</script>

<div class="player-playlist">
    <div class="player-playlist-head">
        <h3>Playlist <span class="muted">{clips.length ? `(${clips.length})` : ''}</span></h3>
        <button class="player-pov-btn" title="Play the clips in order, here" onclick={() => runner.start(clips, 0)}>▶ Play all</button>
        <button class="player-pov-btn" title="Stop the scripted run" disabled={!run} onclick={() => runner.stop()}>■ Stop</button>
        <select title="Append generated clips from this demo" onchange={generate}>
            <option value="">Generate…</option>
            {#if ref}
                {#each Object.entries(GENERATORS) as [id, g] (id)}
                    <option value="gen:{id}">{g.title}</option>
                {/each}
                {#if players.length}
                    <optgroup label="Frags of…">
                        {#each players as name (name)}
                            <option value="frags:{name}">{name}</option>
                        {/each}
                    </optgroup>
                {/if}
            {/if}
        </select>
        <button class="player-pov-btn" title="Copy one hub.quakeworld.nu link playing every clip (one hub demo, up to 20 clips)" onclick={copyHubLink}>Copy hub link</button>
        <button class="player-pov-btn" title="Copy a link to this page carrying the playlist" onclick={copyShareLink}>Copy share link</button>
        <button class="player-pov-btn" title="Remove every clip" onclick={clear}>Clear</button>
        <span class="player-clip-hint">
            {run ? (run.transient ? `Playing: ${run.clips[0].label}` : `Playing ${run.index + 1} / ${run.clips.length}`) : ''}
        </span>
    </div>
    <ol class="playlist-items">
        {#each clips as c, i (c)}
            {@const current = !!ref && c.demo?.key === ref.key}
            <li class="playlist-item" class:other-demo={!current} class:active={onList && run.index === i}
                class:drag-over={dragOver === i}
                style:--pov-color={current && c.who ? colorOf($teams, c.who, $teams.perPlayer) : null}
                draggable="true" ondragstart={(e) => dragStart(e, i)} ondragover={(e) => dragOverRow(e, i)}
                ondragleave={() => (dragOver = -1)} ondrop={(e) => drop(e, i)}>
                <button class="play" title="Play from this clip on" onclick={() => runner.start(clips, i)}>▶</button>
                <span class="idx" title="Drag to reorder">{i + 1}</span>
                <span class="when">{when(c)}</span>
                <span class="who">{c.who || 'Auto'}</span>
                <input class="label" type="text" value={c.label || ''} placeholder="name this clip"
                    oninput={(e) => setLabel(c, e.currentTarget.value)}>
                <span class="demo-tag">{current ? '' : (c.demo?.title || c.demo?.key || '')}</span>
                <span class="ops">
                    <button class="up" title="Move up" onclick={() => moveClip(i, i - 1)}>↑</button>
                    <button class="down" title="Move down" onclick={() => moveClip(i, i + 1)}>↓</button>
                    <button class="del" title="Remove" onclick={() => removeClip(i)}>×</button>
                </span>
            </li>
        {/each}
    </ol>
    {#if !clips.length}
        <div class="player-clip-hint">No clips yet — mark In/Out above, shift-click a Hub link on any tab, or pick Generate.</div>
    {/if}
</div>
