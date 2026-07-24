# Known issues and accepted limitations

Deliberate, bounded gaps in the parser's derived events. Each entry says
what is missed, why, the blast radius, and the fix shape if it ever
matters. See MVD_FORMAT.md for the derived-event model itself.

## Death detection: sampled state vs transactional obituaries

`DeathEvent` has two sources (`parser/stats.go`):

- **Stat edges (primary).** Alive→dead transitions in the sampled wire
  state (`STAT_HEALTH` ≤ 0, `DF_DEAD` in `svc_playerinfo`), deduplicated
  across the two encodings (`maybeEmitDeath`).
- **Obituary backstop.** KTX broadcasts exactly one obit line per
  server-side `deaths++` (ktx/src/client.c), and prints cannot be lost
  to frame sampling. `tryEmitObituaryDeath` (parser/print.go) resolves
  the victim via the canonical obituary table (`parser/obituary.go`) and
  force-emits, bypassing the dedup (`forceEmitDeath`).

The backstop exists because sampling can collapse fast sequences: a
die–respawn–die cycle inside one ~25–40 ms inter-frame gap shows no
alive edge for the second death, and a pent-deflect telefrag (dtTELE2)
kills the telefragger with a zero-frame alive interval. In both, the
obit is the only wire evidence.

### Gap: " ate N loads of …" obituaries are invisible to the backstop

The SSG/rocket splash forms ("X ate 2 loads of Y's buckshot") are a
mini-grammar (variable count, killer embedded between anchors), not a
marker→classification row, so they have no entry in the canonical table
and `FindObituaryVictim` cannot extract their victims. Analytics still
classifies these kills fully (`analyzer/obituary_parse.go` `matchAte`) —
the frag log, scoreboard, and damage attribution are unaffected.

**Blast radius:** only the corroborating death *marker*, and only when
such a kill also lands inside one of the sampling corners above — a
sub-frame alive-mask blip / one missing `death` row in the timeline
views. Never observed in the golden corpus.

**Fix shape if ever needed:** a victim-scan-only pattern kind (~10
lines), kept out today because a generic `" ate "` kill row would leak
into the analytics kill matchers the table now feeds.

### Covered: killer-first forms

"X stomps Y" / "X squishes Y" / "X rips Y a new one" victims are
extracted (suffix-bounded) since the killer-first scan was added to
`FindObituaryVictim`. `ObituaryTeamkillKiller` forms remain excluded by
design — their victim is the generic "teammate", not a name.

## Consumer note

Downstream, `Spawns`/`Deaths` are independent timestamp lists with
hold-last consumers — unpaired sequences (death–death across a missed
spawn) are well-defined, so a missed backstop degrades by one marker,
never corrupts.
