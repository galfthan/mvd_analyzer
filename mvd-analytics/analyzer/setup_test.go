package analyzer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot is the workspace root, resolved from this file's own location so
// it is independent of the directory `go test` was invoked from.
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// thisFile = <root>/mvd-analytics/analyzer/setup_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// bspDirForTests resolves the BSP directory this package's tests must read,
// given the repo root and whatever the environment asked for:
//
//	""          → <root>/bsps        (the `make bsps` map set)
//	relative    → <root>/<relative>  (NOT relative to the package dir)
//	absolute    → verbatim           (a deliberate choice of map set)
//
// The relative case is the one with teeth. mapbsp resolves a relative dir
// against the process working directory, which under `go test` is the PACKAGE
// directory — so the repo's own documented invocation (`MVDA_BSP_DIR=./bsps`,
// damagerecon/ACCURACY.md) pointed the loader at mvd-analytics/analyzer/bsps,
// nothing resolved, and TestGoldenCorpus skipped every comparison while the
// suite reported success.
func bspDirForTests(root, env string) string {
	if root == "" {
		return env
	}
	if env == "" {
		return filepath.Join(root, "bsps")
	}
	if filepath.IsAbs(env) {
		return env
	}
	return filepath.Join(root, env)
}

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
	env := os.Getenv("MVDA_BSP_DIR")
	bspDirExplicit = env != ""
	if dir := bspDirForTests(repoRoot(), env); dir != "" {
		os.Setenv("MVDA_BSP_DIR", dir)
	}
	os.Exit(m.Run())
}

// bspDirExplicit records whether the caller named a BSP directory. An
// unpopulated repo-root bsps/ is a fresh-clone state the golden test may skip
// over (README §regenerating goldens); a dir the caller ASKED for that
// resolves nothing is a misconfiguration, and TestGoldenCorpus fails on it
// rather than reporting green over zero comparisons.
var bspDirExplicit bool

// TestBspDirForTests pins the resolution above, and the invariant it exists
// to hold: whatever this package's tests end up reading BSPs from is an
// ABSOLUTE path, so it cannot silently mean "a directory under the package"
// and turn the golden comparison into a suite-wide skip.
func TestBspDirForTests(t *testing.T) {
	const root = "/repo"
	for _, tc := range []struct{ env, want string }{
		{"", "/repo/bsps"},
		{"./bsps", "/repo/bsps"},
		{"bsps", "/repo/bsps"},
		{"testdata/maps", "/repo/testdata/maps"},
		{"/opt/mvd/bsps", "/opt/mvd/bsps"},
	} {
		if got := bspDirForTests(root, tc.env); got != tc.want {
			t.Errorf("bspDirForTests(%q, %q) = %q, want %q", root, tc.env, got, tc.want)
		}
	}
	if dir := os.Getenv("MVDA_BSP_DIR"); dir != "" && !filepath.IsAbs(dir) {
		t.Errorf("MVDA_BSP_DIR = %q after TestMain — a relative dir resolves against the package directory, which holds no BSPs", dir)
	}
}
