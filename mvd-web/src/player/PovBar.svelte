<script>
    // "Auto" (FTE's stats-based autotrack) plus one button per player,
    // grouped and coloured by the canonical team order. "Auto" lights while
    // autotrack is on; the followed player lights either way, so autotrack
    // shows whom it picked.
    import { demo, teams, teamIndexOf, colorOf } from '../state/index.js';
    import { engine } from './index.js';

    const status = engine.status;

    let players = $derived.by(() => {
        const ids = $demo?.result?.timelineAnalysis?.playerUserIDs || {};
        const t = $teams;
        const idx = (n) => teamIndexOf(t, n, t.perPlayer);
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
        <button class="player-pov-btn player-sound-btn" onclick={() => engine.toggleMute()}>
            {$status.ready && !$status.muted ? '🔊 Sound on' : '🔇 Sound off'}
        </button>
    {/if}
</div>
