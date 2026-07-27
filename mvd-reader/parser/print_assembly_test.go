package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// collectPrints registers a handler that records every assembled
// PrintEvent plus every DeathEvent / ItemPickupPrintEvent derived from
// one, so a test can assert on the line AND its side effects.
type printCollector struct {
	prints  []PrintEvent
	deaths  []DeathEvent
	pickups []ItemPickupPrintEvent
}

func newPrintCollector(p *Parser) *printCollector {
	c := &printCollector{}
	p.OnEvent(func(e Event) error {
		switch ev := e.(type) {
		case *PrintEvent:
			c.prints = append(c.prints, *ev)
		case *DeathEvent:
			c.deaths = append(c.deaths, *ev)
		case *ItemPickupPrintEvent:
			c.pickups = append(c.pickups, *ev)
		}
		return nil
	})
	return c
}

func (c *printCollector) messages() []string {
	out := make([]string, len(c.prints))
	for i := range c.prints {
		out[i] = c.prints[i].Message
	}
	return out
}

func feedPrints(t *testing.T, p *Parser, level, target int, timeMs int32, frags ...string) {
	t.Helper()
	for _, f := range frags {
		if err := p.assemblePrintLine(level, f, target, timeMs); err != nil {
			t.Fatalf("assemblePrintLine(%q): %v", f, err)
		}
	}
}

func wantMessages(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A whole-line print — what every modern KTX broadcast looks like — must
// pass through byte-identically, trailing newline included. This is the
// compatibility property the golden corpus rests on.
func TestAssemblePrintLine_WholeLinePassthrough(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)
	feedPrints(t, p, mvd.PrintMedium, -1, 1000,
		"space was gibbed by dag's rocket\n",
		"dag rides white's rocket\n",
	)
	wantMessages(t, c.messages(), []string{
		"space was gibbed by dag's rocket\n",
		"dag rides white's rocket\n",
	})
	if len(p.printLines) != 0 {
		t.Errorf("buffer not empty after terminated lines: %v", p.printLines)
	}
}

// Old kmod/qwe splits an obituary id1-progs-style across several
// bprints. They must assemble into one line, and the assembled line must
// still drive the parser's obituary death-mining.
func TestAssemblePrintLine_FragmentedObituary(t *testing.T) {
	cases := []struct {
		name  string
		frags []string
	}{
		{"three parts", []string{"DARKLORD", " was gibbed by white's rocket", "\n"}},
		{"four parts", []string{"DARKLORD", " was gibbed by ", "white", "'s rocket\n"}},
		{"two parts", []string{"DARKLORD was gibbed by white", "'s rocket\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			p.matchStarted = true
			p.players[6] = &mvd.PlayerInfo{Name: "DARKLORD"}
			c := newPrintCollector(p)

			feedPrints(t, p, mvd.PrintMedium, -1, 24983, tc.frags...)

			wantMessages(t, c.messages(), []string{"DARKLORD was gibbed by white's rocket\n"})
			if c.prints[0].TimeMs != 24983 {
				t.Errorf("TimeMs = %d, want 24983 (first fragment's time)", c.prints[0].TimeMs)
			}
			if len(c.deaths) != 1 {
				t.Fatalf("got %d DeathEvents, want 1", len(c.deaths))
			}
			if c.deaths[0].PlayerNum != 6 || c.deaths[0].TimeMs != 24983 {
				t.Errorf("death = %+v, want {PlayerNum:6, TimeMs:24983}", c.deaths[0])
			}
		})
	}
}

// Interleaved levels must not cross-contaminate: this is the mvdsv
// stream shape ezquake never sees, because it only ever receives one
// level's worth of fragments at a time.
func TestAssemblePrintLine_InterleavedLevels(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintMedium, -1, 500, "space")
	feedPrints(t, p, mvd.PrintHigh, -1, 500, "white ")
	feedPrints(t, p, mvd.PrintMedium, -1, 500, " rides dag's rocket\n")
	feedPrints(t, p, mvd.PrintHigh, -1, 500, "forces a break!\n")

	wantMessages(t, c.messages(), []string{
		"space rides dag's rocket\n",
		"white forces a break!\n",
	})
}

// The same must hold across the dem_single target dimension: a broadcast
// fragment and a per-client fragment at the same level are different
// lines. KTX interleaves exactly this way — mi_print to other clients
// lands between two pieces of a backpack line (ktx/src/items.c:2485).
func TestAssemblePrintLine_InterleavedTargets(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintLow, 3, 700, "You get ", "the Rocket Launcher")
	feedPrints(t, p, mvd.PrintLow, 5, 700, "You get ", "20 shells\n")
	feedPrints(t, p, mvd.PrintLow, 3, 700, ", ", "5 rockets", "\n")

	wantMessages(t, c.messages(), []string{
		"You get 20 shells\n",
		"You get the Rocket Launcher, 5 rockets\n",
	})
}

// A dem_single KTX pickup print arrives as one complete line and must
// reach tryEmitPickupPrint unchanged — assembly must not delay it or
// splice a broadcast into it.
func TestAssemblePrintLine_PickupPrintUntouched(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintMedium, -1, 900, "dag")
	feedPrints(t, p, mvd.PrintLow, 2, 900, "You got the Red Armor\n")
	feedPrints(t, p, mvd.PrintMedium, -1, 900, " cratered\n")

	if len(c.pickups) != 1 {
		t.Fatalf("got %d pickup events, want 1: %+v", len(c.pickups), c.pickups)
	}
	if c.pickups[0].Kind != "ra" || c.pickups[0].PlayerNum != 2 || c.pickups[0].TimeMs != 900 {
		t.Errorf("pickup = %+v, want {PlayerNum:2, Kind:ra, TimeMs:900}", c.pickups[0])
	}
	wantMessages(t, c.messages(), []string{
		"You got the Red Armor\n",
		"dag cratered\n",
	})
}

// A fragment run belongs to the server frame that produced it. A later
// fragment on the same key starts a new line, and the stale one is
// released rather than swallowing it.
func TestAssemblePrintLine_FrameBoundaryFlush(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintLow, 4, 1000, "You get ", "20 shells")
	feedPrints(t, p, mvd.PrintLow, 4, 9000, "You got the Red Armor\n")

	wantMessages(t, c.messages(), []string{
		"You get 20 shells",
		"You got the Red Armor\n",
	})
	if c.prints[0].TimeMs != 1000 || c.prints[1].TimeMs != 9000 {
		t.Errorf("times = %d,%d want 1000,9000", c.prints[0].TimeMs, c.prints[1].TimeMs)
	}
	if len(c.pickups) != 1 || c.pickups[0].Kind != "ra" {
		t.Errorf("pickups = %+v, want one ra", c.pickups)
	}
}

// The frame-boundary sweep is GLOBAL: a fragment on one key releases a
// stale buffer on a DIFFERENT key, in wire order, before the incoming
// fragment's own line is delivered. External-review scenario: an
// unterminated PRINT_HIGH line buffered at t=1000 must not sit pending
// while a complete PRINT_MEDIUM obituary at t=9000 is delivered — the
// stale line comes out first, keeping the event stream chronological
// (a matchStarted-gating consumer would otherwise judge the obituary
// against pre-announcement state).
func TestAssemblePrintLine_CrossKeyFrameFlush(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintHigh, -1, 1000, "The match begins")
	feedPrints(t, p, mvd.PrintMedium, -1, 9000, "dag rides white's rocket\n")

	wantMessages(t, c.messages(), []string{
		"The match begins",
		"dag rides white's rocket\n",
	})
	if c.prints[0].TimeMs != 1000 || c.prints[1].TimeMs != 9000 {
		t.Errorf("times = %d,%d want 1000,9000", c.prints[0].TimeMs, c.prints[1].TimeMs)
	}
}

// Two lines inside one fragment: everything up to the LAST newline is
// released as one event (ezquake's qwcsrchr rule), and the tail is kept.
func TestAssemblePrintLine_TrailingRemainderKept(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintMedium, -1, 2000, "a cratered\nb blew up\nc")
	wantMessages(t, c.messages(), []string{"a cratered\nb blew up\n"})

	feedPrints(t, p, mvd.PrintMedium, -1, 2000, " sucks it down\n")
	wantMessages(t, c.messages(), []string{
		"a cratered\nb blew up\n",
		"c sucks it down\n",
	})
}

// A demo that stops mid-line must still report what the server said.
// Both buffers share the final frame's time: the global frame-boundary
// sweep releases any earlier-frame buffer as soon as a later fragment
// arrives, so same-frame stragglers are the only shape that can still
// be pending at end of stream.
func TestFlushPendingPrintLines(t *testing.T) {
	p := NewParser(nil)
	c := newPrintCollector(p)

	feedPrints(t, p, mvd.PrintMedium, -1, 5000, "space was gibbed by dag")
	feedPrints(t, p, mvd.PrintLow, 1, 5000, "You get ", "20 shells")
	if len(c.prints) != 0 {
		t.Fatalf("nothing should have been released yet, got %q", c.messages())
	}

	if err := p.flushPendingPrintLines(); err != nil {
		t.Fatalf("flushPendingPrintLines: %v", err)
	}
	// Deterministic order at equal time: level breaks the tie.
	wantMessages(t, c.messages(), []string{
		"You get 20 shells",
		"space was gibbed by dag",
	})

	// Idempotent — a caller that keeps polling past the end gets nothing.
	if err := p.flushPendingPrintLines(); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if len(c.prints) != 2 {
		t.Errorf("second flush released %d extra lines", len(c.prints)-2)
	}
}

// Match-start detection reads assembled lines, so a phrase split across
// fragments still opens the obituary gate. On the 2003 kmod demos the
// announcement is fragmented exactly like the obituaries are.
func TestAssemblePrintLine_FragmentedMatchStart(t *testing.T) {
	p := NewParser(nil)
	newPrintCollector(p)
	feedPrints(t, p, mvd.PrintHigh, -1, 100, "The duel ", "has ", "begun!\n")
	if !p.matchStarted {
		t.Fatal("matchStarted not set from the assembled line")
	}
}
