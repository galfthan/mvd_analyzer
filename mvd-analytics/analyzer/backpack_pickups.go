package analyzer

import "github.com/mvd-analyzer/mvd-analytics/result"

// backpackPickupsPost stamps every `ktx` backpack drop with its pickup side,
// so a consumer reads one list instead of re-doing the join (v77).
//
// The pickup is the weaponPickups row KTX's `//ktx bp` produced for that pack
// (Source "backpack"), joined on (BackpackEnt, DropTime) == (EntNum, Time) —
// by edict AND drop time, because a match recycles a backpack edict through
// many packs (the same key applyExpireHints walks). A joined drop becomes
// Fate picked with the pickup's picker, team, time, HadBefore, Kills and
// NextDeathTime copied over unchanged. An unjoined drop keeps the `expired`
// applyExpireHints stamped from `//ktx expire`, and is otherwise unobserved:
// a demo can carry the drop hint and no pickup hints at all (107 of 330
// archive demos), so no `//ktx bp` is not evidence that nobody took it.
//
// Reconstructed rows keep the linkage's own fate; they only gain
// PickerRelation, which is the same comparison on whichever picker a row
// names.
func backpackPickupsPost(res *Result, _ *CoreOutputs) {
	if len(res.Backpacks) == 0 {
		return
	}
	type key struct{ ent, at int32 }
	byDrop := make(map[key]*result.WeaponPickup)
	for i := range res.WeaponPickups {
		p := &res.WeaponPickups[i]
		if p.Source == "backpack" && p.BackpackEnt != 0 {
			byDrop[key{int32(p.BackpackEnt), p.DropTime}] = p
		}
	}
	for i := range res.Backpacks {
		d := &res.Backpacks[i]
		if d.Source == backpackSourceKTX {
			if p := byDrop[key{int32(d.EntNum), d.Time}]; p != nil && d.EntNum != 0 {
				had, kills := p.HadBefore, p.Kills
				d.Fate = result.BackpackFatePicked
				d.Picker, d.PickerTeam, d.PickupTime = p.Player, p.Team, p.Time
				d.PickerHadWeapon, d.PickerKills, d.PickerNextDeathTime, d.PickerEndTime = &had, &kills, p.NextDeathTime, p.EndTime
			} else if d.Fate == "" {
				d.Fate = result.BackpackFateUnobserved
			}
		}
		switch {
		case d.Picker == "":
		case d.Picker == d.Player:
			// The dropper respawned and ran back to their own pack.
			d.PickerRelation = "self"
		case d.Team != "" && d.Team == d.PickerTeam:
			d.PickerRelation = "team"
		default:
			d.PickerRelation = "enemy"
		}
	}
}
