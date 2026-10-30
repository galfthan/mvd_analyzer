<script>
    // The clip finder's field browser: what a source's rows hold, read off
    // the loaded demo's rows (fieldStats) beside the API spec's text for
    // each field (api-docs.js) — the full path to type, its type and unit,
    // how many rows carry it, the values allowed (the spec's enum) and the
    // values this demo has with their counts, or a number's range — and a
    // real row as JSON. A source whose rows come in kinds (events by
    // `type`, highlights by `kind`) narrows to one kind, which is how the
    // fields of `detail` that belong to a powerup show apart from a frag's.
    // Clicking a value adds `field = value` to the filter (onpick); clicking
    // a path does what the stage the panel was opened from does with a
    // field (`path`: order by it, put it in the label), else copies it.
    import ChevronLeft from '@lucide/svelte/icons/chevron-left';
    import ChevronRight from '@lucide/svelte/icons/chevron-right';
    import { fieldStats, fieldOf } from '../lib/clip-query.js';
    import { formatDuration } from '../lib/result.js';
    import { fieldDoc, fieldEnum, shortDoc } from './api-docs.js';
    import { toast } from './toast.js';

    /**
     * @type {{
     *   doc: import('./api-docs.js').SourceDoc | undefined, rows: any[],
     *   onpick: (field: string, op: '=' | '~', value: import('../lib/clip-query.js').Scalar) => void,
     *   path?: { what: string, fn: (field: string) => void },
     * }}
     */
    let { doc, rows, onpick, path } = $props();

    // The field the rows come in kinds by, if any.
    let kindField = $derived(['type', 'kind'].find((f) => rows.some((r) => typeof fieldOf(r, f) === 'string')) || '');
    let kind = $state('');
    let kinds = $derived(kindField ? fieldStats(rows, 40).get(kindField)?.values || [] : []);
    let shown = $derived(kind ? rows.filter((r) => fieldOf(r, kindField) === kind) : rows);
    let stats = $derived(fieldStats(shown));

    let search = $state('');
    /** @param {string} p */
    const found = (p) => {
        const q = search.trim().toLowerCase();
        return !q || p.toLowerCase().includes(q);
    };
    let paths = $derived([...stats.keys()].filter(found));
    // The documented fields these rows do not carry (another kind of row's,
    // or one this demo never has) — listed on request, greyed.
    let more = $state(false);
    let absent = $derived(Object.keys(doc?.fields || {}).filter((f) => !stats.has(f) && found(f)).sort());

    let at = $state(0);
    $effect(() => {
        shown;
        at = 0;
    });
    let example = $derived(shown[Math.min(at, shown.length - 1)]);

    /** @param {string} path */
    function unit(path) {
        const d = fieldDoc(doc, path)?.description || '';
        return /\bms\b|milliseconds/i.test(d) || /(?:^|\.)(?:time|start|end)$|Time$|Ms$/.test(path) ? 'ms' : '';
    }

    /** @param {string} path */
    async function copy(path) {
        try {
            await navigator.clipboard.writeText(path);
            toast(`Copied ${path}`);
        } catch {
            window.prompt('The field path:', path);
        }
    }

    /**
     * @param {number} v
     * @param {string} u
     */
    const num = (v, u) => (u === 'ms' && v >= 1000 ? `${v} (${formatDuration(v / 1000)})` : String(v));
</script>

<div class="fb">
    <div class="fb-head">
        {#if kinds.length}
            <span class="fb-kinds">
                <button class="fb-chip" class:on={!kind} onclick={() => (kind = '')}>all {rows.length}</button>
                {#each kinds as [k, n] (k)}
                    <button class="fb-chip" class:on={kind === k} title="Only the rows whose {kindField} is {k}, and the fields they carry"
                        onclick={() => (kind = kind === k ? '' : String(k))}>{k} {n}</button>
                {/each}
            </span>
        {/if}
        <input class="fb-search" type="text" placeholder="find a field" bind:value={search}>
    </div>
    <p class="muted fb-hint">
        {shown.length ? `What ${kind ? `the ${shown.length} ${kind} rows` : `these ${shown.length} rows`} hold.` : 'No rows to read yet — the spec\'s fields only.'}
        Type the <strong>full path</strong> in a field box (<code>detail.powerup</code>, <code>victims.0.name</code> — the first victim's name).
        Click a value to keep the rows with it; click a path to {path ? path.what : 'copy it'}. Times are match ms (60000 = 1:00).
    </p>
    <table class="fb-table">
        <tbody>
            {#each more ? [...paths, ...absent] : paths as p (p)}
                {@const st = stats.get(p)}
                {@const d = fieldDoc(doc, p)}
                {@const allowed = fieldEnum(doc, p)}
                {@const u = unit(p)}
                <tr class:absent={!st}>
                    <td class="fb-path">
                        <button class="fb-pathbtn" class:act={!!path} title={path ? `${p}: ${path.what}` : `Copy ${p}`} onclick={() => (path ? path.fn(p) : copy(p))}>{p}</button>
                        <div class="fb-type">{st?.type || d?.type || ''}{st?.list ? ' list' : ''}{u ? ` · ${u}` : ''}</div>
                    </td>
                    <td>
                        {#if d?.description}<div class="fb-desc">{shortDoc(d.description).replace(/ \(ⓘ for more\)$/, '')}</div>{/if}
                        <div class="fb-vals">
                            {#if allowed.length}
                                <!-- Every allowed value, with how many of these rows have it (none: greyed). -->
                                <span class="muted">allowed:</span>
                                {#each allowed as v (v)}
                                    {@const n = st?.values.find(([x]) => String(x) === v)?.[1] || 0}
                                    <button class="fb-chip enum" class:none={!n} title="Keep rows where {p} {st?.list ? '~' : '='} {v} ({n} of {shown.length} rows)"
                                        onclick={() => onpick(p, st?.list ? '~' : '=', v)}>{v} <span class="fb-n">{n}</span></button>
                                {/each}
                            {/if}
                            {#if st}
                                {@const extra = st.values.filter(([v]) => !allowed.includes(String(v)))}
                                {#if st.min != null && st.type === 'number' && st.distinct > 12}
                                    <span class="muted">{num(st.min, u)} … {num(/** @type {number} */ (st.max), u)}</span>
                                {:else}
                                    {#each extra as [v, n] (v)}
                                        <button class="fb-chip" title="Keep rows where {p} {st.list ? '~' : '='} {v} ({n} of {shown.length} rows{st.list ? ' have it' : ''})" onclick={() => onpick(p, st.list ? '~' : '=', v)}>{v} <span class="fb-n">{n}</span></button>
                                    {/each}
                                    {#if st.distinct > st.values.length}<span class="muted">+{st.distinct - st.values.length} more</span>{/if}
                                {/if}
                                <span class="muted fb-cov">in {st.n} of {shown.length}</span>
                            {:else}
                                <span class="muted">not in these rows</span>
                            {/if}
                        </div>
                    </td>
                </tr>
            {/each}
        </tbody>
    </table>
    {#if absent.length}
        <button class="fb-more" onclick={() => (more = !more)}>
            {more ? 'Hide' : 'Show'} {absent.length} more field{absent.length === 1 ? '' : 's'} the API documents for {kind ? 'other kinds of rows' : 'rows these params do not return'}
        </button>
    {/if}
    {#if example}
        <div class="fb-example-head">
            <span class="cf-heading">A row</span>
            <button class="pt-btn icon" disabled={at === 0} title="The previous row" onclick={() => at--}><ChevronLeft size={13} /></button>
            <span class="muted">{at + 1} / {shown.length}</span>
            <button class="pt-btn icon" disabled={at >= shown.length - 1} title="The next row" onclick={() => at++}><ChevronRight size={13} /></button>
        </div>
        <pre class="fb-json">{JSON.stringify(example, null, 2)}</pre>
    {/if}
</div>

<style>
    .fb {
        display: flex;
        flex-direction: column;
        gap: 6px;
    }
    .fb-head {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
    }
    .fb-kinds {
        display: flex;
        flex-wrap: wrap;
        gap: 4px;
    }
    .fb-search {
        margin-left: auto;
        width: 150px;
    }
    .fb-hint {
        margin: 0;
        font-size: 11px;
    }
    .fb-table {
        border-collapse: collapse;
        width: 100%;
    }
    .fb-table td {
        padding: 4px 6px 4px 0;
        vertical-align: top;
        border-top: 1px solid rgba(255, 255, 255, 0.05);
    }
    .fb-table tr.absent {
        opacity: 0.5;
    }
    .fb-path {
        width: 1%;
        white-space: nowrap;
    }
    .fb-pathbtn {
        padding: 0;
        background: none;
        border: none;
        font-family: monospace;
        font-size: 12px;
        color: var(--accent-cyan);
        cursor: copy;
    }
    .fb-pathbtn.act {
        cursor: pointer;
    }
    .fb-type {
        font-size: 10px;
        color: #888;
    }
    .fb-desc {
        color: #bbb;
        margin-bottom: 2px;
    }
    .fb-vals {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 3px;
    }
    .fb-chip {
        padding: 0 6px;
        border-radius: 9px;
        border: 1px solid rgba(255, 255, 255, 0.12);
        background: rgba(255, 255, 255, 0.04);
        color: #ddd;
        font: inherit;
        font-size: 11px;
        cursor: pointer;
    }
    .fb-chip:hover {
        border-color: var(--accent-cyan);
    }
    .fb-chip.on {
        background: rgba(0, 217, 255, 0.15);
        border-color: var(--accent-cyan);
        color: var(--accent-cyan);
    }
    .fb-chip.none {
        opacity: 0.45;
    }
    .fb-n {
        color: #888;
    }
    .fb-cov {
        margin-left: 4px;
        font-size: 10px;
    }
    .fb-more {
        align-self: flex-start;
        padding: 0;
        background: none;
        border: none;
        color: var(--accent-cyan);
        font: inherit;
        font-size: 11px;
        text-decoration: underline;
        cursor: pointer;
    }
    .fb-example-head {
        display: flex;
        align-items: center;
        gap: 6px;
    }
    .fb-json {
        margin: 0;
        max-height: 260px;
        overflow: auto;
        background: rgba(0, 0, 0, 0.25);
        padding: 6px;
        border-radius: 3px;
        font-size: 11px;
    }
</style>
