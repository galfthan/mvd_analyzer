package analyzer_test

// Corpus-wide validation of the bounded damage reconstruction against
// KTX's own accounting embedded in each demo: the per-player scoreboard
// (demoInfo.players[].dmg — bounded dmg_dealt totals) and the per-player-
// per-weapon splits (weapons[].damage.{enemy,team}). The wire carries only
// the unbound value, so near-equality of the reconstruction against these
// independent totals is the correctness signal for the whole shadow-vitals
// + T_Damage-arithmetic pipeline (analyzer/damage.go).
//
// Expected residual per player is small. Under the v55 death-value model a
// survived hit is bounded == raw by identity and a killing hit's overkill
// comes from the end-of-frame death broadcast (bounded = raw + deathValue),
// so given/taken reconcile far tighter than the pre-v55 shadow cap. The
// residuals that remain are: the -99 corpse-health clamp + masked
// (respawn-hidden) deaths falling back to the approximate shadow cap; the
// same-frame multi-hit cascade's approximate save split; and, for ewep, the
// victim-item one-frame window (a same-frame RL/LG pickup reclassifying a
// hit's victim-weapon bucket) — a classification effect independent of the
// health arithmetic, so its band is unchanged from v54. One further class is
// not the reconstruction's at all: damage KTX scores after intermission,
// which our match window excludes (see knownBoundedTeamResiduals).
//
// Run with -v to see every per-player and per-weapon delta.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
)

// Tolerances: measured corpus max |delta| + headroom, re-pinned 2026-07-12
// for the v55 death-value model across the 10-demo corpus (50 players, ~200
// weapon rows). Re-measure with -v when they trip on an intended change.
//
// v54 → v55 measured max |Δ| (the death-value model replacing the shadow
// cap): given 44 → 16, taken 44 → 15, per-weapon enemy 44 → 18 — a ~2.5×
// tightening. ewep (131) and team (1) are unchanged: they are the
// victim-item one-frame window and are independent of the health cap.
const (
	tolBoundedGiven = 40  // measured max |Δ| 16
	tolBoundedTaken = 40  // measured max |Δ| 15
	tolBoundedEWep  = 150 // measured max |Δ| 131 (victim-item one-frame window)
	tolBoundedTeam  = 10  // measured max |Δ| 1
	tolBoundedByWep = 40  // measured max |Δ| 18
)

// knownBoundedTeamResiduals lists "<demo label>/<player>" pairs whose
// bounded TEAM total misses KTX's by more than tolBoundedTeam for a reason
// that is not the reconstruction's. Each is pinned to its MEASURED delta
// with a tight slack rather than waved through: a drift in the team split
// still fails everywhere else, and a regression that widens a known
// residual fails right here.
//
// The one entry is the POST-INTERMISSION class: damage KTX scores that the
// match window excludes. KTX's T_Damage accumulates dmg_team and
// wpn[].tdamage with no match_in_progress gate on that path
// (ktx/src/combat.c:1057-1058), so a hit landing after the match has ended
// still lands on the end-of-match scoreboard, while every total we publish
// is windowed to the match. On this demo Venator lands four dtAXE team hits
// of 20 each (mvdhidden_dmgdone at demo-ms 1206856 and 1207889 on cronus,
// 1209595 and 1210101 on george); matchEnd is 1210045 demo-ms, so the
// fourth is 56 ms past intermission. Three are served (the served
// byWeaponTeam axe total reads 60) and KTX counted all four (80): exactly
// the -20, all of it axe, with every other weapon 0.
//
// It is a WINDOW question, never a missing event: KTX cannot silently omit
// an in-window hidden damage message (combat.c:795-807 writes
// mvdhidden_dmgdone whenever unbound_dmg_dealt > 0), so damage this side of
// intermission always reaches the stream. The same shape — ours short of
// KTX's by a sliver — shows up on this player's given (-4) and taken (-5)
// and stays inside those tolerances; only the axe hit is big enough to name.
type knownTeamResidual struct {
	delta int    // measured stream-minus-KTX delta
	slack int    // permitted drift around it
	why   string // the invariant, not the symptom
}

var knownBoundedTeamResiduals = map[string]knownTeamResidual{
	"4on4_blue_red_200626_e1m2_sameslot_rejoin/Venator": {
		delta: -20, slack: 5,
		why: "one 20-point axe team hit KTX scored 56 ms after intermission, outside our match window",
	},
}

func TestBoundedReconciliationCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	if len(corpus) == 0 {
		t.Skip("testdata/corpus.json has no entries")
	}
	cacheDir := filepath.Join("..", "testdata", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}

	within := func(t *testing.T, what string, stream, score, tol int) {
		t.Helper()
		if d := stream - score; d > tol || d < -tol {
			t.Errorf("%s: stream %d vs KTX %d (delta %+d exceeds ±%d)", what, stream, score, d, tol)
		}
	}

	for _, entry := range corpus {
		t.Run(entry.Label, func(t *testing.T) {
			mvdPath := ensureCached(t, cacheDir, entry)
			res, err := analyzer.NewDefaultRegistry().Analyze(mvdPath)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			d := res.Damage
			if d == nil {
				t.Skip("no damage section")
			}
			if d.BoundedMode != "standard" {
				t.Skipf("bounded reconstruction skipped (%s)", d.BoundedMode)
			}
			if d.Scoreboard == nil {
				t.Skip("no KTX scoreboard in this demo")
			}

			// Per-player scoreboard reconciliation.
			for name, delta := range d.Scoreboard.ByPlayer {
				b := delta.Bounded
				if b == nil {
					t.Errorf("%s: no bounded delta despite standard mode", name)
					continue
				}
				t.Logf("%-18s givenΔ=%+4d takenΔ=%+4d ewepΔ=%+4d teamΔ=%+4d",
					name,
					b.StreamGiven-delta.ScoreGiven,
					b.StreamTaken-delta.ScoreTaken,
					b.StreamEWep-delta.ScoreEWep,
					b.StreamTeam-b.ScoreTeam)
				within(t, name+" bounded given", b.StreamGiven, delta.ScoreGiven, tolBoundedGiven)
				within(t, name+" bounded taken", b.StreamTaken, delta.ScoreTaken, tolBoundedTaken)
				within(t, name+" bounded ewep", b.StreamEWep, delta.ScoreEWep, tolBoundedEWep)
				if known, isKnown := knownBoundedTeamResiduals[entry.Label+"/"+name]; isKnown {
					// Pinned to its measured magnitude: "known" means this
					// exact residual, not an open-ended exemption.
					d := b.StreamTeam - b.ScoreTeam
					if drift := d - known.delta; drift > known.slack || drift < -known.slack {
						t.Errorf("%s bounded team: stream %d vs KTX %d (delta %+d) — known residual is %+d ±%d (%s)",
							name, b.StreamTeam, b.ScoreTeam, d, known.delta, known.slack, known.why)
					} else {
						t.Logf("%s bounded team: stream %d vs KTX %d (delta %+d, known %+d): %s",
							name, b.StreamTeam, b.ScoreTeam, d, known.delta, known.why)
					}
				} else {
					within(t, name+" bounded team", b.StreamTeam, b.ScoreTeam, tolBoundedTeam)
				}
			}

			// Per-weapon reconciliation: our bounded byWeapon vs the KTX
			// demostats weapons[].damage.enemy (key names verified identical:
			// axe/sg/ssg/ng/sng/gl/rl/lg — KTX WpName, ktx/src/stats.c:358).
			// Team-per-weapon has no stored aggregate; derive it from the
			// events log (tele/stomp excluded on both sides — KTX wpNONE).
			if res.DemoInfo != nil {
				for _, p := range res.DemoInfo.Players {
					pd := d.ByPlayer[p.Name]
					if pd == nil || pd.Bounded == nil {
						continue
					}
					teamByWep := map[string]int{}
					for _, e := range d.Events {
						if e.IsTeam && e.Attacker == p.Name {
							v := e.Damage
							if e.Bounded != nil {
								v = *e.Bounded
							}
							teamByWep[e.Weapon] += v
						}
					}
					for wname, w := range p.Weapons {
						if w == nil || w.Damage == nil {
							continue
						}
						if w.Damage.Enemy != 0 || pd.Bounded.ByWeapon[wname] != 0 {
							t.Logf("%-18s %-4s enemyΔ=%+4d", p.Name, wname,
								pd.Bounded.ByWeapon[wname]-w.Damage.Enemy)
							within(t, p.Name+" "+wname+" enemy", pd.Bounded.ByWeapon[wname], w.Damage.Enemy, tolBoundedByWep)
						}
						if w.Damage.Team != 0 || teamByWep[wname] != 0 {
							t.Logf("%-18s %-4s teamΔ =%+4d", p.Name, wname,
								teamByWep[wname]-w.Damage.Team)
							within(t, p.Name+" "+wname+" team", teamByWep[wname], w.Damage.Team, tolBoundedByWep)
						}
					}
				}
			}

			// Pin the live pent-deflect coverage: dm3 contains "Satan's power
			// deflects nlk's telefrag" — on the wire an ordinary dtTELE2
			// dmgdone with the pent holder as attacker and nlk as victim.
			// The fold-in must reconstruct a positive bounded value for it.
			if entry.Label == "4on4_osams_ra_230426_dm3" {
				found := false
				for _, tf := range d.Telefrags {
					if tf.Victim == "nlk" && tf.Bounded != nil && *tf.Bounded > 0 {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("dm3 deflect pin: no telefrag on nlk with a positive bounded value (%+v)", d.Telefrags)
				}
			}
		})
	}
}
