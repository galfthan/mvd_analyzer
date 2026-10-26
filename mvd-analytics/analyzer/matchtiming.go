package analyzer

import (
	"strings"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// MatchTimingDetector is the canonical match-boundary state machine
// for analyzers that need to know whether the match is currently
// running. Every analyzer that previously maintained its own
// matchStarted / matchEnded flags + per-file keyword list now embeds
// one of these so the boundaries cannot drift apart.
//
// The START half is Layer 1's verdict: the parser emits exactly one
// events.MatchStartEvent per stream, at the first of the four wire
// signals it recognises (a match-start print, KTX's `matchdate:` stamp,
// the `//ktx matchstart` stuffcmd, or a serverinfo `status` transition
// into a running clock — see mvd-reader/parser/matchstart.go). This
// detector just latches it. Scanning prints here as well was how the
// two layers came to disagree on matchless FFA demos, where the parser's
// own gate and the analyzers both waited for a "The match has begun!"
// line KTX never prints.
//
// The END half is still analytics-only: the end phrases below gate no
// parser behaviour, and svc_intermission is read straight off the wire.
// Pattern coverage is the union of the lists previously hard-coded
// across match.go, timeline.go, backpacks.go, items.go, weapon_pickups.go,
// and metadata.go. Matches are case-insensitive — the previous match.go
// did ToLower while every other site compared raw, which produced
// "fight!" vs "Fight!" coverage gaps that depended on which server
// printed the line.
//
// The "ended only after started" invariant is preserved: end keywords
// are ignored until a start has been seen. This matches what 4 of the 5
// callers were already doing.
type MatchTimingDetector struct {
	Started   bool
	Ended     bool
	StartTime int32 // demo-clock ms of the match-start signal
	// StartSource names which Layer-1 signal raised the start —
	// "ktx-matchstart" | "print" | "matchdate" | "status". Published on
	// the result as streams.global.matchStartSignal. Empty when no start
	// was detected.
	StartSource string
	EndTime     int32 // demo-clock ms of the match-end print / intermission (0 if unseen)
}

// matchEndPatterns is the case-insensitive substring table behind the
// print half of the match end; svc_intermission is the other half
// (OnIntermission). One entry: KTX's "The match is over" (ktx/src/match.c:331,
// G_bprint at PRINT_HIGH), inherited verbatim from Kombat Teams
// (kteams/v2.07/SRC/MATCH.QC:139, v2.21/SRC/MATCH.QC:172). Five former
// entries — "match ended", "match complete", "game over", "timelimit hit",
// "fraglimit hit" — were removed after a sweep of all 50 964 archive demos
// (.reports/vocab-sweep-2026-08-29, probe S2) found no non-chat print
// carrying any of them except one E0 spectator NAMED "game over".
//
// KTX's other end line, "The point is over" (match.c:326), is deliberately
// NOT here. It is the per-POINT end of a hoony/blitz series: for every
// point but the last, EndMatch resets match_over and re-readies the
// players (match.c:426-447), StartMatch runs again and the same series
// goes on. The series itself closes at svc_intermission, which this
// detector already takes, so a hoony demo spans every point today (archive
// 0543ac01…: 10 points, 191 s, 10 frags, one `//demomark round-N` per
// point boundary). Ending the match at the first point end would cut
// that to point one. Modelling the points as rounds is the open work.
var matchEndPatterns = []string{
	"match is over",
}

// OnMatchStart latches Layer 1's match-start verdict. Idempotent: the
// parser emits the event once per stream, but a detector fed from a
// replayed or concatenated event stream must not move its origin.
func (d *MatchTimingDetector) OnMatchStart(e *events.MatchStartEvent) {
	if d.Started {
		return
	}
	d.Started = true
	d.StartTime = e.TimeMs
	d.StartSource = e.Source
}

// OnPrint feeds a print event into the detector's END half. Idempotent: a
// second matching end print is ignored, and end phrases before a start are
// ignored too.
func (d *MatchTimingDetector) OnPrint(e *events.PrintEvent) {
	// Only broadcast prints (bprint) end a match. KTX emits every
	// match-boundary line at PRINT_MEDIUM/PRINT_HIGH (level <= 2); PRINT_CHAT
	// (level 3) is player say/say_team, which must never flip Ended —
	// otherwise a mid-match "gg game over" chat line would freeze every
	// stream for the rest of the demo. (ktx/include/g_consts.h:
	// PRINT_MEDIUM=1 death, PRINT_CHAT=3.)
	if e.Level == events.PrintChat {
		return
	}
	if !d.Started || d.Ended {
		return
	}
	msg := strings.ToLower(e.Message)
	for _, p := range matchEndPatterns {
		if strings.Contains(msg, p) {
			d.Ended = true
			d.EndTime = e.TimeMs
			return
		}
	}
}

// OnIntermission marks the match as ended when the server fires
// svc_intermission. KTX emits this on timelimit/fraglimit hit even when
// there is no matching bprint string, so it is a more reliable end
// signal than print-keyword scanning alone.
func (d *MatchTimingDetector) OnIntermission(tMs int32) {
	if d.Started && !d.Ended {
		d.Ended = true
		d.EndTime = tMs
	}
}

// EffectiveEndMs returns the match end on the demo clock (ms): the detector's
// explicit EndTime, or the supplied fallback — the latest in-match position
// sample — when the demo was cut before intermission (F13). Shared by the
// clock and the timeline so both close intervals at the same instant.
func (d *MatchTimingDetector) EffectiveEndMs(fallbackMs int32) int32 {
	if !d.Ended {
		return fallbackMs
	}
	return d.EndTime
}
