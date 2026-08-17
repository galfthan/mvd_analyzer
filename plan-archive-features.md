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
"normaltid" +01), missing tz → assume UTC. TRUST GATING is mandatory:
unset server clocks produce bogus date clusters (a whole 2000-01-07
night of demos observed), LAN matchkeys are off (qhlan), and stuffed
values get corrupted ("timelimit" changed to "Final Score is 47 - 9")
— sanity-gate against the serverinfo `*version` era and flag
low-confidence anchors rather than dropping them.

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

The `//ktx drop` hint exists only ≥KTX 1.38; the wire entity stream
cannot substitute as-is (bp edicts are recycled so `ItemSpawnEvent` is
sticky-suppressed, and `ItemStateEvent` carries no origin — measured
16.4 visibility flips per real drop). The productive path is
deterministic reconstruction: KTX `DropBackpack` (`ktx/src/items.c:
2667-2766`) runs on every non-suicide mid-match death and picks the
victim's best droppable weapon with ammo — and death instants, victim
weapon bits, ammo counts and death position are all already in the
Result on ~90% of demos. Reconstruct RL/LG drops from
death + inventory + position, labelled like damage
(`source:"reconstructed"`), validated against `//ktx drop` ground truth
on the 50% of the archive that has it. Cheap enabler either way: add
`Origin` to `ItemStateEvent` (parser change; `diffItemEntity` discards
it today while the mover path deliberately keeps it). Mode caveat:
`DropBackpack` returns early in midair/smashpack/extinction/wipeout/CA
— gate on the same mode detection damage uses. Pickup side stays with
`weapon_pickups`' stat-flip synthesis (the entity flutter is unusable).

## 3. Parse `//finalscores` (62% of archive, two eras before ktxstats)

`//finalscores "<%b %d, %H:%M>" "<mode>" "<map>" "<team1>" <s1>
"<team2>" <s2>` is stuffed at match end (`ktx/src/commands.c:
6963-6975`) on 29% of E2 and ~100% of E3+ — authoritative mode, map and
final scoreline where `demoInfo` doesn't exist. Parser-side: one prefix
matcher beside the `//ktx ` directives (`parser/ktx_pickup.go:65`) and
a typed event; analytics-side: feed metadata/match where demoinfo is
absent (never override ktxstats). Also carries a date (format G) that
corroborates lead 1.

## 4. Surface parser warnings from qw-analyze

The sv_bigcoords desync degraded 5% of the archive for years with zero
operator-visible signal — warnings exist only in diagnostic mode used
by one test harness. Add a `-warn` flag (or a `parseWarnings` count in
the Result) so the next protocol gap is visible. Related residual: the
bigcoords demos still show ~213 `unknown_svc` events after the angle
fix (cmd bytes 61/64/67/128/192/195/224, E3/E4 only) — a second,
smaller desync in the bigcoords path still to find.

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
