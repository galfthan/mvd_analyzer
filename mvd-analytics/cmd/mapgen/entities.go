package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/loc"
	"github.com/mvd-analyzer/mvd-analytics/mapents"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
)

// emitEntities parses a BSP's entity lump, classifies the entities we
// surface, names each by its nearest loc (disambiguated), and writes the
// per-map corpus JSON under outDir.
//
// Point entities (items, spawnpoints, teleport destinations) carry their
// position in the entity origin and are emitted directly. Brush entities
// (buttons, teleport sources, doors) need bmodel bbox resolution from the
// BSP models lump and are skipped here until that lands.
func emitEntities(path, name string, finder *loc.Finder, outDir string, verbose bool) error {
	ents, err := bsp.ReadEntities(path)
	if err != nil {
		return fmt.Errorf("read entities: %w", err)
	}

	out := make([]mapents.MapEntity, 0, len(ents))
	var skippedBrush int
	for _, e := range ents {
		etype, kind, ok := mapents.Classify(e.Classname, e.Spawnflags)
		if !ok {
			continue
		}
		if !mapents.IsPointEntity(etype) {
			skippedBrush++
			continue
		}
		me := mapents.MapEntity{
			Type:       etype,
			Class:      e.Classname,
			Kind:       kind,
			X:          e.Origin[0],
			Y:          e.Origin[1],
			Z:          e.Origin[2],
			Spawnflags: e.Spawnflags,
		}
		if etype == mapents.TypeTeleportDst {
			me.TargetName = e.RawKeys["targetname"]
		}
		if finder != nil {
			me.Loc = finder.FindNearest(me.X, me.Y, me.Z)
		}
		out = append(out, me)
	}

	assignNames(out)
	sortEntities(out)

	doc := mapents.MapEntities{Map: name, Version: mapents.CorpusVersion, Entities: out}
	data, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return fmt.Errorf("marshal entities: %w", err)
	}
	data = append(data, '\n')
	outPath := filepath.Join(outDir, name+".json")
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "  ents %s: entities=%d skipped-brush=%d bytes=%d\n",
			name, len(out), skippedBrush, len(data))
	}
	return nil
}

// assignNames sets a human label on every entity: its nearest loc name
// (or a kind/type fallback when no loc file exists), disambiguated with
// `-1`/`-2` suffixes within each (type, kind, base-name) group so two
// RLs at "low" become "low-1"/"low-2" while an RL and a teleport that
// share a loc both stay "low" (the type/kind fields separate them).
func assignNames(ents []mapents.MapEntity) {
	type key struct{ t, k, b string }
	bases := make([]string, len(ents))
	groups := map[key][]int{}
	for i := range ents {
		bases[i] = baseName(ents[i])
		gk := key{ents[i].Type, ents[i].Kind, bases[i]}
		groups[gk] = append(groups[gk], i)
	}
	for gk, idxs := range groups {
		if len(idxs) == 1 {
			ents[idxs[0]].Name = bases[idxs[0]]
			continue
		}
		sort.Slice(idxs, func(a, b int) bool { return lessPos(ents[idxs[a]], ents[idxs[b]]) })
		for n, i := range idxs {
			ents[i].Name = fmt.Sprintf("%s-%d", gk.b, n+1)
		}
	}
}

func baseName(e mapents.MapEntity) string {
	if e.Loc != "" {
		return e.Loc
	}
	if e.Kind != "" {
		return e.Kind
	}
	switch e.Type {
	case mapents.TypeSpawn:
		return "spawn"
	case mapents.TypeTeleportDst:
		return "tele-dst"
	case mapents.TypeTeleportSrc:
		return "tele-src"
	case mapents.TypeButton:
		return "button"
	case mapents.TypeDoor:
		return "door"
	}
	return e.Type
}

// sortEntities orders the slice deterministically (type, kind, name,
// then position) so regenerated corpora diff cleanly.
func sortEntities(ents []mapents.MapEntity) {
	sort.Slice(ents, func(a, b int) bool {
		if ents[a].Type != ents[b].Type {
			return ents[a].Type < ents[b].Type
		}
		if ents[a].Kind != ents[b].Kind {
			return ents[a].Kind < ents[b].Kind
		}
		if ents[a].Name != ents[b].Name {
			return ents[a].Name < ents[b].Name
		}
		return lessPos(ents[a], ents[b])
	})
}

func lessPos(a, b mapents.MapEntity) bool {
	if a.X != b.X {
		return a.X < b.X
	}
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	return a.Z < b.Z
}
