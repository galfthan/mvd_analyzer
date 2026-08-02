# identity analyser

**Phase:** Core (registered right after `demoinfo`)
**Inputs:** `UserInfoEvent`, `PlayerRejoinEvent`
**Writes to Result:** nothing
**Writes to CoreOutputs:** `co.Sessions` + the `co.SlotIdentityAt(slot, tMs)` resolver it backs

## What it does

Reconstructs player identity across reconnects so per-player outputs
aren't mislabelled. A player who disconnects and reconnects mid-match
gets a *new* wire slot (and a new userid); the slot they vacated is often
reused by someone else or stamped with a late userinfo name. The old
slot→final-name resolution (`co.Slots`) then relabels the player's
pre-reconnect events with the wrong name. KTX itself unifies the player
via its ghost mechanism (`MakeGhost` snapshots the departing player at
`ktx/src/client.c:2729-2799`, and the next connection with the same netname
restores it at `:1464-1490`); this analyser reproduces that unification.

## How it works

1. **Sessions (during the event pass).** Each `UserInfoEvent` opens /
   continues / rotates a per-slot *session* (one contiguous occupancy by
   a single userid), via the shared [`occupancyTracker`](occupancy.go).
   A new session opens when a slot's userid changes; a plain name change
   with the same userid stays one session (a rename, which final-name
   resolution already handled correctly). The server's drop broadcast
   (`UserInfoEvent.Vacated` — an empty userinfo string carrying the
   client's own userid) also ends a session, so a player who times out
   stops owning the slot at the moment they left rather than when the
   next connection lands on it. A drop is final: nothing on the wire
   re-broadcasts a dropped client's userinfo, and an `svc_setinfo`
   synthesis that looks like it (`UserInfoEvent.Partial`) is not an
   occupancy boundary at all. Scalars are copied off `e.Player` — the
   parser mutates that struct in place on the next occupancy
   (`mvd-reader/parser/userinfo.go`, `parseUserInfo`).
2. **Reconnect broadcasts.** `PlayerRejoinEvent.Prefix` is recorded — the
   parser decodes the `rejoins the game with …` / `reenters the game
   without stats` family and applies the `PRINT_CHAT` exclusion, so a chat
   line quoting the marker can no longer enter the reconnect set. The
   prefix is `<name>` or `<name> [<team>]` with no delimiter, so it is
   resolved against known netnames by longest prefix in `PopulateCore`.
3. **Unification (`PopulateCore`).** Sessions are folded into canonical
   identities via union-find over four signals, in priority order:
   (1) shared nonzero `*auth` login; (2) same demoinfo player (login or
   normalized-name join, reusing the `demoinfo` index); (3) a KTX
   `rejoins`/`reenters` print for that netname; (4) bare-demo fallback —
   unify by normalized netname, *only* when there is no demoinfo, no auth
   and no reconnect print (so modern demos never over-merge two distinct
   same-name players).
4. **Output.** `co.Sessions[slot]` is the time-sorted, identity-resolved
   occupancy list. Each entry carries TWO windows: `StartMs`/`EndMs` are
   the *lookup* bounds (first session extends to -inf, last to +inf so
   edge events still resolve) and `OccStartMs`/`OccEndMs` the *observed*
   ones, which is what gets published (schema v66) — a widened bound
   would tell a consumer a connection existed before it did.
   `co.SlotIdentityAt(slot, tMs)` returns the identity that held the slot
   at `tMs`.
5. **Identity keys.** `IdentityKey` is `s<slot>u<userId>` of the group's
   first session (`identityKeys`), not a union-find array index: the key
   is exported on `streams.players[].identity`, so it is a wire fact a
   consumer can reproduce rather than a slice position. It is demo-local
   (a userid names a connection to one server) and disambiguated with an
   `@<startMs>` suffix in the one theoretical clash — a userid reissued
   to the same slot — because the streams builder GROUPS on this key.

## Who consumes it

- **items**, **weapon_pickups**, **timeline** (frag events, powerups,
  streaks) resolve each event by its own timestamp via
  `co.SlotIdentityAt`, so pre-reconnect events stay with the right player.
- **timeline streams** group per-slot builders by
  `ResolvedSession.IdentityKey`, stitching a player's two slots into one
  `PlayerStream` (and carving a slot shared by two players at the
  handover). Phantom sessions with no recorded play are dropped. Since
  v66 the same pass PUBLISHES the identity: `streams.players[].identity`
  plus a `sessions[]` list of the observed occupancy windows
  (`{startMs, endMs, slot, userId, name}`), mirrored onto
  `playerStats.players[]`. Occupancies with no userid of their own are
  withheld — a KTX ghost row is not a connection, and it would otherwise
  publish a window overlapping the slot the player really reconnected
  onto. See RESULT_SCHEMA.md § "Player identity and sessions".

## Limitations / known issues

- A reconnect with **no demoinfo, no auth, no KTX print** and a
  **different netname** each time cannot be unified — there is no signal
  linking the two names. (The bare-demo fallback only joins identical
  normalized names.)
- The bare-demo name fallback could merge two genuinely distinct players
  who share a name on an old non-KTX demo; this matches the pre-existing
  risk of the name-join in `Context.ResolveSlotDemoInfo`.

## Reference

- KTX ghost snapshot: `ktx/src/client.c:2729-2799` (`MakeGhost`)
- KTX ghost restore: `ktx/src/client.c:1464-1490`; rejoin prints `:1481`
  (team) / `:1487` (non-team); reenter-without-stats `:1502` / `:1506`
- KTX leave print: `ktx/src/client.c:2843`, `ktx/src/bot_commands.c:388`
- Wire format: [`mvd-reader/MVD_FORMAT.md`](../../mvd-reader/MVD_FORMAT.md) (search "reconnect")
