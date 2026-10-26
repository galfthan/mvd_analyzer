package analyzer

import (
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// finalScoresEvent is the wire record the parser hands analytics for
//
//	//finalscores "Sep 29, 21:27" "duel" "aerowalk" "kip" 19 "grisling" 24
func finalScoresEvent() *events.FinalScoresEvent {
	return &events.FinalScoresEvent{
		Date: "Sep 29, 21:27", Mode: "duel", Map: "aerowalk",
		Team1: "kip", Score1: 19, Team2: "grisling", Score2: 24,
		TimeMs: 612340,
	}
}

// feedFinalScores runs one directive through the metadata analyzer and returns
// both surfaces it lands on: the Result section and the core artifact.
func feedFinalScores(t *testing.T, e *events.FinalScoresEvent) (*MetadataResult, *CoreOutputs) {
	t.Helper()
	a := NewMetadataAnalyzer()
	if err := a.OnEvent(e); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	res := &Result{}
	if err := a.Finalize(res); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	co := &CoreOutputs{}
	a.PopulateCore(co)
	return res.Metadata, co
}

func TestMetadataFinalScoresVerbatim(t *testing.T) {
	md, co := feedFinalScores(t, finalScoresEvent())
	if md == nil || md.FinalScores == nil {
		t.Fatal("no FinalScores on the metadata section")
	}
	fs := md.FinalScores
	if fs.Date != "Sep 29, 21:27" || fs.Mode != "duel" || fs.Map != "aerowalk" {
		t.Errorf("FinalScores = %+v", fs)
	}
	if fs.Team1 != "kip" || fs.Score1 != 19 || fs.Team2 != "grisling" || fs.Score2 != 24 {
		t.Errorf("scoreline = %+v", fs)
	}
	if co.FinalScores == nil || co.FinalScores.Map != "aerowalk" {
		t.Errorf("CoreOutputs.FinalScores = %+v", co.FinalScores)
	}
}

// The demoinfo block wins every field it names — //finalscores only reaches
// the demos that have no such block, which is the whole point of decoding it.
func TestMatchSourcesPreferDemoInfo(t *testing.T) {
	co := &CoreOutputs{
		DemoInfo:      &DemoInfoResult{Map: "dm3", Mode: "team"},
		ServerInfoMap: "dm3",
		FinalScores:   &FinalScores{Map: "aerowalk", Mode: "duel"},
	}
	a := &MatchAnalyzer{core: co}
	if m, src := a.resolveMap(); m != "dm3" || src != MatchSrcKTX {
		t.Errorf("resolveMap = %q/%q, want dm3/ktx", m, src)
	}
	if m, src := a.resolveMode(); m != "team" || src != MatchSrcKTX {
		t.Errorf("resolveMode = %q/%q, want team/ktx", m, src)
	}
}

func TestMatchSourcesFallBackToFinalScores(t *testing.T) {
	co := &CoreOutputs{FinalScores: &FinalScores{Map: "aerowalk", Mode: "duel"}}
	a := &MatchAnalyzer{core: co}
	// The serverinfo `map` key still outranks the directive; the mode has no
	// such competitor, because the other mode sources speak other vocabularies.
	if m, src := a.resolveMap(); m != "aerowalk" || src != MatchSrcFinalScores {
		t.Errorf("resolveMap = %q/%q, want aerowalk/finalscores", m, src)
	}
	co.ServerInfoMap = "dm6"
	if m, src := a.resolveMap(); m != "dm6" || src != MatchSrcServerInfo {
		t.Errorf("resolveMap = %q/%q, want dm6/serverinfo", m, src)
	}
	if m, src := a.resolveMode(); m != "duel" || src != MatchSrcFinalScores {
		t.Errorf("resolveMode = %q/%q, want duel/finalscores", m, src)
	}
}

func TestMatchTeamsFromFinalScoresOnlyWhenDerivedIsEmpty(t *testing.T) {
	fs := &FinalScores{Team1: "red", Score1: 81, Team2: "blue", Score2: 45}
	a := &MatchAnalyzer{core: &CoreOutputs{FinalScores: fs}}

	// A demo whose scoreboard produced rows keeps them, and says so.
	derived := &MatchResult{Teams: []TeamStat{{Name: "red", Frags: 80}, {Name: "blue", Frags: 45}}}
	a.applyFinalScoresTeams(derived)
	if derived.Sources.Teams != MatchSrcDerived {
		t.Errorf("Sources.Teams = %q, want derived", derived.Sources.Teams)
	}
	if derived.Teams[0].Frags != 80 {
		t.Errorf("derived row overwritten: %+v", derived.Teams)
	}

	// A demo with no team rows at all adopts the two sides KTX stated.
	empty := &MatchResult{}
	a.applyFinalScoresTeams(empty)
	if empty.Sources.Teams != MatchSrcFinalScores {
		t.Fatalf("Sources.Teams = %q, want finalscores", empty.Sources.Teams)
	}
	if len(empty.Teams) != 2 || empty.Teams[0] != (TeamStat{Name: "red", Frags: 81}) ||
		empty.Teams[1] != (TeamStat{Name: "blue", Frags: 45}) {
		t.Errorf("Teams = %+v", empty.Teams)
	}

	// Nothing to adopt: no directive, no rows, no invented provenance.
	none := &MatchResult{}
	(&MatchAnalyzer{core: &CoreOutputs{}}).applyFinalScoresTeams(none)
	if none.Sources.Teams != "" || len(none.Teams) != 0 {
		t.Errorf("Teams = %+v, src = %q", none.Teams, none.Sources.Teams)
	}
}

func TestParseFinalScoresDate(t *testing.T) {
	got, ok := parseFinalScoresDate("Sep 29, 21:27")
	if !ok {
		t.Fatal("well-formed stamp rejected")
	}
	if got.month != 9 || got.day != 29 || got.hour != 21 || got.min != 27 {
		t.Errorf("stamp = %+v", got)
	}
	for _, bad := range []string{"", "Sep 29 21:27:00 2008", "Sep 29,", "Xxx 29, 21:27", "Sep 32, 21:27", "Sep 29, 25:27", "Sep 29, 21"} {
		if _, ok := parseFinalScoresDate(bad); ok {
			t.Errorf("parseFinalScoresDate(%q) accepted", bad)
		}
	}
}

// The year is the one thing the stamp cannot state, so it is taken from the
// reference instant — including across a new year, where the naive reading
// would land a year out.
func TestCompleteFinalScoresYear(t *testing.T) {
	cases := []struct {
		name   string
		stamp  string
		ref    int64
		offset int
		want   int64
	}{
		{"same year", "Sep 29, 21:27", utc(2008, time.September, 29, 21, 30, 0), 0,
			utc(2008, time.September, 29, 21, 27, 0)},
		{"borrowed zone shifts the instant", "Sep 29, 21:27", utc(2008, time.September, 29, 19, 30, 0), 3600,
			utc(2008, time.September, 29, 20, 27, 0)},
		{"stamp just after new year", "Jan 01, 00:05", utc(2007, time.December, 31, 23, 55, 0), 0,
			utc(2008, time.January, 1, 0, 5, 0)},
		{"stamp just before new year", "Dec 31, 23:55", utc(2008, time.January, 1, 0, 5, 0), 0,
			utc(2007, time.December, 31, 23, 55, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := parseFinalScoresDate(tc.stamp)
			if !ok {
				t.Fatalf("parse %q", tc.stamp)
			}
			if got := completeFinalScoresYear(p, tc.ref, tc.offset); got != tc.want {
				t.Errorf("completeFinalScoresYear = %d (%s), want %d (%s)",
					got, time.UnixMilli(got).UTC(), tc.want, time.UnixMilli(tc.want).UTC())
			}
		})
	}
}

// wallClockResultWithFinalScores builds the Result the post-processor reads:
// a matchdate marker on the clock, a `//finalscores` stamp on the metadata
// section, and a match window to project them onto.
func wallClockResultWithFinalScores(matchDate, finalDate string, matchLenMs int32) (*Result, *CoreOutputs) {
	res := &Result{
		Streams:  &Streams{Global: GlobalStream{MatchEnd: matchLenMs, DemoOffset: 0}},
		Metadata: &MetadataResult{FinalScores: &FinalScores{Date: finalDate}},
	}
	co := &CoreOutputs{Clock: &Clock{}}
	if matchDate != "" {
		m, ok := parseDateMarkerPrint("matchdate: "+matchDate, 0)
		if !ok {
			panic("bad matchdate fixture: " + matchDate)
		}
		co.Clock.DateMarkers = []WallClockMarker{m}
	}
	return res, co
}

// An agreeing //finalscores stamp corroborates the anchor: the grade the
// matchdate marker earned on its own must survive, and the stamp is reported
// with the year it borrowed named.
func TestWallClockFinalScoresCorroborates(t *testing.T) {
	const matchLen = int32(20 * 60 * 1000)
	res, co := wallClockResultWithFinalScores("2008-01-05 20:05:38 CET", "Jan 05, 20:25", matchLen)
	wallClockPost(res, co)

	g := res.Streams.Global
	if g.MatchStartConfidence != wallConfExact {
		t.Errorf("MatchStartConfidence = %q (%q), want exact", g.MatchStartConfidence, g.MatchStartNote)
	}
	if g.MatchStartSource != wallSourceMatchDate {
		t.Errorf("MatchStartSource = %q, want matchdate", g.MatchStartSource)
	}
	if len(g.DateMarkers) != 2 {
		t.Fatalf("markers = %+v", g.DateMarkers)
	}
	fs := g.DateMarkers[1]
	if fs.Source != wallSourceFinalScores || fs.Kind != wallKindMatchEnd {
		t.Errorf("finalscores marker = %+v", fs)
	}
	if fs.YearFrom != wallSourceMatchDate {
		t.Errorf("YearFrom = %q, want matchdate", fs.YearFrom)
	}
	if fs.Raw != "Jan 05, 20:25" {
		t.Errorf("Raw = %q", fs.Raw)
	}
	// CET was borrowed from the anchoring marker, so the instant is 19:25Z.
	if want := utc(2008, time.January, 5, 19, 25, 0); fs.UnixMs != want {
		t.Errorf("UnixMs = %d (%s), want %d", fs.UnixMs, time.UnixMilli(fs.UnixMs).UTC(), want)
	}
	// No ktxstats block, so the corroborating stamp is the only match-end
	// wall clock this demo has.
	if g.MatchEndUnixMs != fs.UnixMs {
		t.Errorf("MatchEndUnixMs = %d, want %d", g.MatchEndUnixMs, fs.UnixMs)
	}
}

// A stamp that names a different day is a real disagreement, and downgrades
// the anchor exactly like any other cross-marker disagreement — one soft
// signal, never a drop.
func TestWallClockFinalScoresDisagreement(t *testing.T) {
	const matchLen = int32(20 * 60 * 1000)
	res, co := wallClockResultWithFinalScores("2008-01-05 20:05:38 CET", "Jan 07, 20:25", matchLen)
	wallClockPost(res, co)

	g := res.Streams.Global
	if g.MatchStartConfidence != wallConfUnverified {
		t.Errorf("MatchStartConfidence = %q, want unverified", g.MatchStartConfidence)
	}
	if g.MatchStartSource != wallSourceMatchDate {
		t.Errorf("MatchStartSource = %q, want matchdate", g.MatchStartSource)
	}
	if want := "marker-disagreement: matchdate vs finalscores"; g.MatchStartNote != want {
		t.Errorf("MatchStartNote = %q, want %q", g.MatchStartNote, want)
	}
}

// Standing alone the stamp anchors nothing: no year, no instant, no anchor —
// but it is still reported, because a marker seen is a marker published.
func TestWallClockFinalScoresNeverStandsAlone(t *testing.T) {
	res, co := wallClockResultWithFinalScores("", "Jan 05, 20:25", 20*60*1000)
	wallClockPost(res, co)

	g := res.Streams.Global
	if g.MatchStartUnixMs != 0 || g.MatchStartSource != "" {
		t.Errorf("anchored on a year-less stamp: %+v", g)
	}
	if len(g.DateMarkers) != 1 {
		t.Fatalf("markers = %+v", g.DateMarkers)
	}
	if m := g.DateMarkers[0]; m.Source != wallSourceFinalScores || m.UnixMs != 0 || m.YearFrom != "" || m.Raw != "Jan 05, 20:25" {
		t.Errorf("unresolved marker = %+v", m)
	}
}

// With no zone on the anchoring marker the stamp inherits the same "assumed
// UTC" reading, and the comparison falls back to whole-timezone slack instead
// of manufacturing a disagreement out of an unknown offset.
func TestWallClockFinalScoresZonelessAnchor(t *testing.T) {
	const matchLen = int32(20 * 60 * 1000)
	res, co := wallClockResultWithFinalScores("", "Aug 13, 20:16", matchLen)
	m, ok := parseDateMarkerPrint("matchkey: 8-2005-8-13:19-56-18", 0)
	if !ok {
		t.Fatal("matchkey fixture")
	}
	co.Clock.DateMarkers = []WallClockMarker{m}
	wallClockPost(res, co)

	g := res.Streams.Global
	// matchkey has no zone, so the anchor was already "unverified" before the
	// stamp arrived — the stamp must not push it further.
	if g.MatchStartConfidence != wallConfUnverified {
		t.Errorf("MatchStartConfidence = %q (%q), want unverified", g.MatchStartConfidence, g.MatchStartNote)
	}
	if g.MatchStartNote != "tz-unknown: the marker named no timezone, UTC assumed" {
		t.Errorf("MatchStartNote = %q", g.MatchStartNote)
	}
	fs := g.DateMarkers[1]
	if !fs.AssumedUTC || fs.YearFrom != wallSourceMatchKey {
		t.Errorf("finalscores marker = %+v", fs)
	}
	if want := utc(2005, time.August, 13, 20, 16, 0); fs.UnixMs != want {
		t.Errorf("UnixMs = %d, want %d", fs.UnixMs, want)
	}
}
