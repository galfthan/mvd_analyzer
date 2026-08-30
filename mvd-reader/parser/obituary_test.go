package parser

import (
	"encoding/binary"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// FindObituaryVictim must pick the right victim for representative
// kill / suicide / teamkill obit lines.
func TestFindObituaryVictim(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		victim string
	}{
		{"rl kill", "sailorman rides multibear's rocket\n", "sailorman"},
		{"lg kill", "multibear accepts sailorman's shaft\n", "multibear"},
		{"sg kill", "nlk chewed on clox's boomstick\n", "nlk"},
		{"tk telefrag", "nlk was telefragged by his teammate\n", "nlk"},
		{"suicide rl", "sailorman discovers blast radius\n", "sailorman"},
		{"environmental fall", "ocoini cratered\n", "ocoini"},
		{"environmental lava", "Player visits the Volcano God\n", "Player"},
		{"chat looking like obit", "(sailorman): nice rocket\n", ""},
		{"empty", "", ""},

		// Killer-first forms: the victim FOLLOWS the marker (killer is the
		// prefix — must NOT be returned as the victim).
		{"killer-first stomp", "razor stomps XantoM\n", "XantoM"},
		{"killer-first squish", "razor squishes XantoM\n", "XantoM"},
		{"killer-first quad rl", "razor rips XantoM a new one\n", "XantoM"},

		// Infix-form: Satan's-power-deflect (KTX dtTELE2).
		{"pent deflect", "Satan's power deflects nlk's telefrag\n", "nlk"},

		// KTX dtTELE3 — pent vs pent double-666. Shares the " was
		// telefragged by " verb with the ordinary kill but is a SUICIDE
		// (client.c:5228-5237); the Suffix-discriminated suicide row wins.
		{"pent vs pent", "nlk was telefragged by lakso's Satan's power\n", "nlk"},

		// Explosive box (dtEXPLO_BOX).
		{"explo box", "doberman blew up\n", "doberman"},

		// dtTRIGGER_HURT / world catch-all (client.c:5775-5782). LineEnd-
		// anchored, so a name containing the marker cannot steal a kill line.
		{"died trigger", "doberman died\n", "doberman"},
		{"died inside a name", "x died rides multibear's rocket\n", "x died"},

		// KTX dtTELE4 — k_spawnicide random variants.
		{"spawnicide 1", "doberman couldn't resist the shiny spawn point\n", "doberman"},
		{"spawnicide 2", "doberman got too close to the baby factory\n", "doberman"},
		{"spawnicide 3", "doberman was fragged by poor life choices\n", "doberman"},

		// CRMod variants — confirmed by user against CRMod source.
		{"crmod sg", "Player was disembowled by Other's shotgun\n", "Player"},
		{"crmod ssg", "Player eats 2 scoops of Other's lead shot\n", "Player"},
		{"crmod rl shish", "Player is shish-kebabed by Other's rocket\n", "Player"},
		{"crmod blown chunks rl", "Player was blown to chunks by Other's rocket\n", "Player"},
		{"crmod blown chunks gl", "Player was blown to chunks by Other's grenade\n", "Player"},
		{"crmod gl intimate", "Player gets intimate with Other's grenade\n", "Player"},
		{"crmod lg fuzzy", "Player gets a warm fuzzy feeling from Other\n", "Player"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := FindObituaryVictim(tc.msg)
			if v != tc.victim {
				t.Errorf("FindObituaryVictim(%q) = %q, want %q", tc.msg, v, tc.victim)
			}
		})
	}
}

// "X was telefragged by his teammate" must match the teammate variant
// before the plain "X was telefragged by " kill pattern fires —
// otherwise the killer extraction below would treat "his teammate" as
// the killer's name.
func TestFindObituaryVictim_TeammatePrefersTKPattern(t *testing.T) {
	_, pat := FindObituaryVictim("nlk was telefragged by his teammate\n")
	if pat == nil {
		t.Fatalf("expected a matched pattern for teammate telefrag")
	}
	if !pat.TeamKill {
		t.Errorf("expected TeamKill=true, got pattern %+v", pat)
	}
	if pat.Weapon != "tele" {
		t.Errorf("weapon = %q, want tele: KTX prints this phrasing only for dtTELE1", pat.Weapon)
	}
}

// KTX's dtTELE3 double-pentagram telefrag prints the ordinary " was
// telefragged by " verb but books the death as the VICTIM's own suicide —
// `targ->s.v.frags -= 1; logfrag(targ, targ)`, the surviving player is
// credited nothing (ktx/src/client.c:5228-5237). The plain kill row must not
// swallow the line and hand out a frag the server never gave.
func TestSatanPowerTelefragIsASuicide(t *testing.T) {
	_, pat := FindObituaryVictim("nlk was telefragged by lakso's Satan's power\n")
	if pat == nil {
		t.Fatalf("no pattern matched the dtTELE3 print")
	}
	if !pat.Suicide {
		t.Errorf("Suicide = false; KTX logs this as logfrag(targ, targ)")
	}
	if pat.Weapon != "tele" {
		t.Errorf("weapon = %q, want tele", pat.Weapon)
	}
	// The ordinary dtTELE1 telefrag must still be a kill.
	_, kill := FindObituaryVictim("nlk was telefragged by lakso\n")
	if kill == nil || kill.Suicide {
		t.Errorf("plain telefrag lost its kill classification: %+v", kill)
	}
}

// Every obituary marker must stamp the cause its engine deathtype carries in
// the wire damage log's own vocabulary (mvd.DeathTypeToWeapon /
// EnvironmentalDamageType), so a consumer filtering on a cause gets the
// obituary rows and the damage rows alike.
func TestObituaryCauseMatchesDamageVocabulary(t *testing.T) {
	want := map[string]string{
		" was squished":           "squish",   // dtSQUISH, world/self (client.c:5298, :5729)
		" squishes ":              "squish",   // dtSQUISH, enemy      (client.c:5449)
		" squished a teammate":    "squish",   // dtSQUISH, teamkill   (client.c:5364)
		" blew up":                "explobox", // dtEXPLO_BOX          (client.c:5696)
		" was hooked by ":         "hook",     // dtHOOK               (client.c:5590)
		" cratered":               "fall",     // dtFALL
		" turned into hot slag":   "lava",     // dtLAVA_DMG
		" gulped a load of slime": "slime",    // dtSLIME_DMG
	}
	seen := 0
	for _, p := range ObituaryPatterns {
		w, ok := want[p.Marker]
		if !ok {
			continue
		}
		seen++
		if p.Weapon != w {
			t.Errorf("%q: weapon = %q, want %q", p.Marker, p.Weapon, w)
		}
	}
	if seen != len(want) {
		t.Errorf("matched %d of %d markers — the table lost one", seen, len(want))
	}
	// mvd.DeathTypeToWeapon must agree on the ones it can express.
	for dt, w := range map[int]string{
		mvd.DtSquish: "squish", mvd.DtExploBox: "explobox", mvd.DtHook: "hook",
	} {
		if got := mvd.DeathTypeToWeapon(dt); got != w {
			t.Errorf("DeathTypeToWeapon(%d) = %q, want %q", dt, got, w)
		}
	}
}

// KTX's team branch tests three deathtypes by name before its random
// phrasing pick (ktx/src/client.c:5343-5410): dtTELE1 (:5355), dtSQUISH
// (:5362) and dtSTOMP (:5368). All three carry the real cause — dtSQUISH via
// the one killer-named message in the set — and only the random four
// (:5386-5408) are genuinely cause-less and keep the "teamkill" placeholder.
// Downstream the difference decides whether the kill is priced on the damage
// curve or folded as a positional instant kill (mvd-analytics/damagerecon).
func TestTeamkillPatternsKeepTheirCause(t *testing.T) {
	want := map[string]string{
		" was telefragged by his teammate": "tele",
		" was telefragged by her teammate": "tele",
		" was crushed by his teammate":     "stomp",
		" was crushed by her teammate":     "stomp",
		" was jumped by his teammate":      "stomp",
		" was jumped by her teammate":      "stomp",
		" squished a teammate":             "squish",
		" mows down a teammate":            "teamkill",
		" checks his glasses":              "teamkill",
		" checks her glasses":              "teamkill",
		" loses another friend":            "teamkill",
		" gets a frag for the other team":  "teamkill",
	}
	seen := 0
	for _, p := range ObituaryPatterns {
		w, ok := want[p.Marker]
		if !ok {
			continue
		}
		seen++
		if !p.TeamKill {
			t.Errorf("%q: TeamKill = false", p.Marker)
		}
		if p.Weapon != w {
			t.Errorf("%q: weapon = %q, want %q", p.Marker, p.Weapon, w)
		}
	}
	if seen != len(want) {
		t.Errorf("matched %d of %d teamkill markers — the table lost one", seen, len(want))
	}
}

// Obit-derived DeathEvent must NOT fire before the parser observes a
// match-start phrase — warmup obits (and the very common case of
// match-start telefrag prints arriving before "The match has begun!"
// in the same wire instant) would otherwise pre-seed the dedup state
// and starve the stat-based detector of its post-start emission.
func TestObituaryDeath_GatedOnMatchStart(t *testing.T) {
	p := NewParser(nil)
	p.players[3] = &mvd.PlayerInfo{Slot: 3, Name: "sailorman"}

	var deaths int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*DeathEvent); ok {
			deaths++
		}
		return nil
	})

	// Pre-match obit — must not fire.
	if err := p.tryEmitObituaryDeath("sailorman rides multibear's rocket\n", 1000); err != nil {
		t.Fatalf("pre-match obit: %v", err)
	}
	if deaths != 0 {
		t.Fatalf("pre-match deaths = %d, want 0", deaths)
	}

	// Match-start phrase flips the gate.
	mustMatchStartFromPrint(t, p, mvd.PrintHigh, "The match has begun!\n")
	if !p.matchStarted {
		t.Fatalf("matchStarted gate did not flip on start phrase")
	}

	// Same obit, post-start — must fire exactly once.
	if err := p.tryEmitObituaryDeath("sailorman rides multibear's rocket\n", 2000); err != nil {
		t.Fatalf("post-match obit: %v", err)
	}
	if deaths != 1 {
		t.Errorf("post-match deaths = %d, want 1", deaths)
	}
}

// Older server mods announce the MODE rather than the word "match":
// kmod 1.58 / qwe 0.170 broadcast "The duel has begun!". The pattern
// table used to require "match has begun", so a 2003 kmod duel never
// opened this gate — the demo reported zero deaths and, because stream
// sampling is gated on the same start, produced no streams at all.
// Regression: 1on1_]apollyon[_vs_jogi_[dm4], t=10.022s.
//
// The other two entries are foreign-mod broadcasts with a named archive
// population each (print.go): the CTF mod's "Match Started!" and the arena
// mod's "Series begins in 10 seconds...".
func TestMatchStartPatterns_ModeSpecificAnnouncements(t *testing.T) {
	for _, msg := range []string{
		"The match has begun!\n",
		"The duel has begun!\n",
		"The team match has begun!\n",
		"Match Started!\n",
		"Series begins in 10 seconds...\n",
	} {
		p := NewParser(nil)
		mustMatchStartFromPrint(t, p, mvd.PrintHigh, msg)
		if !p.matchStarted {
			t.Errorf("matchStarted did not flip on %q", msg)
		}
	}

	// A line that merely mentions the match must not open the gate.
	p := NewParser(nil)
	mustMatchStartFromPrint(t, p, mvd.PrintHigh, "The match will begin when both players are ready\n")
	if p.matchStarted {
		t.Errorf("matchStarted flipped on a non-start line")
	}

	// Chat must never open it. The gate never resets, so one prewar
	// "go go go!" would otherwise arm the obituary-death path for the
	// whole demo.
	chat := NewParser(nil)
	mustMatchStartFromPrint(t, chat, mvd.PrintChat, "lets go! the duel has begun i guess\n")
	if chat.matchStarted {
		t.Errorf("matchStarted flipped on a PRINT_CHAT line")
	}

	// Nor may a phrase that is merely a substring of a NAME. The table
	// used to carry a bare "go!", which the whole-archive sweep found
	// firing on nothing but obituary and scoreboard lines naming the E0
	// player "RINGO!!!:::>>>>" — a false match start on 12 demos
	// (.reports/vocab-sweep-2026-08-29, probe S1). Obituaries are
	// PRINT_MEDIUM broadcasts, so the chat refusal above does not cover
	// them; only the table's content does.
	for _, msg := range []string{
		"skii was gibbed by RINGO!!!:::>>>>\n",
		"RINGO!!!:::>>>> rides skii's rocket\n",
		"Fight!\n", // KTX's FIGHT! is a centerprint; a mod bprinting it names no match start
		"latejoin................. join a team after the game started\n",
	} {
		p := NewParser(nil)
		mustMatchStartFromPrint(t, p, mvd.PrintMedium, msg)
		if p.matchStarted {
			t.Errorf("matchStarted flipped on %q", msg)
		}
	}
}

// Obit-emitted DeathEvent bypasses the parser dedup so the pent-
// deflection corner case (KTX dtTELE2, where DF_DEAD never visibly
// leaves the prior dead interval) is still recorded. A second
// consecutive obit for the same player must fire a second
// DeathEvent — matching KTX's authoritative `logfrag(targ, targ)`
// bookkeeping which increments deathcount per obit.
func TestObituaryDeath_ForceEmitsEvenWhenStateAlreadyDead(t *testing.T) {
	p := NewParser(nil)
	p.players[2] = &mvd.PlayerInfo{Slot: 2, Name: "nlk"}
	p.matchStarted = true
	// Pre-seed: parser already thinks nlk is dead.
	p.playerDeadKnown[2] = true
	p.playerDead[2] = true

	var deaths int
	p.OnEvent(func(e Event) error {
		if _, ok := e.(*DeathEvent); ok {
			deaths++
		}
		return nil
	})

	// Two consecutive deflections while nlk's wire state stays dead.
	if err := p.tryEmitObituaryDeath("Satan's power deflects nlk's telefrag\n", 631419); err != nil {
		t.Fatalf("first deflect: %v", err)
	}
	if err := p.tryEmitObituaryDeath("Satan's power deflects nlk's telefrag\n", 633548); err != nil {
		t.Fatalf("second deflect: %v", err)
	}
	if deaths != 2 {
		t.Errorf("deaths = %d, want 2 (both deflections must fire even though state was already dead)", deaths)
	}
}

// End-to-end through parsePrint: a mid-match obit feeds DeathEvent via
// maybeEmitDeath, and the next svc_playerinfo with DF_DEAD clear fires
// SpawnEvent through the existing transition detector.
func TestParsePrint_ObituaryFiresDeathAndNextPlayerInfoFiresSpawn(t *testing.T) {
	p := NewParser(nil)
	p.players[5] = &mvd.PlayerInfo{Slot: 5, Name: "sailorman"}
	p.matchStarted = true
	// Pre-seed: parser thinks sailorman is alive (default state).
	p.playerDeadKnown[5] = true
	p.playerDead[5] = false
	p.playerSeenInfo[5] = true

	var events []Event
	p.OnEvent(func(e Event) error {
		events = append(events, e)
		return nil
	})

	// svc_print payload: [level byte][message string\0].
	payload := []byte{1}
	msg := "sailorman rides multibear's rocket\n"
	payload = append(payload, []byte(msg)...)
	payload = append(payload, 0)
	r := mvd.NewBufferReader(payload)
	if err := p.parsePrint(r, 5000, -1); err != nil {
		t.Fatalf("parsePrint: %v", err)
	}

	// Next svc_playerinfo (DF_DEAD clear) — should fire SpawnEvent.
	pi := []byte{5} // player slot
	pi = binary.LittleEndian.AppendUint16(pi, 0)
	pi = append(pi, 0) // frame
	if err := p.parsePlayerInfo(mvd.NewBufferReader(pi), 5050, false); err != nil {
		t.Fatalf("parsePlayerInfo: %v", err)
	}

	var deaths, spawns int
	for _, e := range events {
		switch e.(type) {
		case *DeathEvent:
			deaths++
		case *SpawnEvent:
			spawns++
		}
	}
	if deaths != 1 || spawns != 1 {
		t.Errorf("deaths=%d spawns=%d, want 1/1", deaths, spawns)
	}
}
