<script>
    // Everyone at the playhead, the Map tab's legend for the Player tab: per
    // team (canonical order and colour) each player's loc, health / armor,
    // weapons, powerups and frags. The player you watch is lit; click a
    // name to watch someone else.
    import { demo, teams, colorOf } from '../../state/index.js';
    import { bucketIndexAtTime, reconstructBucketPlayers } from '../../../../mvd-map-view/src/frames.js';
    import { playersByTeam, rosterTeamOf, fragsAt } from '../../lib/timeline.js';
    import { view as mapView } from '../../tabs/map/map-model.js';
    import { engine } from '../index.js';
    import { onFrame } from './tick.js';

    const WEAPONS = /** @type {const} */ (['rl', 'lg', 'gl', 'sng', 'ssg']);
    const POWERUPS = /** @type {const} */ ([['q', 'Q', 'quad'], ['pe', 'P', 'pent'], ['r', 'R', 'ring']]);

    /** @type {Record<string, any> | null} the columnar bucket view */
    let view = $state.raw(null);
    $effect(() => {
        const handle = $demo;
        view = handle?.result?.timelineAnalysis?.bucketView || null;
        handle?.ready('buckets').then(() => {
            if ($demo === handle) view = handle.result.timelineAnalysis?.bucketView || null;
        }, () => {});
    });

    // Groups by canonical team; a field of individuals is one group.
    let groups = $derived.by(() => {
        const t = $teams;
        if (!view) return [];
        const grouped = playersByTeam(view, t.order, rosterTeamOf($demo?.result), t.perPlayer);
        if (t.perPlayer) return [{ team: '', color: '', names: grouped.flat() }];
        return grouped.map((names, ti) => ({ team: t.order[ti] || '', color: t.colors[ti] || '#ccc', names }));
    });

    /**
     * @typedef {{ loc: string, h: number, a: number, at: string, weapons: string[], powerups: Array<[string, string]> } | null} Row
     */
    /** @type {Record<string, Row>} */
    let rows = $state({});
    /** @type {Record<string, number>} */
    let frags = $state({});
    /** @type {string | null} */
    let pov = $state(null);

    $effect(() => {
        const v = view;
        return onFrame((t) => {
            if (!v || !v.count) return;
            const i = bucketIndexAtTime(v, t);
            const frame = /** @type {Record<string, Record<string, any>>} */ (i >= 0 ? reconstructBucketPlayers(v, i) : {});
            const locs = mapView.state.initialized;
            /** @type {typeof rows} */
            const out = {};
            for (const name of Object.keys(v.players)) {
                const d = frame[name];
                out[name] = d ? {
                    loc: locs && !(d.x === 0 && d.y === 0) ? (mapView.resolvePlayerLoc(d) || '') : '',
                    h: d.h ?? 0, a: d.a ?? 0, at: d.at || '',
                    weapons: WEAPONS.filter((w) => d[w]),
                    powerups: POWERUPS.filter(([k]) => d[k]).map(([, label, cls]) => [label, cls]),
                } : null;
            }
            rows = out;
            frags = fragsAt($demo?.result, t);
            pov = engine.povName();
        });
    });

    /** @param {string[]} names */
    const teamFrags = (names) => names.reduce((n, name) => n + (frags[name] || 0), 0);
</script>

{#each groups as g (g.team)}
    {#if g.names.length}
        {#if g.team}
            <div class="pp-team" style:color={g.color}>{g.team} <span class="muted">· {teamFrags(g.names)} frags</span></div>
        {/if}
        {#each g.names as name (name)}
            {@const r = rows[name]}
            <div class="pp-player" class:pov={name === pov} class:dead={!r}>
                <span class="name" style:color={colorOf($teams, name, $teams.perPlayer)} title="{name} — click to watch"
                    role="button" tabindex="-1" onclick={() => engine.trackName(name)} onkeydown={() => {}}>{name}</span>
                <span class="loc" title={r?.loc || ''}>{r?.loc || ''}</span>
                <span class="stack">
                    {#if r}{r.h}/<span class={r.at ? `armor-${r.at}` : ''}>{r.a}</span>{:else}dead{/if}
                </span>
                <span class="wpn">
                    {#each r?.weapons || [] as w (w)}<span class="w w-{w}">{w.toUpperCase()}</span>{/each}
                    {#each r?.powerups || [] as [label, cls] (cls)}<span class="pu pu-{cls}">{label}</span>{/each}
                </span>
                <span class="frags" title="frags">{frags[name] || 0}</span>
            </div>
        {/each}
    {/if}
{:else}
    <div class="player-clip-hint">No per-player streams in this demo.</div>
{/each}

<style>
    .pp-team {
        font-size: 12px;
        font-weight: 600;
        margin: 4px 0 1px;
    }
    .pp-player {
        display: grid;
        grid-template-columns: 88px minmax(0, 1fr) 56px auto 26px;
        align-items: center;
        gap: 6px;
        font-size: 12px;
        padding: 1px 2px;
    }
    .pp-player.pov {
        background: rgba(0, 217, 255, 0.08);
    }
    .pp-player.dead {
        opacity: 0.5;
    }
    .name,
    .loc {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .name {
        cursor: pointer;
    }
    .loc {
        color: #aaa;
    }
    .stack,
    .frags {
        font-family: monospace;
        text-align: right;
    }
    .wpn {
        display: inline-flex;
        gap: 3px;
        font-size: 10px;
        font-weight: 600;
    }
    .w,
    .pu {
        padding: 0 3px;
        border-radius: 2px;
        background: rgba(255, 255, 255, 0.08);
        color: #bbb;
    }
    /* The Timeline graphs' colours (GRAPH_COLORS, src/lib/timeline.js). */
    .w-rl {
        color: rgb(255, 107, 107);
    }
    .w-lg {
        color: rgb(0, 217, 255);
    }
    .pu-quad {
        color: #fff;
        background: rgba(0, 150, 255, 0.9);
    }
    .pu-pent {
        color: #fff;
        background: rgba(255, 0, 0, 0.9);
    }
    .pu-ring {
        color: #222;
        background: rgba(255, 235, 59, 0.9);
    }
</style>
