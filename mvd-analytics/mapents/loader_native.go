//go:build !(js && wasm)

package mapents

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mvd-analyzer/mvd-analytics/loc"
)

//go:embed data
var embedded embed.FS

// dirOverride, if non-empty, makes LoadForMap read from this directory
// instead of the embedded corpus. Used by cmd/mapgen to write/read a
// working corpus outside the module tree.
var dirOverride string

// SetDir points LoadForMap at an on-disk directory instead of the
// embedded corpus. Native only; WASM routes through the host
// fetchMapEntsSync regardless. Pass "" to revert to the embedded corpus.
func SetDir(dir string) { dirOverride = dir }

// LoadForMap returns the static entity corpus for a map, or an error if
// no corpus file exists. Map name is normalised with the same rules as
// the loc corpus so aliases resolve consistently.
func LoadForMap(mapName string) (*MapEntities, error) {
	base := loc.NormalizeMapName(mapName)
	var (
		data []byte
		err  error
	)
	if dirOverride != "" {
		data, err = os.ReadFile(filepath.Join(dirOverride, base+".json"))
	} else {
		data, err = embedded.ReadFile("data/" + base + ".json")
	}
	if err != nil {
		return nil, fmt.Errorf("no map-entities for map %s: %w", base, err)
	}
	return parse(base, data)
}
