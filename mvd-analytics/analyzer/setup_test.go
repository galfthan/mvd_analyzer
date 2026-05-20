package analyzer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain points the locvis BSP loader at the repo-root bsps/
// directory (populated by `make bsps`) when no explicit override is in
// the environment. Without this, locvis.LoadForMap would look for
// ./bsps relative to the analyzer package directory — which never
// holds BSPs — and the golden pipeline would silently fall back to V1
// for every map, producing inconsistent goldens depending on where the
// developer happened to run `go test` from.
//
// Maps without a corresponding bsps/<map>.bsp on disk still fall back
// to V1 cleanly; only the maps present in the BSP corpus get the
// visibility filter applied.
func TestMain(m *testing.M) {
	if os.Getenv("MVDA_BSP_DIR") == "" {
		if _, thisFile, _, ok := runtime.Caller(0); ok {
			// thisFile = .../mvd-analytics/analyzer/setup_test.go
			// bsps      = .../bsps  (repo root)
			os.Setenv("MVDA_BSP_DIR", filepath.Join(filepath.Dir(thisFile), "..", "..", "bsps"))
		}
	}
	os.Exit(m.Run())
}
