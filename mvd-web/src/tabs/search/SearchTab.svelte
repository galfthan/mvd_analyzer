<script>
    // The Search tab (first; always available even when no demo is loaded):
    // the load panel (a file by button or drop, a hub game id or URL) and
    // the hub search (filters, the result rows, Load more). Its state is
    // search-model.js and src/state/shell.js; a click on a row loads that
    // game. With "Group into matches" on, the games two sides played in one
    // sitting list as one small scoreboard (src/lib/matches.js): the maps
    // each side won, then a column a map with its frags. A click on it opens the
    // match at its first map, a map's name at that map; the map tabs in the
    // top bar (src/shell/MatchTabs.svelte) reach the others from there.
    // A game played on QWLeague's servers carries a "QWL" badge, and where
    // mvd-api knows its series (src/lib/qwl.js) what it was — ladder or
    // tournament, best of how many; "QWL only" searches those games alone.
    // The `active` class is toggled by src/app/tabs.js like on every
    // other tab, so the class attribute stays static here.
    import { SEARCH_MODE_LABELS, searchRow, pagerLabel, hasMore } from '../../lib/search.js';
    import { loadStatus, hubInput, hubBusy } from '../../state/shell.js';
    import { uploadFile, loadFromHub, loadGameFromSearch } from '../../app/load.js';
    import { searchMatches, matchRow, mapScore, matchRestarted } from '../../lib/matches.js';
    import { isQwlGame, seriesBySha, seriesLabel } from '../../lib/qwl.js';
    import { assignTeamColors } from '../../state/teams.js';
    import {
        searchForm, searchStatus, searchBusy, searchResults, searchPaging, runSearch,
        searchGrouped, searchExtraGames, searchSeries, setSearchGrouped,
    } from './search-model.js';

    const matches = $derived($searchGrouped ? searchMatches($searchResults.rows, $searchExtraGames, $searchSeries) : []);
    const seriesOf = $derived(seriesBySha($searchSeries));

    /** @type {HTMLInputElement} */
    let fileInput;
    let dragover = $state(false);

    /** @param {Event} e */
    function onFile(e) {
        // A file input's `files` is never null.
        const files = /** @type {FileList} */ (/** @type {HTMLInputElement} */ (e.target).files);
        if (files.length > 0) uploadFile(files[0]);
    }
    /** @param {DragEvent} e */
    function onDragOver(e) {
        e.preventDefault();
        dragover = true;
    }
    /** @param {DragEvent} e */
    function onDrop(e) {
        e.preventDefault();
        dragover = false;
        // A drop event always carries its dataTransfer.
        const dt = /** @type {DataTransfer} */ (e.dataTransfer);
        if (dt.files.length > 0) uploadFile(dt.files[0]);
    }
    /** @param {SubmitEvent} e */
    function onSubmit(e) {
        e.preventDefault();
        runSearch();
    }
    // The box is a filter like the fields beside it, and searches at once.
    /** @param {Event} e */
    function onQwlOnly(e) {
        $searchForm.qwl = /** @type {HTMLInputElement} */ (e.target).checked ? '1' : '';
        runSearch();
    }
</script>

<div id="tab-search" class="tab-content active">
    <div class="panel search-load-panel">
        <!-- svelte-ignore a11y_no_static_element_interactions: a drop target; the buttons inside are the keyboard path -->
        <div class={dragover ? 'load-row dragover' : 'load-row'} id="drop-zone"
            ondragover={onDragOver} ondragleave={() => { dragover = false; }} ondrop={onDrop}>
            <input type="file" id="file-input" accept=".mvd,.mvd.gz" hidden bind:this={fileInput} onchange={onFile}>
            <button class="load-file-btn" onclick={() => fileInput.click()}>Load from File</button>
            <span class="load-separator">|</span>
            <input type="text" id="hub-input" placeholder="Game ID or hub.quakeworld.nu URL" bind:value={$hubInput}>
            <button id="hub-load-btn" disabled={$hubBusy} onclick={loadFromHub}>Load from Hub</button>
        </div>
        <div id="upload-status" class={$loadStatus.cls}>{$loadStatus.text}</div>
    </div>
    <div class="panel search-filter-panel">
        <form class="search-form" id="search-form" autocomplete="off" onsubmit={onSubmit}>
            <div class="search-row">
                <label class="search-field">
                    <span>Player</span>
                    <input type="text" id="search-player" placeholder="any" bind:value={$searchForm.player}>
                </label>
                <label class="search-field">
                    <span>Team</span>
                    <input type="text" id="search-team" placeholder="any" bind:value={$searchForm.team}>
                </label>
                <label class="search-field">
                    <span>Map</span>
                    <input type="text" id="search-map" placeholder="any" bind:value={$searchForm.map}>
                </label>
                <label class="search-field search-field-short">
                    <span>Mode</span>
                    <!-- The facets are SEARCH_MODE_LABELS (src/lib/search.js),
                         which also labels the rows below. -->
                    <select id="search-mode" bind:value={$searchForm.mode}>
                        <option value="">Any</option>
                        {#each Object.entries(SEARCH_MODE_LABELS) as [value, label] (value)}
                            <option {value}>{label}</option>
                        {/each}
                    </select>
                </label>
                <label class="search-field">
                    <span>Game tag</span>
                    <input type="text" id="search-tag" placeholder="any" bind:value={$searchForm.tag}>
                </label>
                <label class="search-field search-field-date">
                    <span>From</span>
                    <input type="date" id="search-from" bind:value={$searchForm.from}>
                </label>
                <label class="search-field search-field-date">
                    <span>To</span>
                    <input type="date" id="search-to" bind:value={$searchForm.to}>
                </label>
                <button type="submit" class="search-submit" id="search-submit" disabled={$searchBusy}>Search</button>
                <label class="search-group-toggle" title="List the maps two sides played in a row as one match">
                    <input type="checkbox" id="search-grouped" checked={$searchGrouped}
                        onchange={(e) => setSearchGrouped(/** @type {HTMLInputElement} */ (e.target).checked)}>
                    Group into matches
                </label>
                <label class="search-group-toggle" title="Only games played on QWLeague's servers (qwleague.com): its ladders, tournaments and pracs">
                    <input type="checkbox" id="search-qwl" checked={$searchForm.qwl === '1'} disabled={$searchBusy} onchange={onQwlOnly}>
                    QWL only
                </label>
            </div>
        </form>
        <div id="search-status" class={$searchStatus.cls}>{$searchStatus.text}</div>
        <div id="search-results" class="search-results">
            {#if $searchResults.empty}
                <div class="search-empty">No demos match these filters.</div>
            {/if}
            <!-- A row's tag: for a game on QWLeague's servers the badge, then
                 what its series was where that is known, else the hub's tag. -->
            {#snippet tagCell(/** @type {import('../../data/hub.js').HubGame} */ game, /** @type {import('../../lib/qwl.js').QwlSeries | undefined} */ series, /** @type {string} */ tag)}
                <span class="search-result-tag">{#if series || isQwlGame(game)}<span class="qwl-badge">QWL</span>{/if}{series ? seriesLabel(series) : tag}</span>
            {/snippet}
            {#snippet gameRow(/** @type {import('../../data/hub.js').HubGame} */ game)}
                {@const row = searchRow(game)}
                <button type="button" class="search-result" data-game-id={game.id} onclick={() => loadGameFromSearch(game)}><span class="search-result-mode">{row.mode}</span><span class="search-result-map">{row.map}</span><span class="search-result-contest">{row.contest}</span>{@render tagCell(game, seriesOf.get(game.demo_sha256), row.tag)}<span class="search-result-date">{row.date}</span></button>
            {/snippet}
            {#if $searchGrouped}
                {#each matches as match (match.id)}
                    {#if match.games.length === 1}
                        {@render gameRow(match.games[0])}
                    {:else if match.sides}
                        {@const row = matchRow(match)}
                        {@const [a, b] = match.sides}
                        <!-- The colours the demo itself will give these two names (src/state/teams.js). -->
                        {@const [colorA, colorB] = assignTeamColors([a.name, b.name])}
                        {@const restarted = matchRestarted(match)}
                        <div class="search-match" data-match-id={match.id}>
                            <span class="search-result-mode">{SEARCH_MODE_LABELS[match.mode] || match.mode}</span>
                            <div class="search-board">
                                <span></span>
                                <!-- Its ::after covers the row: a click anywhere but a map's name opens the match. -->
                                <button type="button" class="search-board-sides" title="Open the match at its first map"
                                    onclick={() => loadGameFromSearch(match.games[0])}><span style:color={colorA}>{a.label || a.name}</span><span style:color={colorB}>{b.label || b.name}</span></button>
                                <span class="search-board-head search-board-total">maps</span>
                                <span class="search-board-wins search-board-total">{a.wins}</span>
                                <span class="search-board-wins search-board-total">{b.wins}</span>
                                {#each match.games as game (game.id)}
                                    {@const score = mapScore(game, a.name)}
                                    {@const off = restarted.has(String(game.id))}
                                    <button type="button" class="search-board-map" class:restarted={off} data-game-id={game.id}
                                        title={off ? 'Broken off and restarted: not counted. Open this game' : 'Open this map'}
                                        onclick={() => loadGameFromSearch(game)}>{game.map || '-'}</button>
                                    <span class="search-board-frags" class:restarted={off} class:won={!off && score && score.own > score.other}>{score?.own}</span>
                                    <span class="search-board-frags" class:restarted={off} class:won={!off && score && score.other > score.own}>{score?.other}</span>
                                {/each}
                            </div>
                            {@render tagCell(match.games[0], match.qwl, row.tag)}
                            <span class="search-result-date">{row.date}</span>
                        </div>
                    {/if}
                {/each}
            {:else}
                {#each $searchResults.rows as game}
                    {@render gameRow(game)}
                {/each}
            {/if}
            {#if $searchResults.pager}
                <div class="search-pager">
                    <span class="search-pager-count">{pagerLabel($searchPaging)}{#if $searchGrouped}{` in ${matches.length} matches`}{/if}</span>
                    {#if hasMore($searchPaging)}
                        <button type="button" class="search-submit search-pager-more" disabled={$searchBusy} onclick={() => runSearch(true)}>Load more</button>
                    {/if}
                </div>
            {/if}
        </div>
    </div>
</div>
