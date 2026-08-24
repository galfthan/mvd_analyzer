# Plan: archive data features (post-damage-reconstruction)

Successor branch to `reconstruct-damage` (PR #132) — branch off `main`
after that PR merges. Everything here is evidence-backed by the archive
data survey (`.reports/qw-archive-data-survey-2026-08-16/REPORT.md`,
405-demo stratified + 200-demo uniform samples over the 51k-demo
archive) and deliberately **out of scope** for the damage PR.

Population shorthand: eras E0 (qwsv/KTPro, 27.5% of archive) … E5
(MVDSV 1.x, 24.9%); `ktxver` is the sharp feature gate, not `*version`
(boundaries: `//ktx drop` @1.38, dmgdone @1.40, ktxstats @1.41; forks
lie about their version).

## 1. Wall-clock anchors: parse `matchdate:` + `matchkey:` (coverage 24% → ~98%)

**SHIPPED** on `archive-parsing` (schema v72): the `wall-clock` DAG node
(`mvd-analytics/analyzer/wallclock.go`) parses both `matchdate:` layouts,
`matchkey:` and the ktxstats `date`, resolves timezones, and grades each
anchor `exact` / `unverified` / `contradicted` on the contradiction rules
below. Design decision taken: a new `streams.global.matchStartUnixMs`
(plus source/accuracy/confidence/note and `dateMarkers[]`), with
`demoStartUnixMs` back-shifted from an uncontradicted marker by
`demoOffset` so the existing wall-clock formula keeps working —
documented in RESULT_SCHEMA.md. Measured on a 260-demo stratified sample,
archive-weighted: **24.8% → 94.9%** (matchkey covers 82% of the dateless
demos; the rest carry a non-date matchkey variant, e.g. `9-195923-1626`).
`matchkey` turned out to live on the same level-2 broadcast print channel
as `matchdate`, often split across three svc_print fragments. The
remaining follow-up from this lead is format C (`Date....:` stats block).

UPDATE 2026-08-17 (51k readability sweep + community intel from the
#mvd-analyzer Discord, niomic/vikpe): `matchdate:` alone only reaches
~72% of the archive and misses HALF of the reconstructed-era demos —
but old demos carry **`matchkey: <id>-<yyyy>-<m>-<d>:<h>-<mm>-<ss>`**
instead (e.g. `matchkey: 8-2005-8-13:19-56-18` → 2005-08-13T19:56:18),
and in a 300-demo raw scan matchkey (29%) and matchdate (69%) were
PERFECTLY complementary: 98% of demos carry one or the other. Also on
the wire: `sinfoset` (20%) and an `epoch` key on modern demos.
Timezone reality from vikpe's collected cases: Euro abbreviations map
cleanly (EEST +03, CEST +02, CET +01, UTC), numeric offsets (`+03`,
`-05:00`), Swedish locale strings ("Västeuropa, sommartid" +02 /
"normaltid" +01), missing tz → assume UTC. TRUST GATING is mandatory but must gate on CONTRADICTION, never on
the date value (2000 is a live QW year): the hard check is
software-release lower bounds — a `*version`/`ktxver` binary cannot
predate its release, so a mid-2000s MVDSV stamping 2000-01-07 is
provably unset-clock while the same date on QW 2.30/2.40 is fine
(needs a small release-date table for observed version strings).
Softer signals only downgrade confidence in combination: the
epoch-reset signature (a batch counting up from a boot default like
2000-01-01 — observed as a whole 2000-01-07 "night"; alone it is
indistinguishable from a real LAN night), cross-marker disagreement
within a demo (matchdate vs matchkey vs ktxstats vs finalscores; file
mtime as a weak upper bound), and known-off sources (qhlan
matchkeys). Stuffed values also get corrupted outright ("timelimit"
changed to "Final Score is 47 - 9"). Output design: never drop an
anchor — emit it with a confidence grade (exact / unverified /
contradicted) naming the failed check.

KTX broadcasts `matchdate: <stamp>` at match start on 70% of the
archive (100% of E3–E5, 60% of E2) — currently discarded because
`MessagesAnalyzer.handlePrint` routes level-2 prints to the obituary
parser only (`analyzer/messages.go:69-75`). Two strftime layouts:

- A: `matchdate: 2008-01-05 20:05:38 CET` (ISO, E2+)
- B: `matchdate: Mon Jul 03, 01:01:14 2006` (ctime, E0–E2)

Design decision to make explicitly: `DemoStartUnixMs` is documented as
demo-open time, and `matchdate:` fires at match start (+countdown), so
either back-shift by `Streams.Global.DemoOffset` or add an honestly
named `matchStartUnixMs`. Traps recorded in the survey: `%Z` is a local
abbreviation (treat as offset-unknown); `Build date:` is the mvdsv
binary stamp, not a match date; the `serverdemo` filename date is
genuinely ambiguous (`%d%m%y` vs `%y%m%d` both observed) — never parse
it without a print-derived cross-check. Follow-ups in the same theme:
parse the ktxstats `date` string (`%Y-%m-%d %H:%M:%S %z`, 45% of
archive, currently passed through as raw text) into a match-END
instant, and consider format C (`Date....:` stats block) for +28%.

## 2. Backpack reconstruction on pre-KTX-1.38 demos (backpacks 50% → ~95%)

**SHIPPED** on `archive-parsing` (folded into the unreleased schema v72):
the `backpack-recon` post (`mvd-analytics/analyzer/backpack_recon.go`)
fills the same `backpacks` section on demos older than the `//ktx drop`
hint, stamped `source:"reconstructed"`; hint rows now carry
`source:"ktx"`. Measured against the hints on **316 archive demos spanning
KTX 1.38-1.48** (13 749 GT drops, hints withheld): **precision 99.97%,
recall 99.97%** (LG 100/100, RL 99.96/99.96), drop-time error exactly
0 ms, position error p50 9.7 / p90 22.3 / p99 33.9 units. Hint-less
volume sanity over 551 demos across 40 server versions: 0.254 drops/death
vs the hinted population's 0.272, with an independent `STAT_ITEMS`
inventory oracle agreeing on 13 488 / 13 488 drops. Numbers, method and
the `cmd/qw-backpack-eval` reproduction command live in
`mvd-analytics/analyzer/BACKPACKS.md`.

What the original sketch below got wrong, from reading the KTX source:
`DropBackpack` under the shipped default `k_frp 0` packs the victim's
**wielded** weapon verbatim (`item->s.v.items = self->s.v.weapon`,
`items.c:2706`) — the items-bits-plus-priority-order rule only applies
under fairpacks 1 — and the wielded weapon is on the wire as
`STAT_ACTIVEWEAPON` (`mvdsv/src/sv_send.c:1268`). That made the enabler
a new `streams.players[].aw` change stream (opt-in field code `aw`),
NOT `ItemStateEvent.Origin`, which turned out unnecessary: the drop
origin is the victim's own position track. The mode caveat was also
wrong — there is no midair/smashpack/extinction/wipeout/CA early return
in `items.c` (no `smashpack` at all), and the GT run confirms packs drop
in those modes, so `backpackSkipModeReason` is deliberately NARROWER than
`damagerecon.SkipModeReason`: only `k_bloodfest`, `k_yawnmode` and a
non-default `k_frp` (read off KTX's `Fairpacks setting:` broadcast, now
parsed into `metadata.matchSettings.fairpacks`). `dtSUICIDE` is likewise
only the `/kill` command, not self-inflicted death in general.

Residuals: `dp 0` has no wire signal on a pre-1.38 demo; pre-KTX
(qwsv/KTPro) drop rules cannot be GT-validated by construction, though
their rate and inventory consistency match the KTX population. The pickup
side is no longer a residual — lead 10 below closed it off the entity
track — but pack-TRANSFER credit still is, because it needs the picker's
`hadBefore`, which no wire signal carries.

Original note: The `//ktx drop` hint exists only ≥KTX 1.38; the wire entity
stream cannot substitute as-is (bp edicts are recycled so `ItemSpawnEvent`
is sticky-suppressed, and `ItemStateEvent` carries no origin — measured
16.4 visibility flips per real drop).

## 3. Parse `//finalscores` (62% of archive, two eras before ktxstats)

**SHIPPED** on `archive-parsing` (folded into the unreleased schema v72):
`parser/ktx_finalscores.go` decodes the directive into a typed
`FinalScoresEvent`, `metadata` publishes it verbatim as
`metadata.finalScores`, and `match` reads it back for the map, mode and
team rows nothing else answered — new `match.mode` plus a `match.sources`
provenance block, never displacing a demoinfo value. The year-less date
joins lead 1's markers as a corroborator only (year + timezone borrowed
from the anchoring marker, reported as `dateMarkers[].yearFrom`), and
supplies `matchEndUnixMs` where no ktxstats block exists.

Measured on 120 demoinfo-less archive demos: mode 0 → 120/120,
`matchEndUnixMs` 0 → 120/120, anchor grades unchanged (119 exact / 1
unverified). Scoreline vs the derived scoreboard: 105 exact, 7
round-scored modes (CA/Wipeout score ROUNDS, `commands.c:6867-6886`), 3
match-ending frags the scoreboard freeze excludes, 5 duel label
differences. On 60 demos that also carry demoinfo, every resolved field
still came from `ktx`. A 12 000-demo scan found exactly one directive per
demo and no malformed copy — the parser's shape checks (which drop a
garbled line with a `parse_error` warning) never fired.

Original note: `//finalscores "<%b %d, %H:%M>" "<mode>" "<map>" "<team1>"
<s1> "<team2>" <s2>` is stuffed at match end (`ktx/src/commands.c:
6963-6975`) on 29% of E2 and ~100% of E3+ — authoritative mode, map and
final scoreline where `demoInfo` doesn't exist.

## 4. Surface parser warnings from qw-analyze

**SHIPPED** on `archive-parsing` (folded into the unreleased schema v72):
warning COLLECTION is now unconditional in the parser — a census, not a
log (exact `total` + exact per-type counts, plus a 64-row sample table of
distinct messages with counts and first occurrence; what the cap left out
is reported in `droppedWarnings`, an occurrence count — the reader cannot
report DISTINCT dropped messages without the unbounded key set the cap
exists to avoid). `SetDiagnosticMode`
now means only "also retain every individual instance", which is all the
diagnostic harness ever wanted. The census reaches analytics through a
new optional Source capability (`events.WarningReporter`) and rides every
Result as `parseWarnings`, so the CLI JSON, `/overview` and the WASM
result all carry it without opting in. It is deliberately NOT merged into
`errors[]` — that is the analyzer layer, this is the reader layer.
`qw-analyze` prints a one-line stderr summary whenever a demo raises any,
and the new `-warn` prints the whole table (needed because `-format md`
and `-view …` carry no `parseWarnings` of their own). `qw-corpus-survey`
dropped its second diagnostic parse per demo and reads the same numbers
off the analysis pass; CSV columns unchanged, and now filled on every run.

Validated against the 51k census (526 demos, ~1.0%, carry warnings): 28
warning demos across every category mix — including 6 `sv_bigcoords`
cases at 1 900–10 200 warnings — matched the census `warns` and
`unknownSvc` figures exactly; 10 clean demos stayed silent. All 13
golden demos parse clean, so no golden moved.

Related residual, still open: the bigcoords demos show `unknown_svc`
warnings after the angle fix (cmd bytes 61/64/67/128/192/195/224, E3/E4
only) — the worst archive demos run to ~6 900 of them in one file, so the
second, smaller desync in the bigcoords path is still to find. It is now
measurable from any normal run rather than only from a diagnostic
harness.

## 5. Validate reconstruction on E0–E2 (the un-established 40%)

**SHIPPED** on `archive-parsing` (measurement only — no behaviour, schema
or output changed). `cmd/qw-recon-oracle` scores the reconstruction on
demos with no KTX log by withholding the frag log from ATTRIBUTION
(`damagerecon.Options.WithholdObituaries`, delta extraction untouched) and
comparing the evidence-only verdict at each kill instant against the
killer and weapon the obituary names. **15 254 demos, 1 678 259 scored
kills, 54 min on 3 workers.** Verdict: **E0–E2 is not the weak half — it
scores at or above the GT-instrumented eras**: attacker accuracy E0
**97.6%** / E1 98.0% / E2 **98.2%** / E3 98.3% vs E4-GT 96.8% and E5-GT
96.3%, and the ordering survives the real confounder (team size: duels
98.4–99.2%, 4on4s E0 97.3% / E2 97.7% vs E4 95.4% / E5 95.8%). Calibrated
on 3 920 instrumented demos, the oracle reproduces the withheld run's
true accuracy to 0.1 pp and understates the SHIPPED pipeline by 2.3–2.5
pp (it anchors those instants; obituary-vs-GT label noise is 0.1%), so
those figures are floors. **No per-era trust tier and no era gating** —
ACCURACY.md now says so with the numbers.

Two of the three proposed oracles turned out to certify nothing, and
ACCURACY.md records why: the h/a delta IS the reconstruction's bounded
value (`deltas.go`), so it cannot validate magnitude; and given/taken
symmetry is an identity `aggregate.go` enforces, not a test (it does
expose one real gap — an attacker-less `world` telefrag charges the
victim's `taken` with no `given` anywhere and no `takenEnv`: 12 demos of
15 254). The expected weak spot was absent too: sg attribution on E0 (0.27
TE_BLOOD/shot in 4on4) reads 96.2% against E5's 95.4% at 1.48. The
"10–50× sparser" density gap is real but is a TEAM-GAME phenomenon —
duels sit at 0.02 blood/shot in every era including E5.

What the old half does cost, measured: **2.1% of E0 demos (80 of 3 876,
concentrated on QWSV 2.30 — 18 of 23 sampled; 101 such demos archive-wide)
barely broadcast the health stat channel**, so the section reports 83
bounded damage per kill where a healthy demo reports ~300. The
reconstruction is not wrong there, but nothing in the output said the
section is a fraction of the match. **That follow-up is SHIPPED** on
`old-demo-summary`, schema v74: `damage.coverage` `{kills, covered,
ratio}` on every reconstructed section publishes the oracle's own
kill-delta coverage from the normal pass (`damagerecon/aggregate.go
setCoverage`). Rescored 2026-08-23 over the FULL oracle sweeps (10 702
scoreable demos, not the 200/era subsample v74 shipped with): **99.0%
read ≥ 0.95** (median 1.000), the silent-channel class **0.182 median /
0.488 worst** is 0.80%, and **0.18% (19 demos, 18 of them E0) fall
between, spanning 0.500–0.944** on 18–287-kill denominators. A hard
bimodal core plus a thin gradient tail — the v74 "populations do not
overlap / no calibration needed" claim was a subsample artefact and the
docs now say magnitude, not flag. Honesty controls: the same
metric over a WIRE damage log reads exactly 1.000 on all 65 GT demos
(hence no `coverage` on `source: "ktx"` — it would be a constant), and
thinning the stat channel to one sample in four drops a healthy demo to
a 0.35 median with the denominator unmoved (`coverage_test.go`). Bounded
damage per kill was measured and rejected as the metric: the populations
overlap on it (healthy min 107, silent max 326). Nothing is gated on the
figure — the riders (`playerStats` damage family, its reconstructed
`accuracy.hits`, `aim.recon.hits`) inherit it by pointing at the one
field. Frozen weapon bits (`ewep` withheld) also
turned out NOT to be an old-demo speciality: 18% of E0 against 39% of E3
and 35% of E5. Method, tables, circularity analysis and the reproduction
commands live in `damagerecon/ACCURACY.md` §per-era validation; the
sampler, per-demo CSVs and aggregation in
`.reports/qw-recon-oracle-2026-08-19/`.

**Open lead — per-victim coverage.** The shipped figure is one scalar for
the whole demo, and it does not localize. The reconstruction reads
health/armor per VICTIM, so a single unbroadcast player can halve a
duel's ratio on its own; the 19 mid-band demos are exactly the population
where "which player's evidence was missing" is the question a consumer
would ask next, and the whole-demo number cannot answer it. `setCoverage`
already walks the frag log victim by victim, so a `byVictim` map is a
counter split rather than a new measurement — the open questions are the
schema cost on every reconstructed section and whether the natural axis
is the victim (whose stat channel was read) or the attacker (whose row
the consumer is reading). Not scheduled; recorded so the mid-band
finding does not have to be rediscovered.

**Known blind spot — frag-correlated loss.** Coverage divides by the frag
log, so a loss that removes obituaries and damage evidence TOGETHER (a
late-started recording, a hole in the stream, a demo cut short) shrinks
numerator and denominator in step and reads a clean 1.000 over the
surviving fraction. That is a property of the anchor, not a bug: the only
independent completeness signals are the match clock and the KTX
scoreboard / `//finalscores`. The docs now scope the claim to "how much
of the frag-log-visible match is in this section"; measuring recording
completeness against those independent anchors would be its own feature.

## 6. Aim hit recovery on reconstructed demos

**SHIPPED** on `aim-recovery`, schema v73; projectiles added on
`old-demo-summary`, schema v74 (see the follow-up below). `aimcore` re-runs the
fire→damage join against the reconstructed damage log and publishes the
recovered count as `aim.players[].weapons[].recon.hits`, with the new
`aim.hitsSource` (`ktx` | `reconstructed` | absent) naming the evidence.
`hitsMeasured` is untouched — still `false` on a reconstructed section,
every measured counter still withheld — so the two tiers live in
different fields and a reconstructed hit can never be read as a measured
one. The `aim` node now binds `damage:final`.

The design gate lead 5 was supposed to decide (per-era labelling) turned
out to be a non-question: no era gating, one trust statement, per lead
5's measurement. The real gate was per-WEAPON, and the harness the change
ships with (`cmd/qw-aim-eval`) decided it. Withhold-and-compare on 53
dm2/dm3 demos carrying the KTX log — keep the measured aim, swap in the
blind reconstruction of the same match, recompute, pair per player and
weapon — with the same join ALSO run against the wire log as a control,
which separates the join method's error from the reconstruction's:

| weapon | mean \|Δacc\| vs measured | control (join on the wire log) | shipped |
|---|---|---|---|
| lg | 0.3pp | exact | yes |
| sg | 1.3pp | exact | yes |
| ssg | 1.7pp | exact | yes |
| axe | 0.5pp | 0.1pp | yes |
| rl | 7.4pp → **0.6pp** (v74) | +7.3pp → +0.4pp | yes, since v74 |
| gl | 1.3pp → **0.3pp** (v74) | +1.0pp → +0.1pp | yes, since v74 |

rl/gl were withheld in v73 because the gap was not reconstruction error:
the control reproduced it exactly. Their fire→impact link needs the
entity-flight bracket `ShotsAnalyzer` builds and discards, so from a
finished Result the join could only count impacts — a different question
than the measured counter asks (it counts fires whose flight LINKED, so a
point-blank rocket that never broadcast its entity is measured as a
miss).

**Follow-up done, schema v74** (branch `old-demo-summary`): the
association is published as `shots[].flightEnd` — the impact time of the
flight a fire launched, absent when no flight was tracked — and the
projectile join now anchors on it, inside damagerecon's own
`tolProjBeforeMs`/`tolProjAfterMs` window, one damage instant claimed per
flight. Same 53-demo corpus: rl 7.4pp → **0.6pp** mean error (bias +0.4,
91% of rows within 2pp), gl 1.3pp → **0.3pp**, hitscan rows unchanged to
the last row; the +0.4pp control residual is one-sided (recon damage
from never-tracked rockets adopted by a nearby flight inside the log's
own late-stat-instant tolerance). Numbers and method:
`damagerecon/ACCURACY.md`; raw output in
`.reports/aim-rlgl-2026-08-23/`.

ng/sng stay withheld: nail linking is opt-in, so their measured counter
is zero on every corpus row and there is nothing to validate against.

Also withheld on a reconstructed section, per field, in
`result.WeaponAimRecon` and RESULT_SCHEMA: per-fire `hit` columns, the
pellet split, direct/splash, the LG whiff classes, the enemy/team/self
slices — all of them defeated by the same two properties, the
victim-stat-instant anchor and same-instant delta merging. Tables and
method: `damagerecon/ACCURACY.md` §"Aim hit recovery"; raw eval output in
`.reports/qw-aim-eval-2026-08-19/`.

~~Deliberately not done: the web Aim tab still renders "—" for hits on
these demos~~ — SHIPPED as the frontend pass on this branch: the Aim tab's
Hits / Hit % fall back to `recon.hits` where the measured counters are
withheld, marked `.stat-recon` and captioned with `damage.coverage`. See
[`mvd-web/README.md`](mvd-web/README.md) §"Reconstructed damage says so".

## 7. Smaller / opportunistic

- **Crush/squish candidates from MoverStream** — skull-map crushers hide
  ~29k GT raw per demo; mover poses are already tracked
  (`.reports/…/eval-gt-archive.txt` outliers).
- **Old-FFA taken errors** (archive GT eval, small denominators) —
  uninvestigated.
- **`trySplitPair` lacks explosion/discharge candidate families** —
  looks like an omission (simplification-agent finding); eval-verify.
- **`ktxver` feature-gating helper** — parse `ktxver` (+fork handling)
  once, use it where behavior is generation-dependent.
- ~~REST endpoint for `streams.pointEffects`~~ — SHIPPED in PR #132
  (`GET /v1/demos/{id}/streams/point-effects` + `getPointEffects` MCP
  tool). The map hit-marker overlay consuming it is still unbuilt.
- ~~**"no match detected" marker**~~ — SHIPPED as lead 8 stage (a)
  below (`result.noMatch`, schema v74).

## 8. Mid-match recordings: salvage or mark (NEW from the 51k sweep)

**Stage (a) — MARK — SHIPPED** on `old-demo-summary` (folded into the
unreleased schema v74). Stage (b) — SALVAGE — remains.

### What shipped

`result.noMatch`, present on exactly the results that carry no player
streams and absent on every result with players. It names the reason from
a five-value TOTAL PARTITION, and carries the wire evidence behind the
verdict (`statusAtOpen`, `statusRunningSeen`, `gameDir`, `kills`) plus a
human `detail` sentence. `/overview` republishes it beside `errors[]`;
`GET /v1/demos/{id}/artifacts/no-match` serves it directly; the MCP
`getOverview` description tells an agent not to read such a demo as a
failed parse. Node `no-match` (`analyzer/nomatch.go`), evidence published
on `CoreOutputs.ServerStatus` by the metadata node.

### The population, re-measured

The 877 in the original note was the `source:"none"` slice of the
readability CSV. The true stream-less population is **1 032 of 50 951
(2.03%)**: the 877 plus 155 rows the survey had labelled `ktx`/`skipped`
because a `damage` section existed without any streams behind it (race
maps, and 21 demos carrying a full KTX demoinfo block).

| `reason` | evidence | n | notes |
|---|---|---|---|
| `noPlayRecorded` | no running `status` ever, no kills | 636 | idle / aborted servers, mostly a few seconds long |
| `noMatchDeclared` | no running `status` ever, kills > 0 | 170 | usually unmanaged play, but an ABSENCE of evidence: 168 send no `status` key at all, 165 on a foreign `*gamedir` |
| `matchStartUnannounced` | `status` became running DURING the recording | 138 | 32 carry a KTX demoinfo block, 104 carry a date marker |
| `midMatchRecording` | `status` already running at demo open | 68 | 67 parse a frag log |
| `demoUnreadable` | the event stream aborted | 20 | 19 aborted at stream offset 16 with nothing decoded |

Every one of the 1 032 is marked, no leftovers. A 1 500-demo random
control drawn from the 49 919 demos that DO produce streams carries
**zero** markers, and no golden moved.

One correction to the original note: the "~260 REAL matches" estimate
lands as 68 + 138 = **206** demos where the server demonstrably declared
a running match, plus 170 more with real play under no match state.

**The `status` vocabulary, measured.** Over the 1 032 the key takes 1 198
distinct values; the 1 500-demo healthy control adds 1 213 more and no new
spelling. Every remaining-time reading in either set matches one of exactly
two clock formats — KTX's `"%d min left"`
(`ktx/src/match.c:596,723,1330,1337`) and a CTF mod's `"%d:%02d left"` —
and that pair is what `statusNamesRunningGame` tests, tightened from the
original "ends in ` left` and starts with a digit" once the census showed
zero values that the loose rule accepted and the strict pair did not. The
non-reading values are `Standby` / `Countdown` / `Forcestart` / `Normal`,
plus two the original note said were absent: **`Game Ended`** (the CTF
mod's terminal status, 10 demos — it is in neither ktx nor mvdsv because
it is that mod's own string) and **`Round 1/15`…`Round 11/15`** (gamedir
`arena`). Those two are the argument for the tight test: mods do write
their own vocabulary here, so a value merely ending in ` left` is not
evidence of a clock.

`statusAtOpen` is captured from the OPENING `fullserverinfo` dump alone. 3
of the 1 032 open with no `status` key and gain one from a later
`svc_serverinfo` (all three `Countdown`); those read as absent, since the
field is named for demo open, and `statusRunningSeen` still carries the
later readings. No reason moved when that was tightened.

### Why the v52 `timeBase:"demo"` fallback never engaged (the answer)

`flagDemoTimeBase` (`analyzer/timeline_finalize.go:628`) opens with
`if result.Streams == nil { return }` — so on these demos it writes
neither `timeBase:"demo"` nor its `errors[]` notice. `result.Streams` is
nil because `buildStreamsResult` (`analyzer/timeline_streams.go:562`)
ends with `if len(streams.Players) == 0 { return nil }`, and every
per-player builder is empty because every recording path in `timeline.go`
is gated on `a.timing.Started`, which only flips on a recognised
match-start bprint. The fallback was written for a different case — "a
match start WAS detected but at demo `t=0`, so `Clock.MatchStartMs` is 0"
— which is reachable but rare. `wallClockPost` returned early on the same
nil for the same reason. Both are now documented in place, and
`flagDemoTimeBase`'s remaining case is untouched.

### The wall clock: markers shipped, the graded anchor did not

`noMatch.dateMarkers` now carries every stamp the wire printed, verbatim
— 104 of the 1 032 have one, and all of them were previously read and
dropped. `wallClockPost` binds the `no-match` node and writes there when
there is no `streams` block.

The graded `matchStartUnixMs` anchor beside them was deliberately NOT
published, and this is the one design finding worth carrying into stage
(b). The anchor is a PROJECTION through the match window: a match-start
print resolves as `stamp − print's demo time + demoOffset`, a match-end
stamp as `stamp − match length`. Both terms are the match window, which is
exactly what a stream-less result lacks — running the resolver against a
zero window produces "wall clock at demo `t=0`" for the print markers and
"the raw end stamp" for the end markers, two different instants both
labelled `matchStartUnixMs`. Measured on
`01ca7880…` (a real KTX 1.40 demo): the matchdate stamp reads
1627844964000 and the zero-window projection reads 1627844963745, off by
the print's own 255 ms demo offset. Publishing that would be a wrong
answer with a confidence grade attached. Establishing a match origin on
the demo clock — which makes the projection well-defined — IS stage (b).
No GlobalStream re-plumbing was attempted.

### Stage (b) — salvage — remains

Analyze the recorded portion of the 206 demos that demonstrably hold a
match, on the demo clock. The two signals a salvage pass would key on are
now both published as evidence: `statusAtOpen` naming a running game (the
recording began mid-match, so the whole recorded window is in-match), and
the `status` transition to a running value (`matchStartUnannounced` — the
transition instant IS the match start on the demo clock, within one
server frame; on `01ca7880…` the matchdate print lands at demo t=255 ms
and the transition at t=292 ms). Note that 32 of the
`matchStartUnannounced` demos carry a full KTX demoinfo block, so a
salvage pass on them can be validated against an authoritative
scoreboard. A salvaged result would then also unlock the graded wall
clock above.

**Contract moves stage (b) must make, decided now.** `noMatch` is
present exactly when `streams` is absent, so salvage does not "add data
beside the marker" — it removes the marker. Four consequences, settled
here so the implementer does not have to re-litigate them mid-pass:

1. **A salvaged demo loses `noMatch` entirely.** It gains a `streams`
   block, and the invariant is one predicate in both directions. There is
   no "salvaged but still marked" state, and no reason value for one.
2. **Front-truncation gets a NEW marker, on `streams.global`, beside
   `timeBase`.** A salvaged result is a real match whose recorded window
   starts after the match origin — that is a property of the STREAMS, not
   an absence of them, so it belongs where `timeBase` already says "these
   timestamps are not what you assume". Do not reuse a `noMatch` reason
   name for it: `midMatchRecording` describes a result with no streams.
3. **`dateMarkers` relocate with the streams.** They live on
   `noMatch.dateMarkers` only because there is no `GlobalStream`; a
   salvaged result carries them on `streams.global.dateMarkers` like any
   other, and the graded `matchStartUnixMs` anchor becomes publishable
   there for the first time (the match origin stage (b) establishes is
   exactly the missing projection term).
4. **`noMatch` never coexists with `streams`.** Every doc site states
   this and `wallClockPost` routes on it; a stage-(b) shortcut that
   stamped both would break the one thing consumers were told they can
   switch on.

## 9. Derived demoinfo equivalents (NEW from the 51k sweep)

**SHIPPED** on `old-demo-summary` (folded into the unreleased schema
v74). The premise held: 54% of the archive has no KTX demoinfo block and
that ceiling is permanent, but nearly everything the block carries is
derivable from artifacts every demo has.

**The section is `playerStats`, not a new one.** It has been the derived
per-player summary since v63, with a `src` on every family and the KTX
overlay applied at READ time (`view.PlayerStats`) so the stored artifact
is always what this pipeline computed. A parallel `src:"derived"` section
would have duplicated ~90% of it and given consumers two answers to the
same question; deferral on a ktxstats-carrying demo is then automatic and
structural — the overlay replaces the family, so the derived numbers
never surface beside the verbatim ones. Only two gaps were left against
the block, and both are now closed:

- **`score.maxSpree` / `score.maxQuadSpree`** (`analyzer/spree.go`) —
  KTX's `spree.max` / `spree.quad`, replayed from the corrected frag log,
  the protocol death markers and the quad possession runs, following
  KTX's own state machine (`client.c:4864-4876`, `items.c:2180-2181`,
  `stats.c:1637-1638`). They ride `score.kills`' `killsMeasured` gate.
- **`accuracy.src: "reconstructed"` with `hits`** — read off the v73/v74
  aim recon tier (lead 6) rather than re-joined, so its weapon-level
  withholds inherit: `ng`/`sng` carry no `hits` at all. The `player-stats`
  DAG node binds `aim` for this.

Everything else the lead listed already existed and is now MEASURED
rather than assumed: fires from the shot streams, damage from the
reconstruction (`src: "reconstructed"` since v71), powerup control from
`pickups.byKind` + `hold.powerups`.

**Validated withhold-and-compare** (new harness `cmd/qw-demoinfo-eval`,
the qw-aim-eval protocol) against the verbatim block on **188
instrumented archive demos / 665 player rows**: `maxQuadSpree` **99.8%
exact**, `maxSpree` **99.6%** on rows whose `kills` already agrees with
KTX and whose player never suicided (92.9% unconditioned), powerup
`took` **100.0% exact** on all three, powerup possession seconds
0.07–0.49% aggregate, `frags`/`deaths` 99.5%/99.7%, reconstructed
`damage.given`/`taken` 0.49%/0.46%, `accuracy.attacks` 98–100% exact on
every single-projectile weapon, `lg` `hits` 0.89% aggregate. Full table:
[`damagerecon/ACCURACY.md`](mvd-analytics/damagerecon/ACCURACY.md)
§"The whole derived summary vs the verbatim KTX block".

**Two residuals, both named rather than smoothed.** `maxSpree` diverges
from KTX by design — KTX's gate is `strneq(attackerteam, targteam) ||
!tp_num()`, so wherever teamplay is off a player's own suicide bumps
their streak in the call that latches it; 21 of the 22 mismatches on
kills-agreeing rows belong to a suicider and all 22 are at −1, with the
harness's KTX-convention replay reproducing the block on every one. 16 of
the 17 rows at |Δ| ≥ 3 sit where the frag log had already credited 0
kills against KTX's 8–47, a pre-existing kill-attribution gap the streak
inherits — and one with an observable signature (`kills: 0` beside a
large positive `frags`).

**One doc correction the eval forced:** derived `hits` were described as
"broadly comparable" to KTX's on the single-projectile weapons. True for
`lg` (0.9%), false for `rl`/`gl` — KTX counts DIRECT impacts only
(`weapons.c:994`, `:1329`) and ours counts any path, ~4x apart on rl.
RESULT_SCHEMA now carries the per-weapon comparability table, and the
payload carries `accuracy.byWeapon[].hitsConvention` so a consumer does
not have to read prose to know whether two numbers may be compared.
Deriving KTX's own convention on an old demo was tried and does not
work: the wire splash flag reproduces `acc.rl.hits` on 638 of 638 rows,
but the reconstruction's geometric direct/splash verdict answers `gl`
(1.2% aggregate) and not `rl` (+80%).

**Not done, deliberately.** `speed` (max/avg units/s) stays KTX-only —
the position streams could support it, but it is a different derivation
from anything here and belongs with a movement lead. `controlMs` stays
KTX-only: our region-control view answers a different question, and
faking KTX's duel-control heuristic would be a second, disagreeing
number. `dmg.teamWeapons` stays KTX-only (the reconstruction does not
bucket team damage by the victim's inventory). `ng`/`sng` accuracy hits
stay withheld until nail linking has ground truth to validate against.

## 10. Backpack pickup linkage on reconstructed drops (successor to lead 2)

**SHIPPED** on `archive-parsing` (folded into the unreleased schema v72):
the `backpack-linkage` post (`mvd-analytics/analyzer/backpack_linkage.go`)
reads each reconstructed drop's fate off the wire's backpack-ENTITY track
and stamps it on the same row — `backpacks[].fate` (`picked` / `expired` /
`unobserved`) with `picker`, `pickerTeam`, `pickupTime` and the bound
`entNum`. Hint-carrying demos are untouched.

**The premise below was wrong, and measurably so.** The "16.4 PVS-flutter
visibility flips per real pack" was an artefact of our own parser caching an
edict's item kind forever; KTX recycles pack edicts within seconds
(`items.c:2701, 2489`), so every later tenant read as the original pack
flickering. With the model index re-read on every visible frame
(`mvd-reader/parser/entities.go`), a pack's life is one appearance and one
disappearance: **3 205 pack lives against 3 319 deaths over 24 demos, and
ZERO lives re-opening where the previous one ended.** No flutter left to
stitch — the speed-gate stitch this lead asked for was built, measured at
zero hits, and removed (mvdsv will not reuse an edict freed < 0.5 s ago,
`pr_edict.c:118-127`). Parser enablers shipped: `ItemStateEvent.Origin` plus
a new `ItemMoveEvent`.

Feasibility probe (phase 0, 24 hinted demos): binding a drop to a pack by
(t ± 200 ms, nearest to the drop position) hit **947 of 961 (98.5%) and got
zero wrong**, with the drop→appearance offset exactly 0 ms at p50/p99/max
and the position gap p99 23 units. Packs travel p50 58 / p99 422 / max 583
units, so the resting position is tracked. **Stat-flip attribution was
rejected on evidence**, not built as a lower-confidence tier: only 237 of
606 ground-truth pickups came with a weapon-bit gain (KTX ORs the bit in),
so requiring one discards 61% of real pickups. The primary signal is the
server's own touch test — bounding-box overlap including the 15-unit
`FL_ITEM` expansion (`sv_world.c:373-379`), without which the predicate
misses 90% of pickups — and the bit gain only separates two players on one
pack.

Validated exactly like the drops, with EVERY hint withheld, on **223 demos
spanning KTX 1.38–1.48** (10 378 scored drops; the harness additionally
gates on the demo emitting `//ktx bp` at all — 107 of 330 sampled demos
emit `//ktx drop` but not `//ktx bp`, which the wire confirms is a recording
property and not an all-expired match — those demos carry 3 844 drop hints
against 44 `//ktx expire`): picked-vs-not **100.00% precision /
96.13% recall**, `expired` **100.00% precision / 100.00% recall**, **99.77%**
of pickups carry a named picker and **99.98%** of those are correct,
pickup-time error 0 ms at p50/p90. Precision is 100% in every ktxver bucket
and every mode. Hint-less volume sanity over 674 demos: 86.7% `picked` /
10.5% `unobserved` / 2.8% `expired`, picker named on 99.7% of the picked —
the shape the hinted population (96.4% picked) predicts. Pack-entity
coverage on the target population is 9 928 lives / 10 078 deaths over 86
pre-1.38 demos.

Residuals, all named: the largest is unfixable — a pack taken inside the
demo frame it dropped in never reaches the wire (202 of 10 378); then a
picker 0-9 units outside the touch box on both bracketing samples and the
swept path between them (167 — sweeping the PACK along its own track too was
measured and costs 249 pickups, so it stays fixed at its last broadcast); a
liveness edge (8); bind refusals (10). Every figure here survived the PR
review fleet's re-run unchanged, including after the touch test grew
`BackpackTouch`'s mode guard and the expiry boundary became cadence-derived.

**Follow-up SHIPPED: `//ktx expire` decoded (2026-08-18).** The lead below
recorded KTX's third backpack directive as the strongest unused signal in
this area, and it is now the `expired` class's ground truth. `SUB_Remove`
writes `//ktx expire <ent>` for every RL/LG pack it removes untaken
(`ktx/src/g_spawn.c:196-210`); the parser decodes it as
`BackpackExpireHintEvent` and `BackpackAnalyzer` joins it — by edict AND
time, at KTX's own 120 s deadline — onto the hint row it closes, as
`fate: "expired"`. It is the only wire statement that a pack was NOT taken,
which the `weaponPickups` join cannot make.

Two things it changed. `expired` recall went **50.26% → 100.00% (190/190)**
at unchanged 100.00% precision, with no change to the linkage at all: the
old denominator counted every drop with no `//ktx bp`, and half of those
were packs the recording ended on top of, not expiries. And the web UI's
Pack Drops table, which labelled every hint row with no pickup `expired`,
now says `unobserved` there unless the hint confirms it. Wire census over
the 223 demos: 10 384 drops = 10 006 `bp` + 190 `expire` + 188 claimed by
neither, **zero** rows claiming both — so the earlier "drop ≈ bp + expire
holds row by row" note was measured and corrected (the residual is 1.8% of
drops on 102 of the 223 demos, and not one of those rows has a bound pack
entity that ever left the wire). The hint never reaches the reconstruction:
zero occurrences across 531 hint-less demos, since it is co-emitted with
`//ktx drop`, and the eval withholds it regardless. `picked` metrics and the
drop-side eval are byte-identical.
`hadBefore` remains underivable, so pack TRANSFER credit and pack kill
credit stay absent, which is why the linkage output rides the `backpacks`
row instead of `weaponPickups`. Numbers, method and the
`cmd/qw-backpack-eval -linkage` reproduction command live in
`mvd-analytics/analyzer/BACKPACKS.md`.

Original note: Lead 2 reconstructs the DROP; on hint-less demos the pack's
fate stays `unobserved` — mushi's RL stat-flip 5 s after nexus's death is
visible but unlinked. The raw entity signal alone is unusable (recycled bp
edicts, origins currently discarded by `diffItemEnt`, measured 16.4
PVS-flutter visibility flips per real pack), but a reconstructed drop is now
a clean anchor to bind the dirty edict to: (a) surface backpack-entity
origins in the parser (the enabler lead 2 evaluated and skipped); (b) near
each reconstructed drop (t, pos−24Z) bind the appearing backpack-model
entity; (c) follow its origin track — packs FALL (ledges, lift shafts), so
the resting position is tracked, never assumed; (d) stitch over flutter (a
flutter re-appearance carries the same origin; a pickup does not re-appear)
and read the final disappearance: close-in-time+radius to a matching
stat-flip (a player WITHOUT the weapon gaining its bit, world spawners
excluded by position) → attributed pickup; at the KTX removal timeout with
no flip → expired. Validate exactly like the drops: modern demos carry
`//ktx bp` pickup hints — run with hints withheld, score attribution
precision/recall (the drop eval's 99.97/99.97 protocol). Web: Pack Drops
upgrades `unobserved` → `picked up by <name> (reconstructed)` / `expired`.

## Full-archive readability census (2026-08-17)

`qw-corpus-survey -readability` over all 50,951 demos (CSV:
`/mnt/HC_Volume_106625439/data/readability-51k.csv`, summary at the
tail of `readability-51k.log` beside it). Headlines: ktx 46.8% /
reconstructed 51.1% / none 1.7% (the 877 of lead 8 — 1 032 once the
stream-less rows the survey filed under ktx/skipped are counted) / skipped 0.4%;
zero unexpected pipeline errors; parse warnings on 1.0% of demos,
unknown_svc on just 12. Marker coverage: matchdate 71.9%, finalscores
64.1%, //ktx drop 49.2%, demoinfo 45.6%, current wallclock 24.8%.
Reconstructed-half ceilings before this plan's leads: 51.6% dateless
(matchkey closes it — lead 1), 65.3% score-source-less, 83.9% without
backpack GT.

## Validation assets (all in place)

- 51k-demo archive at `/mnt/HC_Volume_106625439/data/mvd/` (sha-named
  `.mvd.gz`), SQLite index `corpus-index.sqlite` (modern subset only).
- Pinned GT corpus `data/eval-corpus-dm2dm3/` (60 demos + manifest,
  re-fetchable via `cmd/fetch-eval-corpus`).
- Survey samples + per-demo feature CSVs + reproduction scripts in
  `.reports/qw-archive-data-survey-2026-08-16/`.
- Harnesses: `cmd/qw-recon-eval` (GT scoring), `cmd/qw-corpus-survey`
  (population sweeps).
