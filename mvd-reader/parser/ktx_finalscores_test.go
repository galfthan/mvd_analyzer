package parser

import (
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// captureFinalScores runs one svc_stufftext through the parser in diagnostic
// mode and returns the typed event (nil when none was emitted) plus the
// warnings the attempt produced.
func captureFinalScores(t *testing.T, cmd string) (*FinalScoresEvent, []Warning) {
	t.Helper()
	p := NewParser(nil)
	p.SetDiagnosticMode(true)
	var captured *FinalScoresEvent
	p.OnEvent(func(e Event) error {
		if fs, ok := e.(*FinalScoresEvent); ok {
			captured = fs
		}
		return nil
	})
	msg := &mvd.DemoMessage{
		Header:  mvd.MessageHeader{MessageType: mvd.DemSingle, PlayerNum: 0},
		Payload: stuffTextPayload(cmd),
		TimeMs:  612340,
	}
	if err := p.parseNetworkMessage(msg); err != nil {
		t.Fatalf("parseNetworkMessage: %v", err)
	}
	return captured, p.DiagnosticWarnings()
}

func TestFinalScores_WellFormed(t *testing.T) {
	got, warns := captureFinalScores(t,
		"//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" \"kip\" 19 \"grisling\" 24\n")
	if got == nil {
		t.Fatal("no FinalScoresEvent emitted")
	}
	if got.Date != "Sep 29, 21:27" {
		t.Errorf("Date = %q", got.Date)
	}
	if got.Mode != "duel" || got.Map != "aerowalk" {
		t.Errorf("Mode/Map = %q/%q", got.Mode, got.Map)
	}
	if got.Team1 != "kip" || got.Score1 != 19 {
		t.Errorf("team1 = %q %d", got.Team1, got.Score1)
	}
	if got.Team2 != "grisling" || got.Score2 != 24 {
		t.Errorf("team2 = %q %d", got.Team2, got.Score2)
	}
	if got.TimeMs != 612340 {
		t.Errorf("TimeMs = %d, want 612340", got.TimeMs)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

// Team and player names routinely carry spaces, an empty side is observed in
// the archive, and a score is negative whenever suicides outnumber frags —
// none of those is a garbled line.
func TestFinalScores_QuotedNamesAndNegativeScores(t *testing.T) {
	got, warns := captureFinalScores(t,
		"//finalscores \"Sep 11, 21:04\" \"team\" \"dm2\" \"Sven Quakesson\" -3 \"\" 0\n")
	if got == nil {
		t.Fatal("no FinalScoresEvent emitted")
	}
	if got.Team1 != "Sven Quakesson" || got.Score1 != -3 {
		t.Errorf("team1 = %q %d", got.Team1, got.Score1)
	}
	if got.Team2 != "" || got.Score2 != 0 {
		t.Errorf("team2 = %q %d", got.Team2, got.Score2)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

// Team names are userinfo values in the Quake charset — the gold-coloured
// "'tbg" arrives as 0xA7 0x74 0x62 0x67 — and must come out normalised like
// every other name the parser emits.
func TestFinalScores_TeamNamesNormalised(t *testing.T) {
	got, _ := captureFinalScores(t,
		"//finalscores \"Mar 08, 21:16\" \"team\" \"dm2\" \"\xa7tbg\" 169 \"\xd0\xc5\xd8\" 214\n")
	if got == nil {
		t.Fatal("no FinalScoresEvent emitted")
	}
	if got.Team1 != "'tbg" {
		t.Errorf("Team1 = %q, want %q", got.Team1, "'tbg")
	}
	if got.Team2 != "PEX" {
		t.Errorf("Team2 = %q, want %q", got.Team2, "PEX")
	}
}

// The multi-word mode name KTX emits for Clan Arena / HoonyMode must survive
// the tokeniser — it is quoted, so it is one field, not two.
func TestFinalScores_MultiWordMode(t *testing.T) {
	got, _ := captureFinalScores(t,
		"//finalscores \"Jan 06, 03:00\" \"Clan Arena\" \"ztndm3\" \"red\" 5 \"blue\" 2\n")
	if got == nil {
		t.Fatal("no FinalScoresEvent emitted")
	}
	if got.Mode != "Clan Arena" {
		t.Errorf("Mode = %q, want %q", got.Mode, "Clan Arena")
	}
}

func TestFinalScores_GarbledLinesDroppedWithWarning(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"truncated", "//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" \"kip\"\n"},
		{"unterminated quote", "//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" \"kip 19 \"grisling\" 24\n"},
		{"score not a number", "//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" \"kip\" x \"grisling\" 24\n"},
		{"name lost its quotes", "//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" kip 19 \"grisling\" 24\n"},
		{"extra field", "//finalscores \"Sep 29, 21:27\" \"duel\" \"aerowalk\" \"kip\" 19 \"grisling\" 24 7\n"},
		{"corrupted payload", "//finalscores Final Score is 47 - 9\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warns := captureFinalScores(t, tc.cmd)
			if got != nil {
				t.Fatalf("garbled line produced an event: %+v", got)
			}
			if len(warns) == 0 {
				t.Fatal("garbled line produced no diagnostic warning")
			}
			if !strings.Contains(warns[0].Message, "//finalscores") {
				t.Errorf("warning does not name the directive: %q", warns[0].Message)
			}
		})
	}
}

// A directive that only shares the prefix is not ours, and must not warn.
func TestFinalScores_PrefixBoundary(t *testing.T) {
	got, warns := captureFinalScores(t, "//finalscoresXX \"Sep 29, 21:27\"\n")
	if got != nil {
		t.Fatalf("unexpected event: %+v", got)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}
