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

// TestMatchStartEvent_Sources pins the two PRINT-borne signals in isolation:
// each raises the event exactly once, with its own Source name and the wire
// time of the signal that raised it. The directive and the status transition
// are pinned from the wire instead (TestMatchStartFromWire_*), which is the
// stronger statement — they are the two whose trigger the parser has to
// recognise inside a real message.
func TestMatchStartEvent_Sources(t *testing.T) {
	cases := []struct {
		name   string
		drive  func(t *testing.T, p *Parser)
		source string
		timeMs int32
	}{
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

// serverInfoPayload builds a svc_serverinfo network-message payload: the
// command byte followed by the two null-terminated strings
// parseNetworkMessage reads for a single-key update (parser.go:512-521).
func serverInfoPayload(key, value string) []byte {
	b := []byte{mvd.SvcServerInfo}
	b = append(b, []byte(key)...)
	b = append(b, 0)
	b = append(b, []byte(value)...)
	return append(b, 0)
}

// feedWire pushes one broadcast demo message through the real
// parseNetworkMessage dispatch, the way source/mvd does — no helper method
// called directly.
func feedWire(t *testing.T, p *Parser, payload []byte, timeMs int32) {
	t.Helper()
	msg := &mvd.DemoMessage{
		Header:  mvd.MessageHeader{MessageType: mvd.DemAll},
		Payload: payload,
		TimeMs:  timeMs,
	}
	if err := p.parseNetworkMessage(msg); err != nil {
		t.Fatalf("parseNetworkMessage: %v", err)
	}
}

// wireTrace records the ORDER events leave the parser in, which is the
// half of the contract a helper-method test cannot see: the MatchStartEvent
// must arrive AFTER the wire event that raised it, so a handler reading
// that trigger still observes the pre-start state.
type wireTrace struct {
	kinds  []string
	starts []MatchStartEvent
}

func newWireTrace(p *Parser) *wireTrace {
	tr := &wireTrace{}
	p.OnEvent(func(e Event) error {
		switch ev := e.(type) {
		case *StuffTextEvent:
			tr.kinds = append(tr.kinds, "stufftext")
		case *ServerInfoEvent:
			tr.kinds = append(tr.kinds, "serverinfo")
		case *PrintEvent:
			tr.kinds = append(tr.kinds, "print")
		case *MatchStartEvent:
			tr.kinds = append(tr.kinds, "matchstart")
			tr.starts = append(tr.starts, *ev)
		}
		return nil
	})
	return tr
}

// wantOneStart asserts the stream raised exactly one match start, from the
// named source, at the named time, and that the whole event sequence so far
// is exactly wantKinds — which is how the ordering half is pinned: the
// MatchStartEvent must be the LAST event out, behind the wire event that
// carried the signal, and no print may appear in a stream that has none.
func (tr *wireTrace) wantOneStart(t *testing.T, source string, timeMs int32, wantKinds ...string) {
	t.Helper()
	if len(tr.starts) != 1 {
		t.Fatalf("emitted %d MatchStartEvents, want 1: %+v (trace %v)", len(tr.starts), tr.starts, tr.kinds)
	}
	if tr.starts[0].Source != source {
		t.Errorf("Source = %q, want %q", tr.starts[0].Source, source)
	}
	if tr.starts[0].TimeMs != timeMs {
		t.Errorf("TimeMs = %d, want %d", tr.starts[0].TimeMs, timeMs)
	}
	if len(tr.kinds) != len(wantKinds) {
		t.Fatalf("event order %v, want %v", tr.kinds, wantKinds)
	}
	for i := range wantKinds {
		if tr.kinds[i] != wantKinds[i] {
			t.Fatalf("event order %v, want %v", tr.kinds, wantKinds)
		}
	}
}

// TestMatchStartFromWire_KtxDirective drives the `//ktx matchstart`
// stuffcmd (ktx/src/match.c:1372) through parseNetworkMessage on a stream
// that carries NO `matchdate:` stamp and no match-start print — the hoony /
// non-deathmatch shape where the directive is the only signal
// (match.c:1290 gates the stamp off). The event must come out of the
// stufftext dispatch itself, after the StuffTextEvent.
func TestMatchStartFromWire_KtxDirective(t *testing.T) {
	p := NewParser(nil)
	tr := newWireTrace(p)

	feedWire(t, p, stuffTextPayload(`fullserverinfo "\map\dm2\status\Standby\*gamedir\qw"`), 0)
	if len(tr.starts) != 0 {
		t.Fatalf("the opening dump raised a match start: %+v", tr.starts)
	}
	feedWire(t, p, stuffTextPayload("//ktx matchstart\n"), 610)

	tr.wantOneStart(t, MatchStartSourceKtxDirective, 610, "stufftext", "stufftext", "matchstart")
	if !p.matchStarted {
		t.Error("matchStarted latch did not flip with the event")
	}

	// A second directive — a demo spanning two matches — must not re-raise
	// it: the event is once per stream.
	feedWire(t, p, stuffTextPayload("//ktx matchstart\n"), 900610)
	if len(tr.starts) != 1 {
		t.Errorf("a second directive re-raised the start: %+v", tr.starts)
	}
}

// TestMatchStartFromWire_StatusTransition drives the svc_serverinfo
// `status` transition (ktx/src/match.c:1337) through parseNetworkMessage:
// an opening `fullserverinfo` dump saying `Standby`, then a single-key
// update carrying a running clock. Both observed clock spellings are
// exercised — the CTF mod's mm:ss (the shape of archive demo
// `2a2ed2e9ca…`, a qwe 2.40 CTF recording whose start is raised at 991 ms
// by exactly this path) and KTX's "%d min left".
func TestMatchStartFromWire_StatusTransition(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clock string
		next  string
	}{
		{"ctf mm:ss clock", "14:59 left", "13:59 left"},
		{"ktx minute clock", "6 min left", "5 min left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			tr := newWireTrace(p)

			feedWire(t, p, stuffTextPayload(`fullserverinfo "\map\dm6\status\Standby\*gamedir\ctf"`), 0)
			if len(tr.starts) != 0 {
				t.Fatalf("the opening dump raised a match start: %+v", tr.starts)
			}
			feedWire(t, p, serverInfoPayload("status", tc.clock), 991)

			tr.wantOneStart(t, MatchStartSourceStatus, 991, "stufftext", "serverinfo", "matchstart")
			if !p.matchStarted {
				t.Error("matchStarted latch did not flip with the event")
			}

			// The once-a-minute ticks that follow are the same running
			// state, not a new transition.
			feedWire(t, p, serverInfoPayload("status", tc.next), 60991)
			if len(tr.starts) != 1 {
				t.Errorf("a later clock tick re-raised the start: %+v", tr.starts)
			}
		})
	}
}

// TestMatchStartFromWire_RunningClockAtOpenDoesNotFire: a recording that
// OPENS with the clock already running is a mid-match recording. The
// opening dump states the server's state at the first frame rather than a
// transition inside the recording, so nothing may raise a match start —
// this is the wire-level half of the no-match marker's midMatchRecording
// verdict.
func TestMatchStartFromWire_RunningClockAtOpenDoesNotFire(t *testing.T) {
	p := NewParser(nil)
	tr := newWireTrace(p)

	feedWire(t, p, stuffTextPayload(`fullserverinfo "\map\dm3\status\14:59 left\*gamedir\ctf"`), 0)
	feedWire(t, p, serverInfoPayload("status", "13:59 left"), 60000)
	feedWire(t, p, serverInfoPayload("status", "12:59 left"), 120000)

	if len(tr.starts) != 0 {
		t.Fatalf("emitted %d MatchStartEvents on a mid-match recording, want 0: %+v", len(tr.starts), tr.starts)
	}
	if p.matchStarted {
		t.Error("matchStarted latch flipped without a transition")
	}
}
