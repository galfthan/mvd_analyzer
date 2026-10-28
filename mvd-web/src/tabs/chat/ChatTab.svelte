<script>
    // The Chat tab: frags and say / say_team lines on one scrolling time
    // track (Kills, then a say column per side — or one "Chat" column for
    // everybody in an individual field), with the current time as a line
    // that the viewport follows. app.js still toggles the `active` class
    // like on every other .tab-content (onTabActivated).
    //
    // Nothing runs while the tab is hidden. The columns are rebuilt — once,
    // on the next frame the tab shows — when the demo, the teams or the
    // "Hide team chat" filter changed; a clock tick only moves the line and
    // the scroll (the page's playback and the Player engine tick at
    // display rate).
    import { onMount } from 'svelte';
    import { clock, demo, teams, activeTab, colorOf } from '../../state/index.js';
    import {
        bucketChat, chatDuration, chatEvents, chatTitles, contentHeight, axisTicks,
        timeLineTop, scrollTarget, stackRow, rowBottom, escapeHtml, formatQuakeMessage,
    } from '../../lib/chat.js';

    // "Hide team chat" — drops say_team lines from the say columns so the
    // public banter (and the taunts a spectator would have heard) reads on
    // its own. Off by default and per demo; the Result keeps every message
    // either way.
    let hideTeam = $state(false);
    // { height, ticks } of the built track; null when there is nothing to
    // show (no messages, fewer than two sides).
    let track = $state(null);
    let lineTop = $state(0);
    let viewport = $state();
    let killCol = $state();
    let aCol = $state();
    let bCol = $state();

    const titles = $derived(chatTitles($teams.order, $teams.perPlayer));

    let dirty = true;
    let queued = false;
    let userScrolling = false;
    let scrollTimer = null;
    let programmatic = false; // our scrollTop writes, told apart from the user's

    function schedule() {
        if (queued || activeTab.get() !== 'chat') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (activeTab.get() !== 'chat') return;
            if (dirty) build();
            follow(clock.t);
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function build() {
        dirty = false;
        const result = demo.get()?.result;
        const t = teams.get();
        const duration = chatDuration(result);
        const events = chatEvents(result);
        for (const col of [killCol, aCol, bCol]) col.replaceChildren();
        if (!events || t.order.length < 2) {
            track = null;
            return;
        }
        const height = contentHeight(duration);
        const single = t.perPlayer;
        const { kills, a, b } = bucketChat(events, t.order, duration, { single, hideTeam });
        fillColumn(killCol, kills, height);
        fillColumn(aCol, a, height, single && ((name) => colorOf(t, name, true)));
        fillColumn(bCol, b, height);
        track = { height, ticks: axisTicks(duration) };
    }

    // fillColumn lays the rows out top to bottom. Built by hand rather than
    // from the template because each row's place depends on the measured
    // height of the one above: the inner track is attached before the rows
    // go in so each row's rendered height is measurable —
    // `.chat-time-marker-msg` wraps (white-space: normal), and a wrapped row
    // is taller than CHAT_ITEM_HEIGHT — stacking the next event at the
    // constant would draw it over the second line. The constant stays the
    // floor. `speakerColor` (the individual field) prefixes each row with
    // the speaker's name in their canonical colour — with two sides the
    // column heading said who was talking, with eight players nothing else
    // does.
    function fillColumn(container, events, height, speakerColor = null) {
        const inner = document.createElement('div');
        inner.style.position = 'relative';
        inner.style.height = `${height}px`;
        container.appendChild(inner);

        let lastBottom = -Infinity;
        for (const event of events) {
            const { top, displaced } = stackRow(event.time, lastBottom);
            const marker = document.createElement('div');
            marker.className = 'chat-time-marker' + (displaced ? ' chat-displaced' : '');
            marker.style.top = `${top}px`;
            const prefix = displaced ? '<span class="chat-displaced-dots">...</span>' : '';
            const speaker = speakerColor && event.player
                ? `<span class="chat-speaker" style="color: ${speakerColor(event.player)}">${escapeHtml(event.player)}</span>`
                : '';
            marker.innerHTML = `${prefix}${speaker}<span class="chat-time-marker-msg ${event.type}">${formatQuakeMessage(event.message)}</span>`;
            inner.appendChild(marker);
            lastBottom = rowBottom(top, marker.offsetHeight);
        }
    }

    function follow(t) {
        lineTop = timeLineTop(t);
        if (userScrolling || !viewport) return;
        programmatic = true;
        viewport.scrollTop = scrollTarget(t, viewport.clientHeight);
        // Reset after the browser has dispatched the scroll event.
        requestAnimationFrame(() => { programmatic = false; });
    }

    // A scroll by the user holds auto-scroll off for 2 s.
    function onScroll() {
        if (programmatic) return;
        userScrolling = true;
        clearTimeout(scrollTimer);
        scrollTimer = setTimeout(() => { userScrolling = false; }, 2000);
    }

    function onHideTeam() {
        invalidate();
    }

    onMount(() => {
        let first = true;
        const offs = [
            demo.subscribe(() => {
                if (first) return;
                hideTeam = false;
                userScrolling = false;
                invalidate();
            }),
            teams.subscribe(invalidate),
            clock.subscribe(schedule),
            activeTab.subscribe(schedule),
        ];
        first = false;
        return () => {
            offs.forEach((off) => off());
            clearTimeout(scrollTimer);
        };
    });
</script>

<div id="tab-chat" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel chat-panel-outer">
        <div class="chat-toolbar">
            <label><input type="checkbox" id="chat-hide-teamsay" bind:checked={hideTeam} onchange={onHideTeam}> Hide team chat</label>
        </div>
        <!-- Column headers (outside scroll) -->
        <div class="chat-header-row">
            <!-- A blank heading, hidden by CSS, keeps the axis column as tall as the others. -->
            <div class="chat-time-axis-col"><h3>{'\u00a0'}</h3></div>
            <div class="chat-columns-wrapper">
                <div class="chat-column-header"><h3>Kills</h3></div>
                <div class="chat-column-header"><h3 id="team-a-chat-title">{titles[0]}</h3></div>
                <div class="chat-column-header" id="team-b-chat-header"><h3 id="team-b-chat-title">{titles[1]}</h3></div>
            </div>
        </div>
        <!-- Single scroll viewport for all columns -->
        <div class="chat-scroll-viewport" id="chat-scroll-viewport" bind:this={viewport} onscroll={onScroll}>
            <div class="chat-scroll-inner">
                <div class="chat-time-axis-col">
                    <div class="chat-time-axis" id="chat-time-axis">
                        {#if track}
                            <div style:position="relative" style:height="{track.height}px">
                                {#each track.ticks as tick (tick.top)}
                                    <div class="chat-tick" style:top="{tick.top}px">{tick.label}</div>
                                {/each}
                            </div>
                        {/if}
                    </div>
                </div>
                <div class="chat-columns-wrapper">
                    <div class="chat-timeline-scroll" id="kill-messages" bind:this={killCol}></div>
                    <div class="chat-timeline-scroll" id="team-a-messages" bind:this={aCol}></div>
                    <div class="chat-timeline-scroll" id="team-b-messages" bind:this={bCol}></div>
                </div>
                {#if track}
                    <div class="chat-current-time-line" id="chat-current-time-line" style:top="{lineTop}px"></div>
                {/if}
            </div>
        </div>
    </div>
</div>
