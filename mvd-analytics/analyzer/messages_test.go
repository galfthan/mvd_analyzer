package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// feedPrints runs a fresh MessagesAnalyzer over the given prints and returns
// the recorded events.
func feedPrints(t *testing.T, prints ...*events.PrintEvent) []MatchEvent {
	t.Helper()
	a := NewMessagesAnalyzer()
	if err := a.Init(&Context{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, p := range prints {
		if err := a.OnEvent(p); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	return a.events
}

func chatPrint(msg string, tSec float64) *events.PrintEvent {
	return &events.PrintEvent{Level: events.PrintChat, Message: msg, Time: tSec}
}

// MVDSV delivers chat per recipient (one svc_print per player), so identical
// copies sharing the same wire-ms must collapse to a single event.
func TestChatDedupIdenticalCopies(t *testing.T) {
	got := feedPrints(t,
		chatPrint("alice: gg", 10.0),
		chatPrint("alice: gg", 10.0),
		chatPrint("alice: gg", 10.0),
	)
	if len(got) != 1 {
		t.Fatalf("want 1 event after dedup, got %d: %+v", len(got), got)
	}
	if got[0].Type != "chat" || got[0].Player != "alice" || got[0].Message != "gg" {
		t.Fatalf("unexpected event: %+v", got[0])
	}
}

// The same line at a different time is a distinct message and must be kept.
func TestChatDedupKeepsDistinctTimes(t *testing.T) {
	got := feedPrints(t,
		chatPrint("alice: gg", 10.0),
		chatPrint("alice: gg", 11.0),
	)
	if len(got) != 2 {
		t.Fatalf("want 2 events for distinct times, got %d: %+v", len(got), got)
	}
}

// say and say_team with the same text/time are different events (the dedup
// key includes Type), so both are kept.
func TestChatDedupSayAndTeamsayIndependent(t *testing.T) {
	got := feedPrints(t,
		chatPrint("alice: gg", 10.0),   // public say  -> type "chat"
		chatPrint("(alice): gg", 10.0), // say_team    -> type "teamsay"
		chatPrint("(alice): gg", 10.0), // duplicate teamsay copy
	)
	if len(got) != 2 {
		t.Fatalf("want 2 events (chat + teamsay), got %d: %+v", len(got), got)
	}
	types := map[string]bool{}
	for _, e := range got {
		types[e.Type] = true
	}
	if !types["chat"] || !types["teamsay"] {
		t.Fatalf("want both chat and teamsay, got %+v", got)
	}
}

// Obituaries arrive as a single broadcast copy and are intentionally NOT
// deduped — the frag path must pass every copy through verbatim.
func TestObituariesNotDeduped(t *testing.T) {
	obit := func(tSec float64) *events.PrintEvent {
		return &events.PrintEvent{Level: events.PrintMedium, Message: "alice rides bob's rocket", Time: tSec}
	}
	got := feedPrints(t, obit(10.0), obit(10.0))
	if len(got) != 2 {
		t.Fatalf("want 2 frag events (frags not deduped), got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Type != "frag" {
			t.Fatalf("want frag events, got %+v", e)
		}
	}
}
