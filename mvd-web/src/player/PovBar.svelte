<script>
    // "Auto" (FTE's stats-based autotrack) plus one button per player,
    // grouped and coloured by the canonical team order. "Auto" lights while
    // autotrack is on; the followed player lights either way, so autotrack
    // shows whom it picked. At the end: sound, and hiding / showing the panes beside the player.
    import Volume2 from '@lucide/svelte/icons/volume-2';
    import VolumeX from '@lucide/svelte/icons/volume-x';
    import PanelRightOpen from '@lucide/svelte/icons/panel-right-open';
    import PanelRightClose from '@lucide/svelte/icons/panel-right-close';
    import { demo, teams, teamIndexOf, colorOf } from '../state/index.js';
    import { engine } from './index.js';
    import { paneLayout, updateLayout } from './layout.js';

    const status = engine.status;

    let players = $derived.by(() => {
        const ids = $demo?.result?.timelineAnalysis?.playerUserIDs || {};
        const t = $teams;
        const idx = (/** @type {string} */ n) => teamIndexOf(t, n, t.perPlayer);
        return Object.keys(ids)
            .sort((a, b) => (idx(a) - idx(b)) || a.localeCompare(b))
            .map((name) => ({ name, userid: ids[name], color: colorOf(t, name, t.perPlayer) }));
    });
</script>

<div class="player-pov-bar">
    {#if $demo}
        <button class="player-pov-btn" class:active={$status.autotrack} onclick={() => engine.track('auto')}>Auto</button>
        {#each players as p (p.name)}
            <button class="player-pov-btn" class:active={$status.track === p.userid}
                style:--pov-color={p.color} onclick={() => engine.track(p.userid)}>{p.name}</button>
        {/each}
        <span class="pov-end">
            <button class="pt-btn icon" title={$status.ready && !$status.muted ? 'Sound on — click to mute' : 'Sound off — click to unmute'}
                onclick={() => engine.toggleMute()}>
                {#if $status.ready && !$status.muted}<Volume2 size={16} />{:else}<VolumeX size={16} />{/if}
            </button>
            <button class="pt-btn icon" title={$paneLayout.hidden ? 'Show the panes beside the player' : 'Hide the panes beside the player'}
                onclick={() => updateLayout((l) => { l.hidden = !l.hidden; })}>
                {#if $paneLayout.hidden}<PanelRightOpen size={16} />{:else}<PanelRightClose size={16} />{/if}
            </button>
        </span>
    {/if}
</div>
