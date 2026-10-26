package parser

import (
	"strconv"
	"strings"
)

// FinalScoresEvent is the typed representation of KTX's `//finalscores`
// stufftext — the end-of-match scoreline the server stuffs into the first
// connected client when it files the match into its lastscores ring
// (ktx/src/commands.c:6963-6977, inside lastscore_add):
//
//	stuffcmd(cl, "//finalscores \"%s\" \"%s\" \"%s\" \"%s\" %d \"%s\" %d\n",
//	         qtvdate, lastscores2str(lst), mapname, e1, s1, e2, s2);
//
// so the wire form is seven space-separated arguments, five of them quoted:
//
//		//finalscores "Sep 29, 21:27" "duel" "aerowalk" "kip" 19 "grisling" 24
//
//	  - Date is strftime "%b %d, %H:%M" of the server's LOCAL clock — no year,
//	    no seconds, no timezone. It is a corroborating date marker only; the
//	    analytics wall-clock node completes its year from another marker and
//	    never anchors a match on it alone.
//	  - Mode is lastscores2str (commands.c:6755): "duel", "team", "FFA", "CTF",
//	    "RA", "Clan Arena", "Wipeout", "HoonyMode", "race", "unknown" — plus
//	    whatever a fork adds ("Extinction" is observed in the archive). It is
//	    KTX's own mode name, in the same family as the demoinfo `mode` string
//	    but NOT identical to it ("FFA" vs "ffa", "Clan Arena" vs "clan-arena").
//	  - Map is `mapname`, the canonical short name ("dm3", "aerowalk").
//	  - Team1/Score1, Team2/Score2 are the two sides with their names — on a
//	    duel the "team" is the player's own name, which is exactly the
//	    player-as-team layout the analytics roster uses. Scores are the server's
//	    own final figures and CAN be negative (suicides): 1.8% of a 12 000-demo
//	    archive sample carries a negative side. What they COUNT depends on the
//	    mode: get_scores1/2 (summed frags) normally, but CA_get_score_1/2 —
//	    ROUNDS WON — on Clan Arena and Wipeout (commands.c:6867-6886).
//
// The stuffcmd goes to a single client, so mvdsv records it as a dem_single
// block and the line appears once per match. The mvdsv `qtvdate` HACK comment
// above the stuffcmd names its original consumer (EZTV); nothing else on the
// wire states the final scoreline on a pre-ktxstats demo, which is what makes
// it worth decoding — it covers 64.1% of the 51k-demo archive against
// demoinfo's 45.6%.
type FinalScoresEvent struct {
	Date   string // "%b %d, %H:%M" server-local stamp, no year ("Sep 29, 21:27")
	Mode   string // KTX lastscores2str name ("duel", "team", "FFA", ...)
	Map    string // canonical short map name
	Team1  string // first team (or, on a duel, the first player)
	Score1 int
	Team2  string
	Score2 int
	TimeMs int32
}

func (e *FinalScoresEvent) EventType() EventType { return EventFinalScores }
func (e *FinalScoresEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *FinalScoresEvent) EventTimeMs() int32   { return e.TimeMs }

const finalScoresPrefix = "//finalscores"

// finalScoresField is one argument of the directive, with the quoting it
// arrived under — a name field that lost its quotes is a garbled line, not a
// name, so the shape check needs to see the difference.
type finalScoresField struct {
	text   string
	quoted bool
}

// tryEmitFinalScores emits a typed FinalScoresEvent for a `//finalscores`
// stufftext. The prefix must end at a token boundary, so `//finalscoresX`
// does not match.
//
// Parsing is deliberately shape-strict: the seven arguments must be present in
// KTX's order and quoting, and both scores must be integers. A demo carrying a
// truncated or corrupted copy — the archive shows stuffed cvar values getting
// mangled outright, e.g. a `timelimit` value overwritten with "Final Score is
// 47 - 9" — would otherwise hand analytics a plausible-looking scoreline built
// from fragments of the wrong fields. A line that fails the shape is dropped
// with a diagnostic warning; the generic StuffTextEvent the caller already
// emitted keeps the raw text available either way.
func (p *Parser) tryEmitFinalScores(cmd string, timeMs int32) error {
	s := strings.TrimRight(cmd, "\n\r")
	if !strings.HasPrefix(s, finalScoresPrefix) {
		return nil
	}
	rest := s[len(finalScoresPrefix):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return nil
	}

	fields, ok := splitFinalScoresFields(rest)
	if !ok || len(fields) != 7 {
		p.warn(timeMs, "parse_error", "//finalscores: expected 7 fields, got %d: %q", len(fields), s)
		return nil
	}
	for _, i := range [...]int{0, 1, 2, 3, 5} {
		if !fields[i].quoted {
			p.warn(timeMs, "parse_error", "//finalscores: field %d is unquoted: %q", i+1, s)
			return nil
		}
	}
	score1, err1 := strconv.Atoi(fields[4].text)
	score2, err2 := strconv.Atoi(fields[6].text)
	if err1 != nil || err2 != nil || fields[4].quoted || fields[6].quoted {
		p.warn(timeMs, "parse_error", "//finalscores: non-integer scores: %q", s)
		return nil
	}

	// The team names are player-authored userinfo values and arrive in the
	// Quake charset, colour codes and all — normalise them exactly as the
	// userinfo path does (userinfo.go:166-168), so a consumer can compare a
	// side here with the roster's team labels. The other three fields are
	// server-generated ASCII and stay verbatim: normalising a corrupted byte
	// in the date or the map name would turn garbage into something that
	// parses, which is the opposite of what the shape checks above are for.
	return p.emit(&FinalScoresEvent{
		Date:   fields[0].text,
		Mode:   fields[1].text,
		Map:    fields[2].text,
		Team1:  cleanString(fields[3].text),
		Score1: score1,
		Team2:  cleanString(fields[5].text),
		Score2: score2,
		TimeMs: timeMs,
	})
}

// splitFinalScoresFields tokenises the argument tail into quoted strings and
// bare words. A quoted field runs to the next '"' (KTX has no escape form:
// mvdsv strips '"' out of every userinfo value, so a name can never carry
// one), and an unterminated quote makes the whole line unparseable rather
// than silently swallowing the rest of it.
func splitFinalScoresFields(s string) ([]finalScoresField, bool) {
	var out []finalScoresField
	for i := 0; i < len(s); {
		switch s[i] {
		case ' ', '\t':
			i++
		case '"':
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				return out, false
			}
			out = append(out, finalScoresField{text: s[i+1 : i+1+end], quoted: true})
			i += end + 2
		default:
			j := strings.IndexAny(s[i:], " \t")
			if j < 0 {
				j = len(s) - i
			}
			out = append(out, finalScoresField{text: s[i : i+j]})
			i += j
		}
	}
	return out, true
}
