//go:build !(js && wasm)

package mapents

import (
	"embed"
	"fmt"

	"github.com/mvd-analyzer/mvd-analytics/loc"
)

//go:embed data
var embedded embed.FS

// LoadForMap returns the static entity corpus for a map, or an error if
// no corpus file exists. Map name is normalised with the same rules as
// the loc corpus so aliases resolve consistently.
//
// The corpus is always the embedded one. cmd/mapgen regenerates it by
// WRITING to mapents/data (-entities-out), never by pointing the loader
// at a scratch directory — unlike mapbsp, which does carry a SetDir
// override because BSPs are too large to embed.
func LoadForMap(mapName string) (*MapEntities, error) {
	base := loc.NormalizeMapName(mapName)
	data, err := embedded.ReadFile("data/" + base + ".json")
	if err != nil {
		return nil, fmt.Errorf("no map-entities for map %s: %w", base, err)
	}
	return parse(base, data)
}
