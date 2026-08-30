package analyzer

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// One resolver for the game mode, feeding result.GameMode.
//
// Before this file the pipeline answered "what mode is this" from five
// unrelated vocabularies with no translation between them, and answered "is
// this a team game" four separate times with four different hand-written
// tables (player_stats.isNonTeamMode, damage.tpModeApplies and its verbatim
// copy in cmd/qw-demoinfo-eval, damagerecon/inputs.go's duel prior,
// app.js's `!1on1 && !ffa`). The tables disagreed, and on an FFA demo the
// consequence was visible in the output: three players wearing the same
// decorative userinfo `team` tag had 34 of 211 kills on ffa_1[dm2] flagged
// as team kills, were dropped from each other's aim enemy sets, and were
// summed into pseudo-team scoreboard rows.
//
// Precedence, per field, strongest first:
//
//	Canonical  demoinfo `mode` → countdown Mode row → serverinfo `mode`
//	           umode → //finalscores mode → roster shape
//	TeamBased  an EXPLICIT individual canonical → demoinfo `tp` →
//	           serverinfo `teamplay` → countdown Teamplay → the canonical
//	           mode alone → roster shape
//	Submodes   serverinfo (composite `mode` key + hand-exported k_* cvars) and the
//	           countdown table, unioned
//	Rounds     the canonical mode alone
//
// The demoinfo block wins because it is the same server one code path over
// (ktx/src/stats.c:309 GetMode, stats_json.c:478-502 for `tp`/`dm`), written
// at match end from the values that were actually in force.
//
// The countdown outranks the serverinfo `mode` key because that key does not
// name the mode the match is being played in: SetMode4ServerInfo writes the
// last usermode COMMAND that ran, `um_name_byidx(current_umode - 1)`
// (ktx/src/world.c:1482); `current_umode` is assigned only by UserMode()
// (ktx/src/commands.c:4848 — a `/4on4`, an election, or a server-invoked
// mode change), so a plain `teamplay` or `k_mode` change afterwards leaves
// it stating the previous game. The countdown centerprint is printed by
// PrintCountdown (match.c:1454, from TimerStartThink :2057) from the
// settings actually in force when the match started. Archive demo b95c35735c4d… is the case: serverinfo `mode=1on1`,
// countdown Mode row `Team`, demoinfo `mode=team` with `tp 2`.
//
// TeamBased's "the mode gets the first word" rule applies only to a canonical
// an explicit mode SOURCE named (demoinfo / countdown / serverinfo /
// //finalscores). A canonical inferred from the ROSTER shape may not veto a
// teamplay ruleset: a CTF server running a 1v1 (archive 2a2ed2e9ca…,
// `*gamedir=ctf`, `teamplay=419`, Chipie [red] vs Pain [blue]) is two
// participants and still a teamplay game, and the roster is the weakest
// source in the table (RESULT_SCHEMA.md's source vocabulary). The match still
// gets the individual LAYOUT there — that is Roster.Duel(), a statement about
// how many sides there are, not about whether the cvar was set.

// canonicalIsIndividual reports whether the mode is one where every player
// fights alone, so a userinfo `team` tag is decoration. This is the positive
// half of the verdict: hoonymode and the NonN blitz variants are omitted
// deliberately (blitz is 2v2/4v4), as is "unknown".
func canonicalIsIndividual(canonical string) bool {
	switch canonical {
	case result.GameModeDuel, result.GameModeFFA, result.GameModeRace,
		result.GameModeRA:
		return true
	}
	return false
}

// canonicalIsRounds reports whether the mode scores ROUNDS rather than
// frags. KTX writes the round count into `//finalscores` on exactly these
// (ktx/src/commands.c:6867-6886).
func canonicalIsRounds(canonical string) bool {
	switch canonical {
	case result.GameModeCA, result.GameModeWipeout, result.GameModeRA,
		result.GameModeHoony:
		return true
	}
	return false
}

// canonicalFromKTXMode maps KTX's demoinfo `mode` string (GetMode,
// ktx/src/stats.c:309-354) and the `//finalscores` lastscores mode name
// (lastscores2str, commands.c:6755-6790) onto a canonical shape. Both
// vocabularies are handled here because they are the same enum spelled two
// ways ("clan-arena" vs "Clan Arena"). The table is exactly the union of
// the two functions' return literals — a drift test reads them out of the
// vendored source — after a whole-archive sweep found seven aliases that
// neither writes ("1on1", "ca", "wo", "hoony", "blitz", "rocket arena",
// "coop") on 0 of 50 964 demos.
//
// GetMode tests k_instagib and k_midair FIRST, so an instagib 4on4 reports
// "instagib" and says nothing about teams at all. Those two return "" —
// "this string does not name a shape" — and the caller falls through to the
// next source. They are still recorded as submodes. "unknown", both
// functions' default, falls through the same way.
func canonicalFromKTXMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "duel":
		return result.GameModeDuel
	case "team":
		return result.GameModeTeam
	case "ffa":
		return result.GameModeFFA
	case "ctf":
		return result.GameModeCTF
	case "clan arena", "clan-arena":
		return result.GameModeCA
	case "wipeout":
		return result.GameModeWipeout
	case "race":
		return result.GameModeRace
	case "hoonymode":
		// isHoonyModeAny() in GetMode — the duel AND the blitz team
		// variants both report this, so it may be a team game.
		return result.GameModeHoony
	case "ra", "rocket-arena":
		return result.GameModeRA
	}
	return ""
}

// canonicalFromUmode maps the base user-mode name of the composite
// serverinfo `mode` key onto a canonical shape. The names are exactly
// um_list's seventeen (ktx/src/commands.c:4535-4553; a drift test reads
// them out of the vendored source): everything of the NonN family —
// including the three-way NonNonN modes and the open-ended XonX — is a team
// game, and "tot" (Tribe of Tjernobyl) sets `k_mode 3` (gtFFA,
// commands.c:4511-4533), so it is an FFA. Race is not a usermode: KTX
// writes it only as the `-race` SUFFIX (world.c:1487-1490, 84 archive
// demos, all `ffa-race`), so a base token "race" is unconstructible and the
// table no longer carries one.
//
// Three base tokens in the archive are not KTX's and deliberately fall
// through to the next source rather than being guessed at: `1` (65 E0
// demos, the CTF mod that also writes `M:SS left`), `extinction` (87 E5
// demos, a KTX fork reporting ktxver 1.47-dev) and `smashpacktdm` (5 E5
// demos, "Quake Smash Mod"). A name is not a shape verdict.
func canonicalFromUmode(umode string) string {
	switch strings.ToLower(strings.TrimSpace(umode)) {
	case "1on1":
		return result.GameModeDuel
	case "2on2", "3on3", "4on4", "10on10", "2on2on2", "3on3on3", "4on4on4", "xonx":
		return result.GameModeTeam
	case "ffa", "tot":
		return result.GameModeFFA
	case "ctf":
		return result.GameModeCTF
	case "hoonymode", "blitz2v2", "blitz4v4":
		// Blitz is HoonyMode's team variant. It is the same shape KTX
		// reports for all three — GetMode says "hoonymode" for
		// isHoonyModeAny() and lastscore_add books them as lsHM (round
		// scored) — so it is hoony here too; TeamBased comes from the
		// teamplay sources, which is what separates blitz from the duel.
		return result.GameModeHoony
	case "wipeout":
		return result.GameModeWipeout
	case "ca":
		return result.GameModeCA
	}
	return ""
}

// canonicalFromCountdown maps the countdown centerprint's Mode row onto a
// canonical shape. The row is written by PrintCountdown
// (ktx/src/match.c:1511-1571) from a fixed set of redtext literals —
// "D u e l", "T e a m", "F F A", "C T F", "R A C E", "C O O P", "CA", "RA",
// "Wipeout", "Hoony", "BlitzTDM", plus "LGC" and "BLOODFST", which name
// rulesets rather than shapes, and "Unknown" — NOT from um_list's
// displayname column, which an earlier version of this table was
// transcribed from (fourteen entries, 0 hits on 50 964 demos). The
// spaced spellings arrive here already flattened ("Duel", "Team", "FFA",
// "COOP"); the table lower-cases and strips spaces once more so either
// form matches. A drift test reads the literals out of the vendored source.
//
// Precedence inside PrintCountdown is a chain of if/else: bloodfest beats
// coop beats RA beats CA/Wipeout beats Hoony beats LGC beats BlitzTDM beats
// race beats duel/team/ffa/ctf. So a Hoony row means the DUEL variant
// (isHoonyModeDuel), a BlitzTDM row the team one — both are hoony here,
// as they are in GetMode and the lastscores round accounting, with the
// teamplay sources telling them apart — and an LGC row hides the
// shape entirely (450 archive demos; 240 of them carry a serverinfo umode,
// which still says "1on1", and the rest fall further down the precedence). "Extinction", from a KTX fork, is the
// one row in the archive this table does not name; it falls through too.
//
// "CA" is deliberately NOT mapped. Of the 23 archive demos whose countdown
// says CA, 17 are wipeout matches — serverinfo `wipeout-wo-df`,
// `//finalscores` mode "Wipeout" on every one that carries it — and all
// 17 come from ONE server (hostname QHLAN:28550, ktxver 1.47-dev), while
// seven other servers on the same 1.47-dev print "Wipeout" for the
// identical mode and table. The Wipeout branch above has been in KTX since
// 1194647 (2022-03-17), so this is not a version that can be bounded: one
// current build prints the isCA() FAMILY name for both k_clan_arena
// values. The serverinfo umode — `ca` or `wipeout`, present on all 23 —
// is the source that tells them apart, and it is next in precedence. A
// "Wipeout" row is unambiguous and mapped.
func canonicalFromCountdown(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	m = strings.ReplaceAll(m, " ", "")
	switch m {
	case "duel":
		return result.GameModeDuel
	case "team":
		return result.GameModeTeam
	case "ffa":
		return result.GameModeFFA
	case "ctf":
		return result.GameModeCTF
	case "wipeout":
		return result.GameModeWipeout
	case "race":
		return result.GameModeRace
	case "hoony", "blitztdm":
		return result.GameModeHoony
	case "ra":
		return result.GameModeRA
	case "coop":
		return result.GameModeCoop
	}
	return ""
}

// submodeSet collects the ruleset modifiers, unioned over every source that
// names one: the composite serverinfo `mode` key's tokens, a k_* cvar
// exported into the serverinfo by hand, and the countdown table's ruleset
// rows. The tokens are KTX's own spellings from SetMode4ServerInfo
// (ktx/src/world.c:1487-1541) — `-race -midair -instagib -lgc -ca -wo -ra
// -gm -df -yw -bf` — and the other two sources map onto the same names so
// a consumer has one vocabulary to test.
//
// The k_* list is not something KTX publishes: it registers every k_ cvar
// with flags 0 (`PF_registercvar`, mvdsv/src/pr_cmds.c:2613 — no
// CVAR_SERVERINFO) and has no `serverinfo k_*` writer anywhere, and the
// whole-archive sweep found none of these six keys on any demo. They stay
// because an admin can `serverinfo k_x 1` by hand and some do — the archive
// carries `k_fallbunny` on 31 demos and three other k_ keys once each — so
// the list is a hedge over a real practice, costing one map lookup each.
func submodeSet(si map[string]string, ms *MatchSettings) (subs []string, src string) {
	set := map[string]bool{}
	note := func(tok, from string) {
		if tok == "" || set[tok] {
			return
		}
		set[tok] = true
		if src == "" {
			src = from
		}
	}

	sm := result.ParseServerinfoMode(si["mode"])
	for _, s := range sm.Submodes {
		note(s, result.GameModeSrcServerInfo)
	}
	for _, m := range [...]struct{ cvar, tok string }{
		{"k_midair", "midair"},
		{"k_instagib", "instagib"},
		{"k_dmgfrags", "df"},
		{"k_lgcmode", "lgc"},
		{"k_yawnmode", "yw"},
		{"k_bloodfest", "bf"},
	} {
		if v := si[m.cvar]; v != "" && v != "0" {
			note(m.tok, result.GameModeSrcServerInfo)
		}
	}
	if ms != nil {
		if ms.Midair {
			note("midair", result.GameModeSrcCountdown)
		}
		if ms.Instagib {
			note("instagib", result.GameModeSrcCountdown)
		}
		if ms.Dmgfrags {
			note("df", result.GameModeSrcCountdown)
		}
		if ms.Yawnmode {
			note("yw", result.GameModeSrcCountdown)
		}
		// Two PrintCountdown Mode literals name a ruleset, not a shape
		// (match.c:1511-1513, :1538-1540): they land here, and the shape
		// comes from the next source down.
		if strings.EqualFold(ms.Mode, "lgc") {
			note("lgc", result.GameModeSrcCountdown)
		}
		if strings.EqualFold(ms.Mode, "bloodfst") {
			note("bf", result.GameModeSrcCountdown)
		}
	}
	if len(set) == 0 {
		return nil, ""
	}
	subs = make([]string, 0, len(set))
	for s := range set {
		subs = append(subs, s)
	}
	sort.Strings(subs)
	return subs, src
}

// resolveGameMode produces the one mode verdict the pipeline reads.
//
// Every argument is optional: a demo may carry no demoinfo block, no
// countdown, no `//finalscores` and no serverinfo at all, and the resolver
// still returns a descriptor (canonical "unknown", TeamBased from the roster
// shape). roster may be nil — a hand-built registry or a unit test.
func resolveGameMode(di *DemoInfoResult, fs *FinalScores, ms *MatchSettings, si map[string]string, roster *Roster, teamCounts map[string]int) result.GameMode {
	gm := result.GameMode{Canonical: result.GameModeUnknown}

	// ── Canonical ──
	// KTX's demoinfo block is authoritative where it exists, but only when
	// it actually parsed: a RawJSON-only DemoInfoResult carries no fields.
	ktx := di != nil && (di.Version != 0 || len(di.Players) > 0)
	switch {
	case ktx && canonicalFromKTXMode(di.Mode) != "":
		gm.Canonical, gm.Sources.Canonical = canonicalFromKTXMode(di.Mode), result.GameModeSrcKTX
	case ms != nil && canonicalFromCountdown(ms.Mode) != "":
		// Ahead of the serverinfo `mode` key: that key names the last
		// usermode COMMAND (world.c:1482 over commands.c:4848), the
		// countdown names the settings the match actually started under.
		gm.Canonical, gm.Sources.Canonical = canonicalFromCountdown(ms.Mode), result.GameModeSrcCountdown
	case canonicalFromUmode(result.ParseServerinfoMode(si["mode"]).Umode) != "":
		gm.Canonical = canonicalFromUmode(result.ParseServerinfoMode(si["mode"]).Umode)
		gm.Sources.Canonical = result.GameModeSrcServerInfo
	case fs != nil && canonicalFromKTXMode(fs.Mode) != "":
		gm.Canonical, gm.Sources.Canonical = canonicalFromKTXMode(fs.Mode), result.GameModeSrcFinalScores
	case roster.Duel():
		// The roster's own verdict: exactly two demoinfo participants. It is
		// the shape test the whole project already trusts for duel layout
		// (roster.go:49-63), so it is the last word rather than a guess.
		gm.Canonical, gm.Sources.Canonical = result.GameModeDuel, result.GameModeSrcRoster
	}

	// ── Submodes ──
	gm.Submodes, gm.Sources.Submodes = submodeSet(si, ms)

	// ── Rounds ──
	if canonicalIsRounds(gm.Canonical) {
		gm.Rounds, gm.Sources.Rounds = true, result.GameModeSrcMode
	}

	// ── TeamBased ──
	//
	// An EXPLICITLY named mode gets the first word, because KTX's own gate
	// does: tp_num() returns `(isTeam() || isCTF() || coop) ? teamplay : 0`
	// (ktx/src/g_utils.c:1586-1588), so on a mode where every player fights
	// alone the cvar cannot apply however it is set. That is not
	// hypothetical — an FFA server running with a `teamplay 2` left over
	// from the previous team game is in the corpus.
	//
	// A canonical the ROSTER inferred does not get that word. "Two
	// participants" is a statement about how many sides played, not about
	// whether the teamplay ruleset was in force, and letting it veto a
	// non-zero `teamplay` published a CTF server's 1v1 (archive
	// 2a2ed2e9ca…, `*gamedir=ctf`, `teamplay=419`) as `teamBased: false,
	// sources.teamBased: "mode"` — a shape inference wearing the
	// provenance of a mode source. It still decides the LAYOUT (one side
	// per player) via Roster.Duel(); the two questions are separate.
	//
	// After that KTX's demoinfo `tp` is decisive, in both directions:
	// FixRules forces teamplay to 0 when the gametype is not team/ctf/coop
	// (ktx/src/world.c:1674-1681) and to 2 when it IS and the cvar holds
	// anything else (:1683-1691), so `teamplay > 0` and "this is a team
	// game" are equivalent on any KTX server. KTX writes the `tp` key only
	// when teamplay is non-zero (stats_json.c:498-502), so its ABSENCE from
	// a demoinfo block that named a mode is itself the verdict — but only
	// from such a block, since a build that wrote neither key says nothing.
	// modeNamed is "an explicit source named this canonical" — everything
	// but the roster shape (and "unknown", which names nothing).
	modeNamed := gm.Sources.Canonical != "" && gm.Sources.Canonical != result.GameModeSrcRoster
	switch {
	case modeNamed && canonicalIsIndividual(gm.Canonical):
		gm.TeamBased, gm.Sources.TeamBased = false, result.GameModeSrcMode
	case ktx && di.Teamplay > 0:
		gm.TeamBased, gm.Sources.TeamBased = true, result.GameModeSrcKTX
	case gm.Sources.Canonical == result.GameModeSrcKTX:
		// `tp` absent from a block that DID name a mode: FixRules'
		// biconditional makes that "teamplay was 0". The mode condition is
		// load-bearing — a block with neither key is one this build wrote
		// no mode into, and reading its silence as a verdict would strip
		// the sides off a real team game. It is this block's OWN mode that
		// licenses the reading: a canonical some other source named says
		// nothing about what this build writes.
		gm.TeamBased, gm.Sources.TeamBased = false, result.GameModeSrcKTX
	case si["teamplay"] != "":
		gm.TeamBased, gm.Sources.TeamBased = si["teamplay"] != "0", result.GameModeSrcServerInfo
	case ms != nil && ms.Teamplay > 0:
		gm.TeamBased, gm.Sources.TeamBased = true, result.GameModeSrcCountdown
	case result.CanonicalIsTeamShaped(gm.Canonical):
		gm.TeamBased, gm.Sources.TeamBased = true, result.GameModeSrcMode
	case canonicalIsIndividual(gm.Canonical):
		// A roster-inferred individual canonical, with no cvar and no
		// countdown to read: two participants are two sides, so nothing
		// here is a team. Provenance stays "roster" — this is the shape
		// talking, and a consumer must be able to tell it from a mode
		// source. It outranks the tag census below, which would call two
		// duellists who both picked `red` a team.
		gm.TeamBased, gm.Sources.TeamBased = false, result.GameModeSrcRoster
	default:
		// Nothing named a mode and nothing named a teamplay cvar. The
		// scoreboard shape is all that is left: an FFA lists every player
		// under their own colour tag, a team game puts more than one player
		// under at least one tag. This is the one source the individual
		// LAYOUT refuses to act on — see individualLayoutFromMode.
		for _, n := range teamCounts {
			if n > 1 {
				gm.TeamBased = true
				break
			}
		}
		gm.Sources.TeamBased = result.GameModeSrcRoster
	}
	return gm
}

// individualLayoutFromMode reports whether a resolved descriptor is strong
// enough to REWRITE the match layout to one team per player.
//
// TeamBased == false is not on its own sufficient: the weakest source
// ("roster") is a shape inference over the very tags the rewrite would
// discard, and a real 4on4 whose demo carries neither a demoinfo block nor a
// teamplay cvar would then have its team tags erased on the strength of a
// scoreboard that has not been read yet. The rewrite therefore needs a
// source that actually saw a mode or a cvar — and the duel case, whose shape
// test the project already treats as authoritative.
func individualLayoutFromMode(gm *result.GameMode) bool {
	if gm == nil || gm.TeamBased {
		return false
	}
	switch gm.Sources.TeamBased {
	case result.GameModeSrcKTX, result.GameModeSrcServerInfo,
		result.GameModeSrcCountdown, result.GameModeSrcMode:
		return true
	}
	return false
}
