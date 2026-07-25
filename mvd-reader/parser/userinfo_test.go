package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

func TestParseUserInfoString_AuthKey(t *testing.T) {
	player := &mvd.PlayerInfo{}
	parseUserInfoString(`\name\Neophyte\team\red\*auth\Neophyte\topcolor\4\bottomcolor\4`, player)

	if player.Auth != "Neophyte" {
		t.Errorf("Auth = %q, want %q", player.Auth, "Neophyte")
	}
	if player.Name != "Neophyte" {
		t.Errorf("Name = %q, want %q", player.Name, "Neophyte")
	}
	if player.Team != "red" {
		t.Errorf("Team = %q, want %q", player.Team, "red")
	}
}

func TestParseUserInfoString_NoAuth(t *testing.T) {
	player := &mvd.PlayerInfo{}
	parseUserInfoString(`\name\splif\team\blue\topcolor\13\bottomcolor\13`, player)

	if player.Auth != "" {
		t.Errorf("Auth = %q, want empty", player.Auth)
	}
	if player.Name != "splif" {
		t.Errorf("Name = %q, want %q", player.Name, "splif")
	}
}

func TestParseUserInfoString_SpectatorStarKey(t *testing.T) {
	// mvdsv rewrites the client's "spectator" key to the server-set star
	// key "*spectator" before broadcast (sv_main.c:1065-1066), so full
	// userinfo strings in MVDs carry the star spelling. A spectator whose
	// flag is missed here leaks into match.players as a 0-frag player.
	player := &mvd.PlayerInfo{}
	parseUserInfoString(`\*spectator\1\*client\ezQuake 8065\team\psy\name\mythic`, player)
	if !player.Spectator {
		t.Errorf("Spectator = false, want true for *spectator key")
	}

	// Bare spelling still accepted (non-mvdsv sources).
	player = &mvd.PlayerInfo{}
	parseUserInfoString(`\spectator\1\name\obs`, player)
	if !player.Spectator {
		t.Errorf("Spectator = false, want true for bare spectator key")
	}

	// "spectator 0" must not set the flag.
	player = &mvd.PlayerInfo{}
	parseUserInfoString(`\*spectator\0\name\p1`, player)
	if player.Spectator {
		t.Errorf("Spectator = true, want false for *spectator=0")
	}
}

func TestStripChatMarkup(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"empty", "", ""},
		{"leading CR", "\rhello", "hello"},
		{"color code", "&c5afix&cffftext", "ixtext"},
		{"reset code", "&rwhite", "white"},
		{"sound trigger K", "going for quad!K", "going for quad"},
		{"sound trigger H", "team take!H", "team take"},
		{"macro braces", "{name}: hi", "name: hi"},
		{"macro brackets", "[loc]", "loc"},
		{
			"real teamsay fixture",
			"\r{&c5afbix&cfff}: coming [{quad.low}]",
			"bix: coming quad.low",
		},
		{
			"team status fixture",
			"\r{&c39faki&cfff}{&c39f:&cfff} 0/100 sng:80 [{ra.low}]",
			"aki: 0/100 sng:80 ra.low",
		},
		{"only sound trigger doesn't strip lowercase", "abc!k", "abc!k"},
		{"idempotent on cleaned", "bix: coming quad.low", "bix: coming quad.low"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripChatMarkup(tc.in)
			if got != tc.want {
				t.Errorf("StripChatMarkup(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Idempotence: applying again should be a no-op.
			if again := StripChatMarkup(got); again != got {
				t.Errorf("not idempotent: StripChatMarkup(%q) = %q (after first pass %q)", got, again, got)
			}
		})
	}
}

func TestParseUserInfoString_AuthWithSpecialChars(t *testing.T) {
	player := &mvd.PlayerInfo{}
	parseUserInfoString(`\name\TestUser\team\blue\*auth\test_login-123`, player)

	if player.Auth != "test_login-123" {
		t.Errorf("Auth = %q, want %q", player.Auth, "test_login-123")
	}
}

// An empty userinfo string is the server's drop broadcast, not a resend:
// SV_DropClient clears the client's name and both userinfo contexts and
// then calls SV_FullClientUpdate (mvdsv/src/sv_main.c:419-428, :487-513),
// so the departure reaches the demo as svc_updatefrags <slot> 0 followed
// by svc_updateuserinfo <slot> <userid> "". Flagging it is what lets the
// analytics layer tell "this slot is now empty" from "this slot's userinfo
// changed", and the carried-forward name is what says who left.
func TestParseUserInfo_EmptyStringFlagsVacated(t *testing.T) {
	p := NewParser(nil)
	var got []*UserInfoEvent
	p.OnEvent(func(e Event) error {
		if ui, ok := e.(*UserInfoEvent); ok {
			got = append(got, ui)
		}
		return nil
	})

	full := "\\name\\DARKLORD\\team\\|l|"
	buf := append([]byte{13}, 0xC6, 0x13, 0, 0) // slot 13, userid 5062 little-endian
	buf = append(buf, full...)
	buf = append(buf, 0)
	if err := p.parseUserInfo(mvd.NewBufferReader(buf), 1000); err != nil {
		t.Fatalf("parseUserInfo: %v", err)
	}

	// The drop: same userid, empty userinfo string.
	buf = append([]byte{13}, 0xC6, 0x13, 0, 0)
	buf = append(buf, 0)
	if err := p.parseUserInfo(mvd.NewBufferReader(buf), 2000); err != nil {
		t.Fatalf("parseUserInfo (drop): %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2", len(got))
	}
	if got[0].Vacated {
		t.Errorf("a full userinfo string must not be flagged Vacated")
	}
	if !got[1].Vacated {
		t.Errorf("an empty userinfo string must be flagged Vacated")
	}
	if got[1].Player.Name != "DARKLORD" {
		t.Errorf("Name = %q, want the carried-forward %q so consumers can tell who left",
			got[1].Player.Name, "DARKLORD")
	}
	if got[1].Player.UserID != got[0].Player.UserID {
		t.Errorf("UserID = %d, want the departing client's own %d",
			got[1].Player.UserID, got[0].Player.UserID)
	}
	for i, ui := range got {
		if ui.Partial {
			t.Errorf("event %d: svc_updateuserinfo must not be flagged Partial", i)
		}
	}
}

// An svc_setinfo event is a synthesis over the parser's cached PlayerInfo:
// it carries whatever userid the last full userinfo left on the slot, so
// the userid on it was never on the wire. Partial says so — without the
// flag, the `svc_setinfo <slot> "*auth" ""` mvdsv emits during the NEXT
// client's connect handshake (SV_Login -> SV_Logout, sv_login.c:588 and
// :644-646) reads as the departed client's own userid returning, and an
// occupancy tracker resumes a connection that is long gone.
//
// Ground truth, hub gameId 216835 slot 7: rusti's drop at t=613452, then
// `svc_setinfo 7 "*auth" ""` at t=685676 with no userinfo of any kind
// between them, then Luk's real userinfo at t=766898.
func TestParseSetInfo_FlagsPartialAndReplaysCachedUserID(t *testing.T) {
	p := NewParser(nil)
	var got []*UserInfoEvent
	p.OnEvent(func(e Event) error {
		if ui, ok := e.(*UserInfoEvent); ok {
			got = append(got, ui)
		}
		return nil
	})

	buf := append([]byte{7}, 8, 0, 0, 0) // slot 7, userid 8
	buf = append(buf, "\\name\\rusti\\team\\jah"...)
	buf = append(buf, 0)
	if err := p.parseUserInfo(mvd.NewBufferReader(buf), 0); err != nil {
		t.Fatalf("parseUserInfo: %v", err)
	}
	// The drop clears the slot but leaves the parser's cache in place.
	buf = append([]byte{7}, 8, 0, 0, 0)
	buf = append(buf, 0)
	if err := p.parseUserInfo(mvd.NewBufferReader(buf), 613452); err != nil {
		t.Fatalf("parseUserInfo (drop): %v", err)
	}
	// The next client's connect handshake: svc_setinfo 7 "*auth" "".
	set := append([]byte{7}, "*auth"...)
	set = append(set, 0, 0)
	if err := p.parseSetInfo(mvd.NewBufferReader(set), 685676); err != nil {
		t.Fatalf("parseSetInfo: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3", len(got))
	}
	last := got[2]
	if !last.Partial {
		t.Errorf("svc_setinfo must be flagged Partial")
	}
	if last.Vacated {
		t.Errorf("svc_setinfo can never carry an empty userinfo string, so it is never Vacated")
	}
	if last.Player.UserID != 8 {
		t.Errorf("UserID = %d, want the cached 8 — this is exactly the trap Partial exists to mark",
			last.Player.UserID)
	}
}
