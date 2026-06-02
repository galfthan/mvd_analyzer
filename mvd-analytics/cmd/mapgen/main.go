// mapgen is a developer tool that parses Quake 1 BSP files and writes
// two kinds of per-map JSON:
//
//   - -out-dir: per-loc walkable-floor polygon geometry (the viewer
//     renders real floor geometry instead of the loc convex-hull blob).
//   - -entities-out: the static map-entity corpus (mapents) — item
//     spawns, player spawnpoints, teleport destinations/sources, buttons
//     — classified from the BSP entity lump and named by nearest loc.
//
// Either output is optional; set the flags for what you want. Missing
// files degrade silently downstream.
//
// Usage (developer machine — point at your own local Quake install):
//
//	go build ./cmd/mapgen
//	./mapgen -bsp-dir ~/quake/id1/maps -verbose                 # geometry (default out-dir)
//	./mapgen -bsp-dir ~/quake/id1/maps -out-dir "" \
//	    -entities-out mvd-analytics/mapents/data -verbose        # entity corpus only
//	./mapgen -bsp-dir /path/to/maps -map dm2 -verbose
//
// This tool is NOT part of CI and is not run during normal builds; only
// the generated JSON files are committed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
	"github.com/mvd-analyzer/mvd-analytics/loc"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/mapgeom"
)

func main() {
	bspDir := flag.String("bsp-dir", "", "directory containing .bsp files (required)")
	outDir := flag.String("out-dir", "internal/web/static/maps", "output directory for geometry JSON; empty to skip geometry")
	entitiesOut := flag.String("entities-out", "", "output directory for per-map entity JSON (mapents corpus); empty to skip entities")
	locDir := flag.String("loc-dir", "internal/web/static/locs", "directory containing .loc files")
	mapFilter := flag.String("map", "", "process only the BSP whose basename (no extension) matches")
	verbose := flag.Bool("verbose", false, "print per-map progress and stats")
	flag.Parse()

	if *outDir == "" && *entitiesOut == "" {
		fmt.Fprintln(os.Stderr, "mapgen: nothing to do — set -out-dir and/or -entities-out")
		flag.Usage()
		os.Exit(2)
	}

	loc.SetLocDir(*locDir)

	if *bspDir == "" {
		fmt.Fprintln(os.Stderr, "mapgen: -bsp-dir is required")
		flag.Usage()
		os.Exit(2)
	}

	info, err := os.Stat(*bspDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mapgen: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "mapgen: %s is not a directory\n", *bspDir)
		os.Exit(1)
	}

	for _, dir := range []string{*outDir, *entitiesOut} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mapgen: create output dir %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	bspPaths, err := findBSPs(*bspDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mapgen: walk bsp-dir: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(bspPaths)

	if *verbose {
		fmt.Fprintf(os.Stderr, "mapgen: found %d BSP files under %s\n", len(bspPaths), *bspDir)
	}

	var processed, failed int
	for _, path := range bspPaths {
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if *mapFilter != "" && name != strings.ToLower(*mapFilter) {
			continue
		}

		if err := processOne(path, name, *outDir, *entitiesOut, *verbose); err != nil {
			fmt.Fprintf(os.Stderr, "  fail %s: %v\n", name, err)
			failed++
			continue
		}
		processed++
	}

	fmt.Fprintf(os.Stderr, "mapgen: processed=%d failed=%d\n", processed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func findBSPs(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".bsp") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func processOne(path, name, outDir, entitiesOut string, verbose bool) error {
	// Loc file is optional: without it, geometry routes every floor face
	// into the unnamed backdrop bucket and entities fall back to
	// kind/type names instead of loc names.
	finder, locErr := loc.LoadForMap(name)
	if locErr != nil {
		finder = nil
		if verbose {
			fmt.Fprintf(os.Stderr, "  note %s: no loc file, emitting unnamed data only\n", name)
		}
	}

	if outDir != "" {
		if err := emitGeometry(path, name, finder, outDir, verbose); err != nil {
			return err
		}
	}
	if entitiesOut != "" {
		if err := emitEntities(path, name, finder, entitiesOut, verbose); err != nil {
			return err
		}
	}
	return nil
}

func emitGeometry(path, name string, finder *loc.Finder, outDir string, verbose bool) error {
	parsed, err := bsp.Parse(path)
	if err != nil {
		return fmt.Errorf("parse bsp: %w", err)
	}

	regions, stats := mapgeom.Build(name, parsed, finder)
	if len(regions.Locs) == 0 {
		return fmt.Errorf("no floor geometry extracted (faces total=%d kept=%d dropped=%d)",
			stats.FacesTotal, stats.FacesKept, stats.FacesDropped)
	}

	outPath := filepath.Join(outDir, name+".json")
	data, err := json.Marshal(regions)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "  ok   %s: locs=%d tris=%d faces=%d/%d unnamed=%d ceiling=%d dropped=%d bytes=%d\n",
			name, stats.Locs, stats.Triangles, stats.FacesKept, stats.FacesTotal,
			stats.FacesUnnamed, stats.FacesCeiling, stats.FacesDropped, len(data))
	}
	return nil
}

