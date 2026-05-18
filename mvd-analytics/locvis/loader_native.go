//go:build !(js && wasm)

package locvis

import (
	"os"
	"path/filepath"

	"github.com/mvd-analyzer/mvd-analytics/loc"
)

// bspDirOverride, if non-empty, takes precedence over MVDA_BSP_DIR.
// Tests set this directly via SetBspDir.
var bspDirOverride string

// SetBspDir points LoadForMap at an on-disk directory of BSP files.
// Pass "" to revert to the env-var lookup (MVDA_BSP_DIR, then ./bsps).
// Native-only; WASM callers route through the host fetchBspSync.
func SetBspDir(dir string) {
	bspDirOverride = dir
}

// LoadForMap returns a Finder for the given map. The loc corpus is
// always required (forwards to loc.LoadForMap). The BSP is best-effort:
// if not present, malformed, or the BSP dir is unset, the Finder is
// returned with no BSP and FindNearest degenerates to V1.
//
// Native BSP lookup order:
//  1. The directory set by SetBspDir, if non-empty.
//  2. $MVDA_BSP_DIR.
//  3. ./bsps (relative to the process working directory).
func LoadForMap(mapName string) (*Finder, error) {
	base, err := loc.LoadForMap(mapName)
	if err != nil {
		return nil, err
	}
	bspBytes := readBspBytes(loc.NormalizeMapName(mapName))
	return newFinder(base, bspBytes), nil
}

func readBspBytes(normalisedMap string) []byte {
	for _, dir := range candidateBspDirs() {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, normalisedMap+".bsp")
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
	}
	return nil
}

func candidateBspDirs() []string {
	return []string{bspDirOverride, os.Getenv("MVDA_BSP_DIR"), "bsps"}
}
