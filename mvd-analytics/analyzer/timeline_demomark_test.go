package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// newDemoMarkTestAnalyzer wires a TimelineAnalyzer just far enough for
// buildDemoMarkers: slot→(name, team, userid) resolution via the context
// roster, plus a (nil-safe) CoreOutputs so TeamFor / resolveAt work.
func newDemoMarkTestAnalyzer() *TimelineAnalyzer {
	a := NewTimelineAnalyzer()
	a.ctx = &Context{}
	a.ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "alpha", Team: "red", UserID: 111}
	a.ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "bravo", Team: "blue", UserID: 222}
	a.ctx.Players[2] = &events.PlayerInfo{Slot: 2, Name: "watcher", UserID: 333, Spectator: true}
	a.playerNames[0] = "alpha"
	a.playerNames[1] = "bravo"
	a.playerUserIDs[0] = 111
	a.playerUserIDs[1] = 222
	a.UseCoreOutputs(&CoreOutputs{})
	return a
}

// TestDemoMarkers_ResolveIdentityAndLabel checks slot attribution: the
// marking slot resolves to name/team/userid, and the argument-tail label
// passes through.
func TestDemoMarkers_ResolveIdentityAndLabel(t *testing.T) {
	a := newDemoMarkTestAnalyzer()
	a.rawDemoMarks = []demoMarkRecord{
		{Time: 435385, PlayerNum: 0, Label: ""},
		{Time: 484217, PlayerNum: 1, Label: "0 round-07"},
	}

	markers := a.buildDemoMarkers()
	if len(markers) != 2 {
		t.Fatalf("got %d markers, want 2", len(markers))
	}

	m0 := markers[0]
	if m0.PlayerName != "alpha" || m0.Team != "red" || m0.PlayerUserID != 111 {
		t.Errorf("marker 0 = %+v, want alpha/red/111", m0)
	}
	if m0.Time != 435385 || m0.Label != "" {
		t.Errorf("marker 0 time/label = %d/%q, want 435385/\"\"", m0.Time, m0.Label)
	}

	m1 := markers[1]
	if m1.PlayerName != "bravo" || m1.Team != "blue" || m1.PlayerUserID != 222 {
		t.Errorf("marker 1 = %+v, want bravo/blue/222", m1)
	}
	if m1.Label != "0 round-07" {
		t.Errorf("marker 1 label = %q, want %q", m1.Label, "0 round-07")
	}
	if m0.Spectator || m1.Spectator {
		t.Errorf("player marks flagged as spectator: %v/%v", m0.Spectator, m1.Spectator)
	}
}

// TestDemoMarkers_SpectatorMarkFlagged checks a mark from a spectator slot
// (KTX /demomark is CF_BOTH) resolves the spectator's name and carries the
// Spectator flag from the roster's *spectator state.
func TestDemoMarkers_SpectatorMarkFlagged(t *testing.T) {
	a := newDemoMarkTestAnalyzer()
	a.rawDemoMarks = []demoMarkRecord{{Time: 200000, PlayerNum: 2}}

	markers := a.buildDemoMarkers()
	if len(markers) != 1 {
		t.Fatalf("got %d markers, want 1", len(markers))
	}
	m := markers[0]
	if !m.Spectator {
		t.Errorf("spectator mark not flagged: %+v", m)
	}
	if m.PlayerName != "watcher" || m.PlayerUserID != 333 {
		t.Errorf("spectator mark = %+v, want watcher/333", m)
	}
}

// TestDemoMarkers_UnaddressedBlockNoAttribution checks a mark whose block
// was not slot-addressed (PlayerNum -1) carries only time and label.
func TestDemoMarkers_UnaddressedBlockNoAttribution(t *testing.T) {
	a := newDemoMarkTestAnalyzer()
	a.rawDemoMarks = []demoMarkRecord{{Time: 100000, PlayerNum: -1, Label: ""}}

	markers := a.buildDemoMarkers()
	if len(markers) != 1 {
		t.Fatalf("got %d markers, want 1", len(markers))
	}
	m := markers[0]
	if m.PlayerName != "" || m.Team != "" || m.PlayerUserID != 0 || m.PlayerSlot != -1 {
		t.Errorf("unaddressed marker = %+v, want no attribution and slot -1", m)
	}
}

// TestDemoMarkers_WarmupMarkKeepsNegativeTime checks a mark inserted
// before match start survives the match-clock rebase as a negative time
// (surface-authoritative-data rule).
func TestDemoMarkers_WarmupMarkKeepsNegativeTime(t *testing.T) {
	a := newDemoMarkTestAnalyzer()
	res := &Result{
		TimelineAnalysis: &TimelineAnalysisResult{
			DemoMarkers: []DemoMarkerEvent{
				{Time: 5000, PlayerName: "alpha", PlayerSlot: 0},  // warmup: before match start
				{Time: 40000, PlayerName: "alpha", PlayerSlot: 0}, // in match
			},
		},
	}

	a.rebaseToMatch(res, 10000)

	got := res.TimelineAnalysis.DemoMarkers
	if got[0].Time != -5000 {
		t.Errorf("warmup mark time = %d, want -5000", got[0].Time)
	}
	if got[1].Time != 30000 {
		t.Errorf("in-match mark time = %d, want 30000", got[1].Time)
	}
}
