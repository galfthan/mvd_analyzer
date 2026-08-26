package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// mustMatchStartFromPrint runs the print-borne match-start detector and
// fails the test if emission errored. Shared with obituary_test.go, which
// exercises the same gate from the obituary side.
func mustMatchStartFromPrint(t *testing.T, p *Parser, level int, msg string) {
	t.Helper()
	if err := p.tryEmitMatchStartFromPrint(level, msg, 0); err != nil {
		t.Fatalf("tryEmitMatchStartFromPrint(%q): %v", msg, err)
	}
}

// collectMatchStarts wires a parser to a handler that records every
// MatchStartEvent it emits.
func collectMatchStarts(p *Parser, out *[]MatchStartEvent) {
	p.OnEvent(func(e Event) error {
		if ms, ok := e.(*MatchStartEvent); ok {
			*out = append(*out, *ms)
		}
		return nil
	})
}

// TestMatchStartEvent_Sources pins each of the four Layer-1 signals in
// isolation: each raises the event exactly once, with its own Source name
// and the wire time of the signal that raised it.
func TestMatchStartEvent_Sources(t *testing.T) {
	cases := []struct {
		name   string
		drive  func(t *testing.T, p *Parser)
		source string
		timeMs int32
	}{
		{
			name: "ktx directive",
			drive: func(t *testing.T, p *Parser) {
				if err := p.tryEmitMatchStartFromStuffText("//ktx matchstart\n", 610); err != nil {
					t.Fatal(err)
				}
			},
			source: MatchStartSourceKtxDirective,
			timeMs: 610,
		},
		{
			name: "print",
			drive: func(t *testing.T, p *Parser) {
				if err := p.tryEmitMatchStartFromPrint(mvd.PrintHigh, "The match has begun!\n", 10104); err != nil {
					t.Fatal(err)
				}
			},
			source: MatchStartSourcePrint,
			timeMs: 10104,
		},
		{
			name: "matchdate",
			drive: func(t *testing.T, p *Parser) {
				if err := p.tryEmitMatchStartFromPrint(mvd.PrintHigh, "matchdate: 2026-01-16 20:57:38 UTC\n", 810); err != nil {
					t.Fatal(err)
				}
			},
			source: MatchStartSourceMatchDate,
			timeMs: 810,
		},
		{
			name: "status transition",
			drive: func(t *testing.T, p *Parser) {
				if err := p.observeFullServerInfoStatus(`fullserverinfo "\map\dm2\status\Countdown"`, 0); err != nil {
					t.Fatal(err)
				}
				if err := p.observeServerInfoStatus("6 min left", 843, true); err != nil {
					t.Fatal(err)
				}
				// The once-a-minute countdown ticks that follow must not
				// re-raise it.
				if err := p.observeServerInfoStatus("5 min left", 60843, true); err != nil {
					t.Fatal(err)
				}
			},
			source: MatchStartSourceStatus,
			timeMs: 843,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			var got []MatchStartEvent
			collectMatchStarts(p, &got)
			tc.drive(t, p)
			if len(got) != 1 {
				t.Fatalf("emitted %d MatchStartEvents, want 1: %+v", len(got), got)
			}
			if got[0].Source != tc.source {
				t.Errorf("Source = %q, want %q", got[0].Source, tc.source)
			}
			if got[0].TimeMs != tc.timeMs {
				t.Errorf("TimeMs = %d, want %d", got[0].TimeMs, tc.timeMs)
			}
			if !p.matchStarted {
				t.Error("matchStarted latch did not flip with the event")
			}
		})
	}
}

// TestMatchStartEvent_FirstSignalWins pins the once-per-stream contract
// against the modern-KTX frame, where three signals land together: KTX
// prints `matchdate:` (match.c:1291) before it updates `status`
// (match.c:1337) and before the `//ktx matchstart` stuffcmd (match.c:1372),
// so the print is the one that names the event and the two that follow are
// no-ops.
func TestMatchStartEvent_FirstSignalWins(t *testing.T) {
	p := NewParser(nil)
	var got []MatchStartEvent
	collectMatchStarts(p, &got)

	if err := p.observeFullServerInfoStatus(`fullserverinfo "\status\Countdown\map\dm2"`, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.tryEmitMatchStartFromPrint(mvd.PrintMedium, "matchdate: 2026-01-16 20:57:38 UTC\n", 810); err != nil {
		t.Fatal(err)
	}
	if err := p.tryEmitMatchStartFromStuffText("//ktx matchstart\n", 810); err != nil {
		t.Fatal(err)
	}
	if err := p.observeServerInfoStatus("6 min left", 810, true); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d MatchStartEvents, want 1: %+v", len(got), got)
	}
	if got[0].Source != MatchStartSourceMatchDate {
		t.Errorf("Source = %q, want %q", got[0].Source, MatchStartSourceMatchDate)
	}
}

// TestMatchStartEvent_MatchDateIsLineInitial: the stamp opens its own
// bprint line, so only a line that STARTS with it is the marker. A chat
// line quoting it is not.
func TestMatchStartEvent_MatchDateIsLineInitial(t *testing.T) {
	p := NewParser(nil)
	var got []MatchStartEvent
	collectMatchStarts(p, &got)
	for _, msg := range []string{
		"gg matchdate: 2026-01-16 20:57:38 UTC\n",
		"see the matchdate: line\n",
	} {
		if err := p.tryEmitMatchStartFromPrint(mvd.PrintMedium, msg, 500); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.tryEmitMatchStartFromPrint(mvd.PrintChat, "matchdate: 2026-01-16 20:57:38 UTC\n", 500); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("emitted %d MatchStartEvents, want 0: %+v", len(got), got)
	}
}

// TestMatchStartEvent_StatusRunningAtOpen: a recording that opens with the
// clock already running is a MID-MATCH recording. Its once-a-minute ticks
// are not a transition, so nothing raises a match start and the no-match
// marker keeps its midMatchRecording verdict.
func TestMatchStartEvent_StatusRunningAtOpen(t *testing.T) {
	p := NewParser(nil)
	var got []MatchStartEvent
	collectMatchStarts(p, &got)
	if err := p.observeFullServerInfoStatus(`fullserverinfo "\status\4 min left\map\dm3"`, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.observeServerInfoStatus("3 min left", 12000, true); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("emitted %d MatchStartEvents, want 0: %+v", len(got), got)
	}
}

// TestStatusNamesRunningGame pins the two remaining-time spellings the
// archive carries against every idle / pre-match value beside them. The
// distinction is load-bearing three times over: it decides the `status`
// match-start signal above, and downstream in the no-match marker it is
// what separates midMatchRecording from the rest and matchStartUnannounced
// from noMatchDeclared.
//
// "20 min left" is the regression case: the digit sits at the FRONT of the
// reading, not against the " left" suffix, so a test that looks at the
// character before the suffix reads KTX's own format as idle.
//
// The idle list carries the whole non-reading vocabulary the census found
// (see StatusNamesRunningGame for the counts) plus the near-misses a looser
// "ends in ` left`" test would accept: mods DO write their own words into
// this key ("Round 1/15", "Game Ended"), so a value that merely ends in
// " left" is not evidence of a clock.
func TestStatusNamesRunningGame(t *testing.T) {
	running := []string{
		"20 min left",  // ktx/src/match.c:596,723,1330 — the common case
		"1 min left",   //
		"0 min left",   // the last tick before the match ends
		"19:35 left",   // the CTF mod's mm:ss reading
		"00:01 left",   // its zero-padded low end
		"14:59 left",   //
		"120 min left", // no upper bound on the reading
	}
	idle := []string{
		"",              // no status key at all (pre-KTX servers, foreign mods)
		"Standby",       // ktx/src/world.c:543
		"Countdown",     // ktx/src/match.c:2475 — about to start is not started
		"Forcestart",    // ktx/src/admin.c:693
		"Normal",        // observed on gamedir fortress
		"Game Ended",    // the CTF mod's terminal status
		"Round 1/15",    // gamedir arena, a round counter — not a clock
		"Round 11/15",   //
		"left",          // suffix with nothing in front of it
		" left",         //
		"min left",      // a reading with no number is not a reading
		"2 rounds left", // a countable that is not a remaining time
		"3 players left",
		"2 cool 4 u left", // free text that happens to end in the suffix
		"20 min",          // a reading with no suffix
		"19:5 left",       // seconds are always two digits
		"1:02:30 left",    // no h:mm:ss spelling occurs
	}
	for _, v := range running {
		if !StatusNamesRunningGame(v) {
			t.Errorf("StatusNamesRunningGame(%q) = false, want true", v)
		}
	}
	for _, v := range idle {
		if StatusNamesRunningGame(v) {
			t.Errorf("StatusNamesRunningGame(%q) = true, want false", v)
		}
	}
}
