package analyzer

import (
	"strings"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// utc is the expected-instant constructor for the table tests.
func utc(y int, mo time.Month, d, h, mi, s int) int64 {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC).UnixMilli()
}

// TestParseDateMarkerPrint covers both matchdate layouts, the matchkey layout,
// and every timezone form the archive shows: European abbreviations, US
// abbreviations, numeric offsets, the Swedish locale strings (as they arrive
// after Q_normalizetext folds the high-bit 'ä' to 'd'), and no zone at all.
func TestParseDateMarkerPrint(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantSource string
		wantUnix   int64
		wantTZ     string
		wantAssume bool
	}{
		{"iso CET", "matchdate: 2008-01-05 20:05:38 CET", wallSourceMatchDate,
			utc(2008, time.January, 5, 19, 5, 38), "CET", false},
		{"iso CEST", "matchdate: 2011-07-05 20:05:38 CEST", wallSourceMatchDate,
			utc(2011, time.July, 5, 18, 5, 38), "CEST", false},
		{"iso EET", "matchdate: 2011-01-05 20:05:38 EET", wallSourceMatchDate,
			utc(2011, time.January, 5, 18, 5, 38), "EET", false},
		{"iso EEST", "matchdate: 2011-07-05 20:05:38 EEST", wallSourceMatchDate,
			utc(2011, time.July, 5, 17, 5, 38), "EEST", false},
		{"iso UTC", "matchdate: 2025-01-31 02:04:23 UTC", wallSourceMatchDate,
			utc(2025, time.January, 31, 2, 4, 23), "UTC", false},
		{"iso GMT", "matchdate: 2016-03-01 09:00:00 GMT", wallSourceMatchDate,
			utc(2016, time.March, 1, 9, 0, 0), "GMT", false},
		{"iso EDT", "matchdate: 2009-06-01 12:00:00 EDT", wallSourceMatchDate,
			utc(2009, time.June, 1, 16, 0, 0), "EDT", false},
		{"iso offset +03", "matchdate: 2019-05-04 21:00:00 +03", wallSourceMatchDate,
			utc(2019, time.May, 4, 18, 0, 0), "+03", false},
		{"iso offset +0200", "matchdate: 2019-05-04 21:00:00 +0200", wallSourceMatchDate,
			utc(2019, time.May, 4, 19, 0, 0), "+0200", false},
		{"iso offset -05:00", "matchdate: 2019-05-04 21:00:00 -05:00", wallSourceMatchDate,
			utc(2019, time.May, 5, 2, 0, 0), "-05:00", false},
		{"swedish summer", "matchdate: 2010-07-05 20:05:38 Vdsteuropa, sommartid", wallSourceMatchDate,
			utc(2010, time.July, 5, 18, 5, 38), "Vdsteuropa, sommartid", false},
		{"swedish standard", "matchdate: 2010-01-05 20:05:38 Vdsteuropa, normaltid", wallSourceMatchDate,
			utc(2010, time.January, 5, 19, 5, 38), "Vdsteuropa, normaltid", false},
		{"iso no zone", "matchdate: 2010-01-05 20:05:38", wallSourceMatchDate,
			utc(2010, time.January, 5, 20, 5, 38), "", true},
		{"iso unknown zone", "matchdate: 2010-01-05 20:05:38 XYZ", wallSourceMatchDate,
			utc(2010, time.January, 5, 20, 5, 38), "XYZ", true},
		{"ctime", "matchdate: Mon Jul 03, 01:01:14 2006", wallSourceMatchDate,
			utc(2006, time.July, 3, 1, 1, 14), "", true},
		{"ctime with zone", "matchdate: Thu Jan 04, 22:14:21 2007 CET", wallSourceMatchDate,
			utc(2007, time.January, 4, 21, 14, 21), "CET", false},
		{"matchkey", "matchkey: 8-2005-8-13:19-56-18", wallSourceMatchKey,
			utc(2005, time.August, 13, 19, 56, 18), "", true},
		{"matchkey 4-digit id", "matchkey: 1019-2004-4-10:22-25-27", wallSourceMatchKey,
			utc(2004, time.April, 10, 22, 25, 27), "", true},
		{"matchkey unpadded hour", "matchkey: 125-2002-2-13:0-52-49", wallSourceMatchKey,
			utc(2002, time.February, 13, 0, 52, 49), "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok := parseDateMarkerPrint(c.line, 4242)
			if !ok {
				t.Fatalf("parseDateMarkerPrint(%q) failed", c.line)
			}
			if m.Source != c.wantSource {
				t.Errorf("Source = %q, want %q", m.Source, c.wantSource)
			}
			if m.Kind != wallKindMatchStart {
				t.Errorf("Kind = %q, want %q", m.Kind, wallKindMatchStart)
			}
			if m.UnixMs != c.wantUnix {
				t.Errorf("UnixMs = %d (%s), want %d (%s)", m.UnixMs,
					time.UnixMilli(m.UnixMs).UTC(), c.wantUnix, time.UnixMilli(c.wantUnix).UTC())
			}
			if m.TZ != c.wantTZ {
				t.Errorf("TZ = %q, want %q", m.TZ, c.wantTZ)
			}
			if m.AssumedUTC != c.wantAssume {
				t.Errorf("AssumedUTC = %v, want %v", m.AssumedUTC, c.wantAssume)
			}
			if m.AtMs != 4242 {
				t.Errorf("AtMs = %d, want 4242", m.AtMs)
			}
		})
	}
}

// TestParseDateMarkerPrintRejects pins the lines that must NOT produce a
// marker: other prints, and stamps whose fields are out of range or truncated
// (a corrupted stuffed value is a real archive phenomenon).
func TestParseDateMarkerPrintRejects(t *testing.T) {
	lines := []string{
		"",
		"someone rocketed someone else",
		"matchdate:",
		"matchdate: not a date at all",
		"matchdate: 2008-13-05 20:05:38 CET", // month 13
		"matchdate: 2008-01-05 25:05:38 CET", // hour 25
		"matchkey: 8-2005-8-13",              // no time half
		"matchkey: x-2005-8-13:19-56-18",     // non-numeric match id
		"matchkey: 8-2005-8:19-56-18",        // short date half
		"the matchdate: 2008-01-05 20:05:38 CET is late",
	}
	for _, line := range lines {
		if m, ok := parseDateMarkerPrint(line, 0); ok {
			t.Errorf("parseDateMarkerPrint(%q) = %+v, want no marker", line, m)
		}
	}
}

// TestParseKTXStatsDate covers the ktxstats `date` string (%Y-%m-%d %H:%M:%S
// %z) and the %Z variant some builds print there instead.
func TestParseKTXStatsDate(t *testing.T) {
	cases := []struct {
		raw      string
		wantUnix int64
		wantAcc  int32
	}{
		{"2021-03-06 21:53:41 +0100", utc(2021, time.March, 6, 20, 53, 41), stampAccuracyMs},
		{"2025-01-31 02:07:23 +0000", utc(2025, time.January, 31, 2, 7, 23), stampAccuracyMs},
		{"2019-08-11 12:00:00 -0430", utc(2019, time.August, 11, 16, 30, 0), stampAccuracyMs},
		{"2013-05-01 18:30:00 CEST", utc(2013, time.May, 1, 16, 30, 0), stampAccuracyMs},
		{"2013-05-01 18:30:00", utc(2013, time.May, 1, 18, 30, 0), tzUnknownAccuracyMs},
	}
	for _, c := range cases {
		st, ok := parseKTXStatsDate(c.raw)
		if !ok {
			t.Fatalf("parseKTXStatsDate(%q) failed", c.raw)
		}
		if st.UnixMs != c.wantUnix {
			t.Errorf("parseKTXStatsDate(%q) = %s, want %s", c.raw,
				time.UnixMilli(st.UnixMs).UTC(), time.UnixMilli(c.wantUnix).UTC())
		}
		if st.AccuracyMs != c.wantAcc {
			t.Errorf("parseKTXStatsDate(%q) accuracy = %d, want %d", c.raw, st.AccuracyMs, c.wantAcc)
		}
	}
	if _, ok := parseKTXStatsDate(""); ok {
		t.Error("parseKTXStatsDate(\"\") should fail")
	}
}

// TestResolveZoneOffset pins the offset table itself, including the Windows
// long names whose DST state is unstated (an hour of residual uncertainty) and
// the tokens that must stay unrecognised so the caller assumes UTC and says so.
func TestResolveZoneOffset(t *testing.T) {
	known := map[string]int{
		"UTC": 0, "GMT": 0, "utc": 0,
		"CET": 3600, "CEST": 7200, "EET": 7200, "EEST": 10800,
		"MSK": 10800, "EST": -5 * 3600, "PST": -8 * 3600,
		"+03": 3 * 3600, "+0200": 2 * 3600, "-05:00": -5 * 3600,
		"UTC+02:00": 2 * 3600, "GMT-5": -5 * 3600,
		"Vdsteuropa, sommartid": 7200, "Vdsteuropa, normaltid": 3600,
	}
	for tok, want := range known {
		off, uncertainty, ok := resolveZoneOffset(tok)
		if !ok {
			t.Errorf("resolveZoneOffset(%q) not recognised", tok)
			continue
		}
		if off != want {
			t.Errorf("resolveZoneOffset(%q) = %d, want %d", tok, off, want)
		}
		if uncertainty != 0 {
			t.Errorf("resolveZoneOffset(%q) uncertainty = %d, want 0", tok, uncertainty)
		}
	}

	off, uncertainty, ok := resolveZoneOffset("Central European Standard Time")
	if !ok || off != 3600 || uncertainty != tzDSTAccuracyMs {
		t.Errorf("Windows long name = (%d, %d, %v), want (3600, %d, true)", off, uncertainty, ok, tzDSTAccuracyMs)
	}

	for _, tok := range []string{"", "XYZ", "Mars/Olympus", "+", "+99"} {
		if _, _, ok := resolveZoneOffset(tok); ok {
			t.Errorf("resolveZoneOffset(%q) should be unrecognised", tok)
		}
	}
}

// TestServerReleaseFloor pins the version → release-floor lookup, including
// suffix collapsing and the bare QuakeWorld protocol version whose floor is
// deliberately loose.
func TestServerReleaseFloor(t *testing.T) {
	cases := []struct {
		info      map[string]string
		wantLabel string
		wantFloor int64
	}{
		{map[string]string{"*version": "MVDSV 0.28b"}, "mvdsv 0.28", utcDate(2006, time.January, 1)},
		{map[string]string{"*version": "MVDSV 0.36-beta-antilag-r402"}, "mvdsv 0.36", utcDate(2021, time.January, 1)},
		{map[string]string{"*version": "mvdsv 0.19.10-develop"}, "mvdsv 0.19", utcDate(2005, time.January, 1)},
		{map[string]string{"*version": "MVDSV 0.27 rev.643"}, "mvdsv 0.27", utcDate(2006, time.January, 1)},
		{map[string]string{"*version": "2.40"}, "quakeworld 2.40", qwProtocolFloor},
		{map[string]string{"*version": "2.30"}, "quakeworld 2.30", qwProtocolFloor},
		// The tightest bound wins: KTX 1.42 postdates MVDSV 0.33.
		{map[string]string{"*version": "MVDSV 0.33", "ktxver": "1.42"}, "ktx 1.42", utcDate(2021, time.January, 1)},
		{map[string]string{"ktxver": "KTX 1.43, with Quake Smash Mod changes by KovaaK"}, "ktx 1.43", utcDate(2021, time.January, 1)},
		// An unrecognised or absent version still bounds the date at
		// QuakeWorld's own release.
		{map[string]string{"*version": "SomethingElse 9.9"}, "quakeworld", qwProtocolFloor},
		{nil, "quakeworld", qwProtocolFloor},
	}
	for _, c := range cases {
		got := serverReleaseFloor(c.info)
		if got.label != c.wantLabel || got.unixMs != c.wantFloor {
			t.Errorf("serverReleaseFloor(%v) = %q/%s, want %q/%s", c.info,
				got.label, time.UnixMilli(got.unixMs).UTC(),
				c.wantLabel, time.UnixMilli(c.wantFloor).UTC())
		}
	}
}

// TestResolveWallClockTrust is the trust model's table: the hard
// version-release check, the soft signals that only downgrade in combination,
// and the rule that nothing is ever dropped.
func TestResolveWallClockTrust(t *testing.T) {
	marker := func(source string, unix int64, tz string, assumed bool) dateMarker {
		return dateMarker{Source: source, Kind: wallKindMatchStart, UnixMs: unix, TZ: tz, AssumedUTC: assumed}
	}
	unset := utc(2000, time.January, 7, 3, 11, 49) // the epoch-reset batch date

	cases := []struct {
		name       string
		in         wallClockInputs
		wantSource string
		wantUnix   int64
		wantConf   string
		wantNote   string // substring
		wantAcc    int32
	}{
		{
			name: "mid-2000s mvdsv stamping 2000-01-07 is contradicted",
			in: wallClockInputs{
				Markers:    []dateMarker{marker(wallSourceMatchKey, unset, "", true)},
				ServerInfo: map[string]string{"*version": "MVDSV 0.28"},
			},
			wantSource: wallSourceMatchKey, wantUnix: unset,
			wantConf: wallConfContradicted, wantNote: "version-floor: mvdsv 0.28",
			wantAcc: tzUnknownAccuracyMs,
		},
		{
			name: "QW 2.40 stamping 2000-01-07 is not contradicted",
			in: wallClockInputs{
				Markers:    []dateMarker{marker(wallSourceMatchKey, unset, "", true)},
				ServerInfo: map[string]string{"*version": "2.40"},
			},
			wantSource: wallSourceMatchKey, wantUnix: unset,
			wantConf: wallConfUnverified, wantNote: "epoch-reset-window",
			wantAcc: tzUnknownAccuracyMs,
		},
		{
			name: "matchdate with a resolved zone and nothing against it is exact",
			in: wallClockInputs{
				Markers:    []dateMarker{marker(wallSourceMatchDate, utc(2008, time.January, 5, 19, 5, 38), "CET", false)},
				ServerInfo: map[string]string{"*version": "MVDSV 0.28"},
			},
			wantSource: wallSourceMatchDate, wantUnix: utc(2008, time.January, 5, 19, 5, 38),
			wantConf: wallConfExact, wantAcc: stampAccuracyMs,
		},
		{
			name: "an unzoned marker is unverified, never dropped",
			in: wallClockInputs{
				Markers: []dateMarker{marker(wallSourceMatchKey, utc(2005, time.August, 13, 19, 56, 18), "", true)},
			},
			wantSource: wallSourceMatchKey, wantUnix: utc(2005, time.August, 13, 19, 56, 18),
			wantConf: wallConfUnverified, wantNote: "tz-unknown", wantAcc: tzUnknownAccuracyMs,
		},
		{
			name: "two markers a whole timezone apart still agree when one is unzoned",
			in: wallClockInputs{
				Markers: []dateMarker{
					marker(wallSourceMatchDate, utc(2008, time.January, 5, 19, 5, 38), "CET", false),
					marker(wallSourceMatchKey, utc(2008, time.January, 5, 20, 5, 38), "", true),
				},
				ServerInfo: map[string]string{"*version": "MVDSV 0.28"},
			},
			wantSource: wallSourceMatchDate, wantUnix: utc(2008, time.January, 5, 19, 5, 38),
			wantConf: wallConfExact, wantAcc: stampAccuracyMs,
		},
		{
			name: "an off-quantum disagreement alone only downgrades to unverified",
			in: wallClockInputs{
				Markers: []dateMarker{
					marker(wallSourceMatchDate, utc(2008, time.January, 5, 19, 5, 38), "CET", false),
					marker(wallSourceMatchKey, utc(2008, time.January, 5, 21, 38, 11), "", true),
				},
				ServerInfo: map[string]string{"*version": "MVDSV 0.28"},
			},
			wantSource: wallSourceMatchDate, wantUnix: utc(2008, time.January, 5, 19, 5, 38),
			wantConf: wallConfUnverified, wantNote: "marker-disagreement: matchdate vs matchkey",
			wantAcc: stampAccuracyMs,
		},
		{
			name: "disagreement plus the epoch-reset window contradicts",
			in: wallClockInputs{
				Markers: []dateMarker{
					marker(wallSourceMatchDate, unset, "CET", false),
					marker(wallSourceMatchKey, utc(2008, time.January, 5, 21, 38, 11), "", true),
				},
				ServerInfo: map[string]string{"*version": "2.40"},
			},
			wantSource: wallSourceMatchDate, wantUnix: unset,
			wantConf: wallConfContradicted, wantNote: "epoch-reset-window",
			wantAcc: stampAccuracyMs,
		},
		{
			name: "the server clock outranks the prints and grades exact",
			in: wallClockInputs{
				Markers:       []dateMarker{marker(wallSourceMatchDate, utc(2025, time.January, 31, 2, 4, 23), "UTC", false)},
				DemoStartUnix: utc(2025, time.January, 31, 2, 4, 13),
				DemoStartAcc:  1000,
				DemoStartSrc:  wallSourceEpoch,
				DemoOffsetMs:  10000,
			},
			wantSource: wallSourceEpoch, wantUnix: utc(2025, time.January, 31, 2, 4, 23),
			wantConf: wallConfExact, wantAcc: 1000,
		},
		{
			name: "a stamp predating QuakeWorld itself is contradicted with no version key",
			in: wallClockInputs{
				Markers: []dateMarker{marker(wallSourceMatchDate, utc(1994, time.March, 1, 12, 0, 0), "CET", false)},
			},
			wantSource: wallSourceMatchDate, wantUnix: utc(1994, time.March, 1, 12, 0, 0),
			wantConf: wallConfContradicted, wantNote: "version-floor: quakeworld was not released before 1996-06-22",
			wantAcc: stampAccuracyMs,
		},
		{
			name: "a stamp past 2100 is contradicted as corrupt",
			in: wallClockInputs{
				Markers: []dateMarker{marker(wallSourceMatchDate, utc(2145, time.March, 1, 12, 0, 0), "UTC", false)},
			},
			wantSource: wallSourceMatchDate, wantUnix: utc(2145, time.March, 1, 12, 0, 0),
			wantConf: wallConfContradicted, wantNote: "impossible-date",
			wantAcc: stampAccuracyMs,
		},
		{
			name: "ktxstats alone anchors match start by walking back the match length",
			in: wallClockInputs{
				Markers: []dateMarker{{
					Source: wallSourceKTXStats, Kind: wallKindMatchEnd,
					UnixMs: utc(2021, time.March, 6, 20, 53, 41), TZ: "+0100",
				}},
				MatchLengthMs: 600000,
			},
			wantSource: wallSourceKTXStats, wantUnix: utc(2021, time.March, 6, 20, 43, 41),
			wantConf: wallConfExact, wantAcc: stampAccuracyMs,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveWallClock(c.in)
			if got.Source != c.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, c.wantSource)
			}
			if got.MatchStartUnixMs != c.wantUnix {
				t.Errorf("MatchStartUnixMs = %s, want %s",
					time.UnixMilli(got.MatchStartUnixMs).UTC(), time.UnixMilli(c.wantUnix).UTC())
			}
			if got.Confidence != c.wantConf {
				t.Errorf("Confidence = %q (note %q), want %q", got.Confidence, got.Note, c.wantConf)
			}
			if got.AccuracyMs != c.wantAcc {
				t.Errorf("AccuracyMs = %d, want %d", got.AccuracyMs, c.wantAcc)
			}
			if c.wantNote != "" && !strings.Contains(got.Note, c.wantNote) {
				t.Errorf("Note = %q, want it to mention %q", got.Note, c.wantNote)
			}
			if c.wantConf == wallConfExact && got.Note != "" {
				t.Errorf("Note = %q, want empty on an exact grade", got.Note)
			}
		})
	}
}

// TestResolveWallClockDemoStartBackfill pins the back-fill rule: a trusted
// marker becomes the demo-open anchor (so the documented wallClockMs formula
// keeps working), a contradicted one does not, and an anchor the demo already
// had is never overwritten.
func TestResolveWallClockDemoStartBackfill(t *testing.T) {
	start := utc(2008, time.January, 5, 19, 5, 38)
	trusted := wallClockInputs{
		Markers: []dateMarker{{
			Source: wallSourceMatchDate, Kind: wallKindMatchStart,
			UnixMs: start, AtMs: 10000, TZ: "CET",
		}},
		ServerInfo:   map[string]string{"*version": "MVDSV 0.28"},
		DemoOffsetMs: 10000,
	}
	got := resolveWallClock(trusted)
	if got.MatchStartUnixMs != start {
		t.Fatalf("MatchStartUnixMs = %s, want %s", time.UnixMilli(got.MatchStartUnixMs).UTC(), time.UnixMilli(start).UTC())
	}
	if got.DemoStartUnixMs != start-10000 || got.DemoStartSource != wallSourceMatchDate {
		t.Errorf("demo-start back-fill = %d/%q, want %d/%q",
			got.DemoStartUnixMs, got.DemoStartSource, start-10000, wallSourceMatchDate)
	}

	contradicted := trusted
	contradicted.Markers = []dateMarker{{
		Source: wallSourceMatchKey, Kind: wallKindMatchStart,
		UnixMs: utc(2000, time.January, 7, 3, 11, 49), AtMs: 10000, AssumedUTC: true,
	}}
	got = resolveWallClock(contradicted)
	if got.Confidence != wallConfContradicted {
		t.Fatalf("Confidence = %q, want %q", got.Confidence, wallConfContradicted)
	}
	if got.DemoStartUnixMs != 0 {
		t.Errorf("contradicted stamp back-filled demoStart = %d, want 0", got.DemoStartUnixMs)
	}
	if got.MatchStartUnixMs == 0 {
		t.Error("contradicted stamp was dropped from matchStart; it must still be reported")
	}

	withServerClock := trusted
	withServerClock.DemoStartUnix = utc(2008, time.January, 5, 19, 5, 28)
	withServerClock.DemoStartAcc = 1
	withServerClock.DemoStartSrc = wallSourceHidden
	got = resolveWallClock(withServerClock)
	if got.DemoStartUnixMs != 0 {
		t.Errorf("back-filled over an existing anchor (%d); want no back-fill", got.DemoStartUnixMs)
	}
	if got.Source != wallSourceHidden {
		t.Errorf("Source = %q, want %q", got.Source, wallSourceHidden)
	}
}

// TestResolveWallClockMarkerShift pins the projection: a marker states the
// instant of its own print, so the anchor is the stamp minus the print's demo
// time plus DemoOffset. It matters on a demo whose match-start print did not
// land on the match-start frame, and on a TimeBase=="demo" result where
// DemoOffset is 0 and g=0 is demo open.
func TestResolveWallClockMarkerShift(t *testing.T) {
	stamp := utc(2011, time.July, 5, 18, 5, 38)
	in := wallClockInputs{
		Markers: []dateMarker{{
			Source: wallSourceMatchDate, Kind: wallKindMatchStart,
			UnixMs: stamp, AtMs: 30000, TZ: "UTC",
		}},
		DemoOffsetMs: 12000,
	}
	got := resolveWallClock(in)
	if want := stamp - 30000 + 12000; got.MatchStartUnixMs != want {
		t.Errorf("MatchStartUnixMs = %d, want %d", got.MatchStartUnixMs, want)
	}
	if want := stamp - 30000; got.DemoStartUnixMs != want {
		t.Errorf("DemoStartUnixMs = %d, want %d", got.DemoStartUnixMs, want)
	}

	in.DemoOffsetMs = 0 // TimeBase == "demo": no match start detected
	got = resolveWallClock(in)
	if want := stamp - 30000; got.MatchStartUnixMs != want {
		t.Errorf("TimeBase=demo MatchStartUnixMs = %d, want %d (demo open)", got.MatchStartUnixMs, want)
	}
}

// TestResolveWallClockNoMarkers guards the empty case: no marker, no anchor,
// and no invented fields.
func TestResolveWallClockNoMarkers(t *testing.T) {
	got := resolveWallClock(wallClockInputs{ServerInfo: map[string]string{"*version": "MVDSV 0.28"}})
	if got != (wallClockAnchor{}) {
		t.Errorf("resolveWallClock(no markers) = %+v, want zero", got)
	}
}

// TestWallClockPostKTXStatsOverPause runs the post-processor end to end on a
// paused match. The ktxstats date is stamped at intermission off a clock that
// kept running through the pause, while MatchEnd is game time — so walking the
// stamp back to match start has to cross the pause too. Without that the
// ktxstats candidate lands a full pause late and contradicts the (correct)
// server-clock anchor.
func TestWallClockPostKTXStatsOverPause(t *testing.T) {
	const (
		demoStart = int64(1779826000000)
		offset    = int32(10000)
		matchEnd  = int32(1200019)
		pause     = int32(126503)
	)
	res := &Result{
		Streams: &Streams{Global: GlobalStream{
			MatchEnd:            matchEnd,
			DemoOffset:          offset,
			DemoStartUnixMs:     demoStart,
			DemoStartAccuracyMs: 1,
			Pauses:              []TimelinePause{{AtMs: 631486, DurationMs: pause}},
		}},
		DemoInfo: &DemoInfoResult{
			Date: time.UnixMilli(demoStart + int64(offset) + int64(matchEnd) + int64(pause)).
				UTC().Format("2006-01-02 15:04:05 +0000"),
		},
	}
	co := &CoreOutputs{Clock: &Clock{DemoStartSource: wallSourceHidden}}
	wallClockPost(res, co)

	g := res.Streams.Global
	if g.MatchStartConfidence != wallConfExact {
		t.Errorf("MatchStartConfidence = %q (%q), want %q", g.MatchStartConfidence, g.MatchStartNote, wallConfExact)
	}
	if want := demoStart + int64(offset); g.MatchStartUnixMs != want {
		t.Errorf("MatchStartUnixMs = %d, want %d", g.MatchStartUnixMs, want)
	}
	if g.MatchStartSource != wallSourceHidden || g.DemoStartSource != wallSourceHidden {
		t.Errorf("sources = %q/%q, want %q", g.MatchStartSource, g.DemoStartSource, wallSourceHidden)
	}
	if g.MatchEndUnixMs == 0 || len(g.DateMarkers) != 1 || g.DateMarkers[0].Source != wallSourceKTXStats {
		t.Errorf("ktxstats marker not reported: end=%d markers=%+v", g.MatchEndUnixMs, g.DateMarkers)
	}
	if g.DemoStartUnixMs != demoStart {
		t.Errorf("DemoStartUnixMs = %d, want the server clock %d untouched", g.DemoStartUnixMs, demoStart)
	}
}

// TestClockCollectsDateMarkers checks the event-pass half: level-2 broadcast
// prints reach the date parser, repeats of one stamp collapse, and obituary
// lines at the same level are untouched by the collector.
func TestClockCollectsDateMarkers(t *testing.T) {
	a := NewClockAnalyzer()
	prints := []struct {
		level  int
		msg    string
		timeMs int32
	}{
		{2, "matchdate: 2008-01-05 20:05:38 CET\n", 10000},
		{2, "matchdate: 2008-01-05 20:05:38 CET\n", 10000}, // duplicate copy
		{2, "player was gibbed by rocket\n", 12000},
		{2, "matchkey: 8-2005-8-13:19-56-18\n", 15000},
		{3, "matchdate: 2019-01-01 00:00:00 CET\n", 16000}, // chat level, not a marker
	}
	for _, p := range prints {
		if err := a.OnEvent(&events.PrintEvent{Level: p.level, Message: p.msg, TimeMs: p.timeMs}); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	co := &CoreOutputs{}
	a.PopulateCore(co)
	if got := len(co.Clock.DateMarkers); got != 2 {
		t.Fatalf("collected %d markers, want 2: %+v", got, co.Clock.DateMarkers)
	}
	if co.Clock.DateMarkers[0].Source != wallSourceMatchDate || co.Clock.DateMarkers[0].AtMs != 10000 {
		t.Errorf("marker 0 = %+v", co.Clock.DateMarkers[0])
	}
	if co.Clock.DateMarkers[1].Source != wallSourceMatchKey || co.Clock.DateMarkers[1].AtMs != 15000 {
		t.Errorf("marker 1 = %+v", co.Clock.DateMarkers[1])
	}
}
