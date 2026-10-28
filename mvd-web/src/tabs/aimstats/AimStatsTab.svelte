<script>
    // The Aim Stats tab (experimental): the Victims filter, the all-players
    // accuracy tables (one per weapon) and, for the player picked inside the
    // Crosshair placement panel, the LG / SG crosshair-density images with
    // their yaw / pitch marginals and the LG ramp. src/lib/aim.js has the
    // table rules and markup, src/lib/aim-plot.js the binning, and
    // density-canvas.js paints the images. src/app/tabs.js toggles the `active`
    // class like on every other .tab-content.
    //
    // The tab is built — once, on the next frame it shows — after the demo
    // or the teams changed; a new demo puts the filter back to All and the
    // picker on the first player. A filter or player change rebuilds at
    // once. Table rows are markup built by hand rather than an {#each},
    // because the shared makeSortable (src/ui/sortable.js) sorts by moving
    // row nodes. The two panels carry data-panel and sit directly in
    // #tab-aimstats for src/ui/panels; when one comes back into view the
    // images are painted again (onPanelReveal).
    import { onMount } from 'svelte';
    import { demo, teams, activeTab } from '../../state/index.js';
    import { hasTeammates } from '../../lib/result.js';
    import { damageProvenanceHtml, provenanceClass } from '../../lib/provenance.js';
    import { aimModeText, aimWeaponTables, aimChips } from '../../lib/aim.js';
    import { aimSampleIndices, aimNodataText, aimDensityGrid, aimHistRow, aimHoverTitle } from '../../lib/aim-plot.js';
    import { paintAimDensity, hoverPoint } from './density-canvas.js';
    import { makeSortable } from '../../ui/sortable.js';
    import { onPanelReveal } from '../../ui/panels/reveal.js';
    import AimHist from './AimHist.svelte';

    const WEAPONS = ['lg', 'sg'];
    const noPlot = (w) => ({ shown: false, nodata: `No ${w.toUpperCase()} fired by this player.`, hists: [], grid: null });

    let result = $state.raw(null);
    let hasData = $state(false);
    let teamless = $state(false);
    let note = $state.raw(null);
    let chips = $state.raw([]);
    let tables = $state.raw([]);
    let filter = $state('all');
    let player = $state(null);
    let mode = $state('');
    let plots = $state.raw({ lg: noPlot('lg'), sg: noPlot('sg') });
    // Bumped on every per-player build so the histogram rows are rebuilt
    // rather than patched.
    let gen = $state(0);
    let chipBox = $state();
    const canvases = $state({});
    const hover = {};

    let dirty = true;
    let queued = false;
    let lastHandle = null;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== 'aimstats') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === 'aimstats') build();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function build() {
        dirty = false;
        const handle = demo.get();
        if (handle !== lastHandle) {
            lastHandle = handle;
            filter = 'all';
            player = null;
        }
        result = handle?.result ?? null;
        const aim = result?.aim;
        hasData = !!(aim && aim.players && aim.players.length);
        // Unconditional: a demo with a wire damage log has no banner, so a
        // reconstructed demo's note cannot survive into the next load.
        note = damageProvenanceHtml(result);
        // The Team option is hidden wherever nobody has a team-mate — every
        // duel and every FFA. Enemy vs All still differ there via RL/GL
        // self-splash, so the rest of the toggle stays.
        //
        // The verdict comes from the mode descriptor (hasTeammates), not from
        // the aim rows' own `mode` field: that field says "team" on an FFA —
        // it names the enemy-set rule the aim join applied, not the ruleset —
        // so keying on it put a filter that can never match anything on
        // every FFA aim tab.
        teamless = !hasTeammates(result);
        if (teamless && filter === 'team') filter = 'all';
        const t = teams.get();
        chips = aimChips(result, t.order, t.colors);
        buildTables();
        buildPlayer();
    }

    function buildTables() {
        const t = teams.get();
        tables = aimWeaponTables(result, filter, t.order, t.colors);
    }

    // The per-player panel: the mode line, then per weapon the samples the
    // filter selects — the image and its histograms, or the no-data line.
    function buildPlayer() {
        if (!hasData) return;
        const players = result.aim.players;
        if (!players.some((p) => p.player === player)) player = players[0].player;
        const pa = players.find((p) => p.player === player);
        mode = aimModeText(pa);
        const c = pa.crosshair;
        const next = {};
        for (const w of WEAPONS) {
            const idx = aimSampleIndices(c, w, filter);
            next[w] = {
                shown: idx.length > 0,
                // The line is only rewritten when it shows.
                nodata: idx.length ? plots[w].nodata : aimNodataText(w, filter),
                hists: aimHistRow(c, idx, w === 'lg' ? pa.lgRamp : null, filter),
                grid: idx.length ? aimDensityGrid(c, idx) : null,
            };
        }
        plots = next;
        gen++;
        paint();
    }

    function paint() {
        for (const w of WEAPONS) {
            const g = plots[w].grid;
            if (g && canvases[w]) hover[w] = paintAimDensity(canvases[w], g);
        }
    }

    function pickPlayer(name) {
        player = name;
        buildPlayer();
    }

    function pickFilter(kind) {
        filter = kind;
        buildTables();
        buildPlayer();
    }

    // Left / Right arrow keys (with the strip focused) step prev/next for
    // quick A/B comparison.
    function chipKey(e) {
        if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
        e.preventDefault();
        if (!chips.length) return;
        let i = chips.findIndex((c) => c.player === player);
        i = (i + (e.key === 'ArrowRight' ? 1 : -1) + chips.length) % chips.length;
        pickPlayer(chips[i].player);
    }

    // Hover read-out: shot/hit counts in the coarse bin under the cursor.
    function onHover(e, w) {
        const d = hover[w];
        if (!d) return;
        const canvas = canvases[w];
        const [mx, my] = hoverPoint(canvas, d, e);
        canvas.title = aimHoverTitle(d, mx, my);
    }

    // Fill the weapon-tables container: one block per weapon, each table
    // made sortable (a fresh table every build, so the sort starts over).
    function weaponBlocks(node, list) {
        const fill = (blocks) => {
            node.replaceChildren();
            for (const b of blocks) {
                const block = document.createElement('div');
                block.className = 'aim-weapon-block';
                block.innerHTML = b.html;
                node.appendChild(block);
                makeSortable(block.querySelector('table'));
            }
        };
        fill(list);
        return { update: fill };
    }

    onMount(() => {
        const offs = [
            demo.subscribe(invalidate),
            teams.subscribe(invalidate),
            activeTab.subscribe(schedule),
            onPanelReveal('aimstats', paint),
        ];
        return () => offs.forEach((off) => off());
    });

    const hide = $derived(!!result && !hasData);
</script>

<div id="tab-aimstats" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>

    <div id="aim-empty" class="empty-message" style:display={hide ? null : 'none'}>
        No aim data in this demo.
    </div>

    <div class="panel aim-controls" id="aim-controls" style:display={hide ? 'none' : null}>
        <span class="aim-controls-label">Victims:</span>
        <div id="aim-victim-toggle" class="aim-chips"
             title="Filter every table and plot by who the shots hit">
            <button type="button" class="aim-chip" data-kind="all" class:active={hasData && filter === 'all'} onclick={() => pickFilter('all')}>All</button>
            <button type="button" class="aim-chip" data-kind="enemy" class:active={hasData && filter === 'enemy'} onclick={() => pickFilter('enemy')}>Enemy</button>
            <button type="button" class="aim-chip" data-kind="team" class:active={hasData && filter === 'team'} style:display={result && teamless ? 'none' : null} onclick={() => pickFilter('team')}>Team</button>
            <button type="button" class="aim-chip" data-kind="self" class:active={hasData && filter === 'self'} onclick={() => pickFilter('self')}>Self</button>
        </div>
    </div>

    <div data-panel="accuracy" class="panel" id="aim-accuracy-panel" style:display={hide ? 'none' : null}>
        <h3>Accuracy &amp; effectiveness</h3>
        <div id="aim-provenance" class={provenanceClass(note)} style:display={note ? null : 'none'}>{@html note?.html ?? ''}</div>
        <details class="panel-info">
            <summary>More info</summary>
            <div class="aim-desc">
                One table per weapon; rows are players (team-coloured, click a
                column to sort). <strong>SG/SSG</strong> show pellets hit / fired
                and how many fires landed all pellets (full), some (partial) or
                none (miss); <strong>RL/GL</strong> show direct vs splash vs
                missed; <strong>LG</strong> splits its misses into miss (an aim
                error — no enemy on the beam's line), blocked (the beam would
                have hit an enemy in range but an object stopped it short) and
                far (the enemy was on the beam's line but beyond its ~600u
                reach). Every count also has
                a <strong>share-of-fires %</strong> column — the counts scale
                with how much a player fired, so the percentages are what
                compare across players. Pellet and direct figures match the
                server's authoritative stats. The <strong>Victims</strong>
                filter above slices every hit figure by who the shots hit:
                <strong>Enemy</strong> (opponents only), <strong>Team</strong>
                (teammates, excluding the shooter), <strong>Self</strong>
                (RL/GL self-splash — rocket jumps) or <strong>All</strong>
                (everything; the view that matches the server's stats). A
                fire that hit both an enemy and a teammate counts in both.
            </div>
        </details>
        <div id="aim-weapon-tables" use:weaponBlocks={tables}></div>
    </div>

    <div data-panel="crosshair" class="panel" id="aim-heatmap-panel" style:display={hide ? 'none' : null}>
        <h3>Crosshair placement <span class="aim-sel-note">— selected player</span></h3>
        <div class="aim-controls">
            <span class="aim-controls-label">Player:</span>
            <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_static_element_interactions: the legacy strip — focus it and use ← → to step players -->
            <div id="aim-player-chips" class="aim-chips" tabindex="0" bind:this={chipBox} onkeydown={chipKey}
                 title="Click a player, or focus and use ← → to step between players">
                {#each chips as chip}
                    <button type="button" class="aim-chip" data-player={chip.player} style:border-bottom-color={chip.color || null}
                        class:active={chip.player === player} onclick={() => { pickPlayer(chip.player); chipBox.focus(); }}>{chip.player}</button>
                {/each}
            </div>
            <span id="aim-mode" class="aim-mode">{mode}</span>
        </div>
        <details class="panel-info">
            <summary>More info</summary>
            <div class="aim-desc">
                Where the enemy sat relative to the crosshair at each fire,
                in <strong>Quake units at the target's range</strong> — both
                axes share the same scale, and <strong>the solid box is
                the player's collision box, true to shape (32 wide × 56
                tall)</strong>; the dashed box is its corner-on silhouette
                (viewed diagonally the axis-aligned box reads ~1.4× wider).
                The image is the <strong>smoothed
                density of shots</strong> (colorbar = shots per bin); a tight
                bright spot at the center is good placement. Hover for exact
                shot/hit counts. Below each image: yaw and pitch histograms
                of the same fires (shaded band = within the hull edge, rule
                = dead center; the outermost bins also collect shots beyond
                the plotted range). LG adds a ramp histogram: hit % by time
                since the shaft opened, hover for the counts. The Victims
                filter applies here too: Team shows only confirmed teammate
                hits (misses always attribute to enemies, so they only
                appear in Enemy/All), and Self shows nothing — hitscan
                cannot self-hit.
            </div>
        </details>

        <div class="aim-heatmap-row">
            {#each WEAPONS as w}
                {@const pl = plots[w]}
                <div class="aim-heatmap-block">
                    {#if w === 'lg'}
                        <h4>LG</h4>
                    {:else}
                        <h4>SG <span class="aim-sel-note">— pellets scatter, so the cloud is wider than the aim</span></h4>
                    {/if}
                    <canvas class="aim-density" id="aim-heatmap-{w}-canvas" bind:this={canvases[w]}
                        style:display={hasData && !pl.shown ? 'none' : null} onmousemove={(e) => onHover(e, w)}></canvas>
                    <div class="aim-hist-row" id="aim-heatmap-{w}-hists" style="display:none" style:display={pl.shown ? null : 'none'}>
                        {#key gen}
                            {#each pl.hists as h}<AimHist {h} />{/each}
                        {/key}
                    </div>
                    <div id="aim-heatmap-{w}-nodata" class="aim-nodata" style="display:none" style:display={hasData && !pl.shown ? null : 'none'}>{pl.nodata}</div>
                </div>
            {/each}
        </div>
    </div>
</div>
