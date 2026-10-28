<script>
    // Over the player while a run moves between clips: a short dip to black
    // that also hides the engine catching up with the jump (a backward jump
    // restarts the demo and fast-forwards), then the clip's title — its
    // place in the run, label and who it watches — fading out after a
    // couple of seconds.
    import { playlist } from './playlist-store.js';
    import { teams, colorOf } from '../state/index.js';
    import { engine } from './index.js';

    const CARD_MS = 2600;
    const VEIL_MIN_MS = 250;
    const VEIL_MAX_MS = 3000;

    /** @type {import('../lib/playlist.js').Clip | null} */
    let shown = null;
    let card = $state(/** @type {{ n: string, label: string, who: string } | null} */ (null));
    let veil = $state(false);
    let veilSince = 0;
    /** @type {ReturnType<typeof setTimeout> | undefined} */
    let cardTimer;

    $effect(() => {
        const run = $playlist.run;
        const clip = run ? run.clips[run.index] : null;
        if (clip === shown) return;
        shown = clip;
        clearTimeout(cardTimer);
        if (!run || !clip) {
            card = null;
            veil = false;
            return;
        }
        card = {
            n: run.transient ? '' : `${run.index + 1} / ${run.clips.length}`,
            label: clip.label || '',
            who: clip.who || '',
        };
        veil = true;
        veilSince = performance.now();
        cardTimer = setTimeout(() => { card = null; }, CARD_MS);
    });

    // The veil lifts once the jump has landed (and has been up long enough
    // to read as a cut), or after a while regardless.
    $effect(() => engine.onTick(() => {
        if (!veil) return;
        const up = performance.now() - veilSince;
        if ((up > VEIL_MIN_MS && !engine.seeking()) || up > VEIL_MAX_MS) veil = false;
    }));

    $effect(() => () => clearTimeout(cardTimer));
</script>

<div class="clip-veil" class:on={veil}></div>
{#if card}
    <div class="clip-card">
        {#if card.n}<span class="n">{card.n}</span>{/if}
        {#if card.label}<span class="label">{card.label}</span>{/if}
        {#if card.who}<span class="who" class:label={!card.label} style:color={colorOf($teams, card.who, $teams.perPlayer)}>watching {card.who}</span>{/if}
    </div>
{/if}

<style>
    .clip-veil {
        position: absolute;
        inset: 0;
        z-index: 1;
        background: #000;
        opacity: 0;
        pointer-events: none;
        transition: opacity 0.35s ease-out;
    }
    .clip-veil.on {
        opacity: 1;
        transition-duration: 0.12s;
    }
    .clip-card {
        position: absolute;
        left: 50%;
        top: 22%;
        transform: translateX(-50%);
        z-index: 2;
        display: flex;
        flex-direction: column;
        align-items: center;
        gap: 2px;
        max-width: 80%;
        padding: 8px 16px;
        border-radius: 6px;
        background: rgba(0, 0, 0, 0.65);
        color: #fff;
        text-align: center;
        pointer-events: none;
        animation: card 2.6s ease-in-out forwards;
    }
    .n {
        font-size: 11px;
        color: #aaa;
        letter-spacing: 0.06em;
    }
    .label {
        font-size: 16px;
        font-weight: 600;
    }
    .who {
        font-size: 12px;
    }
    @keyframes card {
        0% { opacity: 0; }
        10% { opacity: 1; }
        80% { opacity: 1; }
        100% { opacity: 0; }
    }
</style>
