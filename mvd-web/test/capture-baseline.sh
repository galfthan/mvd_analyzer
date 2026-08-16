#!/usr/bin/env bash
# Capture the full map-parity shot set into $1 (default /tmp/mapshots/<label>).
#
# Five runs over four demos: dm3 4on4 (movers + water + 8 players), dm6 and
# aerowalk 2on2, obsidian 4on4 (custom map), plus one dm3 run with the
# geometry fetch blocked so the convex-hull fallback path is covered too.
#
#   mvd-web/test/capture-baseline.sh /tmp/mapshots/baseline   # before a change
#   mvd-web/test/capture-baseline.sh /tmp/mapshots/after      # after it
#   mvd-web/test/compare-shots.sh /tmp/mapshots/{baseline,after}
set -euo pipefail
cd "$(dirname "$0")/../.."

OUT="${1:-/tmp/mapshots/$(date +%s)}"
CACHE=mvd-analytics/testdata/cache
# MAPSHOT_FLAGS passes extra mapshot.py flags through — most usefully
# --no-webgl, which anchors the byte-exact comparison on the 2D backend
# (WebGL shots are only comparable against a same-machine GL baseline).
SHOT="python3 mvd-web/test/mapshot.py ${MAPSHOT_FLAGS:-}"

mkdir -p "$OUT"
$SHOT --demo $CACHE/212260.mvd.gz --out "$OUT" --times 60,300,600   # dm3 4on4
$SHOT --demo $CACHE/211805.mvd.gz --out "$OUT" --times 60,300       # dm6 2on2
$SHOT --demo $CACHE/212535.mvd.gz --out "$OUT" --times 60,300       # aerowalk 2on2
$SHOT --demo $CACHE/212483.mvd.gz --out "$OUT" --times 60,300       # obsidian 4on4
$SHOT --demo $CACHE/212260.mvd.gz --out "$OUT/nogeom" --times 300 --no-geometry

echo "baseline in $OUT ($(find "$OUT" -name '*.png' | wc -l) shots)"
