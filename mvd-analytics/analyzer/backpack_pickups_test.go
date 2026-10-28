package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestBackpackPickupsPost(t *testing.T) {
	res := &Result{
		Backpacks: []result.BackpackDrop{
			{Time: 1000, Player: "a", Team: "red", EntNum: 7, Source: "ktx"},
			{Time: 5000, Player: "a", Team: "red", EntNum: 7, Source: "ktx"},
			{Time: 6000, Player: "b", Team: "blue", EntNum: 8, Source: "ktx", Fate: result.BackpackFateExpired},
			{Time: 7000, Player: "b", Team: "blue", EntNum: 9, Source: "ktx"},
			{Time: 8000, Player: "c", Team: "blue", EntNum: 10, Source: "ktx"},
			{Time: 9000, Player: "d", Team: "red", Source: "reconstructed", Fate: result.BackpackFatePicked, Picker: "e", PickerTeam: "red", PickupTime: 9500},
		},
		WeaponPickups: []result.WeaponPickup{
			// Edict 7 recycled: the pickup joins the drop with its own drop time.
			{Time: 6500, Player: "b", Team: "blue", Source: "backpack", BackpackEnt: 7, DropTime: 5000, HadBefore: false, Kills: 2, NextDeathTime: 20000},
			{Time: 9000, Player: "c", Team: "blue", Source: "backpack", BackpackEnt: 10, DropTime: 8000, HadBefore: true},
			{Time: 9100, Player: "x", Source: "world", BackpackEnt: 9, DropTime: 7000},
		},
	}
	backpackPickupsPost(res, nil)
	bp := res.Backpacks
	if bp[0].Fate != result.BackpackFateUnobserved || bp[0].Picker != "" {
		t.Errorf("drop 0 (edict reused later) = %+v, want unobserved", bp[0])
	}
	d := bp[1]
	if d.Fate != result.BackpackFatePicked || d.Picker != "b" || d.PickerTeam != "blue" || d.PickupTime != 6500 ||
		d.PickerRelation != "enemy" || d.PickerHadWeapon == nil || *d.PickerHadWeapon || d.PickerKills == nil || *d.PickerKills != 2 || d.PickerNextDeathTime != 20000 {
		t.Errorf("drop 1 = %+v", d)
	}
	if bp[2].Fate != result.BackpackFateExpired {
		t.Errorf("an expire hint must survive: %+v", bp[2])
	}
	if bp[3].Fate != result.BackpackFateUnobserved {
		t.Errorf("a world pickup never joins: %+v", bp[3])
	}
	if bp[4].PickerRelation != "self" || !*bp[4].PickerHadWeapon {
		t.Errorf("dropper took their own pack back: %+v", bp[4])
	}
	r := bp[5]
	if r.PickerRelation != "team" || r.PickerHadWeapon != nil || r.PickerKills != nil || r.Fate != result.BackpackFatePicked {
		t.Errorf("reconstructed row keeps its fate, gains only the relation: %+v", r)
	}
}
