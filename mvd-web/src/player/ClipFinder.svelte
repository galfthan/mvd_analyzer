<script module>
    // What the finder holds survives its pane being collapsed or moved.
    /** @type {{ title: string, presetId: string, about: string, aboutKey: string, draft: import('../lib/clip-query.js').Draft | null, code: boolean }} */
    const memo = { title: '', presetId: '', about: '', aboutKey: '', draft: null, code: false };
</script>

<script>
    // The clip finder: a clip query (src/lib/clip-query.js) over the loaded
    // demo, in four stages, each showing how many rows it passes on — find
    // rows (an mvd-api endpoint and its params), keep the rows a filter
    // (and / or / not groups) matches, order them and keep the first N, cut clips from each row.
    // Every source, param and field is documented from the API spec
    // (api-docs.js). Load a preset, name and save your own, share it as a
    // link, edit it all as code, add the clips to the playlist.
    import Plus from '@lucide/svelte/icons/plus';
    import X from '@lucide/svelte/icons/x';
    import ListPlus from '@lucide/svelte/icons/list-plus';
    import Save from '@lucide/svelte/icons/save';
    import Share2 from '@lucide/svelte/icons/share-2';
    import Trash2 from '@lucide/svelte/icons/trash-2';
    import Info from '@lucide/svelte/icons/info';
    import ArrowDown from '@lucide/svelte/icons/arrow-down';
    import ExprGroup from './ExprGroup.svelte';
    import { demo, teams, colorOf } from '../state/index.js';
    import { formatDuration } from '../lib/result.js';
    import {
        SOURCES, PRESETS, viewCall, stages, fieldPaths, fieldOf, toDraft, fromDraft, encodeQuery,
    } from '../lib/clip-query.js';
    import { parseQueryText, queryToText } from '../lib/clip-query-text.js';
    import { miniMarkdown } from '../lib/mini-md.js';
    import { runner, currentRef } from './index.js';
    import { addClips } from './playlist-store.js';
    import { userPresets, savePreset, deletePreset, finderInput } from './clip-presets.js';
    import { apiDocs, loadApiDocs, fieldDoc, shortDoc } from './api-docs.js';
    import { toast } from './toast.js';

    /** @typedef {import('../lib/clip-query.js').Draft} Draft */

    const SHOWN = 100;
    const LABEL_HELP = 'A label template: {field} is the row\'s value, e.g. {player}; {field|s} adds a plural s unless the value is 1; {field|sec} turns ms into whole seconds; {field|upper} upper-cases.';

    // The first time, the first preset.
    const first = memo.draft ? memo : {
        ...memo, title: PRESETS[0].title, about: PRESETS[0].about, aboutKey: JSON.stringify(fromDraft(toDraft(PRESETS[0].query))), draft: toDraft(PRESETS[0].query),
    };
    let title = $state(first.title);
    let presetId = $state(first.presetId);  // the user preset the draft was loaded from
    // What the loaded built-in preset finds, said while the query is still
    // that preset's.
    let about = $state(first.about);
    let aboutKey = $state(first.aboutKey);
    let draft = $state(/** @type {Draft} */ (first.draft));
    let code = $state(memo.code);           // the whole query as text
    let info = $state(false);

    $effect(() => {
        Object.assign(memo, { title, presetId, about, aboutKey, draft: $state.snapshot(draft), code });
    });

    loadApiDocs();
    let doc = $derived($apiDocs?.[draft.source]);

    /**
     * @param {string} t
     * @param {import('../lib/clip-query.js').ClipQuery} q
     * @param {{ id?: string, about?: string }} [from]
     */
    function load(t, q, from = {}) {
        title = t;
        presetId = from.id || '';
        about = from.about || '';
        draft = toDraft(q);
        aboutKey = JSON.stringify(fromDraft(draft));
        codeText = queryToText(q, t);
        codeError = '';
    }

    // The whole query as code (src/lib/clip-query-text.js): good text
    // replaces the draft and the name, bad text says which line and keeps
    // the last good query running.
    let codeText = $state(queryToText(fromDraft(/** @type {Draft} */ (first.draft)), first.title));
    let codeError = $state('');

    /** @param {boolean} on */
    function setCode(on) {
        if (on) {
            codeText = queryToText(fromDraft(draft), title);
            codeError = '';
        }
        code = on;
    }

    /** @param {Event & { currentTarget: HTMLTextAreaElement }} e */
    function editCode(e) {
        codeText = e.currentTarget.value;
        const r = parseQueryText(codeText);
        if (r.query == null) {
            codeError = r.error;
            return;
        }
        codeError = '';
        draft = toDraft(r.query);
        title = r.title;
    }

    // A preset picked elsewhere or a #clipquery= link opens here.
    $effect(() => {
        const input = $finderInput;
        if (!input) return;
        finderInput.set(null);
        load(input.title, input.query);
    });

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function pickPreset(e) {
        const v = e.currentTarget.value;
        e.currentTarget.value = '';
        const builtin = PRESETS.find((p) => p.id === v);
        if (builtin) return load(builtin.title, builtin.query, { about: builtin.about });
        const mine = $userPresets.find((p) => p.id === v);
        if (mine) load(mine.title, mine.query, { id: mine.id });
    }

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function setSource(e) {
        draft.source = e.currentTarget.value;
        draft.params = {};
    }

    let query = $derived(fromDraft(draft));
    let src = $derived(SOURCES[draft.source]);

    // The endpoint's body, refetched when the source or params change (and
    // the demo); filtering, ordering and clips re-run on it without a call.
    let body = $state(/** @type {any} */ (null));
    let error = $state('');
    let fetchKey = $derived(JSON.stringify([query.source, query.params || {}]));
    $effect(() => {
        const handle = $demo;
        const key = fetchKey;
        if (!handle) return;
        let live = true;
        const timer = setTimeout(() => {
            const [source, params] = JSON.parse(key);
            viewCall(handle.view.bind(handle), source, params)
                .then((b) => { if (live) { body = b; error = ''; } })
                .catch((err) => { if (live) { body = null; error = err?.message || String(err); } });
        }, 250);
        return () => { live = false; clearTimeout(timer); };
    });

    let run = $derived.by(() => {
        $teams;
        const ref = $demo ? currentRef() : null;
        if (!body || !ref || !$demo) return null;
        try {
            return stages(query, body, $demo.result, ref);
        } catch {
            return null;
        }
    });
    let rows = $derived(run?.rows || []);
    let clips = $derived(run?.clips || []);
    let fields = $derived(fieldPaths(rows));

    // The fields whose values name a player in this demo: what a clip can
    // watch.
    let playerFields = $derived.by(() => {
        const names = new Set(Object.keys($demo?.result?.timelineAnalysis?.playerUserIDs || {}));
        const sample = rows.slice(0, 200);
        return fields.filter((f) => sample.some((r) => names.has(String(fieldOf(r, f)))));
    });

    /** @param {string} f */
    const fieldTip = (f) => shortDoc(fieldDoc(doc, f)?.description);

    /** @param {string} name */
    const paramDoc = (name) => doc?.params[name];

    /** @param {number} n @param {string} what */
    const count = (n, what) => `${n} ${what}${n === 1 ? '' : 's'}`;

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
        toast(`Added ${count(clips.length, 'clip')}${title ? `: ${title}` : ''}`);
    }

    /** @type {HTMLInputElement | undefined} */
    let nameInput = $state();

    function save() {
        const name = title.trim();
        if (!name) {
            toast('Name the query first');
            nameInput?.focus();
            return;
        }
        title = name;
        presetId = savePreset(name, query);
        toast(`Saved "${name}" — Load and the playlist's Generate list it under My presets`);
    }

    function remove() {
        const p = $userPresets.find((x) => x.id === presetId);
        if (!p || !window.confirm(`Delete your preset "${p.title}"?`)) return;
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

    function addClipSpec() {
        draft.clips.push({ from: '', before: '', to: '', after: '', pov: '', label: '' });
    }

    /** @param {KeyboardEvent} e */
    function infoKey(e) {
        if (e.key === 'Escape') info = false;
    }
</script>

<svelte:window onkeydown={info ? infoKey : undefined} />

<div class="clip-finder">
    <div class="cf-row cf-top">
        <input class="cf-name" type="text" placeholder="Untitled query — name it to save it" bind:value={title} bind:this={nameInput}
            title="The query's name: what Save stores it as, and the toast when its clips are added">
        <select class="pt-select" title="Load a preset into the finder" onchange={pickPreset}>
            <option value="">Load…</option>
            <optgroup label="Presets">
                {#each PRESETS as p (p.id)}
                    <option value={p.id} title={p.about}>{p.title}</option>
                {/each}
            </optgroup>
            {#if $userPresets.length}
                <optgroup label="My presets">
                    {#each $userPresets as p (p.id)}
                        <option value={p.id}>{p.title}</option>
                    {/each}
                </optgroup>
            {/if}
        </select>
        <button class="pt-btn" title="Save under this name in My presets (same name: replaced)" onclick={save}><Save size={14} /> Save</button>
        {#if presetId && $userPresets.some((p) => p.id === presetId)}
            <button class="pt-btn icon" title="Delete this preset from My presets" onclick={remove}><Trash2 size={14} /></button>
        {/if}
        <button class="pt-btn" title="Copy a link to this page carrying the query" onclick={share}><Share2 size={14} /> Link</button>
        <span class="pt-group" title="Edit the query as a form, or all of it as code">
            <button class="pt-btn" class:active={!code} onclick={() => setCode(false)}>Builder</button>
            <button class="pt-btn" class:active={code} onclick={() => setCode(true)}>Code</button>
        </span>
        <button class="pt-btn cf-add" disabled={!clips.length} title="Append every clip below to the playlist" onclick={addAll}>
            <ListPlus size={14} /> Add {count(clips.length, 'clip')} to playlist
        </button>
    </div>
    {#if about && JSON.stringify(query) === aboutKey}<div class="cf-about">{about}</div>{/if}

    <datalist id="cf-fields">
        {#each fields as f (f)}<option value={f} label={fieldTip(f)}></option>{/each}
    </datalist>

    {#if code}
        <textarea class="cf-text cf-code" rows="8" spellcheck="false" value={codeText} oninput={editCode}></textarea>
        {#if codeError}<span class="cf-error">{codeError}</span>{/if}
        <div class="cf-help">
            One line per stage: <code>name "…"</code>, <code>source endpoint param=value …</code>,
            <code>keep</code> a filter, <code>order field desc first 5</code>, a <code>clip from=field-3s to=field+2s watch=field label="…"</code>
            line per clip, <code>label "…"</code> for clips without their own. Indent to continue a line; <code>#</code> comments.
        </div>
        <div class="cf-flow-line">
            {#if run}{count(rows.length, 'row')} found → {count(run.kept.length, 'row')} kept → {count(run.ranked.length, 'row')} ordered → {count(clips.length, 'clip')}{/if}
        </div>
    {:else}
        <!-- 1 · Find rows -->
        <section class="cf-stage">
            <div class="cf-stage-head">
                <span class="cf-step">1</span><span class="cf-heading">Find rows</span>
                <select class="pt-select" value={draft.source} onchange={setSource} title={doc?.summary || ''}>
                    {#each Object.keys(SOURCES) as s (s)}
                        <option value={s} title={$apiDocs?.[s]?.summary || ''}>/{s}{$apiDocs?.[s] ? ` — ${$apiDocs[s].summary}` : ''}</option>
                    {/each}
                </select>
                <button class="pt-btn icon" title="What this source finds, its params and its row fields" onclick={() => (info = true)}><Info size={14} /></button>
            </div>
            {#if src.params.length}
                <div class="cf-params">
                    {#each src.params as name (name)}
                        {@const p = paramDoc(name)}
                        <label class="cf-label" title={shortDoc(p?.description, draft.source)}>{name}
                            {#if p?.enum?.length && p.type !== 'array'}
                                <select class="pt-select" bind:value={draft.params[name]}>
                                    <option value="">{p.default != null ? `default (${p.default})` : 'default'}</option>
                                    {#each p.enum as v (v)}<option value={v}>{v}</option>{/each}
                                </select>
                            {:else}
                                <input class="cf-param" type="text" list={p?.enum?.length ? `cf-enum-${name}` : undefined}
                                    placeholder={p?.default != null ? String(p.default) : p?.type === 'array' ? 'a, b' : ''} bind:value={draft.params[name]}>
                                {#if p?.enum?.length}
                                    <datalist id="cf-enum-{name}">{#each p.enum as v (v)}<option value={v}></option>{/each}</datalist>
                                {/if}
                            {/if}
                        </label>
                    {/each}
                </div>
            {/if}
        </section>
        <div class="cf-flow"><ArrowDown size={13} />
            {#if !$demo}load a demo{:else if error}<span class="cf-error">{error}</span>{:else if run}{count(rows.length, 'row')}{:else}…{/if}
        </div>

        <!-- 2 · Keep rows where -->
        <section class="cf-stage">
            <div class="cf-stage-head">
                <span class="cf-step">2</span><span class="cf-heading">Keep rows where</span>
                <span class="muted cf-note">nothing set: keep every row</span>
            </div>
            <ExprGroup bind:group={draft.where} fields="cf-fields" />
        </section>
        <div class="cf-flow"><ArrowDown size={13} /> {#if run}{count(run.kept.length, 'row')} kept{/if}</div>

        <!-- 3 · Order and keep the first N -->
        <section class="cf-stage">
            <div class="cf-stage-head">
                <span class="cf-step">3</span><span class="cf-heading">Order</span>
                <span class="muted cf-note">nothing set: time order, every row</span>
            </div>
            <div class="cf-row">
                <label class="cf-label" title="Sort the rows by this field — e.g. detail.frags for the best quad runs">by
                    <input class="cf-field" type="text" list="cf-fields" placeholder="time" bind:value={draft.rank.by}
                        title={draft.rank.by ? fieldTip(draft.rank.by) : ''}>
                </label>
                <select class="pt-select" disabled={!draft.rank.by.trim()} bind:value={draft.rank.desc}>
                    <option value={true}>highest first</option>
                    <option value={false}>lowest first</option>
                </select>
                <label class="cf-label" title="Keep only the first N rows of that order (empty: all of them)">keep the first
                    <input class="cf-num" type="text" inputmode="numeric" placeholder="all" bind:value={draft.rank.limit}>
                </label>
            </div>
        </section>
        <div class="cf-flow"><ArrowDown size={13} /> {#if run}{count(run.ranked.length, 'row')}{/if}</div>

        <!-- 4 · Clips per row -->
        <section class="cf-stage">
            <div class="cf-stage-head">
                <span class="cf-step">4</span><span class="cf-heading">Clips from each row</span>
            </div>
            {#each draft.clips as c, i (c)}
                <div class="cf-clip">
                    {#if draft.clips.length > 1}<span class="cf-clip-n">clip {i + 1}</span>{/if}
                    <div class="cf-row">
                        <label class="cf-label" title="The clip starts at this row field (a time), minus the seconds after it">from
                            <input class="cf-field" type="text" list="cf-fields" placeholder={src.clip.from} bind:value={c.from} title={fieldTip(c.from || src.clip.from)}>
                        </label>
                        <label class="cf-label" title="Seconds before that time">−<input class="cf-num" type="text" placeholder={String(src.clip.before / 1000)} bind:value={c.before}>s</label>
                        <label class="cf-label" title="The clip ends at this row field (a time), plus the seconds after it">to
                            <input class="cf-field" type="text" list="cf-fields" placeholder={src.clip.to} bind:value={c.to} title={fieldTip(c.to || src.clip.to)}>
                        </label>
                        <label class="cf-label" title="Seconds after that time">+<input class="cf-num" type="text" placeholder={String(src.clip.after / 1000)} bind:value={c.after}>s</label>
                    </div>
                    <div class="cf-row">
                        <label class="cf-label" title="Which player the camera follows: a row field holding a player's name">watch
                            <select class="pt-select" bind:value={c.pov}>
                                <option value="">{src.clip.pov} (default)</option>
                                {#each playerFields.filter((f) => f !== src.clip.pov) as f (f)}
                                    <option value={f} title={fieldTip(f)}>{f}</option>
                                {/each}
                                {#if c.pov && !playerFields.includes(c.pov) && c.pov !== src.clip.pov}<option value={c.pov}>{c.pov}</option>{/if}
                            </select>
                        </label>
                        <label class="cf-label cf-grow" title={LABEL_HELP}>label
                            <input class="cf-labeltpl" type="text" placeholder={draft.label ? 'the default label below' : 'e.g. {player} · {detail.frags} frags'} bind:value={c.label}>
                        </label>
                        {#if draft.clips.length > 1}
                            <button class="pt-btn icon" title="Remove this clip" onclick={() => draft.clips.splice(i, 1)}><X size={13} /></button>
                        {/if}
                    </div>
                </div>
            {/each}
            {#if draft.clips.length < 4}
                <div class="cf-row">
                    <button class="pt-btn" title="Cut another clip from each row — e.g. a pack's pickup after its drop" onclick={addClipSpec}><Plus size={13} /> clip</button>
                </div>
            {/if}
            <label class="cf-label cf-grow" title={LABEL_HELP}>default label
                <input class="cf-labeltpl" type="text" placeholder="for clips without their own, e.g. {'{'}player{'}'} · {'{'}detail.frags{'}'} frag{'{'}detail.frags|s{'}'}" bind:value={draft.label}>
            </label>
            <div class="cf-help">{LABEL_HELP}</div>
        </section>
        <div class="cf-flow"><ArrowDown size={13} /> {#if run}{count(clips.length, 'clip')}{/if}</div>
    {/if}

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

    {#if info}
        <div class="cf-info" role="dialog" aria-label="/{draft.source}">
            <div class="cf-row">
                <strong>/{draft.source}</strong><span class="muted">{doc?.summary || ''}</span>
                <button class="pt-btn icon cf-close" title="Close (Esc)" onclick={() => (info = false)}><X size={14} /></button>
            </div>
            {#if doc}
                <div class="cf-md">{@html miniMarkdown(doc.description)}</div>
                <h4>Params</h4>
                <dl>
                    {#each src.params as name (name)}
                        {@const p = doc.params[name]}
                        <dt><code>{name}</code>{#if p?.enum?.length} <span class="muted">{p.enum.join(' · ')}</span>{/if}{#if p?.default != null} <span class="muted">default {p.default}</span>{/if}</dt>
                        <dd class="cf-md">{@html miniMarkdown(p?.description || '')}</dd>
                    {/each}
                </dl>
                <h4>Row fields</h4>
                <dl>
                    {#each Object.keys(doc.fields).sort() as f (f)}
                        {@const d = doc.fields[f]}
                        <dt><code>{f}</code> <span class="muted">{d.type || ''}{d.enum?.length ? ` · ${d.enum.join(' · ')}` : ''}</span></dt>
                        <dd class="cf-md">{@html miniMarkdown(d.description || '')}</dd>
                    {/each}
                </dl>
                <h4>Labels</h4>
                <p>{LABEL_HELP}</p>
            {:else}
                <p class="muted">Loading the API docs…</p>
            {/if}
        </div>
    {/if}
</div>

<style>
    .clip-finder {
        position: relative;
        display: flex;
        flex-direction: column;
        gap: 6px;
    }
    .cf-row {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
    }
    .cf-name {
        flex: 1 1 200px;
        font-size: 14px !important;
        font-weight: 600;
    }
    .cf-add {
        margin-left: auto;
    }
    .cf-about {
        font-size: 12px;
        color: #aaa;
    }
    .cf-stage {
        display: flex;
        flex-direction: column;
        gap: 6px;
        border: 1px solid var(--bg-panel);
        border-radius: 4px;
        padding: 6px 8px;
    }
    .cf-stage-head {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
    }
    .cf-step {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 18px;
        height: 18px;
        border-radius: 50%;
        background: var(--bg-panel);
        font-size: 11px;
        font-weight: 600;
    }
    .cf-heading {
        font-size: 12px;
        font-weight: 600;
        color: #ccc;
        text-transform: uppercase;
        letter-spacing: 0.04em;
    }
    .cf-note {
        font-size: 11px;
    }
    .cf-flow {
        display: flex;
        align-items: center;
        gap: 4px;
        margin-left: 14px;
        padding-left: 8px;
        border-left: 2px solid var(--bg-panel);
        font-size: 12px;
        color: #aaa;
        min-height: 18px;
    }
    .cf-flow-line {
        font-size: 12px;
        color: #aaa;
    }
    .cf-params {
        display: flex;
        flex-wrap: wrap;
        gap: 6px 12px;
    }
    .cf-label {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        color: #bbb;
    }
    .cf-grow {
        flex: 1 1 220px;
    }
    .cf-clip {
        display: flex;
        flex-direction: column;
        gap: 4px;
    }
    .cf-clip + .cf-clip {
        border-top: 1px dashed var(--bg-panel);
        padding-top: 6px;
    }
    .cf-clip-n {
        font-size: 11px;
        color: #888;
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
        flex: 1 1 auto;
        min-width: 140px;
    }
    .cf-help {
        font-size: 11px;
        color: #888;
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
    .cf-text {
        width: 100%;
        box-sizing: border-box;
        background: var(--bg-deep);
        color: var(--fg-default);
        border: 1px solid rgba(255, 255, 255, 0.12);
        border-radius: 3px;
        font-family: monospace;
        font-size: 13px;
        padding: 4px 6px;
        resize: vertical;
    }
    .cf-error {
        color: #ff8080;
        font-size: 12px;
    }
    .cf-results {
        max-height: 260px;
        overflow-y: auto;
    }
    .cf-info {
        position: absolute;
        inset: 0 0 auto 0;
        max-height: 70vh;
        overflow-y: auto;
        z-index: 20;
        background: var(--bg-deep);
        border: 1px solid var(--accent-cyan);
        border-radius: 4px;
        padding: 8px 10px;
        font-size: 12px;
        box-shadow: 0 6px 24px rgba(0, 0, 0, 0.6);
    }
    .cf-close {
        margin-left: auto;
    }
    .cf-info h4 {
        margin: 10px 0 4px;
        font-size: 12px;
        text-transform: uppercase;
        color: #aaa;
    }
    .cf-info dl {
        margin: 0;
    }
    .cf-info dt {
        margin-top: 6px;
    }
    .cf-info dd {
        margin: 2px 0 0 12px;
        color: #bbb;
    }
    .cf-md :global(p),
    .cf-md :global(ul) {
        margin: 4px 0;
    }
    .cf-md :global(code),
    .cf-info code,
    .cf-help code {
        font-family: monospace;
        color: var(--accent-cyan);
    }
</style>
