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

Residuals: a reconstructed row carries no `entNum`, so it cannot join to
its pickup and earns no pack-transfer credit; `dp 0` has no wire signal on
a pre-1.38 demo; pre-KTX (qwsv/KTPro) drop rules cannot be GT-validated by
construction, though their rate and inventory consistency match the KTX
population. Pickup side still stays with `weapon_pickups`.

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

Blood/gunshot telemetry is 10–50× sparser on pre-MVDSV-0.30 demos and
no GT eval covers them (archive GT demos are E4/E5). Method that needs
no KTX log: the internal oracle — on survived hits the h/a delta IS the
raw damage — plus frag-log anchoring and given/taken symmetry, run at
scale over the E0–E2 slice of `data/mvd/` (extend `qw-corpus-survey`
with oracle metrics or a sibling tool). Expect weaker sg attribution
there; measure it, then decide whether E0–E2 needs its own trust tier
in ACCURACY.md.

## 6. Aim hit recovery on reconstructed demos (design-gated)

The hits:0 fabrication is fixed (withheld + `aim.hitsMeasured`); the
recovery half remains: link reconstructed damage events back to fires
(the same join `ShotsAnalyzer` does) and emit hit counters labelled
`src:"reconstructed"`. Survey says view angles are fine on 98% of the
archive — hit linkage was the only gap. Per-era honesty: E0–E2 recovery
will be much weaker (see lead 5); decide the labelling story before
building.

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
- **"no match detected" marker** — 2% of the archive (TF/CTF/race
  content) yields empty streams with no way for a consumer to tell
  "no match here" from "parse failed"; add an explicit marker.

## 8. Mid-match recordings: salvage or mark (NEW from the 51k sweep)

Of the 877 "no player streams" demos, ~260 are REAL matches whose
recording starts mid-game — serverinfo `status` reads "1 min left" /
"Game Ended" at demo open, the frag log parses (30-50 frags), 73 carry
matchdate — but match-start detection never fires, streams come out
empty and `errors[]` is EMPTY (the v52 `timeBase:"demo"` fallback does
not engage; investigate why). Fix in two stages: (a) always emit an
explicit marker (extends lead 7's "no match detected" idea — and
distinguish "mid-match recording", detectable from the `status` key,
from "foreign mod content"); (b) salvage: analyze the recorded portion
on the demo clock. The rest of the 877 is genuinely foreign content
(TF/custom-mod maps: genders2, blitzkrieg2, mvdsv-kg) — mark, don't
parse.

Folded in from the v72 review: on these demos the WALL CLOCK is lost too.
`wallClockPost` returns early when `res.Streams == nil`, so the 73 that
carry a `matchdate` publish no `dateMarkers`, no `matchStartUnixMs` and no
grade — the stamps are read off the wire and then dropped silently, while
`metadata.finalScores` (which does not live under `streams`) survives.
This is structural, not a bug in the resolver: the anchor fields are
GlobalStream fields, so surfacing them here means deciding where a
stream-less result carries a match window at all — which is exactly what
(a) and (b) above settle. Fix it as part of this lead, not before it.

## 9. Derived demoinfo equivalents (NEW from the 51k sweep)

54% of the archive has no KTX demoinfo block, and the census shows
that ceiling is permanent (it's the pre-ktxstats half). But most of
what the block carries is derivable from data we already have: weapon
fires from the shot streams, hits via lead 6, sprees from the frag
log, powerup/RA control time from the item/stream intervals. A
`src:"derived"` stats section closing most of the demoinfo gap on old
demos — sequence after leads 5/6 so the hit-side inputs exist.

## Full-archive readability census (2026-08-17)

`qw-corpus-survey -readability` over all 50,951 demos (CSV:
`/mnt/HC_Volume_106625439/data/readability-51k.csv`, summary at the
tail of `readability-51k.log` beside it). Headlines: ktx 46.8% /
reconstructed 51.1% / none 1.7% (the 877 of lead 8) / skipped 0.4%;
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
