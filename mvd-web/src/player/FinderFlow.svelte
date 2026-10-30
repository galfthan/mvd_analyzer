<script>
    // A joint between two clip finder stages: a rail of beads flowing down
    // to the next stage while rows pass on, and the count in a pill beside
    // it (pulsing when it changes). No rows: the rail stands still and the
    // pill turns red. `note` (no demo yet, an error, waiting) replaces the
    // count.
    /** @type {{ n?: number | null, what: string, tone: 'rows' | 'kept' | 'ordered' | 'clips', note?: string, error?: boolean }} */
    let { n = null, what, tone, note = '', error = false } = $props();

    const flowing = $derived(!note && n != null && n > 0);
    const text = $derived(n == null ? '' : `${n} ${what}${n === 1 ? '' : 's'}`);
</script>

<div class="flow" class:flowing>
    <svg class="rail" width="14" height="30" viewBox="0 0 14 30" aria-hidden="true">
        <line x1="7" y1="0" x2="7" y2="30" class="track" />
        <line x1="7" y1="-8" x2="7" y2="38" class="beads tone-{tone}" />
    </svg>
    {#if note}
        <span class="note" class:error>{note}</span>
    {:else if n != null}
        {#key n}<span class="pill tone-{tone}" class:empty={n === 0}>{text}</span>{/key}
    {/if}
</div>

<style>
    .flow {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-left: 6px;
        min-height: 30px;
    }
    .track {
        stroke: var(--bg-panel);
        stroke-width: 2;
    }
    .beads {
        stroke-width: 4;
        stroke-linecap: round;
        stroke-dasharray: 0 8;
        opacity: 0.35;
    }
    .flowing .beads {
        opacity: 1;
        animation: pump 0.9s linear infinite;
    }
    @keyframes pump {
        to { stroke-dashoffset: -8; }
    }
    .pill {
        padding: 1px 9px;
        border-radius: 10px;
        font-size: 12px;
        font-weight: 600;
        color: var(--bg-deep);
        animation: pulse 0.35s ease-out;
    }
    @keyframes pulse {
        from { transform: scale(1.18); filter: brightness(1.4); }
    }
    .note {
        font-size: 12px;
        color: #aaa;
    }
    .note.error {
        color: #ff8080;
    }
    .tone-rows { stroke: #3fc8ff; background: #3fc8ff; }
    .tone-kept { stroke: #6bd66b; background: #6bd66b; }
    .tone-ordered { stroke: #f0b43c; background: #f0b43c; }
    .tone-clips { stroke: #e070f0; background: #e070f0; }
    .pill.empty {
        background: #ff6b6b;
    }
    @media (prefers-reduced-motion: reduce) {
        .flowing .beads,
        .pill {
            animation: none;
        }
    }
</style>
