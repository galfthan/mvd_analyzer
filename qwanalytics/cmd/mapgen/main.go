// mapgen is a developer tool that parses Quake 1 BSP files and writes
// per-loc walkable-floor polygon JSON under internal/web/static/maps/.
//
// The viewer fetches these JSONs at map-init time and, if present,
// renders real floor geometry instead of the loc-based convex-hull blob.
// Missing files fall back silently to the existing rendering.
//
// Usage (developer machine — point at your own local Quake install):
//
//	go build ./cmd/mapgen
//	./mapgen -bsp-dir ~/quake/id1/maps -verbose
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

	"github.com/mvd-analyzer/qwanalytics/mapgen/bsp"
	"github.com/mvd-analyzer/qwanalytics/loc"
	"github.com/mvd-analyzer/qwanalytics/mapgen/mapgeom"
)

var itemsOutDir string

func main() {
	bspDir := flag.String("bsp-dir", "", "directory containing .bsp files (required)")
	outDir := flag.String("out-dir", "internal/web/static/maps", "output directory for geometry JSON")
	itemsDir := flag.String("items-out-dir", "qwanalytics/items/data", "output directory for per-map items JSON (analyzer corpus)")
	locDir := flag.String("loc-dir", "internal/web/static/locs", "directory containing .loc files")
	mapFilter := flag.String("map", "", "process only the BSP whose basename (no extension) matches")
	verbose := flag.Bool("verbose", false, "print per-map progress and stats")
	flag.Parse()
	itemsOutDir = *itemsDir

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

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mapgen: create out-dir: %v\n", err)
		os.Exit(1)
	}
	if itemsOutDir != "" {
		if err := os.MkdirAll(itemsOutDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mapgen: create items-out-dir: %v\n", err)
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

		if err := processOne(path, name, *outDir, *verbose); err != nil {
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

func processOne(path, name, outDir string, verbose bool) error {
	// Loc file is optional: without it, Build() routes every floor
	// face into the unnamed backdrop bucket and the viewer renders it
	// as a neutral underlay beneath any loc-based region highlighting.
	finder, locErr := loc.LoadForMap(name)
	if locErr != nil {
		finder = nil
		if verbose {
			fmt.Fprintf(os.Stderr, "  note %s: no loc file, emitting unnamed geometry only\n", name)
		}
	}

	parsed, err := bsp.Parse(path)
	if err != nil {
		return fmt.Errorf("parse bsp: %w", err)
	}

	regions, stats := mapgeom.Build(name, parsed, finder)
	if len(regions.Locs) == 0 {
		return fmt.Errorf("no floor geometry extracted (faces total=%d kept=%d dropped=%d)",
			stats.FacesTotal, stats.FacesKept, stats.FacesDropped)
	}

	// Decode the entities lump separately (the geometry path in
	// mapgeom.Build deliberately doesn't touch it) and emit the
	// pickup-item subset to a slim per-map items JSON. Kept as a
	// separate file so the analyzer's embedded corpus doesn't have
	// to pull in the per-map geometry bytes it never needs.
	var items []mapgeom.MapItem
	if ents, entErr := bsp.ReadEntities(path); entErr == nil {
		items = extractMapItems(ents)
	} else if verbose {
		fmt.Fprintf(os.Stderr, "  note %s: entities lump: %v\n", name, entErr)
	}

	outPath := filepath.Join(outDir, name+".json")
	data, err := json.Marshal(regions)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	if err := writeItemsFile(name, items, itemsOutDir); err != nil {
		return fmt.Errorf("write items: %w", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "  ok   %s: locs=%d tris=%d items=%d faces=%d/%d unnamed=%d ceiling=%d dropped=%d bytes=%d\n",
			name, stats.Locs, stats.Triangles, len(items), stats.FacesKept, stats.FacesTotal,
			stats.FacesUnnamed, stats.FacesCeiling, stats.FacesDropped, len(data))
	}
	return nil
}

// writeItemsFile writes the per-map items JSON into the items corpus
// directory. Empty item lists still produce a file so the corpus is
// self-documenting about what maps have been processed.
func writeItemsFile(name string, items []mapgeom.MapItem, itemsDir string) error {
	if itemsDir == "" {
		return nil
	}
	if items == nil {
		items = []mapgeom.MapItem{}
	}
	f := mapgeom.MapItemsFile{
		Map:     name,
		Version: 1,
		Items:   items,
	}
	data, err := json.Marshal(&f)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(itemsDir, name+".json"), data, 0o644)
}

// extractMapItems filters a BSP entity list down to recognised pickup
// items and normalises them to the compact Kind strings downstream
// analytics expect.
func extractMapItems(ents []bsp.Entity) []mapgeom.MapItem {
	out := make([]mapgeom.MapItem, 0, len(ents))
	for _, e := range ents {
		kind := mapgeom.EntityToItemKind(e.Classname, e.Spawnflags)
		if kind == "" {
			continue
		}
		out = append(out, mapgeom.MapItem{
			Kind: kind,
			X:    e.Origin[0],
			Y:    e.Origin[1],
			Z:    e.Origin[2],
		})
	}
	return out
}
