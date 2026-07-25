package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

func rosterEvents(t *testing.T, level int, msg string, timeMs int32) []Event {
	t.Helper()
	p := NewParser(nil)
	var got []Event
	p.OnEvent(func(e Event) error {
		switch e.(type) {
		case *PlayerDepartureEvent, *PlayerRejoinEvent:
			got = append(got, e)
		}
		return nil
	})
	if err := p.tryEmitRosterPrint(level, msg, timeMs); err != nil {
		t.Fatalf("tryEmitRosterPrint(%q): %v", msg, err)
	}
	return got
}

func departureOf(t *testing.T, msg string) *PlayerDepartureEvent {
	t.Helper()
	evs := rosterEvents(t, mvd.PrintHigh, msg, 1096572)
	if len(evs) != 1 {
		t.Fatalf("%q produced %d events, want 1", msg, len(evs))
	}
	e, ok := evs[0].(*PlayerDepartureEvent)
	if !ok {
		t.Fatalf("%q produced %T, want *PlayerDepartureEvent", msg, evs[0])
	}
	return e
}

// The complete wire form, and the truncated tail a fragmenting server
// leaves behind: 4on4_l_vs_la[e1m2] (kmod 1.54 / qwe 0.153) splits the
// departure between "…21 frag" and "s\n".
func TestRosterPrint_Departure(t *testing.T) {
	cases := []struct {
		msg   string
		name  string
		frags int
	}{
		{"rusti left the game with 16 frags\n", "rusti", 16},
		{"DARKLORD left the game with 21 frag", "DARKLORD", 21},
		{"shiva left the game with 26 frag", "shiva", 26},
		{"wd.dilbert left the game with 0 frags\n", "wd.dilbert", 0},
		{"/ tin can left the game with 8 frags\n", "/ tin can", 8},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			e := departureOf(t, tc.msg)
			if e.Name != tc.name || !e.FragsKnown || e.Frags != tc.frags {
				t.Errorf("got %+v, want name=%q frags=%d known", e, tc.name, tc.frags)
			}
			if e.TimeMs != 1096572 {
				t.Errorf("TimeMs = %d, want 1096572", e.TimeMs)
			}
		})
	}
}

// The same server that splits "frags" also emits numbers digit by digit —
// at t=1094110 on that demo a team score arrives as the six prints
// "Team [.la.] = ", "", "2", "2", "7", "\n". A departure cut the same way
// would decode as 2 and silently overwrite the correct 26, so a digit run
// is only trusted when " frag" follows it.
func TestRosterPrint_DepartureRejectsNumberCutMidDigits(t *testing.T) {
	e := departureOf(t, "shiva left the game with 2")
	if e.FragsKnown {
		t.Errorf("got %+v, want FragsKnown=false — the number may be the head of a larger one", e)
	}
	if e.Name != "shiva" {
		t.Errorf("Name = %q, want shiva — the departure itself is still known", e.Name)
	}
	// The continuation fragment on its own names nobody and says nothing.
	if evs := rosterEvents(t, mvd.PrintHigh, "6 frags\n", 1096572); len(evs) != 0 {
		t.Errorf("the tail fragment produced %d events, want 0", len(evs))
	}
}

// A player can type anything. Chat is PRINT_CHAT (3); every roster
// broadcast is PRINT_HIGH (2).
func TestRosterPrint_ChatIsNotABroadcast(t *testing.T) {
	for _, msg := range []string{
		"bob left the game with 99 frags\n",
		"bob [aaa] rejoins the game with 99 frags\n",
		"bob reenters the game without stats\n",
	} {
		if evs := rosterEvents(t, mvd.PrintChat, msg, 1000); len(evs) != 0 {
			t.Errorf("chat %q produced %d roster events, want 0", msg, len(evs))
		}
	}
}

// The rejoin / reenter pair, both team and non-team wording. Prefix keeps
// the "[team]" suffix because the wire has no delimiter between a netname
// and the bracket and a netname may contain either.
func TestRosterPrint_RejoinAndReenter(t *testing.T) {
	cases := []struct {
		msg        string
		prefix     string
		withStats  bool
		frags      int
		fragsKnown bool
	}{
		{"rusti [jah] rejoins the game with 16 frags\n", "rusti [jah]", true, 16, true},
		{"rusti rejoins the game with 1 frag\n", "rusti", true, 1, true},
		{"rusti [jah] reenters the game without stats\n", "rusti [jah]", false, 0, false},
		{"rusti reenters the game without stats\n", "rusti", false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			evs := rosterEvents(t, mvd.PrintHigh, tc.msg, 613452)
			if len(evs) != 1 {
				t.Fatalf("%q produced %d events, want 1", tc.msg, len(evs))
			}
			e, ok := evs[0].(*PlayerRejoinEvent)
			if !ok {
				t.Fatalf("%q produced %T, want *PlayerRejoinEvent", tc.msg, evs[0])
			}
			if e.Prefix != tc.prefix || e.WithStats != tc.withStats ||
				e.Frags != tc.frags || e.FragsKnown != tc.fragsKnown {
				t.Errorf("got %+v, want prefix=%q withStats=%v frags=%d known=%v",
					e, tc.prefix, tc.withStats, tc.frags, tc.fragsKnown)
			}
		})
	}
}

// Lines that merely mention the words are not roster broadcasts, and a
// marker at position 0 has no name in front of it.
func TestRosterPrint_NonMatches(t *testing.T) {
	for _, msg := range []string{
		"shiva timed out\n",
		"rusti [jah] arrives late\n",
		"rusti entered the game\n",
		" left the game with 5 frags\n",
		"Team [.la.] leads by 123 frags\n",
	} {
		if evs := rosterEvents(t, mvd.PrintHigh, msg, 1000); len(evs) != 0 {
			t.Errorf("%q produced %d roster events, want 0", msg, len(evs))
		}
	}
}
