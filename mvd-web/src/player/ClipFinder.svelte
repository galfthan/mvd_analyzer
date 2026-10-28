<script>
    // The clip finder: build a clip query (src/lib/clip-query.js) over the
    // loaded demo — an mvd-api endpoint and its params, a filter, a rank,
    // the clip windows and a label — and see its clips live. Start from a
    // preset, save the result as your own, share it as a link, or add the
    // clips to the playlist. Collapsible; open state is per browser.
    import ChevronRight from '@lucide/svelte/icons/chevron-right';
    import ChevronDown from '@lucide/svelte/icons/chevron-down';
    import Plus from '@lucide/svelte/icons/plus';
    import X from '@lucide/svelte/icons/x';
    import ListPlus from '@lucide/svelte/icons/list-plus';
    import Save from '@lucide/svelte/icons/save';
    import Share2 from '@lucide/svelte/icons/share-2';
    import Trash2 from '@lucide/svelte/icons/trash-2';
    import ExprGroup from './ExprGroup.svelte';
    import { demo, teams, colorOf } from '../state/index.js';
    import { formatDuration } from '../lib/result.js';
    import {
        SOURCES, PRESETS, viewCall, clipsOf, fieldPaths, toDraft, fromDraft, encodeQuery,
    } from '../lib/clip-query.js';
    import { runner, currentRef } from './index.js';
    import { addClips } from './playlist-store.js';
    import { userPresets, savePreset, deletePreset, finderInput } from './clip-presets.js';
    import { toast } from './toast.js';

    /** @typedef {import('../lib/clip-query.js').Draft} Draft */

    const OPEN_KEY = 'mvd.clipFinder.open';
    const SHOWN = 100;

    let open = $state(readOpen());
    function readOpen() {
        try {
            return localStorage.getItem(OPEN_KEY) === '1';
        } catch {
            return false;
        }
    }
    /** @param {boolean} v */
    function setOpen(v) {
        open = v;
        try {
            localStorage.setItem(OPEN_KEY, v ? '1' : '0');
        } catch { /* per-viewer convenience */ }
    }

    let title = $state('');
    let presetId = $state('');   // the user preset the draft was loaded from
    let draft = $state(/** @type {Draft} */ (toDraft(PRESETS[0].query)));

    /**
     * @param {string} t
     * @param {import('../lib/clip-query.js').ClipQuery} q
     * @param {string} [id]
     */
    function load(t, q, id = '') {
        title = t;
        presetId = id;
        draft = toDraft(q);
    }

    // A preset picked elsewhere or a #clipquery= link opens here.
    $effect(() => {
        const input = $finderInput;
        if (!input) return;
        load(input.title, input.query);
        setOpen(true);
    });

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function startFrom(e) {
        const v = e.currentTarget.value;
        e.currentTarget.value = '';
        const builtin = PRESETS.find((p) => p.id === v);
        if (builtin) return load(builtin.title, builtin.query);
        const mine = $userPresets.find((p) => p.id === v);
        if (mine) load(mine.title, mine.query, mine.id);
    }

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function setSource(e) {
        draft.source = e.currentTarget.value;
        draft.params = {};
    }

    let query = $derived(fromDraft(draft));
    let src = $derived(SOURCES[draft.source]);

    // The endpoint's body, refetched when the source or params change (and
    // the demo); filtering, ranking and windows re-run on it without a call.
    let body = $state(/** @type {any} */ (null));
    let error = $state('');
    let fetchKey = $derived(JSON.stringify([query.source, query.params || {}]));
    $effect(() => {
        const handle = $demo;
        const key = fetchKey;
        if (!open || !handle) return;
        let live = true;
        const timer = setTimeout(() => {
            const [source, params] = JSON.parse(key);
            viewCall(handle.view.bind(handle), source, params)
                .then((b) => { if (live) { body = b; error = ''; } })
                .catch((err) => { if (live) { body = null; error = err?.message || String(err); } });
        }, 250);
        return () => { live = false; clearTimeout(timer); };
    });

    let rows = $derived(body ? src.rows(body) : []);
    let fields = $derived(fieldPaths(rows));
    let clips = $derived.by(() => {
        $teams;
        const ref = $demo ? currentRef() : null;
        if (!body || !ref || !$demo) return [];
        try {
            return clipsOf(query, body, $demo.result, ref);
        } catch {
            return [];
        }
    });
    let matched = $derived(clips.length);

    /**
     * @param {MouseEvent | KeyboardEvent} e
     * @param {import('../lib/playlist.js').Clip} c
     */
    function clickClip(e, c) {
        if (e.shiftKey) {
            addClips([{ ...c, demo: { ...c.demo } }]);
            toast(`Added to playlist: ${c.label}`);
        } else {
            runner.start([c], 0, true);
        }
    }

    function addAll() {
        if (!clips.length) return;
        addClips(clips.map((c) => ({ ...c, demo: { ...c.demo } })));
        toast(`Added ${clips.length} clip${clips.length === 1 ? '' : 's'}${title ? `: ${title}` : ''}`);
    }

    function save() {
        const name = window.prompt('Save this query as a preset named:', title || '');
        if (!name?.trim()) return;
        title = name.trim();
        presetId = savePreset(title, query);
        toast(`Saved preset "${title}" — it is in Generate → My presets`);
    }

    function remove() {
        const p = $userPresets.find((x) => x.id === presetId);
        if (!p || !window.confirm(`Delete the preset "${p.title}"?`)) return;
        deletePreset(p.id);
        presetId = '';
    }

    async function share() {
        const params = new URLSearchParams();
        const gameId = $demo ? currentRef()?.gameId : null;
        if (gameId) params.set('gameId', String(gameId));
        params.set('tab', 'player');
        const url = `${location.origin}${location.pathname}?${params}#clipquery=${encodeQuery(query, title)}`;
        try {
            await navigator.clipboard.writeText(url);
            toast('Query link copied');
        } catch {
            window.prompt('Copy the query link:', url);
        }
    }

    function addWindow() {
        draft.clips.push({ from: '', before: '', to: '', after: '', pov: '', label: '' });
    }
</script>

<section class="clip-finder">
    <button class="cf-toggle" aria-expanded={open} onclick={() => setOpen(!open)}>
        {#if open}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
        <h3>Clip finder</h3>
        {#if open && title}<span class="muted">{title}</span>{/if}
    </button>
    {#if open}
        <div class="cf-body">
            <div class="cf-row">
                <select class="pt-select" title="Load a preset into the finder" onchange={startFrom}>
                    <option value="">Start from…</option>
                    {#each PRESETS as p (p.id)}
                        <option value={p.id}>{p.title}</option>
                    {/each}
                    {#if $userPresets.length}
                        <optgroup label="My presets">
                            {#each $userPresets as p (p.id)}
                                <option value={p.id}>{p.title}</option>
                            {/each}
                        </optgroup>
                    {/if}
                </select>
                <label class="cf-label">Source
                    <select class="pt-select" value={draft.source} onchange={setSource}>
                        {#each Object.keys(SOURCES) as s (s)}
                            <option value={s}>/{s}</option>
                        {/each}
                    </select>
                </label>
                {#each Object.entries(src.params) as [name, hint] (name)}
                    <label class="cf-label" title={hint}>{name}
                        <input class="cf-param" type="text" placeholder={hint} bind:value={draft.params[name]}>
                    </label>
                {/each}
            </div>

            <datalist id="cf-fields">
                {#each fields as f (f)}<option value={f}></option>{/each}
            </datalist>

            <div class="cf-section">
                <span class="cf-heading">Keep rows where</span>
                <ExprGroup bind:group={draft.where} fields="cf-fields" />
            </div>

            <div class="cf-row">
                <span class="cf-heading">Rank by</span>
                <input class="cf-field" type="text" list="cf-fields" placeholder="field (none: time order)" bind:value={draft.rank.by}>
                <label class="cf-label"><input type="checkbox" bind:checked={draft.rank.desc}> highest first</label>
                <label class="cf-label">limit <input class="cf-num" type="text" inputmode="numeric" placeholder="all" bind:value={draft.rank.limit}></label>
            </div>

            <div class="cf-section">
                <span class="cf-heading">Clips per row</span>
                {#each draft.clips as c, i (c)}
                    <div class="cf-row">
                        <label class="cf-label">from <input class="cf-field" type="text" list="cf-fields" placeholder={src.clip.from} bind:value={c.from}></label>
                        <label class="cf-label">− <input class="cf-num" type="text" placeholder={String(src.clip.before / 1000)} bind:value={c.before}> s</label>
                        <label class="cf-label">to <input class="cf-field" type="text" list="cf-fields" placeholder={src.clip.to} bind:value={c.to}></label>
                        <label class="cf-label">+ <input class="cf-num" type="text" placeholder={String(src.clip.after / 1000)} bind:value={c.after}> s</label>
                        <label class="cf-label">watch <input class="cf-field" type="text" list="cf-fields" placeholder={src.clip.pov} bind:value={c.pov}></label>
                        <input class="cf-labeltpl" type="text" placeholder="label for this window ({'{'}field{'}'})" bind:value={c.label}>
                        {#if draft.clips.length > 1}
                            <button class="pt-btn icon" title="Remove this window" onclick={() => draft.clips.splice(i, 1)}><X size={13} /></button>
                        {/if}
                    </div>
                {/each}
                <div class="cf-row">
                    {#if draft.clips.length < 4}
                        <button class="pt-btn" title="Another clip per row, e.g. the pickup after the drop" onclick={addWindow}><Plus size={13} /> window</button>
                    {/if}
                    <input class="cf-labeltpl wide" type="text" placeholder="label: {'{'}player{'}'} · {'{'}detail.frags{'}'} frag{'{'}detail.frags|s{'}'}" bind:value={draft.label}>
                </div>
            </div>

            <div class="cf-row cf-actions">
                <button class="pt-btn" disabled={!clips.length} onclick={addAll}><ListPlus size={14} /> Add {clips.length} to playlist</button>
                <button class="pt-btn" onclick={save}><Save size={14} /> Save as preset</button>
                {#if presetId}
                    <button class="pt-btn icon" title="Delete this preset" onclick={remove}><Trash2 size={14} /></button>
                {/if}
                <button class="pt-btn" title="Copy a link to this page carrying the query" onclick={share}><Share2 size={14} /> Link</button>
                <span class="player-clip-hint">
                    {#if !$demo}Load a demo to see clips.{:else if error}{error}{:else if body}{rows.length} rows · {matched} clip{matched === 1 ? '' : 's'}{/if}
                </span>
            </div>

            {#if clips.length}
                <ol class="pp-feed cf-results">
                    {#each clips.slice(0, SHOWN) as c (c)}
                        <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
                        <li title="Click: play · Shift-click: add to playlist" role="button" tabindex="0"
                            onclick={(e) => clickClip(e, c)} onkeydown={(e) => e.key === 'Enter' && clickClip(e, c)}>
                            <span class="t">{formatDuration(Math.max(0, c.from - c.demo.offset))}</span>
                            <span style:color={c.who ? colorOf($teams, c.who, $teams.perPlayer) : '#ccc'}>{c.label || c.who || 'clip'}</span>
                        </li>
                    {/each}
                    {#if clips.length > SHOWN}<li class="muted">… and {clips.length - SHOWN} more</li>{/if}
                </ol>
            {/if}
        </div>
    {/if}
</section>

<style>
    .clip-finder {
        margin-top: 8px;
        border: 1px solid var(--bg-panel);
        border-radius: 4px;
        padding: 4px 8px;
    }
    .cf-toggle {
        display: flex;
        align-items: center;
        gap: 6px;
        width: 100%;
        background: none;
        border: 0;
        color: inherit;
        font: inherit;
        padding: 2px 0;
        cursor: pointer;
    }
    .cf-toggle h3 {
        margin: 0;
        font-size: 15px;
    }
    .cf-body {
        display: flex;
        flex-direction: column;
        gap: 8px;
        padding: 6px 0;
    }
    .cf-row {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
    }
    .cf-section {
        display: flex;
        flex-direction: column;
        gap: 4px;
    }
    .cf-heading {
        font-size: 12px;
        color: #aaa;
        text-transform: uppercase;
        letter-spacing: 0.04em;
    }
    .cf-label {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        color: #bbb;
    }
    .cf-param {
        width: 120px;
    }
    .cf-field {
        width: 150px;
    }
    .cf-num {
        width: 44px;
    }
    .cf-labeltpl {
        flex: 1 1 180px;
        min-width: 140px;
    }
    .clip-finder :global(input[type='text']) {
        height: 24px;
        padding: 0 6px;
        background: var(--bg-deep);
        color: var(--fg-default);
        border: 1px solid rgba(255, 255, 255, 0.12);
        border-radius: 3px;
        font: inherit;
        font-size: 12px;
        min-width: 0;
    }
    .clip-finder :global(input[type='text']:focus) {
        outline: none;
        border-color: var(--accent-cyan);
    }
    .clip-finder :global(input[type='text']::placeholder) {
        color: #777;
    }
    .cf-results {
        max-height: 220px;
        overflow-y: auto;
    }
</style>
