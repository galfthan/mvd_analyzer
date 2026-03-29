package analyzer

import (
	"io"

	"github.com/mvd-analyzer/internal/mvd"
	"github.com/mvd-analyzer/internal/parser"
	"github.com/mvd-analyzer/pkg/mvdfile"
)

// Registry manages registered analyzers
type Registry struct {
	analyzers []Analyzer
}

// NewRegistry creates a new analyzer registry
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds an analyzer to the registry
func (r *Registry) Register(a Analyzer) {
	r.analyzers = append(r.analyzers, a)
}

// Analyze runs all registered analyzers on an MVD file
func (r *Registry) Analyze(filePath string) (*Result, error) {
	f, err := mvdfile.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return r.AnalyzeReader(f, filePath)
}

// AnalyzeReader runs all registered analyzers on an MVD data stream
func (r *Registry) AnalyzeReader(reader io.Reader, filename string) (*Result, error) {
	decoder := mvd.NewDecoder(reader)
	p := parser.NewParser(decoder)

	ctx := &Context{
		FragsBySlot: make(map[int]int),
	}

	for _, a := range r.analyzers {
		if err := a.Init(ctx); err != nil {
			return nil, err
		}
	}

	p.OnEvent(func(event parser.Event) error {
		if e, ok := event.(*parser.ServerDataEvent); ok {
			ctx.ServerData = e.Data
		}
		if e, ok := event.(*parser.UserInfoEvent); ok {
			ctx.Players[e.Player.Slot] = e.Player
		}
		if e, ok := event.(*parser.FragUpdateEvent); ok {
			ctx.FragsBySlot[e.PlayerNum] = e.Frags
		}

		for _, a := range r.analyzers {
			if err := a.OnEvent(event); err != nil {
				return err
			}
		}
		return nil
	})

	if err := p.Parse(); err != nil && err != mvd.ErrEndOfDemo {
		// Log error but continue to get partial results
	}

	result := &Result{
		FilePath: filename,
		Duration: decoder.CurrentTime(),
	}

	for _, a := range r.analyzers {
		output, err := a.Finalize()
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}

		switch a.Name() {
		case "match":
			if m, ok := output.(*MatchResult); ok {
				result.Match = m
				if m.EndTime > 0 {
					result.Duration = m.EndTime
				}
			}
		case "frag":
			if f, ok := output.(*FragResult); ok {
				result.Frags = f
			}
		case "stats":
			if s, ok := output.(*StatsResult); ok {
				result.Stats = s
			}
		case "weaponstats":
			if ws, ok := output.(*WeaponStatsResult); ok {
				result.WeaponStats = ws
			}
		case "demoinfo":
			if di, ok := output.(*DemoInfoResult); ok {
				result.DemoInfo = di
			}
		case "messages":
			if m, ok := output.(*MessagesResult); ok {
				result.Messages = m
			}
		case "timelineAnalysis":
			if ta, ok := output.(*TimelineAnalysisResult); ok {
				result.TimelineAnalysis = ta
			}
		}
	}

	// Normalize all times to be match-relative (0-based from match start)
	// This eliminates the need for the frontend to subtract matchStartTime everywhere
	matchStart := 0.0
	if result.TimelineAnalysis != nil {
		matchStart = result.TimelineAnalysis.MatchStartTime
	}
	if matchStart > 0 {
		result.Duration -= matchStart

		if ta := result.TimelineAnalysis; ta != nil {
			for i := range ta.Buckets {
				ta.Buckets[i].StartTime -= matchStart
				ta.Buckets[i].EndTime -= matchStart
			}
			for i := range ta.HighResBuckets {
				ta.HighResBuckets[i].T -= matchStart
			}
			for i := range ta.FragEvents {
				ta.FragEvents[i].Time -= matchStart
			}
			for i := range ta.PowerupEvents {
				ta.PowerupEvents[i].Time -= matchStart
				ta.PowerupEvents[i].EndTime -= matchStart
			}
			ta.DemoOffset = matchStart
			ta.MatchStartTime = 0

			// Filter out warmup buckets (negative times after normalization)
			filtered := ta.Buckets[:0]
			for _, b := range ta.Buckets {
				if b.EndTime > 0 {
					filtered = append(filtered, b)
				}
			}
			ta.Buckets = filtered

			filteredHR := ta.HighResBuckets[:0]
			for _, b := range ta.HighResBuckets {
				if b.T >= 0 {
					filteredHR = append(filteredHR, b)
				}
			}
			ta.HighResBuckets = filteredHR
		}

		if result.Messages != nil {
			for i := range result.Messages.Events {
				result.Messages.Events[i].Time -= matchStart
			}
		}

		if result.Frags != nil {
			for i := range result.Frags.Frags {
				result.Frags.Frags[i].Time -= matchStart
			}
		}

		if result.Match != nil {
			result.Match.StartTime -= matchStart
			result.Match.EndTime -= matchStart
		}
	}

	return result, nil
}

// NewDefaultRegistry creates a registry with all default analyzers
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	// DemoInfo first so it's available in Context for other analyzers
	r.Register(NewDemoInfoAnalyzer())
	r.Register(NewMatchAnalyzer())
	r.Register(NewFragAnalyzer())
	r.Register(NewMessagesAnalyzer())
	r.Register(NewStatsAnalyzer())
	r.Register(NewWeaponStatsAnalyzer())
	r.Register(NewTimelineAnalyzer())
	return r
}
