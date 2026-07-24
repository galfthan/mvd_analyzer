// Package config holds the tunable parameters every qwanalytics
// pipeline run reads.
//
// Per-map region overrides live in regions/<map>.json. They are loaded
// lazily by RegionsForMap so adding a new map's overrides is a pure data
// change — drop a JSON file in regions/ and rebuild.
package config

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed regions/*.json
var embeddedRegions embed.FS

// BlipThresholdMs is the minimum duration a player must reside in a loc
// for that residence to count as stable. Any shorter "blip" (wall-bleed
// jitter, nearest-point flicker along a boundary) is re-attributed to an
// adjacent stable loc before downstream consumers read the per-bucket loc
// index. It feeds the loc-track smoothing behind the loc graph, the
// region-control timeline, and the map view's loc labels.
const BlipThresholdMs = 250

// MapRegionOverride describes one named region for a specific map: the
// display name plus the list of loc names (post variable substitution)
// that belong to it. The schema is stable — qw-web's Save/Load buttons
// emit and consume this exact JSON shape.
type MapRegionOverride struct {
	Name string   `json:"name"`
	Locs []string `json:"locs"`
}

// MapRegionOverrides is the on-disk shape of a regions/<map>.json file.
type MapRegionOverrides struct {
	Regions []MapRegionOverride `json:"regions"`
}

// RegionsForMap returns the embedded per-map region overrides for
// `mapName` (a basename — case-insensitive, no path or .bsp suffix).
// Returns nil if no overrides are defined for the map.
func RegionsForMap(mapName string) []MapRegionOverride {
	base := strings.ToLower(strings.TrimSuffix(mapName, ".bsp"))
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if base == "" {
		return nil
	}
	data, err := embeddedRegions.ReadFile("regions/" + base + ".json")
	if err != nil {
		return nil
	}
	var ov MapRegionOverrides
	if err := json.Unmarshal(data, &ov); err != nil {
		panic(fmt.Errorf("qwanalytics/config: regions/%s.json malformed: %w", base, err))
	}
	return ov.Regions
}
