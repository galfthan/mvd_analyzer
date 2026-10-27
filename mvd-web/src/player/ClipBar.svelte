<script>
    // In / Out marks on the player's clock (demo seconds) and "add clip":
    // In→Out from the current POV. Keys i / o / c while the tab is open
    // (keys typed into the engine's iframe stay there).
    import Plus from '@lucide/svelte/icons/plus';
    import { activeTab } from '../state/index.js';
    import { formatDuration, nameOfUserId } from '../lib/result.js';
    import { engine, currentRef } from './index.js';
    import { addClips } from './playlist-store.js';
    import { toast } from './toast.js';
    import { demo } from '../state/index.js';

    const CLIP_DEFAULT_S = 10;

    let markIn = $state(null);
    let markOut = $state(null);

    // A new demo drops the marks.
    $effect(() => {
        $demo;
        markIn = null;
        markOut = null;
    });

    const fmt = (v) => (v == null ? '–' : formatDuration(Math.max(0, v - engine.offset())));

    function addClip() {
        const ref = currentRef();
        if (!ref) return;
        const now = engine.demoNow();
        let from = markIn;
        let to = markOut;
        if (from == null && to == null) { from = now - CLIP_DEFAULT_S; to = now; }
        else if (to == null) to = Math.max(now, from + CLIP_DEFAULT_S);
        else if (from == null) from = to - CLIP_DEFAULT_S;
        from = Math.max(0, Math.floor(from));
        to = Math.ceil(to);
        if (to <= from) {
            toast('Out is before In — nothing added');
            return;
        }
        const track = engine.povTrack();
        const who = track === 'auto' ? '' : (nameOfUserId($demo?.result, track) || '');
        const label = `${who || 'Auto'} ${formatDuration(Math.max(0, from - ref.offset))}`;
        addClips([{ from, to, track, who, label, demo: ref }]);
        markIn = null;
        markOut = null;
        toast(`Added: ${label}`);
    }

    function onKey(e) {
        if ($activeTab !== 'player' || e.ctrlKey || e.metaKey || e.altKey) return;
        if (e.target.closest?.('input, textarea, select, [contenteditable]')) return;
        if (e.key === 'i') markIn = engine.demoNow();
        else if (e.key === 'o') markOut = engine.demoNow();
        else if (e.key === 'c') addClip();
        else return;
        e.preventDefault();
    }
</script>

<svelte:window onkeydown={onKey} />

<div class="player-clip-bar">
    <span class="pt-group">
        <button class="pt-btn" title="Mark the clip start at the player's time (key: i)"
            onclick={() => (markIn = engine.demoNow())}>In <span class="mark">{fmt(markIn)}</span></button>
        <button class="pt-btn" title="Mark the clip end at the player's time (key: o)"
            onclick={() => (markOut = engine.demoNow())}>Out <span class="mark">{fmt(markOut)}</span></button>
        <button class="pt-btn" title="Add In→Out from the current POV to the playlist (key: c). Without marks: the last 10 s."
            onclick={addClip}><Plus size={14} /> Add clip</button>
    </span>
    <span class="player-clip-hint">Hub links elsewhere: click plays here · shift-click adds to the playlist · ctrl/middle-click opens hub</span>
</div>

<style>
    .mark {
        font-family: monospace;
        color: #9ab;
    }
</style>
