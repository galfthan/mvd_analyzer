package analyzer

import (
	"io"

	"github.com/mvd-analyzer/qwanalytics/config"
	resultpkg "github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwdemo/events"
	mvdsource "github.com/mvd-analyzer/qwdemo/source/mvd"
)

// Registry manages registered analyzers. Config carries the tunable
// parameters individual analyzers read; callers may mutate it before
// analyzing to override defaults for a single run.
type Registry struct {
	analyzers      []Analyzer
	postProcessors []ResultPostProcessor
	Config         *config.Config
}

// ResultPostProcessor mutates the assembled Result after every
// analyser has finalised. Examples: time normalisation (rebase to
// match-relative), duel-mode team rewrites, locgraph synthesis from
// timeline buckets. The function receives CoreOutputs so it can read
// demoinfo / name tables / frag log without re-deriving them.
type ResultPostProcessor func(result *Result, co *CoreOutputs)

// NewRegistry creates an empty analyzer registry seeded with the
// embedded default config. No analysers or post-processors are
// registered — callers wire those up explicitly (or use
// NewDefaultRegistry).
func NewRegistry() *Registry {
	return &Registry{Config: config.Default()}
}

// Register adds an analyzer to the registry
func (r *Registry) Register(a Analyzer) {
	r.analyzers = append(r.analyzers, a)
}

// RegisterPostProcessor adds a Result post-processor. They run in
// registration order after every analyser has finalised.
func (r *Registry) RegisterPostProcessor(p ResultPostProcessor) {
	r.postProcessors = append(r.postProcessors, p)
}

// Analyze runs all registered analyzers on an MVD file at the given path.
// Gzip is auto-detected.
func (r *Registry) Analyze(filePath string) (*Result, error) {
	src, err := mvdsource.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return r.analyzeSource(src, filePath, src.CurrentTime)
}

// AnalyzeReader runs all registered analyzers on an MVD byte stream.
// Provided as a convenience for callers that already have bytes in hand
// (notably the WASM entry, which receives a JS Uint8Array).
func (r *Registry) AnalyzeReader(reader io.Reader, filename string) (*Result, error) {
	src, err := mvdsource.NewFromReader(reader)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return r.analyzeSource(src, filename, src.CurrentTime)
}

// AnalyzeSource runs all registered analyzers against an events.Source.
// This is the source-agnostic entry point: any Source implementation
// (MVD file, QTV live, JSON replay) satisfies the interface.
// `filename` is a display label that flows into Result.FilePath.
func (r *Registry) AnalyzeSource(source events.Source, filename string) (*Result, error) {
	// currentTime is filled in by the MVD source wrapper; an abstract
	// source may not expose a decoder clock, so default to the last
	// event timestamp seen.
	return r.analyzeSource(source, filename, nil)
}

func (r *Registry) analyzeSource(source events.Source, filename string, currentTime func() float64) (*Result, error) {
	ctx := &Context{
		FragsBySlot: make(map[int]int),
	}

	for _, a := range r.analyzers {
		if err := a.Init(ctx); err != nil {
			return nil, err
		}
	}

	var lastTime float64
	for {
		event, err := source.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Log and stop; partial results still usable downstream.
			break
		}
		lastTime = event.EventTime()

		if e, ok := event.(*events.ServerDataEvent); ok {
			ctx.ServerData = e.Data
		}
		if e, ok := event.(*events.UserInfoEvent); ok {
			ctx.Players[e.Player.Slot] = e.Player
		}
		if e, ok := event.(*events.FragUpdateEvent); ok {
			ctx.FragsBySlot[e.PlayerNum] = e.Frags
		}

		for _, a := range r.analyzers {
			if err := a.OnEvent(event); err != nil {
				return nil, err
			}
		}
	}

	duration := lastTime
	if currentTime != nil {
		duration = currentTime()
	}

	result := &Result{
		SchemaVersion: resultpkg.CurrentSchemaVersion,
		FilePath:      filename,
		Duration:      duration,
	}

	co := &CoreOutputs{}

	for _, a := range r.analyzers {
		// Hand the running CoreOutputs to any analyser that wants it
		// before its Finalize runs. Analysers registered later in the
		// slice see every field produced by analysers registered earlier.
		if cc, ok := a.(CoreConsumer); ok {
			cc.UseCoreOutputs(co)
		}

		if err := a.Finalize(result); err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}

		// Producers populate CoreOutputs after their own Finalize so
		// downstream analysers see the new fields.
		if cp, ok := a.(CoreProducer); ok {
			cp.PopulateCore(co)
		}
	}

	// Run registered post-processors in order. Each one operates on the
	// fully-finalized Result; CoreOutputs is passed through for read
	// access. The default ordering (set in NewDefaultRegistry) is:
	//   1. normalizeMatchRelativeTimes
	//   2. normalizeDuelTeams
	//   3. buildLocGraphPost
	// — but the slice is otherwise unconstrained. Add a step by
	// calling r.RegisterPostProcessor(...) before Analyze.
	for _, p := range r.postProcessors {
		p(result, co)
	}

	return result, nil
}

// NewDefaultRegistry creates a registry with all default analyzers,
// configured from the embedded defaults in qwanalytics/config. Callers
// that want to override config values should construct this registry
// and mutate r.Config fields before calling Analyze — analyzers pick
// up their configured values from the registry at construction time,
// so further mutations are applied here via targeted setters.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	// DemoInfo first so it's available in Context for other analyzers.
	r.Register(NewDemoInfoAnalyzer())
	r.Register(NewMetadataAnalyzer())
	r.Register(NewMatchAnalyzer())
	r.Register(NewFragAnalyzer())
	r.Register(NewMessagesAnalyzer())
	ta := NewTimelineAnalyzer()
	ta.SetBlipThresholdMs(r.Config.LocGraph.BlipThresholdMs)
	r.Register(ta)
	r.Register(NewItemAnalyzer())
	r.Register(NewBackpackAnalyzer())
	r.Register(NewWeaponPickupsAnalyzer())

	// Post-processors run in registration order on the assembled
	// Result. Order matters: time normalisation has to land first so
	// duel team rewrite and loc graph construction see match-relative
	// timestamps; locgraph runs last because it consumes the
	// already-rewritten team labels and timeline buckets.
	r.RegisterPostProcessor(normalizeMatchRelativeTimes)
	r.RegisterPostProcessor(duelTeamNormalize)
	r.RegisterPostProcessor(locGraphPost)
	return r
}
