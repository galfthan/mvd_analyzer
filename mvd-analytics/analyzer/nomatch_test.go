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
//
// The idle list carries the whole non-reading vocabulary the census found
// (see statusNamesRunningGame for the counts) plus the near-misses a looser
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

// TestNoMatchPostOnlyOnStreamlessResults pins the contract every doc site
// states: the marker is present exactly when `streams` is absent. That is
// ONE predicate, `res.Streams != nil`, tested here in both directions —
// which is also the false-positive guard (every healthy demo in the archive
// goes down the first path).
//
// The empty-players shape is deliberately on the marker-free side. It is
// not a state the pipeline can build (buildStreamsResult returns nil rather
// than an empty block, timeline_streams.go:731, and is the only writer of
// result.Streams), and treating it as stream-less would be a SECOND
// spelling of the contract — one under which a result could carry a streams
// block and a no-match marker at once, which wallClockPost's routing and
// the schema docs both say is impossible.
func TestNoMatchPostOnlyOnStreamlessResults(t *testing.T) {
	co := &CoreOutputs{ServerStatus: ServerStatus{AtOpen: "Standby"}}

	for name, res := range map[string]*Result{
		"with players":  {Streams: &Streams{Players: []PlayerStream{{Name: "xantom"}}}},
		"empty players": {Streams: &Streams{}},
	} {
		noMatchPost(res, co)
		if res.NoMatch != nil {
			t.Errorf("%s: marker stamped on a result that has a streams block: %+v", name, res.NoMatch)
		}
	}

	res := &Result{}
	noMatchPost(res, co)
	if res.NoMatch == nil {
		t.Fatal("nil streams: no marker stamped")
	}
	if res.NoMatch.Reason != NoMatchNoPlayRecorded {
		t.Errorf("nil streams: reason = %q", res.NoMatch.Reason)
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

// TestMetadataStatusAtOpenIsTheOpeningDumpOnly pins the field's promise
// against the two ways a `status` value can arrive after demo open. Both
// are reachable: 3 of the 1 032 stream-less demos open with no `status` and
// gain one from a later svc_serverinfo, and 4 carry a second
// `fullserverinfo` dump. Neither may restate demo open — a later reading is
// a statement about an instant INSIDE the recording, which is what
// RunningSeen carries.
func TestMetadataStatusAtOpenIsTheOpeningDumpOnly(t *testing.T) {
	for name, feed := range map[string][]events.Event{
		"a later svc_serverinfo sets it": {
			&events.StuffTextEvent{Command: `fullserverinfo "\*gamedir\ctf\map\dm3"`},
			&events.ServerInfoEvent{Key: "status", Value: "Countdown"},
			&events.ServerInfoEvent{Key: "status", Value: "9 min left"},
		},
		"a second fullserverinfo dump carries it": {
			&events.StuffTextEvent{Command: `fullserverinfo "\*gamedir\ctf\map\dm3"`},
			&events.StuffTextEvent{Command: `fullserverinfo "\*gamedir\ctf\map\dm3\status\9 min left"`},
		},
	} {
		a := NewMetadataAnalyzer()
		for _, e := range feed {
			if err := a.OnEvent(e); err != nil {
				t.Fatalf("%s: OnEvent: %v", name, err)
			}
		}
		co := &CoreOutputs{}
		a.PopulateCore(co)
		if co.ServerStatus.AtOpen != "" {
			t.Errorf("%s: AtOpen = %q, want empty — the opening dump had no status key", name, co.ServerStatus.AtOpen)
		}
		if !co.ServerStatus.RunningSeen {
			t.Errorf("%s: RunningSeen = false — the later reading is still evidence", name)
		}
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
