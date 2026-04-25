# demoinfo analyser

**Phase:** Core
**Inputs:** `DemoInfoEvent` (`mvdhidden` 0x0003 reassembled JSON blocks)
**Writes to Result:** `result.DemoInfo` (`*DemoInfoResult`)
**Writes to CoreOutputs:** `co.DemoInfo`, `co.Names`, `co.Slots`

## What it does

Parses the KTX-emitted JSON metadata embedded as hidden messages
(`mvdhidden_demoinfo`, type 0x0003). The JSON is split across multiple
`DemoInfoEvent` blocks — block numbering goes `1, 2, 3, …, 0` where
block 0 is the LAST block (KTX uses 0 as the terminator). The analyser
buffers blocks and reassembles them in correct order before parsing.

## How it works

1. Each `DemoInfoEvent` is keyed by block number into a map.
2. At Finalize, the analyser concatenates `1, 2, …, N, 0` into a JSON
   string and unmarshals it into `DemoInfoResult`.
3. Player and team names are passed through `cleanQuakeName` to strip
   QuakeWorld colour codes and Q_normalizetext folding.
4. The parsed result is mirrored to `ctx.DemoInfo` so
   `Context.ResolveSlotDemoInfo()` (used by `PopulateCore`) can join
   slot → demoinfo player via auth login first, then name.
5. `PopulateCore` builds `co.Slots` for every non-nil
   `ctx.Players[slot]`: the slot's display name is the demoinfo name
   when matched, else the userinfo name.

## Why Core

Three downstream analysers (frag, messages, timeline) read
`co.{DemoInfo,Names}` during their own Finalize. The slot table is
read by match, weapon_pickups, and any future analyser that needs a
canonical "this slot's display name" lookup.

## Limitations / known issues

- Demos without an embedded demoinfo JSON (older recordings, non-KTX
  servers) leave `result.DemoInfo` and every `co.*` field nil — every
  consumer must nil-check.
- Slot resolution falls through to `findPlayerByName` (in `names.go`)
  for the name-join branch, which does exact → normalized → substring
  matching. Substring matches are best-effort and can pick the wrong
  player when one demoinfo name is a prefix of another.
- The "block 0 is last" convention is a KTX detail; if a future server
  mod stops emitting block 0 the parser will skip the demo silently
  (no error). See `qwdemo/MVD_FORMAT.md` for the wire format.

## Reference

- KTX hidden-message emitter: `ktx/src/g_main.c` (`G_DemoSendInfoBlocks`)
- ezquake recorder: `ezquake-source/src/sv_demo.c`
- Wire format: [`qwdemo/MVD_FORMAT.md`](../../qwdemo/MVD_FORMAT.md) (search "mvdhidden_demoinfo")
