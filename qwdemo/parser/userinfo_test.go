package parser

import (
	"testing"

	"github.com/mvd-analyzer/qwdemo/mvd"
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

func TestParseUserInfoString_AuthWithSpecialChars(t *testing.T) {
	player := &mvd.PlayerInfo{}
	parseUserInfoString(`\name\TestUser\team\blue\*auth\test_login-123`, player)

	if player.Auth != "test_login-123" {
		t.Errorf("Auth = %q, want %q", player.Auth, "test_login-123")
	}
}
