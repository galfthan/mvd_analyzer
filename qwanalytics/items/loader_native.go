//go:build !(js && wasm)

package items

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed data/*.json
var embeddedItems embed.FS

// dirOverride, when non-empty, makes LoadForMap read from this
// directory instead of the embedded corpus. Used by cmd/mapgen and by
// tests that want to point at a working items directory.
var dirOverride string

// SetDir points LoadForMap at an on-disk directory instead of the
// embedded JSON corpus. Pass "" to revert to the embedded corpus.
func SetDir(dir string) {
	dirOverride = dir
}

// LoadForMap returns the item list for a map, or an error if the map
// is not in the corpus.
func LoadForMap(mapName string) ([]MapItem, error) {
	base := NormalizeMapName(mapName)
	data, err := readItemsJSON(base)
	if err != nil {
		return nil, err
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("items: decode %s: %w", base, err)
	}
	return f.Items, nil
}

func readItemsJSON(base string) ([]byte, error) {
	if dirOverride != "" {
		data, err := os.ReadFile(filepath.Join(dirOverride, base+".json"))
		if err != nil {
			return nil, fmt.Errorf("no items file for map %s: %w", base, err)
		}
		return data, nil
	}
	data, err := embeddedItems.ReadFile("data/" + base + ".json")
	if err != nil {
		return nil, fmt.Errorf("no items file for map %s: %w", base, err)
	}
	return data, nil
}
