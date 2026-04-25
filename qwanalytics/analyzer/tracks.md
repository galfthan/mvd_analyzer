# tracks analyser (shelved)

**Phase:** would-be Derived
**Status:** code present, not registered

`tracks.go` segments per-life movement into discrete tracks (one per
spawn-to-death window) for visualisation. It's currently unwired —
kept on the shelf until the qw-web side has a consumer ("how do
players approach quad", opening-15-seconds movement, etc.).

When revived it will:

- Register as a Derived analyser.
- Read `co.FragEntries` (or its own death/spawn capture) for life
  boundaries.
- Read the smoothed loc track from timeline buckets (the blipfilter
  output is the canonical loc stream — see [timeline.md](timeline.md)).
- Emit a `result.Tracks` field (schema TBD).

The audit (Phase 3) reserved tracks for "Phase 3-D — promote
post-passes / shelved analysers into the registered pipeline".
