#!/usr/bin/env bash
# Compare two shot sets captured by capture-baseline.sh.
#
#   mvd-web/test/compare-shots.sh /tmp/mapshots/baseline /tmp/mapshots/after
#
# Renderer output is byte-deterministic across runs on the same machine, so
# any DIFF is a real pixel change — inspect it, don't average it away.
set -uo pipefail
A="$1"; B="$2"
same=0; diff=0; missing=0
while IFS= read -r f; do
    rel="${f#"$A"/}"
    if [ ! -f "$B/$rel" ]; then
        echo "MISSING $rel"; missing=$((missing+1)); continue
    fi
    if cmp -s "$f" "$B/$rel"; then
        same=$((same+1))
    else
        echo "DIFF    $rel"; diff=$((diff+1))
    fi
done < <(find "$A" -name '*.png' | sort)
echo "---"
echo "same=$same diff=$diff missing=$missing"
[ "$diff" -eq 0 ] && [ "$missing" -eq 0 ]
