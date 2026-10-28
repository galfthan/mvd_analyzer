<script>
    // Every generated clip of the demo (quad runs, streaks, the highlights)
    // in time order: the ones the playhead is inside are lit, and the list
    // scrolls to keep the next one in view. Click plays the clip;
    // shift-click adds it to the playlist.
    import { demo, teams, colorOf } from '../../state/index.js';
    import { formatDuration } from '../../lib/result.js';
    import { GENERATORS } from '../../lib/playlist.js';
    import { runner, currentRef, engine } from '../index.js';
    import { addClips } from '../playlist-store.js';
    import { toast } from '../toast.js';
    import { onFrame } from './tick.js';

    const KINDS = ['quadRuns', 'fragStreaks', 'quadbores', 'airgibs', 'telefrags', 'discharges'];

    let clips = $derived.by(() => {
        $teams;
        const ref = $demo ? currentRef() : null;
        if (!ref) return [];
        const out = [];
        // ref is only non-null with a demo.
        const result = /** @type {import('../../state/demo.js').DemoHandle} */ ($demo).result;
        for (const id of KINDS) out.push(...GENERATORS[id].build(result, ref));
        return out.sort((a, b) => a.from - b.from);
    });

    let demoT = $state(0);
    let list = $state(/** @type {HTMLOListElement | undefined} */ (undefined));
    $effect(() => onFrame((t) => { demoT = t + engine.offset(); }));

    let next = $derived.by(() => {
        const i = clips.findIndex((c) => c.from > demoT);
        return i < 0 ? clips.length : i;
    });

    // Keep the next moment in view, but only scroll when it changes.
    $effect(() => {
        const anchor = /** @type {HTMLElement | undefined} */ (list?.children[Math.max(0, next - 1)]);
        // An anchor is a child of the list, so the list is there.
        const ol = /** @type {HTMLOListElement} */ (list);
        if (anchor) ol.scrollTop = Math.max(0, anchor.offsetTop - ol.offsetTop - 40);
    });

    /**
     * @param {MouseEvent | KeyboardEvent} e
     * @param {(typeof clips)[number]} c
     */
    function click(e, c) {
        if (e.shiftKey) {
            addClips([{ ...c, demo: { ...c.demo } }]);
            toast(`Added to playlist: ${c.label}`);
        } else {
            runner.start([c], 0, true);
        }
    }
</script>

{#if clips.length}
    <ol class="pp-feed pp-moments" bind:this={list}>
        {#each clips as c (c)}
            <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
            <li class:recent={demoT >= c.from && demoT < c.to} title="Click: play · Shift-click: add to playlist"
                role="button" tabindex="0" onclick={(e) => click(e, c)}
                onkeydown={(e) => e.key === 'Enter' && click(e, c)}>
                <span class="t">{formatDuration(Math.max(0, c.from - c.demo.offset))}</span>
                <span style:color={c.who ? colorOf($teams, c.who, $teams.perPlayer) : '#ccc'}>{c.label}</span>
            </li>
        {/each}
    </ol>
{:else}
    <div class="player-clip-hint">No quad runs, streaks or highlights in this demo.</div>
{/if}

<style>
    .pp-moments {
        max-height: 260px;
        overflow-y: auto;
    }
    :global(.player-pane-body.sized) .pp-moments {
        max-height: none;
    }
</style>
