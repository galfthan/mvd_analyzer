<script>
    // The maps of the match the shown demo belongs to, in the top bar
    // (Topbar.svelte) in place of the single demo's line: the match's score
    // in maps, then one tab a map in the order played, each with its frags,
    // and the day. A click loads that map's demo on the tab the user is on
    // (src/app/load.js loadMatchGame), so the same view — the Summary, the
    // Timeline, the Aim stats — is compared map by map. From anywhere on
    // the page, 1–9 open that map and p / n the one before / after (the
    // arrows are the playback's, src/player/player-keys.js). A match that is
    // a QWLeague series (src/lib/qwl.js) says so after the day, with what it
    // was, linking to its page there.
    import { teams, colorOf } from '../state/teams.js';
    import { mapScore, matchRestarted } from '../lib/matches.js';
    import { seriesLabel } from '../lib/qwl.js';
    import { loadMatchGame } from '../app/load.js';

    /** @type {{ match: import('../lib/matches.js').Match, index: number }} */
    const { match, index } = $props();
    // The top bar shows only a match that has two sides.
    const [a, b] = $derived(/** @type {[import('../lib/matches.js').MatchSide, import('../lib/matches.js').MatchSide]} */ (match.sides));
    // The games broken off and restarted: shown, dimmed, no map for either side.
    const restarted = $derived(matchRestarted(match));
    // The id of the map being loaded, '' when none is.
    let loading = $state('');

    // A side's colour is the one the demo shown gives that name: a side is
    // a team, or a duel's player, and the canonical order (src/state/teams.js)
    // lists exactly those names.
    /** @param {string} name */
    const sideColor = (name) => colorOf($teams, name, true);

    /** @param {import('../data/hub.js').HubGame | undefined} game */
    async function open(game) {
        if (!game || loading || game === match.games[index]) return;
        loading = String(game.id);
        try {
            await loadMatchGame(game);
        } finally {
            loading = '';
        }
    }

    // Keys typed into a field, or with a modifier, are left alone.
    /** @param {KeyboardEvent} e */
    function onKey(e) {
        if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
        if (/** @type {Element} */ (e.target)?.closest?.('input, textarea, select, [contenteditable]')) return;
        const to = e.key === 'n' ? index + 1 : e.key === 'p' ? index - 1 : /^[1-9]$/.test(e.key) ? +e.key - 1 : -1;
        if (!match.games[to]) return;
        e.preventDefault();
        open(match.games[to]);
    }
</script>

<svelte:window onkeydown={onKey} />

<nav class="match-tabs" id="match-tabs" aria-label="Maps of this match">
    <span class="match-tabs-score" title="Maps won">
        <span style:color={sideColor(a.name)} title={a.label}>{a.name}</span>
        <b>{a.wins} : {b.wins}</b>
        <span style:color={sideColor(b.name)} title={b.label}>{b.name}</span>
    </span>
    <button type="button" class="match-tabs-step" title="Previous map (p)" disabled={index === 0 || !!loading}
        onclick={() => open(match.games[index - 1])}>‹</button>
    <div class="match-tabs-list">
        {#each match.games as game, i (game.id)}
            {@const score = mapScore(game, a.name)}
            {@const off = restarted.has(String(game.id))}
            <button type="button" class="match-tab" class:restarted={off} title={off ? `Broken off and restarted: not counted (key ${i + 1})` : `Key ${i + 1}`} class:active={i === index} class:loading={loading === String(game.id)}
                aria-current={i === index ? 'page' : undefined} disabled={!!loading && loading !== String(game.id)}
                data-game-id={game.id} onclick={() => open(game)}>
                <span class="match-tab-n">{i + 1}</span>
                <span class="match-tab-map">{game.map}</span>
                {#if score}
                    <span class="match-tab-score">
                        <span class:won={!off && score.own > score.other} style:color={!off && score.own > score.other ? sideColor(a.name) : undefined}>{score.own}</span>–<span class:won={!off && score.other > score.own} style:color={!off && score.other > score.own ? sideColor(b.name) : undefined}>{score.other}</span>
                    </span>
                {/if}
            </button>
        {/each}
    </div>
    <button type="button" class="match-tabs-step" title="Next map (n)" disabled={index === match.games.length - 1 || !!loading}
        onclick={() => open(match.games[index + 1])}>›</button>
    <span class="match-tabs-date">{(match.games[index].timestamp || '').slice(0, 10)}</span>
    {#if match.qwl}
        <a class="match-tabs-qwl" href={match.qwl.url} target="_blank" rel="noopener" title="This series on qwleague.com"><span class="qwl-badge">QWL</span>{seriesLabel(match.qwl)}</a>
    {/if}
</nav>
