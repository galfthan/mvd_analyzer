package result

// AimResult is the per-player aim analysis (schema v41), derived as a
// post-process from Shots + Streams (interpolated position/view at fire time)
// + Damage + the LG beam stream. It is experimental and additive: it never
// changes the inputs it reads.
//
// Truthfulness contract:
//   - The crosshair-error samples are HITSCAN-ONLY (sg/ssg/lg). Rockets are
//     led, so crosshair-to-enemy is not "error" — rl/gl get the Weapons
//     direct/splash/missed split instead.
//   - Error is reported both as signed degrees (DYaw/DPitch — the literal
//     "degrees off the enemy" drift metric) AND normalized by the target's
//     angular half-size (NYaw/NPitch — comparable across range; |n|<=1 is
//     roughly on the hitbox). The frontend plots offsets in Quake units at
//     the target, derived from DYaw/DPitch and Dist.
//   - Hit/miss is Shot.Hit (the Go-linked truth), never re-derived here —
//     for the MEASURED counters. The one exception is the reconstructed tier
//     (WeaponAimRecon), which exists precisely because Shot.Hit is false by
//     construction on a demo with no wire damage stream; it is a separate
//     field carrying a separately-labelled evidence grade, never a fallback
//     value for the measured one.
//   - Target attribution is exact in duels (Mode "duel") and a labeled
//     nearest-crosshair-enemy heuristic in team games (Mode "team"). A shot
//     is only attributed to an enemy whose position track brackets the fire
//     time and who is alive at it (dead players keep streaming position
//     samples, so a corpse would otherwise remain a candidate).
type AimResult struct {
	Players []PlayerAim `json:"players"`
	// HitsMeasured reports whether the hit-derived counters (hits, the
	// pellet full/partial/miss split, direct/splash, the LG whiff classes)
	// were measured against a wire KTX damage stream. On demos whose
	// damage section is reconstructed (or absent) the shot linker never
	// saw a wire damage event, so every per-shot Hit is false by
	// construction — the hit-derived fields are withheld there rather than
	// fabricated as zeros, and consumers should show "not measured".
	// Shots, crosshair error and LG ramp are shot/track-derived and stay
	// valid either way.
	HitsMeasured bool `json:"hitsMeasured"`

	// HitsSource names the damage evidence the hit counters were linked
	// against, from the same vocabulary as DamageResult.Source:
	//
	//   - AimHitsSourceKTX — the wire mvdhidden_dmgdone stream. HitsMeasured
	//     is true and the MEASURED counters on WeaponAim (Hits, the pellet
	//     split, direct/splash, the LG whiff classes) plus the per-fire Hit
	//     columns are populated.
	//   - AimHitsSourceReconstructed — the reconstructed damage log
	//     (damage.source == "reconstructed"). HitsMeasured stays FALSE and
	//     every measured counter stays withheld; the recovered hit counts
	//     live in their own WeaponAim.Recon block and nowhere else, so a
	//     reconstructed count can never be read as a measured one.
	//
	// Absent when the demo carried no damage section at all — which is the
	// one state HitsMeasured alone could not distinguish from a
	// reconstructed one.
	HitsSource string `json:"hitsSource,omitempty"`
}

// AimResult.HitsSource vocabulary (mirrors DamageResult.Source).
const (
	AimHitsSourceKTX           = DamageSourceKTX
	AimHitsSourceReconstructed = DamageSourceReconstructed
)

// PlayerAim holds one player's aim sub-blocks. Sub-blocks are nil when their
// inputs are absent (e.g. the rl/gl direct/splash split inside Weapons needs
// linked projectile fires, the LG whiff classes need the opt-in beam
// stream), so a non-KTX analysis still yields the crosshair/ramp blocks it
// can compute.
type PlayerAim struct {
	Player string `json:"player"`
	Team   string `json:"team,omitempty"`
	// Mode is "duel" (single enemy, exact attribution) or "team" (nearest-
	// crosshair-enemy heuristic).
	Mode string `json:"mode"`

	Crosshair *CrosshairSamples `json:"crosshair,omitempty"`
	LGRamp    *LGRampSamples    `json:"lgRamp,omitempty"`
	// Weapons is the rich per-weapon effectiveness breakdown (one entry per
	// weapon the player fired): shots/hits for all, plus weapon-specific
	// detail — SG/SSG pellet stats, RL/GL direct/splash, LG whiff geometry.
	// Deliberately an ORDERED array keyed by the entry's Weapon field (sorted
	// by aimWeaponRank), not a map: the weapon order is meaningful here, unlike
	// the unordered byWeapon count maps in FragResult/DamageResult.
	Weapons []WeaponAim `json:"weapons,omitempty"`
}

// CrosshairSamples is the columnar per-hitscan-fire crosshair error to the
// attributed target. All slices share one index. DYaw/DPitch are signed
// degrees — positive DYaw = target LEFT of the crosshair (Quake yaw grows
// counterclockwise; DYaw is target bearing − aim yaw), positive DPitch =
// target above; NYaw/NPitch are those divided by the target's
// angular half-width/half-height at Dist. Dist is the muzzle→target-center
// distance in Quake units (the shot traces from the weapon muzzle,
// ≈ origin+16 — not the +22 eye; see analyzer/aim.go). Team flags samples whose attributed target is a
// teammate (a hit's confirmed victim — misses attribute to enemies only);
// nil when no sample is team-attributed. Self targets cannot occur here:
// the samples are hitscan-only and a hitscan trace cannot hit its shooter.
type CrosshairSamples struct {
	T      []int32   `json:"t"`
	Weapon []string  `json:"w"`
	DYaw   []float32 `json:"dyaw"`
	DPitch []float32 `json:"dpitch"`
	NYaw   []float32 `json:"nyaw"`
	NPitch []float32 `json:"npitch"`
	Dist   []float32 `json:"dist"`
	// Hit is omitted entirely when AimResult.HitsMeasured is false — a
	// per-fire false there would be a fabricated miss, not a measurement.
	Hit    []bool   `json:"hit,omitempty"`
	Target []string `json:"tgt"`
	Team   []bool   `json:"team,omitempty"`
}

// LGRampSamples is the columnar per-LG-fire "ramp onto target" series. Since
// is milliseconds since the start of the shaft the fire belongs to (fires
// less than the shaft-gap apart are one shaft). Hit shares the index. Team
// flags fires that connected but hit no enemy (teammate-only victims); nil
// when none.
type LGRampSamples struct {
	Since []int32 `json:"since"`
	// Hit is omitted when AimResult.HitsMeasured is false (see
	// CrosshairSamples.Hit).
	Hit  []bool `json:"hit,omitempty"`
	Team []bool `json:"team,omitempty"`
}

// WeaponAim is one weapon's effectiveness for a player. Shots (fires) and Hits
// (fires that connected) are populated for every weapon; the rest are
// weapon-specific and omitempty.
//
//   - SG/SSG (per-pellet): Pellets fired (shots × 6/14), PelletHits (Σ damage
//     / 4 — matches KTX acc.hits), and the per-fire split Full (all pellets
//     hit) / Partial (some) / Miss (none).
//   - RL/GL: Direct (non-splash contacts ≈ KTX hits), Splash (linked hits that
//     were splash-only), Missed (fires that linked to no impact);
//     Direct+Splash+Missed == Shots. Projectile linking runs on every parse;
//     the block is present whenever any rl/gl fire linked to its flight
//     (absent only when nothing linked — e.g. non-KTX demos).
//   - LG: of the missed fires, Blocked (the beam stopped short of its ~600-
//     unit max length on geometry and its extension to full range crosses a
//     live enemy's collision hull — on target and in range, the obstruction
//     denied a would-be hit) vs OutOfRange (the beam ran its full length and
//     its extension to infinity crosses a live enemy's hull — on target, the
//     enemy was beyond reach) vs Miss (every other whiff — an aim error, no
//     enemy on the beam's line; reuses the Miss field, which is per-pellet
//     for SG/SSG) vs Unresolved (no beam matched). Present only when the
//     beam stream was built.
type WeaponAim struct {
	Weapon string `json:"weapon"`
	Shots  int    `json:"shots"`
	// Hits is omitted (with every other hit-derived counter) when the demo
	// carried no wire damage stream to link fires against — see
	// AimResult.HitsMeasured. When HitsMeasured is true an absent hits
	// means a measured zero.
	Hits int `json:"hits,omitempty"`

	// Enemy/Team/Self slice the hit counters by victim class (see
	// Shot.VictimKinds); a multi-victim fire counts in every bucket it has a
	// victim in. Emission: Team/Self appear iff the weapon had ≥1 team-/self-
	// victim hit; Enemy appears iff Team or Self does (i.e. iff it differs
	// from the top-level counters — consumers fall back to those otherwise).
	// Shots/Pellets and the LG miss classes are not split: misses have no
	// victim (the miss heuristic targets enemies by construction). Per bucket,
	// splash = hits − direct and missed = shots − hits.
	Enemy *WeaponAimSplit `json:"enemy,omitempty"`
	Team  *WeaponAimSplit `json:"team,omitempty"`
	Self  *WeaponAimSplit `json:"self,omitempty"`

	// Recon is the RECONSTRUCTED hit tier — present only when
	// AimResult.HitsSource is AimHitsSourceReconstructed, and never
	// alongside the measured counters above. See WeaponAimRecon.
	Recon *WeaponAimRecon `json:"recon,omitempty"`

	Pellets    int `json:"pellets,omitempty"`
	PelletHits int `json:"pelletHits,omitempty"`
	Full       int `json:"full,omitempty"`
	Partial    int `json:"partial,omitempty"`
	Miss       int `json:"miss,omitempty"` // SG/SSG: zero-pellet fires; LG: aim-error misses (neither blocked nor out of range)

	Direct int `json:"direct,omitempty"`
	Splash int `json:"splash,omitempty"`
	Missed int `json:"missed,omitempty"`

	Blocked    int `json:"blocked,omitempty"`
	OutOfRange int `json:"outOfRange,omitempty"`
	Unresolved int `json:"unresolved,omitempty"`
}

// WeaponAimRecon is one weapon's hit count recovered from the RECONSTRUCTED
// damage log (damage.source == "reconstructed") by re-running the fire→damage
// join against it. It is a separate, separately-named tier on purpose: the
// underlying evidence is inference, not measurement, so it must never merge
// into the measured counters a consumer reads off WeaponAim.
//
// What it carries: Hits — fires of this weapon that the reconstruction says
// connected — beside the weapon's measurement-grade Shots, so accuracy is
// hits/shots. Nothing else. The reconstruction anchors damage at the VICTIM's
// health/armor stat instant and merges every hit landing on one instant into
// a single delta, which is enough to count a fire as connected but not to
// carry:
//
//   - per-fire Hit flags (CrosshairSamples.Hit / LGRampSamples.Hit) — one
//     misjoined fire is a visible wrong dot on the heatmap, and a
//     multi-attacker merge silently moves a hit between shooters;
//   - the SG/SSG pellet split (PelletHits / Full / Partial / Miss) — a merged
//     delta's magnitude is the sum over every hit on that instant, so Σ/4
//     would credit one shooter with another's pellets;
//   - RL/GL Direct vs Splash — the reconstruction's IsSplash is a
//     damage-model verdict, not the server's own contact flag;
//   - the LG whiff geometry (Blocked / OutOfRange / Unresolved) — it
//     classifies MISSES, and a miss here can be a hit the join did not
//     recover;
//   - the enemy/team/self splits — team and self attribution is the weakest
//     part of the reconstruction (damagerecon/ACCURACY.md).
//
// Emitted only for the weapons whose damage lands in the fire's own server
// frame — lg, sg, ssg and the axe (at its fixed +200 ms traceline delay) —
// where the join was validated exact against the wire log. rl/gl/ng/sng carry
// NO block: their fire→impact association needs the projectile-flight bracket
// the shots analyzer discards, and counting impacts instead reads ~7 points
// above the measured rl convention. An absent block is "not recovered for this
// weapon", never "no hits".
//
// Presence is keyed on the damage SECTION being reconstructed and this player
// having FIRED the weapon — never on him appearing in the reconstructed log. A
// shooter the reconstruction credits with no damage at all gets Hits: 0 on
// every covered weapon he fired, because "fired ten shells, linked nothing" is
// a supported reading and must not be published as the same absence a withheld
// weapon gets.
//
// Accuracy measured against the wire log on demos that carry both:
// damagerecon/ACCURACY.md §"Aim hit recovery" (lg mean 0.3pp, sg 1.3pp,
// ssg 1.7pp, axe 0.5pp of accuracy error vs the measured counter).
type WeaponAimRecon struct {
	// Hits is the reconstructed count of fires that connected. A zero inside
	// a present block is a real "linked nothing", not an absence — the block
	// itself is the presence signal, and it is emitted only for the weapons
	// whose recovery was validated (see the ACCURACY.md table).
	Hits int `json:"hits"`
}

// WeaponAimSplit is one victim-class slice (enemy / team / self) of a
// weapon's hit counters — same semantics as the WeaponAim fields of the same
// names, restricted to that bucket's victims. The SG/SSG per-fire split
// (Full/Partial/Miss) and PelletHits are exact per fire except when the
// per-fire pellet clamp triggers (e.g. quad-multiplied damage), where the
// enemy/team allocation within that fire is approximate. Self hits are
// always splash (a missile cannot collide with its owner), so a Self split
// never sets Direct and never has pellet counters (hitscan cannot self-hit).
type WeaponAimSplit struct {
	Hits       int `json:"hits,omitempty"`
	PelletHits int `json:"pelletHits,omitempty"`
	Full       int `json:"full,omitempty"`
	Partial    int `json:"partial,omitempty"`
	Miss       int `json:"miss,omitempty"`
	Direct     int `json:"direct,omitempty"`
}
