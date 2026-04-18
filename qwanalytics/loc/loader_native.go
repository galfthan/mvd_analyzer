//go:build !(js && wasm)

package loc

import (
	"fmt"
	"os"
	"path/filepath"
)

// locDir is the filesystem directory where .loc files live for native
// (non-WASM) callers. cmd/mapgen overrides this via SetLocDir.
var locDir = "internal/web/static/locs"

// SetLocDir overrides the directory LoadForMap reads .loc files from.
// Only meaningful in native builds; WASM builds fetch over HTTP.
func SetLocDir(dir string) {
	locDir = dir
}

// LoadForMap reads internal/web/static/locs/<base>.loc from disk.
func LoadForMap(mapName string) (*Finder, error) {
	base := NormalizeMapName(mapName)
	data, err := os.ReadFile(filepath.Join(locDir, base+".loc"))
	if err != nil {
		return nil, fmt.Errorf("no loc file for map %s: %w", base, err)
	}
	return buildFinder(base, data)
}
