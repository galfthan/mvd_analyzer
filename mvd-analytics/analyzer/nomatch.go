package analyzer

import (
	"fmt"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// ServerStatus is the serverinfo `status` key as a timeline rather than a
// last-write-wins value: what the server said at demo open, and whether it
// ever said a game was running. Published by the metadata node; read by
// noMatchPost. See events.StatusNamesRunningGame for the spellings.
type ServerStatus struct {
	// AtOpen is the `status` value in the OPENING `fullserverinfo` dump,
	// verbatim. Empty when that dump carried no `status` key — including
	// when a later svc_serverinfo update sets one, which is a statement
	// about an instant inside the recording, not about demo open.
	// RunningSeen below is where those later readings land.
	AtOpen string
	// RunningSeen is set when `status` named a running game at any point —
	// at open, or in a later svc_serverinfo update.
	RunningSeen bool
}

// noMatchPost is the DAG node "no-match": it stamps result.NoMatch on a
// Result that came out with no player streams, naming why.
//
// It exists because the empty result was previously silent. Streams are the
// spine of this pipeline — every derived section hangs off them — and they
// are built only inside the detected match window (timeline.go gates every
// recording path on a.timing.Started), so a demo whose match start was never
// announced produces `buildStreamsResult` → nil and, transitively, nothing
// else. The v52 `timeBase:"demo"` fallback that was supposed to flag exactly
// this cannot: flagDemoTimeBase returns early on `result.Streams == nil`, so
// it fires only for the narrow case where a match start WAS seen but landed
// at demo t=0. Over the 50 951-demo archive sweep, 1 032 demos (2.0%) came
// out with no streams and an entirely empty errors[].
//
// The marker is not an errors[] entry on purpose. errors[] means the
// pipeline failed; "this recording holds no match" is a fact about the demo.
// The one reason that IS a failure, demoUnreadable, names itself and leaves
// the reader's message in errors[].
func noMatchPost(res *Result, co *CoreOutputs) {
	if res == nil {
		return
	}
	// One spelling of the predicate, the same one wallClockPost routes on
	// and the same one every doc site states: the marker is present exactly
	// when `streams` is absent. "No player streams" and "no streams block"
	// are the same state by construction — buildStreamsResult ends with
	// `if len(streams.Players) == 0 { return nil }`
	// (timeline_streams.go:731) and is the only writer of result.Streams
	// (timeline_finalize.go:354) — so testing the players too would be a
	// second, weaker spelling of the contract and would make a split-homed
	// result (streams AND noMatch) constructible.
	if res.Streams != nil {
		return
	}

	var status ServerStatus
	if co != nil {
		status = co.ServerStatus
	}
	gameDir := ""
	if res.Metadata != nil {
		gameDir = res.Metadata.ServerInfo["*gamedir"]
	}
	kills := 0
	if res.Frags != nil {
		kills = len(res.Frags.Frags)
	}

	nm := &NoMatchResult{
		StatusAtOpen:      status.AtOpen,
		StatusRunningSeen: status.RunningSeen,
		GameDir:           gameDir,
		Kills:             kills,
	}
	nm.Reason, nm.Detail = noMatchVerdict(res.Errors, status, gameDir, kills)
	res.NoMatch = nm
}

// noMatchVerdict picks the reason and writes the human sentence for it.
//
// The order of the cases is the order of evidence strength:
//
//   - A truncated read comes first because it invalidates the rest: the
//     match-start announcement may sit past the truncation point, so
//     "nothing was declared" is not a conclusion the bytes support.
//   - `status` at open naming a running game is a direct statement by the
//     server that the recording began after the match did.
//   - `status` reaching a running value later is the same statement about an
//     instant inside the recording — the server did start a match, and the
//     announcement it made was not one this pipeline recognises.
//   - Otherwise no match declaration reached this pipeline, and the only
//     question left is what the frag log parsed.
//
// The last two cases state an ABSENCE OF EVIDENCE, and their sentences say
// so: 168 of the 170 noMatchDeclared demos carry no `status` key at all, and
// a mod that never writes one can still be running a managed match — the
// vocabulary this pipeline reads is KTX's. So the verdict is "no declaration
// we can see", hedged with "or a managed match on a mod whose declarations
// this pipeline cannot read", never "unmanaged play" flat.
//
// The last two cases are also where the archive's foreign content
// (TeamFortress, CTF, custom gamedirs) lands, but the gamedir is NOT what
// decides them: a `fortress` server can run a managed match with its own
// countdown, and a stock `qw` server can record ten seconds of nothing. The
// gamedir rides along as evidence instead.
func noMatchVerdict(errs []string, status ServerStatus, gameDir string, kills int) (reason, detail string) {
	if hasStreamAbort(errs) {
		return NoMatchDemoUnreadable, "the event stream aborted before the demo was read to the end, so whether it holds a match is unknown; see errors[] for the reader's reason"
	}
	if events.StatusNamesRunningGame(status.AtOpen) {
		return NoMatchMidMatchRecording, fmt.Sprintf(
			"the recording starts mid-game: the server already reported %q at demo open, so the match-start announcement this pipeline keys on happened before the first frame%s",
			status.AtOpen, killsClause(kills))
	}
	if status.RunningSeen {
		return NoMatchStartUnannounced, fmt.Sprintf(
			"the server started a match during the recording (its `status` key went to a running game) but never broadcast a match-start line this pipeline recognises%s%s",
			gameDirClause(gameDir), killsClause(kills))
	}
	if kills > 0 {
		return NoMatchNoMatchDeclared, fmt.Sprintf(
			"no match declaration this pipeline can see — the `status` key never named a running game — but the frag log parsed %d kill(s)%s: unmanaged play, or a managed match on a mod whose declarations this pipeline cannot read",
			kills, gameDirClause(gameDir))
	}
	return NoMatchNoPlayRecorded, fmt.Sprintf(
		"no match declaration this pipeline can see — the `status` key never named a running game — and the frag log parsed no kills%s: an idle or aborted server, or a mod whose declarations and obituaries this pipeline cannot read",
		gameDirClause(gameDir))
}

// gameDirClause names the mod when the server ran something other than the
// stock deathmatch gamedir, and says nothing at all otherwise — "gamedir qw"
// is the default and adds no information to a sentence.
func gameDirClause(gameDir string) string {
	if gameDir == "" || gameDir == "qw" {
		return ""
	}
	return fmt.Sprintf(" (gamedir %q, a mod whose rules this pipeline does not model)", gameDir)
}

func killsClause(kills int) string {
	if kills == 0 {
		return ""
	}
	return fmt.Sprintf("; the frag log still parsed %d kill(s)", kills)
}

// hasStreamAbort reports whether the reader gave up mid-demo. The registry
// is the only writer of this entry (registry.go, streamAbortedPrefix).
func hasStreamAbort(errs []string) bool {
	for _, e := range errs {
		if strings.HasPrefix(e, streamAbortedPrefix) {
			return true
		}
	}
	return false
}
