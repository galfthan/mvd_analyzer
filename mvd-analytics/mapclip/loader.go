package mapclip

import (
	"fmt"

	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// LoadForMap builds the worldspawn player clip hull for a map from its
// provisioned BSP, returning an error when no BSP is available (so the
// caller leaves the floor column absent) or the BSP is a format we don't
// parse (HL v30 / Quake 2). The BSP bytes come from the shared mapbsp
// loader — the same source locvis uses for the visibility filter — so a
// deployment only ships BSPs once. No separate corpus is generated or
// embedded; a map update is just a new .bsp.
func LoadForMap(mapName string) (*Hull, error) {
	data := mapbsp.LoadBytes(mapName)
	if data == nil {
		return nil, fmt.Errorf("mapclip: no BSP for map %s", mapName)
	}
	parsed, err := bsp.ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("mapclip: parse BSP for map %s: %w", mapName, err)
	}
	return Build(parsed)
}
