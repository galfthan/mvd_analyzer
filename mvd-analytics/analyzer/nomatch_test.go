package analyzer

import (
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// TestStatusNamesRunningGame pins the two remaining-time spellings the
// archive carries against every idle / pre-match value beside them. The
// distinction is load-bearing twice over: it is what separates
// midMatchRecording from the rest, and it is what separates
// matchStartUnannounced from noMatchDeclared.
//
// "20 min left" is the regression case: the digit sits at the FRONT of the
// reading, not against the " left" suffix, so a test that looks at the
// character before the suffix reads KTX's own format as idle.
func TestStatusNamesRunningGame(t *testing.T) {
	running := []string{
		"20 min left",  // ktx/src/match.c:596,723,1330 — the common case
		"1 min left",   //
		"0 min left",   // the last tick before the match ends
		"19:35 left",   // an older mod's mm:ss reading
		"14:59 left",   //
		"120 min left", // no upper bound on the reading
	}
	idle := []string{
		"",           // no status key at all (pre-KTX servers, foreign mods)
		"Standby",    // ktx/src/world.c:543
		"Countdown",  // ktx/src/match.c:2475 — about to start is not started
		"Forcestart", // ktx/src/admin.c:693
		"Normal",     // observed on a non-KTX mod in the archive
		"left",       // suffix with nothing in front of it
		" left",      //
		"min left",   // a reading with no number is not a reading
	}
	for _, v := range running {
		if !statusNamesRunningGame(v) {
			t.Errorf("statusNamesRunningGame(%q) = false, want true", v)
		}
	}
	for _, v := range idle {
		if statusNamesRunningGame(v) {
			t.Errorf("statusNamesRunningGame(%q) = true, want false", v)
		}
	}
}

// TestNoMatchVerdict walks the reason precedence with one case per branch,
// including the two orderings that matter: a truncated read outranks every
// positive signal (the evidence past the truncation is unknown), and a
// running status at open outranks the same status arriving later.
func TestNoMatchVerdict(t *testing.T) {
	cases := []struct {
		name    string
		errs    []string
		status  ServerStatus
		gameDir string
		kills   int
		want    string
		// detail must name this substring, so the sentence and the code
		// cannot drift apart.
		detail string
	}{{
		name: "truncated read outranks everything",
		errs: []string{streamAbortedPrefix + "dem block size 1124103029 exceeds maximum"},
		// A mid-match status AND kills are present; neither is a sound
		// conclusion when the rest of the demo was never read.
		status:  ServerStatus{AtOpen: "13 min left", RunningSeen: true},
		gameDir: "qw",
		kills:   40,
		want:    NoMatchDemoUnreadable,
		detail:  "errors[]",
	}, {
		name:   "an unrelated analyzer error is not a truncated read",
		errs:   []string{"region control: no regions"},
		status: ServerStatus{AtOpen: "13 min left", RunningSeen: true},
		kills:  40,
		want:   NoMatchMidMatchRecording,
		detail: `"13 min left"`,
	}, {
		name:    "running at open",
		status:  ServerStatus{AtOpen: "1 min left", RunningSeen: true},
		gameDir: "qw",
		kills:   42,
		want:    NoMatchMidMatchRecording,
		detail:  "42 kill(s)",
	}, {
		name:    "running only later",
		status:  ServerStatus{AtOpen: "Countdown", RunningSeen: true},
		gameDir: "fortress",
		kills:   259,
		want:    NoMatchStartUnannounced,
		detail:  `gamedir "fortress"`,
	}, {
		name:    "never running, with kills",
		status:  ServerStatus{AtOpen: "Standby"},
		gameDir: "fortress",
		kills:   149,
		want:    NoMatchNoMatchDeclared,
		detail:  "149 kill(s)",
	}, {
		name:    "never running, no status key at all, with kills",
		gameDir: "ctf",
		kills:   3,
		want:    NoMatchNoMatchDeclared,
		detail:  `gamedir "ctf"`,
	}, {
		name:    "never running, nothing played",
		status:  ServerStatus{AtOpen: "Standby"},
		gameDir: "qw",
		want:    NoMatchNoPlayRecorded,
		detail:  "no kills",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, detail := noMatchVerdict(tc.errs, tc.status, tc.gameDir, tc.kills)
			if reason != tc.want {
				t.Errorf("reason = %q, want %q", reason, tc.want)
			}
			if !strings.Contains(detail, tc.detail) {
				t.Errorf("detail %q does not name %q", detail, tc.detail)
			}
			// The stock gamedir is the default and adds nothing to a
			// sentence; naming it would be noise on 96% of demos.
			if tc.gameDir == "qw" && strings.Contains(detail, "gamedir") {
				t.Errorf("detail names the stock gamedir: %q", detail)
			}
		})
	}
}

// TestNoMatchPostOnlyOnStreamlessResults is the false-positive guard: the
// marker exists to explain an ABSENCE, so a result that has players must
// never carry one. Every healthy demo in the archive goes down this path.
func TestNoMatchPostOnlyOnStreamlessResults(t *testing.T) {
	co := &CoreOutputs{ServerStatus: ServerStatus{AtOpen: "Standby"}}

	withPlayers := &Result{Streams: &Streams{Players: []PlayerStream{{Name: "xantom"}}}}
	noMatchPost(withPlayers, co)
	if withPlayers.NoMatch != nil {
		t.Errorf("marker stamped on a result with players: %+v", withPlayers.NoMatch)
	}

	// The pipeline's own empty state is Streams == nil (buildStreamsResult
	// returns nil rather than an empty block), but a hand-built registry can
	// produce the empty-players shape and it means the same thing.
	for name, res := range map[string]*Result{
		"nil streams":   {},
		"empty players": {Streams: &Streams{}},
	} {
		noMatchPost(res, co)
		if res.NoMatch == nil {
			t.Fatalf("%s: no marker stamped", name)
		}
		if res.NoMatch.Reason != NoMatchNoPlayRecorded {
			t.Errorf("%s: reason = %q", name, res.NoMatch.Reason)
		}
	}
}

// TestNoMatchPostReadsTheAssembledResult pins where each evidence field
// comes from: the gamedir off metadata's serverinfo, the kill count off the
// frag log, the status off CoreOutputs (NOT off serverInfo["status"], which
// is last-write-wins and so names the state at demo END — here "Standby",
// while the recording opened mid-game).
func TestNoMatchPostReadsTheAssembledResult(t *testing.T) {
	res := &Result{
		Metadata: &MetadataResult{ServerInfo: map[string]string{
			"*gamedir": "fortress",
			"status":   "Standby",
		}},
		Frags: &FragResult{Frags: make([]FragEntry, 7)},
	}
	noMatchPost(res, &CoreOutputs{ServerStatus: ServerStatus{AtOpen: "5 min left", RunningSeen: true}})

	nm := res.NoMatch
	if nm == nil {
		t.Fatal("no marker stamped")
	}
	if nm.Reason != NoMatchMidMatchRecording {
		t.Errorf("reason = %q, want %q", nm.Reason, NoMatchMidMatchRecording)
	}
	if nm.StatusAtOpen != "5 min left" || !nm.StatusRunningSeen {
		t.Errorf("status evidence = %q / %v", nm.StatusAtOpen, nm.StatusRunningSeen)
	}
	if nm.GameDir != "fortress" {
		t.Errorf("gameDir = %q", nm.GameDir)
	}
	if nm.Kills != 7 {
		t.Errorf("kills = %d", nm.Kills)
	}
}

// TestMetadataTracksStatusOverTime is the analyzer-side half: serverInfo
// keeps the LAST status (what the server said at demo end), CoreOutputs
// keeps the first one and the running flag. A demo that opens on "Countdown"
// and ends on "Standby" is indistinguishable from an idle server on the
// last-write-wins map alone — the transition through a running reading is
// the only thing that says a match happened.
func TestMetadataTracksStatusOverTime(t *testing.T) {
	a := NewMetadataAnalyzer()
	feed := []events.Event{
		&events.StuffTextEvent{Command: `fullserverinfo "\*gamedir\qw\map\ultrav\status\Countdown"`},
		&events.ServerInfoEvent{Key: "status", Value: "20 min left"},
		&events.ServerInfoEvent{Key: "status", Value: "19 min left"},
		&events.ServerInfoEvent{Key: "status", Value: "Standby"},
	}
	for _, e := range feed {
		if err := a.OnEvent(e); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	res := &Result{}
	if err := a.Finalize(res); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got := res.Metadata.ServerInfo["status"]; got != "Standby" {
		t.Errorf("serverInfo status = %q, want the last value Standby", got)
	}
	co := &CoreOutputs{}
	a.PopulateCore(co)
	if co.ServerStatus.AtOpen != "Countdown" {
		t.Errorf("AtOpen = %q, want Countdown", co.ServerStatus.AtOpen)
	}
	if !co.ServerStatus.RunningSeen {
		t.Error("RunningSeen = false, want true")
	}
}

// TestWallClockPostPublishesMarkersWithoutStreams: the date stamps a
// stream-less demo printed used to be read and dropped, because their only
// home was Streams.Global. They now ride the marker — raw, and without the
// graded anchor, which is a projection through a match window this result
// does not have.
func TestWallClockPostPublishesMarkersWithoutStreams(t *testing.T) {
	res := &Result{NoMatch: &NoMatchResult{Reason: NoMatchStartUnannounced}}
	co := &CoreOutputs{Clock: &Clock{DateMarkers: []WallClockMarker{{
		Source: wallSourceMatchDate,
		Kind:   wallKindMatchStart,
		UnixMs: 1627844964000,
		AtMs:   255,
		TZ:     "EDT",
		Raw:    "2021-08-01 15:09:24 EDT",
	}}}}
	wallClockPost(res, co)

	nm := res.NoMatch
	if len(nm.DateMarkers) != 1 {
		t.Fatalf("markers = %+v", nm.DateMarkers)
	}
	if got := nm.DateMarkers[0].UnixMs; got != 1627844964000 {
		t.Errorf("marker reported as %d, want the verbatim stamp", got)
	}

	// Nothing is written when there is no marker to publish, and a result
	// with neither streams nor a marker is left alone entirely.
	empty := &Result{NoMatch: &NoMatchResult{Reason: NoMatchNoPlayRecorded}}
	wallClockPost(empty, &CoreOutputs{Clock: &Clock{}})
	if empty.NoMatch.DateMarkers != nil {
		t.Errorf("markers invented from nothing: %+v", empty.NoMatch.DateMarkers)
	}
	unmarked := &Result{}
	wallClockPost(unmarked, co)
	if unmarked.NoMatch != nil {
		t.Errorf("wall clock created a marker: %+v", unmarked.NoMatch)
	}
}
