package analyzer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Wall-clock anchoring from the date markers a QuakeWorld server puts on the
// wire. Three of them exist, in three different eras:
//
//   - `matchdate: 2008-01-05 20:05:38 CET` — KTX bprints this at match start,
//     one frame before "The match has begun!" (ktx/src/match.c:1291), with the
//     strftime layout "%Y-%m-%d %H:%M:%S %Z". Older builds print the ctime
//     layout instead (`matchdate: Mon Jul 03, 01:01:14 2006`).
//   - `matchkey: 8-2005-8-13:19-56-18` — the kmod / KTeam-era predecessor,
//     also a level-2 broadcast print, `<matchid>-<y>-<m>-<d>:<h>-<mm>-<ss>`
//     with no timezone at all.
//   - the `date` field of the KTX demoinfo (ktxstats) block — written when
//     the block is dumped at intermission, so it names match END.
//   - `//finalscores "Sep 29, 21:27" …` — the end-of-match stuffcmd
//     (ktx/src/commands.c:6969). Its strftime layout carries NO YEAR, no
//     seconds and no zone, so it can never anchor a match on its own; it is
//     collected as a corroborating marker whose year is completed from
//     whichever marker did anchor it, and whose month/day/hour/minute then
//     cross-check that anchor. Coverage 64.1% of the archive.
//
// matchdate and matchkey are near-perfectly complementary across the 51k-demo
// archive (69% / 29%), which is what lifts wall-clock coverage from the ~25%
// the server-clock anchors reach (mvdhidden 0x000B, serverinfo `epoch`) to
// ~95% (measured on a 260-demo stratified sample, archive-weighted).
//
// TRUST. Old servers ran with unset clocks, so some stamps are wrong — but the
// value alone never proves it (2000 is a live QuakeWorld year, and a whole LAN
// night can legitimately sit in one January). Gating is therefore on
// CONTRADICTION only:
//
//   - HARD: a binary cannot predate its own release. `*version` / `ktxver` name
//     the binary, so a stamp before that build's release floor is provably an
//     unset clock. This is the check that catches the 2000-01-07 batches.
//   - SOFT: markers inside one demo disagreeing beyond timezone slack, and the
//     boot-default window (early 2000-01) the epoch-reset signature lands in.
//     Either one alone is indistinguishable from a real match, so a single soft
//     signal only downgrades to "unverified"; two of them contradict.
//
// Nothing is ever dropped: a failed check produces a graded anchor plus a note
// naming the check, and every marker seen is reported in Global.DateMarkers.

// Wall-clock marker sources (GlobalStream.MatchStartSource / DemoStartSource
// and WallClockMarker.Source).
const (
	wallSourceHidden      = "mvdhidden"
	wallSourceEpoch       = "epoch"
	wallSourceMatchDate   = "matchdate"
	wallSourceMatchKey    = "matchkey"
	wallSourceKTXStats    = "ktxstats"
	wallSourceFinalScores = "finalscores"
)

// Marker kinds (WallClockMarker.Kind).
const (
	wallKindMatchStart = "matchStart"
	wallKindMatchEnd   = "matchEnd"
)

// Confidence grades (GlobalStream.MatchStartConfidence).
const (
	wallConfExact        = "exact"
	wallConfUnverified   = "unverified"
	wallConfContradicted = "contradicted"
)

const (
	// tzUnknownAccuracyMs is the uncertainty carried by a stamp with no
	// timezone: real server offsets span UTC-12..UTC+14, so reading it as UTC
	// is right to within the widest of those, 14 hours, and no better. It is
	// the same bound maxTZSpanMs states for the agreement comparison — an
	// accuracy narrower than the span the comparison allows for would be
	// claiming a precision the assumption does not have.
	tzUnknownAccuracyMs = 14 * 60 * 60 * 1000
	// tzDSTAccuracyMs is the uncertainty of a zone NAME that does not say
	// whether daylight saving was in force (the Windows "… Standard Time"
	// long names, which some builds print year-round).
	tzDSTAccuracyMs = 60 * 60 * 1000
	// stampAccuracyMs is the resolution of a marker with a resolved zone —
	// every layout prints whole seconds.
	stampAccuracyMs = 1000

	// markerAgreeToleranceMs is how far two markers describing the same
	// instant may sit apart before it counts as a disagreement. The stamps are
	// whole seconds and are emitted within a frame of each other, so this is
	// pure slack for print ordering and clock granularity.
	markerAgreeToleranceMs = 120 * 1000
	// ktxStatsToleranceMs is the slack for any comparison involving the
	// ktxstats date. Unlike the prints, its instant is not pinned to a frame of
	// the match: the block is written when the stats are dumped, after
	// intermission and after any pause the server did not embed a
	// paused_duration block for (only current mvdsv does). Observed spreads run
	// to ~3.5 min on demos whose two other markers agree to the second.
	ktxStatsToleranceMs = 300 * 1000
	// finalScoresToleranceMs is the slack for the `//finalscores` stamp: it is
	// stuffed from the same end-of-match code path as the ktxstats block, so it
	// inherits that spread, plus the whole minute its layout truncates to.
	finalScoresToleranceMs = ktxStatsToleranceMs + 60*1000
	// finalScoresAccuracyMs is the resolution of a `//finalscores` stamp whose
	// zone was borrowed from the anchoring marker — one minute, the layout's
	// granularity.
	finalScoresAccuracyMs = 60 * 1000
	// tzQuantumMs is the granularity every real timezone offset is a multiple
	// of — two markers whose difference is a whole quantum agree up to an
	// unknown zone, which is the only comparison available when one of them
	// printed no zone.
	tzQuantumMs = 15 * 60 * 1000
	// maxTZSpanMs bounds that reasoning: beyond UTC-12..UTC+14 no timezone can
	// explain the gap, so it is a real disagreement whatever the quantum says.
	maxTZSpanMs = 14 * 60 * 60 * 1000

	// epochResetWindowStart/End bracket the boot default an unset RTC comes up
	// with (observed across the archive as whole "nights" of 2000-01-0x). It is
	// a SOFT signal only: a real match played in early January 2000 is
	// indistinguishable from it.
	epochResetWindowStart = 946684800000 // 2000-01-01T00:00:00Z
	epochResetWindowEnd   = 949363200000 // 2000-02-01T00:00:00Z
)

// dateMarker is a marker as collected during the event pass: the parsed
// instant plus the demo-clock time of the print that carried it.
type dateMarker = WallClockMarker

// wallClockStamp is a parsed date stamp — the instant plus how well its
// timezone is pinned.
type wallClockStamp struct {
	UnixMs     int64
	TZ         string // zone token exactly as printed ("" when absent)
	AssumedUTC bool   // zone missing or unrecognised — read as UTC
	AccuracyMs int32
}

// parseDateMarkerPrint recognises the two broadcast date prints and returns
// the marker they carry. msg is one assembled, normalised print line; atMs its
// demo-clock time. Anything else returns ok=false.
func parseDateMarkerPrint(msg string, atMs int32) (dateMarker, bool) {
	line := strings.TrimSpace(msg)
	var source, raw string
	switch {
	case strings.HasPrefix(line, "matchdate:"):
		source, raw = wallSourceMatchDate, strings.TrimSpace(line[len("matchdate:"):])
	case strings.HasPrefix(line, "matchkey:"):
		source, raw = wallSourceMatchKey, strings.TrimSpace(line[len("matchkey:"):])
	default:
		return dateMarker{}, false
	}
	if raw == "" {
		return dateMarker{}, false
	}

	var stamp wallClockStamp
	var ok bool
	if source == wallSourceMatchDate {
		stamp, ok = parseMatchDateStamp(raw)
	} else {
		stamp, ok = parseMatchKeyStamp(raw)
	}
	if !ok {
		return dateMarker{}, false
	}
	return dateMarker{
		Source:     source,
		Kind:       wallKindMatchStart,
		UnixMs:     stamp.UnixMs,
		AtMs:       atMs,
		TZ:         stamp.TZ,
		AssumedUTC: stamp.AssumedUTC,
		Raw:        raw,
	}, true
}

// parseMatchDateStamp parses the two `matchdate:` layouts KTX has shipped:
// ISO ("2008-01-05 20:05:38 CET", %Y-%m-%d %H:%M:%S %Z) and ctime
// ("Mon Jul 03, 01:01:14 2006"). Either may carry a trailing zone token, and
// on the ctime layout it may be absent entirely.
func parseMatchDateStamp(raw string) (wallClockStamp, bool) {
	if civil, zone, ok := parseISODateStamp(raw); ok {
		return applyZone(civil, zone), true
	}
	if civil, zone, ok := parseCtimeDateStamp(raw); ok {
		return applyZone(civil, zone), true
	}
	return wallClockStamp{}, false
}

// parseISODateStamp splits "2008-01-05 20:05:38 CET" into its civil time and
// the zone remainder. The zone is whatever follows the seconds field — it can
// be an abbreviation, a numeric offset, or a locale sentence with spaces and
// commas in it ("Vdsteuropa, sommartid", the Swedish long name after
// high-bit normalisation).
func parseISODateStamp(raw string) (time.Time, string, bool) {
	datePart, rest, ok := splitField(raw)
	if !ok {
		return time.Time{}, "", false
	}
	timePart, zone, _ := splitField(rest)
	y, mo, d, ok := parseYMD(datePart, '-')
	if !ok {
		return time.Time{}, "", false
	}
	h, mi, s, ok := parseHMS(timePart, ':')
	if !ok {
		return time.Time{}, "", false
	}
	civil, ok := civilDate(y, mo, d, h, mi, s)
	if !ok {
		return time.Time{}, "", false
	}
	return civil, strings.TrimSpace(zone), true
}

// civilDate builds a zone-less civil instant from already range-checked
// components and rejects the dates that do not exist. time.Date NORMALISES an
// out-of-range day instead of failing — "2024-02-31" comes back as 2024-03-02 —
// so without this a corrupted stamp is published as a confident date two days
// off. The field ranges alone cannot catch it: 31 is a legal day and 2 a legal
// month, only the pair is impossible.
func civilDate(y, mo, d, h, mi, s int) (time.Time, bool) {
	t := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// ctimeMonths maps the abbreviated month names strftime's %b emits (C locale)
// to their ordinals.
var ctimeMonths = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// parseCtimeDateStamp splits "Mon Jul 03, 01:01:14 2006" (%a %b %d, %H:%M:%S
// %Y) into its civil time and any zone left over after the year.
func parseCtimeDateStamp(raw string) (time.Time, string, bool) {
	_, rest, ok := splitField(raw) // weekday — carries no information
	if !ok {
		return time.Time{}, "", false
	}
	monName, rest, ok := splitField(rest)
	if !ok {
		return time.Time{}, "", false
	}
	mo, ok := ctimeMonths[strings.ToLower(monName)]
	if !ok {
		return time.Time{}, "", false
	}
	dayField, rest, ok := splitField(rest)
	if !ok {
		return time.Time{}, "", false
	}
	d, err := strconv.Atoi(strings.TrimSuffix(dayField, ","))
	if err != nil {
		return time.Time{}, "", false
	}
	timePart, rest, ok := splitField(rest)
	if !ok {
		return time.Time{}, "", false
	}
	h, mi, s, ok := parseHMS(timePart, ':')
	if !ok {
		return time.Time{}, "", false
	}
	yearField, zone, _ := splitField(rest)
	y, err := strconv.Atoi(yearField)
	if err != nil {
		return time.Time{}, "", false
	}
	civil, ok := civilDate(y, mo, d, h, mi, s)
	if !ok {
		return time.Time{}, "", false
	}
	return civil, strings.TrimSpace(zone), true
}

// parseMatchKeyStamp parses `<matchid>-<yyyy>-<m>-<d>:<h>-<mm>-<ss>`
// ("8-2005-8-13:19-56-18"). The leading field is the server's match counter,
// not part of the date, and the layout never carries a zone — so the instant
// is read as UTC and marked assumed.
func parseMatchKeyStamp(raw string) (wallClockStamp, bool) {
	datePart, timePart, found := strings.Cut(raw, ":")
	if !found {
		return wallClockStamp{}, false
	}
	fields := strings.Split(datePart, "-")
	if len(fields) != 4 {
		return wallClockStamp{}, false
	}
	if _, err := strconv.Atoi(fields[0]); err != nil { // match id
		return wallClockStamp{}, false
	}
	y, mo, d, ok := parseYMD(strings.Join(fields[1:], "-"), '-')
	if !ok {
		return wallClockStamp{}, false
	}
	h, mi, s, ok := parseHMS(strings.TrimSpace(timePart), '-')
	if !ok {
		return wallClockStamp{}, false
	}
	civil, ok := civilDate(y, mo, d, h, mi, s)
	if !ok {
		return wallClockStamp{}, false
	}
	return applyZone(civil, ""), true
}

// parseKTXStatsDate parses the ktxstats `date` string, strftime
// "%Y-%m-%d %H:%M:%S %z" — the same ISO layout as matchdate but with a
// numeric offset ("2021-03-06 21:53:41 +0100"). Some builds print %Z there
// instead, which the shared zone resolver handles too.
func parseKTXStatsDate(raw string) (wallClockStamp, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return wallClockStamp{}, false
	}
	if civil, zone, ok := parseISODateStamp(raw); ok {
		return applyZone(civil, zone), true
	}
	if civil, zone, ok := parseCtimeDateStamp(raw); ok {
		return applyZone(civil, zone), true
	}
	return wallClockStamp{}, false
}

// finalScoresStamp is the year-less civil stamp `//finalscores` carries
// (strftime "%b %d, %H:%M" of the server's local clock). It is deliberately
// NOT a wallClockStamp: without a year there is no instant to hold, and the
// whole point of keeping it in this shape is that nothing downstream can
// mistake it for one.
type finalScoresStamp struct {
	month, day, hour, min int
	raw                   string
}

// parseFinalScoresDate parses "Sep 29, 21:27". Anything that is not that
// layout returns ok=false — the field is a fixed strftime output, so a
// deviation is a corrupted line rather than a variant to accommodate.
func parseFinalScoresDate(raw string) (finalScoresStamp, bool) {
	monName, rest, ok := splitField(strings.TrimSpace(raw))
	if !ok {
		return finalScoresStamp{}, false
	}
	mo, ok := ctimeMonths[strings.ToLower(monName)]
	if !ok {
		return finalScoresStamp{}, false
	}
	dayField, rest, ok := splitField(rest)
	if !ok {
		return finalScoresStamp{}, false
	}
	d, err := strconv.Atoi(strings.TrimSuffix(dayField, ","))
	if err != nil || d < 1 || d > 31 {
		return finalScoresStamp{}, false
	}
	timePart, _, ok := splitField(rest)
	if !ok {
		return finalScoresStamp{}, false
	}
	hh, mm, found := strings.Cut(timePart, ":")
	if !found {
		return finalScoresStamp{}, false
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 || h > 23 {
		return finalScoresStamp{}, false
	}
	mi, err := strconv.Atoi(mm)
	if err != nil || mi < 0 || mi > 59 {
		return finalScoresStamp{}, false
	}
	return finalScoresStamp{month: mo, day: d, hour: h, min: mi, raw: strings.TrimSpace(raw)}, true
}

// completeFinalScoresYear turns the year-less stamp into an instant by taking
// the year from a reference instant that another marker established — the ONLY
// way this stamp can name a moment at all.
//
// offsetSec is the zone the reference was printed in, which the stamp is read
// as having too: both come from the same server's strftime(localtime), so
// borrowing the offset compares like with like. The year is picked from the
// three candidates around the reference so a match played across midnight on
// 31 December lands in the right one instead of a year away.
func completeFinalScoresYear(p finalScoresStamp, refUnixMs int64, offsetSec int) int64 {
	refCivil := time.UnixMilli(refUnixMs).UTC().Add(time.Duration(offsetSec) * time.Second)
	best := int64(0)
	bestDist := int64(-1)
	for y := refCivil.Year() - 1; y <= refCivil.Year()+1; y++ {
		// A 29 February stamp does not exist in two of any three consecutive
		// years, and the layout carries no year to disambiguate with — skip
		// the years where the date is impossible rather than let time.Date
		// normalise them into 1 March.
		civil, ok := civilDate(y, p.month, p.day, p.hour, p.min, 0)
		if !ok {
			continue
		}
		unix := civil.UnixMilli() - int64(offsetSec)*1000
		d := unix - refUnixMs
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = unix, d
		}
	}
	return best
}

// applyZone turns a civil (zone-less) time plus a zone token into an instant.
// An unrecognised or missing zone is read as UTC and flagged — assuming UTC is
// the plan's rule, and AccuracyMs is where the cost of the assumption is
// stated rather than hidden.
func applyZone(civil time.Time, zone string) wallClockStamp {
	offsetSec, uncertaintyMs, ok := resolveZoneOffset(zone)
	st := wallClockStamp{TZ: zone, AccuracyMs: stampAccuracyMs}
	if !ok {
		st.AssumedUTC = true
		st.AccuracyMs = tzUnknownAccuracyMs
	} else if uncertaintyMs > st.AccuracyMs {
		st.AccuracyMs = uncertaintyMs
	}
	st.UnixMs = civil.Unix()*1000 - int64(offsetSec)*1000
	return st
}

// zoneOffsets maps the %Z abbreviations observed across the archive to their
// UTC offset in seconds. Ambiguous abbreviations are deliberately absent: an
// entry that is wrong by a whole zone is worse than the honest "assumed UTC"
// fallback, which at least reports its own uncertainty. The US set is included
// because those abbreviations do appear (EDT/EST/CDT/PST) and the QuakeWorld
// population makes the American reading of them overwhelmingly the right one —
// CST notably also names China's +08:00 zone, which no QW server has printed.
var zoneOffsets = map[string]int{
	"UT": 0, "UTC": 0, "GMT": 0, "Z": 0, "WET": 0,
	"WEST": 3600, "BST": 3600, "CET": 3600, "MET": 3600,
	"CEST": 7200, "MEST": 7200, "EET": 7200,
	"EEST": 10800, "MSK": 10800,
	"MSD": 14400,
	"EST": -5 * 3600, "EDT": -4 * 3600,
	"CST": -6 * 3600, "CDT": -5 * 3600,
	"MST": -7 * 3600, "MDT": -6 * 3600,
	"PST": -8 * 3600, "PDT": -7 * 3600,
	"AKST": -9 * 3600, "AKDT": -8 * 3600, "HST": -10 * 3600,
	"BRT": -3 * 3600, "BRST": -2 * 3600,
	"YEKT": 5 * 3600, "YEKST": 6 * 3600,
	"JST": 9 * 3600, "KST": 9 * 3600,
	"AWST": 8 * 3600, "ACST": 9*3600 + 1800, "ACDT": 10*3600 + 1800,
	"AEST": 10 * 3600, "AEDT": 11 * 3600,
	"NZST": 12 * 3600, "NZDT": 13 * 3600,
}

// zoneOffsetUncertainty marks the abbreviations in zoneOffsets whose offset is
// not constant across the years the archive spans, so the single number above
// cannot be exact for every stamp that printed them. Russia ran permanent
// summer time from 2011-03-27 to 2014-10-26: MSK was +04 and YEKT +06 in that
// window, +03 / +05 on either side of it. The table stays on the modern value
// and carries an hour of slack instead of becoming date-dependent — a
// date-dependent offset would decide the date from the date, and the hour is
// the honest width of the ambiguity either way. This is the same treatment the
// Windows "… Standard Time" long names get below.
var zoneOffsetUncertainty = map[string]int32{
	"MSK":  tzDSTAccuracyMs,
	"YEKT": tzDSTAccuracyMs,
}

// zoneLongNames maps the locale long names Windows servers print for %Z to an
// offset. They are matched on a lower-cased substring because the Swedish ones
// arrive mangled — the print goes through Q_normalizetext, which folds the
// high-bit 'ä' of "Västeuropa" down to 'd' — and because the leading region
// word varies while the "…tid" / "… Standard Time" tail does not. A "Standard
// Time" name does not say whether DST was in force when it was printed, so
// those entries carry an hour of uncertainty.
var zoneLongNames = []struct {
	substr        string
	offsetSec     int
	uncertaintyMs int32
}{
	{"sommartid", 7200, 0},                             // Swedish: W. Europe, DST
	{"normaltid", 3600, 0},                             // Swedish: W. Europe, standard
	{"gmt standard time", 0, tzDSTAccuracyMs},          // UK
	{"w. europe standard time", 3600, tzDSTAccuracyMs}, // DE/SE/NL/…
	{"central europe standard time", 3600, tzDSTAccuracyMs},
	{"central european standard time", 3600, tzDSTAccuracyMs},
	{"romance standard time", 3600, tzDSTAccuracyMs}, // FR/ES
	{"fle standard time", 7200, tzDSTAccuracyMs},     // FI/BALTICS
	{"gtb standard time", 7200, tzDSTAccuracyMs},     // GR/RO
	{"russian standard time", 10800, tzDSTAccuracyMs},
	{"e. south america standard time", -3 * 3600, tzDSTAccuracyMs}, // BR
	{"aus eastern standard time", 10 * 3600, tzDSTAccuracyMs},
}

// resolveZoneOffset maps a strftime %Z / %z token to a UTC offset in seconds,
// plus how much residual uncertainty the token leaves (0 for a fixed offset,
// an hour for a name that does not state its DST status). ok=false means the
// token was empty or unrecognised — the caller then assumes UTC and says so.
func resolveZoneOffset(tok string) (offsetSec int, uncertaintyMs int32, ok bool) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return 0, 0, false
	}
	up := strings.ToUpper(tok)
	if off, ok := zoneOffsets[up]; ok {
		return off, zoneOffsetUncertainty[up], true
	}
	if off, ok := parseNumericZone(tok); ok {
		return off, 0, true
	}
	lower := strings.ToLower(tok)
	for _, ln := range zoneLongNames {
		if strings.Contains(lower, ln.substr) {
			return ln.offsetSec, ln.uncertaintyMs, true
		}
	}
	return 0, 0, false
}

// parseNumericZone parses the %z forms and their prefixed variants: "+03",
// "+0200", "-05:00", "UTC+02", "GMT-5".
func parseNumericZone(tok string) (int, bool) {
	s := strings.TrimSpace(tok)
	for _, prefix := range []string{"UTC", "GMT", "UT"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	if s == "" {
		return 0, false
	}
	sign := 1
	switch s[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	s = s[1:]
	hh, mm := s, ""
	if h, m, found := strings.Cut(s, ":"); found {
		hh, mm = h, m
	} else if len(s) == 4 {
		hh, mm = s[:2], s[2:]
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h > 14 {
		return 0, false
	}
	m := 0
	if mm != "" {
		if m, err = strconv.Atoi(mm); err != nil || m > 59 {
			return 0, false
		}
	}
	return sign * (h*3600 + m*60), true
}

// splitField cuts the leading whitespace-delimited field off s.
func splitField(s string) (field, rest string, ok bool) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		if s == "" {
			return "", "", false
		}
		return s, "", true
	}
	return s[:i], strings.TrimLeft(s[i:], " \t"), true
}

// parseYMD parses "2008-01-05" / "2005-8-13" (the markers do not zero-pad).
func parseYMD(s string, sep byte) (y, mo, d int, ok bool) {
	parts := strings.Split(s, string(sep))
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if y, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if mo, err = strconv.Atoi(parts[1]); err != nil || mo < 1 || mo > 12 {
		return 0, 0, 0, false
	}
	if d, err = strconv.Atoi(parts[2]); err != nil || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	return y, mo, d, true
}

// parseHMS parses "20:05:38" / "19-56-18" (again unpadded in places).
func parseHMS(s string, sep byte) (h, mi, sec int, ok bool) {
	parts := strings.Split(s, string(sep))
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if h, err = strconv.Atoi(parts[0]); err != nil || h < 0 || h > 23 {
		return 0, 0, 0, false
	}
	if mi, err = strconv.Atoi(parts[1]); err != nil || mi < 0 || mi > 59 {
		return 0, 0, 0, false
	}
	if sec, err = strconv.Atoi(parts[2]); err != nil || sec < 0 || sec > 60 {
		return 0, 0, 0, false
	}
	return h, mi, sec, true
}

// releaseFloor is one entry of the version → earliest-possible-date table.
type releaseFloor struct {
	label  string
	unixMs int64
}

// utcDate is the table's date constructor.
func utcDate(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

// mvdsvReleaseFloors / ktxReleaseFloors give, per <major>.<minor> version
// family, a date the binary provably cannot predate. They are LOWER bounds
// held deliberately loose: each is a full year before the earliest date the
// 51k-demo archive shows that family stamping, so ordinary drift (a server
// clock a few weeks slow, a demo re-dated by a re-encode) can never trip the
// hard check, while the multi-year gap of an unset clock always does. Version
// suffixes (-dev, -beta, -SVN, -antilag-r402, -RekiFork, rev.643) collapse into
// the family: they are builds of that generation, and the earliest of them
// fixes the bound.
//
// The vendored mvdsv/ and ktx/ trees are single-commit snapshots with no tag
// history, so the bounds are derived from the archive's own version×date
// distribution rather than from release notes — which is the conservative
// direction: the archive can only show a family EARLIER than its release, never
// later, so rounding a year below the observed minimum can only ever loosen it.
//
// Because the entries come from OBSERVATION they are not naturally monotone:
// a family the archive barely used can show a later first sighting than its
// successor (mvdsv 1.00 vs 1.01, 0.21 vs 0.25). A binary cannot have been
// released after a version that supersedes it, so init() rewrites both tables
// into monotone floors — see monotoneReleaseFloors. The literals below are the
// raw observations; read the effective floor through serverReleaseFloor.
var mvdsvReleaseFloors = map[string]int64{
	"0.19": utcDate(2005, time.January, 1),
	"0.20": utcDate(2005, time.January, 1),
	"0.21": utcDate(2006, time.January, 1),
	"0.25": utcDate(2005, time.January, 1),
	"0.26": utcDate(2006, time.January, 1),
	"0.27": utcDate(2006, time.January, 1),
	"0.28": utcDate(2006, time.January, 1),
	"0.29": utcDate(2007, time.January, 1),
	"0.30": utcDate(2008, time.January, 1),
	"0.31": utcDate(2011, time.January, 1),
	"0.32": utcDate(2018, time.January, 1),
	"0.33": utcDate(2019, time.January, 1),
	"0.34": utcDate(2019, time.January, 1),
	"0.35": utcDate(2021, time.January, 1),
	"0.36": utcDate(2021, time.January, 1),
	"1.00": utcDate(2024, time.January, 1),
	"1.01": utcDate(2023, time.January, 1),
	"1.10": utcDate(2024, time.January, 1),
	"1.20": utcDate(2024, time.January, 1),
}

var ktxReleaseFloors = map[string]int64{
	"1.34": utcDate(2006, time.January, 1),
	"1.35": utcDate(2007, time.January, 1),
	"1.36": utcDate(2007, time.January, 1),
	"1.37": utcDate(2008, time.January, 1),
	"1.38": utcDate(2018, time.January, 1),
	"1.39": utcDate(2019, time.January, 1),
	"1.40": utcDate(2019, time.January, 1),
	"1.41": utcDate(2021, time.January, 1),
	"1.42": utcDate(2021, time.January, 1),
	"1.43": utcDate(2021, time.January, 1),
	"1.44": utcDate(2023, time.January, 1),
	"1.45": utcDate(2024, time.January, 1),
	"1.46": utcDate(2024, time.January, 1),
	"1.47": utcDate(2025, time.January, 1),
	"1.48": utcDate(2025, time.January, 1),
}

func init() {
	monotoneReleaseFloors(mvdsvReleaseFloors)
	monotoneReleaseFloors(ktxReleaseFloors)
}

// monotoneReleaseFloors rewrites a release-floor table in place so that no
// family's floor sits above the floor of a LATER family: floor(v) becomes
// min(floor(w)) over every w >= v in numeric <major>.<minor> order.
//
// The invariant it enforces is a fact about binaries, not about the archive: a
// build cannot have been released after the build that superseded it, so its
// lower bound cannot be later either. Left unenforced, an under-observed family
// contradicts perfectly ordinary demos — mvdsv 1.00's observed floor of 2024
// called every genuine 2023 match on that binary an unset clock, even though
// 1.01, its successor, was already stamping 2023.
//
// The rewrite can only ever LOWER a floor, so it can only ever make the hard
// check more permissive — the safe direction for a check whose false positives
// are silent data loss.
func monotoneReleaseFloors(table map[string]int64) {
	fams := make([]string, 0, len(table))
	for fam := range table {
		fams = append(fams, fam)
	}
	sort.Slice(fams, func(i, j int) bool { return versionFamilyLess(fams[i], fams[j]) })
	for i := len(fams) - 2; i >= 0; i-- {
		if later := table[fams[i+1]]; later < table[fams[i]] {
			table[fams[i]] = later
		}
	}
}

// versionFamilyLess orders "<major>.<minor>" families numerically, so 1.01
// precedes 1.10 (a lexicographic sort would agree here but not on 0.9 vs 0.10,
// and the tables are edited by hand).
func versionFamilyLess(a, b string) bool {
	amaj, amin := versionFamilyOrder(a)
	bmaj, bmin := versionFamilyOrder(b)
	if amaj != bmaj {
		return amaj < bmaj
	}
	return amin < bmin
}

// versionFamilyOrder splits "0.36" into (0, 36). A malformed key sorts first;
// the tables are in-code literals, so it cannot happen at run time.
func versionFamilyOrder(fam string) (major, minor int) {
	maj, min, found := strings.Cut(fam, ".")
	if !found {
		return 0, 0
	}
	major, _ = strconv.Atoi(maj)
	minor, _ = strconv.Atoi(min)
	return major, minor
}

// qwProtocolFloor is the release of QuakeWorld itself. It is the floor for a
// bare `*version` (2.30 / 2.40 — what the pre-MVDSV kmod / QWE servers report)
// and the universal baseline for every demo: nothing recorded in this format
// can predate it. It exists precisely so the old servers are NOT contradicted —
// a 2000 stamp on QW 2.40 is an ordinary date, and only the mid-2000s binaries
// make the same stamp impossible.
var qwProtocolFloor = utcDate(1996, time.June, 22)

// serverReleaseFloor returns the latest release floor implied by the serverinfo
// version keys — the tightest bound available, since every named binary must
// have existed for the demo to be recorded. It always yields at least the
// QuakeWorld floor, so an unrecognised (or absent) version string still bounds
// the date rather than accepting anything. label names the binary behind the
// bound, for the note on a contradicted anchor.
func serverReleaseFloor(serverInfo map[string]string) releaseFloor {
	best := releaseFloor{label: "quakeworld", unixMs: qwProtocolFloor}
	consider := func(f releaseFloor) {
		if f.unixMs > best.unixMs {
			best = f
		}
	}
	if v, ok := serverInfo["*version"]; ok {
		if fam, isMVDSV := mvdsvVersionFamily(v); isMVDSV {
			if floor, ok := mvdsvReleaseFloors[fam]; ok {
				consider(releaseFloor{label: "mvdsv " + fam, unixMs: floor})
			}
		} else if fam, isQW := qwVersionFamily(v); isQW {
			best.label = "quakeworld " + fam
		}
	}
	if v, ok := serverInfo["ktxver"]; ok {
		if fam, isKTX := ktxVersionFamily(v); isKTX {
			if floor, ok := ktxReleaseFloors[fam]; ok {
				consider(releaseFloor{label: "ktx " + fam, unixMs: floor})
			}
		}
	}
	return best
}

// mvdsvVersionFamily pulls the <major>.<minor> family out of an `*version`
// value like "MVDSV 0.36-beta-antilag-r402" or "mvdsv 0.19.10-develop".
func mvdsvVersionFamily(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) < len("mvdsv ") || !strings.EqualFold(v[:len("mvdsv ")], "mvdsv ") {
		return "", false
	}
	return versionFamily(v[len("mvdsv "):])
}

// ktxVersionFamily pulls the family out of a `ktxver` value like
// "1.44-dev-r402" or "KTX 1.43, with Quake Smash Mod changes by KovaaK".
func ktxVersionFamily(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) >= len("ktx ") && strings.EqualFold(v[:len("ktx ")], "ktx ") {
		v = v[len("ktx "):]
	}
	return versionFamily(v)
}

// qwVersionFamily recognises a bare QuakeWorld protocol version ("2.30",
// "2.40") — the `*version` the pre-MVDSV servers report.
func qwVersionFamily(v string) (string, bool) {
	fam, ok := versionFamily(v)
	if !ok || !strings.HasPrefix(fam, "2.") {
		return "", false
	}
	return fam, true
}

// versionFamily reads the leading <major>.<minor> of a version string,
// discarding any patch level and suffix ("0.19.10-develop" → "0.19",
// "1.43, with …" → "1.43").
func versionFamily(v string) (string, bool) {
	v = strings.TrimSpace(v)
	i := 0
	for i < len(v) && v[i] >= '0' && v[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(v) || v[i] != '.' {
		return "", false
	}
	major := v[:i]
	j := i + 1
	for j < len(v) && v[j] >= '0' && v[j] <= '9' {
		j++
	}
	if j == i+1 {
		return "", false
	}
	return major + "." + v[i+1:j], true
}

// wallClockAnchor is the resolved verdict the post-processor writes onto
// Streams.Global.
type wallClockAnchor struct {
	MatchStartUnixMs int64
	AccuracyMs       int32
	Source           string
	Confidence       string
	Note             string
	MatchEndUnixMs   int64
	// FinalScoresMarker is the `//finalscores` stamp with its year completed
	// from the chosen anchor (WallClockMarker.YearFrom names it). Nil when the
	// demo carried no such stamp; UnixMs 0 when there was no other marker to
	// take a year from — the stamp is still reported, it just names no instant.
	FinalScoresMarker *dateMarker
	// DemoStart is the demo-open anchor implied by MatchStartUnixMs, set only
	// when the demo carried no server-clock anchor of its own and the verdict
	// is not "contradicted".
	DemoStartUnixMs int64
	DemoStartSource string
	DemoStartAccMs  int32
}

// wallClockInputs is everything the resolution reads: the markers collected
// during the event pass, the ktxstats date, the server-clock anchor the clock
// already found, and the match window the markers are projected onto.
type wallClockInputs struct {
	Markers       []dateMarker
	ServerInfo    map[string]string
	DemoStartUnix int64
	DemoStartAcc  int32
	DemoStartSrc  string
	DemoOffsetMs  int32
	// FinalScores is the year-less `//finalscores` stamp, when the demo carried
	// one. It is held apart from Markers because it is not a marker yet: it
	// only becomes one once another marker has supplied a year.
	FinalScores *finalScoresStamp
	// MatchLengthMs is the match's WALL-CLOCK length: the game-time window
	// plus every pause, since the game clock freezes during a pause while the
	// clock the ktxstats date is stamped from keeps running. Walking a match-end
	// stamp back over the game-time window alone lands the anchor a full pause
	// too late (observed: a 20-minute match with a 126 s pause).
	MatchLengthMs int32
}

// candidate is one marker projected onto the match-start instant, ready to be
// compared with the others.
type candidate struct {
	source     string
	matchStart int64
	accuracyMs int32
	assumedUTC bool
	// zoneOffsetSec / zonePinned are the zone this candidate's stamp was
	// printed in, when it named one. They exist for the year-less
	// `//finalscores` stamp, which has to borrow a zone to be read at all —
	// they are NOT the same statement as assumedUTC, which grades the chosen
	// anchor's confidence and must keep meaning exactly what it meant before.
	zoneOffsetSec int
	zonePinned    bool
}

// resolveWallClock picks the match-start anchor and grades it. Priority is by
// how directly the source states the instant: the server clock (mvdhidden /
// epoch, true UTC at a known demo time) first, then the match-start prints,
// then the ktxstats block, which states match END and has to be walked back
// over the match length. Every candidate that is not chosen still participates
// as a cross-check.
func resolveWallClock(in wallClockInputs) wallClockAnchor {
	var cands []candidate
	if in.DemoStartUnix != 0 {
		cands = append(cands, candidate{
			source:     in.DemoStartSrc,
			matchStart: in.DemoStartUnix + int64(in.DemoOffsetMs),
			accuracyMs: in.DemoStartAcc,
		})
	}
	var matchEndUnix int64
	for _, m := range in.Markers {
		switch m.Kind {
		case wallKindMatchStart:
			// The print names the instant it was emitted at, so the demo-open
			// instant is the stamp minus the print's demo time; match start is
			// that plus DemoOffset. On a normal demo the print sits AT match
			// start and the two shifts cancel, but this stays correct on a
			// TimeBase=="demo" result (no detected match start, DemoOffset 0)
			// and on a marker printed off the match-start frame.
			cands = append(cands, newMarkerCandidate(m, m.UnixMs-int64(m.AtMs)+int64(in.DemoOffsetMs)))
		case wallKindMatchEnd:
			if matchEndUnix == 0 {
				matchEndUnix = m.UnixMs
			}
			cands = append(cands, newMarkerCandidate(m, m.UnixMs-int64(in.MatchLengthMs)))
		}
	}
	if len(cands) == 0 {
		// Nothing to take a year from, so the `//finalscores` stamp names no
		// instant — but it is still evidence, and this package never drops a
		// marker it saw.
		if in.FinalScores != nil {
			m := unresolvedFinalScoresMarker(*in.FinalScores)
			return wallClockAnchor{FinalScoresMarker: &m}
		}
		return wallClockAnchor{}
	}

	sort.SliceStable(cands, func(i, j int) bool {
		return sourcePriority(cands[i].source) < sourcePriority(cands[j].source)
	})
	best := cands[0]

	var fsMarker *dateMarker
	if in.FinalScores != nil {
		fsCand, m := resolveFinalScores(*in.FinalScores, best, in.MatchLengthMs)
		cands = append(cands, fsCand)
		fsMarker = &m
		if matchEndUnix == 0 {
			matchEndUnix = m.UnixMs
		}
	}

	anchor := wallClockAnchor{
		MatchStartUnixMs:  best.matchStart,
		AccuracyMs:        best.accuracyMs,
		Source:            best.source,
		MatchEndUnixMs:    matchEndUnix,
		FinalScoresMarker: fsMarker,
	}

	var notes []string
	hard := false
	if floor := serverReleaseFloor(in.ServerInfo); best.matchStart < floor.unixMs {
		hard = true
		notes = append(notes, fmt.Sprintf("version-floor: %s was not released before %s",
			floor.label, time.UnixMilli(floor.unixMs).UTC().Format("2006-01-02")))
	} else if best.matchStart >= maxDemoStartUnixMs {
		// The same upper bound the server-clock anchors are range-checked
		// against (timeshift.go): past 2100 the stamp is corrupt, not a date.
		hard = true
		notes = append(notes, "impossible-date: the stamp lands after 2100")
	}

	soft := 0
	if disagreeing := disagreeingSources(cands, 0); len(disagreeing) > 0 {
		soft++
		notes = append(notes, "marker-disagreement: "+best.source+" vs "+strings.Join(disagreeing, ", "))
	}
	if best.matchStart >= epochResetWindowStart && best.matchStart < epochResetWindowEnd {
		soft++
		notes = append(notes, "epoch-reset-window: the stamp lands in the unset-clock boot default (2000-01)")
	}

	switch {
	case hard || soft >= 2:
		anchor.Confidence = wallConfContradicted
	case soft == 1 || best.assumedUTC:
		anchor.Confidence = wallConfUnverified
	default:
		anchor.Confidence = wallConfExact
	}
	if best.assumedUTC {
		notes = append(notes, "tz-unknown: the marker named no timezone, UTC assumed")
	}
	if anchor.Confidence != wallConfExact {
		anchor.Note = strings.Join(notes, "; ")
	}

	// Back-fill the demo-open anchor from the marker so the documented
	// wallClockMs formula keeps working on the ~73% of the archive that has a
	// date marker but no server-clock anchor. A contradicted stamp is NOT
	// back-filled: it stays visible on the match-start fields, where its grade
	// travels with it, rather than silently becoming the mapping origin.
	if in.DemoStartUnix == 0 && anchor.Confidence != wallConfContradicted {
		anchor.DemoStartUnixMs = anchor.MatchStartUnixMs - int64(in.DemoOffsetMs)
		anchor.DemoStartSource = best.source
		anchor.DemoStartAccMs = best.accuracyMs
	}
	return anchor
}

// newMarkerCandidate builds the candidate for one collected marker, projected
// onto match start by the caller. It carries the marker's zone along so a
// year-less stamp can borrow it.
func newMarkerCandidate(m dateMarker, matchStart int64) candidate {
	c := candidate{
		source:     m.Source,
		matchStart: matchStart,
		accuracyMs: markerAccuracy(m),
		assumedUTC: m.AssumedUTC,
	}
	if !m.AssumedUTC {
		if off, _, ok := resolveZoneOffset(m.TZ); ok {
			c.zoneOffsetSec, c.zonePinned = off, true
		}
	}
	return c
}

// resolveFinalScores completes the year-less `//finalscores` stamp against the
// chosen anchor and returns it both as a cross-check candidate and as the
// marker to report.
//
// The completed instant is NOT independent evidence of the year — it is the
// anchor's own year — so the value of this candidate is entirely in its
// month/day/hour/minute, which is exactly what a comparison against the anchor
// tests. When the anchor named no zone (a server-clock anchor, or a zone-less
// matchkey) the stamp is read as UTC and flagged assumed, which routes the
// comparison through markersAgree's whole-timezone slack — the honest answer
// when the two are separated by an unknown offset.
func resolveFinalScores(p finalScoresStamp, best candidate, matchLengthMs int32) (candidate, dateMarker) {
	offset, assumed := 0, true
	if best.zonePinned {
		offset, assumed = best.zoneOffsetSec, false
	}
	endUnix := completeFinalScoresYear(p, best.matchStart+int64(matchLengthMs), offset)

	accuracy := int32(finalScoresAccuracyMs)
	if assumed {
		accuracy = tzUnknownAccuracyMs
	}
	cand := candidate{
		source:     wallSourceFinalScores,
		matchStart: endUnix - int64(matchLengthMs),
		accuracyMs: accuracy,
		assumedUTC: assumed,
	}
	m := unresolvedFinalScoresMarker(p)
	m.UnixMs = endUnix
	m.AssumedUTC = assumed
	m.YearFrom = best.source
	return cand, m
}

// unresolvedFinalScoresMarker is the `//finalscores` stamp as reported when no
// year could be attached to it: the raw text and nothing invented around it.
func unresolvedFinalScoresMarker(p finalScoresStamp) dateMarker {
	return dateMarker{
		Source:     wallSourceFinalScores,
		Kind:       wallKindMatchEnd,
		AssumedUTC: true,
		Raw:        p.raw,
	}
}

// markerAccuracy re-derives a marker's uncertainty from what it recorded about
// its zone (the parsed stamp is not kept around past the event pass).
func markerAccuracy(m dateMarker) int32 {
	if m.AssumedUTC {
		return tzUnknownAccuracyMs
	}
	if _, uncertainty, ok := resolveZoneOffset(m.TZ); ok && uncertainty > stampAccuracyMs {
		return uncertainty
	}
	return stampAccuracyMs
}

// sourcePriority orders the candidate sources by how directly they state the
// match-start instant.
func sourcePriority(source string) int {
	switch source {
	case wallSourceHidden:
		return 0
	case wallSourceEpoch:
		return 1
	case wallSourceMatchDate:
		return 2
	case wallSourceMatchKey:
		return 3
	case wallSourceKTXStats:
		return 4
	case wallSourceFinalScores:
		// Last, and never actually reached: the year-less stamp is added to the
		// candidate list only AFTER the anchor has been chosen, precisely so it
		// can never become one.
		return 5
	}
	return 6
}

// disagreeingSources names the candidates that cannot be reconciled with the
// chosen one (cands[bestIdx]). Two stamps taken in different (or unknown)
// timezones differ by a whole quarter-hour, so that residual is allowed for
// whenever either side's zone is unpinned; a gap no timezone could explain, or
// one that is off-quantum beyond the tolerance, is a real disagreement.
//
// Candidates from the SAME source count too — the skip is by position, not by
// name. A demo holding two matches legitimately carries two `matchdate` prints,
// but candidates are compared on the instant each one PROJECTS to (stamp minus
// its own print time), and on a consistent clock both projections name the same
// demo-open moment however far apart the matches were. Two projections that do
// not agree therefore mean the server clock moved mid-demo, which is exactly
// the signal cross-source disagreement reports — and the old name-based skip
// let the worst case through silently, two stamps a year apart grading "exact"
// on whichever printed first.
func disagreeingSources(cands []candidate, bestIdx int) []string {
	best := cands[bestIdx]
	var out []string
	seen := make(map[string]bool, len(cands))
	for i, c := range cands {
		if i == bestIdx || markersAgree(best, c) || seen[c.source] {
			continue
		}
		seen[c.source] = true
		out = append(out, c.source)
	}
	return out
}

func markersAgree(a, b candidate) bool {
	d := a.matchStart - b.matchStart
	if d < 0 {
		d = -d
	}
	tol := int64(markerAgreeToleranceMs)
	if a.source == wallSourceKTXStats || b.source == wallSourceKTXStats {
		tol = ktxStatsToleranceMs
	}
	if a.source == wallSourceFinalScores || b.source == wallSourceFinalScores {
		tol = finalScoresToleranceMs
	}
	if d <= tol {
		return true
	}
	if !a.assumedUTC && !b.assumedUTC {
		// Both zones are pinned, so there is no offset left to explain a gap.
		return false
	}
	if d > maxTZSpanMs+tol {
		return false
	}
	r := d % tzQuantumMs
	return r <= tol || r >= tzQuantumMs-tol
}

// wallClockPost is the DAG node "wall-clock": it resolves the match-start
// wall-clock anchor from the date markers the clock collected, the ktxstats
// date on the assembled Result, and the serverinfo version keys, then writes
// the verdict onto Streams.Global.
//
// It runs as a post-processor rather than inside the clock because the
// evidence is spread across three artifacts — the clock's markers, demoinfo's
// parsed ktxstats block, and metadata's serverinfo map — and because the
// projection onto match time needs the match window the timeline publishes on
// Streams.Global.
func wallClockPost(res *Result, co *CoreOutputs) {
	if res == nil || res.Streams == nil {
		return
	}
	g := &res.Streams.Global

	in := wallClockInputs{
		DemoStartUnix: g.DemoStartUnixMs,
		DemoStartAcc:  g.DemoStartAccuracyMs,
		DemoOffsetMs:  g.DemoOffset,
		MatchLengthMs: g.MatchEnd - g.MatchStart,
	}
	for _, p := range g.Pauses {
		// Only pauses INSIDE the match count. MatchLengthMs is the wall-clock
		// width of the match-start → match-end window, and a countdown pause
		// (AtMs < 0, the clock frozen before the match began) sits outside it —
		// adding it walks a match-end stamp back past match start and can
		// invent a marker disagreement on a demo whose stamps all agree.
		if p.AtMs > 0 {
			in.MatchLengthMs += p.DurationMs
		}
	}
	if co != nil && co.Clock != nil {
		in.Markers = append(in.Markers, co.Clock.DateMarkers...)
		in.DemoStartSrc = co.Clock.DemoStartSource
	}
	if res.Metadata != nil {
		in.ServerInfo = res.Metadata.ServerInfo
	}
	if res.DemoInfo != nil && res.DemoInfo.Date != "" {
		if st, ok := parseKTXStatsDate(res.DemoInfo.Date); ok {
			in.Markers = append(in.Markers, dateMarker{
				Source:     wallSourceKTXStats,
				Kind:       wallKindMatchEnd,
				UnixMs:     st.UnixMs,
				TZ:         st.TZ,
				AssumedUTC: st.AssumedUTC,
				Raw:        res.DemoInfo.Date,
			})
		}
	}
	if res.Metadata != nil && res.Metadata.FinalScores != nil && res.Metadata.FinalScores.Date != "" {
		if st, ok := parseFinalScoresDate(res.Metadata.FinalScores.Date); ok {
			in.FinalScores = &st
		}
	}
	if len(in.Markers) > 0 {
		g.DateMarkers = append([]WallClockMarker(nil), in.Markers...)
	}
	if g.DemoStartSource == "" {
		g.DemoStartSource = in.DemoStartSrc
	}

	anchor := resolveWallClock(in)
	// The `//finalscores` marker is appended last because it is resolved last —
	// its year comes out of the anchor the other markers produced.
	if anchor.FinalScoresMarker != nil {
		g.DateMarkers = append(g.DateMarkers, *anchor.FinalScoresMarker)
	}
	if anchor.MatchStartUnixMs == 0 {
		return
	}
	g.MatchStartUnixMs = anchor.MatchStartUnixMs
	g.MatchStartAccuracyMs = anchor.AccuracyMs
	g.MatchStartSource = anchor.Source
	g.MatchStartConfidence = anchor.Confidence
	g.MatchStartNote = anchor.Note
	g.MatchEndUnixMs = anchor.MatchEndUnixMs
	if anchor.DemoStartUnixMs != 0 {
		g.DemoStartUnixMs = anchor.DemoStartUnixMs
		g.DemoStartAccuracyMs = anchor.DemoStartAccMs
		g.DemoStartSource = anchor.DemoStartSource
	}
}
