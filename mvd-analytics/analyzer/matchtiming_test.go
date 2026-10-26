package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// TestMatchTimingDetector_OnMatchStartLatchesOnce pins the idempotence the
// START half rests on. Layer 1 emits exactly one MatchStartEvent per stream
// (mvd-reader/parser/matchstart.go raises it behind a once-per-stream
// latch), so this detector never has to CHOOSE between two starts on a demo
// read front to back — but it is a plain struct that ten analyzers embed
// and feed by hand, and the match origin it holds is what every stream,
// bucket and interval is rebased onto. The guard is the contract that an
// embedder handing it a second start (a replayed or concatenated event
// stream, a test harness driving the analyzer directly) cannot move that
// origin out from under the samples already recorded against it.
func TestMatchTimingDetector_OnMatchStartLatchesOnce(t *testing.T) {
	var d MatchTimingDetector

	d.OnMatchStart(&events.MatchStartEvent{TimeMs: 810, Source: "matchdate"})
	d.OnMatchStart(&events.MatchStartEvent{TimeMs: 900810, Source: "status"})

	if !d.Started {
		t.Fatal("Started = false after a match start")
	}
	if d.StartTime != 810 {
		t.Errorf("StartTime = %d, want 810 — the second start moved the origin", d.StartTime)
	}
	if d.StartSource != "matchdate" {
		t.Errorf("StartSource = %q, want %q — the second start renamed the signal", d.StartSource, "matchdate")
	}
}

// TestMatchTimingDetector_EndBeforeStartIgnored pins the "ended only after
// started" invariant across both end signals. It is not decoration: Ended
// is a one-way latch, so an end phrase accepted before the match began
// would leave every downstream recording path (positions, occupancy, frag
// updates) permanently shut for the rest of the demo. The pre-match window
// is exactly where such a line turns up — the previous match's "The match
// is over" is still on the wire ahead of this one's start on a server that
// records two matches into one file, and KTX's own intermission from that
// previous match arrives the same way.
func TestMatchTimingDetector_EndBeforeStartIgnored(t *testing.T) {
	t.Run("print", func(t *testing.T) {
		var d MatchTimingDetector
		d.OnPrint(&events.PrintEvent{Level: events.PrintHigh, Message: "The match is over\n", TimeMs: 4000})
		if d.Ended || d.EndTime != 0 {
			t.Fatalf("an end print before the start ended the match: Ended=%v EndTime=%d", d.Ended, d.EndTime)
		}

		// The same line after the start is the real one.
		d.OnMatchStart(&events.MatchStartEvent{TimeMs: 10000, Source: "print"})
		d.OnPrint(&events.PrintEvent{Level: events.PrintHigh, Message: "The match is over\n", TimeMs: 610000})
		if !d.Ended || d.EndTime != 610000 {
			t.Fatalf("Ended=%v EndTime=%d, want true/610000", d.Ended, d.EndTime)
		}

		// And a later one does not move the end.
		d.OnPrint(&events.PrintEvent{Level: events.PrintHigh, Message: "The match is over\n", TimeMs: 620000})
		if d.EndTime != 610000 {
			t.Errorf("EndTime = %d, want 610000 — a second end print moved the boundary", d.EndTime)
		}
	})

	// KTX's per-point line on a hoony/blitz series (ktx/src/match.c:326)
	// must NOT end the match: every point but the last is followed by
	// another StartMatch on the same series, and the series closes at
	// svc_intermission. Archive 0543ac01… spans 10 points on that signal;
	// accepting this line would cut it to the first.
	t.Run("hoony point end is not a match end", func(t *testing.T) {
		var d MatchTimingDetector
		d.OnMatchStart(&events.MatchStartEvent{TimeMs: 10109, Source: "matchdate"})
		d.OnPrint(&events.PrintEvent{Level: events.PrintHigh, Message: "The point is over\n", TimeMs: 45698})
		if d.Ended {
			t.Fatalf("a point end ended the series at %d", d.EndTime)
		}
		d.OnIntermission(201606)
		if !d.Ended || d.EndTime != 201606 {
			t.Fatalf("Ended=%v EndTime=%d, want true/201606", d.Ended, d.EndTime)
		}
	})

	t.Run("intermission", func(t *testing.T) {
		var d MatchTimingDetector
		d.OnIntermission(4000)
		if d.Ended {
			t.Fatal("svc_intermission before the start ended the match")
		}
		d.OnMatchStart(&events.MatchStartEvent{TimeMs: 10000, Source: "matchdate"})
		d.OnIntermission(610000)
		if !d.Ended || d.EndTime != 610000 {
			t.Fatalf("Ended=%v EndTime=%d, want true/610000", d.Ended, d.EndTime)
		}
	})

	// Chat is refused at any time: a mid-match "gg game over" say line would
	// otherwise freeze every stream for the rest of the demo.
	t.Run("chat never ends a match", func(t *testing.T) {
		var d MatchTimingDetector
		d.OnMatchStart(&events.MatchStartEvent{TimeMs: 10000, Source: "print"})
		d.OnPrint(&events.PrintEvent{Level: events.PrintChat, Message: "dag: gg game over\n", TimeMs: 300000})
		if d.Ended {
			t.Fatal("a chat line ended the match")
		}
	})
}
