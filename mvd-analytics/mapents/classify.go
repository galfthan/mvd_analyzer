package mapents

// Classify maps a BSP entity classname (plus spawnflags, needed only for
// item_health) to our entity Type and — for items — the compact Kind.
// ok is false for classnames we don't surface (worldspawn, triggers we
// don't track, monsters, lights, …).
//
// Ground truth: ktx/src/items.c spawn functions and the standard Quake 1
// progs. item_health spawnflags H_ROTTEN(1)/H_MEGA(2) select the small
// rotten / megahealth variants (ktx/include/g_consts.h:241-242,
// ktx/src/items.c:244-270).
func Classify(classname string, spawnflags int) (etype, kind string, ok bool) {
	if k, isItem := itemKind(classname, spawnflags); isItem {
		return TypeItem, k, true
	}
	switch classname {
	// Deathmatch / team spawn points only. info_player_start(2) and
	// info_player_coop are the singleplayer / coop starts — present in
	// nearly every id map but NOT where QuakeWorld deathmatch spawns
	// players — so they are deliberately not counted as DM spawnpoints
	// (they would show as a phantom extra spawn, e.g. dm3's YA.Quad).
	case "info_player_deathmatch",
		"info_player_team1", "info_player_team2",
		"info_player_team1_deathmatch", "info_player_team2_deathmatch",
		"info_player_teamspawn":
		return TypeSpawn, "", true
	case "info_teleport_destination":
		return TypeTeleportDst, "", true
	case "trigger_teleport", "trigger_custom_teleport":
		return TypeTeleportSrc, "", true
	case "func_button":
		return TypeButton, "", true
	case "func_door", "func_door_secret":
		return TypeDoor, "", true
	}
	return "", "", false
}

// itemKind classifies the pickup classnames into the compact kind
// vocabulary shared with result.ItemTimeline.Kind.
func itemKind(classname string, spawnflags int) (string, bool) {
	switch classname {
	case "weapon_supershotgun":
		return "ssg", true
	case "weapon_nailgun":
		return "ng", true
	case "weapon_supernailgun":
		return "sng", true
	case "weapon_grenadelauncher":
		return "gl", true
	case "weapon_rocketlauncher":
		return "rl", true
	case "weapon_lightning":
		return "lg", true
	case "item_shells":
		return "shells", true
	case "item_spikes":
		return "nails", true
	case "item_rockets":
		return "rockets", true
	case "item_cells":
		return "cells", true
	case "item_armor1":
		return "ga", true
	case "item_armor2":
		return "ya", true
	case "item_armorInv":
		return "ra", true
	case "item_artifact_super_damage":
		return "quad", true
	case "item_artifact_invulnerability":
		return "pent", true
	case "item_artifact_invisibility":
		return "ring", true
	case "item_artifact_envirosuit":
		return "suit", true
	case "item_health":
		const hRotten, hMega = 1, 2
		switch {
		case spawnflags&hRotten != 0:
			return "h15", true
		case spawnflags&hMega != 0:
			return "mh", true
		default:
			return "h25", true
		}
	}
	return "", false
}

// IsPointEntity reports whether an entity Type carries its position in
// the entity origin (true) or in a brush bmodel bbox that must be
// resolved from the BSP models lump (false). Used by the generator to
// decide which entities it can place without geometry.
func IsPointEntity(etype string) bool {
	switch etype {
	case TypeItem, TypeSpawn, TypeTeleportDst:
		return true
	default:
		return false
	}
}
