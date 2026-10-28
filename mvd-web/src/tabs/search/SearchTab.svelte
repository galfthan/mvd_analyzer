<script>
    // The Search tab (first; always available even when no demo is loaded):
    // the load panel (a file by button or drop, a hub game id or URL) and
    // the hub search (filters, the result rows, Load more). Its state is
    // search-model.js and src/state/shell.js; a click on a row loads that
    // game. The `active` class is toggled by src/app/tabs.js like on every
    // other tab, so the class attribute stays static here.
    import { SEARCH_MODE_LABELS, searchRow, pagerLabel, hasMore } from '../../lib/search.js';
    import { loadStatus, hubInput, hubBusy } from '../../state/shell.js';
    import { uploadFile, loadFromHub, loadGameFromSearch } from '../../app/load.js';
    import { searchForm, searchStatus, searchBusy, searchResults, searchPaging, runSearch } from './search-model.js';

    let fileInput;
    let dragover = $state(false);

    function onFile(e) {
        if (e.target.files.length > 0) uploadFile(e.target.files[0]);
    }
    function onDragOver(e) {
        e.preventDefault();
        dragover = true;
    }
    function onDrop(e) {
        e.preventDefault();
        dragover = false;
        if (e.dataTransfer.files.length > 0) uploadFile(e.dataTransfer.files[0]);
    }
    function onSubmit(e) {
        e.preventDefault();
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
                <label class="search-field">
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
            </div>
        </form>
        <div id="search-status" class={$searchStatus.cls}>{$searchStatus.text}</div>
        <div id="search-results" class="search-results">
            {#if $searchResults.empty}
                <div class="search-empty">No demos match these filters.</div>
            {/if}
            {#each $searchResults.rows as game}
                {@const row = searchRow(game)}
                <button type="button" class="search-result" data-game-id={game.id} onclick={() => loadGameFromSearch(game)}><span class="search-result-mode">{row.mode}</span><span class="search-result-map">{row.map}</span><span class="search-result-contest">{row.contest}</span><span class="search-result-tag">{row.tag}</span><span class="search-result-date">{row.date}</span></button>
            {/each}
            {#if $searchResults.pager}
                <div class="search-pager">
                    <span class="search-pager-count">{pagerLabel($searchPaging)}</span>
                    {#if hasMore($searchPaging)}
                        <button type="button" class="search-submit search-pager-more" onclick={() => runSearch(true)}>Load more</button>
                    {/if}
                </div>
            {/if}
        </div>
    </div>
</div>
