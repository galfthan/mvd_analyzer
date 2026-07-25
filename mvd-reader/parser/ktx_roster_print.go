package parser

import (
	"strings"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// KTX / kmod roster broadcasts — the three server prints that announce a
// player leaving the game and coming back to it. They are one wire family
// with one grammar, so they are decoded once here rather than re-scanned by
// every analyser that wants a piece of them.
//
// The wire forms, all `G_bprint(PRINT_HIGH, ...)`:
//
//	"%s left the game with %.0f frags\n"              ktx/src/client.c:2843
//	                                                  ktx/src/bot_commands.c:388
//	"%s \220%s\221 rejoins the game with %d frag%s\n" ktx/src/client.c:1481
//	"%s rejoins the game with %d frag%s\n"            ktx/src/client.c:1487
//	"%s \220%s\221 reenters the game without stats\n" ktx/src/client.c:1502
//	"%s reenters the game without stats\n"            ktx/src/client.c:1506
//
// The pre-KTX kmod / qwe mods use the same wording, which is why the
// 2002-era demos in demo-test-data/ decode here too.
//
// The departure line is the only place the wire ever states a departing
// player's final score: KTX prints it from ClientDisconnect while the match
// is running (`match_in_progress == 2`), and the SV_DropClient that follows
// immediately zeroes the slot's scoreboard entry.
//
// The `\220`/`\221` team brackets fold to `[`/`]` and redtext folds to
// plain during Q-normalisation (see qNormalizeTable in userinfo.go), so by
// the time these functions see the message it reads
// "rusti [jah] rejoins the game with 16 frags".
//
// # Fragmentation
//
// Old servers split one logical broadcast across several svc_print
// messages, and they split it at arbitrary points — including *inside* a
// number. 4on4_l_vs_la[e1m2] (kmod 1.54 / qwe 0.153) carries the departure
// as "DARKLORD left the game with 21 frag" + "s\n", and at t=1094110 the
// same server emits a team score as the six fragments
// "Team [.la.] = ", "", "2", "2", "7", "\n".
//
// Two rules follow, and both are load-bearing:
//
//   - The marker match tolerates the truncated tail ("frag", not "frags"),
//     otherwise the two real departures on that demo decode as nothing.
//   - The frag count is accepted only when the digit run is followed by
//     " frag". A number cut mid-digits — "…with 2" then "6 frags\n" —
//     would otherwise decode as 2 and silently overwrite the correct 26.
//     Both genuine forms ("26 frags\n", "21 frag") satisfy the guard; the
//     truncated one cannot, and FragsKnown reports the difference so a
//     consumer falls back to its own reconstruction instead of trusting a
//     wrong number.
//
// # PRINT_CHAT exclusion
//
// A player can say anything, including "bob left the game with 99 frags".
// Chat arrives at PRINT_CHAT (3) and every one of these broadcasts is
// PRINT_HIGH (2), so chat is excluded outright.

// PlayerDepartureEvent is one "<name> left the game with N frags"
// broadcast. See the file comment for the wire form.
//
// Name is the text preceding the marker. For this line that is exactly the
// departing player's netname — KTX formats the netname and nothing else
// before it.
//
// Frags is meaningful only when FragsKnown is true; see the file comment on
// fragmentation for when it is not.
type PlayerDepartureEvent struct {
	Name       string
	Frags      int
	FragsKnown bool
	TimeMs     int32
}

func (e *PlayerDepartureEvent) EventType() EventType { return EventPlayerDeparture }
func (e *PlayerDepartureEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *PlayerDepartureEvent) EventTimeMs() int32   { return e.TimeMs }

// PlayerRejoinEvent is one "rejoins the game with N frags" or "reenters the
// game without stats" broadcast — KTX announcing that a reconnecting client
// was matched to a ghost (stats restored) or was not (stats lost).
//
// Prefix is the text preceding the marker. Unlike the departure line this
// is NOT necessarily the bare netname: in team modes KTX prints
// "<netname> [<team>]" and the wire gives no delimiter between the two, and
// a netname may itself contain spaces or brackets. Consumers resolve it by
// matching the known netnames against Prefix by longest prefix rather than
// tokenising it.
//
// WithStats distinguishes the two lines: true for "rejoins the game with N
// frags" (the ghost was found and the score restored, ktx/src/client.c:1464-1490),
// false for "reenters the game without stats" (no ghost, :1496-1508). Frags
// is only ever set on the WithStats form, and only when FragsKnown.
type PlayerRejoinEvent struct {
	Prefix     string
	Frags      int
	FragsKnown bool
	WithStats  bool
	TimeMs     int32
}

func (e *PlayerRejoinEvent) EventType() EventType { return EventPlayerRejoin }
func (e *PlayerRejoinEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *PlayerRejoinEvent) EventTimeMs() int32   { return e.TimeMs }

// Markers, deliberately truncated where a fragmenting server can cut them:
// the trailing "s" of "frags" is dropped from the departure and rejoin
// markers because 4on4_l_vs_la[e1m2] splits exactly there.
const (
	departureMarker = " left the game with "
	rejoinMarker    = "rejoins the game with"
	reenterMarker   = "reenters the game without stats"
)

// tryEmitRosterPrint decodes the departure / rejoin / reenter broadcasts out
// of one print line. Called from parsePrint after PrintEvent is emitted.
func (p *Parser) tryEmitRosterPrint(level int, msg string, timeMs int32) error {
	if level == mvd.PrintChat {
		return nil
	}

	if i := strings.Index(msg, departureMarker); i > 0 {
		frags, known := parseBroadcastFrags(msg[i+len(departureMarker):])
		return p.emit(&PlayerDepartureEvent{
			Name:       msg[:i],
			Frags:      frags,
			FragsKnown: known,
			TimeMs:     timeMs,
		})
	}
	if i := strings.Index(msg, rejoinMarker); i > 0 {
		rest := msg[i+len(rejoinMarker):]
		frags, known := parseBroadcastFrags(strings.TrimPrefix(rest, " "))
		return p.emit(&PlayerRejoinEvent{
			Prefix:     strings.TrimSuffix(msg[:i], " "),
			Frags:      frags,
			FragsKnown: known,
			WithStats:  true,
			TimeMs:     timeMs,
		})
	}
	if i := strings.Index(msg, reenterMarker); i > 0 {
		return p.emit(&PlayerRejoinEvent{
			Prefix: strings.TrimSuffix(msg[:i], " "),
			TimeMs: timeMs,
		})
	}
	return nil
}

// parseBroadcastFrags reads the leading digit run of s and reports whether
// it is a whole number rather than the head of one a fragmenting server cut
// in half. The test is that " frag" follows the digits, which is true of
// every complete form the mods emit ("26 frags\n", "21 frag") and false of
// a truncation ("…with 2" as its own print).
func parseBroadcastFrags(s string) (int, bool) {
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 0 || !strings.HasPrefix(s[j:], " frag") {
		return 0, false
	}
	n := 0
	for _, c := range []byte(s[:j]) {
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0, false
		}
	}
	return n, true
}
