package analyzer

import (
	"strings"

	"github.com/mvd-analyzer/mvd-reader/parser"
)

// parsedObituary is the neutral, weapon-and-name result of matching one
// KTX / mvdsv obituary or suicide print line, independent of how a consumer
// represents it. FragAnalyzer maps it to a FragEntry (adding the
// team-membership teamkill test it has always done); MessagesAnalyzer maps it
// to a MatchEvent.
//
// The string→classification data all lives in ONE canonical table in
// mvd-reader (parser.ObituaryPatterns); the matchers below build their
// working slices from it by Kind. frag.go's behavior is the reference: the
// slices and walker order below reproduce it exactly, and the messages
// output matches it. The one form with no table row is the " ate N loads of
// X's buckshot" splash parser (matchAte) — it is not a marker→weapon mapping
// but a bespoke sub-parse, so it stays inline here.
type parsedObituary struct {
	Killer string
	Victim string
	Weapon string
	// Suicide marks a self-kill: checkSuicide patterns, the Satan's-power
	// self-telefrag, and "X gets a frag for the other team" (frag.go books
	// that one as a suicide until recoverTeamkills finds the real victim).
	Suicide bool
	// TeamKill marks a phrasing-based teamkill — one of Killer/Victim is the
	// generic "teammate" placeholder because the obituary named only one
	// party. The membership-based teamkill test (killer and victim resolve
	// to the same team) is deliberately NOT done here: frag.go applies it in
	// its own mapper against ctx.Players, and messages.go doesn't need it.
	TeamKill bool
	// Cause is the pattern row's sub-cause beneath Weapon ("discharge",
	// "deflect", "spawnicide"; see parser.ObituaryPattern.Cause), plus the
	// one promotion the table cannot express: "accepts X's discharge" shares
	// its marker with "accepts X's shaft", so matchKill sets it off the
	// suffix.
	Cause string
	// Other is the named party who is NEITHER killer nor victim: on the
	// dtTELE3 double-pentagram row ("X was telefragged by Y's Satan's
	// power") the death is booked as X's own suicide, and Y — the pentagram
	// holder X died on — would otherwise be lost. Empty everywhere else.
	Other string
}

// obituaryPatternsOfKind filters the canonical reader table to one Kind,
// preserving table order (which is load-bearing within a Kind).
func obituaryPatternsOfKind(k parser.ObituaryKind) []parser.ObituaryPattern {
	var out []parser.ObituaryPattern
	for _, p := range parser.ObituaryPatterns {
		if p.Kind == k {
			out = append(out, p)
		}
	}
	return out
}

// The per-Kind matcher slices, sourced once from the canonical reader table.
var (
	suicidePatterns        = obituaryPatternsOfKind(parser.ObituarySuicide)
	killPatterns           = obituaryPatternsOfKind(parser.ObituaryKill)
	gibbedPatterns         = obituaryPatternsOfKind(parser.ObituaryGibbed)
	teamkillVictimPatterns = obituaryPatternsOfKind(parser.ObituaryTeamkillVictim)
	teamkillKillerPatterns = obituaryPatternsOfKind(parser.ObituaryTeamkillKiller)
	killerFirstPatterns    = obituaryPatternsOfKind(parser.ObituaryKillerFirst)
	satanPatterns          = obituaryPatternsOfKind(parser.ObituaryInfix)
)

// parseObituaryLine matches msg against the full obituary/suicide pattern set
// in frag.go's canonical order: suicide, "ate N loads" (SSG/RL), killer-first
// (rips / stomps / squishes), then the kill group — phrasing teamkills,
// victim-first kill patterns, gibbed-by, Satan-deflect. Returns nil when msg
// is not an obituary.
//
// Order is semantic and load-bearing (Phase 1 F3 fixed the CRMod "eats 2
// scoops" vs generic " eats " ordering within killPatterns); do not reorder a
// group or a within-group entry without re-checking the golden corpus.
func parseObituaryLine(msg string) *parsedObituary {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	if o := matchSuicide(msg); o != nil {
		return o
	}
	if o := matchAte(msg); o != nil {
		return o
	}
	if o := matchKillerFirst(msg); o != nil {
		return o
	}
	// The kill group. checkTeamKill runs before the victim-first kill
	// patterns so "X was telefragged by his teammate" matches the teamkill
	// form before the shorter " was telefragged by " marker.
	if o := matchTeamKill(msg); o != nil {
		return o
	}
	if o := matchKill(msg); o != nil {
		return o
	}
	if o := matchGibbedBy(msg); o != nil {
		return o
	}
	if o := matchSatanDeflect(msg); o != nil {
		return o
	}
	return nil
}

// matchSuicide scans the self-kill phrases. See parser.ObituaryPatterns for
// the KTX client.c origins.
func matchSuicide(msg string) *parsedObituary {
	for _, p := range suicidePatterns {
		if idx := strings.Index(msg, p.Marker); idx > 0 {
			// Suffix on a suicide row is a REQUIRED discriminator, not a
			// victim bound: KTX's dtTELE3 prints the same " was telefragged
			// by " verb the kill row uses but books the death as the
			// victim's own suicide (parser.ObituaryPatterns).
			if p.Suffix != "" && !strings.Contains(msg[idx+len(p.Marker):], p.Suffix) {
				continue
			}
			// LineEnd anchors a marker short enough to hide inside a player
			// name (" died") to the end of the line, where both engine
			// prints that produce it put it.
			if p.LineEnd && strings.TrimSpace(msg[idx+len(p.Marker):]) != "" {
				continue
			}
			victim := strings.TrimSpace(msg[:idx])
			if victim != "" {
				o := &parsedObituary{Killer: victim, Victim: victim, Weapon: p.Weapon, Suicide: true, Cause: p.Cause}
				if p.Suffix != "" {
					// The text between marker and suffix names the other
					// party (dtTELE3: the surviving pentagram holder).
					rest := msg[idx+len(p.Marker):]
					o.Other = strings.TrimSpace(rest[:strings.Index(rest, p.Suffix)])
				}
				return o
			}
		}
	}
	return nil
}

// matchAte handles "victim ate N loads of killer's buckshot" (SSG) and the
// rarer "... N rockets from killer" (RL) splash attribution. This is a
// bespoke sub-parse, not a marker→weapon mapping, so it carries no
// parser.ObituaryPatterns row.
func matchAte(msg string) *parsedObituary {
	idx := strings.Index(msg, " ate ")
	if idx <= 0 {
		return nil
	}
	victim := strings.TrimSpace(msg[:idx])
	rest := msg[idx+5:]

	// "ate N loads of X's buckshot" = SUPER SHOTGUN.
	if strings.Contains(rest, "'s buckshot") {
		killerEnd := strings.Index(rest, "'s buckshot")
		loadsIdx := strings.Index(rest, " loads of ")
		if loadsIdx >= 0 && loadsIdx < killerEnd {
			killer := strings.TrimSpace(rest[loadsIdx+10 : killerEnd])
			return &parsedObituary{Killer: killer, Victim: victim, Weapon: "ssg"}
		}
	}
	if strings.Contains(rest, "'s rocket") || strings.Contains(rest, " rockets from ") {
		if loadsIdx := strings.Index(rest, " rockets from "); loadsIdx >= 0 {
			killer := stripQuadSuffix(strings.TrimSpace(rest[loadsIdx+14:]))
			return &parsedObituary{Killer: killer, Victim: victim, Weapon: "rl"}
		}
	}
	return nil
}

// matchKillerFirst handles the "killer <verb> victim" forms (X_FRAGS_Y),
// driven by the ObituaryKillerFirst rows. When a row carries a Suffix the
// victim is bounded by it ("X rips Y a new one"); otherwise the victim is the
// rest of the line ("X stomps Y").
func matchKillerFirst(msg string) *parsedObituary {
	for _, p := range killerFirstPatterns {
		idx := strings.Index(msg, p.Marker)
		if idx <= 0 {
			continue
		}
		killer := strings.TrimSpace(msg[:idx])
		rest := msg[idx+len(p.Marker):]
		var victim string
		if p.Suffix != "" {
			end := strings.Index(rest, p.Suffix)
			if end <= 0 {
				continue
			}
			victim = strings.TrimSpace(rest[:end])
		} else {
			victim = strings.TrimSpace(rest)
		}
		if killer != "" && victim != "" {
			return &parsedObituary{Killer: killer, Victim: victim, Weapon: p.Weapon}
		}
	}
	return nil
}

// matchTeamKill handles the phrasing-based teamkills where the obituary names
// only one party; the other becomes the generic "teammate" placeholder.
func matchTeamKill(msg string) *parsedObituary {
	// Killer-named ("X loses another friend"): victim is generic.
	for _, p := range teamkillKillerPatterns {
		if idx := strings.Index(msg, p.Marker); idx > 0 {
			player := strings.TrimSpace(msg[:idx])
			return &parsedObituary{
				Killer:   player,
				Victim:   "teammate",
				Weapon:   p.Weapon,
				Suicide:  p.Suicide,
				TeamKill: true,
			}
		}
	}
	// Victim-named ("X was telefragged by his teammate"): killer is generic.
	for _, p := range teamkillVictimPatterns {
		if idx := strings.Index(msg, p.Marker); idx > 0 {
			victim := strings.TrimSpace(msg[:idx])
			return &parsedObituary{
				Killer:   "teammate",
				Victim:   victim,
				Weapon:   p.Weapon,
				TeamKill: true,
			}
		}
	}
	return nil
}

// matchKill walks the victim-first kill patterns and disambiguates the
// shared " was blown to chunks by " verb by weapon suffix.
func matchKill(msg string) *parsedObituary {
	for _, p := range killPatterns {
		if idx := strings.Index(msg, p.Marker); idx > 0 {
			victim := strings.TrimSpace(msg[:idx])
			rest := msg[idx+len(p.Marker):]
			killer := extractKillerName(rest)

			weapon := p.Weapon
			// "X was blown to chunks by Y's rocket" (rl) vs "... Y's grenade"
			// (gl) share the verb — disambiguate via the suffix.
			if p.Marker == " was blown to chunks by " {
				if strings.Contains(rest, "'s grenade") || strings.HasSuffix(strings.TrimSpace(rest), "' grenade") {
					weapon = "gl"
				}
			}
			// "X accepts Y's shaft" (dtLG_BEAM) vs "X accepts Y's discharge"
			// (dtLG_DIS, ktx/src/client.c:5656) share the verb — the suffix
			// carries the cause.
			cause := p.Cause
			if p.Marker == " accepts " {
				if strings.Contains(rest, "'s discharge") || strings.HasSuffix(strings.TrimSpace(rest), "' discharge") {
					cause = "discharge"
				}
			}

			if victim != "" && killer != "" {
				return &parsedObituary{Killer: killer, Victim: victim, Weapon: weapon, Cause: cause}
			}
		}
	}
	return nil
}

// matchGibbedBy handles "was gibbed by X's grenade/rocket" where the weapon
// depends on the suffix.
func matchGibbedBy(msg string) *parsedObituary {
	for _, p := range gibbedPatterns {
		idx := strings.Index(msg, p.Marker)
		if idx <= 0 {
			continue
		}
		victim := strings.TrimSpace(msg[:idx])
		rest := msg[idx+len(p.Marker):]

		weapon := p.Weapon // "rl" default
		if strings.Contains(rest, "'s grenade") || strings.HasSuffix(strings.TrimSpace(rest), "' grenade") {
			weapon = "gl"
		}

		killer := extractKillerName(rest)
		if victim == "" || killer == "" {
			continue
		}
		return &parsedObituary{Killer: killer, Victim: victim, Weapon: weapon}
	}
	return nil
}

// matchSatanDeflect handles the "Satan's power deflects X's telefrag"
// self-telefrag suicide (KTX dtTELE2), an infix form the prefix suicide loop
// can't catch.
func matchSatanDeflect(msg string) *parsedObituary {
	for _, p := range satanPatterns {
		if !strings.HasPrefix(msg, p.Marker) {
			continue
		}
		rest := msg[len(p.Marker):]
		end := strings.Index(rest, p.Suffix)
		if end <= 0 {
			continue
		}
		victim := strings.TrimSpace(rest[:end])
		if victim == "" {
			continue
		}
		return &parsedObituary{Killer: victim, Victim: victim, Weapon: p.Weapon, Suicide: p.Suicide, Cause: p.Cause}
	}
	return nil
}
