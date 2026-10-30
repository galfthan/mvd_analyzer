<script>
    // The demo's moments in two levels: the groups — the Key Moments and
    // Pack Drops tabs' tables, each a clip finder preset (powerup runs,
    // streaks, top damage windows and kills, demo markers, the highlights,
    // pack drops) — with how many rows each found, and under an opened group
    // its clips in time order. The ones the playhead is inside are lit (a
    // dot on a closed group's head). Click plays a clip, shift-click adds
    // it; a group's + adds all of them. Which groups are open is the pane's
    // option. The runs are preset-runs.js's, shared with Generate.
    import ChevronRight from '@lucide/svelte/icons/chevron-right';
    import ChevronDown from '@lucide/svelte/icons/chevron-down';
    import ListPlus from '@lucide/svelte/icons/list-plus';
    import { demo, teams, colorOf } from '../../state/index.js';
    import { presetRuns } from '../preset-runs.js';
    import { formatDuration } from '../../lib/result.js';
    import { PRESETS, MOMENT_GROUPS } from '../../lib/clip-query.js';
    import { runner, engine } from '../index.js';
    import { addClips } from '../playlist-store.js';
    import { paneLayout, paneOpt, setPaneOpt } from '../layout.js';
    import { toast } from '../toast.js';
    import { onFrame } from './tick.js';

    /** @typedef {import('../../lib/playlist.js').Clip} Clip */

    // Every id names a preset (test/clip-query.test.js).
    const GROUPS = MOMENT_GROUPS.map((id) => /** @type {(typeof PRESETS)[number]} */ (PRESETS.find((p) => p.id === id)));

    // Per group: its rows' count and clips (null while it runs).
    let found = $derived($presetRuns);

    let open = $derived(new Set(paneOpt($paneLayout, 'moments', 'open', 'quad-runs').split(',').filter(Boolean)));

    /** @param {string} id */
    function toggle(id) {
        const next = new Set(open);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        setPaneOpt('moments', 'open', [...next].join(','));
    }

    let demoT = $state(0);
    $effect(() => onFrame((t) => { demoT = t + engine.offset(); }));

    /** @param {Clip} c */
    const isNow = (c) => demoT >= c.from && demoT < c.to;

    /**
     * @param {MouseEvent | KeyboardEvent} e
     * @param {Clip} c
     */
    function click(e, c) {
        if (e.shiftKey) {
            addClips([{ ...c, demo: { ...c.demo } }]);
            toast(`Added to playlist: ${c.label}`);
        } else {
            runner.start([c], 0, true);
        }
    }

    /** @param {(typeof GROUPS)[number]} g */
    function addGroup(g) {
        const clips = found[g.id]?.clips || [];
        if (!clips.length) return;
        addClips(clips.map((c) => ({ ...c, demo: { ...c.demo } })));
        toast(`Added ${clips.length} clip${clips.length === 1 ? '' : 's'}: ${g.title}`);
    }
</script>

{#if $demo}
    <ul class="mg">
        {#each GROUPS as g (g.id)}
            {@const f = found[g.id]}
            {@const n = f?.rows ?? null}
            {@const isOpen = open.has(g.id) && !!n}
            <li class="mg-group" class:empty={n === 0}>
                <div class="mg-head">
                    <button class="mg-toggle" disabled={!n} title={g.about} onclick={() => toggle(g.id)}>
                        {#if isOpen}<ChevronDown size={13} />{:else}<ChevronRight size={13} />{/if}
                        <span class="mg-title">{g.title}</span>
                        {#if !isOpen && f?.clips.some(isNow)}<span class="mg-now" title="The playhead is in one of these"></span>{/if}
                    </button>
                    {#if f?.error}
                        <span class="mg-count error" title="Could not run: {f.error}">!</span>
                    {:else}
                        <span class="mg-count">{n ?? '…'}</span>
                    {/if}
                    <button class="mg-add" disabled={!f?.clips.length} title="Add all {f?.clips.length ?? ''} clips to the playlist" onclick={() => addGroup(g)}><ListPlus size={13} /></button>
                </div>
                {#if isOpen && f}
                    <ol class="pp-feed mg-clips">
                        {#each f.clips as c (c)}
                            <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
                            <li class:recent={isNow(c)} title="Click: play · Shift-click: add to playlist"
                                role="button" tabindex="0" onclick={(e) => click(e, c)}
                                onkeydown={(e) => e.key === 'Enter' && click(e, c)}>
                                <span class="t">{formatDuration(Math.max(0, c.from - c.demo.offset))}</span>
                                <span style:color={c.who ? colorOf($teams, c.who, $teams.perPlayer) : '#ccc'}>{c.label}</span>
                            </li>
                        {/each}
                    </ol>
                {/if}
            </li>
        {/each}
    </ul>
{:else}
    <div class="player-clip-hint">Load a demo to see its moments.</div>
{/if}

<style>
    .mg {
        list-style: none;
        margin: 0;
        padding: 0;
        font-size: 12px;
    }
    .mg-head {
        display: flex;
        align-items: center;
        gap: 6px;
        padding: 1px 2px;
        border-bottom: 1px solid rgba(255, 255, 255, 0.05);
    }
    .mg-toggle {
        flex: 1;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        min-width: 0;
        padding: 2px 0;
        background: none;
        border: none;
        color: #cdd;
        font: inherit;
        text-align: left;
        cursor: pointer;
    }
    .mg-toggle:hover:not(:disabled) .mg-title {
        color: var(--accent-cyan);
    }
    .mg-toggle:disabled {
        cursor: default;
    }
    .mg-title {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .mg-now {
        width: 6px;
        height: 6px;
        border-radius: 50%;
        background: var(--accent-cyan);
        flex: none;
    }
    .mg-count {
        min-width: 2.5em;
        padding: 0 7px;
        border-radius: 9px;
        background: rgba(0, 217, 255, 0.12);
        color: var(--accent-cyan);
        font-family: monospace;
        text-align: center;
    }
    .mg-count.error {
        background: rgba(255, 107, 107, 0.15);
        color: #ff8080;
    }
    .mg-add {
        display: inline-flex;
        padding: 2px;
        background: none;
        border: none;
        color: #889;
        cursor: pointer;
    }
    .mg-add:hover:not(:disabled) {
        color: var(--accent-cyan);
    }
    .mg-add:disabled {
        opacity: 0.25;
        cursor: default;
    }
    .mg-group.empty .mg-toggle,
    .mg-group.empty .mg-count {
        opacity: 0.4;
    }
    .mg-clips {
        max-height: 220px;
        overflow-y: auto;
        margin: 2px 0 6px 16px;
    }
    :global(.player-pane-body.sized) .mg-clips {
        max-height: none;
    }
</style>
