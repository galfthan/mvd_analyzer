package parser

import "strings"

// This file hosts the single canonical obituary (death-print) table for
// the whole project. The ground truth for the marker strings and their
// weapon codes is the KTX server mod (ktx/src/client.c death-message
// table). The table lives in Layer 1 (mvd-reader) and is consumed by two
// sides:
//
//   - Layer 1, here: FindObituaryVictim projects the victim-prefix subset
//     to recover a dying player's name for the parser's corroborating
//     DeathEvent (print.go's maybeEmitDeath path). Only the *name* is used
//     by the parser; the weapon/suicide/teamkill classification is carried
//     for the benefit of Layer 2.
//   - Layer 2, mvd-analytics/analyzer/obituary_parse.go: builds every frag
//     and message matcher by filtering this table by Kind (analytics may
//     import mvd-reader; the reverse is forbidden). That analyzer owns the
//     killer-name resolution and the one bespoke splash parser (" ate N
//     loads of X's buckshot"), which is not a marker→weapon mapping and so
//     has no row here — everything that *is* a marker→classification
//     mapping lives in this one table.
//
// A previous cleanup left a comment here claiming the two sides already
// shared one table; they did not, and the copies had drifted (" becomes
// bored with life" resolved to "suicide" here vs "rl" in analytics; this
// table lacked the killer-first, teamkill-killer, and CRMod/splash forms).
// This is the real unification: analytics semantics are the reference
// (the golden frag log pins them), so the values below are the analytics
// values.

// ObituaryKind classifies how the victim (and killer) name sits around a
// marker, so each consumer knows how to extract the name(s).
type ObituaryKind uint8

const (
	// ObituarySuicide: "<victim> <marker>" — self-kill; the victim is the
	// whole prefix and there is no killer.
	ObituarySuicide ObituaryKind = iota
	// ObituaryKill: "<victim> <marker> <killer suffix>" — the victim is the
	// prefix; the killer's name follows the marker (resolved by the
	// analytics extractor from the trailing weapon suffix).
	ObituaryKill
	// ObituaryGibbed: like ObituaryKill, but the weapon depends on the
	// trailing suffix ("'s rocket" → rl, "'s grenade" → gl). Kept a distinct
	// kind so the analytics kill loop does not pin it to one weapon.
	ObituaryGibbed
	// ObituaryTeamkillVictim: "<victim> <marker>" phrasing teamkill that
	// names only the victim; the killer is the generic "teammate".
	ObituaryTeamkillVictim
	// ObituaryTeamkillKiller: "<killer> <marker>" phrasing teamkill that
	// names only the killer; the victim is the generic "teammate".
	ObituaryTeamkillKiller
	// ObituaryKillerFirst: "<killer> <marker> <victim>[<suffix>]" — the
	// killer is the prefix and the victim follows the marker (bounded by
	// Suffix when set, else the rest of the line).
	ObituaryKillerFirst
	// ObituaryInfix: "<marker><victim><suffix>" — the victim is bracketed by
	// a fixed prefix (Marker) and Suffix (KTX's Satan's-power self-telefrag).
	ObituaryInfix
)

// ObituaryPattern is one death-print marker with its classification.
// `Marker` is the identifying substring; `Suffix` bounds the victim for the
// infix / killer-first kinds and, on a SUICIDE row, is an extra REQUIRED
// substring that disambiguates a marker shared with a kill row (KTX's
// dtTELE3 double-pentagram telefrag prints the ordinary " was telefragged
// by " verb but books the death as the victim's own suicide); empty
// otherwise. `Weapon` is the canonical short code for downstream
// attribution (rl, lg, sg, …, plus the synthetic "suicide", "water",
// "lava", "world", "explobox", "fall", "squish", "tele", "teamkill",
// "unknown"); `Suicide` / `TeamKill` flag the variants consumers bucket
// separately.
type ObituaryPattern struct {
	Marker string
	Suffix string
	Weapon string
	// LineEnd requires the marker to END the line rather than merely appear
	// in it. Set on markers short enough that a player NAME could contain
	// them (" died"); both engine prints that produce it put it last, so the
	// anchor costs nothing and removes the substring hazard.
	LineEnd  bool
	Suicide  bool
	TeamKill bool
	Kind     ObituaryKind
}

// ObituaryPatterns is the canonical table. Order within a Kind is
// load-bearing: more specific markers precede the generic supersets they
// contain (e.g. " somehow becomes bored with life" before " becomes bored
// with life"; " eats 2 scoops of " before " eats "). Do not reorder a
// within-Kind run without re-checking the golden corpus.
var ObituaryPatterns = []ObituaryPattern{
	// --- Suicides (self-kill; victim is the whole prefix). --------------
	// The /kill console command (dtSUICIDE, −2 frags).
	{Marker: " suicides", Weapon: "suicide", Suicide: true, Kind: ObituarySuicide},

	// Rocket Launcher self-damage.
	{Marker: " discovers blast radius", Weapon: "rl", Suicide: true, Kind: ObituarySuicide},
	// KTX catch-all self-kill of unknown cause (client.c:5330). Must precede
	// the shorter " becomes bored with life" substring it contains; cause
	// unknown, so it stays "suicide".
	{Marker: " somehow becomes bored with life", Weapon: "suicide", Suicide: true, Kind: ObituarySuicide},
	{Marker: " becomes bored with life", Weapon: "rl", Suicide: true, Kind: ObituarySuicide},

	// Grenade Launcher self-damage.
	{Marker: " tries to put the pin back in", Weapon: "gl", Suicide: true, Kind: ObituarySuicide},

	// Lightning Gun discharge self-damage.
	{Marker: " electrocutes himself", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},
	{Marker: " electrocutes herself", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},
	{Marker: " heats up the water", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},
	{Marker: " discharges into the water", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},
	{Marker: " discharges into the slime", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},
	{Marker: " discharges into the lava", Weapon: "lg", Suicide: true, Kind: ObituarySuicide},

	// Water drowning.
	{Marker: " sleeps with the fishes", Weapon: "water", Suicide: true, Kind: ObituarySuicide},
	{Marker: " sucks it down", Weapon: "water", Suicide: true, Kind: ObituarySuicide},

	// Slime.
	{Marker: " gulped a load of slime", Weapon: "slime", Suicide: true, Kind: ObituarySuicide},
	{Marker: " can't exist on slime alone", Weapon: "slime", Suicide: true, Kind: ObituarySuicide},

	// Lava.
	{Marker: " burst into flames", Weapon: "lava", Suicide: true, Kind: ObituarySuicide},
	{Marker: " turned into hot slag", Weapon: "lava", Suicide: true, Kind: ObituarySuicide},
	{Marker: " visits the Volcano God", Weapon: "lava", Suicide: true, Kind: ObituarySuicide},

	// Fall.
	{Marker: " cratered", Weapon: "fall", Suicide: true, Kind: ObituarySuicide},
	{Marker: " fell to his death", Weapon: "fall", Suicide: true, Kind: ObituarySuicide},
	{Marker: " fell to her death", Weapon: "fall", Suicide: true, Kind: ObituarySuicide},

	// Environmental (world). KTX prints these only from the
	// `attacker->ct != ctPlayer` branch, so the killer is the map, and the
	// print does not carry a finer cause than that:
	//   - " was spiked" is dtNG *or* dtSNG (client.c:5711) with a non-player
	//     attacker. The two deathtypes share one string, so picking `ng` or
	//     `sng` here would be a guess; `world` is what the line actually
	//     establishes. (mvd/types.go still spells the deathtype ng/sng on the
	//     damage side, where the wire carries the deathtype itself.)
	//   - dtLASER and dtFIREBALL have no token of their own in either
	//     vocabulary.
	{Marker: " was spiked", Weapon: "world", Suicide: true, Kind: ObituarySuicide},     // nails, non-player attacker
	{Marker: " was zapped", Weapon: "world", Suicide: true, Kind: ObituarySuicide},     // laser
	{Marker: " ate a lavaball", Weapon: "world", Suicide: true, Kind: ObituarySuicide}, // fireball
	{Marker: " was squished", Weapon: "squish", Suicide: true, Kind: ObituarySuicide},  // squish
	{Marker: " tried to leave", Weapon: "world", Suicide: true, Kind: ObituarySuicide}, // changelevel
	// dtEXPLO_BOX. The damage log spells this deathtype "explobox"
	// (mvd/types.go DeathTypeToWeapon), so the obituary does too rather than
	// flattening it into the generic "world".
	{Marker: " blew up", Weapon: "explobox", Suicide: true, Kind: ObituarySuicide},

	// Legacy.
	{Marker: " blew himself up", Weapon: "rl", Suicide: true, Kind: ObituarySuicide},
	{Marker: " blew herself up", Weapon: "rl", Suicide: true, Kind: ObituarySuicide},
	{Marker: " finds a way out", Weapon: "suicide", Suicide: true, Kind: ObituarySuicide},

	// KTX k_spawnicide variants (client.c:5240-5261, dtTELE4). Only emitted
	// when k_spawnicide is enabled; counted as a suicide (KTX
	// logfrag(targ, targ)).
	{Marker: " couldn't resist the shiny spawn point", Weapon: "tele", Suicide: true, Kind: ObituarySuicide},
	{Marker: " got too close to the baby factory", Weapon: "tele", Suicide: true, Kind: ObituarySuicide},
	{Marker: " was fragged by poor life choices", Weapon: "tele", Suicide: true, Kind: ObituarySuicide},

	// dtTELE3, the double-pentagram telefrag (client.c:5228-5237): BOTH
	// players hold 666, so the would-be telefragger's victim survives and the
	// telefragger dies. KTX prints the ordinary " was telefragged by " verb
	// with the suffix "'s Satan's power" and then books it as the victim's
	// own suicide — `targ->s.v.frags -= 1; logfrag(targ, targ)`, the attacker
	// gets nothing. Without this row the generic kill marker below swallows
	// the line and credits the surviving player a frag the server never gave
	// them. Suffix is a REQUIRED extra substring here, not a victim bound
	// (the victim is still the whole prefix), and the suicide run is scanned
	// before the kill run in both consumers, so this wins.
	{Marker: " was telefragged by ", Suffix: "'s Satan's power", Weapon: "tele", Suicide: true, Kind: ObituarySuicide},

	// dtTRIGGER_HURT and the world branch's unenumerated catch-all
	// (client.c:5775-5782) share ONE string, so the print cannot say which,
	// and "world" — a world-dealt death of unstated cause — is all it
	// establishes. (`trigger` stays a damage-log-only token: the wire carries
	// the deathtype there, this line does not.) Marker last in the suicide
	// run and LineEnd-anchored: " died" is short enough to sit inside a
	// player name, and both engine prints put it at the end.
	{Marker: " died", LineEnd: true, Weapon: "world", Suicide: true, Kind: ObituarySuicide},

	// --- Kills (victim is the prefix; killer follows the marker). -------
	// Telefrag (dtTELE1).
	{Marker: " was telefragged by ", Weapon: "tele", Kind: ObituaryKill},

	// Lightning Gun (dtLG_BEAM, dtLG_DIS).
	{Marker: " accepts ", Weapon: "lg", Kind: ObituaryKill},                      // "accepts X's shaft"
	{Marker: " gets a natural disaster from ", Weapon: "lg", Kind: ObituaryKill}, // quad gib
	{Marker: " drains ", Weapon: "lg", Kind: ObituaryKill},                       // "drains X's batteries" (discharge kill)

	// Rocket Launcher (dtRL).
	{Marker: " rides ", Weapon: "rl", Kind: ObituaryKill},             // "rides X's rocket"
	{Marker: " was brutalized by ", Weapon: "rl", Kind: ObituaryKill}, // quad gib variant
	{Marker: " was smeared by ", Weapon: "rl", Kind: ObituaryKill},    // quad gib variant

	// CRMod SSG ("X eats 2 scoops of Y's lead shot") must precede the generic
	// GL " eats " below: strings.Index would otherwise hit the shorter
	// " eats " first and mislabel the kill "gl".
	{Marker: " eats 2 scoops of ", Weapon: "ssg", Kind: ObituaryKill}, // suffix "'s lead shot"

	// Grenade Launcher (dtGL).
	{Marker: " eats ", Weapon: "gl", Kind: ObituaryKill}, // "eats X's pineapple"

	// Nailgun (dtNG) — before SNG.
	{Marker: " was body pierced by ", Weapon: "ng", Kind: ObituaryKill},
	{Marker: " was nailed by ", Weapon: "ng", Kind: ObituaryKill},

	// Super Nailgun (dtSNG).
	{Marker: " was straw-cuttered by ", Weapon: "sng", Kind: ObituaryKill}, // quad gib
	{Marker: " was perforated by ", Weapon: "sng", Kind: ObituaryKill},
	{Marker: " was punctured by ", Weapon: "sng", Kind: ObituaryKill},
	{Marker: " was ventilated by ", Weapon: "sng", Kind: ObituaryKill},

	// Shotgun (dtSG).
	{Marker: " chewed on ", Weapon: "sg", Kind: ObituaryKill},            // "chewed on X's boomstick"
	{Marker: " was lead poisoned by ", Weapon: "sg", Kind: ObituaryKill}, // gib
	{Marker: " was instagibbed by ", Weapon: "sg", Kind: ObituaryKill},   // instagib mode

	// Axe (dtAXE).
	{Marker: " was ax-murdered by ", Weapon: "axe", Kind: ObituaryKill},
	{Marker: " was axed to pieces by ", Weapon: "axe", Kind: ObituaryKill}, // instagib

	// Grappling Hook (dtHOOK).
	{Marker: " was hooked by ", Weapon: "hook", Kind: ObituaryKill},

	// Rail Gun (sv_mod_frags.h, DMM8/TF).
	{Marker: " was railed by ", Weapon: "rail", Kind: ObituaryKill},

	// Stomp kills (dtSTOMP).
	{Marker: " softens ", Weapon: "stomp", Kind: ObituaryKill}, // "X softens Y's fall"
	{Marker: " tried to catch ", Weapon: "stomp", Kind: ObituaryKill},
	{Marker: " was literally stomped into particles by ", Weapon: "stomp", Kind: ObituaryKill}, // instagib
	{Marker: " was jumped by ", Weapon: "stomp", Kind: ObituaryKill},
	{Marker: " was crushed by ", Weapon: "stomp", Kind: ObituaryKill},

	// CRMod obituary variants. " was blown to chunks by " is shared rl/gl and
	// is disambiguated by suffix in the analytics kill matcher.
	{Marker: " was disembowled by ", Weapon: "sg", Kind: ObituaryKill},             // [sic] CRMod misspelling; suffix "'s shotgun"
	{Marker: " is shish-kebabed by ", Weapon: "rl", Kind: ObituaryKill},            // suffix "'s rocket"
	{Marker: " was blown to chunks by ", Weapon: "rl", Kind: ObituaryKill},         // suffix "'s rocket" — fixed up to gl when suffix is "'s grenade"
	{Marker: " gets intimate with ", Weapon: "gl", Kind: ObituaryKill},             // suffix "'s grenade"
	{Marker: " gets a warm fuzzy feeling from ", Weapon: "lg", Kind: ObituaryKill}, // no weapon suffix; rest is just the killer name

	// Generic.
	{Marker: " was killed by ", Weapon: "unknown", Kind: ObituaryKill},
	{Marker: " was fragged by ", Weapon: "unknown", Kind: ObituaryKill},

	// --- Gibbed-by (victim prefix; weapon depends on the suffix). -------
	{Marker: " was gibbed by ", Weapon: "rl", Kind: ObituaryGibbed}, // "'s rocket" → rl, "'s grenade" → gl

	// --- Phrasing teamkills naming only the victim. --------------------
	// Must precede the non-team " was telefragged by " / " was crushed by "
	// / " was jumped by " kill markers so those don't steal the line.
	//
	// These carry the REAL weapon, not the "teamkill" placeholder. KTX's
	// team branch of ClientObituary (ktx/src/client.c:5343-5410) tests three
	// deathtypes by name before it reaches its random phrasing pick, and each
	// gets its own message: dtTELE1 → "was telefragged by his teammate"
	// (:5355), dtSQUISH → "<killer> squished a teammate" (:5362, the one
	// cause-carrying form that names the KILLER instead — see below), dtSTOMP
	// → "was jumped/crushed by his teammate" (:5368). For all three the death
	// CAUSE is on the wire; only the random four (:5386-5408) are genuinely
	// cause-less. Consumers that treat tele/stomp as positional instant kills
	// rather than weapon damage (mvd-analytics/damagerecon, analyzer/damage.go)
	// need the distinction: booking a team telefrag as ordinary team damage
	// charges the telefragger the victim's whole corpse drop instead of their
	// capacity.
	{Marker: " was telefragged by his teammate", Weapon: "tele", TeamKill: true, Kind: ObituaryTeamkillVictim},
	{Marker: " was telefragged by her teammate", Weapon: "tele", TeamKill: true, Kind: ObituaryTeamkillVictim},
	{Marker: " was crushed by his teammate", Weapon: "stomp", TeamKill: true, Kind: ObituaryTeamkillVictim},
	{Marker: " was crushed by her teammate", Weapon: "stomp", TeamKill: true, Kind: ObituaryTeamkillVictim},
	{Marker: " was jumped by his teammate", Weapon: "stomp", TeamKill: true, Kind: ObituaryTeamkillVictim},
	{Marker: " was jumped by her teammate", Weapon: "stomp", TeamKill: true, Kind: ObituaryTeamkillVictim},

	// --- Phrasing teamkills naming only the killer. --------------------
	// " squished a teammate" is the third of the team branch's deathtype-
	// tested messages (dtSQUISH, ktx/src/client.c:5362-5367) and is the only
	// cause-carrying one that names the killer rather than the victim, so it
	// keeps the real weapon "squish" — the same token the non-team form
	// "<killer> squishes <victim>" (:5447) carries. A mover crush is ordinary
	// damage on the wire (KTX logs it through T_Damage with the door's
	// activator as the attacker, ktx/src/doors.c:68), NOT a positional
	// instant kill, so this weapon is cause information only and changes no
	// routing.
	//
	// The other four are the random pick at :5386-5408 — no deathtype in
	// them, so "teamkill" is all a consumer can be told. " gets a frag for
	// the other team" is a self-inflicted team frag; the analytics frag
	// mapper tags it suicide until the real victim is recovered
	// (recoverTeamkills).
	{Marker: " squished a teammate", Weapon: "squish", TeamKill: true, Kind: ObituaryTeamkillKiller},
	{Marker: " gets a frag for the other team", Weapon: "teamkill", Suicide: true, TeamKill: true, Kind: ObituaryTeamkillKiller},
	{Marker: " mows down a teammate", Weapon: "teamkill", TeamKill: true, Kind: ObituaryTeamkillKiller},
	{Marker: " checks his glasses", Weapon: "teamkill", TeamKill: true, Kind: ObituaryTeamkillKiller},
	{Marker: " checks her glasses", Weapon: "teamkill", TeamKill: true, Kind: ObituaryTeamkillKiller},
	{Marker: " loses another friend", Weapon: "teamkill", TeamKill: true, Kind: ObituaryTeamkillKiller},

	// --- Killer-first kills ("killer <verb> victim"). ------------------
	{Marker: " rips ", Suffix: " a new one", Weapon: "rl", Kind: ObituaryKillerFirst}, // "X rips Y a new one" (quad RL)
	{Marker: " stomps ", Weapon: "stomp", Kind: ObituaryKillerFirst},                  // "X stomps Y"
	{Marker: " squishes ", Weapon: "squish", Kind: ObituaryKillerFirst},               // "X squishes Y"

	// --- Infix (victim bracketed by prefix + suffix). ------------------
	// KTX pentagram-deflection self-telefrag (dtTELE2, client.c:5219): the
	// would-be telefragger dies, booked as a suicide.
	{Marker: "Satan's power deflects ", Suffix: "'s telefrag", Weapon: "tele", Suicide: true, Kind: ObituaryInfix},
}

// obituaryVictimScan is the victim-prefix subset in the order
// FindObituaryVictim must try it: phrasing teamkills first (so "<victim>
// was telefragged by his teammate" wins over the shorter " was telefragged
// by " kill marker), then suicides, then kills, then the gibbed-by form.
// Killer-first and killer-named teamkill forms are excluded — their victim
// is not the prefix, so the parser (which needs the victim) never derives a
// death from them; analytics handles those directly.
var obituaryVictimScan = func() []ObituaryPattern {
	var out []ObituaryPattern
	for _, k := range []ObituaryKind{ObituaryTeamkillVictim, ObituarySuicide, ObituaryKill, ObituaryGibbed} {
		for i := range ObituaryPatterns {
			if ObituaryPatterns[i].Kind == k {
				out = append(out, ObituaryPatterns[i])
			}
		}
	}
	return out
}()

// FindObituaryVictim scans `msg` against the canonical obituary patterns
// and, on the first match, returns the victim's display name (the text
// before the matched marker, trimmed) and a pointer to the matched pattern.
// Returns ("", nil) when no pattern fits.
//
// Callers that need only "did somebody die" can ignore the pattern pointer.
// Victim-prefix patterns are tried first (the bulk of KTX's fragfile
// lines), then infix patterns (Satan's-deflection obits where the victim is
// bracketed by a fixed prefix and suffix).
func FindObituaryVictim(msg string) (string, *ObituaryPattern) {
	for i := range obituaryVictimScan {
		p := &obituaryVictimScan[i]
		idx := strings.Index(msg, p.Marker)
		if idx <= 0 {
			continue
		}
		// A suicide row may carry Suffix as a REQUIRED discriminator when its
		// marker is shared with a kill row (dtTELE3).
		if p.Kind == ObituarySuicide && p.Suffix != "" &&
			!strings.Contains(msg[idx+len(p.Marker):], p.Suffix) {
			continue
		}
		if p.LineEnd && strings.TrimRight(msg[idx+len(p.Marker):], " \t\r\n") != "" {
			continue
		}
		victim := strings.TrimSpace(msg[:idx])
		if victim == "" {
			continue
		}
		return victim, p
	}
	// Killer-first forms ("X stomps Y"): the victim FOLLOWS the marker,
	// bounded by Suffix when set (" rips Y a new one") else the line end.
	// Scanned after the victim-prefix kinds so an overlapping victim-prefix
	// marker (" was literally stomped into particles by ") wins first.
	// ObituaryTeamkillKiller is deliberately excluded: its victim is the
	// generic "teammate", not a name. The one classifier-known form with no
	// victim row anywhere is the bespoke " ate N loads of X's buckshot"
	// splash sub-parse (analytics matchAte) — those victims stay invisible
	// to this scan, as they always were.
	for i := range ObituaryPatterns {
		p := &ObituaryPatterns[i]
		if p.Kind != ObituaryKillerFirst {
			continue
		}
		idx := strings.Index(msg, p.Marker)
		if idx <= 0 {
			continue
		}
		rest := msg[idx+len(p.Marker):]
		if p.Suffix != "" {
			end := strings.Index(rest, p.Suffix)
			if end <= 0 {
				continue
			}
			rest = rest[:end]
		}
		victim := strings.TrimSpace(rest)
		if victim == "" {
			continue
		}
		return victim, p
	}
	for i := range ObituaryPatterns {
		p := &ObituaryPatterns[i]
		if p.Kind != ObituaryInfix {
			continue
		}
		if !strings.HasPrefix(msg, p.Marker) {
			continue
		}
		rest := msg[len(p.Marker):]
		suffixIdx := strings.Index(rest, p.Suffix)
		if suffixIdx <= 0 {
			continue
		}
		victim := strings.TrimSpace(rest[:suffixIdx])
		if victim == "" {
			continue
		}
		return victim, p
	}
	return "", nil
}
