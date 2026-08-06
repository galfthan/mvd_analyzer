package analyzer_test

// Cross-check of the derived victim-weapon kill split against the server's
// own accounting. Runs on every golden-corpus demo (called from the
// TestGoldenCorpus subtest, so it costs no extra pipeline run).
//
// This is the evidence behind the claim that playerStats.score.byEnemyWeapon
// is not merely plausible but reproduces KTX exactly. KTX counts ekills
// INCLUSIVELY — every bucket the victim's inventory carried at death, so a
// victim holding both RL and LG bumps both (ktx/src/client.c:4703-4741) —
// while our buckets are exclusive. The two therefore relate as
//
//	ktx.ekills.rl == derived.rl + derived.both
//	ktx.ekills.lg == derived.lg + derived.both
//
// which is the identity asserted here. It is also what makes the exclusive
// keying safe to publish: no information is lost relative to the server's
// figure, it is just re-cut so the buckets partition.
//
// Only rl and lg are compared, because they are the only buckets KTX always
// writes honestly: it force-zeroes axe and sg, and zeroes EVERY bucket on
// deathmatch >= 4 and k_instagib (ktx/src/stats_json.c:377-380).

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func checkEnemyWeaponKillsVsKTX(t *testing.T, res *result.Result, label string) {
	t.Helper()
	if res.PlayerStats == nil || res.DemoInfo == nil || len(res.DemoInfo.Players) == 0 {
		return // no KTX block to compare against — nothing to say
	}
	ktx := make(map[string]*result.DemoInfoPlayer, len(res.DemoInfo.Players))
	for i := range res.DemoInfo.Players {
		ktx[res.DemoInfo.Players[i].Name] = &res.DemoInfo.Players[i]
	}

	ekills := func(p *result.DemoInfoPlayer, weapon string) int {
		w := p.Weapons[weapon]
		if w == nil || w.Kills == nil {
			return 0
		}
		return w.Kills.Enemy
	}

	var ktxTotal, derivedTotal int
	type cmp struct {
		name                       string
		dRL, dLG, kRL, kLG, killed int
	}
	var rows []cmp
	for i := range res.PlayerStats.Players {
		row := &res.PlayerStats.Players[i]
		k := ktx[row.Name]
		if k == nil {
			continue
		}
		m := row.Score.ByEnemyWeapon
		c := cmp{
			name: row.Name,
			dRL:  m[result.VictimWeaponRL] + m[result.VictimWeaponBoth],
			dLG:  m[result.VictimWeaponLG] + m[result.VictimWeaponBoth],
			kRL:  ekills(k, "rl"),
			kLG:  ekills(k, "lg"),
		}
		if row.Score.Kills != nil {
			c.killed = *row.Score.Kills
		}
		rows = append(rows, c)
		ktxTotal += c.kRL + c.kLG
		derivedTotal += c.dRL + c.dLG

		// The buckets are published as a PARTITION of kills, which is a
		// stronger claim than the KTX identity and worth its own check:
		// a classification that quietly dropped or double-counted a kill
		// would still satisfy the rl/lg comparison above.
		if row.Score.Kills != nil && len(m) > 0 {
			sum := 0
			for _, n := range m {
				sum += n
			}
			if sum != *row.Score.Kills {
				t.Errorf("%s: %s byEnemyWeapon sums to %d, want kills = %d (%v)",
					label, row.Name, sum, *row.Score.Kills, m)
			}
		}

		// The cross-tab publishes a third view of the same kills, so both
		// its marginals must reproduce the maps beside it — otherwise a
		// consumer picking a different one of the three gets a different
		// answer. Asserted on real demos because the two marginals reach
		// the row by different routes: byWeapon is the frag analyzer's own
		// tally (with its Finalize reclassification fixups), while the
		// cross-tab is counted here from the finished entries.
		if cross := row.Score.ByWeaponVsEnemyWeapon; len(cross) > 0 {
			outer, inner := map[string]int{}, map[string]int{}
			for w, byBucket := range cross {
				for b, n := range byBucket {
					outer[w] += n
					inner[b] += n
				}
			}
			for w, n := range outer {
				if row.Score.ByWeapon[w] != n {
					t.Errorf("%s: %s cross-tab[%s] sums to %d, want byWeapon[%s] = %d",
						label, row.Name, w, n, w, row.Score.ByWeapon[w])
				}
			}
			for w, n := range row.Score.ByWeapon {
				if outer[w] != n {
					t.Errorf("%s: %s byWeapon[%s] = %d but the cross-tab has %d — a weapon went missing",
						label, row.Name, w, n, outer[w])
				}
			}
			for b, n := range inner {
				if m[b] != n {
					t.Errorf("%s: %s cross-tab bucket %s sums to %d, want byEnemyWeapon[%s] = %d",
						label, row.Name, b, n, b, m[b])
				}
			}
			for b, n := range m {
				if inner[b] != n {
					t.Errorf("%s: %s byEnemyWeapon[%s] = %d but the cross-tab has %d",
						label, row.Name, b, n, inner[b])
				}
			}
		}
	}

	// A mode that suppresses ekills server-side (deathmatch >= 4,
	// k_instagib) writes zeros for everyone, which is not a reading and
	// must not be asserted against. Distinguish that from a genuine
	// all-zero match by whether WE saw armed victims at all.
	if ktxTotal == 0 && derivedTotal > 0 {
		t.Logf("%s: KTX ekills all zero while %d armed-victim kills were derived — "+
			"mode suppresses the counter (stats_json.c:377-380), comparison skipped",
			label, derivedTotal)
		return
	}

	for _, c := range rows {
		if c.dRL != c.kRL {
			t.Errorf("%s: %s enemy-RL kills = %d (rl+both), want KTX ekills.rl = %d",
				label, c.name, c.dRL, c.kRL)
		}
		if c.dLG != c.kLG {
			t.Errorf("%s: %s enemy-LG kills = %d (lg+both), want KTX ekills.lg = %d",
				label, c.name, c.dLG, c.kLG)
		}
	}
}
