#!/usr/bin/env bash
# fetch-bsps.sh — download a small curated set of Quake 1 BSP files used
# by the locvis visibility filter. Populates the directory passed as
# $1 (default ./bsps). Idempotent: existing files with a matching
# SHA-256 are kept; mismatches are re-fetched.
#
# These BSPs are NOT committed to the repository. They are downloaded
# from public mirrors documented in experiments/locattr/bsps/SOURCES.md.
# The 12 maps below are the most-played QuakeWorld competitive maps;
# any map not in this set falls back to the V1 (Euclidean) attribution
# in mvd-analytics/loc.Finder — locvis treats the missing-BSP case as
# "no veto", so the rest of the pipeline keeps working.
#
# Format of each entry: "<name> <url> <sha256-or-empty>".
# When the sha256 column is empty the script downloads without integrity
# checking and prints a warning; populate it from the curl output to
# pin the file.

set -euo pipefail

BSP_DIR="${1:-bsps}"
mkdir -p "$BSP_DIR"

# Per-map URL + sha256. Multi-line read-friendly format; "" means
# unverified — we still download but warn.
ENTRIES=(
  # name        url                                                                                         sha256
  "aerowalk    https://maps.quakeworld.nu/all/aerowalk.bsp                                                  6c297aaa5ccb8f10f6f7ee4991ba6663f887414e31e6eac8358510e14e4ec98b"
  "dm2         https://raw.githubusercontent.com/marcusgadbem/nquakesv/master/qw/maps/dm2.bsp              "
  "dm3         https://raw.githubusercontent.com/marcusgadbem/nquakesv/master/qw/maps/dm3.bsp               aec9edbb727c0a206edc2c0688775ce8242c0d51e1ee7583c7126c76f7c3b2f1"
  "dm4         https://raw.githubusercontent.com/marcusgadbem/nquakesv/master/qw/maps/dm4.bsp              "
  "dm6         https://raw.githubusercontent.com/marcusgadbem/nquakesv/master/qw/maps/dm6.bsp               5b55566c88561b44534b3bdd4554923ff92e480f89bb4e10f9e45361bc2c5253"
  "e1m2        https://raw.githubusercontent.com/quakeworld/id-maps-gpl/master/e1m2.bsp.gz                  62d270e89514e6e492fac0a8c5dea583ccabd6515a48919d750b08dd94fcb23a"
  "ztndm3      https://maps.quakeworld.nu/all/ztndm3.bsp                                                   "
  "povdmm4     https://maps.quakeworld.nu/all/povdmm4.bsp                                                  "
  "schloss     https://maps.quakeworld.nu/all/schloss.bsp                                                  "
  "obsidian    https://maps.quakeworld.nu/all/obsidian.bsp                                                 "
  "skull       https://maps.quakeworld.nu/all/skull.bsp                                                    "
  "bravado     https://maps.quakeworld.nu/all/bravado.bsp                                                  "
)

# Returns 0 if the file at $1 has SHA-256 == $2.
check_sha() {
  local file="$1" expected="$2"
  [[ -z "$expected" ]] && return 0
  local actual
  actual="$(sha256sum "$file" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]]
}

download_one() {
  local name="$1" url="$2" expected_sha="$3"
  local out="$BSP_DIR/$name.bsp"

  if [[ -f "$out" ]]; then
    if check_sha "$out" "$expected_sha"; then
      printf "  ok    %-12s %s\n" "$name" "(cached)"
      return 0
    else
      printf "  redo  %-12s %s\n" "$name" "(sha mismatch, refetching)"
      rm -f "$out"
    fi
  fi

  local tmp
  tmp="$(mktemp)"
  if ! curl -fsSL --retry 3 --retry-delay 2 -o "$tmp" "$url"; then
    printf "  fail  %-12s %s\n" "$name" "(download failed: $url)"
    rm -f "$tmp"
    return 1
  fi

  # Gzip-stream sources (only e1m2 today). curl writes the raw body; we
  # gunzip in-place if the URL ends in .gz.
  if [[ "$url" == *.gz ]]; then
    if ! gunzip -c "$tmp" > "$out"; then
      printf "  fail  %-12s %s\n" "$name" "(gunzip failed)"
      rm -f "$tmp" "$out"
      return 1
    fi
    rm -f "$tmp"
  else
    mv "$tmp" "$out"
  fi

  if [[ -n "$expected_sha" ]]; then
    if check_sha "$out" "$expected_sha"; then
      printf "  ok    %-12s %s\n" "$name" "(verified)"
    else
      local actual
      actual="$(sha256sum "$out" | awk '{print $1}')"
      printf "  FAIL  %-12s sha mismatch: got %s want %s\n" "$name" "$actual" "$expected_sha"
      rm -f "$out"
      return 1
    fi
  else
    local actual
    actual="$(sha256sum "$out" | awk '{print $1}')"
    printf "  ok    %-12s (downloaded, no sha pin — record %s)\n" "$name" "$actual"
  fi
}

echo "Fetching BSPs into $BSP_DIR/ ..."
fail=0
for entry in "${ENTRIES[@]}"; do
  # shellcheck disable=SC2086
  set -- $entry
  name="$1"; url="$2"; sha="${3:-}"
  if ! download_one "$name" "$url" "$sha"; then
    fail=$((fail + 1))
  fi
done

if (( fail > 0 )); then
  echo "WARNING: $fail BSP(s) failed to download. The locvis filter will fall back"
  echo "to V1 for those maps; the rest of the pipeline is unaffected."
  exit 1
fi
echo "All BSPs ready in $BSP_DIR/"
