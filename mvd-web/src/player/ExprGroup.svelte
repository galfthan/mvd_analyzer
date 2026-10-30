<script>
    // One group of the clip finder's filter: all / any of its items, or
    // neither (not), each item a condition or a nested group. Edits the
    // builder's tree (src/lib/clip-query.js toEditTree) in place. A
    // condition's field box suggests the source's fields and says what the
    // one typed means (`tip`); its value box suggests the values the field
    // allows and the ones these rows hold (`values`).
    import Plus from '@lucide/svelte/icons/plus';
    import X from '@lucide/svelte/icons/x';
    import Self from './ExprGroup.svelte';
    import { OPS } from '../lib/clip-query.js';

    /**
     * @typedef {import('../lib/clip-query.js').EditGroup} EditGroup
     * @typedef {import('../lib/clip-query.js').EditCond} EditCond
     */

    /** @type {{ group: EditGroup, fields: string, tip: (field: string) => string, values: (field: string) => string[], depth?: number, onremove?: () => void }} */
    let { group = $bindable(), fields, tip, values, depth = 0, onremove } = $props();

    const OP_TITLES = /** @type {Record<string, string>} */ ({
        '=': 'is (numbers as numbers, text ignoring case)',
        '!=': 'is not — also keeps the rows without the field',
        '<': 'less than', '<=': 'at most', '>': 'more than', '>=': 'at least',
        '~': 'contains (text) / has as a member (list)',
        in: 'is any of (comma-separated)',
    });

    let uid = $props.id();

    function addCond() {
        group.items.push({ kind: 'cond', field: '', op: '=', value: '' });
    }

    function addGroup() {
        group.items.push({ kind: 'group', op: group.op === 'and' ? 'or' : 'and', not: false, items: [{ kind: 'cond', field: '', op: '=', value: '' }] });
    }

    /** @param {number} i */
    function remove(i) {
        group.items.splice(i, 1);
    }
</script>

<div class="cf-group" class:nested={depth > 0}>
    <div class="cf-group-head">
        <label class="cf-not" title="Keep the rows this group does NOT match"><input type="checkbox" bind:checked={group.not}> not</label>
        <span class="pt-group">
            <button class="pt-btn" class:active={group.op === 'and'} title="Every item must hold" onclick={() => (group.op = 'and')}>all of</button>
            <button class="pt-btn" class:active={group.op === 'or'} title="Any item may hold" onclick={() => (group.op = 'or')}>any of</button>
        </span>
        <button class="pt-btn" title="Add a condition" onclick={addCond}><Plus size={13} /> condition</button>
        {#if depth < 6}
            <button class="pt-btn" title="Add a nested group" onclick={addGroup}><Plus size={13} /> group</button>
        {/if}
        {#if onremove}
            <button class="pt-btn icon" title="Remove this group" onclick={onremove}><X size={13} /></button>
        {/if}
    </div>
    {#each group.items as item, i (item)}
        {#if item.kind === 'group'}
            <Self group={/** @type {EditGroup} */ (item)} {fields} {tip} {values} depth={depth + 1} onremove={() => remove(i)} />
        {:else}
            {@const cond = /** @type {EditCond} */ (item)}
            {@const vals = cond.field ? values(cond.field) : []}
            <div class="cf-cond">
                <input class="cf-field" type="text" list={fields} placeholder="field, e.g. detail.frags" bind:value={cond.field}
                    title={cond.field ? tip(cond.field) || cond.field : 'A row field by its full path — ⓘ lists them with their values'}>
                <select class="pt-select" bind:value={cond.op} title={OP_TITLES[cond.op] || ''}>
                    {#each OPS as op (op)}
                        <option value={op} title={OP_TITLES[op] || ''}>{op}</option>
                    {/each}
                </select>
                <input class="cf-value" type="text" placeholder={cond.op === 'in' ? 'a, b, c' : vals.length ? `e.g. ${vals[0]}` : 'value'} bind:value={cond.value}
                    list={vals.length ? `${uid}-v${i}` : undefined}>
                {#if vals.length}<datalist id="{uid}-v{i}">{#each vals as v (v)}<option value={v}></option>{/each}</datalist>{/if}
                <button class="pt-btn icon" title="Remove this condition" onclick={() => remove(i)}><X size={13} /></button>
            </div>
        {/if}
    {/each}
</div>

<style>
    .cf-group {
        display: flex;
        flex-direction: column;
        gap: 4px;
    }
    .cf-group.nested {
        margin-left: 14px;
        padding-left: 8px;
        border-left: 2px solid var(--bg-panel);
    }
    .cf-group-head,
    .cf-cond {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
    }
    .cf-not {
        display: inline-flex;
        align-items: center;
        gap: 3px;
        font-size: 12px;
        color: #aaa;
    }
    .cf-field {
        width: 170px;
    }
    .cf-value {
        width: 140px;
    }
</style>
