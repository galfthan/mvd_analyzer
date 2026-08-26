package analyzer

import (
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// Clock is the match-relative time base every producer converts to at
// Finalize. It is produced once by ClockAnalyzer (a CoreProducer with no
// dependencies, so scheduled early) from the same MatchTimingDetector state
// the timeline runs, plus the pause and wall-clock-anchor inputs.
//
// Publishing it on CoreOutputs replaces the old whole-Result
// normalizeMatchRelativeTimes rebase: instead of stamping every timestamp on
// the demo clock and shifting the assembled Result afterwards, each producer
// stamps match-relative directly by subtracting MatchStartMs (via ToMatch) as
// it emits — "born correct". The old post-hoc rebase was the field-by-field
// enumeration that F1 (killEvents left on the demo clock) fell through; a
// producer that forgets to convert is now a local, testable omission rather
// than a missed entry in one giant function.
type Clock struct {
	// MatchStartMs is the demo-clock ms at which the match started
	// (the detector's StartTime). 0 when no match start was
	// detected — in which case ToMatch is the identity and no producer
	// shifts, exactly as the old rebase returned early on matchStart<=0.
	MatchStartMs int32

	// MatchEndMs is the effective match end on the demo clock: the
	// detector's explicit end, or the latest in-match position sample when
	// the demo was cut before intermission (F13). It mirrors the value the
	// timeline computes for Streams.Global.MatchEnd; the timeline still owns
	// that field.
	MatchEndMs int32

	// DemoOffsetMs is ms from demo open to match start (== MatchStartMs when
	// a match was detected, else 0). It is what Streams.Global.DemoOffset
	// records so a consumer can map match time back to demo time.
	DemoOffsetMs int32

	// DemoStartUnixMs / DemoStartAccuracyMs are the demo-open wall-clock
	// anchor: 1 ms from the mvdhidden 0x000B block, else 1000 ms from the
	// whole-second serverinfo `epoch` cvar, else 0/0. This folds in the old
	// deriveDemoStartAnchor post-processor's fallback. DemoStartSource names
	// which of the two it came from.
	DemoStartUnixMs     int64
	DemoStartAccuracyMs int32
	DemoStartSource     string

	// DateMarkers are the date stamps the wire's broadcast prints carried
	// (`matchdate:` / `matchkey:`), in the order seen, each with the demo-clock
	// time of its print. The wall-clock node (wallclock.go) resolves them —
	// together with the ktxstats date and the serverinfo version keys — into
	// the graded match-start anchor; the clock only collects.
	DateMarkers []WallClockMarker

	// Pauses are the coalesced game pauses with demo-relative AtMs. The
	// timeline shifts them to match time when it writes Streams.Global.Pauses.
	Pauses []TimelinePause
}

// ToMatch converts a demo-clock timestamp (ms) to match-relative ms. When no
// match start was detected (MatchStartMs <= 0) it is the identity, matching
// the old rebase's early return — nothing shifts on a demo with no match.
// Nil-safe so unit tests that never wire a clock resolve to demo time.
func (c *Clock) ToMatch(t int32) int32 {
	if c == nil || c.MatchStartMs <= 0 {
		return t
	}
	return t - c.MatchStartMs
}

// ClockAnalyzer is the CoreProducer node that produces the Clock. It owns a
// MatchTimingDetector (the same state machine the timeline gates on), the
// pause-sample collection, the mvdhidden 0x000B demo-start anchor, and the
// serverinfo `epoch` fallback — every input the match-relative epoch and the
// demo-open wall clock need. It writes nothing to Result directly; consumers
// read the published Clock from CoreOutputs at their Finalize.
type ClockAnalyzer struct {
	timing              MatchTimingDetector
	demoStartUnixMs     int64
	demoStartFromHidden bool
	epoch               string // last-seen serverinfo `epoch` value
	rawPauses           []pauseSample
	maxInMatchPosMs     int32 // latest in-match position sample (match-end fallback)
	dateMarkers         []WallClockMarker
	seenMarkers         map[string]struct{} // (source, raw) dedup for repeated prints
}

// NewClockAnalyzer creates the clock analyzer.
func NewClockAnalyzer() *ClockAnalyzer { return &ClockAnalyzer{} }

func (a *ClockAnalyzer) Name() string { return "clock" }

func (a *ClockAnalyzer) Init(ctx *Context) error { return nil }

func (a *ClockAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.MatchStartEvent:
		a.timing.OnMatchStart(e)
	case *events.PrintEvent:
		a.timing.OnPrint(e)
		// The date markers ride level-2 BROADCAST prints, the same level the
		// obituary log uses — collecting them here (rather than in the
		// messages analyser, which routes level<=2 into the obituary parser
		// only) keeps both readers on the raw print stream with neither
		// filtering the other's lines.
		//
		// The broadcast gate matters: a dem_single print is addressed to one
		// player's client, so it states that client's view, not the server's,
		// and must never anchor the global clock. Measured on 315 archive
		// demos carrying a marker (180 matchdate, 135 matchkey), every single
		// one arrived as level 2 with TargetPlayerNum -1, so the gate costs no
		// coverage — KTX and kmod both emit these through bprint.
		if e.Level == events.PrintHigh && e.TargetPlayerNum < 0 {
			a.collectDateMarker(e)
		}
	case *events.IntermissionEvent:
		a.timing.OnIntermission(e.TimeMs)
	case *events.PlayerPositionEvent:
		// Mirror the timeline's stream gate (timeline.go handlePositionUpdate):
		// only in-match samples feed the match-end fallback, keyed on the same
		// TimeMs the position stream records. The global max equals the latest
		// per-player posT the timeline's fallback scans.
		if a.timing.Started && !a.timing.Ended && e.TimeMs > a.maxInMatchPosMs {
			a.maxInMatchPosMs = e.TimeMs
		}
	case *events.DemoStartTimestampEvent:
		// mvdhidden 0x000B: server wall-clock (Unix ms) at demo open. Keep the
		// first plausible one; some 2026 demos carry a non-timestamp payload
		// (values like 61 / 11701) that must fall back to `epoch` instead.
		if !a.demoStartFromHidden && plausibleDemoStartUnixMs(e.UnixMs) {
			a.demoStartUnixMs = e.UnixMs
			a.demoStartFromHidden = true
		}
	case *events.PausedDurationEvent:
		// mvdhidden 0x000A: one real-ms sample per idle frame while the game
		// clock is paused. Collect raw; coalesced per-pause at Finalize.
		a.rawPauses = append(a.rawPauses, pauseSample{Time: e.TimeMs, DurationMs: e.DurationMs})
	case *events.StuffTextEvent:
		// The bulk cvar dump `fullserverinfo "\...\epoch\<secs>\..."` carries
		// the whole-second wall-clock fallback. Same source metadata parses.
		if strings.HasPrefix(e.Command, "fullserverinfo ") {
			if v, ok := parseInfoString(e.Command)["epoch"]; ok {
				a.epoch = v
			}
		}
	case *events.ServerInfoEvent:
		// Mid-game single-key updates — last write wins, like metadata.
		if e.Key == "epoch" {
			a.epoch = e.Value
		}
	}
	return nil
}

// collectDateMarker records a `matchdate:` / `matchkey:` print. Identical
// (source, stamp) pairs are recorded once: a bprint reaches every client as one
// broadcast copy, but a demo cut from a longer recording can replay the same
// line, and a repeat carries no information the first copy did not. Distinct
// stamps are all kept — a demo holding two matches legitimately holds two
// matchdate prints.
func (a *ClockAnalyzer) collectDateMarker(e *events.PrintEvent) {
	m, ok := parseDateMarkerPrint(e.Message, e.TimeMs)
	if !ok {
		return
	}
	key := m.Source + "\x00" + m.Raw
	if _, seen := a.seenMarkers[key]; seen {
		return
	}
	if a.seenMarkers == nil {
		a.seenMarkers = make(map[string]struct{}, 2)
	}
	a.seenMarkers[key] = struct{}{}
	a.dateMarkers = append(a.dateMarkers, m)
}

// Finalize is a no-op: the clock writes nothing to Result. The demo-start
// anchor, match window, and pauses are written by the timeline (which owns
// Streams.Global) from the published Clock.
func (a *ClockAnalyzer) Finalize(result *Result) error { return nil }

// PopulateCore publishes the Clock. Every node that stamps match-relative
// timestamps declares a `requires` edge on "clock", so the DAG schedules this
// before them and every such producer sees a complete Clock.
func (a *ClockAnalyzer) PopulateCore(co *CoreOutputs) {
	// Effective match end: the explicit detector end, or the latest in-match
	// position sample when the demo was cut before intermission (F13). This
	// mirrors the timeline's own computation (timeline_finalize.go), sharing
	// MatchTimingDetector.EffectiveEndMs.
	matchStartMs := a.timing.StartTime

	clk := &Clock{
		MatchStartMs: matchStartMs,
		MatchEndMs:   a.timing.EffectiveEndMs(a.maxInMatchPosMs),
		Pauses:       coalescePauses(a.rawPauses),
		DateMarkers:  a.dateMarkers,
	}
	if matchStartMs > 0 {
		clk.DemoOffsetMs = matchStartMs
	}

	// Wall-clock anchor: the millisecond-accurate 0x000B block wins; otherwise
	// the whole-second `epoch` cvar (the fallback the old deriveDemoStartAnchor
	// owned).
	if a.demoStartFromHidden {
		clk.DemoStartUnixMs = a.demoStartUnixMs
		clk.DemoStartAccuracyMs = 1
		clk.DemoStartSource = wallSourceHidden
	} else if a.epoch != "" {
		if secs, err := strconv.ParseInt(strings.TrimSpace(a.epoch), 10, 64); err == nil && plausibleDemoStartUnixMs(secs*1000) {
			clk.DemoStartUnixMs = secs * 1000
			clk.DemoStartAccuracyMs = 1000
			clk.DemoStartSource = wallSourceEpoch
		}
	}

	co.Clock = clk
}
