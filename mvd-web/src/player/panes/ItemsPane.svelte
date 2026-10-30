<script>
    // Items: the Map tab's item list as a pane — every tracked armour,
    // weapon, mega and powerup, its loc, and whether it is up, held or the
    // seconds to its respawn at the playhead (MvdMap.itemStatus over the map
    // model's data). The swatch mirrors the item's map marker.
    import { ITEM_MARKER_STYLES } from '../../../../mvd-map-view/src/index.js';
    import { panelItems, itemStatusCell } from '../../lib/map.js';
    import { view, mapData } from '../../tabs/map/map-model.js';
    import { onFrame } from './tick.js';

    /** @typedef {import('../../lib/map.js').MapItem & { loc?: string }} Item */

    let items = $derived(/** @type {Item[]} */ (panelItems($mapData?.result.items?.items, ITEM_MARKER_STYLES)));
    let cells = $state.raw(/** @type {Array<{ text: string, cls: string, taken: boolean }>} */ ([]));

    $effect(() => {
        const list = items;
        return onFrame((t) => {
            cells = list.map((it) => {
                const st = view.itemStatus(it, t);
                return { ...itemStatusCell(st), taken: !st.up };
            });
        });
    });

    /**
     * @param {HTMLElement} node
     * @param {typeof ITEM_MARKER_STYLES[string]} style
     */
    function swatch(node, style) {
        node.style.background = style.fill;
        if (style.outline) {
            node.style.border = `1.5px solid ${style.outline}`;
            node.style.boxSizing = 'border-box';
        }
        if (style.label) {
            node.textContent = style.label;
            node.style.color = style.textColor || style.outline || '#fff';
        }
    }
</script>

{#if items.length}
    <table class="map-items-table">
        <tbody>
            {#each items as item, i (item)}
                <tr class:taken={cells[i]?.taken}>
                    <td class="item-swatch-cell"><span class="item-swatch" use:swatch={ITEM_MARKER_STYLES[item.kind]}></span></td>
                    <td class="item-loc">{item.loc || ''}</td>
                    <td class={cells[i]?.cls ?? 'item-status'}>{cells[i]?.text ?? ''}</td>
                </tr>
            {/each}
        </tbody>
    </table>
{:else}
    <div class="player-clip-hint">No tracked items in this demo.</div>
{/if}
