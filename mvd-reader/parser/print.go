package parser

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// PrintEvent is emitted when a print message is received. For prints
// wrapped in a `dem_single` MVD message, TargetPlayerNum identifies
// the player slot the server addressed (pickup messages, personal
// damage feedback, centerprint-equivalents). For broadcast prints
// (`dem_all`, `dem_multiple`, or `dem_read` in non-MVD streams) the
// field is -1 — no single target.
//
// Message is a complete console LINE, not necessarily one svc_print
// payload: QuakeC emits a line as a run of `sprint`/`bprint` calls
// ("DARKLORD", "'s rocket", "\n") and the parser reassembles them
// before emitting. See assemblePrintLine for the rules and the
// TimeMs convention.
type PrintEvent struct {
	Level           int
	Message         string
	TargetPlayerNum int // 0-based slot for dem_single; -1 for broadcast prints
	TimeMs          int32
}

func (e *PrintEvent) EventType() EventType { return EventPrint }
func (e *PrintEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *PrintEvent) EventTimeMs() int32   { return e.TimeMs }

// parsePrint parses svc_print message. `targetPlayerNum` is the
// dem_single slot from the MVD container (or -1 for non-dem_single
// wrappers); the caller in parser.go derives it from msg.Header.
//
// The payload is a console *fragment*, not necessarily a line, so it
// goes through assemblePrintLine before anything consumes it.
func (p *Parser) parsePrint(r *mvd.BufferReader, timeMs int32, targetPlayerNum int) error {
	level, err := r.ReadByte()
	if err != nil {
		return err
	}

	message, err := r.ReadString()
	if err != nil {
		return err
	}

	// cleanString is a per-byte map (plus NUL elision), so cleaning each
	// fragment and concatenating is identical to concatenating the raw
	// fragments and cleaning once — the assembly below can work on
	// already-cleaned text. Byte 10 maps to '\n' (userinfo.go:261), so
	// the line terminator survives normalisation.
	return p.assemblePrintLine(int(level), cleanString(message), targetPlayerNum, timeMs)
}

// printLineKey is the buffer key for print-fragment assembly: the print
// level plus the dem_single target slot (-1 for broadcasts).
type printLineKey struct {
	level  int
	target int
}

// printLineBuf is a partially assembled line: text carries no '\n' (the
// assembler always releases up to and including the last one), timeMs is
// the wire time of its FIRST fragment.
type printLineBuf struct {
	text   string
	timeMs int32
}

// assemblePrintLine buffers svc_print fragments into whole console lines
// and hands each completed line to deliverPrintLine.
//
// WHY. QuakeC builds a console line out of several `sprint`/`bprint`
// calls and each one becomes its own svc_print on the wire. Old
// kmod/qwe progs print obituaries id1-style — `ktx/src/items.c:2404,
// 2480, 2534, 2618` shows the same shape still alive in modern KTX for
// the backpack pickup line ("You get ", "the %s", ", ", "20 shells",
// "\n"). A consumer that matches per-svc_print therefore sees
// "DARKLORD", "'s rocket" and "\n" and matches none of them: on
// `4on4_l_vs_la[e1m2]` the obituary table matched zero of 368 deaths
// before this assembly existed.
//
// HOW. This mirrors ezquake's client-side assembler, `CL_ProcessPrint`
// (`ezquake-source/src/cl_parse.c:3072-3105`): append the fragment to a
// buffer, and when the buffer contains a newline flush everything up to
// and INCLUDING the last one (`qwcsrchr(cl.sprint_buf, '\n')`), keeping
// the remainder for the next fragment. ezquake also flushes when the
// print LEVEL changes (`cl.sprint_level`); we express the same rule by
// keying a buffer per level instead of flushing one shared buffer, which
// is equivalent for a single stream and necessary for ours (below).
//
// WHY THE KEY CARRIES THE TARGET TOO. ezquake is one client and sees one
// stream: mvdsv has already routed away every dem_single addressed at
// somebody else. We demultiplex the whole recording, so broadcast and
// per-client fragments interleave in one call sequence — and they
// genuinely interleave *inside* a single line: `ktx/src/items.c:2485`
// emits mi_print to other clients between the "the %s" and ", " pieces
// of the backpack line. Keying on level alone would splice a broadcast
// into a personal line and vice versa, so the key is (level, target).
// This is also what protects the KTX pickup prints ktx_pickup_print.go
// consumes: those are dem_single PRINT_LOW and can never merge with a
// broadcast fragment.
//
// FRAME BOUNDARY. Any incoming fragment releases EVERY pending buffer
// from an earlier frame, not just its own key's. Every fragment of one
// line is written inside the server frame that produced it, so a buffer
// from an earlier frame is definitionally a finished (or abandoned)
// line; without this a line whose terminator never arrived would
// swallow an unrelated print minutes later. The sweep is deliberately
// global: releasing only the incoming key's buffer would let a stale
// buffer on another key — say an unterminated match-start announcement
// — sit pending while later prints are delivered, emitting events out
// of chronological order and letting the matchStarted gate judge those
// events before the line that would have started the match
// (external-review finding, 2026-07-27).
//
// TIME. The assembled event carries the wire time of the line's FIRST
// fragment — the moment the server started saying it, and the only
// choice that keeps an obituary's DeathEvent at the frame the kill
// happened in.
func (p *Parser) assemblePrintLine(level int, msg string, target int, timeMs int32) error {
	key := printLineKey{level: level, target: target}

	// Frame boundary — see the doc comment: every buffer from an earlier
	// frame is released before this fragment is handled, keeping the
	// delivered event stream in wire order across keys.
	if err := p.flushPrintLinesOutsideFrame(timeMs); err != nil {
		return err
	}

	text := msg
	start := timeMs
	if buf, ok := p.printLines[key]; ok {
		// Same frame is guaranteed by the sweep above.
		text = buf.text + msg
		start = buf.timeMs
	}

	nl := strings.LastIndexByte(text, '\n')
	if nl < 0 {
		if p.printLines == nil {
			p.printLines = make(map[printLineKey]*printLineBuf)
		}
		p.printLines[key] = &printLineBuf{text: text, timeMs: start}
		return nil
	}
	if rest := text[nl+1:]; rest != "" {
		if p.printLines == nil {
			p.printLines = make(map[printLineKey]*printLineBuf)
		}
		// The remainder came out of the fragment that just arrived, so it
		// starts in the current frame, not the buffered one.
		p.printLines[key] = &printLineBuf{text: rest, timeMs: timeMs}
	} else {
		delete(p.printLines, key)
	}
	return p.deliverPrintLine(level, text[:nl+1], target, start)
}

// flushPendingPrintLines releases every unterminated buffer. Called once
// the stream is over (clean end of demo or truncation) so a line whose
// terminating "\n" never made it into the recording is still reported
// rather than silently dropped. Order is deterministic (time, level,
// target) because Go map iteration is not.
func (p *Parser) flushPendingPrintLines() error {
	return p.flushPrintLinesWhere(func(*printLineBuf) bool { return true })
}

// flushPrintLinesOutsideFrame releases every pending buffer whose first
// fragment arrived in a different frame than timeMs — the global
// frame-boundary sweep assemblePrintLine documents.
func (p *Parser) flushPrintLinesOutsideFrame(timeMs int32) error {
	return p.flushPrintLinesWhere(func(b *printLineBuf) bool { return b.timeMs != timeMs })
}

// flushPrintLinesWhere delivers, in deterministic (time, level, target)
// order, every pending buffer the predicate selects.
func (p *Parser) flushPrintLinesWhere(pred func(*printLineBuf) bool) error {
	if len(p.printLines) == 0 {
		return nil
	}
	keys := make([]printLineKey, 0, len(p.printLines))
	for k := range p.printLines {
		if pred(p.printLines[k]) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if ta, tb := p.printLines[a].timeMs, p.printLines[b].timeMs; ta != tb {
			return ta < tb
		}
		if a.level != b.level {
			return a.level < b.level
		}
		return a.target < b.target
	})
	for _, k := range keys {
		buf := p.printLines[k]
		delete(p.printLines, k)
		if err := p.deliverPrintLine(k.level, buf.text, k.target, buf.timeMs); err != nil {
			return err
		}
	}
	return nil
}

// deliverPrintLine emits one assembled console line and runs every
// print-derived side effect on it.
//
// Aside from emitting PrintEvent (and any KTX pickup-print follow-up),
// the broadcast obituary path is mined here for DeathEvent: KTX's
// fragfile lines (X was rocketed by Y, etc.) are the only signal that
// fires when the server compresses a death/respawn cycle into a single
// svc_playerinfo gap — short enough that DF_DEAD never flips on the
// wire and the dem_stats block carrying the health drop is addressed
// to a different POV. maybeEmitDeath dedupes against the other two
// sources so we don't double-count when they do fire.
func (p *Parser) deliverPrintLine(level int, msg string, target int, timeMs int32) error {
	if err := p.emit(&PrintEvent{
		Level:           level,
		Message:         msg,
		TargetPlayerNum: target,
		TimeMs:          timeMs,
	}); err != nil {
		return err
	}
	// After the PrintEvent above, before the obituary path below: a
	// consumer of the triggering print still sees the pre-start state,
	// and the obituary corroborator this gate opens sees the post-start
	// state from the very next line.
	if err := p.tryEmitMatchStartFromPrint(level, msg, timeMs); err != nil {
		return err
	}
	if err := p.tryEmitObituaryDeath(msg, timeMs); err != nil {
		return err
	}
	if err := p.tryEmitRosterPrint(level, msg, timeMs); err != nil {
		return err
	}
	return p.tryEmitPickupPrint(level, msg, target, timeMs)
}

// tryEmitObituaryDeath inspects an obituary print line, resolves the
// named victim to a player slot via the userinfo table, and fires
// DeathEvent via forceEmitDeath (bypassing the
// skip-if-already-dead dedup). KTX is authoritative for whether a
// death happened: obits map 1:1 to "deaths++" on the server-side
// scoreboard, even in the pent-deflection corner case where the
// player's entity state never visibly leaves the previous dead
// interval. See forceEmitDeath's doc for the full rationale and
// the two scenarios (tight respawn cycles, dtTELE2 deflections).
//
// Gated on p.matchStarted: warmup-era obits (and the telefrag obits
// that fire at the *exact* wire time of the start print but earlier
// in the message order — see comment on Parser.matchStarted) are
// silenced so they cannot pre-seed the parser dedup state and starve
// the stat-based detector of its match-start emission. After the
// gate opens, the follow-up SpawnEvent arrives naturally on the
// player's next svc_playerinfo frame with DF_DEAD clear — the same
// state-transition the existing maybeEmitSpawn path detects.
func (p *Parser) tryEmitObituaryDeath(msg string, timeMs int32) error {
	if !p.matchStarted {
		return nil
	}
	victim, _ := FindObituaryVictim(msg)
	if victim == "" {
		return nil
	}
	slot := p.lookupSlotByName(victim)
	if slot < 0 {
		return nil
	}
	return p.forceEmitDeath(slot, timeMs)
}

// MatchStartPatterns is the canonical set of case-insensitive substrings
// that mark a match start in a broadcast print line. It is one of the four
// signals behind MatchStartEvent (see matchstart.go) — the print one, and
// the only one that reaches pre-KTX servers. Match-END phrases gate no
// parser behaviour and stay in the analyzer.
//
// Only ONE phrase here is a verified modern-KTX server broadcast:
// "has begun" (G_bprint at PRINT_MEDIUM, ktx/src/match.c:1296). The other
// five are not reachable from a current KTX server through svc_print —
// "fight!" is a G_centerprint / G_cp2all (ktx/src/arena.c:602,617-618;
// clan_arena.c:1402-1403,1537), "go!" is a G_cp2all (race.c:2614),
// "game start" only occurs inside the centerprinted countdown "N seconds
// left before game starts" (admin.c:624), "match started" is a C comment
// (commands.c:5123), and "begins in 1" has no printed string anywhere in
// ktx/, mvdsv/ or ezquake-source/. All five are kept anyway: dropping a
// pattern can only lose match-start detection on some mod nobody here has
// a demo for, and a centerprint-only phrase costs nothing here because it
// arrives as svc_centerprint and never reaches the print path. See
// MVD_FORMAT.md's match-start table for the per-entry provenance.
var MatchStartPatterns = []string{
	// "has begun" rather than "match has begun": KTX prints "The match has
	// begun!" (ktx/src/match.c:1296), but kmod/qwe announces the MODE —
	// "The duel has begun!" — and the narrower pattern missed it. A 2003
	// kmod duel in the test corpus therefore detected no match start at
	// all, which left every stream empty (streams only record between
	// Started and Ended) and silently dropped the whole streams-derived
	// half of the pipeline. This entry is still tighter than "go!" below.
	"has begun",
	"match started",
	"fight!",
	"go!",
	"begins in 1",
	"game start",
}

// lookupSlotByName finds the player slot whose userinfo name matches
// the supplied display name. Returns -1 when no slot matches. Names
// are case-sensitive — KTX prints render the userinfo name verbatim so
// the obit line's name and the userinfo name are byte-identical.
func (p *Parser) lookupSlotByName(name string) int {
	for slot, info := range p.players {
		if info == nil {
			continue
		}
		if info.Name == name {
			return slot
		}
	}
	return -1
}
