package parser

import (
	"strings"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// MatchStartEvent is emitted exactly once per stream, at the first wire
// signal that the match went live. It is the Layer-1 verdict on the match
// boundary: every analytics consumer gates on this one event instead of
// re-scanning prints, so the answer cannot drift between the parser's own
// obituary gate (p.matchStarted) and the analyzers'.
//
// Four independent signals can raise it (see the MatchStartSource*
// constants). KTX emits three of them from the same StartMatch() call
// (ktx/src/match.c:1291, :1337, :1372) — the two prints and the stuffcmd go
// out in that frame, the `serverinfo status` localcmd is queued and runs
// at the next frame's Cbuf_Execute (mvdsv/src/sv_main.c:3323) — and the
// demo stamps them with the same time, so on a modern KTX demo the TimeMs
// is the same whichever one is seen first and the Source names only which
// byte arrived first, not a different instant.
// The reason all four exist is the demos where only some are present: a
// matchless FFA/CTF server (k_matchless 1, ktx/src/world.c:1874-1877)
// SKIPS the "The match has begun!" broadcast — `match.c:1294-1297` gates
// it on `!k_matchLess || cvar("k_matchless_countdown")` — while still
// printing `matchdate:`, stuffing `//ktx matchstart` and moving the
// serverinfo `status` key to a running clock. Before this event existed
// the whole streams half of the pipeline stood down on those demos.
type MatchStartEvent struct {
	TimeMs int32
	// Source names the signal that raised the event; one of the
	// MatchStartSource* constants below.
	Source string
}

func (e *MatchStartEvent) EventType() EventType { return EventMatchStart }
func (e *MatchStartEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *MatchStartEvent) EventTimeMs() int32   { return e.TimeMs }

// MatchStartEvent.Source vocabulary. First signal on the wire wins — the
// event is emitted immediately after the wire event that raised it, so
// there is no window in which a later, "stronger" signal from the same
// server frame could revise it. Measured on the 13-demo matchless FFA set
// and the 14-demo golden corpus, `matchdate:` is the first of the KTX
// three on every single demo (KTX prints it at match.c:1291, before the
// status update at :1337 and the stuffcmd at :1372).
const (
	// MatchStartSourceKtxDirective: the `//ktx matchstart` stuffcmd
	// (ktx/src/match.c:1372, STUFFCMD_DEMOONLY). Unconditional at every
	// match start in every KTX mode since the directive exists. It is the
	// last line of StartMatch, after the date print (:1291) and the "has
	// begun" print (:1296), so on a demo that carries either of those it
	// never arrives first; the `serverinfo status` localcmd (:1337) is
	// written before it in the source but executes at the NEXT frame's
	// Cbuf_Execute (mvdsv/src/sv_main.c:3323), so on the wire the directive
	// precedes the status update (archive 0543ac01…: stufftext, then the
	// status key, same timestamp). On a non-deathmatch match and on every
	// hoony point after the first, where `matchdate:` is gated off
	// (match.c:1287, `deathmatch && (!isHoonyModeAny() ||
	// HM_current_point() == 0)`), it is therefore the FIRST signal, with
	// the status transition a frame behind it; the once-per-stream latch
	// means those later points do not re-raise it.
	MatchStartSourceKtxDirective = "ktx-matchstart"
	// MatchStartSourcePrint: a broadcast print matching MatchStartPatterns
	// — "The match has begun!" (ktx/src/match.c:1296) and the kmod/qwe
	// mode-specific spellings. The only signal on pre-KTX servers.
	MatchStartSourcePrint = "print"
	// MatchStartSourceMatchDate: a broadcast print whose line STARTS with
	// `matchdate:` (ktx/src/match.c:1291). Line-initial rather than
	// "contains" so a player quoting the line in chat-adjacent output
	// cannot raise it.
	MatchStartSourceMatchDate = "matchdate"
	// MatchStartSourceStatus: a svc_serverinfo update moving the `status`
	// key to a running clock (ktx/src/match.c:1337) from a value that was
	// not one. Weakest of the four and last on the wire, so it decides
	// only where the other three are absent — the ktx 1.38 / 1.40-beta
	// demos in the archive that print no `matchdate:`.
	MatchStartSourceStatus = "status"
)

// noteMatchStart flips the parser's own match gate and emits the
// MatchStartEvent, once per stream. Callers invoke it AFTER emitting the
// wire event that raised it (the PrintEvent / StuffTextEvent /
// ServerInfoEvent), so a handler reading that trigger still observes the
// pre-start state — the ordering the print path has always had.
func (p *Parser) noteMatchStart(source string, timeMs int32) error {
	if p.matchStarted {
		return nil
	}
	p.matchStarted = true
	return p.emit(&MatchStartEvent{TimeMs: timeMs, Source: source})
}

// tryEmitMatchStartFromStuffText raises the match start on KTX's own
// `//ktx matchstart` directive. Called from the stufftext path next to the
// other `//ktx ` hint matchers.
func (p *Parser) tryEmitMatchStartFromStuffText(cmd string, timeMs int32) error {
	if strings.TrimRight(cmd, "\n\r ") != ktxMatchStartDirective {
		return nil
	}
	return p.noteMatchStart(MatchStartSourceKtxDirective, timeMs)
}

// ktxMatchStartDirective is the exact stuffcmd KTX writes at the end of
// StartMatch (ktx/src/match.c:1372).
const ktxMatchStartDirective = "//ktx matchstart"

// matchDatePrefix opens the KTX match-start date broadcast
// (ktx/src/match.c:1291, `G_bprint(2, "matchdate: %s\n", date)`).
const matchDatePrefix = "matchdate:"

// tryEmitMatchStartFromPrint raises the match start on an assembled
// broadcast console line: either one of the canonical match-start phrases
// (MatchStartPatterns) or the line-initial `matchdate:` stamp.
//
// Chat is refused: the gate never resets once flipped, so a single prewar
// "go go go!" in team chat would open the obituary-death path for the rest
// of the demo. Same guard the analytics MatchTimingDetector applies
// (analyzer/matchtiming.go) and the KTX pickup-print matcher.
func (p *Parser) tryEmitMatchStartFromPrint(level int, msg string, timeMs int32) error {
	if p.matchStarted || level == mvd.PrintChat {
		return nil
	}
	lower := strings.ToLower(msg)
	for _, phrase := range MatchStartPatterns {
		if strings.Contains(lower, phrase) {
			return p.noteMatchStart(MatchStartSourcePrint, timeMs)
		}
	}
	// Line-initial, not "contains": the stamp is a whole bprint line of
	// its own, and a substring test would let any line that merely
	// mentions it (a mod echoing the console, a name) start a match.
	if strings.HasPrefix(msg, matchDatePrefix) {
		return p.noteMatchStart(MatchStartSourceMatchDate, timeMs)
	}
	return nil
}

// observeServerInfoStatus feeds one serverinfo `status` reading to the
// match-start detector and remembers it.
//
// `update` distinguishes a svc_serverinfo key/value update from the
// opening `fullserverinfo` dump: only an UPDATE can raise the match start,
// because a dump states the server's state at the instant the recording
// opened, not a transition inside it. A recording that begins mid-match
// therefore records a running clock as its baseline and never fires — the
// no-match marker's `midMatchRecording` verdict stays the verdict.
func (p *Parser) observeServerInfoStatus(value string, timeMs int32, update bool) error {
	running := StatusNamesRunningGame(value)
	prev := p.statusRunning
	p.statusRunning = running
	if !update || !running || prev {
		return nil
	}
	return p.noteMatchStart(MatchStartSourceStatus, timeMs)
}

// observeFullServerInfoStatus takes the baseline `status` reading out of a
// `fullserverinfo "\key\value\..."` stuffcmd. A stufftext that carries no
// `status` key leaves the baseline untouched.
func (p *Parser) observeFullServerInfoStatus(cmd string, timeMs int32) error {
	if !strings.HasPrefix(cmd, fullServerInfoPrefix) {
		return nil
	}
	v, ok := infoStringValue(strings.TrimPrefix(cmd, fullServerInfoPrefix), "status")
	if !ok {
		return nil
	}
	return p.observeServerInfoStatus(v, timeMs, false)
}

// fullServerInfoPrefix opens the bulk cvar dump mvdsv stuffs at connect
// time (sv_main.c SV_FullClientUpdate / SV_New_f).
const fullServerInfoPrefix = "fullserverinfo "

// infoStringValue reads one key out of a backslash-delimited Quake info
// string, tolerating the surrounding quotes the stuffcmd wraps it in.
func infoStringValue(s, key string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	s = strings.TrimPrefix(s, "\\")
	parts := strings.Split(s, "\\")
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] == key {
			return parts[i+1], true
		}
	}
	return "", false
}

// StatusNamesRunningGame reports whether a serverinfo `status` value says a
// game is under way. Two spellings, and only two, are a running reading:
// KTX's `"%d min left"` (ktx/src/match.c:596, :723, :1330, :1337) and a
// CTF mod's `"%d:%02d left"`.
//
// KTX writes exactly four values into the key, from eleven call sites:
// that clock, `Standby` (world.c:543, match.c:2565, admin.c:585, :602,
// :714), `Countdown` (match.c:2475) and `Forcestart` (admin.c:693). All
// but Forcestart are inherited verbatim from Kombat Teams
// (kteams/v2.07/SRC/MATCH.QC:270, :394, :505, :520), which is why the
// oldest archive demos read the same way.
//
// The test is deliberately the exact pair of clock formats rather than
// something looser like "ends in ` left`", and that is a decision from the
// whole-archive sweep (.reports/vocab-sweep-2026-08-29, probe S3), not from
// taste. Every running reading in 50 964 demos matches one of the two
// forms; beside them the key carries 43 spellings from mods that write
// their own vocabulary into it — the CTF mod's terminal `Game Ended`, the
// `arena` mod's `Round n/9` … `Round n/15` counters, `fortress`'s `Normal`,
// one E0 server's `match over`. Those are exactly why a looser test is the
// riskier one: "2 rounds left" from some mod would read as a running clock
// and move a demo to midMatchRecording/matchStartUnannounced on no
// evidence. A mod that spells its clock in a THIRD way is read as idle
// instead — the failure this direction is a demo landing in
// noMatchDeclared / noPlayRecorded with its verbatim `status` published as
// evidence, which a reader can see, rather than a fabricated match
// verdict, which they cannot.
//
// It lives in Layer 1 because two consumers share it: the match-start
// detector above and the analytics no-match marker (via the events
// re-export), and the dependency arrow only allows analytics to import
// mvd-reader, not the reverse.
func StatusNamesRunningGame(v string) bool {
	rest, ok := strings.CutSuffix(v, " left")
	if !ok {
		return false
	}
	if mins, ok := strings.CutSuffix(rest, " min"); ok {
		return allDigits(mins)
	}
	mins, secs, ok := strings.Cut(rest, ":")
	return ok && allDigits(mins) && len(secs) == 2 && allDigits(secs)
}

// allDigits reports whether s is a non-empty run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
