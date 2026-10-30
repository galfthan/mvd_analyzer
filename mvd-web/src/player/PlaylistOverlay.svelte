<script>
    // Over the game view while a run plays: the current item's title card
    // (a colour, fading in, and out once the next clip plays — the jump to
    // it, or its demo loading, happens underneath; its time is a minimum)
    // and its texts, each shown between its own
    // from / to inside the item with its fades. Time inside a clip is the
    // engine's demo clock from the clip's start; inside a card, the wall
    // clock from when it came up (src/lib/playlist.js Card / Text).
    import { playlist } from './playlist-store.js';

    /** @typedef {import('../lib/playlist.js').Clip} Clip */
    /** @typedef {import('../lib/playlist.js').Text} Text */

    /** @type {{ engine: import('./engine.js').PlayerEngine }} */
    let { engine } = $props();

    let item = $state.raw(/** @type {Clip | null} */ (null));
    // The card drawn, and whether it is up (it stays drawn while it fades out).
    let card = $state.raw(/** @type {Clip | null} */ (null));
    let cardUp = $state(false);
    let cardMs = $state(0);    // ms into the card shown
    let clipMs = $state(0);    // ms into the clip playing
    let since = 0;             // the card's start, performance.now()
    let frame = 0;
    /** @type {ReturnType<typeof setTimeout> | undefined} */
    let fadeTimer;
    // A card held up after its time until the next clip plays (or a
    // while): its texts stay, the clip's wait.
    let holding = $state(false);
    const HOLD_MAX_MS = 20000;

    function fadeOut() {
        holding = false;
        cancelAnimationFrame(frame);
        clearTimeout(fadeTimer);
        cardUp = false;
        const gone = card;
        fadeTimer = setTimeout(() => { if (card === gone && !cardUp) card = null; }, gone?.card?.fadeOut ?? 0);
    }

    $effect(() => {
        const run = $playlist.run;
        const next = run ? run.clips[run.index] ?? null : null;
        if (next === item) return;
        item = next;
        clipMs = 0;
        clearTimeout(fadeTimer);
        if (next?.card) {
            cancelAnimationFrame(frame);
            holding = false;
            cardMs = 0;
            card = next;
            cardUp = false;
            since = performance.now();
            // Up on the next frame, so the fade-in runs from 0.
            frame = requestAnimationFrame(() => { cardUp = true; tick(); });
        } else if (card && cardUp && next) {
            holding = true;
            fadeTimer = setTimeout(fadeOut, HOLD_MAX_MS);
        } else if (card) {
            fadeOut();
        }
    });

    function tick() {
        cardMs = performance.now() - since;
        if (item?.card || holding) frame = requestAnimationFrame(tick);
    }

    // A clip's time: the engine's demo clock.
    $effect(() => engine.onTick((demoT) => {
        if (item && !item.card && item.text) clipMs = (demoT - item.from) * 1000;
        if (holding && !engine.seeking() && engine.isPlaying()) fadeOut();
    }));

    $effect(() => () => { cancelAnimationFrame(frame); clearTimeout(fadeTimer); });

    // The texts drawn, at their item's time: a held card's (no end until it
    // goes), a card's, or a clip's.
    let texts = $derived.by(() => {
        if (holding && card) return { list: card.text || [], at: cardMs, length: Infinity };
        if (!item) return { list: [], at: 0, length: 0 };
        if (item.card) return { list: item.text || [], at: cardMs, length: item.card.ms };
        return { list: item.text || [], at: clipMs, length: (item.to - item.from) * 1000 };
    });
    /** @param {Text} x */
    const shown = (x) => texts.at >= x.from && texts.at < (x.to ?? texts.length);
</script>

<div class="po">
    {#if card?.card}
        <div class="po-card" style:background={card.card.bg}
            style:opacity={cardUp ? card.card.opacity : 0}
            style:transition-duration="{cardUp ? card.card.fadeIn : card.card.fadeOut}ms"></div>
    {/if}
    {#each texts.list as x (x)}
        {@const on = shown(x)}
        <div class="po-text at-{x.at} font-{x.font}" style:font-size="{x.size}cqh" style:color={x.color}
            style:opacity={on ? 1 : 0} style:transition-duration="{on ? x.fadeIn : x.fadeOut}ms">{x.t}</div>
    {/each}
</div>

<style>
    .po {
        position: absolute;
        inset: 0;
        z-index: 3;
        container-type: size;
        pointer-events: none;
        overflow: hidden;
    }
    .po-card {
        position: absolute;
        inset: 0;
        transition-property: opacity;
        transition-timing-function: ease-in-out;
    }
    .po-text {
        position: absolute;
        max-width: 90%;
        line-height: 1.15;
        white-space: pre-line;
        text-align: center;
        text-shadow: 0 0 0.12em rgba(0, 0, 0, 0.9), 0 0.04em 0.08em rgba(0, 0, 0, 0.9);
        transition-property: opacity;
        transition-timing-function: ease-in-out;
    }
    .font-title {
        font-family: 'Rajdhani', sans-serif;
        font-weight: 700;
        letter-spacing: 0.02em;
    }
    .font-body {
        font-family: 'Inter', sans-serif;
    }
    .font-mono {
        font-family: monospace;
    }
    .at-center { left: 50%; top: 50%; transform: translate(-50%, -50%); }
    .at-top { left: 50%; top: 6%; transform: translateX(-50%); }
    .at-bottom { left: 50%; bottom: 6%; transform: translateX(-50%); }
    .at-left { left: 4%; top: 50%; transform: translateY(-50%); text-align: left; }
    .at-right { right: 4%; top: 50%; transform: translateY(-50%); text-align: right; }
    .at-top-left { left: 4%; top: 6%; text-align: left; }
    .at-top-right { right: 4%; top: 6%; text-align: right; }
    .at-bottom-left { left: 4%; bottom: 6%; text-align: left; }
    .at-bottom-right { right: 4%; bottom: 6%; text-align: right; }
</style>
