package mapents

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		class      string
		spawnflags int
		wantType   string
		wantKind   string
		wantOK     bool
	}{
		// Items — weapons / ammo / armor / powerups.
		{"weapon_rocketlauncher", 0, TypeItem, "rl", true},
		{"weapon_lightning", 0, TypeItem, "lg", true},
		{"weapon_supershotgun", 0, TypeItem, "ssg", true},
		{"item_spikes", 0, TypeItem, "nails", true},
		{"item_cells", 0, TypeItem, "cells", true},
		{"item_armor1", 0, TypeItem, "ga", true},
		{"item_armor2", 0, TypeItem, "ya", true},
		{"item_armorInv", 0, TypeItem, "ra", true},
		{"item_artifact_super_damage", 0, TypeItem, "quad", true},
		{"item_artifact_invulnerability", 0, TypeItem, "pent", true},
		{"item_artifact_invisibility", 0, TypeItem, "ring", true},
		// item_health spawnflags: H_ROTTEN=1, H_MEGA=2, else 25.
		{"item_health", 0, TypeItem, "h25", true},
		{"item_health", 1, TypeItem, "h15", true},
		{"item_health", 2, TypeItem, "mh", true},
		// Structural.
		{"info_player_deathmatch", 0, TypeSpawn, "", true},
		{"info_player_team1", 0, TypeSpawn, "", true},
		{"info_player_teamspawn", 0, TypeSpawn, "", true},
		// SP / coop starts are NOT deathmatch spawnpoints.
		{"info_player_start", 0, "", "", false},
		{"info_player_start2", 0, "", "", false},
		{"info_player_coop", 0, "", "", false},
		{"info_teleport_destination", 0, TypeTeleportDst, "", true},
		{"trigger_teleport", 0, TypeTeleportSrc, "", true},
		{"func_button", 0, TypeButton, "", true},
		{"func_door", 0, TypeDoor, "", true},
		// Not surfaced.
		{"worldspawn", 0, "", "", false},
		{"light", 0, "", "", false},
		{"trigger_hurt", 0, "", "", false},
		{"monster_army", 0, "", "", false},
	}
	for _, c := range cases {
		gotType, gotKind, gotOK := Classify(c.class, c.spawnflags)
		if gotType != c.wantType || gotKind != c.wantKind || gotOK != c.wantOK {
			t.Errorf("Classify(%q, %d) = (%q,%q,%v), want (%q,%q,%v)",
				c.class, c.spawnflags, gotType, gotKind, gotOK,
				c.wantType, c.wantKind, c.wantOK)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[string]string{
		"rl": "weapon", "ra": "armor", "mh": "mega",
		"h25": "health", "quad": "powerup", "cells": "ammo", "": "",
	}
	for kind, want := range cases {
		if got := (MapEntity{Kind: kind}).Category(); got != want {
			t.Errorf("Category(kind=%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestIsPointEntity(t *testing.T) {
	for _, et := range []string{TypeItem, TypeSpawn, TypeTeleportDst} {
		if !IsPointEntity(et) {
			t.Errorf("IsPointEntity(%q) = false, want true", et)
		}
	}
	for _, et := range []string{TypeTeleportSrc, TypeButton, TypeDoor} {
		if IsPointEntity(et) {
			t.Errorf("IsPointEntity(%q) = true, want false", et)
		}
	}
}
