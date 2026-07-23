package parser

import "strings"

// DemoMarkEvent is the typed representation of KTX's `//demomark[ <args>]`
// stufftext — a bookmark a player inserts into the recording with the
// in-game `/demomark` command (ktx/src/commands.c:295 →
// stuffcmd(self, "//demomark\n")). ezquake's demo_jump_mark seeks between
// these strings (cl_parse.c:3122).
//
// Attribution comes ONLY from the demo block: mvdsv records the stuffcmd
// as a dem_single block targeted at the marking client's slot
// (mvdsv/src/pr_cmds.c:840-848); the payload carries no player identity.
// PlayerSlot is that target, or -1 when the block is not slot-addressed
// (dem_all / dem_multiple).
//
// The mark fires even out of match (KTX's 10-marker cap and 5 s debounce
// gate only the end-of-match text listing, not the wire signal), so this
// event is surfaced un-gated — the consumer decides. A HoonyMode variant
// carries an argument tail ("//demomark 0 round-07", ktx/src/match.c:428)
// captured in Label; Label is "" for the plain form.
type DemoMarkEvent struct {
	TimeMs     int32
	PlayerSlot int    // dem_single target slot; -1 if not slot-addressed
	Label      string // argument tail (e.g. "0 round-07"); "" for the plain form
}

func (e *DemoMarkEvent) EventType() EventType { return EventDemoMark }
func (e *DemoMarkEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *DemoMarkEvent) EventTimeMs() int32   { return e.TimeMs }

const demoMarkPrefix = "//demomark"

// tryEmitDemoMark emits a typed DemoMarkEvent when the stufftext payload is
// a `//demomark` directive at a token boundary — the next character after
// the prefix must be whitespace or end of string, so `//demomarkX` does NOT
// match. playerSlot is the dem_single target (or -1); the caller supplies it
// from the demo block header. The generic StuffTextEvent for the same
// command is emitted by the caller regardless, so a non-match here is a
// silent no-op.
func (p *Parser) tryEmitDemoMark(cmd string, playerSlot int, timeMs int32) error {
	s := strings.TrimRight(cmd, "\n\r")
	if !strings.HasPrefix(s, demoMarkPrefix) {
		return nil
	}
	rest := s[len(demoMarkPrefix):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return nil
	}
	return p.emit(&DemoMarkEvent{
		TimeMs:     timeMs,
		PlayerSlot: playerSlot,
		Label:      strings.TrimSpace(rest),
	})
}
