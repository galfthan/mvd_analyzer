<script>
    // Entities: learn mode's filters and table as a pane — the map's static
    // layout (items, teleporters with their exits, spawns, buttons, doors)
    // by category. The filters are the map model's (entityFilters), so the
    // map panes show the categories ticked here while learning the map (the
    // map menu's "Learn the map").
    import { view, mapData, learnMode } from '../../tabs/map/map-model.js';
    import { entityRows, entityRowsHtml } from '../../lib/map.js';

    const CATEGORIES = [
        ['weapon', 'Weapons', '#ff6b6b'], ['armor', 'Armor', '#ffc107'], ['health', 'Health', '#00c853'],
        ['ammo', 'Ammo', '#b0a070'], ['powerup', 'Powerups', '#2196f3'], ['teleporter', 'Teleporters', '#b388ff'],
        ['spawn', 'Spawns', '#888'], ['button', 'Buttons', '#ff9800'], ['door', 'Doors', '#a1887f'],
    ];

    const s = view.state;
    let filters = $state({ ...s.entityFilters });
    let html = $derived.by(() => {
        $mapData;
        return entityRowsHtml(entityRows(s.mapEntities || [], filters, (/** @type {Record<string, any>} */ e) => view.entityCategory(e)));
    });

    /** @param {string} cat @param {boolean} on */
    function setFilter(cat, on) {
        s.entityFilters[cat] = on;
        filters = { ...s.entityFilters };
        s.renderDirty = true;
    }
</script>

{#if $mapData && s.mapEntities.length}
    {#if !$learnMode}
        <div class="player-clip-hint">The map shows these in learn mode — <button class="pt-btn" onclick={() => learnMode.set(true)}>Learn the map</button></div>
    {/if}
    <div class="map-entity-filters">
        {#each CATEGORIES as [cat, label, color] (cat)}
            <label class="map-entity-filter"><input type="checkbox" checked={!!filters[cat]} onchange={(e) => setFilter(cat, e.currentTarget.checked)}><span class="map-entity-swatch" style:background={color}></span>{label}</label>
        {/each}
    </div>
    <table class="stats-table entities-table">
        <thead><tr><th>Class</th><th>Type</th><th>Name</th><th>Loc</th><th>Destination</th></tr></thead>
        <tbody>{@html html}</tbody>
    </table>
{:else}
    <div class="player-clip-hint">No map entities for this demo.</div>
{/if}

<style>
    .entities-table {
        font-size: 12px;
    }
</style>
