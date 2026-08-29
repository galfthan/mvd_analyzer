# player-stats post-processor

**Phase:** Post-processor (non-event)
**Inputs (artifacts):** `clock`, `identity`, `roster`, `timeline` (for
            `Streams`), `match:final`, `frags:final`, `damage:final`,
            `shots`, `items`, `weapon-pickups`, `backpacks:final`,
            `metadata`, `aim`
            — `identity` supplies the `*auth` login, `shots` the fire
            stream the derived accuracy is reconstructed from, and `aim`
            both hit tiers: the reconstructed one that fills
            `accuracy.hits` on demos with no wire damage stream, and
            (v75) the measured pellet / direct-impact counters the
            wire-linked branch publishes on the demos that have one
**Writes to Result:** `result.PlayerStats` (`*PlayerStatsResult`),
            schema v75

## What it does

Produces the canonical per-player (and per-team) statistics row, for
**every** demo — no KTX demoinfo block required.

Two things motivate it. First, the "how did this player do" answer was
scattered: the corrected scoreboard in `match`, team-kills in `frags`,
damage in `damage`, pickups across `items` / `weaponPickups` /
`backpacks`, and the KTX-only fields in `demoInfo` — so every consumer
re-implemented the join, differently. Second, none of them carry
**possession time**, and neither does KTX's block.

The node produces the fully **derived** form. `view.PlayerStats`
overlays KTX at read time where KTX is the better source, mirroring how
`view.Damage` applies `boundedSource` — so the stored artifact and the
golden corpus always say what this pipeline actually computed.

## How it works

1. **Windows.** `presenceWindow` reads the player's first/last activity
   (position track, else spawn/death markers, else the whole match).
   `aliveIntervals` converts the spawn/death markers into alive
   intervals; it **is** the repo's canonical liveness rule, and since
   v64 the same function also produces the stored
   `PlayerStream.Alive` that LOS, aim, loc-graph, region control and
   `/loc-trails` read (`timeline_finalize.go` `deriveAliveIntervals`).
   The two rival copies — `analyzer.losAliveAt` and `aimcore`'s
   `aimAliveAt` — used a strict `lastSpawn > lastDeath`, which reports
   a death and the spawn it triggers on the same millisecond as DEAD
   and then latches there for the rest of the life. That tie-break is
   wrong (instant respawn means alive), so v64 deleted `losAliveAt`,
   rewrote `aimAliveAt` into a reader over `Alive`, and moved the LOS
   and aim figures accordingly. `view.playerActiveInWindow` (the
   `/buckets` `alive` mask) stays separate on purpose: it is a
   window-overlap test, a different question, and it already resolves
   the tie correctly.
   Alive is then intersected with presence, which is what stops a late
   joiner being credited alive time from match start (the liveness rule
   says "no death yet ⇒ alive since match start", right for a player
   who was there, wrong for one who had not connected). That clip is
   `presenceWindow` (raw first/last activity), while the stored
   `Alive` clips to `result.TrackHoldEnd` widened by marker evidence,
   so `window.aliveMs` and the summed durations of
   `streams.players[].alive` can differ by up to ~250 ms per player.
   Known and transitional; documented in RESULT_SCHEMA.md.
2. **Hold.** Each possession stream is merged, clipped to
   `[0, matchEnd]`, and intersected with the alive intervals; `ms`,
   `runs` and `longestMs` fall out of the resulting interval list.
   The armor `ArmorType` change stream is first run-length-converted
   into per-type intervals (`armorRuns`); `armor.none` is the
   alive-time complement.
3. **Score / damage.** Read from `match:final` (frag-log-corrected
   kills/deaths/suicides), `frags:final` (team-kills, and the kill
   stream `spree.go` replays into `maxSpree` / `maxQuadSpree`) and the damage
   reconstruction's **bounded** family — bounded is KTX-scoreboard
   semantics, which is what the KTX overlay replaces it with.
   `takenEnemy` is re-summed from the per-hit log over enemy hits only
   (KTX's `dmg_t` is enemy-only, `ktx/src/combat.c:1069`, while our
   `taken` counts every source), with enemy telefrags and stomps folded
   back in since they live outside `Events` but do count in KTX's total;
   `takenToDie` is that divided by the corrected death count.
4. **Accuracy / login.** Accuracy from the fire stream, login from the
   `*auth` userinfo key — both so a demo with no KTX block degrades to a
   rougher number rather than to a missing field. On a demo whose damage
   section is itself reconstructed, `hits` is lifted from the published
   `aim` recon tier and the family says `src: "reconstructed"`; reading
   the tier rather than re-running its join is what makes its
   weapon-level withholds (`ng`/`sng`) inherit here by construction.
5. **Pickups.** Non-weapon kinds from the item timeline; weapons from
   `weaponPickups` (a weapon can also arrive in a backpack, which the
   item timeline never sees); `dropped` from `backpacks`; transfers
   joined from backpack-sourced pickups.
6. **Teams.** Summed per team, with hold shares recomputed over team
   time — never averaged from per-player shares. The `shareMatch`
   denominator counts only members who were actually in the match, so a
   scoreboard-only row (`presentMs` 0) cannot dilute it; `members` is
   published on the row so the denominator stays recoverable.

## Why our hold times differ from KTX's

KTX tracks weapon hold time internally (`ps.wpn[].time`) but
`json_weap_detail` never writes it into the demoinfo block
(`ktx/src/stats_json.c:132-217`); it reaches only the end-of-match text
tables (`ktx/src/statsTables.c:390`). **No demo of any age carries it.**

Armor hold time *is* in the block and overcounts: the clock opens at
pickup and closes only on death or on picking up a different armor type
(`ktx/src/items.c:505-522`, `ktx/src/client.c:4600`) — never when the
armor is chewed to zero by damage. Ours closes when the item bits clear,
so it reads lower. Measured on gameId 212423 (1on1, dm2): KTX `ra`
213 s vs ours 129 s, and 317 s vs 266 s for the two players.

## Transfers (`xfer` / `xferSelf`)

KTX's `xferRL`/`xferLG` (`ktx/src/items.c:2586-2615`) credit the
**dropper** when a pack containing exactly the RL (or exactly the LG) is
taken by someone on the dropper's team, in teamplay only (`isTeam()`).

- A pack holds the weapon the player was **wielding**
  (`DropBackpack`: `item->s.v.items = self->s.v.weapon`), one bit — so
  KTX's exact-equality test has no mixed-contents case to model.
- KTX has no `other != dropper` check, so re-taking your own pack counts.
  We split that into `xferSelf`; `xfer + xferSelf` reproduces KTX
  exactly. A unit test pins the decomposition and
  `TestCorpusTransferIdentity` pins the identity against KTX's own
  numbers on every teamplay demo in the golden corpus — it currently
  holds for every player and both weapons.
- The counters are **pointers**: absent means the demo carries no
  backpack hints, i.e. transfers are unobservable — a different fact
  from an observed zero.

## Limitations / known issues

- **Weapon-stay modes** (`deathmatch` 2/3/5, coop) give everyone every
  weapon from spawn, so weapon hold time reads ~100% for all players.
  That is the truth about the mode; it is not suppressed.
- **Presence** is inferred from stream activity, not from a connect /
  disconnect record — the pipeline has none. It errs in both directions.
  A player who idles out of the position stream without disconnecting
  shows a shorter `presentMs` than they were connected for; and because
  presence is a single first-to-last interval, a mid-match absence is
  **bridged** rather than excluded, so a player who disconnects and
  rejoins counts as present (and, absent a death marker, alive)
  throughout. Neither is fixable with what we have: the identity
  analyser's `Sessions` look like a presence record but their outer
  bounds are widened to ±inf so lookups always resolve
  (`identity.go:239-240`), and splitting on a position-track gap would need
  an invented threshold. On the one reconnect demo in the corpus
  (gameId 216835) there is no gap to split on anyway — the largest
  interval between position samples is 56 ms for every player.
- **The nailgun has no hold time.** `PlayerStream` carries no NG
  possession interval (only rl/lg/gl/ssg/sng), so `hold.weapons` has no
  `ng` key. KTX's own text table does track it
  (`ktx/src/statsTables.c:394`); matching that means adding the stream
  first.
- **Derived `accuracy` is only as good as the shot attribution.** With
  no KTX block the family is reconstructed from the decoded fire stream
  (`src: "derived"`), which inherits `/shots`' own attribution limits —
  on gameId 71035 one player's 552 fires are all labelled `sg`. `hits`
  is omitted entirely when nothing could count it — no wire damage
  stream AND no recovery from the aim recon tier for that weapon, or
  (v75) a nailgun row on a parse without `-include nails`, the only way
  a nail fire links to its damage — since a zero there would read as
  "shot and never hit".
- **Both tiers now answer KTX's question on every weapon but one, and
  that one is named.** Every weapon carrying `hits` also carries
  `hitsConvention` (`anyDamage` | `directImpact` | `pellets`), per
  weapon, because one `src: "ktx"` row uses all three at once (v74). v74
  put the RECONSTRUCTED tier on KTX's rl/gl direct-impact convention;
  v75 does the same for the WIRE-linked one by reading the aim section's
  measured counters — `pellets`/`pelletHits` for sg/ssg, `direct` for
  rl and gl — instead of recomputing them. Measured against the verbatim
  block on 186 instrumented archive demos (`cmd/qw-demoinfo-eval`, the
  `/measured` columns): sg/ssg `attacks` AND `hits` 100.0% row-exact at
  0.00% aggregate on both weapons, `rl.hits` 99.8% / 0.02%, `gl.hits`
  92.0% / 3.79%. The shotgun
  `hits` are an estimate — a fire's same-frame damage sum over the 4 a
  pellet does, or the 16 it does under the shooter's quad — and dividing
  by the quad state at fire time is what closed their last residual.
  `gl`'s is not a wire reading at all: KTX counts a grenade that
  TOUCHED a player (`ktx/src/weapons.c:1331`) and the touch detonates it,
  so every damage row the server writes is splash-flagged and the wire
  holds no record of the touch — counting non-splash gl rows off the wire
  reproduces 0.00% of the block's total. The touch is re-derived from the
  grenade's tracked flight, its detonation point against the victim's
  hull and the 2.5 s fuse, by the same classifier a reconstructed row
  uses (`damagerecon/direct.go`, fed the wire rows by
  `damagerecon.WireDirectTouches`) — which is why it is CONDITIONAL on
  the spatial shot streams (`Registry.BuildShotStreams`): without them
  the row falls back to the any-path count and says `anyDamage`. The
  condition is read off the AIM ROW ITSELF — `WeaponAim.Direct` is an
  `*int` and is nil exactly where the split did not run — not re-derived
  from `Streams.ShotStreamsComputed`, so the two cannot disagree about
  what was measured. `sg`/`ssg` keep `anyDamage` on a
  RECONSTRUCTED row for the mirror reason to gl's wire problem: a
  reconstructed delta merges every hit on one instant, so its magnitude
  cannot be split into pellets (`result.WeaponAimRecon`). `ng`/`sng` are
  on KTX's scale but the linker recovers only 68-77% of the nails that
  connected. See `damagerecon/ACCURACY.md` §"The wire-linked accuracy
  family vs the verbatim block".
- **`maxSpree` inherits the kill side's residual.** The streak replay is
  exact where the underlying kill attribution is (99.6% of rows whose
  `kills` already agrees with KTX and whose player never suicided); 16 of
  the 17 large disagreements in the eval sat on a row the frag log had
  already credited 0 kills against KTX's 8-47, which is the observable
  signature to read it by — `kills: 0` beside a large positive `frags`
  means inherited-unknown, not a measured zero. Suicide rows read exactly
  1 low by design — see RESULT_SCHEMA "The derived spree".
- **`ping`, `handicap` and `bot` stay KTX-only.** `handicap` and `bot`
  are server-side state with no wire signal. `ping` IS on the wire
  (`svc_updateping`) but the parser skips it
  (`mvd-reader/parser/parser.go:809`, `skipCommand`) — decoding it is a Layer-1 change
  left for a follow-up.

## Reference

- Schema + field docs: `result/player_stats.go`,
  [`RESULT_SCHEMA.md`](../RESULT_SCHEMA.md#playerstatsresult-playerstats)
- KTX ground truth: `ktx/src/stats_json.c`, `ktx/src/items.c`,
  `ktx/src/client.c`, `ktx/src/statsTables.c`
