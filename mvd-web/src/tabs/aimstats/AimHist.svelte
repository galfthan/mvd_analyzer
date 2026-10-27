<script>
    // One histogram under a crosshair image (aimMarginalHist / aimRampHist,
    // src/lib/aim-plot.js): bars, hover titles, the axis labels; a marginal
    // adds the shaded on-hull band and the dead-center rule, the ramp its
    // labelled y gutter. A bin with fires keeps a 2px stub.
    let { h } = $props();
</script>

{#snippet cols()}
    {#each h.cols as col}
        <div class="aim-hist-col" title={col.title}><div class="aim-hist-fill" style:height={col.height} style:opacity={h.kind === 'ramp' ? col.opacity : null} style:min-height={col.stub ? '2px' : null}></div></div>
    {/each}
{/snippet}

<div class="aim-hist">
    {#if h.kind === 'ramp'}
        <div class="aim-hist-title">Ramp <span class="aim-sel-note">hit % by time since the shaft opened; fires &lt; 150 ms apart are one shaft; faint = few fires</span></div>
        <div class="aim-hist-ywrap">
            <div class="aim-hist-yaxis"><span>{h.yMax}%</span><span>0</span></div>
            <div class="aim-hist-plot">{@render cols()}</div>
        </div>
        <div class="aim-hist-axis aim-hist-axis-inset">{#each h.axis as a}<span>{a}</span>{/each}</div>
    {:else}
        <div class="aim-hist-title">{h.name} <span class="aim-sel-note">{h.dirNote}</span></div>
        <div class="aim-hist-plot">
            <div class="aim-hist-band" style:left={h.band.left} style:width={h.band.width}></div>
            <div class="aim-hist-zero"></div>
            {@render cols()}
        </div>
        <div class="aim-hist-axis">{#each h.axis as a}<span>{a}</span>{/each}</div>
    {/if}
</div>
