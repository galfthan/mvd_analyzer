// Package config holds the tunable parameters every qwanalytics
// pipeline run reads. Defaults are embedded from defaults.json so a
// binary built on qwanalytics works with sensible values out of the
// box; callers that want to override a value construct Default() and
// mutate the returned struct before handing it to the registry.
//
// Keep the struct layout mirroring the JSON shape — fields grouped by
// the analyzer or post-processor that owns them — so adding a new
// knob is (1) a field on the right sub-struct, (2) a JSON default,
// and (3) a read site in the code that should consume it.
//
// Per-map region overrides live alongside in regions/<map>.json. They
// are loaded lazily by RegionsForMap so adding a new map's overrides
// is a pure data change — drop a JSON file in regions/ and rebuild.
package config

import (
	"embed"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed defaults.json
var defaultsJSON []byte

//go:embed regions/*.json
var embeddedRegions embed.FS

// Config is the full set of analyzer-tunable parameters.
type Config struct {
	LocGraph LocGraphConfig `json:"locGraph"`
}

// LocGraphConfig controls the loc-track smoothing that feeds the loc
// graph, the region-control timeline, and the map view's loc labels.
type LocGraphConfig struct {
	// BlipThresholdMs is the minimum duration a player must reside in a
	// loc for that residence to count as stable. Any shorter "blip"
	// (wall-bleed jitter, nearest-point flicker along a boundary) is
	// re-attributed to an adjacent stable loc before downstream
	// consumers read the per-bucket loc index. 0 disables the filter.
	BlipThresholdMs int `json:"blipThresholdMs"`
}

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

// Default returns a fresh Config populated from the embedded
// defaults.json. Callers may mutate the returned pointer to override
// individual values for a single pipeline run.
func Default() *Config {
	var c Config
	if err := json.Unmarshal(defaultsJSON, &c); err != nil {
		panic(fmt.Errorf("qwanalytics/config: embedded defaults.json is malformed: %w", err))
	}
	return &c
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
