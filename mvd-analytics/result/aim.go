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
//   - SG/SSG (per-pellet): Pellets fired (shots × 6/14), PelletHits, and the
//     per-fire split Full (all pellets hit) / Partial (some) / Miss (none).
//     PelletHits is ESTIMATED from magnitude — a fire's same-frame damage sum
//     divided by the 4 a pellet does, or by 16 while the shooter holds the
//     quad (T_Damage's ×4, ktx/src/combat.c:540-546), clamped to the fire's
//     6/14. On the 186-demo archive eval that estimate reproduces KTX's own
//     acc.hits EXACTLY on 534 of 534 sg and 390 of 390 player rows
//     (damagerecon/ACCURACY.md); the quad divisor is what closed it, and
//     before it every quad row over-counted. Two per-fire pellet counts it
//     does not model — k_instagib's single-slug sg and k_yawnmode's 21-pellet
//     ssg — are stated, not guarded (see analyzer.deriveMeasuredAcc).
//
//   - RL/GL: Direct (projectiles that TOUCHED a player — KTX's own rl/gl hits
//     counter, incremented in the touch handler and nowhere else,
//     ktx/src/weapons.c:994 and :1329), Splash (linked hits that were
//     splash-only), Missed (fires that linked to no impact). Projectile
//     linking runs on every parse; the block is present whenever any rl/gl
//     fire linked to its flight (absent only when nothing linked — e.g.
//     non-KTX demos).
//
//     Direct and Splash are POINTERS for that reason: nil is "not
//     classified", a present zero is a measured "touched nobody". The two
//     are different claims and a consumer reading a bare 0 could not tell
//     them apart — the row's own presence is the signal, not a global latch
//     somewhere else in the Result.
//
//     The two weapons reach Direct from DIFFERENT evidence, and it matters
//     to a reader of the number. rl's is the wire's own splash flag: a direct
//     T_MissileTouch writes its damage as an unflagged row, dmg_is_splash
//     being raised only inside T_RadiusDamage (ktx/src/combat.c:1207), so
//     Direct is a subset of the fires that connected and
//     Direct+Splash+Missed == Shots. gl's cannot be — GrenadeTouch does ALL
//     its damage through T_RadiusDamage, so every gl row on the wire is
//     splash-flagged whatever the grenade hit — and is instead the
//     flight-geometry touch classifier's count (damagerecon/direct.go, fed
//     the wire rows by damagerecon.WireDirectTouches; the same classifier a
//     reconstructed demo's recon.directHits uses). Two consequences:
//     gl's Direct is bounded by the FIRES rather than by Hits, so
//     Direct+Splash+Missed may exceed Shots and Splash floors at zero where
//     a touch's fire went unlinked; and it is WITHHELD — no Direct, no
//     Splash — on a parse without the spatial shot streams the geometry
//     needs (Registry.BuildShotStreams; mvd-api and the WASM build always
//     request them). Measured against the verbatim KTX block on 186 archive
//     demos: rl 99.8% of 632 player rows exact at 0.02% aggregate, gl 92.0%
//     of 424 at 3.79% (damagerecon/ACCURACY.md).
//
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
	// splash = hits − direct and missed = shots − hits — for rl; a gl bucket's
	// direct is a touch count that its hits does not bound (see the type
	// comment), so there splash is the floor-at-zero remainder.
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

	// Direct is the projectile-TOUCH count, Splash the linked hits that were
	// splash-only and Missed the fires that linked to nothing. Read the type
	// comment before comparing a gl Direct with an rl one: rl's comes off the
	// wire's splash flag and partitions its fires with Splash/Missed, gl's
	// comes off the flight-geometry classifier and is bounded by the fires
	// alone.
	//
	// NIL IS NOT ZERO. Both are set together, exactly when the split RAN for
	// this weapon, and are nil when it did not — so nil means "this parse did
	// not classify these fires" and a present 0 means "classified, and none
	// of them touched / none of the hits were splash-only". Two reachable nil
	// cases, and a consumer must render them as withheld rather than as a
	// zero: gl on a parse that built no spatial shot streams (the classifier's
	// own input — mvd-api and the WASM build always request them, a bare
	// qw-analyze parse does not), and rl or gl on a demo where the linker
	// resolved no rl/gl fire at all, where Hits is likewise 0 and a Direct of
	// 0 would be indistinguishable from a measurement. Missed does NOT ride
	// the split: it is shots − hits, which the linker answers on its own.
	Direct *int `json:"direct,omitempty"`
	Splash *int `json:"splash,omitempty"`
	Missed int  `json:"missed,omitempty"`

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
//   - the RL/GL Direct / Splash / Missed per-fire split — DirectHits below
//     carries the direct-impact COUNT, which is a different (and separately
//     validated) claim from splitting each fire three ways;
//   - the LG whiff geometry (Blocked / OutOfRange / Unresolved) — it
//     classifies MISSES, and a miss here can be a hit the join did not
//     recover;
//   - the enemy/team/self splits — team and self attribution is the weakest
//     part of the reconstruction (damagerecon/ACCURACY.md).
//
// Emitted for two weapon families. The same-frame weapons — lg, sg, ssg and
// the axe (at its fixed +200 ms traceline delay) — link a fire to damage in
// the fire's own server frame, where the join was validated exact against the
// wire log. rl and gl link through the fire's TRACKED FLIGHT (Shot.FlightEnd,
// schema v74): the flight's impact instant is joined to the reconstructed
// damage there, and a fire whose projectile was never tracked counts as a miss
// — the measured counter's own definition, which is why the two are
// comparable. Before v74 published that association the join could only count
// impacts, a different question that read ~7 points above the measured rl
// convention.
//
// ng/sng carry NO block: nail flights are bracketed only when nail decoding is
// enabled, so their measured counter is zero on every validation row and there
// is nothing to check a recovery against. An absent block is "not recovered for
// this weapon", never "no hits".
//
// Presence is keyed on the damage SECTION being reconstructed and this player
// having FIRED the weapon — never on him appearing in the reconstructed log. A
// shooter the reconstruction credits with no damage at all gets Hits: 0 on
// every covered weapon he fired, because "fired ten shells, linked nothing" is
// a supported reading and must not be published as the same absence a withheld
// weapon gets.
//
// Accuracy measured against the wire log on demos that carry both:
// damagerecon/ACCURACY.md §"Aim hit recovery" (mean accuracy error vs the
// measured counter: lg 0.3pp, sg 1.3pp, ssg 1.8pp, axe 0.6pp, rl 0.5pp,
// gl 0.4pp).
type WeaponAimRecon struct {
	// Hits is the reconstructed count of fires that connected. A zero inside
	// a present block is a real "linked nothing", not an absence — the block
	// itself is the presence signal, and it is emitted only for the weapons
	// whose recovery was validated (see the ACCURACY.md table).
	//
	// It counts a fire that landed damage by ANY path, splash included —
	// HitsAnyDamage in the PlayerStatsAcc.HitsConvention vocabulary.
	Hits int `json:"hits"`

	// DirectHits (schema v74) is the count of this player's rl (or gl)
	// projectiles the reconstruction says TOUCHED somebody —
	// HitsDirectImpact, the convention KTX's own `acc.rl.hits` /
	// `acc.gl.hits` counts, because KTX increments that counter in the touch
	// handler and nowhere else (ktx/src/weapons.c:990-996, :1327-1333).
	// Publishing it is what lets a pre-instrumentation demo answer the same
	// question a KTX demoinfo block answers, so the two eras can be compared
	// at all; `playerStats.accuracy.byWeapon[rl|gl].hits` on a reconstructed
	// row IS this number.
	//
	// NOT a subset of Hits, and NOT the same join. Hits asks whether a FIRE
	// connected and answers it through the fire's tracked flight, so a
	// point-blank rocket whose entity the server never broadcast is a miss
	// there — the measured counter's own definition. A touch has no such
	// notion: one projectile touches at most one player, so a touch simply IS
	// a non-splash damage row, and DirectHits counts those rows
	// (aimcore.ReconDirectHits). DirectHits > Hits is therefore possible and
	// meaningful — it is exactly the untracked-flight population — and
	// routing this count through the flight join instead measured 9.5%
	// aggregate error against the verbatim block where the row count runs
	// 0.65% (damagerecon/ACCURACY.md).
	//
	// What a consumer CAN rely on: DirectHits <= Shots (the publisher clamps
	// it, since a fire cannot touch twice), both numbers scope to the same
	// query window, and both are absent together with the block. Nothing
	// orders it against Hits.
	//
	// PRESENT ONLY FOR rl AND gl. For every other weapon the two conventions
	// coincide (KTX counts any connecting lg/axe/ng/sng fire) and a separate
	// field would only invite a spurious distinction. Absent is therefore
	// "the question does not arise here", never "no direct hits".
	//
	// The evidence is damagerecon's per-explosion direct/splash verdict
	// (damagerecon/direct.go: the flight's trajectory against the victim's
	// hull, the flat-110 magnitude prior on a server whose constant the demo
	// established, and the spent grenade fuse as a certain non-touch).
	// Validated against the verbatim KTX block in damagerecon/ACCURACY.md
	// §"Can an old demo answer KTX's rl/gl question?".
	DirectHits *int `json:"directHits,omitempty"`
}

// WeaponAimSplit is one victim-class slice (enemy / team / self) of a
// weapon's hit counters — same semantics as the WeaponAim fields of the same
// names, restricted to that bucket's victims. The SG/SSG per-fire split
// (Full/Partial/Miss) and PelletHits are exact per fire except when the
// per-fire pellet clamp triggers, where the enemy/team allocation within that
// fire is approximate — the estimator divides the fire's damage sum by the 4
// a pellet does (16 under the shooter's quad) and cannot tell a saturating
// fire's victims apart any finer. Self hits are
// always splash (a missile cannot collide with its owner), so a Self split's
// Direct is a certain zero and it never has pellet counters (hitscan cannot
// self-hit).
type WeaponAimSplit struct {
	Hits       int `json:"hits,omitempty"`
	PelletHits int `json:"pelletHits,omitempty"`
	Full       int `json:"full,omitempty"`
	Partial    int `json:"partial,omitempty"`
	Miss       int `json:"miss,omitempty"`
	// Direct carries WeaponAim.Direct's nil-is-not-zero rule into the bucket:
	// nil where the split did not run for this weapon, a present 0 where it
	// ran and this bucket's victims were all reached by splash. The consumer
	// derives the bucket's splash as hits − direct, which nil forbids and 0
	// permits.
	Direct *int `json:"direct,omitempty"`
}
