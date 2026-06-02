package analyzer

import (
	"github.com/mvd-analyzer/mvd-analytics/mapents"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// MapEntitiesAnalyzer attaches the map's static designed layout (item
// spawns, player spawnpoints, teleport destinations/sources, buttons)
// from the offline-generated mapents corpus, keyed by map name. It reads
// no events — the data is per-map, not per-demo — and is simply absent
// when no corpus file exists for the map.
type MapEntitiesAnalyzer struct {
	ctx *Context
}

func NewMapEntitiesAnalyzer() *MapEntitiesAnalyzer { return &MapEntitiesAnalyzer{} }

func (a *MapEntitiesAnalyzer) Name() string { return "map_entities" }

func (a *MapEntitiesAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *MapEntitiesAnalyzer) OnEvent(events.Event) error { return nil }

func (a *MapEntitiesAnalyzer) Finalize(result *Result) error {
	if a.ctx == nil || a.ctx.ServerData == nil {
		return nil
	}
	// MapFile ("maps/dm6.bsp") is the authoritative, KTX-independent map
	// identifier from the model list; fall back to the level name.
	mapName := a.ctx.ServerData.MapFile
	if mapName == "" {
		mapName = a.ctx.ServerData.LevelName
	}
	if mapName == "" {
		return nil
	}

	corpus, err := mapents.LoadForMap(mapName)
	if err != nil || corpus == nil || len(corpus.Entities) == 0 {
		return nil // no corpus for this map — section stays absent
	}

	out := &MapEntitiesResult{
		Map:      corpus.Map,
		Entities: make([]MapEntity, 0, len(corpus.Entities)),
	}
	for _, e := range corpus.Entities {
		me := MapEntity{
			Type:       e.Type,
			Class:      e.Class,
			Kind:       e.Kind,
			Name:       e.Name,
			X:          e.X,
			Y:          e.Y,
			Z:          e.Z,
			Loc:        e.Loc,
			Target:     e.Target,
			TargetName: e.TargetName,
			Spawnflags: e.Spawnflags,
		}
		if e.Bounds != nil {
			me.Bounds = &Bounds{Min: e.Bounds.Min, Max: e.Bounds.Max}
		}
		out.Entities = append(out.Entities, me)
	}
	result.MapEntities = out
	return nil
}
